package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

type classifyEmailsBody struct {
	IDs   []string `json:"ids"`
	Limit int      `json:"limit"`
}

type classifyResultJSON struct {
	EmailID    string `json:"emailId"`
	Category   string `json:"category"`
	Importance string `json:"importance"`
	Summary    string `json:"summary"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) handleEmailClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if s.kxmemory == nil {
		writeError(w, http.StatusServiceUnavailable, "classifier not configured")
		return
	}
	var body classifyEmailsBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	limit := body.Limit
	if limit <= 0 {
		limit = 20
	}
	items, err := s.emailStore.ListUnclassifiedScoped(r.Context(), userID, wsID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(body.IDs) > 0 {
		allow := map[string]struct{}{}
		for _, id := range email.CapClassifyIDs(body.IDs, limit) {
			allow[id] = struct{}{}
		}
		filtered := items[:0]
		for _, it := range items {
			if _, ok := allow[it.ID]; ok {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	results := make([]classifyResultJSON, 0, len(items))
	for _, it := range items {
		one, cerr := s.classifyOneEmail(r.Context(), it, userID, wsID)
		results = append(results, one)
		if cerr != nil {
			log.Printf("[email/classify] %s: %v", it.ID, cerr)
		}
	}
	remaining, _ := s.emailStore.CountUnclassifiedScoped(r.Context(), userID, wsID)
	classified := 0
	for _, row := range results {
		if row.Category != "" && row.Error == "" {
			classified++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"classified": classified,
		"remaining":  remaining,
		"results":    results,
	})
}

func (s *Server) classifyOneEmail(ctx context.Context, it email.ClassifyItem, userID, workspaceID string) (classifyResultJSON, error) {
	out := classifyResultJSON{EmailID: it.ID}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := s.kxmemory.ClassifyEmails(callCtx, kxmemory.ClassifyEmailsRequest{
		Emails: []kxmemory.EmailForClassification{{
			EmailID:     it.ID,
			Subject:     it.Subject,
			Snippet:     it.Snippet,
			FromAddress: it.FromAddress,
			FromName:    it.FromName,
		}},
	})
	if err != nil {
		out.Error = err.Error()
		return out, err
	}
	if len(resp.Results) == 0 {
		out.Error = "empty classifier result"
		return out, nil
	}
	row := resp.Results[0]
	out.Category = email.NormalizeCategory(row.Category)
	out.Importance = row.Importance
	out.Summary = row.Summary
	if err := s.emailStore.SetClassificationScoped(callCtx, it.ID, userID, workspaceID,
		out.Category, out.Importance, out.Summary, row.SuggestedAction); err != nil {
		out.Error = err.Error()
		return out, err
	}
	return out, nil
}
