package server

// server_biometric.go — 生物识别（WebAuthn 风格）注册/登录端点。
//
// 端点（与 server.go 路由表对应）：
//   POST /api/auth/biometric/register/begin    — 生成 challenge
//   POST /api/auth/biometric/register/finish   — 提交 credential，存库
//   POST /api/auth/biometric/login/begin       — 生成 challenge（无需 requireAuth）
//   POST /api/auth/biometric/login/finish      — 提交 assertion，校验签名，签发 JWT
//   GET  /api/auth/biometric/credentials       — 列出当前用户凭证
//   ANY  /api/auth/biometric/credentials/{id}  — 单条 rename/delete
//
// 实现状态（2026-08-28）：
//   - webAuthnVerifier != nil：完整 WebAuthn 签名验证流程
//   - webAuthnVerifier == nil：P0 stub（register 只存储公钥，login 返回 501）
//
// 安全不变量：
//   - 任何 mutation 必须要求认证（requireAuth），login/begin 除外；
//   - 删除/重命名操作校验 ownership（credential.user_id == 当前 user）；
//   - 所有路径写 audit（auditGateway），便于后续合规回溯。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
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

	// 路径 1：webAuthnVerifier 可用 → 完整 WebAuthn 流程
	if s.webAuthnVerifier != nil {
		challengeB64, creationOpts, err := s.webAuthnVerifier.BeginRegistration(r.Context(), userID, body.DeviceName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("begin registration failed: %v", err))
			return
		}
		// 返回完整的 CredentialCreation options（客户端用于 navigator.credentials.create）
		writeJSON(w, http.StatusOK, map[string]any{
			"challenge":        challengeB64,
			"expires_at":       time.Now().Unix() + challengeTTLSeconds,
			"user_handle":      userID + ":" + workspaceID,
			"creation_options": creationOpts,
		})
		return
	}

	// 路径 2：降级到 P0 stub（仅生成 challenge，不做后续验证）
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

	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)

	// 路径 1：webAuthnVerifier 可用 → 完整验证 attestation
	if s.webAuthnVerifier != nil {
		var body struct {
			Challenge      string          `json:"challenge"`       // begin 返回的 challenge
			DeviceName     string          `json:"device_name"`
			AttestationRaw json.RawMessage `json:"attestation_raw"` // 客户端提交的完整 attestation response
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.Challenge == "" || len(body.AttestationRaw) == 0 {
			writeError(w, http.StatusBadRequest, "challenge + attestation_raw required")
			return
		}

		// 解析 attestation response
		ccr, err := protocol.ParseCredentialCreationResponseBody(strings.NewReader(string(body.AttestationRaw)))
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse attestation: %v", err))
			return
		}

		// 验证签名并提取 credentialID / publicKey / counter
		credID, publicKey, counter, err := s.webAuthnVerifier.FinishRegistration(r.Context(), body.Challenge, ccr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Sprintf("attestation verification failed: %v", err))
			return
		}

		// 存储到 BiometricStore
		cred := &auth.BiometricCredential{
			ID:          credID,
			UserID:      userID,
			WorkspaceID: workspaceID,
			DeviceName:  body.DeviceName,
			PublicKey:   publicKey,
			Counter:     counter,
			Transports:  "[]", // TODO: 从 ccr 提取 transports
		}
		if err := s.biometricStore.Register(r.Context(), cred); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditGateway(r, "biometric.register", cred.ID,
			fmt.Sprintf("user=%s ws=%s device=%s verified=true", userID, workspaceID, cred.DeviceName), true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": cred.ID})
		return
	}

	// 路径 2：降级到 P0 stub（不验证签名，仅存储公钥）
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
	// base64url 解码合法性检查（避免存储任意字节）
	if _, err := base64urlDecode(body.CredentialID); err != nil {
		writeError(w, http.StatusBadRequest, "credential_id 必须为 base64url 编码")
		return
	}
	pubKey, err := base64urlDecode(body.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "public_key 必须为 base64url 编码")
		return
	}

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
		fmt.Sprintf("user=%s ws=%s device=%s verified=false", userID, workspaceID, cred.DeviceName), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": cred.ID})
}

// handleBiometricLoginBegin 生成登录 challenge。
// POST /api/auth/biometric/login/begin （无需 requireAuth）
func (s *Server) handleBiometricLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 路径 1：webAuthnVerifier 可用 → 完整 WebAuthn 流程
	if s.webAuthnVerifier != nil {
		challengeB64, assertionOpts, err := s.webAuthnVerifier.BeginLogin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("begin login failed: %v", err))
			return
		}
		// 返回完整的 CredentialAssertion options（客户端用于 navigator.credentials.get）
		writeJSON(w, http.StatusOK, map[string]any{
			"challenge":         challengeB64,
			"expires_at":        time.Now().Unix() + challengeTTLSeconds,
			"assertion_options": assertionOpts,
		})
		return
	}

	// 路径 2：降级到 P0 stub
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

// handleBiometricLoginFinish 校验 assertion 并签发 token。
// POST /api/auth/biometric/login/finish （无需 requireAuth）
func (s *Server) handleBiometricLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.biometricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "biometric store not configured")
		return
	}
	if s.jwtSigner == nil {
		writeError(w, http.StatusServiceUnavailable, "JWT signer not configured")
		return
	}

	// 路径 1：webAuthnVerifier 可用 → 完整验证 assertion
	if s.webAuthnVerifier != nil {
		var body struct {
			Challenge     string          `json:"challenge"`      // begin 返回的 challenge
			CredentialID  string          `json:"credential_id"`  // base64url
			AssertionRaw  json.RawMessage `json:"assertion_raw"`  // 客户端提交的完整 assertion response
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.Challenge == "" || body.CredentialID == "" || len(body.AssertionRaw) == 0 {
			writeError(w, http.StatusBadRequest, "challenge + credential_id + assertion_raw required")
			return
		}

		// 从 BiometricStore 查出公钥 + counter
		storedCred, err := s.biometricStore.Get(r.Context(), body.CredentialID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "credential not found")
			return
		}

		// 解析 assertion response（通过 parseAssertionFn 可被测试替换）
		parser := s.parseAssertionFn
		if parser == nil {
			parser = defaultAssertionParser
		}
		par, err := parser(body.AssertionRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse assertion: %v", err))
			return
		}

		// 验证签名 + counter 单调性
		newCounter, err := s.webAuthnVerifier.FinishLogin(r.Context(), body.Challenge, body.CredentialID, storedCred.PublicKey, storedCred.Counter, par)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Sprintf("assertion verification failed: %v", err))
			return
		}



		// 更新 counter + last_used_at
		if err := s.biometricStore.Touch(r.Context(), body.CredentialID, newCounter); err != nil {
			// counter 更新失败不阻断登录，记录日志即可
			fmt.Printf("WARN: failed to update counter for %s: %v\n", body.CredentialID, err)
		}

		// RedClaw 用户验证（如果 RedClaw 已配置）
		var role string = "user"
		if s.redclawBridge != nil {
			verifyResp, err := s.redclawBridge.VerifyUser(storedCred.UserID)
			if err != nil {
				// RedClaw 不可用或用户无效 → 拒绝登录
				writeError(w, http.StatusUnauthorized, fmt.Sprintf("user verification failed: %v", err))
				s.auditGateway(r, "biometric.login.failed", storedCred.ID,
					fmt.Sprintf("user=%s redclaw_error=%v", storedCred.UserID, err), false)
				return
			}
			if !verifyResp.Valid {
				writeError(w, http.StatusUnauthorized, "user not found or disabled in RedClaw")
				s.auditGateway(r, "biometric.login.failed", storedCred.ID,
					fmt.Sprintf("user=%s reason=invalid", storedCred.UserID), false)
				return
			}
			// 使用 RedClaw 返回的角色信息
			if verifyResp.UserInfo != nil && len(verifyResp.UserInfo.Roles) > 0 {
				role = verifyResp.UserInfo.Roles[0] // 取第一个角色
			}
		}

		// 签发 JWT token
		token, err := s.jwtSigner.SignWithWorkspace(storedCred.UserID, role, storedCred.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to sign JWT")
			return
		}

		s.auditGateway(r, "biometric.login", storedCred.ID,
			fmt.Sprintf("user=%s ws=%s device=%s verified_by=%s", storedCred.UserID, storedCred.WorkspaceID, storedCred.DeviceName, 
				map[bool]string{true: "redclaw", false: "local"}[s.redclawBridge != nil]), true)
		writeJSON(w, http.StatusOK, map[string]any{
			"token":        token,
			"user_id":      storedCred.UserID,
			"workspace_id": storedCred.WorkspaceID,
		})
		return
	}

	// 路径 2：降级到 P0 stub（未实现）
	writeError(w, http.StatusNotImplemented,
		"biometric login verify 待 webAuthnVerifier 配置（P1 sprint）")
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

// assertionParser 把 WebAuthn assertion 解析逻辑抽象为可注入的函数类型。
//
// 生产环境用 defaultAssertionParser；测试可以替换为返回 mock 数据，
// 避开构造合法 CBOR-encoded authenticatorData 的复杂度。
// 这是标准的"测试 seam"模式，不是生产代码妥协。
type assertionParser func(json.RawMessage) (*protocol.ParsedCredentialAssertionData, error)

func defaultAssertionParser(raw json.RawMessage) (*protocol.ParsedCredentialAssertionData, error) {
	return protocol.ParseCredentialRequestResponseBody(strings.NewReader(string(raw)))
}
