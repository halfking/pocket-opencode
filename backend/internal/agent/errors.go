package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Error 是 AgentAdapter 的结构化错误。
//
// 替代之前的 OpenCodeError，复用 kxmemory.Error 的设计思想：
//   - Code 字段让前端区分错误类型
//   - CanRetry 字段让前端决定是否自动重试（避免和 error.Is 的 CanRetry 方法冲突）
//   - StatusCode 透传上游 HTTP 状态（如果有）
//   - Cause 保留底层 error 用于 errors.Is/As
//
// Code 字段语义（前端可基于此展示不同 UI）：
//   - "AGENT_UNREACHABLE"   — transport 失败（dial tcp / DNS / 连接拒绝 / WS 断开）
//   - "AGENT_TIMEOUT"        — context deadline / 读写超时
//   - "AGENT_UPSTREAM"       — agent 返回 5xx
//   - "AGENT_BAD_REQUEST"    — agent 返回 4xx
//   - "AGENT_PROTOCOL"       — JSON-RPC 帧错误 / ACP schema 违反
//   - "AGENT_CAPABILITY"     — agent 不支持请求的能力（如 LoadSession not supported）
//   - "AGENT_CANCELLED"      — 用户主动取消
type Error struct {
	Code       string
	Message    string
	Cause      error
	CanRetry   bool
	StatusCode int // 上游 HTTP 状态码（如果有）
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

// Unwrap 支持 errors.Is / errors.As。
func (e *Error) Unwrap() error { return e.Cause }

// WithStatus 补充上游 HTTP 状态码并自动调整 Code 字段。
func (e *Error) WithStatus(code int) *Error {
	e.StatusCode = code
	switch {
	case code >= 500:
		e.Code = "AGENT_UPSTREAM"
		e.CanRetry = true
	case code >= 400:
		e.Code = "AGENT_BAD_REQUEST"
		e.CanRetry = false
	}
	return e
}

// ---- 构造器 ----

// NewUnreachableError 包装 dial/network/WS 失败 → retryable=true。
func NewUnreachableError(cause error) *Error {
	return &Error{
		Code:     "AGENT_UNREACHABLE",
		Message:  "agent unreachable",
		Cause:    cause,
		CanRetry: true,
	}
}

// NewTimeoutError 包装 context deadline 或读写超时 → retryable=true。
func NewTimeoutError(cause error) *Error {
	return &Error{
		Code:     "AGENT_TIMEOUT",
		Message:  "agent request timed out",
		Cause:    cause,
		CanRetry: true,
	}
}

// NewUpstreamError 包装 5xx 上游错误 → retryable=true。
func NewUpstreamError(statusCode int, body string, cause error) *Error {
	msg := fmt.Sprintf("agent returned %d", statusCode)
	if body != "" {
		msg += ": " + truncateStr(body, 200)
	}
	return &Error{
		Code:       "AGENT_UPSTREAM",
		Message:    msg,
		Cause:      cause,
		CanRetry:   true,
		StatusCode: statusCode,
	}
}

// NewBadRequestError 包装 4xx 客户端错误 → retryable=false。
func NewBadRequestError(statusCode int, body string, cause error) *Error {
	msg := fmt.Sprintf("agent returned %d", statusCode)
	if body != "" {
		msg += ": " + truncateStr(body, 200)
	}
	return &Error{
		Code:       "AGENT_BAD_REQUEST",
		Message:    msg,
		Cause:      cause,
		CanRetry:   false,
		StatusCode: statusCode,
	}
}

// NewProtocolError 包装 JSON-RPC 帧错误 / schema 违反 → 通常 retryable=false。
func NewProtocolError(cause error) *Error {
	return &Error{
		Code:     "AGENT_PROTOCOL",
		Message:  "agent protocol error",
		Cause:    cause,
		CanRetry: false,
	}
}

// NewCapabilityError 表示 agent 不支持请求的能力（永久错误）。
func NewCapabilityError(cap string) *Error {
	return &Error{
		Code:     "AGENT_CAPABILITY",
		Message:  fmt.Sprintf("agent does not support capability: %s", cap),
		CanRetry: false,
	}
}

// NewCancelledError 用户主动取消。
func NewCancelledError() *Error {
	return &Error{
		Code:     "AGENT_CANCELLED",
		Message:  "agent request cancelled",
		CanRetry: false,
	}
}

// ---- 错误分类 ----

// ClassifyNetworkError 把 net / context 错误分类到 AgentError。
//
// 复用 kxmemory.classifyNetworkError 模式：net 包错误通常指 dial 阶段
// 失败（unreachable），context deadline 指请求被取消（timeout）。
func ClassifyNetworkError(err error) *Error {
	if err == nil {
		return nil
	}
	// context.DeadlineExceeded → timeout
	if errors.Is(err, context.DeadlineExceeded) {
		return NewTimeoutError(err)
	}
	// 其他网络错误 → unreachable
	if isNetError(err) {
		return NewUnreachableError(err)
	}
	// 兜底：未知错误归 unreachable
	return NewUnreachableError(err)
}

// isNetError 检测是否是网络层错误（用于分类）。
func isNetError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}

// truncateStr 截断字符串到 maxLen（用于错误日志脱敏）。
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// 编译期检查 *Error 实现了 error 接口。
var _ error = (*Error)(nil)
