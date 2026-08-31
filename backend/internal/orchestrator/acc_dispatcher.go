package orchestrator

// acc_dispatcher.go — 把 mcp.Client 适配成 orchestrator.CloudDispatcher，
// 把"云端任务分发"动作落到 ACC MCP 任务整合上。
//
// 行为契约：
//   - Dispatch 把 orchestrator.Task 翻译为 acc_create_task 的参数，调用
//     mcp.Client.CreateTask 创建远端任务；如果 ACC 同步返回结果，按 Result
//     返回；如果 ACC 返回 task_id 但没结果，标记 status="submitted"，由上层
//     后续轮询 / 订阅完成。
//   - IsAvailable 反映 mcp.Client 的就绪状态（baseURL 非空 + tenant 校验
//     通过）；nil client 视为不可用。
//   - 失败语义：CreateTask 错误时整个 Dispatch 失败，由 orchestrator 兜底
//     链路决策是否切换本地（这是调度策略的责任，本 dispatcher 不做兜底）。
//
// 安全：
//   - 不把 MCP API key / tenant_id 写日志；mcp.Client 自身的 callWriteTool
//     已经做了 redacted logging。
//   - ctx 取消通过 context.Context 透传到 mcp.Client，HTTP 客户端会自动
//     终止请求。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// ACCDispatcher 通过 mcp.Client 把任务派发到云端 ACC。
//
// 装配方式：
//
//	dispatcher := orchestrator.NewACCDispatcher(mcpClient)
//	orch := orchestrator.New(dispatcher, nil, nil) // 仅云端
type ACCDispatcher struct {
	client *mcp.Client
}

// NewACCDispatcher 创建 ACCDispatcher；client 为 nil 时返回 nil 指针以便
// 调用方检查 IsAvailable。
func NewACCDispatcher(client *mcp.Client) *ACCDispatcher {
	if client == nil {
		return nil
	}
	return &ACCDispatcher{client: client}
}

func (d *ACCDispatcher) Dispatch(ctx context.Context, task *Task) (*Result, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("acc dispatcher: mcp client not configured")
	}
	if task == nil {
		return nil, errors.New("acc dispatcher: task is nil")
	}
	if task.WorkspaceID == "" || task.UserID == "" {
		return nil, errors.New("acc dispatcher: workspace_id and user_id required")
	}
	if tenant := d.client.TenantID(); tenant != "" && tenant != task.WorkspaceID {
		return nil, fmt.Errorf("acc dispatcher: workspace %q does not match configured tenant %q",
			task.WorkspaceID, tenant)
	}

	// 把 orchestrator.Task 翻译为 acc_create_task 的 args。
	// key 命名与 acc-go/internal/mcp/server.go 中 acc_create_task 的入参对齐。
	args := map[string]interface{}{
		"tenant_id":     task.WorkspaceID,
		"user_id":       task.UserID,
		"task_id_local": task.ID,
		"prompt":        task.Prompt,
		"type":          task.Type,
		"skills":        task.Skills,
		"context":       task.Context,
		"priority":      task.Priority,
		"idempotency":   idempotencyKey(task),
	}

	start := time.Now()
	out, err := d.client.CreateTask(ctx, args)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return &Result{
			TaskID:     task.ID,
			Status:     "error",
			Error:      err.Error(),
			ExecutedBy: "cloud",
			DurationMs: duration,
			Metadata:   map[string]interface{}{"remote_error": true},
		}, err
	}

	return &Result{
		TaskID:     task.ID,
		Status:     "submitted",
		Output:     out,
		ExecutedBy: "cloud",
		DurationMs: duration,
		Metadata: map[string]interface{}{
			"dispatched": "acc_create_task",
			"remote":     "acc",
		},
	}, nil
}

func (d *ACCDispatcher) IsAvailable() bool {
	if d == nil || d.client == nil {
		return false
	}
	return d.client.TenantID() != ""
}

// idempotencyKey 为同一个 orchestrator.Task 派生稳定的幂等键。
// - 优先使用 task.ID；
// - ID 为空时使用 (workspaceID + userID + 类型 + prompt 前 64 字节) 哈希。
//
// 该键被 acc_create_task 用于去重；同 key 的二次提交不会在 ACC 侧产生
// 重复任务，但本 dispatcher 不保证 ACC 侧的语义（由 ACC MCP 实施）。
func idempotencyKey(t *Task) string {
	if t.ID != "" {
		return "t-" + t.ID
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "k-" + t.WorkspaceID + "-" + t.UserID + "-" + t.Type + "-" + hex.EncodeToString(b)
}
