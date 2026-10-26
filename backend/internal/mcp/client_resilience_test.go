package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 这组判据守的是「MCP 客户端会不会被**一次**瞬时故障永久毒化」。
//
// ★ 缺陷原型（2026-10-26 实测）：
//
//	ensureInitialized 用 sync.Once + 缓存的 initErr：
//	  c.initOnce.Do(func() { c.initErr = c.doInitialize(ctx) })
//	sync.Once 保证那段闭包**一辈子只跑一次** ⇒ initialize 失败一次，
//	c.initErr 就被永久钉住 ⇒ 此后每一次 CallTool 都返回
//	「MCP not initialized: initialize failed: ...」
//	⇒ 一次网络抖动 = 整个进程生命周期内 ACC 委派全废，而且**没有任何自愈**。
//
// 实测症状（2026-10-26 12:00 前后）：ACC 侧 MCP 面返回过一次 EOF，
// 之后 pocketd 的 delegate 全部 502/超时，且日志里再没有一次成功的 initialize。
//
// 判据要求：
//  1. initialize 失败**不**被缓存：下一次调用必须能重新握手并成功。
//  2. 瞬时故障（503 / 连接建不起来）必须**重试**，而不是一次就放弃。
//  3. 永久故障（403）必须**快速失败**，不能白重试（也不该被当成瞬时故障）。

// mcpTestServer 造一个可控的 MCP 假服务器。
//
// ★ 2026-10-26 扩充：除了「第一次 initialize 被掐」，现在还能控制
//   「连续掐前 N 次 initialize / 前 N 次 tools/call」以及
//   「掐掉全部 initialize（模拟 ACC 持续不可用）」，
//   并允许在测试中途把服务器改回健康 ——
//   因为「EOF 也要重试」和「失败不缓存」是**两条不同的判据**，
//   共用一个夹具才能同时测出「一次 EOF 就让整个进程永久失效」。
type mcpTestServer struct {
	*httptest.Server
	mu            sync.Mutex
	initCalls     int
	callCalls     int
	initStatus    int  // 0 = 正常；非 0 = initialize 返回该状态码
	killInitCalls int  // 还要掐断多少次 initialize（客户端看到 EOF）；0 = 不掐
	killCallCalls int  // 还要掐断多少次 tools/call（客户端看到 EOF）；0 = 不掐
	killAllInits  bool // true = 掐掉**每一次** initialize（ACC 持续不可用）
}

func newMCPTestServer(t *testing.T, cfg func(*mcpTestServer)) *mcpTestServer {
	t.Helper()
	ts := &mcpTestServer{initStatus: 0}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)

		ts.mu.Lock()
		switch req.Method {
		case "initialize":
			ts.initCalls++
			status := ts.initStatus
			kill := ts.killAllInits
			if ts.killInitCalls > 0 {
				ts.killInitCalls--
				kill = true
			}
			ts.mu.Unlock()
			if kill {
				// 直接掐连接：客户端的 Do() 会拿到 EOF。
				if hijackClose(w) {
					return
				}
			}
			if status != 0 {
				http.Error(w, `{"error":"upstream busy"}`, status)
				return
			}
			ts.serveOK(w)
			return
		case "tools/call":
			ts.callCalls++
			kill := ts.killCallCalls > 0
			if kill {
				ts.killCallCalls--
			}
			ts.mu.Unlock()
			if kill {
				if hijackClose(w) {
					return
				}
			}
			ts.serveOK(w)
			return
		default:
			// notifications/initialized：没有响应体也算成功。
			ts.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(ts.Server.Close)
	if cfg != nil {
		cfg(ts)
	}
	return ts
}

// hijackClose 直接掐断连接（客户端的 Do() 会拿到 EOF）。
func hijackClose(w http.ResponseWriter) bool {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return false
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// setHealthy 把服务器改回健康（用于「失败不缓存」判据的后半段）。
func (ts *mcpTestServer) setHealthy() {
	ts.mu.Lock()
	ts.killAllInits = false
	ts.killInitCalls = 0
	ts.killCallCalls = 0
	ts.mu.Unlock()
}

func (ts *mcpTestServer) serveOK(w http.ResponseWriter) {
	w.Header().Set("mcp-session-id", "sess-1")
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(mcpSSEResult))
}

// mcpSSEResult 是一份合法的 MCP 工具结果（SSE 帧）。
// ⚠ 必须用带引号的字符串：raw string（反引号）里的 \n 是**字面两字符**，
//
//	不是换行 ⇒ parseSSEResponse 会因为「一行都没解析出来」而报
//	「no JSON data found in SSE response」。
const mcpSSEResult = "event: message\n" +
	"data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n\n"

func (ts *mcpTestServer) counts() (int, int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.initCalls, ts.callCalls
}

// ① 判据：initialize 失败**不能**被永久缓存。
//
// ⚠ 2026-10-26 改写：原来用 killFirst（掐**第一次** initialize），
//   那时 initialize 不重试 ⇒ 第一次 CallTool 必然失败。
//   现在 initialize 对 EOF **会重试**（见 client_eof_retry_test.go），
//   所以「掐一次」的语义变了 —— 要测「失败不被缓存」，
//   必须掐**每一次** initialize（模拟 ACC 持续不可用），
//   再把服务器改回健康，看客户端能否自愈。
func TestCallTool_InitializeFailureDoesNotPoisonClient(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.killAllInits = true })
	c := NewClient(srv.URL, "acc-api-key", false)
	ctx := context.Background()

	if _, err := c.CallTool(ctx, "acc_get_tasks", map[string]any{}); err == nil {
		t.Fatalf("服务器一直掐连接 ⇒ 第一次调用应当失败")
	}
	// 关键断言：客户端必须能自愈。
	srv.setHealthy()
	if _, err := c.CallTool(ctx, "acc_get_tasks", map[string]any{}); err != nil {
		t.Fatalf("第二次调用应当成功：初始化失败被永久缓存了（sync.Once + initErr）⇒ %v", err)
	}
	inits, calls := srv.counts()
	if inits < 2 {
		t.Fatalf("initialize 只被尝试 %d 次 ⇒ 没有重新握手", inits)
	}
	if calls < 1 {
		t.Fatalf("tools/call 从未被发出（%d 次）", calls)
	}
}

// ② 判据：瞬时故障（503）必须重试。
func TestCallTool_RetriesTransientInitializeFailure(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.initStatus = http.StatusServiceUnavailable })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_get_tasks", map[string]any{}); err == nil {
		t.Fatalf("503 一直持续 ⇒ 整个调用应当失败")
	}
	inits, _ := srv.counts()
	if inits < 2 {
		t.Fatalf("initialize 只试了 %d 次 ⇒ 瞬时故障没有重试", inits)
	}
}

// ③ 判据：永久故障（403）必须快速失败，不白重试。
func TestCallTool_DoesNotRetryPermanentFailure(t *testing.T) {
	srv := newMCPTestServer(t, func(s *mcpTestServer) { s.initStatus = http.StatusForbidden })
	c := NewClient(srv.URL, "acc-api-key", false)

	if _, err := c.CallTool(context.Background(), "acc_get_tasks", map[string]any{}); err == nil {
		t.Fatalf("403 应当失败")
	}
	inits, _ := srv.counts()
	if inits != 1 {
		t.Fatalf("403 被重试了 %d 次 ⇒ 永久故障不该白重试", inits)
	}
}

// ④ 判据：连接建不起来（dial 失败）时，**写**工具也必须重试 ——
//
//	请求根本没到服务器 ⇒ 重发不会造成重复执行。
func TestCallTool_RetriesDialFailureForWriteTool(t *testing.T) {
	tr := &countingDialTransport{failFirst: 2}
	c := NewClient("http://acc.invalid/api/v2/mcp", "acc-api-key", false)
	c.httpClient.Transport = tr

	if _, err := c.CallTool(context.Background(), "acc_create_task", map[string]any{}); err != nil {
		t.Fatalf("第三次应当成功（dial 失败必须重试）：%v", err)
	}
	if tr.calls() < 3 {
		t.Fatalf("只尝试了 %d 次 ⇒ dial 失败没有重试", tr.calls())
	}
}

// countingDialTransport 前 failFirst 次返回 dial 层错误（请求没到服务器），
// 之后正常返回。
type countingDialTransport struct {
	mu        sync.Mutex
	failFirst int
	n         int
}

func (t *countingDialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.n++
	n := t.n
	t.mu.Unlock()
	if n <= t.failFirst {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	body := mcpSSEResult
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Mcp-Session-Id": []string{"sess-1"}, "Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (t *countingDialTransport) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}
