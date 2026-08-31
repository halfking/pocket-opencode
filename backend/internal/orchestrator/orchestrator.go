package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/localagent"
)

// Package orchestrator 实现"移动分布式AI工作平台"的本地/云端编排器。
//
// 设计目标：
//   - 统一任务分发入口：接收任务，按策略决定执行位置（本地 LocalAgent
//     vs 云端 ACC）。
//   - 三种基础策略 + 智能混合：LocalFirst（优先本地，失败兜底云端）、
//     CloudFirst（优先云端，失败兜底本地）、Hybrid（按任务复杂度启发式
//     决策）、以及仅本地/仅云端的强制模式（离线模式 / 省电模式）。
//   - 与 scheduledtask 集成：新增 Kind "local_agent" 和 "cloud_dispatch"
//     （由 server 层在装配 executor 时接入，本包只提供分发原语）。
//
// 架构：
//   1. Orchestrator：持有 LocalDispatcher + CloudDispatcher，Dispatch()
//      按 Strategy 选择路径，并在启用兜底时做失败重试。
//   2. LocalDispatcher / CloudDispatcher 接口：分别包装 localagent.Runtime
//      与云端 ACC API，互不感知对方存在。
//   3. LocalAgentDispatcher：把 localagent.Runtime 适配成 LocalDispatcher。
//   4. ACCDispatcher：云端 ACC HTTP 客户端骨架（真实调用留待后续 sprint）。

// Task 是编排器处理的任务单元。
type Task struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspace_id"`
	UserID      string                 `json:"user_id"`
	Type        string                 `json:"type"` // "text" | "code" | "complex" | "workflow"
	Prompt      string                 `json:"prompt"`
	Context     map[string]interface{} `json:"context,omitempty"`
	Skills      []string               `json:"skills,omitempty"`
	Priority    int                    `json:"priority,omitempty"` // 0-10
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Strategy 是分发策略。
type Strategy string

const (
	LocalFirst Strategy = "local_first" // 优先本地，失败后云端兜底
	CloudFirst Strategy = "cloud_first" // 优先云端，失败后本地兜底
	Hybrid     Strategy = "hybrid"      // 按任务复杂度启发式决策
	LocalOnly  Strategy = "local_only"  // 仅本地（离线模式）
	CloudOnly  Strategy = "cloud_only"  // 仅云端（移动端省电模式）
)

// Result 是任务执行结果。
type Result struct {
	TaskID       string                 `json:"task_id"`
	Status       string                 `json:"status"` // "success" | "error" | "timeout"
	Output       string                 `json:"output"`
	Error        string                 `json:"error,omitempty"`
	ExecutedBy   string                 `json:"executed_by"` // "local" | "cloud"
	UsageTokens  int                    `json:"usage_tokens,omitempty"`
	DurationMs   int64                  `json:"duration_ms"`
	FallbackUsed bool                   `json:"fallback_used,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// LocalDispatcher 本地分发器接口。
type LocalDispatcher interface {
	Dispatch(ctx context.Context, task *Task) (*Result, error)
	IsAvailable() bool
}

// CloudDispatcher 云端分发器接口。
type CloudDispatcher interface {
	Dispatch(ctx context.Context, task *Task) (*Result, error)
	IsAvailable() bool
}

// Config 编排器配置。
type Config struct {
	DefaultStrategy           Strategy `json:"default_strategy"`
	LocalTimeoutMs            int64    `json:"local_timeout_ms"`            // 本地执行超时（毫秒）
	CloudTimeoutMs            int64    `json:"cloud_timeout_ms"`            // 云端执行超时（毫秒）
	EnableFallback            bool     `json:"enable_fallback"`             // 是否启用兜底链
	HybridComplexityThreshold int      `json:"hybrid_complexity_threshold"` // Hybrid 模式复杂度阈值（0-100）
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	return &Config{
		DefaultStrategy:           LocalFirst,
		LocalTimeoutMs:            30000,  // 30s
		CloudTimeoutMs:            120000, // 2min
		EnableFallback:            true,
		HybridComplexityThreshold: 50,
	}
}

// Orchestrator 是编排器主结构。
type Orchestrator struct {
	local  LocalDispatcher
	cloud  CloudDispatcher
	config *Config
}

// New 创建编排器。config 为 nil 时使用 DefaultConfig。
func New(local LocalDispatcher, cloud CloudDispatcher, config *Config) *Orchestrator {
	if config == nil {
		config = DefaultConfig()
	}
	return &Orchestrator{local: local, cloud: cloud, config: config}
}

// Dispatch 按策略分发任务。strategy 为空时使用 config.DefaultStrategy。
func (o *Orchestrator) Dispatch(ctx context.Context, task *Task, strategy Strategy) (*Result, error) {
	if task == nil {
		return nil, errors.New("orchestrator: task is nil")
	}
	if strategy == "" {
		strategy = o.config.DefaultStrategy
	}

	startTime := time.Now()

	switch strategy {
	case LocalFirst:
		return o.dispatchLocalFirst(ctx, task, startTime)
	case CloudFirst:
		return o.dispatchCloudFirst(ctx, task, startTime)
	case Hybrid:
		return o.dispatchHybrid(ctx, task, startTime)
	case LocalOnly:
		return o.dispatchLocal(ctx, task, startTime)
	case CloudOnly:
		return o.dispatchCloud(ctx, task, startTime)
	default:
		return nil, fmt.Errorf("orchestrator: unknown strategy %q", strategy)
	}
}

func (o *Orchestrator) dispatchLocalFirst(ctx context.Context, task *Task, startTime time.Time) (*Result, error) {
	if o.local != nil && o.local.IsAvailable() {
		result, err := o.dispatchLocal(ctx, task, startTime)
		// 兜底链触发条件:本地执行出错 或 结果状态非 success。
		// 此前仅看 err==nil && status=="success",在 status=="error" 但
		// err==nil 的情况下(error result 是某些 dispatcher 的合法返回,
		// 例如 LocalAgentDispatcher.Runtime.Execute 返回带 error 状态的
		// AgentResult)不会触发兜底 — 这是 bug。
		localSucceeded := err == nil && result != nil && result.Status == "success"
		if localSucceeded {
			return result, nil
		}
		if o.config.EnableFallback && o.cloud != nil && o.cloud.IsAvailable() {
			cloudResult, cloudErr := o.dispatchCloud(ctx, task, startTime)
			if cloudErr == nil {
				cloudResult.FallbackUsed = true
				return cloudResult, nil
			}
		}
		return result, err
	}

	if o.cloud != nil && o.cloud.IsAvailable() {
		return o.dispatchCloud(ctx, task, startTime)
	}

	return nil, errors.New("orchestrator: no dispatcher available")
}

func (o *Orchestrator) dispatchCloudFirst(ctx context.Context, task *Task, startTime time.Time) (*Result, error) {
	if o.cloud != nil && o.cloud.IsAvailable() {
		result, err := o.dispatchCloud(ctx, task, startTime)
		cloudSucceeded := err == nil && result != nil && result.Status == "success"
		if cloudSucceeded {
			return result, nil
		}
		if o.config.EnableFallback && o.local != nil && o.local.IsAvailable() {
			localResult, localErr := o.dispatchLocal(ctx, task, startTime)
			if localErr == nil {
				localResult.FallbackUsed = true
				return localResult, nil
			}
		}
		return result, err
	}

	if o.local != nil && o.local.IsAvailable() {
		return o.dispatchLocal(ctx, task, startTime)
	}

	return nil, errors.New("orchestrator: no dispatcher available")
}

func (o *Orchestrator) dispatchHybrid(ctx context.Context, task *Task, startTime time.Time) (*Result, error) {
	complexity := o.estimateComplexity(task)
	if complexity < o.config.HybridComplexityThreshold {
		return o.dispatchLocalFirst(ctx, task, startTime)
	}
	return o.dispatchCloudFirst(ctx, task, startTime)
}

// estimateComplexity 用一个简单的启发式给任务打分（0-100）：prompt 长度 +
// 任务类型权重 + 所需技能数量。这是 v1 的粗粒度信号，真实场景应替换为
// 基于历史执行时长/资源占用的统计模型。
func (o *Orchestrator) estimateComplexity(task *Task) int {
	complexity := len(task.Prompt) / 20

	switch task.Type {
	case "text":
		complexity += 10
	case "code":
		complexity += 30
	case "complex":
		complexity += 60
	case "workflow":
		complexity += 80
	}

	complexity += len(task.Skills) * 5

	if complexity > 100 {
		complexity = 100
	}
	return complexity
}

func (o *Orchestrator) dispatchLocal(ctx context.Context, task *Task, startTime time.Time) (*Result, error) {
	if o.local == nil || !o.local.IsAvailable() {
		return nil, errors.New("orchestrator: local dispatcher not available")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(o.config.LocalTimeoutMs)*time.Millisecond)
	defer cancel()

	result, err := o.local.Dispatch(timeoutCtx, task)
	if err != nil {
		// 错误时仅返回 err,不再构造 Result{Status:error, Error:...} —
		// 调用方依靠 err 判断与走兜底链;Result.Error 容易在 WS / 审计层
		// 意外暴露底层 dispatcher 内部错误文本。
		return nil, err
	}

	result.DurationMs = time.Since(startTime).Milliseconds()
	return result, nil
}

func (o *Orchestrator) dispatchCloud(ctx context.Context, task *Task, startTime time.Time) (*Result, error) {
	if o.cloud == nil || !o.cloud.IsAvailable() {
		return nil, errors.New("orchestrator: cloud dispatcher not available")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(o.config.CloudTimeoutMs)*time.Millisecond)
	defer cancel()

	result, err := o.cloud.Dispatch(timeoutCtx, task)
	if err != nil {
		// 错误时不构造带 Error 文本的 result — 调用方只通过 err 判断。
		// 避免把底层 dispatcher(可能是 ACC)的内部错误文本泄露到上层
		// Result.Error 字段,被序列化进 WS 事件或审计日志。
		return nil, err
	}

	result.DurationMs = time.Since(startTime).Milliseconds()
	return result, nil
}

// ---------------------------------------------------------------------------
// LocalAgentDispatcher — 把 localagent.Runtime 适配成 LocalDispatcher。
// ---------------------------------------------------------------------------

// LocalAgentDispatcher 把 localagent.Runtime 包装成 orchestrator.LocalDispatcher。
type LocalAgentDispatcher struct {
	runtime *localagent.Runtime
}

// NewLocalAgentDispatcher 创建本地 agent 分发器。
func NewLocalAgentDispatcher(runtime *localagent.Runtime) *LocalAgentDispatcher {
	return &LocalAgentDispatcher{runtime: runtime}
}

func (d *LocalAgentDispatcher) Dispatch(ctx context.Context, task *Task) (*Result, error) {
	if d.runtime == nil {
		return nil, errors.New("local dispatcher: runtime not configured")
	}

	agentTask := &localagent.AgentTask{
		ID:      task.ID,
		Prompt:  task.Prompt,
		Context: task.Context,
		Skills:  task.Skills,
	}

	agentResult, err := d.runtime.Execute(ctx, agentTask)
	if err != nil {
		return nil, err
	}

	return &Result{
		TaskID:      task.ID,
		Status:      agentResult.Status,
		Output:      agentResult.Output,
		Error:       agentResult.Error,
		ExecutedBy:  "local",
		UsageTokens: agentResult.UsageTokens,
	}, nil
}

func (d *LocalAgentDispatcher) IsAvailable() bool {
	return d.runtime != nil
}

// ---------------------------------------------------------------------------
// ACCDispatcher — 真实实现见 acc_dispatcher.go（基于 internal/mcp.Client）。
// ---------------------------------------------------------------------------
// （旧的 baseURL/apiKey 字段版本已删除；新版接受 *mcp.Client。）

// ---------------------------------------------------------------------------
// MockCloudDispatcher — 用于测试。
// ---------------------------------------------------------------------------

// MockCloudDispatcher 模拟云端分发器，用于单元测试与本地演示。
type MockCloudDispatcher struct {
	available bool
}

// NewMockCloudDispatcher 创建 mock 云端分发器。
func NewMockCloudDispatcher(available bool) *MockCloudDispatcher {
	return &MockCloudDispatcher{available: available}
}

func (d *MockCloudDispatcher) Dispatch(ctx context.Context, task *Task) (*Result, error) {
	if !d.available {
		return nil, errors.New("mock cloud: not available")
	}

	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &Result{
		TaskID:      task.ID,
		Status:      "success",
		Output:      fmt.Sprintf("Mock cloud response for: %s", task.Prompt),
		ExecutedBy:  "cloud",
		UsageTokens: len(task.Prompt) / 4,
	}, nil
}

func (d *MockCloudDispatcher) IsAvailable() bool {
	return d.available
}

var _ LocalDispatcher = (*LocalAgentDispatcher)(nil)
var _ CloudDispatcher = (*ACCDispatcher)(nil)
var _ CloudDispatcher = (*MockCloudDispatcher)(nil)
