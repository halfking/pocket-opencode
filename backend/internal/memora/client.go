// Package memora provides a Go client for the memora 记忆库服务
// （ai-platform/memora，module github.com/kx/kxmemory-go）。
//
// 与 internal/kxmemory（AI 编排 FastAPI：笔记/邮件分类、总结）是**两个服务**：
// memora 负责记忆条目的存储与语义检索（/api/add、/api/search/hybrid、
// /api/stats 等，契约见 memora 仓 contracts/routes.json）。pocketd 侧
// 「笔记与知识库打通 memora」即通过本客户端：笔记推送到记忆库、
// 会话/笔记语境的混合检索。
//
// 鉴权（memora internal/auth）：
//   - HS256 JWT，claims 至少含 sub / user_id / tenant_id / exp
//     （见 memora internal/auth/project_acl_sharing_integration_test.go 的签发口径），
//     密钥 = memora 的 JWT_SECRET（这里经 POCKET_MEMORA_JWT_SECRET 注入）。
//   - 或静态 Bearer（POCKET_MEMORA_API_KEY），适配网关/apikey 部署形态。
//
// 未配置 POCKET_MEMORA_BASE_URL 时 NewClient 返回 nil，server 侧路由如实报
// 503「memora 未配置」，行为与 notesStore == nil 的既有降级一致。
package memora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/halfking/pocket-opencode/backend/internal/outbound"
)

// Result 是 /api/search/hybrid 单条命中（memora HybridSearch 的 scored 行）。
type Result struct {
	ID         string  `json:"id"`
	Memory     string  `json:"memory"`
	Score      float64 `json:"score"`
	BM25Score  float64 `json:"bm25_score"`
	RRFScore   float64 `json:"rrf_score"`
	FinalScore float64 `json:"final_score"`
}

// SearchResponse 是混合检索响应。
type SearchResponse struct {
	OK       bool     `json:"ok"`
	Results  []Result `json:"results"`
	Strategy string   `json:"strategy"`
	Total    int      `json:"total"`
}

// AddRequest 是记忆写入载荷（memora addBody 的子集：写笔记够用）。
type AddRequest struct {
	Content  string         `json:"content"`
	UserID   string         `json:"user_id,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AddResponse 是记忆写入响应（memora Add：id + 归一化内容等，这里只取 id）。
type AddResponse struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// StatsResponse 是 GET /api/stats 的透传（结构由 memora 演进，这里保持宽松）。
type StatsResponse map[string]any

// Client 是 memora 客户端抽象，便于 httptest 注入。
type Client interface {
	Health(ctx context.Context) error
	Stats(ctx context.Context) (StatsResponse, error)
	SearchHybrid(ctx context.Context, query string, topK int, tags []string) (*SearchResponse, error)
	Add(ctx context.Context, req AddRequest) (*AddResponse, error)
}

var _ Client = (*HTTPClient)(nil)

// HTTPClient 是 memora 的 HTTP 实现，无状态、并发安全（token 缓存有锁）。
type HTTPClient struct {
	BaseURL    string // 例 http://127.0.0.1:8091（不带 /api 后缀）
	APIKey     string // 静态 Bearer；设置后优先于 JWT
	JWTSecret  string // memora JWT_SECRET；与 APIKey 二选一
	TenantID   string // 默认 default
	ProjectID  string // 默认 default
	UserID     string // 以 pocketd 服务身份写入时的 user_id，默认 pocketd
	HTTPClient *http.Client

	signMu    sync.Mutex
	cachedTok string
	cachedExp time.Time
}

// NewClient 构造 memora 客户端。baseURL 为空时返回 (nil, nil)——调用方按
// 「未配置」降级，而不是拿一个必然失败的客户端制造噪音。
func NewClient(baseURL, apiKey, jwtSecret, tenantID, projectID, userID string, timeout time.Duration) *HTTPClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	if tenantID == "" {
		tenantID = "default"
	}
	if projectID == "" {
		projectID = "default"
	}
	if userID == "" {
		userID = "pocketd"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPClient{
		BaseURL:   baseURL,
		APIKey:    strings.TrimSpace(apiKey),
		JWTSecret: strings.TrimSpace(jwtSecret),
		TenantID:  tenantID,
		ProjectID: projectID,
		UserID:    userID,
		// §950：出站客户端只能由 internal/outbound 构造 —— 它保证
		// 「timeout <= 0 回落默认值」+「Timeout 恒为正」⇒ 请求恒有上界。
		// 这里自己写 `&http.Client{Timeout: timeout}` 会绕过那个兜底
		// （且被 §950 卡口的「不许有裸字面量」判据当场抓住）。
		HTTPClient: outbound.NewClient(timeout),
	}
}

// getOrSignToken 返回有效 JWT；缓存到过期前 30s（同 kxmemory 客户端口径）。
// 静态 APIKey 存在时不签 JWT（Authorization 直接用 Bearer APIKey）。
func (c *HTTPClient) getOrSignToken() (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.JWTSecret == "" {
		// 无鉴权部署形态（memora local-auth 关闭鉴权）：不加 Authorization 头。
		return "", nil
	}
	c.signMu.Lock()
	defer c.signMu.Unlock()
	now := time.Now()
	if c.cachedTok != "" && c.cachedExp.Sub(now) > 30*time.Second {
		return c.cachedTok, nil
	}
	exp := now.Add(10 * time.Minute)
	claims := jwt.MapClaims{
		"sub":        "pocketd",
		"user_id":    c.UserID,
		"tenant_id":  c.TenantID,
		"project_id": c.ProjectID,
		"iat":        now.Unix(),
		"exp":        exp.Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(c.JWTSecret))
	if err != nil {
		return "", err
	}
	c.cachedTok = signed
	c.cachedExp = exp
	return signed, nil
}

// do 发起一次 JSON 请求并解码响应。非 2xx 一律 error（信息含状态码与响应片段）。
func (c *HTTPClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("memora: encode body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("memora: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// project 归属：memora 侧 ProjectAuthz 中间件按 claim / 头解析；
	// 头是显式通道，claim 由上面 JWT 带上，两者一致。
	req.Header.Set("X-Tenant-Id", c.TenantID)
	req.Header.Set("X-Project-Id", c.ProjectID)
	if tok, terr := c.getOrSignToken(); terr != nil {
		return fmt.Errorf("memora: sign token: %w", terr)
	} else if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("memora: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("memora: %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return fmt.Errorf("memora: %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

// Health 探测 memora 可达性（GET /api/health）。
func (c *HTTPClient) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/health", nil, nil)
}

// Stats 读取记忆库统计（GET /api/stats）。
func (c *HTTPClient) Stats(ctx context.Context) (StatsResponse, error) {
	var out StatsResponse
	if err := c.do(ctx, http.MethodGet, "/api/stats", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchHybrid 混合检索（POST /api/search/hybrid，BM25+向量 RRF/MMR 融合）。
// topK <= 0 时由 memora 侧默认（10）。
func (c *HTTPClient) SearchHybrid(ctx context.Context, query string, topK int, tags []string) (*SearchResponse, error) {
	body := map[string]any{"query": query}
	if topK > 0 {
		body["top_k"] = topK
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	var out SearchResponse
	if err := c.do(ctx, http.MethodPost, "/api/search/hybrid", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Add 写入一条记忆（POST /api/add）。content 必填由 memora 校验。
func (c *HTTPClient) Add(ctx context.Context, req AddRequest) (*AddResponse, error) {
	if req.UserID == "" {
		req.UserID = c.UserID
	}
	var out AddResponse
	if err := c.do(ctx, http.MethodPost, "/api/add", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
