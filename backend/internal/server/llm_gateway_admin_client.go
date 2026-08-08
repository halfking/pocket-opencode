package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// gatewayAdminClient 负责与 llm-gateway-go 的 admin API 通话。
//
// 网关 admin API 自 2026-07-10 起只认 JWT：POST /api/auth/token 用用户名+密码
// 换 access_token，TTL 24h。sk-* 数据面 key 无法访问 /api/providers、
// /api/credentials/* 这些端点，所以这里必须自己维护登录态。
//
// token 按 (workspace, node) 缓存在内存里，进程重启即失效（重新登录成本很低，
// 没必要持久化一个短命凭据）。缓存条目带凭据指纹：用户名或密码被改过之后，
// 旧 token 会被自动丢弃，不会出现"改了密码但仍在用旧会话"的情况。
type gatewayAdminClient struct {
	store *GatewayNodeStore

	mu     sync.Mutex
	tokens map[string]*gatewayToken
}

type gatewayToken struct {
	value string
	// expiresAt 是本地判定的过期时刻；实际比上游 expires_at 提前
	// gatewayTokenRefreshLead，避免 token 在飞行途中过期。
	expiresAt time.Time
	// fingerprint 绑定签发时使用的凭据，凭据变更后旧 token 自动失效。
	fingerprint string
	// role 是签发时 /api/auth/token 返回的角色，用于给出
	// "该账号不是 super_admin" 这类可诊断的提示。
	role string
}

const (
	// gatewayTokenRefreshLead 是提前刷新窗口。上游 TTL 是 24h，提前 5min
	// 重登对网关几乎没有额外负担。
	gatewayTokenRefreshLead = 5 * time.Minute
	// gatewayAdminTimeout 覆盖普通 admin API 调用。monitor-summary 上游自己
	// 有 15s 的 ctx 超时且带 30s 缓存，这里留一点余量。
	gatewayAdminTimeout = 20 * time.Second
	// gatewayLoginTimeout 单独设短一点：登录只是一次 bcrypt 校验。
	gatewayLoginTimeout = 10 * time.Second
)

func newGatewayAdminClient(store *GatewayNodeStore) *gatewayAdminClient {
	return &gatewayAdminClient{
		store:  store,
		tokens: make(map[string]*gatewayToken),
	}
}

// gatewayAPIError 表示上游返回了非 2xx。保留上游状态码，让 handler 能把
// 403/404 这类语义原样透传给移动端，而不是一律 502。
type gatewayAPIError struct {
	StatusCode int
	Message    string
}

func (e *gatewayAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gateway returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("gateway returned status %d: %s", e.StatusCode, e.Message)
}

// adminBaseURL 归一化节点 base URL 到 admin API 的根。
//
// 历史配置（llm_gateway_configs）存的是数据面端点，通常带 /v1 后缀
// （https://llmgo.kxpms.cn/v1）。admin API 挂在根路径上，所以这里统一剥掉
// 尾部的 /v1。同时校验目标地址，防止节点表被写入内网/元数据地址后被当作
// SSRF 跳板使用。
func adminBaseURL(rawBase string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(rawBase), "/")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		return "", fmt.Errorf("node base URL is empty")
	}
	if err := validateGatewayURL(base); err != nil {
		return "", err
	}
	return base, nil
}

// buildURL 把上游路径与 query 拼到 admin base 上。
// upstreamPath 必须以 / 开头且由代码控制（来自白名单表），不接受调用方拼接。
func buildURL(base, upstreamPath string, query url.Values) (string, error) {
	if !strings.HasPrefix(upstreamPath, "/") {
		return "", fmt.Errorf("upstream path must start with /")
	}
	u, err := url.Parse(base + upstreamPath)
	if err != nil {
		return "", err
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

func credentialFingerprint(username, password string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + password))
	return hex.EncodeToString(sum[:8])
}

func tokenCacheKey(workspaceID string, nodeID int64) string {
	return fmt.Sprintf("%s/%d", normalizeWorkspace(workspaceID), nodeID)
}

// InvalidateNode 丢弃某节点的缓存 token。节点被更新或删除时调用。
func (c *gatewayAdminClient) InvalidateNode(workspaceID string, nodeID int64) {
	key := tokenCacheKey(workspaceID, nodeID)
	c.mu.Lock()
	delete(c.tokens, key)
	c.mu.Unlock()
}

// login 用节点凭据换一个新 token，并写入缓存。
func (c *gatewayAdminClient) login(ctx context.Context, secret *GatewayNodeSecret) (*gatewayToken, error) {
	if secret.Node.AdminUsername == "" || secret.AdminPassword == "" {
		return nil, fmt.Errorf("node %q has no admin credentials configured", secret.Node.Name)
	}
	base, err := adminBaseURL(secret.Node.BaseURL)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]string{
		"username": secret.Node.AdminUsername,
		"password": secret.AdminPassword,
	})
	if err != nil {
		return nil, err
	}

	loginCtx, cancel := context.WithTimeout(ctx, gatewayLoginTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(loginCtx, http.MethodPost, base+"/api/auth/token", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := gatewayHTTPClient(gatewayLoginTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway login failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMGatewayResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("gateway login: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &gatewayAPIError{
			StatusCode: resp.StatusCode,
			Message:    gatewayErrorMessage(body, "login rejected"),
		}
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   string `json:"expires_at"`
		User        struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("gateway login: invalid response: %w", err)
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("gateway login: response carried no access_token")
	}

	// 上游给的是绝对时刻；解析失败时退回一个保守的 1h，宁可多登录几次也不要
	// 缓存一个不知道何时失效的 token。
	expiresAt := time.Now().Add(time.Hour)
	if parsed.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, parsed.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	tok := &gatewayToken{
		value:       parsed.AccessToken,
		expiresAt:   expiresAt.Add(-gatewayTokenRefreshLead),
		fingerprint: credentialFingerprint(secret.Node.AdminUsername, secret.AdminPassword),
		role:        parsed.User.Role,
	}

	c.mu.Lock()
	c.tokens[tokenCacheKey(secret.Node.WorkspaceID, secret.Node.ID)] = tok
	c.mu.Unlock()
	return tok, nil
}

// token 返回一个可用 token：命中缓存且未过期、且凭据指纹一致时直接复用，
// 否则重新登录。
func (c *gatewayAdminClient) token(ctx context.Context, secret *GatewayNodeSecret) (*gatewayToken, error) {
	key := tokenCacheKey(secret.Node.WorkspaceID, secret.Node.ID)
	fp := credentialFingerprint(secret.Node.AdminUsername, secret.AdminPassword)

	c.mu.Lock()
	cached, ok := c.tokens[key]
	if ok && cached.fingerprint == fp && time.Now().Before(cached.expiresAt) {
		c.mu.Unlock()
		return cached, nil
	}
	// 指纹不符（凭据被改过）或已过期 —— 先清掉，避免并发调用继续读到旧值。
	if ok {
		delete(c.tokens, key)
	}
	c.mu.Unlock()

	return c.login(ctx, secret)
}

// gatewayErrorMessage 从上游错误响应里提取可读信息。
//
// 上游错误体一般是 {"error":"..."}；不是 JSON 时（nginx 502 页面之类）截断
// 返回，避免把整张 HTML 塞进移动端响应。
func gatewayErrorMessage(body []byte, fallback string) string {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error != "" {
			return parsed.Error
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fallback
	}
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "…"
	}
	return trimmed
}

// do 向节点的 admin API 发一次请求，返回原始响应体。
//
// 401 时清缓存并重登一次 —— token 可能因为网关重启（JWT secret 轮换）或
// 用户被禁用而提前失效。只重试一次，避免凭据真的失效时打成登录风暴。
func (c *gatewayAdminClient) do(
	ctx context.Context,
	secret *GatewayNodeSecret,
	method, upstreamPath string,
	query url.Values,
	body []byte,
) ([]byte, error) {
	base, err := adminBaseURL(secret.Node.BaseURL)
	if err != nil {
		return nil, err
	}
	target, err := buildURL(base, upstreamPath, query)
	if err != nil {
		return nil, err
	}

	tok, err := c.token(ctx, secret)
	if err != nil {
		return nil, err
	}

	respBody, status, err := c.roundTrip(ctx, target, method, tok.value, body)
	if err != nil {
		return nil, err
	}

	if status == http.StatusUnauthorized {
		c.InvalidateNode(secret.Node.WorkspaceID, secret.Node.ID)
		refreshed, loginErr := c.login(ctx, secret)
		if loginErr != nil {
			return nil, loginErr
		}
		respBody, status, err = c.roundTrip(ctx, target, method, refreshed.value, body)
		if err != nil {
			return nil, err
		}
	}

	if status < 200 || status >= 300 {
		return nil, &gatewayAPIError{
			StatusCode: status,
			Message:    gatewayErrorMessage(respBody, "upstream request failed"),
		}
	}
	return respBody, nil
}

func (c *gatewayAdminClient) roundTrip(
	ctx context.Context,
	target, method, token string,
	body []byte,
) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, gatewayAdminTimeout)
	defer cancel()

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, target, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := gatewayHTTPClient(gatewayAdminTimeout).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("gateway request failed: %w", err)
	}
	defer resp.Body.Close()

	// 限长读取：上游 monitor-summary 在凭据很多时体积可观，但仍应远小于 1MB。
	// 超限视为异常响应而不是截断后当正常数据用。
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMGatewayResponseBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("gateway response read failed: %w", err)
	}
	if len(payload) > maxLLMGatewayResponseBytes {
		return nil, 0, fmt.Errorf("gateway response exceeded %d bytes", maxLLMGatewayResponseBytes)
	}
	return payload, resp.StatusCode, nil
}

// stream 打开一个上游 SSE 连接，调用方负责关闭返回的 Response.Body。
//
// 这里不能复用 do：SSE 是长连接，既不能设 client.Timeout（会在流中途掐断），
// 也不能一次性 ReadAll。ctx 由调用方控制生命周期 —— 移动端断开时 cancel ctx
// 即可让上游连接一起收敛，不会泄一条空转的 SSE。
func (c *gatewayAdminClient) stream(
	ctx context.Context,
	secret *GatewayNodeSecret,
	upstreamPath string,
	query url.Values,
) (*http.Response, error) {
	base, err := adminBaseURL(secret.Node.BaseURL)
	if err != nil {
		return nil, err
	}
	target, err := buildURL(base, upstreamPath, query)
	if err != nil {
		return nil, err
	}

	tok, err := c.token(ctx, secret)
	if err != nil {
		return nil, err
	}

	resp, err := c.openStream(ctx, target, tok.value)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		c.InvalidateNode(secret.Node.WorkspaceID, secret.Node.ID)
		refreshed, loginErr := c.login(ctx, secret)
		if loginErr != nil {
			return nil, loginErr
		}
		if resp, err = c.openStream(ctx, target, refreshed.value); err != nil {
			return nil, err
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &gatewayAPIError{
			StatusCode: resp.StatusCode,
			Message:    gatewayErrorMessage(body, "upstream stream rejected"),
		}
	}
	return resp, nil
}

func (c *gatewayAdminClient) openStream(ctx context.Context, target, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// timeout=0：长连接不能有整体超时，拨号阶段的超时由 transport 的
	// Dialer.Timeout 兜住。
	resp, err := gatewayHTTPClient(0).Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway stream failed: %w", err)
	}
	return resp, nil
}

// probe 验证节点凭据是否可用，返回登录后拿到的角色。
//
// 除了登录，还额外拉一次 /api/auth/me：登录成功只证明密码对，而 me 能确认
// token 真的能过 admin 中间件（比如用户被禁用、必须改密码这类状态）。
func (c *gatewayAdminClient) probe(ctx context.Context, secret *GatewayNodeSecret) (string, error) {
	// 强制重登，probe 的意义就是验证当前凭据而不是复用旧会话。
	c.InvalidateNode(secret.Node.WorkspaceID, secret.Node.ID)
	tok, err := c.login(ctx, secret)
	if err != nil {
		return "", err
	}

	body, err := c.do(ctx, secret, http.MethodGet, "/api/auth/me", nil, nil)
	if err != nil {
		return tok.role, err
	}

	var me struct {
		Role     string `json:"role"`
		Username string `json:"username"`
		User     struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &me); err == nil {
		if me.Role != "" {
			return me.Role, nil
		}
		if me.User.Role != "" {
			return me.User.Role, nil
		}
	}
	return tok.role, nil
}
