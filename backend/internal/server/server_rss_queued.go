package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

func (s *Server) handleRSSQueuedSource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
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
		OperationID     string    `json:"operationId"`
		ClientUpdatedAt time.Time `json:"clientUpdatedAt"`
		URL             string    `json:"url"`
		Title           string    `json:"title"`
		Enabled         *bool     `json:"enabled"`
		FetchInterval   string    `json:"fetchInterval"`
		PortalEpoch     *int64    `json:"portalEpoch"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF || body.Enabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "rss_invalid_source_intent")
		return
	}
	interval := 30 * time.Minute
	if body.FetchInterval != "" {
		parsed, err := time.ParseDuration(body.FetchInterval)
		if err != nil || parsed < time.Second {
			writeError(w, http.StatusUnprocessableEntity, "rss_invalid_source_intent")
			return
		}
		interval = parsed
	}
	sourceID, err := store.CreateQueuedSource(r.Context(), scope, body.OperationID, body.ClientUpdatedAt, rss.CreateSourceRequest{
		URL: body.URL, Title: body.Title, Enabled: *body.Enabled, FetchInterval: interval, PortalEpoch: body.PortalEpoch,
	})
	switch {
	case errors.Is(err, rss.ErrQueuedSourceInvalid):
		writeError(w, http.StatusUnprocessableEntity, "rss_invalid_source_intent")
	case errors.Is(err, rss.ErrQueuedSourceConflict):
		writeError(w, http.StatusConflict, "rss_source_conflict")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "rss_source_write_failed")
	default:
		writeJSON(w, http.StatusOK, map[string]string{
			"operation_id": body.OperationID, "source_id": sourceID, "user_id": scope.UserID, "workspace_id": scope.WorkspaceID,
		})
	}
}
