package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/halfking/pocket-opencode/backend/internal/recommendations"
)

// handleGetNoteRecommendations returns similar notes for a given note.
// GET /api/notes/:id/recommendations?limit=10
func (s *Server) handleGetNoteRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	noteID := chi.URLParam(r, "id")
	if noteID == "" {
		writeError(w, http.StatusBadRequest, "note ID required")
		return
	}

	// Extract authenticated user and workspace from context
	userID, workspaceID, ok := s.extractAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Check if recommendations are available
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "recommendations service not available")
		return
	}

	// Parse limit parameter
	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 50 {
		limit = 50
	}

	// Create calculator and find similar notes
	calculator := recommendations.NewSimilarityCalculator(s.pool)
	similar, err := calculator.FindSimilarNotes(ctx, noteID, userID, workspaceID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to find recommendations: %v", err))
		return
	}

	// Return results
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"recommendations": similar,
		"count":           len(similar),
	})
}

// suggestTagsRequest is the request body for tag suggestions.
type suggestTagsRequest struct {
	Content string `json:"content,omitempty"` // Optional: override note content
}

// handleSuggestNoteTags suggests tags for a note based on its content.
// POST /api/notes/:id/suggest-tags
// Body: { "content": "optional override" }
func (s *Server) handleSuggestNoteTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	noteID := chi.URLParam(r, "id")
	if noteID == "" {
		writeError(w, http.StatusBadRequest, "note ID required")
		return
	}

	// Extract authenticated user and workspace
	userID, workspaceID, ok := s.extractAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Check if notes store is available
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes service not available")
		return
	}

	// Parse request body
	var req suggestTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is OK, we'll use the note's existing content
		req.Content = ""
	}

	// Get the note
	note, err := s.notesStore.GetByIDScoped(ctx, noteID, userID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get note: %v", err))
		return
	}
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	// Use provided content or fall back to note's content
	content := req.Content
	if content == "" {
		content = note.Content
		if content == "" {
			content = note.Snippet
		}
	}

	// Parse existing tags
	var existingTags []string
	if note.Tags != "" {
		_ = json.Unmarshal([]byte(note.Tags), &existingTags)
	}

	// Generate suggestions
	suggester := recommendations.NewTagSuggester()
	suggestions := suggester.SuggestTagsFromNote(note.Title, content, note.Domain, note.Tags)

	// Return results
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"suggestions":  suggestions,
		"existingTags": existingTags,
		"count":        len(suggestions),
	})
}
