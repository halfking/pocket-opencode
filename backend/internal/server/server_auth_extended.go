package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/notify"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// ============================================================================
// C4 — 邮箱注册 / 验证码登录 / 忘记密码 相关 HTTP handler
// ============================================================================
//
// 一期切换后：
//   - send-code / register / code-login / forgot-password：
//     RedClaw Admin 已配置时返回 501（RedClaw 暂无对应端点，由 openpocket
//     走本地 codeStore + userStore 旧路径 + 镜像到 RedClaw），保证用户面
//     行为不变；RedClaw 暴露相应端点后可直接切到代理模式。
//   - reset-password(已登录改密)：直接代理到 RedClaw /api/v1/auth/change-password。
//   - logout：新增 /api/auth/logout 端点，调 RedClaw 撤销 session。

// handleAuthSendCode — POST /api/auth/send-code
//
// Body: {"email": "...", "purpose": "register|reset|login"}
// 响应：200 {"ok": true, "ttl_sec": 300, "debug_code": "123456"}（debug_code 仅当 POCKET_SMTP_DEBUG_ECHO=true 且 SMTP 未配置时）
func (s *Server) handleAuthSendCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.codeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "send-code not enabled")
		return
	}
	var body struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	purpose := auth.CodePurpose(strings.ToLower(strings.TrimSpace(body.Purpose)))
	if !purpose.Valid() {
		writeError(w, http.StatusBadRequest, "invalid purpose")
		return
	}

	code, ttlSec, err := s.codeStore.Generate(r.Context(), body.Email, purpose, clientIP(r))
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			writeError(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		if errors.Is(err, auth.ErrEmailInvalid) {
			// 邮箱格式无效：同样恒返 200（防枚举），不写库
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ttl_sec": 0})
			return
		}
		log.Printf("WARN: send-code generate: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ttl_sec": 0})
		return
	}

	// 发邮件：失败仅 log（频控已成功消耗；用户后续可重试）
	go s.sendCodeEmail(code, ttlSec, body.Email, purpose)

	resp := map[string]any{"ok": true, "ttl_sec": ttlSec}
	// Debug 回显：SMTP 未配置 + 启用 debug 时，把 code 暴露给前端 dev 面板
	if s.cfg.SMTPDebugEcho && s.smtpClient == nil {
		resp["debug_code"] = code
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) sendCodeEmail(code string, ttlSec int, to string, purpose auth.CodePurpose) {
	if s.smtpClient == nil {
		log.Printf("INFO: [send-code] smtp not configured; code=%s email=%s purpose=%s", code, to, purpose)
		return
	}
	subject, body := codeEmailContent(code, ttlSec, purpose)
	ctx, cancel := withTimeout(10 * time.Second)
	defer cancel()
	if err := s.smtpClient.Send(ctx, notify.Message{To: to, Subject: subject, Text: body}); err != nil {
		log.Printf("WARN: [send-code] smtp send to=%s: %v", to, err)
	}
}

// handleAuthRegister — POST /api/auth/register
//
// Body: {"email":"...", "code":"123456", "username":"...", "password":"..."}
// 成功：200 {"token":"...", "user":"...", "user_id":"...", "workspace_id":"..."}
func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.codeStore == nil || s.userStore == nil || s.jwtSigner == nil {
		writeError(w, http.StatusServiceUnavailable, "register not enabled")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := validateUsername(body.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.codeStore.Verify(r.Context(), body.Email, auth.PurposeRegister, body.Code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	emailLower := strings.ToLower(strings.TrimSpace(body.Email))
	u := &auth.User{
		ID:            "user-" + emailLower,
		Username:      body.Username,
		Email:         emailLower,
		EmailVerified: true,
		Role:          "user",
	}
	if err := s.userStore.InsertUser(r.Context(), u, body.Password, emailLower); err != nil {
		if errors.Is(err, auth.ErrEmailAlreadyExists) {
			writeError(w, http.StatusConflict, "邮箱已被注册")
			return
		}
		// 用户名唯一冲突：pg 也会回 23505
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "23505") {
			writeError(w, http.StatusConflict, "用户名已被占用")
			return
		}
		log.Printf("WARN: register insert user: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	s.issueTokenAndRespond(w, r, u.ID, body.Username, u.Email)
	// mirror（fail-soft）：注册成功后才尝试同步到 RedClaw
	go s.mirrorRegisterToRedClaw(u, body.Password)
}

// handleAuthCodeLogin — POST /api/auth/code-login
//
// Body: {"email":"...", "code":"123456"}
// 成功：200 同 register
func (s *Server) handleAuthCodeLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.codeStore == nil || s.userStore == nil || s.jwtSigner == nil {
		writeError(w, http.StatusServiceUnavailable, "code-login not enabled")
		return
	}
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.codeStore.Verify(r.Context(), body.Email, auth.PurposeLogin, body.Code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.userStore.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.issueTokenAndRespond(w, r, u.ID, u.Username, u.Email)
}

// handleAuthForgotPassword — POST /api/auth/forgot-password
//
// Body: {"email":"...", "code":"123456", "new_password":"..."}
// 成功：200 {"ok": true}（不返回 token，强制重新登录）
func (s *Server) handleAuthForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.codeStore == nil || s.userStore == nil {
		writeError(w, http.StatusServiceUnavailable, "forgot-password not enabled")
		return
	}
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := validatePassword(body.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.codeStore.Verify(r.Context(), body.Email, auth.PurposeReset, body.Code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 邮件不存在 → 同样恒返 ok（防枚举）
	if err := s.userStore.UpdatePasswordByEmail(r.Context(), body.Email, body.NewPassword); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		log.Printf("WARN: forgot-password update: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthResetPassword — POST /api/auth/reset-password（已登录改密码）
//
// 一期切换：直接代理到 RedClaw /api/v1/auth/change-password。
// 旧逻辑（用 userStore 改本地密码）作为 legacy 路径保留，
// 仅在 POCKET_AUTH_LEGACY_ONLY=true 且本地 userStore 可用时启用。
//
// Body: {"email":"...", "old_password":"...", "new_password":"..."}
func (s *Server) handleAuthResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Email       string `json:"email"`
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := validatePassword(body.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 路径 1：RedClaw 代理（一期主路径）
	if s.redclawAdminClient != nil {
		// 拿当前请求的 RedClaw token（前端存在 localStorage 里的 pocket_token
		// 就是 RedClaw 颁发的 HS256 JWT，可直接复用）
		raw := extractBearerToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		if err := s.redclawAdminClient.ChangePassword(r.Context(), raw, body.OldPassword, body.NewPassword); err != nil {
			s.handleRedClawAuthError(w, err, "redclaw change-password failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// 路径 2：legacy（本地 userStore）
	if s.userStore == nil {
		writeError(w, http.StatusServiceUnavailable, "reset-password not enabled")
		return
	}
	uid := s.userIDFromRequest(r)
	if uid == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := s.userStore.GetUserByEmail(r.Context(), body.Email)
	if err != nil || u.ID != uid {
		writeError(w, http.StatusUnauthorized, "invalid email")
		return
	}
	if _, err := s.userStore.VerifyPassword(r.Context(), u.Username, body.OldPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "old password incorrect")
		return
	}
	if err := s.userStore.UpdatePasswordByEmail(r.Context(), body.Email, body.NewPassword); err != nil {
		log.Printf("WARN: reset-password update: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthLogout — POST /api/auth/logout
//
// 一期新增：调 RedClaw Admin 撤销会话（401 视作幂等成功）。
// 前端在调通本端点后再清 localStorage，确保服务端 session 真正失效。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.redclawAdminClient == nil {
		// legacy 模式：本地 JWT 没办法主动撤销（仅依赖自然过期），直接返 ok
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	raw := extractBearerToken(r)
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	if err := s.redclawAdminClient.Logout(r.Context(), raw); err != nil {
		s.handleRedClawAuthError(w, err, "redclaw logout failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthMe — GET /api/auth/me（一期新增：前端用它判断 token 状态）
//
// 返回当前 RedClaw employee 画像（id/name/role/email/...），前端用来
// 续期 / 显示用户卡。401 → 前端跳登录。
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil {
		// legacy 模式：仅返回 JWT claim 推断的最小画像
		uid := s.userIDFromRequest(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":    uid,
			"name":  uid,
			"role":  "user",
			"email": "",
		})
		return
	}
	raw := extractBearerToken(r)
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	res, err := s.redclawAdminClient.Me(r.Context(), raw)
	if err != nil {
		s.handleRedClawAuthError(w, err, "redclaw me failed")
		return
	}
	// MeResult 即扁平的 EmployeeInfo(RedClaw /auth/me 顶层结构)
	writeJSON(w, http.StatusOK, res)
}

// handleAuthRefresh — POST /api/auth/refresh（JWT 滑动续期，runbook §15.2 方案 c 落地）。
//
// 前端在 token 临期（剩余 <5min）主动调用、或在 401 后单飞重试一次；每次仍签发
// 短 TTL token，撤销窗口不变长，与 349a14e 加固基线兼容。链路：
//  1. requireAuth 已验证当前 token 签名与有效期（过期/伪造在此 401）；
//  2. RedClaw 模式下经 Me 复检撤销状态——已撤销/会话失效 fail-closed 401；
//     legacy 本地 token 无撤销表，跳过此步；
//  3. issueTokenAndRespond 重签（EnsureDefaultWorkspace + SignWithWorkspace +
//     RecordShadow），响应形状与 /api/auth/login 一致 {token,user,user_id,workspace_id}。
func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		writeError(w, http.StatusUnauthorized, "missing auth claims")
		return
	}
	uid := claims.UserID
	username := uid
	email := ""
	if s.redclawAdminClient != nil {
		raw := extractBearerToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		me, err := s.redclawAdminClient.Me(r.Context(), raw)
		if err != nil {
			s.handleRedClawAuthError(w, err, "redclaw me failed")
			return
		}
		if me != nil {
			if name := strings.TrimSpace(me.Name); name != "" {
				username = name
			}
			email = me.Email
		}
	}
	s.issueTokenAndRespond(w, r, uid, username, email)
}

// handleAuthSsoStatus — GET /api/auth/sso/status
//
// 无副作用的启用探测端点：LoginView 每次加载都探测一次，若走
// /api/auth/sso/login 会铸 nonce + 落 cookie（audit 修复项：探测应有
// 零副作用）。200 = 启用，404 = 未启用。
func (s *Server) handleAuthSsoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": true})
}

// handleAuthSsoLogin — GET /api/auth/sso/login
//
// 返回 { "url": "https://redclaw/.../api/v1/sso/login?external_state=...&redirect_url=..." }，
// 前端用 window.location 跳转。
//
// state 合约（2026-09-05 方案 A 双侧落地，见 docs/handoff/2026-09-05-sso-state-contract-mismatch.md）：
// RedClaw auth-agent /sso/login 接受 external_state 并存入 replay 表，
// /sso/callback 在响应中原样回显。pocket 的 CSRF 绑定因此是双层的：
//  1. 本 handler 生成 32 字节随机 nonce，写入待消费表并落 HttpOnly +
//     SameSite=Lax cookie（Path 收窄到 /api/auth/sso/），并以 external_state
//     参数传给 auth-agent；
//  2. /api/auth/sso/callback 消费该 cookie（防冷启动 / 重放回调），并把
//     auth-agent 回显的 external_state 与 nonce 做常量时间严格比对
//     （防 login-CSRF：失败 302 error=sso_state，fail-closed，无兼容开关
//     —— 需与 RedClaw auth-agent 同版本部署）。
//
// 兼容：旧前端带的 state query 参数不再使用，仅忽略。
func (s *Server) handleAuthSsoLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	nonce, err := s.ssoTxns.Issue(clientIP(r))
	if err != nil {
		log.Printf("WARN: sso login issue nonce: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start sso login")
		return
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
	redirectURL := r.URL.Query().Get("redirect_url")
	writeJSON(w, http.StatusOK, map[string]string{
		"url": s.redclawAdminClient.SsoLoginURL(nonce, redirectURL),
	})
}

// handleAuthSsoCallback — GET /api/auth/sso/callback
//
// IdP 完成认证后浏览器被带回本端点（OIDC redirect_uri 指向 pocket，
// 或 auth-agent 登录成功后重定向回 pocket）。流程：
//  1. 校验并单次消费 /api/auth/sso/login 落下的绑定 cookie（CSRF 绑定 1/2）；
//  2. 透传 code+state 给 auth-agent /sso/callback 换平台 JWT，并严格比对
//     其回显的 external_state == login nonce（CSRF 绑定 2/2，login-CSRF 根治）；
//  3. 签发一次性短时 code，302 到 SPA /#/auth/sso/callback?sso_code=...。
//     token 不再进 URL（P1-2：浏览器历史 / 访问日志泄露面），前端拿 code
//     POST /api/auth/sso/exchange 换登录结果。
func (s *Server) handleAuthSsoCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	// 1. 绑定 cookie：缺失 / 未知 / 已消费 / 已过期一律拒绝。
	ck, err := r.Cookie(ssoTxnCookie)
	if err != nil || !s.ssoTxns.Consume(ck.Value) {
		s.auditGateway(r, "auth.sso.callback", "-", "reason=invalid_binding_cookie", false)
		s.clearSsoTxnCookie(w, r)
		s.ssoRedirectError(w, r, "sso_session")
		return
	}
	nonce := ck.Value // 绑定 nonce，callback 结束前用于 external_state 端到端比对
	s.clearSsoTxnCookie(w, r)

	// 2. IdP 侧错误（如 casdoor 回传 error=...）原样转成 SPA 可展示的错误码。
	if e := r.URL.Query().Get("error"); e != "" {
		// IdP error 值用户可控，进日志/审计前必须清洗（防控制字符注入）。
		log.Printf("WARN: sso callback: IdP returned error=%s", sanitizeAuditDetail(e))
		s.auditGateway(r, "auth.sso.callback", "-", "reason=idp_error:"+sanitizeAuditDetail(e), false)
		s.ssoRedirectError(w, r, "sso_idp")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		s.auditGateway(r, "auth.sso.callback", "-", "reason=missing_code_or_state", false)
		s.ssoRedirectError(w, r, "sso_invalid")
		return
	}

	// 3. 透传给 auth-agent 换平台 JWT。state 是 auth-agent 自发并由 IdP
	//    带回的那份，由它的 replayGuard 单次校验；upstream 失败细节只进日志，
	//    不进重定向 URL。
	res, err := s.redclawAdminClient.SsoCallback(r.Context(), code, state)
	if err != nil {
		log.Printf("WARN: redclaw sso callback failed: %v", err)
		s.auditGateway(r, "auth.sso.callback", "-", "reason=upstream_rejected", false)
		s.ssoRedirectError(w, r, "sso_upstream")
		return
	}
	// 3b. 端到端 state 比对（login-CSRF 根治项）：auth-agent 必须原样回显
	//     login 时我方传入的 external_state。旧版 auth-agent 无回显（空串）
	//     或值不符一律 fail-closed——部署上要求 RedClaw auth-agent 同版本。
	if subtle.ConstantTimeCompare([]byte(res.ExternalState), []byte(nonce)) != 1 {
		log.Printf("WARN: sso callback external_state mismatch (len=%d)", len(res.ExternalState))
		s.auditGateway(r, "auth.sso.callback", "-", "reason=external_state_mismatch", false)
		s.ssoRedirectError(w, r, "sso_state")
		return
	}
	if res.Claims.Sub == "" {
		// 不允许空 user_id:所有 SSO 用户若共用一个 sentinel id 会互相冒充。
		//（SsoCallback 客户端已拒绝空 sub，这里是语义兜底。）
		log.Printf("WARN: redclaw sso callback returned empty subject")
		s.auditGateway(r, "auth.sso.callback", "-", "reason=empty_employee_id", false)
		s.ssoRedirectError(w, r, "sso_no_user")
		return
	}
	userID := res.Claims.Sub
	wsID := s.ensureWorkspaceForRedClawUser(r.Context(), userID)
	auth.RecordShadow("redclaw", userID, wsID, res.Claims.Name, res.Claims.Email)

	// 4. 签发一次性 code，302 到 SPA。
	ssoCode, err := s.ssoXchg.Put(ssoHandoff{
		Token:       res.JWT,
		User:        res.Claims.Name,
		UserID:      userID,
		WorkspaceID: wsID,
	})
	if err != nil {
		log.Printf("WARN: sso issue exchange code: %v", err)
		s.ssoRedirectError(w, r, "sso_upstream")
		return
	}
	s.auditGateway(r, "auth.sso.callback", userID, "sso login completed", true)
	http.Redirect(w, r, "/auth/sso/callback?"+url.Values{"sso_code": {ssoCode}}.Encode(), http.StatusFound)
}

// handleAuthSsoExchange — POST /api/auth/sso/exchange
//
// Body: {"code": "..."}。一次性 code（90s TTL、单次消费）换回登录结果，
// 响应形状与旧 302 query 载荷一致：{token, user, user_id, workspace_id}。
// P1-2 修复的一半：token 只出现在这个 POST 响应体里，不再进浏览器历史。
func (s *Server) handleAuthSsoExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	h, ok := s.ssoXchg.Take(body.Code)
	if !ok {
		s.auditGateway(r, "auth.sso.exchange", "-", "reason=invalid_or_expired_code", false)
		writeError(w, http.StatusUnauthorized, "invalid or expired sso code")
		return
	}
	s.auditGateway(r, "auth.sso.exchange", h.UserID, "sso token exchanged", true)
	writeJSON(w, http.StatusOK, h)
}

// ssoRedirectError 把失败重定向到 SPA 回调页（SsoCallbackView 展示错误码）。
// 只回传稳定错误码，不回传 upstream 细节；错误码文案映射见前端 SsoCallbackView。
func (s *Server) ssoRedirectError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/auth/sso/callback?"+url.Values{"error": {code}}.Encode(), http.StatusFound)
}

// clearSsoTxnCookie 清掉已消费的绑定 cookie（Path 必须与签发时一致）。
func (s *Server) clearSsoTxnCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     ssoTxnCookie,
		Value:    "",
		Path:     "/api/auth/sso/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}

// requestIsHTTPS 依据直连 TLS 或反代头判断是否走 HTTPS（决定 cookie Secure 位）。
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// extractBearerToken 复用 requireAuth 的 token 解析规则。
func extractBearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if r.URL.Path == "/ws" || r.URL.Path == "/plugin/ws" || strings.Contains(r.URL.Path, "/event") {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

// ============================================================================
// helpers
// ============================================================================

// issueTokenAndRespond 走与 handleAuthLogin 完全一致的 JWT 签发链路：
// EnsureDefaultWorkspace + SignWithWorkspace + RecordShadow
func (s *Server) issueTokenAndRespond(w http.ResponseWriter, r *http.Request, userID, username, email string) {
	wsID := "default"
	if s.identityStore != nil {
		ws, err := s.identityStore.EnsureDefaultWorkspace(r.Context(), userID)
		if err != nil {
			log.Printf("WARN: EnsureDefaultWorkspace for %s failed: %v (falling back to 'default')", userID, err)
		} else if ws != nil {
			wsID = ws.ID
		}
	}
	token, err := s.jwtSigner.SignWithWorkspace(userID, "user", wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign JWT")
		return
	}
	auth.RecordShadow("pocket", userID, wsID, username, email)
	writeJSON(w, http.StatusOK, map[string]string{
		"token":        token,
		"user":         username,
		"user_id":      userID,
		"workspace_id": wsID,
	})
}

// validateUsername 规则：3-32 字符，字母/数字/下划线/点/中划线
func validateUsername(name string) error {
	name = strings.TrimSpace(name)
	if n := len(name); n < 3 || n > 32 {
		return errors.New("用户名长度需在 3-32 之间")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '-':
		default:
			return errors.New("用户名仅支持字母、数字、下划线、点、中划线")
		}
	}
	return nil
}

// validatePassword 规则：≥8 字符，至少 1 数字 + 1 字母
func validatePassword(p string) error {
	if len(p) < 8 {
		return errors.New("密码至少 8 位")
	}
	hasDigit, hasLetter := false, false
	for _, r := range p {
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		}
	}
	if !hasDigit || !hasLetter {
		return errors.New("密码需包含字母和数字")
	}
	return nil
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func codeEmailContent(code string, ttlSec int, purpose auth.CodePurpose) (subject, body string) {
	ttlMin := ttlSec / 60
	if ttlMin < 1 {
		ttlMin = 1
	}
	subject = "【Redclaw】您的验证码"
	switch purpose {
	case auth.PurposeRegister:
		body = "欢迎注册 Redclaw！您的注册验证码是：" + code + "，" +
			"有效期 " + intStr(ttlMin) + " 分钟。如非本人操作，请忽略此邮件。"
	case auth.PurposeReset:
		body = "您正在重置 Redclaw 账户密码，验证码是：" + code + "，" +
			"有效期 " + intStr(ttlMin) + " 分钟。如非本人操作，请忽略并尽快修改密码。"
	case auth.PurposeLogin:
		body = "您正在使用邮箱验证码登录 Redclaw，验证码是：" + code + "，" +
			"有效期 " + intStr(ttlMin) + " 分钟。"
	default:
		body = "您的验证码是：" + code + "，有效期 " + intStr(ttlMin) + " 分钟。"
	}
	return
}

func intStr(n int) string {
	// 避免引入 strconv 仅为少量格式化
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// mirrorRegisterToRedClaw fail-soft：AuthClient 自身已 fail-soft（不返回 error），
// 此处再 defer recover 兜 panic，log 出错便于运维观测。
func (s *Server) mirrorRegisterToRedClaw(u *auth.User, _ string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("WARN: redclaw mirror panic: %v", rec)
		}
	}()
	if s.redclawAuthClient == nil || u == nil {
		return
	}
	s.redclawAuthClient.RegisterUser(context.Background(), redclaw.RegisterUserRequest{
		Email:     u.Email,
		Username:  u.Username,
		UserID:    u.ID,
		CreatedAt: time.Now().Unix(),
	})
}

// withTimeout 返回带超时的 ctx（bg context）。
func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
