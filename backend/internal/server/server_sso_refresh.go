package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

const redClawOIDCMethod = "redclaw-oidc"

// handleAuthSsoRefresh admits a bearer only for renewal. Admin verifies the
// employee, the Auth Agent owns the parent session, and Pocketd owns membership.
func (s *Server) handleAuthSsoRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "", "POST only")
		return
	}
	if s.redclawAdminClient == nil || !s.cfg.RedClawSsoEnabled {
		s.writeStructuredError(w, r, http.StatusNotFound, "", "RedClaw SSO is not enabled")
		return
	}
	bearer := extractBearerToken(r)
	if bearer == "" {
		s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated, "missing authorization token")
		return
	}
	if s.identityStore == nil || s.cfg.RedClawTenantID == "" {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "workspace authority unavailable")
		return
	}
	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	body := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := body.Decode(&input); err != nil || body.Decode(new(json.RawMessage)) != io.EOF || !validRenewalWorkspace(input.WorkspaceID) {
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "a valid workspace_id is required")
		return
	}
	old := s.ssoRefreshProfile(w, r, bearer, input.WorkspaceID, "")
	if old == nil {
		return
	}
	newBearer, err := s.redclawAdminClient.SsoRefresh(r.Context(), bearer)
	switch {
	case errors.Is(err, redclaw.ErrInvalidCredentials):
		s.writeStructuredError(w, r, http.StatusUnauthorized, "reauth_required", "sign in again to renew the session")
		return
	case errors.Is(err, redclaw.ErrStaleSSOSession):
		s.writeStructuredError(w, r, http.StatusConflict, "stale_session_token", "use the latest session token")
		return
	case err != nil:
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "RedClaw session renewal unavailable")
		return
	}
	profile := s.ssoRefreshProfile(w, r, newBearer, input.WorkspaceID, old.ID)
	if profile == nil {
		return
	}
	writeJSON(w, http.StatusOK, ssoHandoff{
		Token: newBearer, User: profile.Name, UserID: profile.ID, WorkspaceID: input.WorkspaceID, AuthMethod: redClawOIDCMethod,
	})
}

func validRenewalWorkspace(workspace string) bool {
	return workspace != "" && workspace == strings.TrimSpace(workspace) && workspace != "default" &&
		workspace != "." && workspace != ".." && !strings.ContainsAny(workspace, "/\\%?#\x00")
}

func (s *Server) ssoRefreshProfile(w http.ResponseWriter, r *http.Request, bearer, workspace, employee string) *redclaw.MeResult {
	profile, err := s.redclawAdminClient.ProvisionedMe(r.Context(), bearer)
	switch {
	case errors.Is(err, redclaw.ErrInvalidCredentials):
		s.writeStructuredError(w, r, http.StatusUnauthorized, "parent_session_invalid", "sign in again to renew the session")
		return nil
	case errors.Is(err, redclaw.ErrTenantMismatch), errors.Is(err, redclaw.ErrIdentityNotProvisioned):
		s.writeStructuredError(w, r, http.StatusForbidden, "session_scope_changed", "employee scope is no longer verified")
		return nil
	case err != nil:
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "employee authority unavailable")
		return nil
	case !profile.Provisioned || strings.TrimSpace(profile.ID) == "" || profile.TenantID != s.cfg.RedClawTenantID ||
		(employee != "" && employee != profile.ID):
		s.writeStructuredError(w, r, http.StatusForbidden, "session_scope_changed", "employee scope is no longer verified")
		return nil
	}
	status, member, err := s.identityStore.CheckMembership(r.Context(), workspace, profile.ID)
	switch {
	case err != nil:
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "workspace membership verification unavailable")
		return nil
	case status != identity.MembershipActive || member == nil:
		s.writeStructuredError(w, r, http.StatusForbidden, "workspace_membership_required", "current workspace membership required")
		return nil
	}
	return profile
}
