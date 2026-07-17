package agent

// codec.go — ACP 协议级类型定义（与 transport 解耦）
//
// 这些类型对应 https://agentclientprotocol.com 的 JSON Schema。
// camelCase 是 spec 强制要求。

import "encoding/json"

// ---- initialize / 握手 ----

// InitializeRequest 是 ACP initialize 方法的参数。
type InitializeRequest struct {
	ProtocolVersion    int                   `json:"protocolVersion"`
	ClientInfo         ImplementationInfo    `json:"clientInfo"`
	ClientCapabilities ClientCapabilitiesACP `json:"clientCapabilities,omitempty"`
}

// InitializeResponse 是 ACP initialize 方法的返回值。
type InitializeResponse struct {
	ProtocolVersion   int                  `json:"protocolVersion"`
	AgentInfo         ImplementationInfo   `json:"agentInfo"`
	AgentCapabilities AgentCapabilitiesACP `json:"agentCapabilities,omitempty"`
	AuthMethods       []AuthMethod         `json:"authMethods,omitempty"`
}

// ImplementationInfo 是 client/agent 元信息。
type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// ClientCapabilitiesACP 是 client 侧能力声明（注意与 AgentCapabilities 区分）。
type ClientCapabilitiesACP struct {
	FS       *FSClientCapability `json:"fs,omitempty"`
	Terminal bool                `json:"terminal,omitempty"`
}

// FSClientCapability 是文件系统能力（read/write text file）。
type FSClientCapability struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// AgentCapabilitiesACP 是 agent 侧能力声明。
type AgentCapabilitiesACP struct {
	LoadSession         bool                 `json:"loadSession,omitempty"`
	PromptCapabilities  *PromptCapabilities  `json:"promptCapabilities,omitempty"`
	MCPCapabilities     *MCPCapabilities     `json:"mcpCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
}

// PromptCapabilities 是 agent 支持的 prompt 内容类型。
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// MCPCapabilities 是 agent 支持的 MCP transport。
type MCPCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// SessionCapabilities 是 agent 支持的会话操作。
type SessionCapabilities struct {
	List   *json.RawMessage `json:"list,omitempty"`
	Delete *json.RawMessage `json:"delete,omitempty"`
}

// AuthMethod 是 ACP 支持的认证方式（v1 只定义了 agent 委托）。
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ---- session/new ----

// NewSessionRequest 是 session/new 的参数。
type NewSessionRequest struct {
	CWD        string         `json:"cwd"`
	MCPServers []MCPServer    `json:"mcpServers,omitempty"`
	Metadata   map[string]any `json:"_meta,omitempty"`
}

// NewSessionResponse 是 session/new 的返回值。
type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

// MCPServer 是 ACP 启动时注入 agent 的 MCP server 配置。
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ---- session/prompt ----

// PromptRequest 是 session/prompt 的参数。
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Metadata  map[string]any `json:"_meta,omitempty"`
}

// PromptResponse 是 session/prompt 的返回值。
type PromptResponse struct {
	StopReason string `json:"stopReason"` // end_turn | max_tokens | max_turn_requests | refusal | cancelled
}

// ---- session/cancel (notification) ----

// CancelNotification 是 session/cancel 的参数（无 id）。
type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// ---- session/update (notification) ----

// SessionUpdateNotification 是 agent → client 的 streaming notification。
type SessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate 是 union 类型，根据 SessionUpdateType 区分实际内容。
type SessionUpdate struct {
	SessionUpdateType string         `json:"sessionUpdate"` // discriminator
	Data              map[string]any `json:"-"`             // 原始 payload
}

// ---- session/request_permission ----

// RequestPermissionParams 是 agent → client 的权限请求参数。
type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	Tool      *ToolCall          `json:"toolCall,omitempty"`
	Options   []PermissionOption `json:"options"`
}

// ToolCall 是 ACP 工具调用描述（permission request 中携带）。
type ToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"` // read | edit | execute | ...
	Status     string         `json:"status,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	Locations  []any          `json:"locations,omitempty"`
}

// ---- 自定义 unmarshal（解析 union） ----

// 实现 SessionUpdate 自定义 unmarshal 不直接展开（避免每种 update 写一个 type）。
// 调用方用 map[string]any 拿到原始 JSON 字段。
//
// 如果需要类型安全，把每个 update 类型（UserMessageChunk / AgentMessageChunk
// / ToolCall / ToolCallUpdate / Plan）定义为独立 struct，在 caller 端做
// json.Unmarshal 到具体类型。这里保持灵活。
