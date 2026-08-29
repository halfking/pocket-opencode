package executors

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// RedClawChatClient is the subset of the RedClaw bridge used by the executor.
// Keeping this as an interface makes the executor deterministic in unit tests
// and prevents it from depending on the bridge's health-loop implementation.
type RedClawChatClient interface {
	Chat(redclaw.ChatRequest) (*redclaw.ChatResponse, error)
}

type RedClawChatExecutor struct{ client RedClawChatClient }

func NewRedClawChatExecutor(client RedClawChatClient) *RedClawChatExecutor {
	return &RedClawChatExecutor{client: client}
}

func (e *RedClawChatExecutor) Kind() scheduledtask.Kind { return scheduledtask.KindRedClawChat }

// redClawChatPayload intentionally accepts only the fields that are safe and
// useful for an automation. Tenant and user identity always come from the
// persisted task owner; payload cannot impersonate another tenant/user.
type redClawChatPayload struct {
	Model    string            `json:"model,omitempty"`
	Messages []redclaw.Message `json:"messages"`
}

func (e *RedClawChatExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("redclaw chat client is not configured")
	}
	if t == nil || t.UserID == "" || t.WorkspaceID == "" {
		return nil, fmt.Errorf("redclaw chat task owner and workspace are required")
	}
	var payload redClawChatPayload
	if err := json.Unmarshal(t.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode redclaw chat payload: %w", err)
	}
	if len(payload.Messages) == 0 {
		return nil, fmt.Errorf("redclaw chat payload requires messages")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	resp, err := e.client.Chat(redclaw.ChatRequest{
		TenantID: t.WorkspaceID,
		UserID:   t.UserID,
		Model:    payload.Model,
		Messages: payload.Messages,
	})
	if err != nil {
		return nil, err
	}
	output, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode redclaw chat result: %w", err)
	}
	return &scheduledtask.Result{Output: output}, nil
}

type RedClawKnowledgeClient interface {
	KnowledgeSearch(redclaw.KnowledgeSearchRequest) (*redclaw.KnowledgeSearchResponse, error)
}

type RedClawKnowledgeExecutor struct{ client RedClawKnowledgeClient }

func NewRedClawKnowledgeExecutor(client RedClawKnowledgeClient) *RedClawKnowledgeExecutor {
	return &RedClawKnowledgeExecutor{client: client}
}

func (e *RedClawKnowledgeExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindRedClawKnowledge
}

type redClawKnowledgePayload struct {
	Query string `json:"query"`
	TopK  int    `json:"topK,omitempty"`
}

func (e *RedClawKnowledgeExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("redclaw knowledge client is not configured")
	}
	if t == nil || t.WorkspaceID == "" || t.UserID == "" {
		return nil, fmt.Errorf("redclaw knowledge task owner and workspace are required")
	}
	var payload redClawKnowledgePayload
	if err := json.Unmarshal(t.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode redclaw knowledge payload: %w", err)
	}
	if payload.Query == "" {
		return nil, fmt.Errorf("redclaw knowledge payload requires query")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	resp, err := e.client.KnowledgeSearch(redclaw.KnowledgeSearchRequest{TenantID: t.WorkspaceID, Query: payload.Query, TopK: payload.TopK})
	if err != nil {
		return nil, err
	}
	output, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode redclaw knowledge result: %w", err)
	}
	return &scheduledtask.Result{Output: output}, nil
}
