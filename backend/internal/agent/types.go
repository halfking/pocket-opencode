// Package agent 提供与编程 agent 通信的通用抽象层。
//
// 设计目标：让 pocketd 通过同一套接口与任何实现 ACP（Agent Client Protocol）
// 的 agent 通信，包括 OpenCode（HTTP 适配）、Codex / Claude Code / Gemini CLI
// （stdio JSON-RPC 2.0）、以及未来任何 HTTP/WebSocket transport 接入的 agent。
//
// 三个核心抽象：
//   - AgentAdapter  接口 — handler 层只与这个交互，屏蔽 transport 细节
//   - Transport     接口 — JSON-RPC 2.0 over stdio / HTTP / WS 统一接口
//   - AgentRef     结构 — 标识一个具体 agent 实例（含 type + target）
//
// 协议参考：https://agentclientprotocol.com（Zed Industries 主导）
//
// 本包取代原 internal/adapter.OpenCodeAdapter。新代码应使用 AgentAdapter。
package agent

import (
	"time"
)

// AgentRef 标识一个具体的 agent 实例。
//
// Type 取值：
//   - "opencode"   — OpenCode HTTP API（兼容旧路径）
//   - "acp-stdio"  — ACP over stdio 子进程
//   - "acp-http"   — ACP over Streamable HTTP
//   - "acp-ws"     — ACP over WebSocket
//   - "mock"       — 测试用 mock adapter
//
// Target 是连接目标：
//   - opencode:  base URL（如 "http://localhost:4096"）
//   - acp-stdio: agent 可执行文件路径（如 "/usr/local/bin/claude-code"）
//   - acp-http:  base URL（如 "https://api.example.com"）
//   - acp-ws:    WebSocket URL（如 "ws://localhost:8080/acp"）
type AgentRef struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

// String 实现 fmt.Stringer 接口。
func (r AgentRef) String() string {
	return r.Type + ":" + r.Target
}

// IsValid 验证 AgentRef 是否合法（非空 type+target）。
func (r AgentRef) IsValid() bool {
	return r.Type != "" && r.Target != ""
}

// AgentSession 是 agent 的会话抽象（不绑定特定 agent 协议）。
type AgentSession struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`          // "idle" | "busy" | "retry" | "error"
	Agent      string         `json:"agent,omitempty"` // agent 名称（"opencode" | "codex" 等）
	WorkingDir string         `json:"workingDir,omitempty"`
	CreatedAt  time.Time      `json:"createdAt,omitempty"`
	UpdatedAt  time.Time      `json:"updatedAt,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// AgentMessage 是 agent 单条消息抽象。
type AgentMessage struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	Role      string         `json:"role"` // "user" | "assistant" | "system"
	Parts     []ContentBlock `json:"parts"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ContentBlock 是消息内容的多态 union。
//
// Type 取值（ACP v1）：
//   - "text"          — 纯文本
//   - "image"         — 图片（URL 或 base64）
//   - "audio"         — 音频（URL 或 base64）
//   - "resource_link" — 资源链接
//   - "tool_use"      — 工具调用（OpenCode 内部用）
//   - "tool_result"   — 工具结果（OpenCode 内部用）
type ContentBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	URL      string         `json:"url,omitempty"`
	MimeType string         `json:"mimeType,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// AgentEvent 是 agent 流式事件抽象（对应 ACP session/update notification）。
type AgentEvent struct {
	Type      string         `json:"type"` // "message_chunk" | "tool_call" | "tool_call_update" | "permission_request" | "done"
	SessionID string         `json:"sessionId,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"` // type-specific payload
}

// PermissionRequest 是 agent 向用户请求授权（对应 ACP session/request_permission）。
type PermissionRequest struct {
	ID        string             `json:"id"`
	SessionID string             `json:"sessionId"`
	Tool      string             `json:"tool,omitempty"`
	Action    string             `json:"action"`
	Reason    string             `json:"reason,omitempty"`
	Options   []PermissionOption `json:"options,omitempty"`
	Metadata  map[string]any     `json:"metadata,omitempty"`
}

// PermissionOption 是权限选项（如 "allow once" / "allow always" / "deny"）。
type PermissionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PermissionDecision 是用户对权限请求的回复。
type PermissionDecision struct {
	OptionID string `json:"optionId"` // "allow_once" | "allow_always" | "deny"
	Message  string `json:"message,omitempty"`
}

// Question 是 agent 向用户提问（对应 ACP questions request）。
type Question struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Prompt    string           `json:"prompt"`
	Options   []QuestionOption `json:"options"`
	Multi     bool             `json:"multi,omitempty"` // 多选 vs 单选
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

// QuestionOption 是问题选项。
type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// QuestionAnswer 是用户对单个问题的回答。
type QuestionAnswer struct {
	OptionIDs []string `json:"optionIds"`
	Comment   string   `json:"comment,omitempty"`
}

// AgentCapabilities 描述 adapter 支持的能力。
//
// 用于前端按能力显示不同 UI（"不支持 delete" → 隐藏删除按钮）。
type AgentCapabilities struct {
	LoadSession     bool `json:"loadSession,omitempty"`
	ListSessions    bool `json:"listSessions,omitempty"`
	DeleteSession   bool `json:"deleteSession,omitempty"`
	SetMode         bool `json:"setMode,omitempty"`
	SetConfigOption bool `json:"setConfigOption,omitempty"`
	PromptImage     bool `json:"promptImage,omitempty"`
	PromptAudio     bool `json:"promptAudio,omitempty"`
	PromptEmbedCtx  bool `json:"promptEmbeddedContext,omitempty"`
	MCPHTTP         bool `json:"mcpHttp,omitempty"`
	MCPSSE          bool `json:"mcpSse,omitempty"`
	Permission      bool `json:"permission,omitempty"` // 是否实现 PermissionCapable
	Question        bool `json:"question,omitempty"`   // 是否实现 QuestionCapable
	Streaming       bool `json:"streaming,omitempty"`  // 是否支持 SubscribeEvents
}

// ListOptions 是 List* 方法的通用选项。
type ListOptions struct {
	Limit  int    `json:"limit,omitempty"`
	Order  string `json:"order,omitempty"` // "asc" | "desc"
	After  string `json:"after,omitempty"` // cursor / 时间戳
	Domain string `json:"domain,omitempty"`
}

// CreateSessionRequest 是创建会话请求。
type CreateSessionRequest struct {
	Title      string         `json:"title,omitempty"`
	Agent      string         `json:"agent,omitempty"`
	Model      string         `json:"model,omitempty"`
	ParentID   string         `json:"parentId,omitempty"` // fork 时填父 session ID
	WorkingDir string         `json:"workingDir,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// SendPromptRequest 是发送 prompt 请求。
type SendPromptRequest struct {
	Text     string         `json:"text,omitempty"`  // 简单文本（最常用）
	Parts    []ContentBlock `json:"parts,omitempty"` // 多模态时使用
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SendPromptResult 是发送 prompt 的结果。
type SendPromptResult struct {
	MessageID  string `json:"messageId,omitempty"`
	Enqueued   bool   `json:"enqueued,omitempty"`
	StopReason string `json:"stopReason,omitempty"` // ACP: end_turn | max_tokens | cancelled | refusal
}

// Mode 是会话运行模式（ACP session/set_mode 的 modeId）。
type Mode struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}
