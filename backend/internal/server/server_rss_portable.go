package server

import (
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) rssPortableScope(w http.ResponseWriter, r *http.Request) (rss.Scope, bool) {
	claims := s.claimsFromContext(r)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || strings.TrimSpace(claims.WorkspaceID) == "" {
		writeError(w, 401, "verified workspace claims required")
		return rss.Scope{}, false
	}
	if s.identityStore == nil {
		writeError(w, 503, "workspace membership verification unavailable")
		return rss.Scope{}, false
	}
	status, _, err := s.identityStore.CheckMembership(r.Context(), claims.WorkspaceID, claims.UserID)
	if err != nil {
		writeError(w, 503, "workspace membership verification unavailable")
		return rss.Scope{}, false
	}
	if status != identity.MembershipActive {
		writeError(w, 403, "current workspace membership required")
		return rss.Scope{}, false
	}
	return rss.Scope{UserID: claims.UserID, WorkspaceID: claims.WorkspaceID}, true
}

func validRSSPortableEpoch(epoch *int64) bool {
	return epoch == nil || (*epoch >= 0 && *epoch <= 9007199254740991)
}

func (s *Server) handleRSSPortableChanges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	store := s.requireRSSStore(w)
	if store == nil {
		return
	}
	cursor := int64(0)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var err error
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 || cursor > 9007199254740991 {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
	}
	scope, ok := s.rssPortableScope(w, r)
	if !ok {
		return
	}
	page, err := store.ListPortableChanges(r.Context(), scope, cursor, limit)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"user_id": scope.UserID, "workspace_id": scope.WorkspaceID, "changes": page.Changes, "next_cursor": page.NextCursor})
}
