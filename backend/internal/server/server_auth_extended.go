package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
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
// 设计要点：
//   - send-code 恒返回 200，不暴露邮箱是否注册（防枚举）
//   - forgot-password 成功后不直接返 token，强制重新走密码/验证码登录
//   - JWT 签发复用现有 jwtSigner + identityStore.EnsureDefaultWorkspace 链路
//   - RedClaw 镜像调用全部 fail-soft（defer recover + log.Warn，不影响主流程）

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

// handleAuthResetPassword — POST /api/auth/reset-password（已登录改密码，按 email 改）
//
// Body: {"email":"...", "old_password":"...", "new_password":"..."}
func (s *Server) handleAuthResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.userStore == nil {
		writeError(w, http.StatusServiceUnavailable, "reset-password not enabled")
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
	uid := s.userIDFromRequest(r)
	if uid == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// 用 email 找用户 + 校验旧密码 + 更新
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
