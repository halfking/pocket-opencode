package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/snippet"
)

func (s *Server) handleSnippets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListSnippets(w, r)
	case http.MethodPost:
		s.handleCreateSnippet(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSnippetOps(w http.ResponseWriter, r *http.Request) {
	// Extract snippet ID from path: /api/snippets/{id}
	id := r.URL.Path[len("/api/snippets/"):]
	if id == "" {
		http.Error(w, `{"error":"missing snippet id"}`, http.StatusBadRequest)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		snip, err := s.snippetStore.GetScoped(id, uid, wsID)
		if err != nil {
			http.Error(w, `{"error":"snippet not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snip); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		if err := s.snippetStore.DeleteScoped(id, uid, wsID); err != nil {
			http.Error(w, `{"error":"snippet not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListSnippets(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	req := snippet.ListSnippetsRequest{
		Language:  r.URL.Query().Get("language"),
		Tag:       r.URL.Query().Get("tag"),
		ProjectID: r.URL.Query().Get("project_id"),
		Search:    r.URL.Query().Get("search"),
	}

	snippets, err := s.snippetStore.ListScoped(req, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to list snippets"}`, http.StatusInternalServerError)
		return
	}

	if snippets == nil {
		snippets = []*snippet.Snippet{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"snippets": snippets,
		"total":    len(snippets),
	}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var req snippet.CreateSnippetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Title == "" || req.Code == "" {
		http.Error(w, `{"error":"title and code are required"}`, http.StatusBadRequest)
		return
	}

	snip, err := s.snippetStore.CreateScoped(req, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to create snippet"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(snip); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}