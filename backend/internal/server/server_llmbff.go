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
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/quota"
)

// 多模态图片限制：单条消息最多 4 张，每张（data URL 或外链）最长 6MB 字符。
const (
	maxChatImagesPerMessage = 4
	maxChatImageBytes       = 6 << 20
)

// checkQuotaOrAudit 在 BFF 调用前做一次 pre-flight：拉取 Enforcer 决定并
// 写 audit。estTokens 仅用于 audit detail，不参与预算判定（预算策略自己
// 决定如何用 token 估算）。
//
// 返回值 blocked 表示「EnforceMode=true 且 Decision.Allow=false」——调用方
// 必须立刻阻断（HTTP 429 + 结构化错误）。EnforceMode=false 时永远返 false，
// 行为与历史一致（仅审计）。
func (s *Server) checkQuotaOrAudit(r *http.Request, action, kind, model string, estTokens int) (blocked bool) {
	if s.quotaEnforcer == nil {
		return false
	}
	wsID := s.workspaceIDFromRequest(r)
	in := quota.DecisionInput{
		WorkspaceID:    wsID,
		Kind:           kind,
		Model:          model,
		EstimatedTokens: estTokens,
	}
	dec, err := s.quotaEnforcer.Check(r.Context(), in)
	if err != nil {
		s.Write(r, "llm.quota.error", "llm:"+action, AuditFields{
			Success: false,
			Detail:  fmt.Sprintf("kind=%s model=%s err=%s", kind, model, err.Error()),
		})
		// Store 错误按 fail-open 处理：审计已记录，不阻断，避免预算子系统
		// 故障连带杀死整个 BFF。
		return false
	}
	if !dec.Allow {
		s.Write(r, "llm.quota.denied", "llm:"+action, AuditFields{
			Success: false,
			Detail:  fmt.Sprintf("kind=%s model=%s reason=%s strategy=%s enforce_mode=%v", kind, model, dec.Reason, s.quotaEnforcer.StrategyName(), s.quotaEnforcer.EnforceMode()),
		})
		return s.quotaEnforcer.EnforceMode()
	}
	s.Write(r, "llm.quota.checked", "llm:"+action, AuditFields{
		Success: true,
		Detail:  fmt.Sprintf("kind=%s model=%s strategy=%s", kind, model, s.quotaEnforcer.StrategyName()),
	})
	return false
}

// handleLLMBFFStream — 流式 chat completion，SSE 输出。
//
// 请求体同 OpenAI（messages/model/temperature/max_tokens），额外可选 kind
// 字段标记用途（chat/summarize/translate...）用于成本分类。
// 响应 Content-Type: text/event-stream，每行 "data: {...}\n\n"。
//
// Quota 拦截：EnforceMode=true 且 Decision.Allow=false 时，**必须**在写
// 任何 SSE header 之前返 429 + 结构化错误。一旦 Content-Type:
// text/event-stream 已写，Go 的 ResponseWriter 状态码即固定，再改只能
// 走 chunked SSE error 事件，前端需要更复杂的解析路径。
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
		// 多模态图片校验：数量与单张大小限制 + scheme 白名单（只放行
		// https 外链与 data:image 内联，杜绝 http 明文与其它协议）。
		if len(m.Images) > maxChatImagesPerMessage {
			writeError(w, http.StatusBadRequest, "too many images (max 4 per message)")
			return
		}
		for _, img := range m.Images {
			if len(img) > maxChatImageBytes {
				writeError(w, http.StatusBadRequest, "image too large (max 6MB)")
				return
			}
			if !strings.HasPrefix(img, "https://") && !strings.HasPrefix(img, "data:image/") {
				writeError(w, http.StatusBadRequest, "image must be https:// or data:image/ URL")
				return
			}
		}
	}

	// Quota pre-flight：在 body 校验通过、但 SSE header 尚未写入前拦截。
	// EnforceMode=true 且 Deny 时返 429 + 结构化错误；EnforceMode=false
	// 时永远 false（仅审计）。
	if s.checkQuotaOrAudit(r, "llm.stream", body.Kind, body.Model, 0) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":     "quota exceeded",
			"code":      "llm.quota.denied",
			"resource":  "llm:llm.stream",
			"retryable": false,
		})
		return
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

// handleLLMBFFQuota — 返回当前 workspace 的配额状态：每个预算的 kind /
// limit / period，以及当前 Enforcer 策略的 allow 标志。
//
// 响应：
//
//	{
//	  "workspace_id": "...",
//	  "budgets":      [Budget, ...],
//	  "strategy":     "always_allow",
//	  "enforce_mode": false
//	}
//
// 当前默认策略为 always_allow，enforce_mode=false——所有调用都放行，
// 但事件可被审计。仅当部署替换 Enforcer 策略后才进入 enforce_mode=true。
func (s *Server) handleLLMBFFQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.quotaEnforcer == nil {
		writeError(w, http.StatusServiceUnavailable, "quota enforcer not configured")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	budgets, err := s.quotaEnforcer.Store().BudgetsFor(r.Context(), wsID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "quota query: "+err.Error())
		return
	}
	if budgets == nil {
		budgets = []quota.Budget{} // JSON 消费方（未来的 Vue 视图）需要 [] 而非 null
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace_id": wsID,
		"budgets":      budgets,
		"strategy":     s.quotaEnforcer.StrategyName(),
		"enforce_mode": s.quotaEnforcer.EnforceMode(),
	})
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
