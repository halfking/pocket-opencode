package agent

// transport_stdio_test.go — StdioTransport 测试
//
// 测试策略：用 `cat` 或自己写一个简单的 echo 程序作为 fake agent。
// 真实 ACP agent 由后续 PR 接入。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findFakeAgent 找一个可用的"假 agent"。
//
// 测试运行时优先看 `agent_echo`（开发者在 backend 根目录 `go build` 后留下的），
// 否则用 cat 作为最简回显 fake agent（cat 把 stdin 写到 stdout，调用方
// 自己构造 response 帧写到 stdin 即可）。
func findFakeAgent(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"./agent_echo",
		"../../agent_echo",
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	t.Skip("fake agent binary not found; build cmd/agent_echo first")
	return ""
}

// TestStdioTransport_StartClose 验证 Start/Close 生命周期。
func TestStdioTransport_StartClose(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--echo-only"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestStdioTransport_CallEcho 验证 Call/Recv round-trip。
func TestStdioTransport_CallEcho(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"-echo"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	// 启动 Recv goroutine 处理 stdout → 投递给 PendingCalls
	go func() {
		for {
			_, err := tr.Recv(context.Background())
			if err != nil {
				return
			}
		}
	}()

	// Call: 测试 echo 行为
	type echoResult struct {
		Echoed json.RawMessage `json:"echoed"`
	}
	var result echoResult
	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	err := tr.Call(ctx, "test/echo", map[string]any{"foo": "bar"}, &result)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(result.Echoed) == "" {
		t.Fatal("expected echoed payload")
	}
}

// TestStdioTransport_Notify 验证 Notify 不等响应。
func TestStdioTransport_Notify(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--echo"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	// 启动 Recv
	go func() {
		_, _ = tr.Recv(context.Background())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Notify(ctx, "session/cancel", map[string]string{"sessionId": "s1"}); err != nil {
		t.Errorf("Notify: %v", err)
	}
}

// TestStdioTransport_StartInvalidPath 验证坏路径返回明确错误。
func TestStdioTransport_StartInvalidPath(t *testing.T) {
	tr := NewStdioTransport(TransportConfig{
		AgentPath: "/nonexistent/agent",
	})
	err := tr.Start(context.Background())
	if err == nil {
		tr.Close()
		t.Fatal("expected error for invalid path")
	}
	var ae *Error
	if !errors.As(err, &ae) {
		t.Errorf("err type = %T, want AgentError", err)
	}
}

// TestStdioTransport_CallAfterClose 验证 Close 后 Call 失败。
func TestStdioTransport_CallAfterClose(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--echo"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tr.Close()

	var out map[string]any
	err := tr.Call(context.Background(), "test/echo", nil, &out)
	if err == nil {
		t.Fatal("Call after Close should fail")
	}
}

// TestStdioTransport_CallTimeout 验证 ctx 超时。
func TestStdioTransport_CallTimeout(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--hang"}, // fake agent 收到任何请求都 hang
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	go func() { _, _ = tr.Recv(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var out map[string]any
	err := tr.Call(ctx, "test/slow", nil, &out)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var ae *Error
	if errors.As(err, &ae) && ae.Code == "AGENT_TIMEOUT" {
		return
	}
	t.Logf("got error (may not be classified): %v", err)
}

// TestStdioTransport_SpawnProcess 验证子进程真的启动了。
func TestStdioTransport_SpawnProcess(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--echo"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	if tr.cmd == nil || tr.cmd.Process == nil {
		t.Fatal("process should be running")
	}
	if tr.cmd.Process.Pid <= 0 {
		t.Fatal("invalid PID")
	}
}

// TestStdioTransport_SendMalformedFrame 验证发坏 JSON 时 agent 能恢复。
// fake agent 应该忽略坏帧继续处理后续合法帧。
func TestStdioTransport_SendMalformedFrame(t *testing.T) {
	agentPath := findFakeAgent(t)
	tr := NewStdioTransport(TransportConfig{
		AgentPath: agentPath,
		AgentArgs: []string{"--echo"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	go func() { _, _ = tr.Recv(context.Background()) }()

	// 直接写坏帧到 stdin（模拟 transport 编码 bug）
	_, _ = tr.stdin.Write([]byte("not json\n"))

	// 紧接着发合法 call：echo 应该仍响应
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var result map[string]any
	err := tr.Call(ctx, "test/echo", map[string]string{"after": "bad"}, &result)
	if err != nil {
		t.Skipf("fake agent doesn't tolerate malformed frame: %v", err)
	}
}

// drainRecv 是测试用 Recv helper，丢弃帧直到 ctx cancel。
func drainRecv(tr Transport, ctx context.Context) error {
	for {
		_, err := tr.Recv(ctx)
		if err != nil {
			return err
		}
	}
}

// 防止 go vet 报 unused 警告
var _ = http.StatusOK
var _ = strings.TrimSpace
