package llmbff

// service_test.go — unit tests for the BFF Service using a fake Provider and
// a recording Recorder. No network, no DB — pure orchestration logic.

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a controllable Provider for tests.
type fakeProvider struct {
	chatResp    *ChatResponse
	chatErr     error
	streamDeltas []Delta
	streamUsage  *Usage
	streamErr    error
	embedResp   *EmbedResponse
	embedErr    error

	// Capture fields to assert what the BFF forwarded.
	lastChatReq  *ChatRequest
	lastEmbedReq *EmbedRequest
}

func (f *fakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	f.lastChatReq = &req
	if f.chatErr != nil {
		return nil, f.chatErr
	}
	return f.chatResp, nil
}

func (f *fakeProvider) Stream(ctx context.Context, req ChatRequest, fn func(Delta) bool) (*Usage, error) {
	f.lastChatReq = &req
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	for _, d := range f.streamDeltas {
		if !fn(d) {
			break
		}
	}
	if f.streamUsage == nil {
		return &Usage{}, nil
	}
	return f.streamUsage, nil
}

func (f *fakeProvider) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	f.lastEmbedReq = &req
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	return f.embedResp, nil
}

// recordingRecorder captures RecordUsage calls for assertions.
type recordingRecorder struct {
	calls []recordedCall
}

type recordedCall struct {
	wsID, model, userID string
	usage               Usage
	kind                string
}

func (r *recordingRecorder) RecordUsage(_ context.Context, wsID, model, userID string, u Usage, kind string) error {
	r.calls = append(r.calls, recordedCall{wsID, model, userID, u, kind})
	return nil
}

func TestServiceChat_RecordsUsage(t *testing.T) {
	fp := &fakeProvider{chatResp: &ChatResponse{
		Content: "hi",
		Model:   "gpt-test",
		Usage:   Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}}
	rr := &recordingRecorder{}
	svc := NewService(fp, rr)

	resp, err := svc.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws1",
		Model:       "gpt-test",
		Messages:    []Message{{Role: RoleUser, Content: "hello"}},
		User:        "u1",
	}, "chat")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q", resp.Content)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(rr.calls))
	}
	c := rr.calls[0]
	if c.wsID != "ws1" || c.model != "gpt-test" || c.userID != "u1" || c.kind != "chat" || c.usage.TotalTokens != 8 {
		t.Errorf("recorded call mismatch: %+v", c)
	}
}

func TestServiceChat_NoUsageNotRecorded(t *testing.T) {
	// Provider returns zero usage → should not record a row.
	fp := &fakeProvider{chatResp: &ChatResponse{Content: "x", Model: "m"}}
	rr := &recordingRecorder{}
	svc := NewService(fp, rr)

	_, _ = svc.Chat(context.Background(), ChatRequest{WorkspaceID: "w"}, "chat")
	if len(rr.calls) != 0 {
		t.Errorf("zero-usage call should not be recorded, got %d", len(rr.calls))
	}
}

func TestServiceStream_ForwardsDeltasAndRecordsUsage(t *testing.T) {
	fp := &fakeProvider{
		streamDeltas: []Delta{
			{Content: "Hel"},
			{Content: "lo"},
			{Done: true, FinishReason: "stop"},
		},
		streamUsage: &Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4},
	}
	rr := &recordingRecorder{}
	svc := NewService(fp, rr)

	var collected string
	usage, err := svc.Stream(context.Background(), ChatRequest{
		WorkspaceID: "ws2",
		Model:       "m",
		User:        "u2",
	}, "summarize", func(d Delta) bool {
		collected += d.Content
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if collected != "Hello" {
		t.Errorf("collected = %q", collected)
	}
	if usage == nil || usage.TotalTokens != 4 {
		t.Errorf("usage = %+v", usage)
	}
	if len(rr.calls) != 1 || rr.calls[0].kind != "summarize" {
		t.Errorf("usage recording wrong: %+v", rr.calls)
	}
}

func TestServiceChat_ProviderError(t *testing.T) {
	fp := &fakeProvider{chatErr: errors.New("boom")}
	rr := &recordingRecorder{}
	svc := NewService(fp, rr)

	_, err := svc.Chat(context.Background(), ChatRequest{}, "chat")
	if err == nil {
		t.Fatal("expected error from provider")
	}
	if len(rr.calls) != 0 {
		t.Errorf("failed call should not record usage, got %d", len(rr.calls))
	}
}

func TestServiceNotConfigured(t *testing.T) {
	svc := NewService(nil, nil) // nil provider
	_, err := svc.Chat(context.Background(), ChatRequest{}, "chat")
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNoopRecorder(t *testing.T) {
	// Should be safe to call and return nil.
	if err := (NoopRecorder{}).RecordUsage(context.Background(), "w", "m", "u", Usage{}, "chat"); err != nil {
		t.Errorf("noop recorder errored: %v", err)
	}
}
