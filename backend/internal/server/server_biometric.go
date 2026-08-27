package server

// server_biometric.go — 生物识别（WebAuthn 风格）注册/登录端点。
//
// 端点（与 server.go 路由表对应）：
//   POST /api/auth/biometric/register/begin    — 生成 challenge
//   POST /api/auth/biometric/register/finish   — 提交 credential_public_key，存库
//   POST /api/auth/biometric/login/begin       — 生成 challenge（无需 requireAuth）
//   POST /api/auth/biometric/login/finish      — 提交 assertion，校验签名（stub）
//   GET  /api/auth/biometric/credentials       — 列出当前用户凭证
//   ANY  /api/auth/biometric/credentials/{id}  — 单条 rename/delete
//
// 实现状态：本文件为最小可编译骨架。register/login 的 challenge↔session
// 绑定 + COSE 验签逻辑交给后续 sprint（依赖 identity-go webauthn helper）；
// storage 层（auth.BiometricStore）已就绪，handler 只承担：
//   - 入参校验
//   - 调用 store 持久化
//   - 返回标准结构化错误 / 占位 501（等待签名验证模块）
//
// 安全不变量：
//   - 任何 mutation 必须要求认证（requireAuth），login/begin 除外；
//   - 删除/重命名操作校验 ownership（credential.user_id == 当前 user）；
//   - 所有路径写 audit（writeAudit），便于后续合规回溯。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
)

// challengeTTL = 5 分钟（与 WebAuthn spec 一致）。
const challengeTTLSeconds = 300

type biometricChallenge struct {
	Challenge  string `json:"challenge"`
	ExpiresAt  int64  `json:"expires_at"`
	UserHandle string `json:"user_handle,omitempty"`
}

type biometricRegisterBeginRequest struct {
	DeviceName string `json:"device_name"`
}

// handleBiometricRegisterBegin 生成注册 challenge。
// POST /api/auth/biometric/register/begin
func (s *Server) handleBiometricRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body biometricRegisterBeginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = biometricRegisterBeginRequest{}
	}
	if strings.TrimSpace(body.DeviceName) == "" {
		body.DeviceName = "未命名设备"
	}

	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	challenge, err := auth.NewChallengeID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "challenge 生成失败")
		return
	}

	writeJSON(w, http.StatusOK, biometricChallenge{
		Challenge:  challenge,
		ExpiresAt:  time.Now().Unix() + challengeTTLSeconds,
		UserHandle: userID + ":" + workspaceID,
	})
}

// handleBiometricRegisterFinish 校验 attestation，存库。
// POST /api/auth/biometric/register/finish
func (s *Server) handleBiometricRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.biometricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "biometric store not configured")
		return
	}
	var body struct {
		CredentialID string `json:"credential_id"`
		PublicKey    string `json:"public_key"` // base64url（COSE）
		DeviceName   string `json:"device_name"`
		Transports   string `json:"transports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.CredentialID == "" || body.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "credential_id + public_key required")
		return
	}
	// base64url 解码合法性检查（避免存储任意字节）。签名验证留给 webauthn helper。
	if _, err := base64urlDecode(body.CredentialID); err != nil {
		writeError(w, http.StatusBadRequest, "credential_id 必须为 base64url 编码")
		return
	}
	pubKey, err := base64urlDecode(body.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "public_key 必须为 base64url 编码")
		return
	}

	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	cred := &auth.BiometricCredential{
		ID:          body.CredentialID,
		UserID:      userID,
		WorkspaceID: workspaceID,
		DeviceName:  body.DeviceName,
		PublicKey:   pubKey,
		Transports:  body.Transports,
	}
	if err := s.biometricStore.Register(r.Context(), cred); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditGateway(r, "biometric.register", cred.ID,
		fmt.Sprintf("user=%s ws=%s device=%s", userID, workspaceID, cred.DeviceName), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": cred.ID})
}

// handleBiometricLoginBegin 生成登录 challenge。
// POST /api/auth/biometric/login/begin （无需 requireAuth）
func (s *Server) handleBiometricLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	challenge, err := auth.NewChallengeID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "challenge 生成失败")
		return
	}
	writeJSON(w, http.StatusOK, biometricChallenge{
		Challenge: challenge,
		ExpiresAt: time.Now().Unix() + challengeTTLSeconds,
	})
}

// handleBiometricLoginFinish 校验 assertion 并签发 token（stub）。
// POST /api/auth/biometric/login/finish （无需 requireAuth）
func (s *Server) handleBiometricLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 等 webauthn helper 合入后启用；当前先返回 501 让客户端有明确反馈。
	writeError(w, http.StatusNotImplemented,
		"biometric login verify 待 webauthn helper 合入（P2 sprint）")
}

// handleBiometricCredentials 列出当前用户在当前 workspace 的所有凭证。
// GET /api/auth/biometric/credentials
func (s *Server) handleBiometricCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.biometricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "biometric store not configured")
		return
	}
	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	creds, err := s.biometricStore.ListByUser(r.Context(), userID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if creds == nil {
		creds = []*auth.BiometricCredential{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

// handleBiometricCredentialOps 单条凭证的 rename / delete。
// PATCH /api/auth/biometric/credentials/{id}    — rename
// DELETE /api/auth/biometric/credentials/{id}  — delete
func (s *Server) handleBiometricCredentialOps(w http.ResponseWriter, r *http.Request) {
	if s.biometricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "biometric store not configured")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/auth/biometric/credentials/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "credential id missing")
		return
	}

	cred, err := s.biometricStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if cred.UserID != s.userIDFromRequest(r) {
		writeError(w, http.StatusForbidden, "not your credential")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var body struct {
			DeviceName string `json:"device_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		ok, err := s.biometricStore.Rename(r.Context(), id, body.DeviceName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		s.auditGateway(r, "biometric.rename", id, "ok", true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		ok, err := s.biometricStore.Delete(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		s.auditGateway(r, "biometric.delete", id, "ok", true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// base64urlDecode 容忍无 padding 的 URL-safe base64（WebAuthn 标准）。
func base64urlDecode(s string) ([]byte, error) {
	fixed := strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(fixed) % 4; pad != 0 {
		fixed += strings.Repeat("=", 4-pad)
	}
	return base64StdDecode(fixed)
}
