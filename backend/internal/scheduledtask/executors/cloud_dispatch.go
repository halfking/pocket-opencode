package executors

// cloud_dispatch.go — scheduledtask Kind "cloud_dispatch" executor。
//
// 职责：把 scheduled task payload 翻译为 orchestrator.CloudDispatcher
// 的输入并调用云端 ACC。
//
// 与 local_agent 的差异：
//   - 失败语义不同：本地超时后云端兜底是 orchestrator 的责任，cloud_dispatch
//     默认只在云端不可用或返回 error 时失败；不会自行切换到本地。
//   - payload 形态相同（共享 localAgentPayload），便于前端复用表单。
//   - Strategy 字段透传，orchestrator 在 CloudOnly / CloudFirst 路径使用。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/orchestrator"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// CloudDispatcherProvider 由 Server 在装配时注入。
type CloudDispatcherProvider interface {
	Cloud() orchestrator.CloudDispatcher
}

type CloudDispatchExecutor struct {
	provider CloudDispatcherProvider
}

func NewCloudDispatchExecutor(p CloudDispatcherProvider) *CloudDispatchExecutor {
	return &CloudDispatchExecutor{provider: p}
}

func (e *CloudDispatchExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindCloudDispatch
}

func (e *CloudDispatchExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.provider == nil {
		return nil, fmt.Errorf("cloud dispatch provider is not configured")
	}
	if t == nil || t.WorkspaceID == "" || t.UserID == "" {
		return nil, fmt.Errorf("cloud dispatch task requires workspace and user")
	}
	dispatcher := e.provider.Cloud()
	if dispatcher == nil || !dispatcher.IsAvailable() {
		return nil, fmt.Errorf("cloud dispatcher is not available")
	}

	var p localAgentPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode cloud_dispatch payload: %w", err)
	}
	if p.Prompt == "" {
		return nil, fmt.Errorf("cloud_dispatch payload: prompt is required")
	}

	task := &orchestrator.Task{
		ID:          t.ID,
		WorkspaceID: t.WorkspaceID,
		UserID:      t.UserID,
		Type:        p.Type,
		Prompt:      p.Prompt,
		Context:     p.Context,
		Skills:      p.Skills,
		Metadata: map[string]interface{}{
			"source":    "scheduled_task",
			"task_name": t.Name,
		},
	}

	result, err := dispatcher.Dispatch(ctx, task)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("cloud dispatcher returned nil result")
	}

	outputJSON, err := json.Marshal(map[string]interface{}{
		"task_id":      result.TaskID,
		"status":       result.Status,
		"output":       result.Output,
		"executed_by":  result.ExecutedBy,
		"usage_tokens": result.UsageTokens,
		"duration_ms":  result.DurationMs,
		"fallback":     result.FallbackUsed,
		"metadata":     result.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cloud_dispatch result: %w", err)
	}
	return &scheduledtask.Result{Output: outputJSON}, nil
}
