package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/agentbridge"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// AgentBridgeClient keeps full-session AI dispatch behind the narrow method
// needed by scheduled tasks.
type AgentBridgeClient interface {
	Send(context.Context, string, string, agentbridge.SendOptions) (*agentbridge.SendResult, error)
}

type AgentBridgeExecutor struct{ bridge AgentBridgeClient }

func NewAgentBridgeExecutor(bridge AgentBridgeClient) *AgentBridgeExecutor {
	return &AgentBridgeExecutor{bridge: bridge}
}
func (*AgentBridgeExecutor) Kind() scheduledtask.Kind { return scheduledtask.KindAgentBridge }

type agentPayload struct {
	AgentID    string `json:"agentId"`
	Prompt     string `json:"prompt"`
	Role       string `json:"role,omitempty"`
	AgentName  string `json:"agentName,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Directory  string `json:"directory,omitempty"`
}

func (e *AgentBridgeExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.bridge == nil {
		return nil, fmt.Errorf("agent bridge is not configured")
	}
	var p agentPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode agent payload: %w", err)
	}
	if p.AgentID == "" || strings.TrimSpace(p.Prompt) == "" {
		return nil, fmt.Errorf("agentId and prompt are required")
	}
	res, err := e.bridge.Send(ctx, p.AgentID, p.Prompt, agentbridge.SendOptions{WorkspaceID: t.WorkspaceID, Role: p.Role, AgentName: p.AgentName, ModelID: p.ModelID, ProviderID: p.ProviderID, Directory: p.Directory})
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	return &scheduledtask.Result{Output: out, ReferencedTaskID: res.TaskID}, nil
}

type LLMBFFClient interface {
	Chat(context.Context, llmbff.ChatRequest, string) (*llmbff.ChatResponse, error)
}
type LLMBFFExecutor struct{ service LLMBFFClient }

func NewLLMBFFExecutor(service LLMBFFClient) *LLMBFFExecutor {
	return &LLMBFFExecutor{service: service}
}
func (*LLMBFFExecutor) Kind() scheduledtask.Kind { return scheduledtask.KindLLMBFFSummary }

type llmPayload struct {
	Model       string           `json:"model,omitempty"`
	Messages    []llmbff.Message `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"maxTokens,omitempty"`
	Kind        string           `json:"kind,omitempty"`
}

func (e *LLMBFFExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.service == nil {
		return nil, fmt.Errorf("llmbff service is not configured")
	}
	var p llmPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode llmbff payload: %w", err)
	}
	if len(p.Messages) == 0 {
		return nil, fmt.Errorf("messages are required")
	}
	kind := p.Kind
	if kind == "" {
		kind = "scheduled_summary"
	}
	res, err := e.service.Chat(ctx, llmbff.ChatRequest{WorkspaceID: t.WorkspaceID, User: t.UserID, Model: p.Model, Messages: p.Messages, Temperature: p.Temperature, MaxTokens: p.MaxTokens}, kind)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	return &scheduledtask.Result{Output: out}, nil
}

type KxmemoryExecutor struct{ client kxmemory.Client }

func NewKxmemoryExecutor(client kxmemory.Client) *KxmemoryExecutor {
	return &KxmemoryExecutor{client: client}
}
func (*KxmemoryExecutor) Kind() scheduledtask.Kind { return scheduledtask.KindKxmemorySummary }

type kxmemoryPayload struct {
	Date   string                            `json:"date,omitempty"`
	Emails []kxmemory.EmailForClassification `json:"emails,omitempty"`
}

func (e *KxmemoryExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("kxmemory client is not configured")
	}
	var p kxmemoryPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode kxmemory payload: %w", err)
	}
	if p.Date == "" {
		p.Date = time.Now().Format("2006-01-02")
	}
	res, err := e.client.DailySummary(ctx, kxmemory.DailySummaryRequest{Date: p.Date, Emails: p.Emails})
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	return &scheduledtask.Result{Output: out}, nil
}
