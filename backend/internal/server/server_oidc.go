package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/logging"
)

// ============================================================================
// Casdoor 直连 OIDC（2026-11-14 认证收敛轮）
//
// 背景：认证收敛目标 —— 全部用户认证放到 Casdoor，前端 UI 分散在各系统。
// Openpocket 此前的 SSO 面只覆盖「RedClaw auth-agent 镜像」一条道（三跳：
// pocket → RedClaw admin → auth-agent → IdP），而认证收敛要求 pocket 直接
// 作为 Casdoor OIDC client。本文件在**不新增路由**的前提下，把
// /api/auth/sso/* 全套管道（绑定 cookie → IdP 回调 → 一次性 exchange code）
// 扩展出 Casdoor 直连分支：
//
//   status   → 200 {"enabled":true,"mode":"oidc"|"redclaw"}（任一道启用）
//   login    → OIDC 分支直接返回 Casdoor authorize URL（PKCE S256 + state）
//   callback → OIDC 分支消费 code（PKCE verifier）→ /api/userinfo →
//              签发 pocketd 会话 JWT（与 issueTokenAndRespond 同链路）→
//              一次性 sso_code 302 回 SPA
//   exchange → 与 RedClaw 分支共用 ssoXchg（前端无感知）
//
// state 合约：与 RedClaw 分支同款双层 CSRF 绑定 —— /login 铸 nonce 落
// HttpOnly cookie 并以 state 传给 Casdoor，/callback 单次消费 cookie 并
// 常量时间比对 Casdoor 回显的 state == nonce。
//
// 启用条件：POCKET_CASDOOR_ENDPOINT + CLIENT_ID + CLIENT_SECRET 三项齐备
//（cfg.OIDCEnabled()）。启用且未配置 RedClaw admin 时，本地凭据面（口令
// 登录/注册/验证码/忘记密码）整体退役为 410（见 oidcRetiredLocalCredential）。
// ============================================================================

// ssoEnabled 任一 SSO 面（RedClaw 镜像或 Casdoor 直连）启用即视为 SSO 可用。
// 同时启用时 OIDC 分支优先（redirect_uri 在 Casdoor 侧注册的是本端点）。
func (s *Server) ssoEnabled() bool {
	return s.oidcEnabled() || (s.redclawAdminClient != nil && s.cfg.RedClawSsoEnabled)
}

func (s *Server) oidcEnabled() bool { return s.cfg.OIDCEnabled() }

// oidcRetiredLocalCredential 纯 OIDC 模式（OIDC 启用且无 RedClaw admin）下
// 本地凭据面的统一 410。返回 true 表示已写出响应，调用方直接 return。
func (s *Server) oidcRetiredLocalCredential(w http.ResponseWriter, r *http.Request) bool {
	if !(s.cfg.OIDCEnabled() && s.redclawAdminClient == nil) {
		return false
	}
	s.auditGateway(r, "auth.local_credential_retired", "-", "oidc-only mode", false)
	writeError(w, http.StatusGone,
		"local_credential_path_retired: 本地凭据面已退役，请使用 Casdoor SSO 登录（GET /api/auth/sso/login）")
	return true
}

// newPKCEPair 生成 RFC 7636 S256 对：verifier 43+ 字符 base64url，challenge
// 为其 SHA-256。与 RedClaw authagent 同款实现形状。
func newPKCEPair() (verifier, challenge string, err error) {
	raw := make([]byte, 48)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("pkce entropy: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// oidcAuthorizeURL 构建 Casdoor 授权端点 URL（state=绑定 nonce）。
func (s *Server) oidcAuthorizeURL(challenge, state string) string {
	q := url.Values{}
	q.Set("client_id", s.cfg.OIDCClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", s.cfg.OIDCRedirectURI())
	q.Set("scope", "read")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return strings.TrimRight(s.cfg.OIDCEndpoint, "/") + "/login/oauth/authorize?" + q.Encode()
}

// oidcLogin OIDC 分支的 /api/auth/sso/login 主体：铸 PKCE 对 + nonce，
// 落绑定 cookie，返回 Casdoor authorize URL。
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) (string, error) {
	verifier, challenge, err := newPKCEPair()
	if err != nil {
		return "", err
	}
	nonce, err := s.ssoTxns.IssueOIDC(clientIP(r), verifier)
	if err != nil {
		logging.Warnf("WARN: oidc login issue nonce: %v", err)
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ssoTxnCookie,
		Value:    nonce,
		Path:     "/api/auth/sso/",
		MaxAge:   int(ssoTxnTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
	return s.oidcAuthorizeURL(challenge, nonce), nil
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// oidcExchangeToken 用 code + PKCE verifier 换 access token
//（Casdoor /api/login/oauth/access_token，form 编码）。
func (s *Server) oidcExchangeToken(ctx context.Context, code, verifier string) (*oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", s.cfg.OIDCClientID)
	form.Set("client_secret", s.cfg.OIDCClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", s.cfg.OIDCRedirectURI())
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.cfg.OIDCEndpoint, "/")+"/api/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var tok oidcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned empty access_token")
	}
	return &tok, nil
}

type oidcUserInfo struct {
	Sub               string `json:"sub"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
}

// oidcFetchUserinfo 拉取 Casdoor /api/userinfo（Bearer access_token）。
// identity 以 IdP 的 sub 为准（不落在本地 users 表上）。
func (s *Server) oidcFetchUserinfo(ctx context.Context, accessToken string) (*oidcUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.cfg.OIDCEndpoint, "/")+"/api/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo endpoint status %d", resp.StatusCode)
	}
	var ui oidcUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&ui); err != nil {
		return nil, err
	}
	if ui.Sub == "" {
		return nil, fmt.Errorf("userinfo returned empty sub")
	}
	return &ui, nil
}

// handleAuthOidcCallback OIDC 分支的 /api/auth/sso/callback 主体。
// 流程与 RedClaw 分支同形：绑定 cookie 单次消费 → IdP 错误透传为稳定码 →
// code+state 校验（state == nonce）→ PKCE 换 token → userinfo → 签发
// pocketd 会话 JWT → 一次性 sso_code → 302 回 SPA。
func (s *Server) handleAuthOidcCallback(w http.ResponseWriter, r *http.Request) {
	// 1. 绑定 cookie + PKCE verifier：缺失/未知/已消费/已过期一律拒绝。
	ck, err := r.Cookie(ssoTxnCookie)
	if err != nil {
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=missing_binding_cookie", false)
		s.clearSsoTxnCookie(w, r)
		s.oidcRedirectError(w, r, "sso_session")
		return
	}
	nonce := ck.Value
	verifier, ok := s.ssoTxns.ConsumeOIDC(nonce)
	s.clearSsoTxnCookie(w, r)
	if !ok {
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=invalid_binding_cookie", false)
		s.oidcRedirectError(w, r, "sso_session")
		return
	}

	// 2. IdP 侧错误（casdoor 回传 error=...）转成 SPA 可展示的稳定错误码。
	if e := r.URL.Query().Get("error"); e != "" {
		logging.Warnf("WARN: oidc callback: IdP returned error=%s", sanitizeAuditDetail(e))
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=idp_error:"+sanitizeAuditDetail(e), false)
		s.oidcRedirectError(w, r, "sso_idp")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=missing_code_or_state", false)
		s.oidcRedirectError(w, r, "sso_invalid")
		return
	}

	// 3. 端到端 state 比对（login-CSRF 根治项）：Casdoor 必须原样回显
	//    login 时我方传入的 state（== 绑定 nonce）。
	if subtle.ConstantTimeCompare([]byte(state), []byte(nonce)) != 1 {
		logging.Warnf("WARN: oidc callback state mismatch (len=%d)", len(state))
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=state_mismatch", false)
		s.oidcRedirectError(w, r, "sso_state")
		return
	}

	tok, err := s.oidcExchangeToken(r.Context(), code, verifier)
	if err != nil {
		logging.Warnf("WARN: oidc token exchange failed: %v", err)
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=token_exchange_rejected", false)
		s.oidcRedirectError(w, r, "sso_upstream")
		return
	}
	ui, err := s.oidcFetchUserinfo(r.Context(), tok.AccessToken)
	if err != nil {
		logging.Warnf("WARN: oidc userinfo failed: %v", err)
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=userinfo_failed", false)
		s.oidcRedirectError(w, r, "sso_upstream")
		return
	}
	userID := ui.Sub
	displayName := ui.Name
	if displayName == "" {
		displayName = ui.PreferredUsername
	}

	// 4. 签发 pocketd 会话 JWT（与 issueTokenAndRespond 同链路）。
	token, wsID, err := s.mintSessionToken(r.Context(), "casdoor", userID, displayName, ui.Email)
	if err != nil {
		logging.Warnf("WARN: oidc mint session: %v", err)
		s.auditGateway(r, "auth.oidc.callback", "-", "reason=sign_failed", false)
		s.oidcRedirectError(w, r, "sso_upstream")
		return
	}

	// 5. 一次性 code → 302 回 SPA（token 不进 URL，前端 exchange 换取）。
	ssoCode, err := s.ssoXchg.Put(ssoHandoff{
		Token:       token,
		User:        displayName,
		UserID:      userID,
		WorkspaceID: wsID,
	})
	if err != nil {
		logging.Warnf("WARN: oidc issue exchange code: %v", err)
		s.oidcRedirectError(w, r, "sso_upstream")
		return
	}
	s.auditGateway(r, "auth.oidc.callback", userID, "oidc login completed", true)
	http.Redirect(w, r, s.oidcSPAURL(url.Values{"sso_code": {ssoCode}}), http.StatusFound)
}

// oidcSPAURL 一次性 code / 错误码的 SPA 落点。hash-router 需要
// 「path + search + #route」双段齐备：SsoCallbackView 读 location.search，
// vue-router（createWebHashHistory）匹配 # 段 —— 任缺一段回调链即断。
func (s *Server) oidcSPAURL(q url.Values) string {
	base := s.cfg.OIDCBrowserReturnURL()
	if base == "" {
		// Validate() 在生产已拦；此处兜底退化为相对路径（dev 内联场景）。
		return "/auth/sso/callback?" + q.Encode()
	}
	return base + "/auth/sso/callback?" + q.Encode() + "#/auth/sso/callback"
}

// oidcRedirectError 把失败重定向到 SPA 回调页（只回传稳定错误码）。
func (s *Server) oidcRedirectError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, s.oidcSPAURL(url.Values{"error": {code}}), http.StatusFound)
}

// mintSessionToken 与 issueTokenAndRespond 同链路的签发主体：
// EnsureDefaultWorkspace + SignWithWorkspace + RecordShadow。
// source 是 shadow 记录的身份来源（"pocket"=本地链路；"casdoor"=OIDC 直连）。
// OIDC 回调在 302 语境里只拿 token，不复用写响应的版本。
func (s *Server) mintSessionToken(ctx context.Context, source, userID, username, email string) (string, string, error) {
	wsID := "default"
	if s.identityStore != nil {
		ws, err := s.identityStore.EnsureDefaultWorkspace(ctx, userID)
		if err != nil {
			logging.Warnf("WARN: EnsureDefaultWorkspace for %s failed: %v (falling back to 'default')", userID, err)
		} else if ws != nil {
			wsID = ws.ID
		}
	}
	if s.jwtSigner == nil {
		return "", "", fmt.Errorf("jwt signer unavailable")
	}
	token, err := s.jwtSigner.SignWithWorkspace(userID, "user", wsID)
	if err != nil {
		return "", "", fmt.Errorf("sign session jwt: %w", err)
	}
	auth.RecordShadow(source, userID, wsID, username, email)
	return token, wsID, nil
}
