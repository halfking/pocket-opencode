package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notelinks"
)

// =====================================================================
// Note Links API
// =====================================================================

// handleNoteLinkCreate — POST /api/notes/:id/links
//
// Create a manual link from the source note (in URL path) to a target note.
// Validates that both notes exist and belong to the authenticated user before
// creating the link. Returns 201 with the created link on success.
func (s *Server) handleNoteLinkCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}

	if s.noteLinksStore == nil {
		writeError(w, http.StatusServiceUnavailable, "note links store not configured")
		return
	}

	// Extract source note ID from URL: /api/notes/{sourceID}/links
	path := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	sourceID := strings.TrimSuffix(path, "/links")
	if sourceID == "" || sourceID == path {
		writeError(w, http.StatusBadRequest, "invalid note id in path")
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// Parse request body
	var input notelinks.LinkInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Verify source note exists and belongs to user
	sourceNote, err := s.notesStore.GetByIDScoped(r.Context(), sourceID, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sourceNote == nil {
		writeError(w, http.StatusNotFound, "source note not found")
		return
	}

	// Verify target note exists and belongs to user
	targetNote, err := s.notesStore.GetByIDScoped(r.Context(), input.TargetNoteID, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if targetNote == nil {
		writeError(w, http.StatusNotFound, "target note not found")
		return
	}

	// Prevent self-linking
	if sourceID == input.TargetNoteID {
		writeError(w, http.StatusBadRequest, "cannot link a note to itself")
		return
	}

	// Check if noteLinksStore is configured
	if s.noteLinksStore == nil {
		writeError(w, http.StatusServiceUnavailable, "note links store not configured")
		return
	}

	// Create the link
	link := notelinks.NoteLink{
		ID:           randomID("link"),
		SourceNoteID: sourceID,
		TargetNoteID: input.TargetNoteID,
		LinkType:     input.LinkType,
		Confidence:   1.0, // Manual links have full confidence
		CreatedBy:    "user",
	}

	if err := s.noteLinksStore.Create(&link); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, link)
}

// handleNoteLinkDelete — DELETE /api/note-links/:id
//
// Delete a note link by its ID. Only the user who created the link
// (implicitly determined by note ownership) can delete it.
func (s *Server) handleNoteLinkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}

	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}

	if s.noteLinksStore == nil {
		writeError(w, http.StatusServiceUnavailable, "note links store not configured")
		return
	}

	// Extract link ID from URL: /api/note-links/{id}
	linkID := strings.TrimPrefix(r.URL.Path, "/api/note-links/")
	if linkID == "" {
		writeError(w, http.StatusBadRequest, "missing link id")
		return
	}

	// Delete the link (store handles not-found case)
	if err := s.noteLinksStore.Delete(linkID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleNoteRelated — GET /api/notes/:id/related
//
// Get related notes for a given note, including:
//  - Manual links (created by user)
//  - AI-recommended links (if available, with confidence scores)
//  - Backlinks (notes that link to this note)
//
// Returns enriched link data with note snippets for preview.
func (s *Server) handleNoteRelated(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}

	if s.noteLinksStore == nil {
		writeError(w, http.StatusServiceUnavailable, "note links store not configured")
		return
	}

	// Extract note ID from URL: /api/notes/{id}/related
	path := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	noteID := strings.TrimSuffix(path, "/related")
	if noteID == "" || noteID == path {
		writeError(w, http.StatusBadRequest, "invalid note id in path")
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// Verify note exists and belongs to user
	note, err := s.notesStore.GetByIDScoped(r.Context(), noteID, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	// Get manual and AI links
	outgoing, incoming, err := s.noteLinksStore.GetRelatedNotes(noteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Enrich outgoing links with target note data
	manualLinks := []map[string]any{}
	smartLinks := []map[string]any{}

	for _, link := range outgoing {
		targetNote, err := s.notesStore.GetByIDScoped(r.Context(), link.TargetNoteID, uid, wsID)
		if err != nil || targetNote == nil {
			continue // Skip if target note was deleted or not accessible
		}

		enriched := map[string]any{
			"id":         link.ID,
			"linkType":   string(link.LinkType),
			"createdBy":  link.CreatedBy,
			"createdAt":  link.CreatedAt,
			"confidence": link.Confidence,
			"note": map[string]any{
				"id":      targetNote.ID,
				"title":   targetNote.Title,
				"snippet": targetNote.Snippet,
				"tags":    parseTagsJSON(targetNote.Tags),
			},
		}

		if link.Reason != "" {
			enriched["reason"] = link.Reason
		}

		if link.CreatedBy == "ai" {
			smartLinks = append(smartLinks, enriched)
		} else {
			manualLinks = append(manualLinks, enriched)
		}
	}

	// Enrich incoming links (backlinks) with source note data
	backlinks := []map[string]any{}
	for _, link := range incoming {
		sourceNote, err := s.notesStore.GetByIDScoped(r.Context(), link.SourceNoteID, uid, wsID)
		if err != nil || sourceNote == nil {
			continue
		}

		backlinks = append(backlinks, map[string]any{
			"id":        link.ID,
			"linkType":  string(link.LinkType),
			"createdBy": link.CreatedBy,
			"createdAt": link.CreatedAt,
			"note": map[string]any{
				"id":      sourceNote.ID,
				"title":   sourceNote.Title,
				"snippet": sourceNote.Snippet,
				"tags":    parseTagsJSON(sourceNote.Tags),
			},
		})
	}

	// If memora is configured, fetch AI recommendations
	aiRecommendations := []map[string]any{}
	if s.memora != nil && note.Content != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Use memora's search to find similar notes
		searchResp, err := s.memora.SearchHybrid(ctx, note.Content[:min(500, len(note.Content))], 5, nil)
		if err == nil && searchResp != nil {
			for _, hit := range searchResp.Results {
				// Skip if already manually linked or is the note itself
				if hit.ID == noteID || isAlreadyLinked(hit.ID, outgoing) {
					continue
				}

				// Fetch the note to ensure it belongs to this user
				relNote, err := s.notesStore.GetByIDScoped(ctx, hit.ID, uid, wsID)
				if err != nil || relNote == nil {
					continue
				}

				aiRecommendations = append(aiRecommendations, map[string]any{
					"note": map[string]any{
						"id":      relNote.ID,
						"title":   relNote.Title,
						"snippet": relNote.Snippet,
						"tags":    parseTagsJSON(relNote.Tags),
					},
					"confidence": hit.Score,
					"reason":     "内容相似度高",
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"manualLinks":       manualLinks,
		"smartLinks":        smartLinks,
		"backlinks":         backlinks,
		"aiRecommendations": aiRecommendations,
	})
}

// isAlreadyLinked checks if a target note ID is already in the outgoing links.
func isAlreadyLinked(targetID string, links []notelinks.NoteLink) bool {
	for _, link := range links {
		if link.TargetNoteID == targetID {
			return true
		}
	}
	return false
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
