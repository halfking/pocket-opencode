package server

// server_identity.go — S0-A Identity Core 的 HTTP handler。
//
// 路由：
//   GET    /api/workspaces                  列出我加入的所有 workspace
//   POST   /api/workspaces                  创建新 workspace（仅 owner，一般由 EnsureDefault 自动建）
//   GET    /api/workspaces/{id}             workspace 详情
//   GET    /api/workspaces/{id}/members     成员列表
//   POST   /api/workspaces/{id}/members     邀请成员（受 MaxInvitees 限制）
//   DELETE /api/workspaces/{id}/members/{userID}  撤销成员（owner 不可移除）
//   GET    /api/workspaces/{id}/devices     设备列表
//   POST   /api/workspaces/{id}/devices     注册/刷新设备
//   DELETE /api/workspaces/{id}/devices/{deviceID}  注销设备
//
// 身份边界：所有写操作都要求 JWT 中的 user 是该 workspace 的 member。
// owner 才能邀请/移除成员、注销他人设备。
//
// 降级：identityStore 为 nil 时返回 503，提示 S0-A 未启用。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
)

// workspaceIDFromRequest 从 JWT claim 取 workspace_id；为空时回退 "default"
// （兼容 S0 之前的数据）。
func (s *Server) workspaceIDFromRequest(r *http.Request) string {
	if c := s.claimsFromRequest(r); c != nil && c.WorkspaceID != "" {
		return c.WorkspaceID
	}
	return "default"
}

// claimsFromRequest 解析 Authorization: Bearer JWT，失败返回 nil。
// 与 userIDFromRequest 同源，但返回完整 claims 供 workspace 边界判断。
func (s *Server) claimsFromRequest(r *http.Request) *authClaims {
	if s.jwtSigner == nil {
		return nil
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil
	}
	token := strings.TrimSpace(authHeader[len("Bearer "):])
	claims, err := s.jwtSigner.Parse(token)
	if err != nil {
		return nil
	}
	return &authClaims{UserID: claims.UserID, Role: claims.Role, WorkspaceID: claims.WorkspaceID}
}

// handleWorkspaces 处理 GET/POST /api/workspaces
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if s.identityStore == nil {
		writeError(w, http.StatusServiceUnavailable, "identity core not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listMyWorkspaces(w, r)
	case http.MethodPost:
		s.createWorkspace(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) listMyWorkspaces(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	wss, err := s.identityStore.ListWorkspacesForUser(r.Context(), uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workspaces: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": wss})
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"` // "default" | "shadow"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ID == "" {
		body.ID = "ws_" + uid + "_" + strings.ToLower(strings.ReplaceAll(body.Name, " ", "_"))
	}
	if body.Name == "" {
		body.Name = "新工作空间"
	}
	if body.Type == "" {
		body.Type = "shadow"
	}
	ws := &identity.Workspace{ID: body.ID, OwnerID: uid, Name: body.Name, Type: body.Type}
	if err := s.identityStore.CreateDefaultWorkspace(r.Context(), ws); err != nil {
		writeError(w, http.StatusInternalServerError, "create workspace: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// handleWorkspaceOps 处理 /api/workspaces/{id} 子树。
//
// 路径切分：/api/workspaces/{id}[/members|/members/{uid}|/devices|/devices/{did}]
func (s *Server) handleWorkspaceOps(w http.ResponseWriter, r *http.Request) {
	if s.identityStore == nil {
		writeError(w, http.StatusServiceUnavailable, "identity core not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "workspace id required")
		return
	}
	wsID := parts[0]

	// 权限：必须是该 workspace 成员才能继续
	if !s.isMember(r, wsID) {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// /api/workspaces/{id}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		ws, err := s.identityStore.GetWorkspace(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ws)
		return
	}

	sub := parts[1]
	switch sub {
	case "members":
		s.handleMembers(w, r, wsID, parts[2:])
	case "devices":
		s.handleDevices(w, r, wsID, parts[2:])
	default:
		writeError(w, http.StatusNotFound, "unknown subpath: "+sub)
	}
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		members, err := s.identityStore.ListMembers(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": members})

	case len(rest) == 0 && r.Method == http.MethodPost:
		// 邀请：仅 owner
		if !s.isOwner(r, wsID) {
			writeError(w, http.StatusForbidden, "only owner can invite")
			return
		}
		var body struct {
			UserID    string `json:"user_id"`
			ExpiresIn int64  `json:"expires_in_seconds"` // 0 = 永不过期
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id required")
			return
		}
		m := &identity.Member{
			WorkspaceID: wsID,
			UserID:      body.UserID,
			Role:        identity.RoleInvitee,
		}
		if body.ExpiresIn > 0 {
			m.ExpiresAt = time.Now().Unix() + body.ExpiresIn
		}
		if err := s.identityStore.AddMember(r.Context(), m); err != nil {
			if errors.Is(err, identity.ErrInviteeLimit) {
				writeError(w, http.StatusConflict, "invitee limit reached (max 3)")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, m)

	case len(rest) == 1 && r.Method == http.MethodDelete:
		// 撤销成员：仅 owner，且不能撤销自己（owner）
		if !s.isOwner(r, wsID) {
			writeError(w, http.StatusForbidden, "only owner can remove members")
			return
		}
		target := rest[0]
		if err := s.identityStore.RemoveMember(r.Context(), wsID, target); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"removed": target})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		devs, err := s.identityStore.ListDevices(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devs})

	case len(rest) == 0 && r.Method == http.MethodPost:
		// 任何成员可注册自己的设备
		var body struct {
			ID          string `json:"id"`
			Fingerprint string `json:"fingerprint"`
			PushToken   string `json:"push_token"`
			OS          string `json:"os"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			writeError(w, http.StatusBadRequest, "id required")
			return
		}
		d := &identity.Device{
			ID:          body.ID,
			UserID:      s.userIDFromRequest(r),
			WorkspaceID: wsID,
			Fingerprint: body.Fingerprint,
			PushToken:   body.PushToken,
			OS:          body.OS,
		}
		if err := s.identityStore.UpsertDevice(r.Context(), d); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, d)

	case len(rest) == 1 && r.Method == http.MethodDelete:
		// 注销设备：owner 可注销任何人设备；其他成员只能注销自己的
		target := rest[0]
		if !s.isOwner(r, wsID) {
			// 非_owner：检查设备归属。简化处理：从列表里找。
			devs, err := s.identityStore.ListDevices(r.Context(), wsID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			owned := false
			uid := s.userIDFromRequest(r)
			for _, d := range devs {
				if d.ID == target && d.UserID == uid {
					owned = true
					break
				}
			}
			if !owned {
				writeError(w, http.StatusForbidden, "can only revoke your own device")
				return
			}
		}
		if err := s.identityStore.DeleteDevice(r.Context(), target); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"revoked": target})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// isMember 判断当前请求用户是否为该 workspace 的成员。
func (s *Server) isMember(r *http.Request, wsID string) bool {
	uid := s.userIDFromRequest(r)
	m, err := s.identityStore.GetMember(r.Context(), wsID, uid)
	return err == nil && m != nil
}

// isOwner 判断当前请求用户是否为该 workspace 的 owner。
func (s *Server) isOwner(r *http.Request, wsID string) bool {
	uid := s.userIDFromRequest(r)
	m, err := s.identityStore.GetMember(r.Context(), wsID, uid)
	return err == nil && m != nil && m.Role == identity.RoleOwner
}
