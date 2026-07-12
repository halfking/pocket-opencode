package server

// server_llmbff.go — S0-B unified LLM BFF HTTP handlers.
//
// Routes (registered in server.go):
//   POST /api/llm/stream   流式 chat completion（SSE，OpenAI delta shape）
//   GET  /api/llm/usage     当前 workspace 的用量汇总（S3 dashboard 数据源）
//
// Note: /api/llm/chat (non-stream) and /api/embed (embedding) already exist
// (server_assistant.go). When llmBFF is configured, those handlers SHOULD be
// migrated to call llmBFF too, but for now they keep their existing behavior
// to avoid a breaking change mid-sprint. The new stream + usage endpoints are
// additive and go straight through the BFF.
//
// Security: the gateway admin token lives only in the server (Provider holds
// it); it never appears in any request/response here (spec §6 risk R6).
// Workspace isolation: every call is tagged with the caller's workspace_id
// (from JWT claim, defaulting to "default") so usage attribution is correct
// and S3 dashboards are scoped.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// handleLLMBFFStream — 流式 chat completion，SSE 输出。
//
// 请求体同 OpenAI（messages/model/temperature/max_tokens），额外可选 kind
// 字段标记用途（chat/summarize/translate...）用于成本分类。
// 响应 Content-Type: text/event-stream，每行 "data: {...}\n\n"。
func (s *Server) handleLLMBFFStream(w http.ResponseWriter, r *http.Request) {
	if s.llmBFF == nil {
		writeError(w, http.StatusServiceUnavailable, "llm bff not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		Model       string             `json:"model"`
		Messages    []llmbff.Message   `json:"messages"`
		Temperature float64            `json:"temperature"`
		MaxTokens   int                `json:"max_tokens"`
		Kind        string             `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}
	if len(body.Messages) > 50 {
		writeError(w, http.StatusBadRequest, "too many messages (max 50)")
		return
	}
	for _, m := range body.Messages {
		if len(m.Content) > 32000 {
			writeError(w, http.StatusBadRequest, "message too long (max 32000 chars)")
			return
		}
	}

	// Flushable writer for SSE chunking.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: disable buffering
	w.WriteHeader(http.StatusOK)

	req := llmbff.ChatRequest{
		WorkspaceID: s.workspaceIDFromRequest(r),
		Model:       body.Model,
		Messages:    body.Messages,
		Temperature: body.Temperature,
		MaxTokens:   body.MaxTokens,
		Stream:      true,
		User:        s.userIDFromRequest(r),
	}

	ctx := r.Context()
	abort := false
	_, err := s.llmBFF.Stream(ctx, req, body.Kind, func(d llmbff.Delta) bool {
		// Client disconnect: ctx.Done() fires; stop sending.
		select {
		case <-ctx.Done():
			abort = true
			return false
		default:
		}
		payload, _ := json.Marshal(d)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
		return true
	})

	if abort {
		return // client gone; nothing more to write
	}
	if err != nil {
		// Stream already started (200 + headers sent) — surface error as an
		// SSE event rather than trying to change the status code.
		errDelta := llmbff.Delta{Done: true, FinishReason: "error"}
		payload, _ := json.Marshal(map[string]any{"error": err.Error(), "delta": errDelta})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
		return
	}

	// Final [DONE] marker (OpenAI convention) so clients know the stream ended.
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleLLMBFFUsage — 返回当前 workspace 的用量汇总。
//
// Query params: days=7 (默认 7 天，最大 90)。
// 响应: { workspace_id, period_start, period_end, total_tokens, ..., call_count }
func (s *Server) handleLLMBFFUsage(w http.ResponseWriter, r *http.Request) {
	if s.llmBFFSummarizer == nil {
		writeError(w, http.StatusServiceUnavailable, "usage tracking not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := parseIntDefault(d, 7); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	wsID := s.workspaceIDFromRequest(r)
	to := time.Now()
	from := to.AddDate(0, 0, -days)
	summary, err := s.llmBFFSummarizer.Summarize(r.Context(), wsID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage query: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// parseIntDefault parses s as int, returning def on error. Local helper to
// avoid pulling strconv into the handler.
func parseIntDefault(s string, def int) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def, err
	}
	return n, nil
}
