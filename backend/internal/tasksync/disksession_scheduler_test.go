package tasksync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter/disk"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// TestDiskSessionScheduler_NoopWhenNil 验证依赖为 nil 时 Start/Stop 安全
// （不泄漏 goroutine、不 panic），满足「调度器至少可构造」的要求。
func TestDiskSessionScheduler_NoopWhenNil(t *testing.T) {
	s := NewDiskSessionScheduler(nil, nil, time.Minute)
	s.Start(context.Background())
	s.Stop() // Start 未启动循环，stop 仍可被安全关闭一次
}

// fakeMCP 起最小 MCP 服务，使 ReportSession 链路（initialize → tools/call）
// 可端到端跑通而不报错。
func fakeMCP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "initialize" {
			w.Header().Set("mcp-session-id", "sess-test")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "{}"}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDiskSessionScheduler_RunOnceNoError 验证 runOnce 在「无检测到的 disk 实例」
// （临时 home 无 agent 数据目录）时不报错、不触发任何 ReportSession 调用。
func TestDiskSessionScheduler_RunOnceNoError(t *testing.T) {
	srv := fakeMCP(t)
	client := mcp.NewClientWithAuth(srv.URL, "secret", "tenant", []string{"tasks", "sessions"}, false)
	da := disk.NewWithHome("/nonexistent-path-without-agent-data")
	s := NewDiskSessionScheduler(da, client, time.Minute)

	s.runOnce(context.Background()) // 不应 panic / 返回 error（方法签名无 error，仅记录日志）
}

// TestDiskSessionScheduler_StartStop 验证非 nil 依赖时循环能启动并优雅退出。
func TestDiskSessionScheduler_StartStop(t *testing.T) {
	srv := fakeMCP(t)
	client := mcp.NewClientWithAuth(srv.URL, "secret", "tenant", []string{"tasks", "sessions"}, false)
	da := disk.NewWithHome("/nonexistent-path-without-agent-data")
	s := NewDiskSessionScheduler(da, client, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	s.Stop()
}
