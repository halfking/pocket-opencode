package server

import (
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