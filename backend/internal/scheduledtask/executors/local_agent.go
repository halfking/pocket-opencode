package executors

// local_agent.go — scheduledtask Kind "local_agent" executor。
//
// 职责：把 scheduled task 的 payload 翻译为 orchestrator.LocalAgentDispatcher
// 的输入，调用本地智能体运行时执行，并返回 scheduledtask.Result。
//
// 关键约束：
//   - 不直接调用 localagent.Runtime（保持 executor 与运行时解耦），
//     而是依赖 orchestrator.LocalDispatcher 接口；这样既符合 server 装配
//     的依赖反转原则，也便于在测试中替换为 mock。
//   - payload 形态：
//       {
//         "prompt":      "用户消息",
//         "context":     { ... },            // 可选，结构化上下文
//         "skills":      ["skill-a", ...],   // 可选
//         "type":        "text|code|complex|workflow",
//         "strategy":    "local_first|local_only|...",
//         "max_tokens":  1024                // 可选
//       }
//   - 失败语义：LocalAgentDispatcher 内部已经把本地执行超时与 orchestrator
//     策略考虑在内；本 executor 只负责"翻译 + 调用"，不做兜底（兜底属于
//     编排器的责任，保持职责清晰）。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/orchestrator"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// LocalDispatcherProvider 是注入的本地分发器提供方。Server 在装配时把
// *orchestrator.Orchestrator 的 LocalDispatcher 字段暴露出来；测试则
// 可以换成 mock。
type LocalDispatcherProvider interface {
	Local() orchestrator.LocalDispatcher
}

// LocalAgentExecutor 持有 LocalDispatcher 的 provider，按 Kind 执行任务。
type LocalAgentExecutor struct {
	provider LocalDispatcherProvider
}

func NewLocalAgentExecutor(p LocalDispatcherProvider) *LocalAgentExecutor {
	return &LocalAgentExecutor{provider: p}
}

func (e *LocalAgentExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindLocalAgent
}

type localAgentPayload struct {
	Prompt    string                 `json:"prompt"`
	Context   map[string]interface{} `json:"context,omitempty"`
	Skills    []string               `json:"skills,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Strategy  string                 `json:"strategy,omitempty"`
	MaxTokens int                    `json:"max_tokens,omitempty"`
}

func (e *LocalAgentExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.provider == nil {
		return nil, fmt.Errorf("local agent provider is not configured")
	}
	if t == nil || t.WorkspaceID == "" || t.UserID == "" {
		return nil, fmt.Errorf("local agent task requires workspace and user")
	}
	dispatcher := e.provider.Local()
	if dispatcher == nil || !dispatcher.IsAvailable() {
		return nil, fmt.Errorf("local agent runtime is not available")
	}

	var p localAgentPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode local_agent payload: %w", err)
	}
	if p.Prompt == "" {
		return nil, fmt.Errorf("local_agent payload: prompt is required")
	}

	// task ID 优先使用 scheduler 提供的 Task.ID；为空时由 orchestrator 生成。
	// Strategy 是 orchestrator 层的概念；本 executor 只接受"是否仅本地"
	// 这一简单信号：local_agent 严格走本地（local_only 等价语义）。
	task := &orchestrator.Task{
		ID:          t.ID,
		WorkspaceID: t.WorkspaceID,
		UserID:      t.UserID,
		Type:        p.Type,
		Prompt:      p.Prompt,
		Context:     p.Context,
		Skills:      p.Skills,
		Metadata: map[string]interface{}{
			"source":     "scheduled_task",
			"task_name":  t.Name,
			"max_tokens": p.MaxTokens,
		},
	}

	result, err := dispatcher.Dispatch(ctx, task)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("local agent dispatcher returned nil result")
	}

	// 包成 Result.Output；error 字段由 scheduler 持久化。
	outputJSON, err := json.Marshal(map[string]interface{}{
		"task_id":      result.TaskID,
		"status":       result.Status,
		"output":       result.Output,
		"executed_by":  result.ExecutedBy,
		"usage_tokens": result.UsageTokens,
		"duration_ms":  result.DurationMs,
		"metadata":     result.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal local_agent result: %w", err)
	}
	return &scheduledtask.Result{Output: outputJSON}, nil
}
