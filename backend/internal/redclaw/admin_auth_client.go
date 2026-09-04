// Package redclaw — admin_auth_client.go 提供 RedClaw Admin 后端
// (POST /api/v1/auth/{login,me,change-password,logout}) 以及 Auth Agent
// 的 SSO 入口 (/api/v1/sso/{login,callback}) 的调用客户端。
//
// 设计目标：
//   - openpocket 不再自签 JWT，仅做 token 透传；权威源在 RedClaw。
//   - 严格 fail-soft：网络/4xx/5xx 全部以 error 形式抛出，由调用方决定
//     是否回退到本地（dev 旁路）。
//   - 复用 internal/redclaw.ClientConfig 的样式，保持参数风格一致。
package redclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AdminAuthClientConfig 描述 RedClaw Admin / Auth Agent 客户端配置。
//
// 与 ClientConfig 的差别：
//   - AdminURL 指向 RedClaw Admin 后端（默认 :8081），用于 /api/v1/auth/*。
//   - AuthAgentURL 指向 RedClaw Auth Agent（默认 :8082），用于 /api/v1/sso/*。
//   - Secret 是这两个端口共用的 POCKET_REDCLAW_ADMIN_SECRET（HS256 共享密钥）。
//   - 若 AdminURL == AuthAgentURL（单进程部署），AuthAgentURL 留空。
type AdminAuthClientConfig struct {
	AdminURL     string
	AuthAgentURL string
	Secret       string
	TenantID     string
	TimeoutSec   int
}

// AdminAuthClient 调用 RedClaw Admin / Auth Agent 的客户端。
//
// 与 AuthClient（fail-soft 镜像）不同，AdminAuthClient 的所有方法都会
// 向上返回 error——因为登录路径必须明确告诉调用方"成功/失败"，不能像
// 镜像那样静默吞掉。
type AdminAuthClient struct {
	cfg       AdminAuthClientConfig
	adminHTTP *http.Client
	authHTTP  *http.Client
	// authBase 解析后的 Auth Agent 基址（AuthAgentURL 为空时回落 AdminURL，
	// 单进程部署场景）。SsoLoginURL / SsoCallback 必须共用同一解析结果，
	// 否则 login 带了 fallback 而 callback 拿空 base 拼出相对 URL。
	authBase string
}

// NewAdminAuthClient 构造 AdminAuthClient。
func NewAdminAuthClient(cfg AdminAuthClientConfig) (*AdminAuthClient, error) {
	if cfg.AdminURL == "" {
		return nil, fmt.Errorf("redclaw admin: AdminURL cannot be empty")
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("redclaw admin: Secret cannot be empty")
	}
	if cfg.TenantID == "" {
		cfg.TenantID = "default"
	}
	authBase := strings.TrimRight(cfg.AuthAgentURL, "/")
	if authBase == "" {
		authBase = strings.TrimRight(cfg.AdminURL, "/")
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 10
	}
	return &AdminAuthClient{
		cfg:       cfg,
		adminHTTP: &http.Client{Timeout: time.Duration(timeout) * time.Second},
		authHTTP:  &http.Client{Timeout: time.Duration(timeout) * time.Second},
		authBase:  authBase,
	}, nil
}

// =====================================================================
// 请求/响应 DTO（与 RedClaw admin/handlers/security.go 对齐）
// =====================================================================

// LoginRequest 发送给 RedClaw Admin 的登录请求。
//
// RedClaw 接受 employee_id / employeeNo 任一字段；为兼容老前端表单，沿用
// openpocket 的 "username" 字段名，但映射到 employeeNo。
type LoginRequest struct {
	EmployeeID string `json:"employee_id"`
	Password   string `json:"password"`
}

// LoginResult RedClaw Admin /auth/login 的响应。
type LoginResult struct {
	Token              string        `json:"token"`
	MustChangePassword bool          `json:"mustChangePassword"`
	Employee           *EmployeeInfo `json:"employee"`
}

// SsoCallbackClaims 是 auth-agent /sso/callback 响应中 claims 字段的形状
// （解析自 id_token 的受信声明）。
type SsoCallbackClaims struct {
	Sub    string `json:"sub"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Tenant string `json:"tenant"`
}

// SsoCallbackResult 是 RedClaw auth-agent /sso/callback 的真实响应形状：
// {"jwt","claims","next","external_state"}。注意这与 Admin /auth/login 的
// LoginResult（token+employee）不同——此前误按 Login 形状解码，真实链路
// 会在 empty token 处断裂（2026-09-05 review 发现并修正）。
type SsoCallbackResult struct {
	JWT    string            `json:"jwt"`
	Claims SsoCallbackClaims `json:"claims"`
	Next   string            `json:"next"`
	// ExternalState 是 auth-agent 对 login 时 external_state 参数的回显
	//（见 SsoLoginURL）。pocket 的 handleAuthSsoCallback 用它与本地绑定
	// nonce 做常量时间比对；空值即失配（旧版 auth-agent 无回显，严格比对
	// fail-closed）。
	ExternalState string `json:"external_state"`
}

// EmployeeInfo 是 RedClaw Admin 返回的当前用户画像（camelCase 与前端 AuthUser 对齐）。
type EmployeeInfo struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Role               string   `json:"role"`
	Email              string   `json:"email"`
	DepartmentID       string   `json:"departmentId"`
	PositionID         string   `json:"positionId"`
	AgentID            string   `json:"agentId"`
	Channels           []string `json:"channels"`
	MustChangePassword bool     `json:"mustChangePassword"`
}

// MeResult RedClaw Admin /auth/me 的响应。
//
// 注意:RedClaw 的 /auth/me 返回顶层扁平的 employee 字段
// ({"id":..,"name":..,"role":..} 而非 {"employee":{...}}),与 /login 的
// 嵌套结构不同,所以这里直接解到 EmployeeInfo。
type MeResult = EmployeeInfo

// ChangePasswordRequest RedClaw Admin /auth/change-password 的请求。
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// =====================================================================
// 错误类型
// =====================================================================

// ErrRedClawUnavailable 表示网络/5xx/解析失败——调用方应降级。
var ErrRedClawUnavailable = fmt.Errorf("redclaw admin: service unavailable")

// ErrInvalidCredentials 表示 401/403——凭据错误。
var ErrInvalidCredentials = fmt.Errorf("redclaw admin: invalid credentials")

// ErrTenantMismatch 表示 tenant_id 不一致。
var ErrTenantMismatch = fmt.Errorf("redclaw admin: tenant mismatch")

// redClawAdminError 解析 RedClaw 的 { "error": { "code", "message" } } 结构。
type redClawAdminError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// =====================================================================
// Login — POST /api/v1/auth/login
// =====================================================================

// Login 用 employee_id + password 在 RedClaw 登录。
//
// 成功返回 LoginResult（包含 token + employee 画像）；
// 失败按 HTTP 状态码分类：
//   - 401 / 403 → ErrInvalidCredentials
//   - 502 / 503 / 网络错误 → ErrRedClawUnavailable
func (c *AdminAuthClient) Login(ctx context.Context, employeeID, password string) (*LoginResult, error) {
	if employeeID == "" || password == "" {
		return nil, fmt.Errorf("redclaw admin: employee_id and password are required")
	}
	body, _ := json.Marshal(LoginRequest{EmployeeID: employeeID, Password: password})
	resp, err := c.doAdmin(ctx, http.MethodPost, "/api/v1/auth/login", body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	defer resp.Body.Close()

	// 401 → 凭据错；403 → 让 parseAdminError 解析，区分 tenant_mismatch vs 其它
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidCredentials
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrRedClawUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseAdminError(resp.StatusCode, resp.Body)
	}

	var out LoginResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode login response: %v", ErrRedClawUnavailable, err)
	}
	if out.Token == "" {
		return nil, fmt.Errorf("%w: empty token in response", ErrRedClawUnavailable)
	}
	return &out, nil
}

// =====================================================================
// Me — GET /api/v1/auth/me
// =====================================================================

// Me 用 RedClaw token 拉取当前 employee 画像。
func (c *AdminAuthClient) Me(ctx context.Context, token string) (*MeResult, error) {
	if token == "" {
		return nil, fmt.Errorf("redclaw admin: token is required")
	}
	resp, err := c.doAdminWithToken(ctx, http.MethodGet, "/api/v1/auth/me", nil, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidCredentials
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrRedClawUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseAdminError(resp.StatusCode, resp.Body)
	}

	var out MeResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode me response: %v", ErrRedClawUnavailable, err)
	}
	return &out, nil
}

// =====================================================================
// ChangePassword — POST /api/v1/auth/change-password
// =====================================================================

// ChangePassword 用 RedClaw token 改密。
func (c *AdminAuthClient) ChangePassword(ctx context.Context, token, oldPwd, newPwd string) error {
	if token == "" {
		return fmt.Errorf("redclaw admin: token is required")
	}
	if oldPwd == "" || newPwd == "" {
		return fmt.Errorf("redclaw admin: old/new password are required")
	}
	body, _ := json.Marshal(ChangePasswordRequest{OldPassword: oldPwd, NewPassword: newPwd})
	resp, err := c.doAdminWithToken(ctx, http.MethodPost, "/api/v1/auth/change-password", body, token)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidCredentials
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: HTTP %d", ErrRedClawUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseAdminError(resp.StatusCode, resp.Body)
	}
	return nil
}

// =====================================================================
// Logout — POST /api/v1/auth/logout
// =====================================================================

// Logout 撤销 RedClaw 上的当前 session。
func (c *AdminAuthClient) Logout(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("redclaw admin: token is required")
	}
	resp, err := c.doAdminWithToken(ctx, http.MethodPost, "/api/v1/auth/logout", nil, token)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	defer resp.Body.Close()

	// 401 也视作"已登出"，幂等
	if resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: HTTP %d", ErrRedClawUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseAdminError(resp.StatusCode, resp.Body)
	}
	return nil
}

// =====================================================================
// SSO — GET /api/v1/sso/login  &  /api/v1/sso/callback
// =====================================================================

// SsoLoginURL 拼出 RedClaw Auth Agent 的 SSO 登录入口 URL。
// state 由 pocket 生成（登录绑定 nonce），以 external_state 参数传给
// auth-agent：RedClaw 侧（2026-09-05 方案 A）把它存入 replay 表并在
// /sso/callback 响应中原样回显，pocket 据此做端到端比对。空则省略参数。
func (c *AdminAuthClient) SsoLoginURL(state, redirectURL string) string {
	u, err := url.Parse(c.authBase + "/api/v1/sso/login")
	if err != nil {
		return ""
	}
	q := u.Query()
	if state != "" {
		q.Set("external_state", state)
	}
	if redirectURL != "" {
		q.Set("redirect_url", redirectURL)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// SsoCallback 用 IdP 返回的 code + state 调 RedClaw auth-agent /sso/callback，
// 成功返回平台 JWT + 受信 claims（+ external_state 回显，供端到端比对）。
func (c *AdminAuthClient) SsoCallback(ctx context.Context, code, state string) (*SsoCallbackResult, error) {
	if code == "" || state == "" {
		return nil, fmt.Errorf("redclaw admin: code and state are required")
	}
	u, err := url.Parse(c.authBase + "/api/v1/sso/callback")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	q := u.Query()
	q.Set("code", code)
	q.Set("state", state)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)

	resp, err := c.authHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedClawUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrInvalidCredentials
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrRedClawUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseAdminError(resp.StatusCode, resp.Body)
	}

	var out SsoCallbackResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode sso callback: %v", ErrRedClawUnavailable, err)
	}
	if out.JWT == "" {
		return nil, fmt.Errorf("%w: empty jwt in sso callback", ErrRedClawUnavailable)
	}
	if out.Claims.Sub == "" {
		return nil, fmt.Errorf("%w: empty sub in sso callback claims", ErrRedClawUnavailable)
	}
	return &out, nil
}

// =====================================================================
// 内部 HTTP 工具
// =====================================================================

func (c *AdminAuthClient) doAdmin(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.AdminURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.adminHTTP.Do(req)
}

func (c *AdminAuthClient) doAdminWithToken(ctx context.Context, method, path string, body []byte, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.AdminURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// 优先用 RedClaw 用户 token 鉴权；Secret 作为服务级 token 仅用于 /login。
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.adminHTTP.Do(req)
}

// parseAdminError 解析 RedClaw 的标准错误包络 { "error": { "code", "message" } }。
func (c *AdminAuthClient) parseAdminError(status int, body io.Reader) error {
	data, _ := io.ReadAll(body)
	var env redClawAdminError
	if json.Unmarshal(data, &env) == nil && env.Error.Code != "" {
		switch env.Error.Code {
		case "tenant_mismatch":
			return fmt.Errorf("%w: %s", ErrTenantMismatch, env.Error.Message)
		case "unauthorized", "invalid_credentials":
			return fmt.Errorf("%w: %s", ErrInvalidCredentials, env.Error.Message)
		case "weak_password":
			return fmt.Errorf("redclaw admin: weak password: %s", env.Error.Message)
		}
		return fmt.Errorf("redclaw admin: HTTP %d %s: %s", status, env.Error.Code, env.Error.Message)
	}
	return fmt.Errorf("redclaw admin: HTTP %d: %s", status, string(data))
}
