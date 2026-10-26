package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// 这组判据守的是「ACC 的 MCP 面用 EOF 把委派打死」这条具体链路。
//
// ★ 缺陷原型（2026-10-26 22:09 真机夹具实测）：
//
//	acc_create_task failed: MCP not initialized: initialize failed:
//	failed to send request: Post "http://100.106.67.198:4101/api/v2/mcp": EOF
//
// 根因：initialize 的重试集是「dial 失败 | 429/502/503/504 | 超时」，
//
//	**EOF 不在里面** ⇒ 一次 EOF 就让握手失败；
//	而 initialize 是**幂等**的（只是握手，重做一次没有副作用）⇒ 必须重试。
//
// ⚠ 写工具**不能**跟着重试：EOF 同样可能发生在
//   「服务器已经建好任务、只是响应没回来」之后，
//   而 acc_create_task **没有幂等键** ⇒ 重发会建出重复任务。
//
// 判据清单：
//  1. initialize 遇到 EOF 必须重试（一次掐断 ⇒ 单次调用就该成功）。
//  2. 连续掐断也要重试（不止一次）。
//  3. 重试**必须有上限**（不能无限重试拖死调用方）。
//  4. 写工具遇到 EOF **不许重试**（否则建出重复任务）。
//  5. 分类器本身也要被测（判据的判据）：
//     EOF / unexpected EOF / ECONNRESET / EPIPE 算传输层掐断；
//     超时**不算** EOF（两者语义不同，超时另有分支）；
//     nil / 普通错误不算。

// ① 判据：initialize 遇到一次 EOF 之后必须重试 ⇒ 单次调用即成功。
func TestInitialize_RetriesTransportEOF(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killInitCalls = 1 })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_get_tasks", map[string]any{}); err != nil {
		t.Fatalf("第一次握手被掐断后应当自动重试成功：%v", err)
	}
	inits, _ := srv.counts()
	if inits < 2 {
		t.Fatalf("initialize 只试了 %d 次 ⇒ EOF 没有被重试", inits)
	}
}

// ② 判据：连续掐断不止一次也要重试（证明不是「碰巧多试了一次」）。
func TestInitialize_RetriesTransportEOFRepeatedly(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killInitCalls = 2 })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_get_tasks", map[string]any{}); err != nil {
		t.Fatalf("连续两次 EOF 之后应当重试成功：%v", err)
	}
	inits, _ := srv.counts()
	if inits < 3 {
		t.Fatalf("initialize 只试了 %d 次 ⇒ 没有连续重试", inits)
	}
}

// ③ 判据：重试**必须有上限** —— 服务器一直掐断时必须快速失败，
//
//	而不是无限重试把调用方拖死。
func TestInitialize_EOFRetryBudgetIsBounded(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killInitCalls = 999 })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_get_tasks", map[string]any{}); err == nil {
		t.Fatalf("服务器一直掐连接 ⇒ 整个调用应当失败")
	}
	inits, _ := srv.counts()
	if inits != mcpRetryAttempts {
		t.Fatalf("initialize 试了 %d 次（期望上限 %d）⇒ 重试预算没封顶", inits, mcpRetryAttempts)
	}
}

// ④ 判据：**写工具**遇到 EOF 不许重试 —— acc_create_task 没有幂等键，
//
//	重发会建出重复任务。
func TestWriteTool_DoesNotRetryTransportEOF(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killCallCalls = 1 })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_create_task", map[string]any{}); err == nil {
		t.Fatalf("tools/call 被掐断 ⇒ 这次写调用应当失败")
	}
	_, calls := srv.counts()
	if calls != 1 {
		t.Fatalf("tools/call 发了 %d 次（期望恰好 1 次）⇒ 写工具被重试了 ⇒ 可能建出重复任务", calls)
	}
}

// ⑤ 判据：分类器本身（判据的判据）。
func TestIsTransportEOF(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"普通错误", errors.New("boom"), false},
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"连接被重置", syscall.ECONNRESET, true},
		{"管道断开", syscall.EPIPE, true},
		{"被 %w 包起来的 EOF", fmt.Errorf("failed to send request: %w", io.EOF), true},
		{"被 %w 包起来的 ECONNRESET", fmt.Errorf("x: %w", syscall.ECONNRESET), true},
		{
			"url.Error 包着 EOF（http.Client 的真实形态）",
			&url.Error{Op: "Post", URL: "http://acc/api/v2/mcp", Err: io.EOF},
			true,
		},
		{"超时（语义不同，不算 EOF）", os.ErrDeadlineExceeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransportEOF(tc.err); got != tc.want {
				t.Fatalf("isTransportEOF(%v) = %v，期望 %v", tc.err, got, tc.want)
			}
		})
	}
}

// ⑥ 判据：超时与 EOF 必须是**两个独立**的分类 ——
//
//	否则「超时归入 EOF」会把写工具的重试边界搅浑。
func TestTimeoutAndEOFAreDistinctClasses(t *testing.T) {
	if isTransportEOF(os.ErrDeadlineExceeded) {
		t.Fatalf("超时被算成 EOF 了")
	}
	if !isTimeoutError(os.ErrDeadlineExceeded) {
		t.Fatalf("超时没有被算成超时")
	}
	if isTimeoutError(io.EOF) {
		t.Fatalf("EOF 被算成超时了")
	}
}

// ⑦ 判据（端到端语义）：读工具（acc_get_tasks）在 EOF 之后能自愈，
//
//	写工具（acc_create_task）在 EOF 之后必须把错误**如实**报出去。
func TestReadToolRecoversButWriteToolReportsEOF(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killInitCalls = 1 })
	c := NewClient(srv.URL, "acc-api-key", false)
	ctx := context.Background()

	if _, err := c.CallTool(ctx, "acc_get_tasks", map[string]any{}); err != nil {
		t.Fatalf("读工具应当自愈：%v", err)
	}

	srv.mu.Lock()
	srv.killInitCalls = 0
	srv.killCallCalls = 1
	srv.mu.Unlock()

	err := func() error {
		_, e := c.CallTool(ctx, "acc_create_task", map[string]any{})
		return e
	}()
	if err == nil {
		t.Fatalf("写工具遇到 EOF 必须报错，不能假装成功")
	}
}
