package server

import (
	"encoding/json"
	"net/http"

	syncsvc "github.com/halfking/pocket-opencode/backend/internal/sync"
)

// handleSyncPull handles GET /api/sync/pull - pull updates from memora.
func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if s.syncOrchestrator == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sync service not configured",
		})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	status, err := s.syncOrchestrator.GetSyncStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to get sync status: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "pull initiated",
		"status":  status,
	})
}

// handleSyncStatus handles GET /api/sync/status - get current sync status overview.
func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.syncOrchestrator == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sync service not configured",
		})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	status, err := s.syncOrchestrator.GetSyncStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to get sync status: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// handleSyncResolve handles POST /api/sync/resolve/:noteId - resolve conflict for a note.
func (s *Server) handleSyncResolve(w http.ResponseWriter, r *http.Request) {
	if s.syncOrchestrator == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sync service not configured",
		})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	noteID := r.PathValue("noteId")
	if noteID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "noteId is required",
		})
		return
	}

	var req struct {
		Resolution string `json:"resolution"` // 'local' | 'remote' | 'merge'
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
		return
	}

	if req.Resolution == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "resolution is required",
		})
		return
	}

	if err := s.syncOrchestrator.ResolveConflict(r.Context(), noteID, req.Resolution); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to resolve conflict: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "conflict resolved",
		"noteId":      noteID,
		"resolution":  req.Resolution,
	})
}

// handleSavedSearches handles GET/POST /api/knowledge/search/saved - get or save searches.
func (s *Server) handleSavedSearches(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleGetSavedSearches(w, r)
	} else if r.Method == http.MethodPost {
		s.handleSaveSearch(w, r)
	} else {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
	}
}

// handleSavedSearchItem handles DELETE /api/knowledge/search/saved/:id - delete a saved search.
func (s *Server) handleSavedSearchItem(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.handleDeleteSavedSearch(w, r)
	} else {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
	}
}

// handleSaveSearch handles POST /api/knowledge/search/saved - save a search.
func (s *Server) handleSaveSearch(w http.ResponseWriter, r *http.Request) {
	if s.savedSearchStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "saved search service not configured",
		})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	userID := s.userIDFromRequest(r)
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "user not authenticated",
		})
		return
	}

	var req struct {
		Name    string   `json:"name"`
		Query   string          `json:"query"`
		Filters map[string]interface{} `json:"filters,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
		return
	}

	if req.Name == "" || req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "name and query are required",
		})
		return
	}

	search := &syncsvc.SavedSearch{
		ID:      generateUUID(),
		UserID:  userID,
		Name:    req.Name,
		Query:   req.Query,
		Filters: req.Filters,
	}

	if err := s.savedSearchStore.Create(search); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to save search: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusCreated, search)
}

// handleGetSavedSearches handles GET /api/knowledge/search/saved - get saved searches.
func (s *Server) handleGetSavedSearches(w http.ResponseWriter, r *http.Request) {
	if s.savedSearchStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "saved search service not configured",
		})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	userID := s.userIDFromRequest(r)
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "user not authenticated",
		})
		return
	}

	searches, err := s.savedSearchStore.ListByUser(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to get saved searches: " + err.Error(),
		})
		return
	}

	if searches == nil {
		searches = []syncsvc.SavedSearch{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"searches": searches,
		"total":    len(searches),
	})
}

// handleDeleteSavedSearch handles DELETE /api/knowledge/search/saved/:id - delete a saved search.
func (s *Server) handleDeleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	if s.savedSearchStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "saved search service not configured",
		})
		return
	}

	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	userID := s.userIDFromRequest(r)
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "user not authenticated",
		})
		return
	}

	searchID := r.PathValue("id")
	if searchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "id is required",
		})
		return
	}

	if err := s.savedSearchStore.Delete(searchID, userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to delete saved search: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "saved search deleted",
	})
}
