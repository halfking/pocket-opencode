package agent

import "context"

// AgentAdapter 是与编程 agent 通信的统一接口。
//
// 所有实现（OpenCode HTTP、ACP over stdio、ACP over HTTP/WS、mock 等）
// 都实现这套接口；handler 层只与 AgentAdapter 交互，不感知底层 transport。
//
// 设计原则：
//   - AgentRef 参数贯穿所有方法 — 标识调用哪个 agent 实例
//   - Optional capabilities（PermissionCapable、QuestionCapable）通过类型断言探测
//   - 所有方法都接受 context（用于超时控制）
//   - 错误统一用 *AgentError（前端可基于 code/retryable 区分）
//
// 实现这个接口的示例：
//   - *OpenCodeAdapter       — 包装 internal/adapter.OpenCodeHTTPAdapter
//   - *ACPStdioAdapter       — stdio JSON-RPC（计划中）
//   - *ACPHTTPAdapter        — Streamable HTTP（计划中）
//   - *ACPWSAdapter          — WebSocket（计划中）
//   - *MockAgentAdapter      — 测试用 mock
//
// 编译期断言：每个实现都应该有 `var _ AgentAdapter = (*YourImpl)(nil)`。
type AgentAdapter interface {
	// AdapterType 返回 adapter 类型字符串（用于 metrics/log/debug）。
	AdapterType() string

	// Capabilities 返回该 adapter 支持的能力子集（不同 agent 支持不同）。
	Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error)

	// HealthCheck 检查 agent 实例是否可达。
	HealthCheck(ctx context.Context, ref AgentRef) error

	// ---- 会话生命周期 ----

	// ListSessions 列出 agent 上的会话。
	ListSessions(ctx context.Context, ref AgentRef, opts ListOptions) ([]AgentSession, error)

	// CreateSession 创建新会话。
	CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error)

	// LoadSession 加载历史会话（可选能力）。
	LoadSession(ctx context.Context, ref AgentRef, sessionID string) (*AgentSession, error)

	// DeleteSession 删除会话（可选能力）。
	DeleteSession(ctx context.Context, ref AgentRef, sessionID string) error

	// ---- 对话 ----

	// GetMessages 获取会话历史消息。
	GetMessages(ctx context.Context, ref AgentRef, sessionID string, opts ListOptions) ([]AgentMessage, error)

	// SendPrompt 发送 prompt 给会话。
	SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error)

	// InterruptSession 中断进行中的 agent 循环。
	InterruptSession(ctx context.Context, ref AgentRef, sessionID string) error

	// SetSessionMode 切换会话模式（可选能力，如 plan / code 模式）。
	SetSessionMode(ctx context.Context, ref AgentRef, sessionID, modeID string) error

	// ---- 流式事件 ----

	// SubscribeEvents 订阅 agent 的流式事件（ACP session/update notifications）。
	// 返回的 channel 在 ctx cancel 或 cleanup 调用后关闭。
	SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error)
}

// PermissionCapable 是可选能力接口 — agent 支持权限请求时实现。
//
// handler 层用类型断言探测：
//
//	if pc, ok := adapter.(PermissionCapable); ok {
//	    perms, _ := pc.ListPendingPermissions(...)
//	}
type PermissionCapable interface {
	ListPendingPermissions(ctx context.Context, ref AgentRef, sessionID string) ([]PermissionRequest, error)
	ReplyPermission(ctx context.Context, ref AgentRef, sessionID, requestID string, reply PermissionDecision) error
}

// QuestionCapable 是可选能力接口 — agent 支持提问时实现。
type QuestionCapable interface {
	ListPendingQuestions(ctx context.Context, ref AgentRef, sessionID string) ([]Question, error)
	ReplyQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string, answers []QuestionAnswer) error
	RejectQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string) error
}
