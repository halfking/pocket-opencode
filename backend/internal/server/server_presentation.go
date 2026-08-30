// internal/server/server_presentation.go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/presentation"
)

func (s *Server) handlePresentations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleGeneratePresentation(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGeneratePresentation(w http.ResponseWriter, r *http.Request) {
	var req presentation.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Topic == "" {
		http.Error(w, `{"error":"topic is required"}`, http.StatusBadRequest)
		return
	}

	generator := &presentation.Generator{}
	resp, err := generator.Generate(req)
	if err != nil {
		http.Error(w, `{"error":"failed to generate presentation"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp.Presentation); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleRenderPresentation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Format string `json:"format"` // html / markdown
		Title  string `json:"title"`
		Slides []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"slides"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if len(req.Slides) == 0 {
		http.Error(w, `{"error":"slides are required"}`, http.StatusBadRequest)
		return
	}

	if req.Format == "" {
		http.Error(w, `{"error":"format is required (html or markdown)"}`, http.StatusBadRequest)
		return
	}

	// 转换为 Presentation 对象
	p := &presentation.Presentation{
		Title: req.Title,
	}
	for _, s := range req.Slides {
		p.Slides = append(p.Slides, presentation.Slide{
			Title:   s.Title,
			Content: s.Content,
		})
	}

	renderer := &presentation.Renderer{}

	switch req.Format {
	case "html":
		html, err := renderer.RenderHTML(p)
		if err != nil {
			http.Error(w, `{"error":"failed to render HTML"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))

	case "markdown":
		md, err := renderer.RenderToMarkdown(p)
		if err != nil {
			http.Error(w, `{"error":"failed to render markdown"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(md))

	default:
		http.Error(w, `{"error":"unsupported format, use html or markdown"}`, http.StatusBadRequest)
	}
}
