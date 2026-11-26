package server

import (
	"encoding/json"
	"errors"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleRSSMigrationSnapshot(w http.ResponseWriter, r *http.Request, store *rss.Store, id string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	scope, ok := s.rssPortableScope(w, r)
	if !ok {
		return
	}
	source, err := store.GetSource(r.Context(), id, scope)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	if source.PortalManaged {
		writeError(w, http.StatusConflict, "RSS source already participates in portal synchronization")
		return
	}
	address, err := url.Parse(source.URL)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" ||
		address.User != nil || strings.ContainsAny(source.URL, "?#") {
		writeError(w, http.StatusUnprocessableEntity, "RSS source URL cannot be copied safely")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id": source.ID, "user_id": source.UserID, "workspace_id": source.WorkspaceID,
		"title": source.Title, "url": source.URL, "category": source.Category,
		"updated_at": source.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) handleRSSPortableItemStates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "POST only")
		return
	}
	store := s.requireRSSStore(w)
	if store == nil {
		return
	}
	scope, ok := s.rssPortableScope(w, r)
	if !ok {
		return
	}
	var body struct {
		Items []struct {
			rss.PortableItemState
			OriginalEpoch *int64 `json:"epoch"`
		} `json:"items"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, 400, "invalid status operations")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid status operations")
		return
	}
	items := make([]rss.PortableItemState, 0, len(body.Items))
	for _, item := range body.Items {
		if item.OriginalEpoch == nil {
			writeError(w, 400, "original status epoch required")
			return
		}
		item.PortableItemState.Epoch = *item.OriginalEpoch
		items = append(items, item.PortableItemState)
	}
	acks, err := store.ApplyPortableItemStates(r.Context(), scope, items)
	if err != nil {
		if errors.Is(err, rss.ErrPortableStateConflict) {
			writeRSSError(w, err)
		} else {
			writeError(w, 400, "status operations were not saved")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"user_id": scope.UserID, "workspace_id": scope.WorkspaceID, "acks": acks})
}

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
