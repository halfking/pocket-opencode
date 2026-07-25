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
	// 从路径提取 snippet ID: /api/snippets/{id}
	id := r.URL.Path[len("/api/snippets/"):]
	if id == "" {
		http.Error(w, "missing snippet id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		snip, err := s.snippetStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snip)

	case http.MethodDelete:
		if err := s.snippetStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListSnippets(w http.ResponseWriter, r *http.Request) {
	req := snippet.ListSnippetsRequest{
		Language:  r.URL.Query().Get("language"),
		Tag:       r.URL.Query().Get("tag"),
		ProjectID: r.URL.Query().Get("project_id"),
		Search:    r.URL.Query().Get("search"),
	}

	snippets, err := s.snippetStore.List(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"snippets": snippets,
		"total":    len(snippets),
	})
}

func (s *Server) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	var req snippet.CreateSnippetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Title == "" || req.Code == "" {
		http.Error(w, "title and code are required", http.StatusBadRequest)
		return
	}

	snip, err := s.snippetStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(snip)
}