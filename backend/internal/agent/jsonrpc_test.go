package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestIDAllocator_Unique 验证 IDAllocator 分配唯一递增 ID。
func TestIDAllocator_Unique(t *testing.T) {
	a := NewIDAllocator()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := a.Next()
		s := string(id)
		if seen[s] {
			t.Fatalf("duplicate id: %s", s)
		}
		seen[s] = true
	}
}

// TestMarshalRequest_Notification 验证 notification 无 id 字段。
func TestMarshalRequest_Notification(t *testing.T) {
	b, err := MarshalNotification("session/cancel", map[string]string{"sessionId": "s1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `"id"`) {
		t.Errorf("notification should not have id field, got: %s", s)
	}
	if !strings.Contains(s, `"session/cancel"`) {
		t.Errorf("missing method: %s", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("frame must end with newline (stdio framing): %q", s)
	}
}

// TestMarshalRequest_Call 验证 call 有 id 字段。
func TestMarshalRequest_Call(t *testing.T) {
	id := jsonRaw(t, `"abc"`)
	b, err := MarshalRequest(id, "session/new", map[string]string{"cwd": "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"id":"abc"`) {
		t.Errorf("missing id field: %s", s)
	}
	if !strings.Contains(s, `"session/new"`) {
		t.Errorf("missing method: %s", s)
	}
}

// TestMarshalResponse 验证成功响应格式。
func TestMarshalResponse(t *testing.T) {
	id := jsonRaw(t, `"42"`)
	b, err := MarshalResponse(id, map[string]string{"sessionId": "sess_1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"id":"42"`) {
		t.Errorf("missing id: %s", s)
	}
	if !strings.Contains(s, `"result"`) {
		t.Errorf("missing result: %s", s)
	}
	if strings.Contains(s, `"error"`) {
		t.Errorf("success response should not have error field: %s", s)
	}
}

// TestMarshalError 验证错误响应格式。
func TestMarshalError(t *testing.T) {
	id := jsonRaw(t, `"42"`)
	b, err := MarshalError(id, CodeMethodNotFound, "method not found", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"code":-32601`) {
		t.Errorf("missing code: %s", s)
	}
	if !strings.Contains(s, `"method not found"`) {
		t.Errorf("missing message: %s", s)
	}
}

// TestParseFrame_Request 验证请求帧解析。
func TestParseFrame_Request(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":"1","method":"session/new","params":{"cwd":"/tmp"}}`)
	frameType, req, resp, errResp, err := ParseFrame(line)
	if err != nil {
		t.Fatal(err)
	}
	if frameType != "request" {
		t.Errorf("frameType = %q", frameType)
	}
	if req == nil {
		t.Fatal("req is nil")
	}
	if req.Method != "session/new" {
		t.Errorf("method = %q", req.Method)
	}
	if resp != nil || errResp != nil {
		t.Errorf("expected only req, got resp=%v errResp=%v", resp, errResp)
	}
}

// TestParseFrame_Notification 验证 notification 帧解析（无 id）。
func TestParseFrame_Notification(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`)
	frameType, req, resp, errResp, err := ParseFrame(line)
	if err != nil {
		t.Fatal(err)
	}
	if frameType != "notification" {
		t.Errorf("frameType = %q, want notification", frameType)
	}
	if req == nil || req.Method != "session/cancel" {
		t.Errorf("req = %+v", req)
	}
	if resp != nil || errResp != nil {
		t.Errorf("expected only req, got resp=%v errResp=%v", resp, errResp)
	}
}

// TestParseFrame_Response 验证响应帧解析。
func TestParseFrame_Response(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":"1","result":{"sessionId":"sess_1"}}`)
	frameType, req, resp, errResp, err := ParseFrame(line)
	if err != nil {
		t.Fatal(err)
	}
	if frameType != "response" {
		t.Errorf("frameType = %q", frameType)
	}
	if resp == nil || resp.Result == nil {
		t.Fatal("resp.result is nil")
	}
	if req != nil || errResp != nil {
		t.Errorf("expected only resp, got req=%v errResp=%v", req, errResp)
	}
}

// TestParseFrame_ErrorResponse 验证 error 响应帧。
func TestParseFrame_ErrorResponse(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"method not found"}}`)
	frameType, _, _, errResp, err := ParseFrame(line)
	_ = err
	if frameType != "error" {
		t.Errorf("frameType = %q", frameType)
	}
	if errResp == nil {
		t.Fatal("errResp is nil")
	}
	if errResp.Error.Code != -32601 {
		t.Errorf("Code = %d", errResp.Error.Code)
	}
}

// TestParseFrame_Empty 验证空行返回错误。
func TestParseFrame_Empty(t *testing.T) {
	_, _, _, _, err := ParseFrame([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty frame")
	}
}

// TestPendingCalls_Deliver 验证 PendingCalls 投递。
func TestPendingCalls_Deliver(t *testing.T) {
	pc := NewPendingCalls()
	id := jsonRaw(t, `"1"`)
	ch := pc.Register(id)

	go func() {
		// 模拟 transport：构造 response 投递
		resp := &Response{JSONRPC: "2.0", ID: id, Result: jsonRaw(t, `"ok"`)}
		pc.Deliver(resp)
	}()

	select {
	case resp := <-ch:
		if resp == nil || len(resp.Result) == 0 {
			t.Fatal("expected response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delivery")
	}

	if pc.PendingCount() != 0 {
		t.Errorf("pending count = %d, want 0", pc.PendingCount())
	}
}

// TestPendingCalls_DeliverUnknown 验证投递未知 id 不阻塞。
func TestPendingCalls_DeliverUnknown(t *testing.T) {
	pc := NewPendingCalls()
	id := jsonRaw(t, `"unknown"`)
	resp := &Response{JSONRPC: "2.0", ID: id}
	if pc.Deliver(resp) {
		t.Fatal("Deliver should return false for unknown id")
	}
}

// TestPendingCalls_Cancel 验证 cancel 关闭所有 pending。
func TestPendingCalls_Cancel(t *testing.T) {
	pc := NewPendingCalls()
	id := jsonRaw(t, `"1"`)
	ch := pc.Register(id)
	pc.Cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after Cancel")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

// TestError_ClassifyNetwork 验证网络错误分类。
func TestError_ClassifyNetwork(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if e := ClassifyNetworkError(nil); e != nil {
			t.Errorf("expected nil, got %+v", e)
		}
	})
	t.Run("context deadline", func(t *testing.T) {
		e := ClassifyNetworkError(context.DeadlineExceeded)
		if e.Code != "AGENT_TIMEOUT" {
			t.Errorf("Code = %q", e.Code)
		}
		if !e.CanRetry {
			t.Error("should be retryable")
		}
	})
}

// TestError_WithStatus 验证 WithStatus 自动设置 code。
func TestError_WithStatus(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		e := (&Error{}).WithStatus(502)
		if e.Code != "AGENT_UPSTREAM" {
			t.Errorf("Code = %q", e.Code)
		}
		if !e.CanRetry {
			t.Error("5xx should be retryable")
		}
	})
	t.Run("4xx", func(t *testing.T) {
		e := (&Error{}).WithStatus(404)
		if e.Code != "AGENT_BAD_REQUEST" {
			t.Errorf("Code = %q", e.Code)
		}
		if e.CanRetry {
			t.Error("4xx should not be retryable")
		}
	})
	t.Run("2xx ignored", func(t *testing.T) {
		e := (&Error{}).WithStatus(200)
		if e.Code != "" {
			t.Errorf("Code should remain empty for 2xx, got %q", e.Code)
		}
	})
}

// TestError_Unwrap 验证 errors.As/Is 链路。
func TestError_Unwrap(t *testing.T) {
	cause := context.DeadlineExceeded
	e := NewTimeoutError(cause)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("errors.Is should find cause")
	}
	var ae *Error
	if !errors.As(e, &ae) {
		t.Fatal("errors.As should find Error")
	}
	if ae.Code != "AGENT_TIMEOUT" {
		t.Errorf("Code = %q", ae.Code)
	}
}

// jsonRaw 返回字面 JSON 字节（运行时 sanity check）。
func jsonRaw(t *testing.T, s string) []byte {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("invalid JSON: %s", s)
	}
	return []byte(s)
}
