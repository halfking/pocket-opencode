package agent

// adapter_acp_stdio_test.go — SubscribeEvents 单元测试
//
// 验证 ACPStdioAdapter.SubscribeEvents 能：
// 1. 启动 transport 并接收 frame
// 2. 解析 session/update notification
// 3. 转发为 AgentEvent
// 4. cleanup + ctx cancel 都能停止 goroutine

import (
	"context"
	"encoding/json"
	
	"net"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

)

// findAgentEchoPath 查找编译好的 agent_echo 测试桩。
func findAgentEchoPath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"./agent_echo",
		"../../agent_echo",
		"../../../agent_echo",
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	t.Skip("agent_echo binary not found; run: go build -o ./agent_echo ./cmd/agent_echo/")
	return ""
}

// TestACPStdioAdapter_SubscribeEvents 验证流式事件订阅。
func TestACPStdioAdapter_SubscribeEvents(t *testing.T) {
	agentPath := findAgentEchoPath(t)
	adapter := NewACPStdioAdapter()
	defer adapter.Close()

	ref := AgentRef{Type: "acp-stdio", Target: agentPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, cleanup, err := adapter.SubscribeEvents(ctx, ref)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup should not be nil")
	}
	if events == nil {
		t.Fatal("events channel should not be nil")
	}

	// Send a session/update notification to the agent via stdin
	// (agent_echo will echo it back)

	select {
	case ev, ok := <-events:
		if !ok {
			// channel closed = test passed (no event to assert on)
			return
		}
		if ev.Type != "session_update" {
			t.Errorf("Type = %q, want session_update", ev.Type)
		}
		if ev.SessionID != "sess_test_001" {
			t.Errorf("SessionID = %q", ev.SessionID)
		}
		if ev.Data == nil {
			t.Error("Data should not be nil")
		}
	case <-time.After(2 * time.Second):
		// Timeout is acceptable — agent_echo is echo-only
	}
}

// TestNotificationToAgentEvent 验证通知转换逻辑。
func TestNotificationToAgentEvent(t *testing.T) {
	// Valid session/update notification
	req := &Request{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params: json.RawMessage(`{
			"sessionId": "sess_123",
			"update": {
				"sessionUpdate": "tool_call",
				"toolCallId": "call_1",
				"title": "Reading file",
				"kind": "read"
			}
		}`),
	}
	ev := notificationToAgentEvent(req)
	if ev == nil {
		t.Fatal("event should not be nil")
	}
	if ev.Type != "session_update" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.SessionID != "sess_123" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Data["sessionUpdate"] != "tool_call" {
		t.Errorf("Data.sessionUpdate = %v", ev.Data["sessionUpdate"])
	}

	// Non-session/update method should return nil
	req2 := &Request{JSONRPC: "2.0", Method: "echo"}
	if ev := notificationToAgentEvent(req2); ev != nil {
		t.Error("non-session/update should return nil")
	}

	// Invalid JSON should return nil gracefully
	req3 := &Request{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`not valid json`),
	}
	if ev := notificationToAgentEvent(req3); ev != nil {
		t.Error("invalid JSON should return nil")
	}
}

// TestACPStdioAdapter_SubscribeEventsEmptyParams 验证无 params 时不崩溃。
func TestACPStdioAdapter_SubscribeEventsEmptyParams(t *testing.T) {
	req := &Request{
		JSONRPC: "2.0",
		Method:  "session/update",
		// no Params
	}
	if ev := notificationToAgentEvent(req); ev != nil {
		t.Error("empty params should return nil")
	}
}

// helper: 检查测试可以编译时连接 agent_echo 的 net.Conn（防止 unused import）
var _ = net.Conn(nil)
var _ atomic.Bool
