package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
)

// handlePortalResourceToken —— POST /api/v1/portal/workspaces/{workspace}/resource-token
//
// 用登录 JWT 换发一个 ≤30s 的作用域资源凭证（rss / email-metadata / embedding）。
// 前端契约见 frontend/src/api/pocket-resource.ts（pocketResourceCredential）：
// 响应必须含 token / user_id / workspace_id / tenant_id / scopes（恰好 1 个）/
// issued_at / expires_at，且 expires_at - issued_at ≤ 30、expires_at > now，
// 否则前端把它判为 PocketResourceUnavailableError。响应字段与校验一一对应，
// 不要增删顶层字段（前端按白名单逐项校验，多出的字段无害但缺了会失败）。
//
// 为什么由 pocketd 自己实现（而不是独立 RedClaw portal 服务）：
// resolveRedclawPortalBase() 的降级链末端是 resolveRuntimeApiBase() —— 即
// pocketd 同源。生产部署没有独立 portal 服务时，这个端点就是交换的落点；
// 没有它，RSS / 邮件元数据 / embedding 三类请求全部 fail-closed
// （实测 2026-11-12：交换 404 → 前端报 pocket_resource_authority_rejected）。
//
// 安全面（与 auth.ResourceScope / tryResourceAuth 的既有约束一致）：
//   - 换发主体必须与登录 token 同一 user + 同一 workspace（路径里的 workspace
//     不匹配一律 403，防止拿 A 工作区的会话给 B 工作区铸凭证）；
//   - scopes 走 CanonicalResourceScopes 白名单，且前端只接受恰好 1 个；
//   - 有效期钳制在 auth.ResourceMaxTTL（30s）与登录 token 剩余寿命的较小者；
//   - 路由本身在 requireAuth 之后，resource token 不允许换发 resource token
//     （tryResourceAuth 对本路径不在 ResourceScope 白名单内 → 403）。
func (s *Server) handlePortalResourceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 精确段解析：api/v1/portal/workspaces/{ws}/resource-token
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 6 || parts[5] != "resource-token" {
		writeError(w, http.StatusNotFound, "unknown portal resource route")
		return
	}
	pathWorkspace := parts[4]
	if s.jwtSigner == nil {
		writeError(w, http.StatusServiceUnavailable, "JWT signer not configured")
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	workspace := claims.WorkspaceID
	if workspace == "" {
		workspace = "default" // 与 workspaceIDFromRequest 同口径
	}
	if pathWorkspace != workspace {
		writeError(w, http.StatusForbidden, "workspace mismatch")
		return
	}

	// body 限制 1MB；scopes 恰好 1 个（前端契约）。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	scopes, err := auth.CanonicalResourceScopes(req.Scopes)
	if err != nil || len(scopes) != 1 {
		writeError(w, http.StatusBadRequest, "exactly one valid scope required (rss | email-metadata | embedding)")
		return
	}

	// 取登录 token 的真实剩余寿命作为 parent 边界：resource token 的有效期
	// 不应超过签发它的会话。IdentityVerifierResult 不透出 jti，ParentSessionID
	// 用 user id 兜底，只满足 ResourceClaims 的 canonical 非空要求。
	now := time.Now()
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	parent, err := auth.VerifyToken(s.jwtSigner, raw)
	if err != nil || parent == nil || parent.ExpiresAt.IsZero() || !parent.ExpiresAt.After(now) {
		writeError(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}
	parentSession := parent.UserID
	tenant := claims.TenantID
	if tenant == "" {
		tenant = "default"
	}

	out, signed, err := s.jwtSigner.SignResource(auth.ResourceClaims{
		TenantID:        tenant,
		WorkspaceID:     workspace,
		ParentSessionID: parentSession,
		ParentExpiresAt: parent.ExpiresAt.Unix(),
		Subject:         claims.UserID,
		Scopes:          scopes,
	}, now.Add(auth.ResourceMaxTTL), now)
	if err != nil {
		writeError(w, http.StatusForbidden, "resource token exchange rejected: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        out,
		"user_id":      claims.UserID,
		"workspace_id": workspace,
		"tenant_id":    tenant,
		"scopes":       []string{scopes[0]},
		"issued_at":    now.Unix(),
		"expires_at":   signed.ExpiresAt.Unix(),
	})
}
