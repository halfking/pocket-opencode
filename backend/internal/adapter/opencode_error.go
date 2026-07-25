package adapter

import (
	"errors"
	"fmt"
	"net/http"
)

// OpenCodeError 表示 OpenCode HTTP adapter 的标准化错误。
//
// 它通过 errors.As 透传到上层，translateOpenCodeError 会把它映射成
// agent.Error，前端按统一 code 字段渲染。
type OpenCodeError struct {
	Code       string // 业务错误码，例如 OPENCODE_UNREACHABLE / OPENCODE_TIMEOUT
	Message    string // 人类可读消息
	Cause      error  // 底层错误，可选
	Retryable  bool   // 是否允许重试
	StatusCode int    // 上游 HTTP 状态码（0 表示非 HTTP 来源）
}

// Error 实现 error 接口。
func (e *OpenCodeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("opencode %s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("opencode %s: %s", e.Code, e.Message)
}

// Unwrap 让 errors.Is / errors.As 能下钻到 Cause。
func (e *OpenCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is 允许用 errors.Is 匹配同 Code 的 OpenCodeError。
func (e *OpenCodeError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	var other *OpenCodeError
	if errors.As(target, &other) && other != nil {
		return other.Code == e.Code
	}
	return false
}

// 常用 OpenCodeError 工厂方法。

// NewUnreachable 创建 OPENCODE_UNREACHABLE 错误。
func NewUnreachable(cause error) *OpenCodeError {
	return &OpenCodeError{
		Code:      "OPENCODE_UNREACHABLE",
		Message:   "opencode instance unreachable",
		Cause:     cause,
		Retryable: true,
	}
}

// NewTimeout 创建 OPENCODE_TIMEOUT 错误。
func NewTimeout(cause error) *OpenCodeError {
	return &OpenCodeError{
		Code:      "OPENCODE_TIMEOUT",
		Message:   "opencode request timeout",
		Cause:     cause,
		Retryable: true,
	}
}

// NewUpstream 创建 OPENCODE_UPSTREAM 错误。
func NewUpstream(statusCode int, message string, cause error) *OpenCodeError {
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &OpenCodeError{
		Code:       "OPENCODE_UPSTREAM",
		Message:    message,
		Cause:      cause,
		Retryable:  statusCode >= 500,
		StatusCode: statusCode,
	}
}

// NewBadRequest 创建 OPENCODE_BAD_REQUEST 错误。
func NewBadRequest(statusCode int, message string, cause error) *OpenCodeError {
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &OpenCodeError{
		Code:       "OPENCODE_BAD_REQUEST",
		Message:    message,
		Cause:      cause,
		Retryable:  false,
		StatusCode: statusCode,
	}
}
