package agent

// agentecho_helper_test.go — 测试用 fake agent（agent_echo）现编译 helper
//
// 背景：仓库曾误提交一个 macOS/arm64 构建的 backend/agent_echo 二进制，
// Linux CI 上 fork/exec 报 "exec format error"，导致 TestStdioTransport_*
// 与 TestACPStdioAdapter_SubscribeEvents 存量红（本地恰好同架构故全绿）。
// 现在测试不再探测任何预置/残留二进制，而是从 backend/cmd/agent_echo
// 源码现场编译到临时目录：跨平台正确、hermetic、不依赖开发机状态。
// go build 有构建缓存，同一测试二进制生命周期内最多真实构建一次。

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	agentEchoMu   sync.Mutex
	agentEchoPath string // 现编译产物路径（包级共享，TestMain 统一清理）
)

// buildAgentEcho 现编译 backend/cmd/agent_echo 并返回其绝对路径。
// 所有需要 fake agent 的测试统一走这里，保留 echo/hang/echo-only 全部
// 测试语义（断言不缩水）。
func buildAgentEcho(t *testing.T) string {
	t.Helper()

	agentEchoMu.Lock()
	defer agentEchoMu.Unlock()
	if agentEchoPath != "" {
		if _, err := os.Stat(agentEchoPath); err == nil {
			return agentEchoPath
		}
		// 产物意外丢失（不应发生）——落到下方重建
	}

	dir, err := os.MkdirTemp("", "pocket-agent-echo-*")
	if err != nil {
		t.Fatalf("mktemp for agent_echo: %v", err)
	}
	bin := filepath.Join(dir, "agent_echo")
	// 测试 cwd = backend/internal/agent，cmd 包相对路径为 ../../cmd/agent_echo
	out, err := exec.Command("go", "build", "-o", bin, "../../cmd/agent_echo").CombinedOutput()
	if err != nil {
		t.Fatalf("go build cmd/agent_echo (需要 go 工具链在 PATH): %v\n%s", err, out)
	}
	agentEchoPath = bin
	return bin
}

// TestMain 在全部测试结束后清理现编译的 agent_echo 产物。
func TestMain(m *testing.M) {
	code := m.Run()
	if agentEchoPath != "" {
		_ = os.RemoveAll(filepath.Dir(agentEchoPath))
	}
	os.Exit(code)
}
