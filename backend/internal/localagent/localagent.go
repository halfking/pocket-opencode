package localagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Package localagent 实现 OpenPocket 本地轻量级智能体运行时。
//
// 设计目标：
//   - 在移动端/桌面端 pocketd 进程内嵌入一个轻量级 agent 循环，能够执行
//     简单任务（文本转换、本地工具调用、MCP 技能）而无需派发到云端 ACC。
//   - 多后端支持：WASM（sandbox 沙箱）、Python（embedded interpreter）、
//     ACP stdio（fork 子进程，如 claude-code CLI）。
//   - 参考 Pi Agent 设计：AgentLoop + StreamFn + 事件驱动 + 队列式
//     steering/follow-up（本骨架先落地事件流与结果聚合，指令插队机制留待
//     真实后端接入时按需补充）。
//
// 架构：
//   1. Backend 接口：不同运行时（WASM/Python/ACP）实现同一份执行契约
//   2. Runtime：持有一个 Backend，做技能加载 + 任务执行 + 事件聚合
//   3. SkillLoader：从 marketplace 下载并加载技能包到 Runtime
//   4. Orchestrator 集成：LocalAgentDispatcher 把 Runtime 包装成
//      orchestrator.LocalDispatcher
//
// 当前实现：骨架 + MockBackend（真实 WASM/Python/ACP 运行时需后续 sprint 集成）。

// AgentTask 是本地 agent 执行的任务单元。
type AgentTask struct {
	ID          string                 `json:"id"`
	Prompt      string                 `json:"prompt"`
	Context     map[string]interface{} `json:"context,omitempty"`
	Skills      []string               `json:"skills,omitempty"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Temperature float64                `json:"temperature,omitempty"`
}

// AgentEvent 是 agent 循环产生的流式事件（类似 Pi Agent 的 AgentEvent）。
type AgentEvent struct {
	Type      string                 `json:"type"` // "thinking" | "tool_call" | "text_delta" | "done" | "error"
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// AgentResult 是任务完成后的最终结果。
type AgentResult struct {
	TaskID      string                 `json:"task_id"`
	Output      string                 `json:"output"`
	Status      string                 `json:"status"` // "success" | "error" | "timeout"
	Error       string                 `json:"error,omitempty"`
	UsageTokens int                    `json:"usage_tokens,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Backend 是 agent 运行时后端接口。不同后端（WASM/Python/ACP）实现此接口。
type Backend interface {
	// Name 返回后端名称（"wasm" | "python" | "acp-stdio" | "mock"）。
	Name() string
	// Execute 执行任务，返回事件流 channel。channel 必须在任务结束（成功/
	// 失败/取消）后关闭，否则 Runtime.Execute 的聚合循环会永久阻塞。
	Execute(ctx context.Context, task *AgentTask) (<-chan AgentEvent, error)
	// LoadSkill 加载技能到运行时。
	LoadSkill(ctx context.Context, skillID, skillPath string) error
	// Close 释放后端资源。
	Close() error
}

// Runtime 是本地 agent 运行时管理器：持有一个 Backend，负责技能注册表与
// 任务执行时的事件聚合。
type Runtime struct {
	backend Backend
	skills  map[string]string // skillID -> skillPath
	mu      sync.RWMutex
}

// NewRuntime 创建本地 agent 运行时。
func NewRuntime(backend Backend) *Runtime {
	return &Runtime{
		backend: backend,
		skills:  make(map[string]string),
	}
}

// LoadSkill 加载技能到运行时并记录到本地注册表。
func (r *Runtime) LoadSkill(ctx context.Context, skillID, skillPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.backend == nil {
		return errors.New("localagent: backend not configured")
	}
	if err := r.backend.LoadSkill(ctx, skillID, skillPath); err != nil {
		return fmt.Errorf("localagent: load skill %s: %w", skillID, err)
	}
	r.skills[skillID] = skillPath
	return nil
}

// LoadedSkills 返回当前已加载的技能 ID 列表（用于 UI 展示 / 调试）。
func (r *Runtime) LoadedSkills() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.skills))
	for id := range r.skills {
		out = append(out, id)
	}
	return out
}

// Execute 执行任务：驱动后端产生事件流，聚合为最终结果。
func (r *Runtime) Execute(ctx context.Context, task *AgentTask) (*AgentResult, error) {
	r.mu.RLock()
	backend := r.backend
	r.mu.RUnlock()

	if backend == nil {
		return nil, errors.New("localagent: backend not configured")
	}
	if task == nil {
		return nil, errors.New("localagent: task is nil")
	}

	eventChan, err := backend.Execute(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("localagent: execute task: %w", err)
	}

	var output string
	status := "success"
	var errorMsg string
	var usageTokens int

	for event := range eventChan {
		switch event.Type {
		case "text_delta":
			if text, ok := event.Data["text"].(string); ok {
				output += text
			}
		case "done":
			if result, ok := event.Data["result"].(string); ok {
				output = result
			}
			if tokens, ok := event.Data["tokens"].(int); ok {
				usageTokens = tokens
			}
		case "error":
			status = "error"
			if msg, ok := event.Data["message"].(string); ok {
				errorMsg = msg
			}
		}
	}

	return &AgentResult{
		TaskID:      task.ID,
		Output:      output,
		Status:      status,
		Error:       errorMsg,
		UsageTokens: usageTokens,
	}, nil
}

// Close 关闭运行时并释放后端资源。
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backend != nil {
		return r.backend.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// MockBackend — 用于测试与本地开发演示，不依赖真实运行时。
// ---------------------------------------------------------------------------

// MockBackend 是用于测试的 mock 后端：逐字符回显 prompt，模拟流式输出。
type MockBackend struct {
	name string
}

// NewMockBackend 创建 mock 后端。
func NewMockBackend(name string) *MockBackend {
	return &MockBackend{name: name}
}

func (m *MockBackend) Name() string { return m.name }

func (m *MockBackend) Execute(ctx context.Context, task *AgentTask) (<-chan AgentEvent, error) {
	eventChan := make(chan AgentEvent, 10)

	go func() {
		defer close(eventChan)

		select {
		case eventChan <- AgentEvent{Type: "thinking", Timestamp: time.Now(), Data: map[string]interface{}{"text": "Analyzing task..."}}:
		case <-ctx.Done():
			return
		}

		result := fmt.Sprintf("Mock response for: %s (backend: %s)", task.Prompt, m.name)
		for i := 0; i < len(result); i += 32 {
			end := i + 32
			if end > len(result) {
				end = len(result)
			}
			select {
			case eventChan <- AgentEvent{Type: "text_delta", Timestamp: time.Now(), Data: map[string]interface{}{"text": result[i:end]}}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case eventChan <- AgentEvent{Type: "done", Timestamp: time.Now(), Data: map[string]interface{}{
			"result": result,
			"tokens": len(result) / 4,
		}}:
		case <-ctx.Done():
		}
	}()

	return eventChan, nil
}

func (m *MockBackend) LoadSkill(ctx context.Context, skillID, skillPath string) error {
	return nil
}

func (m *MockBackend) Close() error { return nil }

// ---------------------------------------------------------------------------
// WASMBackend — 骨架，实际运行时需集成 wazero/wasmer。
// ---------------------------------------------------------------------------

// WASMBackend 是 WASM sandbox 运行时后端（骨架）。
type WASMBackend struct{}

// NewWASMBackend 创建 WASM 后端骨架。
func NewWASMBackend() *WASMBackend { return &WASMBackend{} }

func (w *WASMBackend) Name() string { return "wasm" }

func (w *WASMBackend) Execute(ctx context.Context, task *AgentTask) (<-chan AgentEvent, error) {
	return nil, errors.New("localagent: wasm backend not implemented yet")
}

func (w *WASMBackend) LoadSkill(ctx context.Context, skillID, skillPath string) error {
	return errors.New("localagent: wasm backend not implemented yet")
}

func (w *WASMBackend) Close() error { return nil }

// ---------------------------------------------------------------------------
// PythonBackend — 骨架，需集成嵌入式 Python 解释器或子进程调用。
// ---------------------------------------------------------------------------

// PythonBackend 是 Python 运行时后端（骨架）。
type PythonBackend struct{}

// NewPythonBackend 创建 Python 后端骨架。
func NewPythonBackend() *PythonBackend { return &PythonBackend{} }

func (p *PythonBackend) Name() string { return "python" }

func (p *PythonBackend) Execute(ctx context.Context, task *AgentTask) (<-chan AgentEvent, error) {
	return nil, errors.New("localagent: python backend not implemented yet")
}

func (p *PythonBackend) LoadSkill(ctx context.Context, skillID, skillPath string) error {
	return errors.New("localagent: python backend not implemented yet")
}

func (p *PythonBackend) Close() error { return nil }

// ---------------------------------------------------------------------------
// ACPStdioBackend — 骨架，实际实现应复用 internal/agent 的 ACP 适配器。
// ---------------------------------------------------------------------------

// ACPStdioBackend 是 ACP over stdio 后端（fork 子进程，如 claude-code CLI）。
type ACPStdioBackend struct {
	command string
}

// NewACPStdioBackend 创建 ACP stdio 后端骨架。
func NewACPStdioBackend(command string) *ACPStdioBackend {
	return &ACPStdioBackend{command: command}
}

func (a *ACPStdioBackend) Name() string { return "acp-stdio" }

func (a *ACPStdioBackend) Execute(ctx context.Context, task *AgentTask) (<-chan AgentEvent, error) {
	return nil, errors.New("localagent: acp-stdio backend not implemented yet")
}

func (a *ACPStdioBackend) LoadSkill(ctx context.Context, skillID, skillPath string) error {
	return errors.New("localagent: acp-stdio backend not implemented yet")
}

func (a *ACPStdioBackend) Close() error { return nil }

// ---------------------------------------------------------------------------
// SkillLoader — 从技能市场下载并加载技能包。
// ---------------------------------------------------------------------------

// SkillLoader 从技能市场加载技能包到 Runtime。
type SkillLoader struct {
	marketplaceBaseURL string
	runtime            *Runtime
}

// NewSkillLoader 创建技能加载器。
func NewSkillLoader(marketplaceBaseURL string, runtime *Runtime) *SkillLoader {
	return &SkillLoader{marketplaceBaseURL: marketplaceBaseURL, runtime: runtime}
}

// LoadSkillFromMarketplace 从市场下载并加载技能（骨架）。
//
// 真实实现需要：
//  1. 通过 releaseID 从 marketplace API 下载技能包 blob
//  2. 解压到本地缓存目录并校验 digest/签名
//  3. 调用 Runtime.LoadSkill 完成注册
func (s *SkillLoader) LoadSkillFromMarketplace(ctx context.Context, skillID string) error {
	return errors.New("localagent: skill loader not implemented yet")
}

var _ Backend = (*MockBackend)(nil)
var _ Backend = (*WASMBackend)(nil)
var _ Backend = (*PythonBackend)(nil)
var _ Backend = (*ACPStdioBackend)(nil)
