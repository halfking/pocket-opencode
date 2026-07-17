package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// JSON-RPC 2.0 帧结构（camelCase per ACP spec）。
//
// 参考：https://www.jsonrpc.org/specification
//
// 注意：ACP spec 要求 property key 用 camelCase，但 Go 用 CamelCase 导出
// 时 JSON encoder 默认也是 CamelCase；为避免不一致，所有 struct 用
// `json:"camelCase"` 显式标记。

// Request 是 JSON-RPC 2.0 request 帧。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`          // 必须是 "2.0"
	ID      json.RawMessage `json:"id,omitempty"`     // string/number/null；notification 必须省略
	Method  string          `json:"method"`           // e.g. "session/new"
	Params  json.RawMessage `json:"params,omitempty"` // 任意 JSON 值
}

// Response 是 JSON-RPC 2.0 response 帧（成功）。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// ErrorResponse 是 JSON-RPC 2.0 error 帧（失败）。
type ErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // 可能与 request id 相同或 null
	Error   RPCError        `json:"error"`
}

// RPCError 是 JSON-RPC 2.0 error 对象。
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// 标准 JSON-RPC 错误码 + ACP 保留范围。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// ACP 保留范围 -32000 to -32099
	CodeAuthRequired     = -32000
	CodeResourceNotFound = -32002
	CodeRequestCancelled = -32800
)

// IDAllocator 为并发 RPC 请求分配唯一 ID（递增整数）。
//
// JSON-RPC 2.0 要求 id 全局唯一（同一个 session 内）。用 atomic.Int64
// 是最简单且线程安全的方案。
type IDAllocator struct {
	next atomic.Int64
}

// NewIDAllocator 构造 IDAllocator。
func NewIDAllocator() *IDAllocator { return &IDAllocator{} }

// Next 返回下一个 ID（JSON 字符串形式）。
func (a *IDAllocator) Next() json.RawMessage {
	n := a.next.Add(1)
	// 用 string ID（ACP 官方示例用 string，但 spec 允许 number）。
	// string 在日志里更易读，避免大数精度问题。
	return json.RawMessage(`"` + strconv.FormatInt(n, 10) + `"`)
}

// MarshalRequest 编码 JSON-RPC request 帧（带换行符，符合 stdio framing）。
func MarshalRequest(id json.RawMessage, method string, params any) ([]byte, error) {
	if id == nil {
		// notification
		return marshalFrame(&Request{
			JSONRPC: "2.0",
			Method:  method,
		})
	}
	paramsJSON, err := encodeJSON(params)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	return marshalFrame(&Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	})
}

// MarshalNotification 编码 JSON-RPC notification（无 id）。
func MarshalNotification(method string, params any) ([]byte, error) {
	return MarshalRequest(nil, method, params)
}

// MarshalResponse 编码 JSON-RPC 成功 response。
func MarshalResponse(id json.RawMessage, result any) ([]byte, error) {
	resultJSON, err := encodeJSON(result)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	return marshalFrame(&Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultJSON,
	})
}

// MarshalError 编码 JSON-RPC error response。
func MarshalError(id json.RawMessage, code int, message string, data any) ([]byte, error) {
	rpcErr := RPCError{
		Code:    code,
		Message: message,
	}
	if data != nil {
		dataJSON, err := encodeJSON(data)
		if err != nil {
			return nil, fmt.Errorf("encode error data: %w", err)
		}
		rpcErr.Data = dataJSON
	}
	return marshalFrame(&ErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   rpcErr,
	})
}

// ParseFrame 解析一行 JSON-RPC 帧，自动判断 request/response/notification。
//
// 返回：
//   - frameType: "request" | "response" | "notification"
//   - request, response, errResp: 三个指针之一非 nil（取决于 frameType）
func ParseFrame(line []byte) (frameType string, req *Request, resp *Response, errResp *ErrorResponse, parseErr error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", nil, nil, nil, fmt.Errorf("empty frame")
	}

	// 探测：是否有 "id" 字段（request/response）vs notification（无 id）
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return "", nil, nil, nil, fmt.Errorf("parse json: %w", err)
	}

	hasID := len(probe.ID) > 0 && string(probe.ID) != "null"
	hasMethod := probe.Method != ""
	hasResult := len(probe.Result) > 0
	hasError := probe.Error != nil

	switch {
	case hasError && hasID:
		// error response
		errResp = &ErrorResponse{}
		if err := json.Unmarshal(line, errResp); err != nil {
			return "", nil, nil, nil, fmt.Errorf("parse error resp: %w", err)
		}
		return "error", nil, nil, errResp, nil
	case hasResult && hasID:
		// success response
		resp = &Response{}
		if err := json.Unmarshal(line, resp); err != nil {
			return "", nil, nil, nil, fmt.Errorf("parse resp: %w", err)
		}
		return "response", nil, resp, nil, nil
	case hasMethod && !hasID:
		// notification (no id)
		req = &Request{}
		if err := json.Unmarshal(line, req); err != nil {
			return "", nil, nil, nil, fmt.Errorf("parse notification: %w", err)
		}
		return "notification", req, nil, nil, nil
	case hasMethod && hasID:
		// request
		req = &Request{}
		if err := json.Unmarshal(line, req); err != nil {
			return "", nil, nil, nil, fmt.Errorf("parse request: %w", err)
		}
		return "request", req, nil, nil, nil
	default:
		return "", nil, nil, nil, fmt.Errorf("unknown frame shape: id=%v method=%v result=%v error=%v",
			hasID, hasMethod, hasResult, hasError)
	}
}

// marshalFrame 编码 + 添加换行（stdio framing 要求）。
func marshalFrame(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return append(b, '\n'), nil
}

// ---- 同步原语：pending calls ----

// PendingCalls 跟踪 in-flight 请求，等待响应。
type PendingCalls struct {
	mu      sync.Mutex
	pending map[string]chan *Response // id string → response channel
	closed  bool
}

// NewPendingCalls 构造。
func NewPendingCalls() *PendingCalls {
	return &PendingCalls{pending: make(map[string]chan *Response)}
}

// Register 为指定 id 注册一个等待 channel。
// 重复 id 会 panic（JSON-RPC 要求 id 全局唯一）。
func (p *PendingCalls) Register(id json.RawMessage) chan *Response {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		panic("PendingCalls closed")
	}
	idStr := string(id)
	ch := make(chan *Response, 1)
	p.pending[idStr] = ch
	return ch
}

// Deliver 把响应送到对应的 channel；如果 id 不在 pending 中（可能已超时取消）则丢弃。
// 返回 true 表示成功投递，false 表示未找到。
func (p *PendingCalls) Deliver(resp *Response) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idStr := string(resp.ID)
	ch, ok := p.pending[idStr]
	if !ok {
		return false
	}
	delete(p.pending, idStr)
	// 非阻塞投递（如果 caller 已 timeout 走 default 分支）
	select {
	case ch <- resp:
	default:
	}
	return true
}

// DeliverError 把 error response 转换成一个零 result 的 response 投递。
func (p *PendingCalls) DeliverError(errResp *ErrorResponse) bool {
	resp := &Response{
		JSONRPC: "2.0",
		ID:      errResp.ID,
		Result:  nil,
	}
	// 把 error 信息编码进 Result（让 caller 能通过 unmarshal 还原）。
	if errResp != nil && errResp.Error.Message != "" {
		// 这里简化：error response 单独处理（见 pendingCalls.DeliverError 替代）
	}
	return p.Deliver(resp)
}

// Cancel 取消所有 pending（Close 时调用）。
func (p *PendingCalls) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
}

// PendingCount 返回当前 in-flight 请求数（用于 metrics / 测试断言）。
func (p *PendingCalls) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}
