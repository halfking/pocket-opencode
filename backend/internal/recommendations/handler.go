package recommendations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// Handler provides HTTP endpoints for AI recommendations.
type Handler struct {
	pool       *pgxpool.Pool
	calculator *SimilarityCalculator
	suggester  *TagSuggester
}

// NewHandler creates a new recommendations handler.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{
		pool:       pool,
		calculator: NewSimilarityCalculator(pool),
		suggester:  NewTagSuggester(),
	}
}

// RegisterRoutes mounts the recommendations endpoints on the given router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/notes/{noteID}/recommendations", h.handleGetRecommendations)
	r.Post("/api/notes/{noteID}/suggest-tags", h.handleSuggestTags)
}

// handleGetRecommendations returns similar notes for a given note.
// GET /api/notes/:id/recommendations?limit=10
func (h *Handler) handleGetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	noteID := chi.URLParam(r, "noteID")
	if noteID == "" {
		http.Error(w, "note ID required", http.StatusBadRequest)
		return
	}

	// Extract authenticated user and workspace from context
	// (set by auth middleware in production)
	userID, _ := ctx.Value("userID").(string)
	workspaceID, _ := ctx.Value("workspaceID").(string)
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if workspaceID == "" {
		workspaceID = "default"
	}

	// Parse limit parameter
	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Find similar notes
	similar, err := h.calculator.FindSimilarNotes(ctx, noteID, userID, workspaceID, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to find recommendations: %v", err), http.StatusInternalServerError)
		return
	}

	// Return results
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"recommendations": similar,
		"count":           len(similar),
	})
}

// SuggestTagsRequest is the request body for tag suggestions.
type SuggestTagsRequest struct {
	Content string `json:"content,omitempty"` // Optional: override note content
}

// handleSuggestTags suggests tags for a note based on its content.
// POST /api/notes/:id/suggest-tags
// Body: { "content": "optional override" }
func (h *Handler) handleSuggestTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	noteID := chi.URLParam(r, "noteID")
	if noteID == "" {
		http.Error(w, "note ID required", http.StatusBadRequest)
		return
	}

	// Extract authenticated user and workspace
	userID, _ := ctx.Value("userID").(string)
	workspaceID, _ := ctx.Value("workspaceID").(string)
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if workspaceID == "" {
		workspaceID = "default"
	}

	// Parse request body
	var req SuggestTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is OK, we'll use the note's existing content
		req.Content = ""
	}

	// Get the note
	note, err := h.getNote(ctx, noteID, userID, workspaceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get note: %v", err), http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Error(w, "note not found", http.StatusNotFound)
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
	suggestions := h.suggester.SuggestTagsFromNote(note.Title, content, note.Domain, note.Tags)

	// Return results
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"suggestions":  suggestions,
		"existingTags": existingTags,
		"count":        len(suggestions),
	})
}

// getNote retrieves a note with ownership checks.
func (h *Handler) getNote(ctx context.Context, noteID, userID, workspaceID string) (*notes.Note, error) {
	var n notes.Note
	err := h.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, created_at, updated_at
		FROM notes 
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, noteID, userID, workspaceID).Scan(
		&n.ID, &n.UserID, &n.WorkspaceID, &n.Title, &n.Content, &n.Snippet,
		&n.ContentType, &n.Domain, &n.Tags, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &n, nil
}
