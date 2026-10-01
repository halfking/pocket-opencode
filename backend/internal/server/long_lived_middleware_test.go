package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLongLivedPathMiddlewareClearsDeadlineOnSSEPrefix 确认中间件确实清了本
// 连接的写 deadline —— 后续 SSE handler 写入 chunk 不会被 30s WriteTimeout 掐断。
//
// httptest.NewRecorder() 不实现 SetWriteDeadline（其底层不是真实的 TCP 连接），
// 所以必须用 httptest.NewServer 跑在真连接上验证，否则测的是 ResponseController
// 本身是否支持 deadline clear，不是中间件在生产链路下能否生效。
func TestLongLivedPathMiddlewareClearsDeadlineOnSSEPrefix(t *testing.T) {
	for _, p := range longLivedPaths {
		t.Run(p, func(t *testing.T) {
			handler := longLivedPathMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// 中间件已经写过 deadline —— 再写一次必须成功（语义不变）。
				// 如果中间件包装层太厚 / 没穿透，handler 这里拿到的 ResponseController
				// 可能不支持 SetWriteDeadline，errno 返回 feature not supported。
				if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
					t.Errorf("handler 看不到 SetWriteDeadline 支持：%v", err)
				}
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL + p + "/some-event")
			if err != nil {
				t.Fatalf("请求失败：%v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("状态码 = %d, want 200", resp.StatusCode)
			}
		})
	}
}

// TestLongLivedPathMiddlewareLeavesOtherPathsAlone 确认中间件只放宽白名单里
// 的前缀；其它路径直接放行，不强制 deadline clear（避免在短请求上做无用功）。
func TestLongLivedPathMiddlewareLeavesOtherPathsAlone(t *testing.T) {
	notLongLived := []string{
		"/api/llm-gateway/config",
		"/api/opencode/sessions",
		"/healthz",
		"/api/notes",
	}
	for _, p := range notLongLived {
		t.Run(p, func(t *testing.T) {
			executed := false
			handler := longLivedPathMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executed = true
			}))
			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL + p)
			if err != nil {
				t.Fatalf("请求失败：%v", err)
			}
			defer resp.Body.Close()
			if !executed {
				t.Fatal("下游 handler 未执行")
			}
		})
	}
}

// --- BUG-AW：WriteTimeout 掐断长耗时同步端点（2026-10-01） ---
//
// 现象：服务端日志记 `POST /api/email/pipeline/run - 200`，客户端却拿到
// `UND_ERR_SOCKET: other side closed` 且 bytesRead: 0。根因不是网络抖动，
// 而是 http.Server 的 WriteTimeout: 30s（cmd/pocketd/main.go）在连接 30s
// 处让写 deadline 到期、连接作废；handler 在 1m30s 才 writeJSON，一个字节
// 都写不出去。**「服务端 200 / 客户端空响应」看到这个组合，先查 longLivedPaths。**
//
// 下面两个用例把 30s 按比例缩到几百毫秒，在真实 TCP 连接上复现同一个故障。
// 真实 30s 跑一遍要半分钟且只会得到一个偶发结果，缩时序能把故障变成确定性的。
// 服务端日志里的 "200" 由 logging 中间件在 handler 返回后打，与客户端是否
// 真的收到响应无关 —— 这正是本 bug 难查的原因，所以对照组必须同时抓两侧。

// newWriteTimeoutServer 起一个带 WriteTimeout 的真实 HTTP server。
// httptest.NewServer() 不暴露 Config.WriteTimeout，必须用 Unstarted 版手动设。
func newWriteTimeoutServer(t *testing.T, writeTimeout time.Duration, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestLongLivedPathSurvivesServerWriteTimeout 确认白名单端点在 handler
// 耗时超过 server WriteTimeout 之后，仍能把响应体完整送达客户端。
func TestLongLivedPathSurvivesServerWriteTimeout(t *testing.T) {
	const (
		writeTimeout = 300 * time.Millisecond // 缩时后的 30s
		handlerDelay = 600 * time.Millisecond // 缩时后的 1m30s
		payload      = `{"ok":true}`
	)

	for _, p := range []string{"/api/email/pipeline/run", "/api/emails/invoices/harvest"} {
		t.Run(p, func(t *testing.T) {
			srv := newWriteTimeoutServer(t, writeTimeout, longLivedPathMiddleware(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(handlerDelay) // 模拟同步耗时
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(payload))
				})))

			resp, err := http.Get(srv.URL + p)
			if err != nil {
				t.Fatalf("白名单端点请求失败（连接在 %v 处被掐断）：%v", writeTimeout, err)
			}
			defer resp.Body.Close()

			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("读取响应体失败：%v", err)
			}
			if !bytes.Equal(got, []byte(payload)) {
				t.Fatalf("响应体 = %q, want %q", got, payload)
			}
		})
	}
}

// TestNonLongLivedPathStillCutOffByWriteTimeout 是上面那个用例的**对照组**。
//
// 如果它没有按预期失败（照样拿到完整响应），说明这台机器上的 TCP /
// deadline 行为与生产不同，上面那个绿测就证明不了任何东西 —— 绿了不算数。
// 所以这个用例断言的是「非白名单路径**必须**拿不到响应」。
func TestNonLongLivedPathStillCutOffByWriteTimeout(t *testing.T) {
	const (
		writeTimeout = 300 * time.Millisecond
		handlerDelay = 600 * time.Millisecond
		payload      = `{"ok":true}`
	)

	delivered := false
	srv := newWriteTimeoutServer(t, writeTimeout, longLivedPathMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(handlerDelay)
			_, _ = w.Write([]byte(payload))
		})))

	resp, err := http.Get(srv.URL + "/api/notes")
	if err == nil {
		defer resp.Body.Close()
		if body, rerr := io.ReadAll(resp.Body); rerr == nil && bytes.Equal(body, []byte(payload)) {
			delivered = true
		}
	}

	if delivered {
		t.Fatalf("对照组失效：非白名单路径 %q 竟然完整收到了 %q。"+
			"说明本机环境不会复现 WriteTimeout 掐断，上一个用例的绿是假绿。",
			"/api/notes", payload)
	}
}
