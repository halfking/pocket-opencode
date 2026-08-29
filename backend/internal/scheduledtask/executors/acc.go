package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// ACCClient is the typed subset of the ACC MCP client used by this executor.
// It deliberately exposes only the supported tool methods; the payload cannot
// select arbitrary JSON-RPC methods.
type ACCClient interface {
	TenantID() string
	GetRemoteTasks(ctx context.Context, status string, limit int) ([]mcp.ParsedTask, error)
	CreateTask(ctx context.Context, args map[string]interface{}) (string, error)
	ClaimTask(ctx context.Context, args map[string]interface{}) (string, error)
	CompleteTask(ctx context.Context, args map[string]interface{}) (string, error)
	ReportSession(ctx context.Context, args map[string]interface{}) (string, error)
}

type ACCMCPExecutor struct{ client ACCClient }

func NewACCMCPExecutor(client ACCClient) *ACCMCPExecutor { return &ACCMCPExecutor{client: client} }
func (e *ACCMCPExecutor) Kind() scheduledtask.Kind       { return scheduledtask.KindACCMCP }

type accPayload struct {
	Tool   string                 `json:"tool"`
	Args   map[string]interface{} `json:"args"`
	Status string                 `json:"status,omitempty"`
	Limit  int                    `json:"limit,omitempty"`
}

func (e *ACCMCPExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("ACC MCP client is not configured")
	}
	if t == nil || strings.TrimSpace(t.WorkspaceID) == "" || strings.TrimSpace(t.UserID) == "" {
		return nil, fmt.Errorf("ACC task owner and workspace are required")
	}
	if tenantID := strings.TrimSpace(e.client.TenantID()); tenantID == "" || tenantID != t.WorkspaceID {
		return nil, fmt.Errorf("ACC tenant mismatch for workspace %q", t.WorkspaceID)
	}
	var p accPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode ACC payload: %w", err)
	}
	p.Tool = strings.TrimSpace(p.Tool)
	if p.Args == nil {
		p.Args = map[string]interface{}{}
	}
	var (
		out string
		err error
	)
	switch p.Tool {
	case mcp.ToolGetTasks:
		limit := p.Limit
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		var tasks []mcp.ParsedTask
		tasks, err = e.client.GetRemoteTasks(ctx, p.Status, limit)
		if err == nil {
			b, marshalErr := json.Marshal(tasks)
			if marshalErr != nil {
				err = marshalErr
			} else {
				out = string(b)
			}
		}
	case mcp.ToolCreateTask:
		out, err = e.client.CreateTask(ctx, p.Args)
	case mcp.ToolTaskClaim:
		out, err = e.client.ClaimTask(ctx, p.Args)
	case mcp.ToolTaskComplete:
		out, err = e.client.CompleteTask(ctx, p.Args)
	case mcp.ToolReportSession:
		out, err = e.client.ReportSession(ctx, p.Args)
	default:
		return nil, fmt.Errorf("unsupported ACC tool %q", p.Tool)
	}
	if err != nil {
		return nil, err
	}
	return &scheduledtask.Result{Output: json.RawMessage(mustJSON(out))}, nil
}

func mustJSON(s string) []byte {
	var raw json.RawMessage
	if json.Unmarshal([]byte(s), &raw) == nil {
		return []byte(s)
	}
	b, _ := json.Marshal(s)
	return b
}
