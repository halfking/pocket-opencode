package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/kaixuan/identity-go/token"
)

// Dedicated protocol boundary: this token is never accepted by requireAuth or
// exposed to ordinary API handlers, and contains no membership authorization.
func (s *Server) handleWorkspaceAuthority(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID := strings.TrimSpace(s.cfg.RedClawTenantID)
	secret, err := token.LoadSharedSecret("IDENTITY_SHARED_SECRET")
	if err != nil || tenantID == "" || s.identityStore == nil {
		writeError(w, 503, "workspace authority delegation is not configured")
		return
	}
	const prefix = "/internal/workspace-authority/workspaces"
	path := r.URL.Path
	var workspaceID string
	if path != prefix {
		if !strings.HasPrefix(path, prefix+"/") || !strings.HasSuffix(path, "/binding") {
			writeError(w, 404, "unknown workspace authority route")
			return
		}
		workspaceID = strings.TrimSuffix(strings.TrimPrefix(path, prefix+"/"), "/binding")
		if workspaceID == "" || workspaceID != strings.TrimSpace(workspaceID) || strings.ContainsAny(workspaceID, "/\\%?#") || workspaceID == "." || workspaceID == ".." {
			writeError(w, 400, "invalid workspace id")
			return
		}
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "workspace delegation does not accept query parameters")
		return
	}
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	claims, err := identity.VerifyDelegation(secret, raw, tenantID, r.Method, path, time.Now())
	if err != nil {
		writeError(w, 401, "invalid or expired workspace delegation")
		return
	}
	if err = s.identityStore.ConsumeDelegation(r.Context(), claims, time.Now()); err != nil {
		if errors.Is(err, identity.ErrDelegationReplay) {
			writeError(w, 401, "workspace delegation already used")
		} else {
			writeError(w, 503, "workspace delegation replay protection unavailable")
		}
		return
	}
	ctx := context.WithValue(r.Context(), authClaimsContextKey{}, &authClaims{UserID: claims.Subject, TenantID: tenantID, WorkspaceID: workspaceID})
	r = r.Clone(ctx)
	if workspaceID == "" {
		workspaces, err := s.identityStore.ListActiveWorkspacesForUser(r.Context(), claims.Subject)
		if err != nil {
			writeError(w, 503, "workspace membership lookup unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"workspaces": workspaces})
		return
	}
	r.URL.Path = "/api/workspaces/" + workspaceID + "/binding"
	r.URL.RawPath = ""
	s.handleWorkspaceBinding(w, r)
}
