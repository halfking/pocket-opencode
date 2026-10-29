package server

// 工作区成员绑定断言的 HTTP 面（2026-10-29）。
//
// ⚠ 这一层补的是「服务间可信验证」的**缺块**：
//
//	改动前：「调用方属于哪个工作区」只有两种来源 ——
//	  (a) 客户端自报（登录响应体 / localStorage）；
//	  (b) openpocket 从 JWT claim 里读出的 workspace_id。
//	两者都不是**签名的事实**：JWT 里的 workspace_id 是 openpocket 自己签的，
//	但没有任何东西把「这个用户确实是这个工作区的成员」绑定进签名 ⇒
//	任何能改动自己令牌载荷的一方（或能改动工作区声明的一方）
//	都能让服务端认领任意工作区。
//
//	改动后：工作区权威（openpocket）签发**独立的成员绑定断言**，
//	断言覆盖用户、租户、工作区、角色、签发方、受众、有效期，
//	并由验证方用共享密钥验签。客户端自报值从此**不是**授权依据。
//
// 📌 端点：
//
//	GET /api/workspaces/{id}/binding   签发当前用户的成员绑定断言
//	                                （要求调用者确为该工作区成员）
//
//	POST /api/auth/workspace-binding   校验一个断言并回报它描述的范围
//	                                （供服务间调用方自检 / 调试）

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
)

// handleWorkspaceBinding — GET /api/workspaces/{id}/binding
//
// ⚠ **签发前必须查库确认成员资格当下成立**（store.IssueBinding 做的）。
//
//	查库与签发分开：撤销后的成员拿不到新断言，旧断言在自己的 TTL 内失效。
//
// ⚠ 共享密钥未配置 ⇒ 503（不是 200 带空断言）。
//
//	「签不出来」必须表现为**服务不可用**，不能表现为「没有绑定」
//	—— 后者会被调用方误解成「不在这个工作区」。
func (s *Server) handleWorkspaceBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.identityStore == nil {
		writeError(w, http.StatusServiceUnavailable, "identity core not configured")
		return
	}
	signer := identity.BindingSignerFromEnv()
	if signer == nil {
		// ⚠ fail-closed：无法签发 ⇒ 无法证明成员资格 ⇒ 不给断言。
		writeError(w, http.StatusServiceUnavailable, "workspace binding is not configured")
		return
	}
	wsID := workspaceIDFromPath(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace id required")
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		writeError(w, http.StatusUnauthorized, "missing auth claims")
		return
	}
	ttl := s.workspaceBindingTTL()
	signed, assertion, err := s.identityStore.IssueBinding(
		r.Context(), signer, wsID, claims.UserID, s.redclawTenantID(claims), ttl,
	)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrBindingExpired):
			writeError(w, http.StatusForbidden, "workspace membership has expired")
		case errors.Is(err, identity.ErrBindingMismatch):
			writeError(w, http.StatusForbidden, "not a member of this workspace")
		default:
			writeError(w, http.StatusInternalServerError, "issue workspace binding: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"binding":      signed,
		"user_id":      assertion.UserID,
		"tenant_id":    assertion.TenantID,
		"workspace_id": assertion.WorkspaceID,
		"role":         assertion.Role,
		"issuer":       assertion.Issuer,
		"audience":     assertion.Audience,
		"issued_at":    assertion.IssuedAt,
		"expires_at":   assertion.ExpiresAt,
	})
}

// handleAuthWorkspaceBinding — POST /api/auth/workspace-binding
//
// 请求体：{"binding": "<assertion>", "user_id": "...", "workspace_id": "..."}
// 作用：**验证**一个断言是否描述了给定的用户/工作区。
//
// ⚠ 为什么需要「验证」端点（而不是只在签发端点自证）：
//
//	调用方（RedClaw 或任何服务间调用方）需要的是一个**独立**的裁决点：
//	「这个断言是真的吗？它说的是这个人这个工作区吗？」
//	把裁决放在权威侧，避免每个调用方各自实现一遍验签（各自实现 =
//	各自可能实现错）。
func (s *Server) handleAuthWorkspaceBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	signer := identity.BindingSignerFromEnv()
	if signer == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace binding is not configured")
		return
	}
	var body struct {
		Binding     string `json:"binding"`
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(body.Binding) == "" {
		writeError(w, http.StatusBadRequest, "binding is required")
		return
	}
	assertion, err := signer.Verify(body.Binding)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrBindingExpired):
			writeError(w, http.StatusUnauthorized, "workspace binding expired")
		default:
			writeError(w, http.StatusUnauthorized, "workspace binding invalid")
		}
		return
	}
	// ⚠ 显式要求 user/workspace 时才做范围匹配；缺省时只验签。
	if strings.TrimSpace(body.UserID) != "" || strings.TrimSpace(body.WorkspaceID) != "" {
		if !assertion.Covers(body.UserID, body.WorkspaceID) {
			writeError(w, http.StatusForbidden, "workspace binding does not match the requested scope")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":        true,
		"user_id":      assertion.UserID,
		"tenant_id":    assertion.TenantID,
		"workspace_id": assertion.WorkspaceID,
		"role":         assertion.Role,
		"issuer":       assertion.Issuer,
		"audience":     assertion.Audience,
		"issued_at":    assertion.IssuedAt,
		"expires_at":   assertion.ExpiresAt,
	})
}

// workspaceIDFromPath extracts the workspace id from
// /api/workspaces/{id}/... without consuming the request body.
func workspaceIDFromPath(r *http.Request) string {
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part != "" && part != "binding" {
			return strings.TrimSpace(part)
		}
	}
	return ""
}

// workspaceBindingTTL is how long a minted assertion stays valid.
//
// ⚠ 上界有界：成员资格**可被撤销**，永久断言等于把撤销能力从权威手里拿走。
func (s *Server) workspaceBindingTTL() time.Duration {
	return identity.DefaultBindingTTL
}
