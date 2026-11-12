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
// 两张鉴权面（2026-11-13 notebook 集成）：
//   - 既有 /api/* 通道（SearchHybrid/Add/Stats/Health）沿用上面的旧 claims；
//   - /api/v2/notebook/* 走 memora 的 V2JWT fail-closed（internal/middleware/
//     v2jwt.go）：必需 claims iss / aud / sub / tenant_id / scope / exp，
//     aud 必须精确匹配 MEMORA_V2_JWT_AUDIENCE（默认 memora-api）。两个
//     token 各自缓存，v2 claims 的增补**不动**旧通道的 token 形状。
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
	"net/url"
	"strconv"
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

// ─── notebook 面（/api/v2/notebook/*，契约源 memora internal/notebook/handler.go）───
//
// json tag 全部蛇形，与 memora 的 NoteListItem / noteViewBody / noteSaveRequest
// 逐字段对齐；本侧只取薄集成用得到的字段，多余字段忽略。

// NotebookNote 是 memora notebook 的单条笔记。
//
//   - 列表行（GET /notes）只有到 CurrentVersion 为止的头部字段；
//   - 详情（GET /notes/:id）另有 HTML / PlainText / ChangeReason / Etag。
type NotebookNote struct {
	ID             string   `json:"id"`
	KBID           string   `json:"kb_id"`
	Title          string   `json:"title"`
	Lifecycle      string   `json:"lifecycle"`
	CurrentVersion int      `json:"current_version"`
	Revision       int      `json:"revision"`
	HTML           string   `json:"html"`
	PlainText      string   `json:"plain_text"`
	Tags           []string `json:"tags"`
	ChangeReason   string   `json:"change_reason"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	Etag           string   `json:"etag"`
	Removed        bool     `json:"removed"`
}

// NotebookListResponse 是 GET /notes 的 {items,total}。
type NotebookListResponse struct {
	Items []NotebookNote `json:"items"`
	Total int            `json:"total"`
}

// NotebookQuery 是 GET /notes 的查询参数（memora ListNotesQuery 的透传）。
// Limit 由 memora 侧校验（≤200），这里不做二次裁剪，只防 0/负值。
type NotebookQuery struct {
	KBID      string
	Q         string
	Tag       string
	Lifecycle string
	Page      int
	Limit     int
}

// PushNoteRequest 是 POST /notes 的请求体（memora noteSaveRequest 子集；
// attachments 本轮不用）。HTML 必须落在 memora 的标签白名单内
// （p/strong/em/ul/ol/li/h1-h6/a），调用方（server_memora.go）负责从纯文本
// 生成受限 HTML；SourceUpdatedAt 为 RFC3339，空则由 memora 取当前时间。
type PushNoteRequest struct {
	KBID            string   `json:"kb_id,omitempty"`
	Title           string   `json:"title"`
	HTML            string   `json:"html"`
	Tags            []string `json:"tags,omitempty"`
	ChangeReason    string   `json:"change_reason,omitempty"`
	SourceUpdatedAt string   `json:"source_updated_at,omitempty"`
}

// PushNoteResponse 是创建（201）/更新（200）响应的薄投影。
type PushNoteResponse struct {
	ID       string `json:"id"`
	KBID     string `json:"kb_id"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
	Etag     string `json:"etag"`
	Removed  bool   `json:"removed"`
}

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

	// V2 面（/api/v2/notebook/*）的 JWT 可调项：对齐 memora
	// MEMORA_V2_JWT_ISSUER / MEMORA_V2_JWT_AUDIENCE / scope 的校验口径。
	// 零值由 NewClient 回落默认值（issuer=pocketd / aud=memora-api /
	// scope=notebook.read notebook.write），日常部署无需配置。
	V2Issuer   string
	V2Audience string
	V2Scope    string

	signMu    sync.Mutex
	cachedTok string
	cachedExp time.Time

	v2SignMu    sync.Mutex
	v2CachedTok string
	v2CachedExp time.Time
}

// v2 三个 claims 的默认值。audience 与 memora config.MEMORA_V2_JWT_AUDIENCE
// 的 envDefault 一致；scope 命名对齐 memora 侧 "memory.read memory.write"
// 的惯例（V2 校验只要求非空，这里给 notebook 面最小语义）。
const (
	defaultV2Issuer   = "pocketd"
	defaultV2Audience = "memora-api"
	defaultV2Scope    = "notebook.read notebook.write"
)

// NewClient 构造 memora 客户端。baseURL 为空时返回 (nil, nil)——调用方按
// 「未配置」降级，而不是拿一个必然失败的客户端制造噪音。
//
// v2Issuer/v2Audience/v2Scope 为 0 时回落 defaultV2* 默认值（见上）。
func NewClient(baseURL, apiKey, jwtSecret, tenantID, projectID, userID string, timeout time.Duration) *HTTPClient {
	return NewClientV2(baseURL, apiKey, jwtSecret, tenantID, projectID, userID, "", "", "", timeout)
}

// NewClientV2 同 NewClient，多收 V2 面三个 JWT 可调项（空串回落默认）。
// 单独成构造器是为了不动既有调用点的形状。
func NewClientV2(baseURL, apiKey, jwtSecret, tenantID, projectID, userID, v2Issuer, v2Audience, v2Scope string, timeout time.Duration) *HTTPClient {
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
	if v2Issuer == "" {
		v2Issuer = defaultV2Issuer
	}
	if v2Audience == "" {
		v2Audience = defaultV2Audience
	}
	if v2Scope == "" {
		v2Scope = defaultV2Scope
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPClient{
		BaseURL:    baseURL,
		APIKey:     strings.TrimSpace(apiKey),
		JWTSecret:  strings.TrimSpace(jwtSecret),
		TenantID:   tenantID,
		ProjectID:  projectID,
		UserID:     userID,
		V2Issuer:   v2Issuer,
		V2Audience: v2Audience,
		V2Scope:    v2Scope,
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

// getOrSignV2Token 与 getOrSignToken 同纪律（APIKey 优先 / 无鉴权部署不加
// 头 / 缓存到过期前 30s），但 claims 按 memora V2JWTAuth（internal/middleware/
// v2jwt.go）的 fail-closed 清单补齐：iss / aud / sub / tenant_id / scope / exp
// 缺一即 401。sub 取 UserID（V2 面的 principal = sub，memora 侧 ProjectAuthz
// 按 v2_actor_id 判定归属，与项目 owner 对齐）。**旧通道的 token 不带这些
// claims，两边互不影响。**
func (c *HTTPClient) getOrSignV2Token() (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.JWTSecret == "" {
		// 无鉴权部署形态（memora local-auth 关闭鉴权）：不加 Authorization 头。
		return "", nil
	}
	c.v2SignMu.Lock()
	defer c.v2SignMu.Unlock()
	now := time.Now()
	if c.v2CachedTok != "" && c.v2CachedExp.Sub(now) > 30*time.Second {
		return c.v2CachedTok, nil
	}
	exp := now.Add(10 * time.Minute)
	claims := jwt.MapClaims{
		"iss":       c.V2Issuer,
		"sub":       c.UserID,
		"aud":       c.V2Audience,
		"user_id":   c.UserID,
		"tenant_id": c.TenantID,
		"scope":     c.V2Scope,
		"iat":       now.Unix(),
		"exp":       exp.Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(c.JWTSecret))
	if err != nil {
		return "", err
	}
	c.v2CachedTok = signed
	c.v2CachedExp = exp
	return signed, nil
}

// doV2 是 do 的 /api/v2 通道版本：同一套头纪律（X-Tenant-Id / X-Project-Id，
// memora ProjectScope 强制 X-Project-ID），仅 token 走 V2 claims。extraHdrs
// 附加业务头（如 If-Match），nil 即无。非 2xx 一律 error（信息含状态码与响应
// 片段，412 version_conflict 也不例外——调用方按字符串里的状态码分支）。
func (c *HTTPClient) doV2(ctx context.Context, method, path string, body any, out any, extraHdrs map[string]string) error {
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
	for k, v := range extraHdrs {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Tenant-Id", c.TenantID)
	req.Header.Set("X-Project-Id", c.ProjectID)
	if tok, terr := c.getOrSignV2Token(); terr != nil {
		return fmt.Errorf("memora: sign v2 token: %w", terr)
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

// ─── notebook 面（/api/v2/notebook/*）────────────────────────────────────

// notebookBase 是 memora notebook 路由前缀（RegisterRoutes 的 group）。
const notebookBase = "/api/v2/notebook"

// SearchNotes 列出/检索 notebook 笔记（GET /notes）。q 走 memora 的标题与
// 标签过滤语义（非全文检索），tag 为精确匹配——openpocket 的「云端记忆库
// 搜索」用它，幂等去重也靠 tag=sync:openpocket:note:<id> 定位旧推送。
func (c *HTTPClient) SearchNotes(ctx context.Context, q NotebookQuery) (*NotebookListResponse, error) {
	params := url.Values{}
	if v := strings.TrimSpace(q.KBID); v != "" {
		params.Set("kb_id", v)
	}
	if v := strings.TrimSpace(q.Q); v != "" {
		params.Set("q", v)
	}
	if v := strings.TrimSpace(q.Tag); v != "" {
		params.Set("tag", v)
	}
	if v := strings.TrimSpace(q.Lifecycle); v != "" {
		params.Set("lifecycle", v)
	}
	if q.Page > 0 {
		params.Set("page", strconv.Itoa(q.Page))
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	path := notebookBase + "/notes"
	if enc := params.Encode(); enc != "" {
		path += "?" + enc
	}
	var out NotebookListResponse
	if err := c.doV2(ctx, http.MethodGet, path, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetNote 读单条笔记（GET /notes/:id，head 版本；历史 revision 本轮不用）。
func (c *HTTPClient) GetNote(ctx context.Context, id string) (*NotebookNote, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("memora: note id is required")
	}
	var out NotebookNote
	if err := c.doV2(ctx, http.MethodGet, notebookBase+"/notes/"+url.PathEscape(id), nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// PushNote 创建 notebook 笔记（POST /notes → 201 {revision:1, etag, removed}）。
// memora 侧没有幂等键消费（Idempotency-Key 不被识别），重试安全由调用方
// 按「tag 定位旧推送 + 内容哈希比对 + PUT 更新」组合实现（见 server_memora.go）。
func (c *HTTPClient) PushNote(ctx context.Context, req PushNoteRequest) (*PushNoteResponse, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("memora: note title is required")
	}
	var out PushNoteResponse
	// 201 也在 2xx 区间，doV2 直接过。
	if err := c.doV2(ctx, http.MethodPost, notebookBase+"/notes", req, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateNote 更新 notebook 笔记（PUT /notes/:id，If-Match: base_version）。
// baseVersion 必传：memora 侧更新必带 If-Match（缺 base 一律 400，无 LWW），
// noteSaveRequest 没有 base_version 字段——版本只能走 If-Match 头。版本冲突
// 时 memora 回 412 {error:version_conflict,...}，经 doV2 成为带 "HTTP 412" 的
// error 返回。
func (c *HTTPClient) UpdateNote(ctx context.Context, id string, baseVersion int, req PushNoteRequest) (*PushNoteResponse, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("memora: note id is required")
	}
	if baseVersion <= 0 {
		return nil, fmt.Errorf("memora: base version is required for update (If-Match)")
	}
	var out PushNoteResponse
	hdrs := map[string]string{"If-Match": strconv.Itoa(baseVersion)}
	if err := c.doV2(ctx, http.MethodPut, notebookBase+"/notes/"+url.PathEscape(id), req, &out, hdrs); err != nil {
		return nil, err
	}
	return &out, nil
}
