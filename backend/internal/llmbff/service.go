// Package llmbff is S0-B: the unified LLM Backend-for-Frontend for the
// Personal Super Terminal.
//
// It is a thin orchestration layer that sits between the HTTP handlers
// (/api/llm/*) and the underlying LLM providers (the existing aigate direct
// clients OR the llm-gateway-go-3 client). Its responsibilities, per the S0
// design (spec §3.2 decision 2):
//
//   1. Provide ONE request/response vocabulary for the whole backend so S1/S2/S3
//      business code never has to know whether a call is going to aigate or
//      llm-gateway.
//   2. Relay SSE streaming chat completions (the gateway supports stream=true;
//      the old aigate.LLMClient only did non-stream).
//   3. Record per-call token / cost usage into model_usage, scoped by
//      workspace_id, so S3-Console can render cost dashboards.
//   4. Keep the gateway admin token inside the backend — it is read from PG by
//      the server and handed to this package as a Provider; it NEVER crosses
//      the BFF boundary to the mobile client (spec §6 risk R6).
//
// Design notes:
//   - This package deliberately does NOT import aigate or llmgateway. It only
//     defines the Provider interface + types. The server package wires concrete
//     adapters (see llmbff_provider_adapters.go). This keeps llmbff free of
//     HTTP-client bloat and easy to unit-test with a fake Provider.
//   - Usage recording is best-effort: a failed INSERT must not fail the user's
//     chat. The Recorder interface lets handlers swap in a no-op for tests.
package llmbff

import (
	"context"
	"time"
)

// Role identifies a chat message author (OpenAI convention).
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a chat conversation. Mirrors OpenAI/llm-gateway shape.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the unified BFF request for a chat completion.
//
// WorkspaceID is REQUIRED for usage attribution and per-workspace quota (S3).
// Model is optional — the Provider picks a default when empty.
// Stream toggles streaming (SSE delta) vs. one-shot completion.
type ChatRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	Model       string    `json:"model,omitempty"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	User        string    `json:"user,omitempty"` // end-user id for abuse/audit
}

// ChatResponse is the non-streaming completion result. Usage is always
// populated when the provider returns it (zero on missing).
type ChatResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
	Usage   Usage  `json:"usage"`
}

// Usage is the token accounting for one call. Cost is computed by the Recorder
// using a per-model price table (kept out of this package).
type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// Delta is one chunk of a streaming response. A stream ends when Done=true.
//
// Content is the incremental text (OpenAI delta shape). The finish reason
// (stop/length/tool_calls) arrives in the final delta with Done=true, and
// Usage is populated on that final chunk when the provider supports
// stream_options.include_usage.
type Delta struct {
	Content     string `json:"content,omitempty"`
	Done        bool   `json:"done"`
	FinishReason string `json:"finish_reason,omitempty"`
	Usage       *Usage `json:"usage,omitempty"`
}

// EmbedRequest is the unified embedding request.
type EmbedRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Model       string `json:"model,omitempty"`
	Input       string `json:"input"`
}

// EmbedResponse is the embedding result.
type EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Model     string    `json:"model"`
	Usage     Usage     `json:"usage"`
}

// Provider is the abstraction every concrete LLM backend must satisfy.
// Implementations live in the server package (aigate / llmgateway adapters).
type Provider interface {
	// Chat does a non-streaming completion.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Stream does a streaming completion, pushing Deltas to fn until the
	// stream ends or fn returns false (client disconnect). The final Delta
	// has Done=true and, when available, Usage populated.
	Stream(ctx context.Context, req ChatRequest, fn func(Delta) bool) (*Usage, error)
	// Embed computes an embedding vector.
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
}

// Recorder persists per-call usage. Implementations: UsageStore (PG) or
// noopRecorder (tests / disabled mode).
type Recorder interface {
	// RecordUsage writes one usage row. Best-effort: must not block the caller
	// for more than a short bounded time. Errors are logged and discarded by
	// the caller (chat must not fail because usage logging failed).
	RecordUsage(ctx context.Context, wsID, model, userID string, u Usage, kind string) error
}

// NoopRecorder is a Recorder that discards everything. Used when PG is absent
// or in tests.
type NoopRecorder struct{}

func (NoopRecorder) RecordUsage(context.Context, string, string, string, Usage, string) error {
	return nil
}

// Service is the BFF orchestration entrypoint. Handlers call Chat/Stream/Embed
// on it; the Service dispatches to the Provider and records usage.
type Service struct {
	provider Provider
	recorder Recorder
}

// NewService constructs the BFF. recorder may be NoopRecorder.
func NewService(provider Provider, recorder Recorder) *Service {
	if recorder == nil {
		recorder = NoopRecorder{}
	}
	return &Service{provider: provider, recorder: recorder}
}

// Chat performs a non-streaming completion and records usage.
// The kind tag (e.g. "chat", "summarize", "translate") is stored for
// per-use-case cost breakdowns in S3-Console.
func (s *Service) Chat(ctx context.Context, req ChatRequest, kind string) (*ChatResponse, error) {
	if s.provider == nil {
		return nil, ErrNotConfigured
	}
	resp, err := s.provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	// Best-effort usage recording — never fail the chat on a logging error.
	if resp.Usage.TotalTokens > 0 {
		_ = s.recorder.RecordUsage(ctx, req.WorkspaceID, resp.Model, req.User, resp.Usage, kind)
	}
	return resp, nil
}

// Stream performs a streaming completion, forwarding Deltas to fn. After the
// stream ends, usage (if returned by the provider) is recorded once.
//
// Returns the final Usage (may be zero if the provider didn't send
// include_usage). fn returns false to abort the stream early (client
// disconnect); the provider should stop sending.
func (s *Service) Stream(ctx context.Context, req ChatRequest, kind string, fn func(Delta) bool) (*Usage, error) {
	if s.provider == nil {
		return nil, ErrNotConfigured
	}
	usage, err := s.provider.Stream(ctx, req, fn)
	if err != nil {
		return nil, err
	}
	if usage != nil && usage.TotalTokens > 0 {
		_ = s.recorder.RecordUsage(ctx, req.WorkspaceID, req.Model, req.User, *usage, kind)
	}
	return usage, nil
}

// Embed computes an embedding and records usage.
func (s *Service) Embed(ctx context.Context, req EmbedRequest, kind string) (*EmbedResponse, error) {
	if s.provider == nil {
		return nil, ErrNotConfigured
	}
	resp, err := s.provider.Embed(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Usage.TotalTokens > 0 {
		_ = s.recorder.RecordUsage(ctx, req.WorkspaceID, resp.Model, "", resp.Usage, kind)
	}
	return resp, nil
}

// Usage aggregates the S3 dashboard reads.
type UsageSummary struct {
	WorkspaceID     string    `json:"workspace_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	TotalTokens     int       `json:"total_tokens"`
	PromptTokens    int       `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalCostUSD    float64   `json:"total_cost_usd"`
	CallCount       int       `json:"call_count"`
}

// Summarizer reads back usage aggregates for S3 dashboards. Backed by
// UsageStore; NoopSummarizer returns zeros.
type Summarizer interface {
	Summarize(ctx context.Context, wsID string, from, to time.Time) (UsageSummary, error)
}

// ErrNotConfigured means no Provider was wired (POCKET_LLM_* env unset and no
// llm-gateway config in PG). Handlers map this to HTTP 503.
var ErrNotConfigured = bffError("llmbff: no provider configured")

// bffError is a string error type so callers can use errors.Is without import.
type bffError string

func (e bffError) Error() string { return string(e) }
