package agent

// transport.go — Transport 抽象接口
//
// Transport 是 JSON-RPC 2.0 over 不同底层协议的统一抽象。三种实现：
//   - StdioTransport  — 子进程 stdin/stdout
//   - HTTPTransport   — Streamable HTTP（POST /acp + SSE）
//   - WSTransport     — WebSocket /acp
//
// 公共职责：
//   - Start(): 建子进程 / 打开 HTTP 连接 / WS upgrade
//   - Call(): 阻塞发送 request 等 response（按 id 匹配）
//   - Notify(): 非阻塞发送 notification
//   - Recv(): 接收下一帧（notification 或 server-initiated request）
//
// 设计要点：
//   - 所有方法 ctx 优先（超时/取消立刻传播）
//   - Call 必须在 transport 启动后才能调用；否则返回 ProtocolError
//   - Recv 是单消费者：每个 transport 实例只能被一个 goroutine 调 Recv
//   - 多 Call 可并发（id 唯一分配），由 PendingCalls 在内部做匹配

import (
	"context"
	"encoding/json"
)

// Transport 是 JSON-RPC 2.0 over 不同底层协议的统一抽象。
type Transport interface {
	// Start 启动 transport（建子进程 / 打开 HTTP 连接 / WS upgrade）。
	// 必须在 Call/Notify/Recv 之前调用。
	Start(ctx context.Context) error

	// Close 关闭并清理。
	Close() error

	// Call 发送 JSON-RPC request 阻塞等待 response。
	// params 可以是 nil、结构体、map、json.RawMessage。
	// out 必须是非 nil 指针。
	Call(ctx context.Context, method string, params any, out any) error

	// Notify 发送 JSON-RPC notification（不等待 response）。
	Notify(ctx context.Context, method string, params any) error

	// Recv 阻塞接收下一帧（agent → client 的 notification 或 server-initiated request）。
	// 返回 raw JSON 帧；调用方用 ParseFrame 分类。
	//
	// 单消费者：一个 transport 实例只有一个 goroutine 应该调 Recv。
	// 多消费者场景需 transport 内部做 fan-out（目前未实现，留给上层）。
	Recv(ctx context.Context) ([]byte, error)
}

// TransportConfig 是构造 transport 的配置（被各具体实现的 NewXxxTransport 解析）。
//
// 不强制所有字段；不同 transport 用不同子集。
type TransportConfig struct {
	// AgentPath 是 stdio transport 的可执行文件路径。
	AgentPath string

	// AgentArgs 是 stdio transport 启动参数。
	AgentArgs []string

	// AgentEnv 是 stdio transport 环境变量（key=value 形式）。
	AgentEnv []string

	// BaseURL 是 HTTP/WS transport 的 base URL。
	BaseURL string

	// AuthToken 是 HTTP/WS transport 的 Authorization Bearer。
	AuthToken string

	// InsecureSkipVerify 是 HTTP/WS transport 的 TLS 配置（开发用）。
	InsecureSkipVerify bool

	// Headers 是 HTTP/WS transport 的额外请求头。
	Headers map[string]string

	// Logger 是 transport 的 stderr / 调试日志输出（用于 stdio 转发子进程日志）。
	Logger func(string)

	// DialTimeoutSec 是 transport 启动超时（秒）。
	DialTimeoutSec int
}

// rawFrame 是从 Recv 返回的原始帧（带类型提示，便于调用方路由）。
type rawFrame struct {
	raw     []byte
	isError bool // 如果 Recv 自身出错，isError=true
}

// 编译期断言：transport_stdio.go 实现的 StdioTransport 满足 Transport。
var _ Transport = (*StdioTransport)(nil)

// 编译期断言：transport_http.go 实现的 HTTPTransport 满足 Transport。
var _ Transport = (*HTTPTransport)(nil)

// 编译期断言：transport_ws.go 实现的 WSTransport 满足 Transport。
var _ Transport = (*WSTransport)(nil)

// encodeJSON 是 Call/Notify 共用的 JSON 编码 helper。
func encodeJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
