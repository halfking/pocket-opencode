package server

import (
	"context"
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

// handleAuthSsoLogin — GET /api/auth/sso/login
//
// 返回 { "url": "https://redclaw/.../api/v1/sso/login?state=..." }，前端用
// window.location 跳转。state 在前端生成、落到 sessionStorage，callback 时回传。
func (s *Server) handleAuthSsoLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing state")
		return
	}
	redirectURL := r.URL.Query().Get("redirect_url")
	writeJSON(w, http.StatusOK, map[string]string{
		"url": s.redclawAdminClient.SsoLoginURL(state, redirectURL),
	})
}

// handleAuthSsoCallback — GET /api/auth/sso/callback
//
// 浏览器从 RedClaw Auth Agent 跳回 openpocket（RedClaw 已完成 IdP token
// exchange 并铸造平台 JWT）。openpocket 拿到 RedClaw token 后 302 到 SPA
// 路径 /#/auth/sso/callback?token=...，由前端 SsoCallbackView 落 localStorage。
func (s *Server) handleAuthSsoCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		writeError(w, http.StatusNotFound, "sso not enabled")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code/state")
		return
	}
	res, err := s.redclawAdminClient.SsoCallback(r.Context(), code, state)
	if err != nil {
		s.handleRedClawAuthError(w, err, "redclaw sso callback failed")
		return
	}
	if res.Employee == nil || res.Employee.ID == "" {
		// 不允许空 user_id:所有 SSO 用户若共用一个 sentinel id 会互相冒充。
		log.Printf("WARN: redclaw sso callback returned empty employee id")
		writeError(w, http.StatusBadGateway, "redclaw sso returned no user id")
		return
	}
	userID := res.Employee.ID
	wsID := s.ensureWorkspaceForRedClawUser(r.Context(), userID)
	auth.RecordShadow("redclaw", userID, wsID, res.Employee.Name, res.Employee.Email)
	// 302 到 SPA 路径 + query 带 token。前端 SsoCallbackView 负责落 store。
	// 之所以用 query(不是 fragment):openpocket 用的是 vue-router history
	// 模式，fragment 不会被后端看到，但会被 vue-router 看到；为了简化，让
	// 后端用 query 注入 token，前端用 URLSearchParams 读取。
	// state 原样回传,SsoCallbackView 与 sessionStorage 中的值做 CSRF 比对。
	redirect := "/auth/sso/callback"
	q := url.Values{}
	q.Set("token", res.Token)
	q.Set("user", res.Employee.Name)
	q.Set("user_id", userID)
	q.Set("workspace_id", wsID)
	q.Set("state", state)
	http.Redirect(w, r, redirect+"?"+q.Encode(), http.StatusFound)
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
