// middleware_logging_test.go — 请求日志中间件的承重判据。
//
// 这道门守的是 2026-10-17 审计补上的那个洞：**「失败得很快」的请求
// 在原实现下不产生任何日志行**。
//
// 原实现只有 `if duration > 500ms { log("[SLOW]...") }`。于是：
//
//	· 一个瞬间 500（连库失败 / 反序列化炸了 / handler 直接报错）
//	  —— 快 ⇒ 不记 ⇒ **需要追查的故障恰恰是查不到的那一类**
//	· 即使记了 SLOW，也没有 request_id，而客户端拿得到 X-Request-ID
//	  ⇒ 日志与客户端看到的 ID 对不上，只能靠时间戳猜
//
// 判据刻意做双向：**该记的必须记、不该记的必须不记**。
// 只写「有输出」那条的话，把中间件改成无脑打全量日志也照样绿 ——
// 那恰恰是要防的退化（200 的常规流量会把真正的错误淹掉）。
package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureLogs 把 stdlib log 的输出重定向到内存缓冲，跑完再还原。
// ⚠️ 必须还原：log 是**包级全局**，不还原会污染同一包内其它测试的输出。
func captureLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	flags, out := log.Flags(), log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0) // 去掉时间戳前缀，避免断言被时间戳干扰
	t.Cleanup(func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return &buf
}

// runThroughLogging 走一次完整的中间件链（含 requestIDMiddleware，
// 与 server.go 里的真实组装顺序一致：requestID 在**外层**，
// 所以 loggingMiddleware 执行时 ctx 里已经能取到 request_id）。
func runThroughLogging(s *Server, h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var handler http.Handler = s.loggingMiddleware(h)
	handler = requestIDMiddleware(handler)
	handler.ServeHTTP(rec, req)
	return rec
}

func newLoggingTestServer() *Server {
	return &Server{}
}

func TestLoggingMiddleware_InstantServerErrorIsLogged(t *testing.T) {
	// ★ 本条是这次修复的**核心**：一个不慢的 500 也必须留下痕迹。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	req.Header.Set("X-Request-ID", "req_instant500")

	runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}, req)

	out := buf.String()
	if !strings.Contains(out, "[ERROR]") {
		t.Fatalf("瞬间 500 竟没有 [ERROR] 日志 —— 这正是本次要修的洞。实际输出：%q", out)
	}
	if !strings.Contains(out, "/api/tasks") {
		t.Errorf("[ERROR] 日志缺少请求路径，无法定位：%q", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("[ERROR] 日志缺少状态码：%q", out)
	}
}

func TestLoggingMiddleware_ErrorLogCarriesRequestID(t *testing.T) {
	// 没有关联 ID，日志和客户端看到的 X-Request-ID 就对不上。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	req := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	req.Header.Set("X-Request-ID", "req_corr_abc123")

	runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}, req)

	out := buf.String()
	if !strings.Contains(out, "req=req_corr_abc123") {
		t.Errorf("[ERROR] 日志缺少 request id，无法与客户端对上：%q", out)
	}
}

func TestLoggingMiddleware_NormalRequestIsNotLogged(t *testing.T) {
	// 反向判据：正常 200 **不得**打日志。
	// 没有这条，把中间件改成「一律 log.Printf」也能通过上面所有断言 ——
	// 而那会把真正的错误淹没在常规流量里。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, req)

	if out := buf.String(); out != "" {
		t.Errorf("正常请求不应产生日志（否则错误会被常规流量淹没）。实际输出：%q", out)
	}
}

func TestLoggingMiddleware_ClientErrorIsNotLoggedAsServerError(t *testing.T) {
	// 4xx 是**调用方**的问题（参数错、没登录、找不到），不是服务端故障。
	// 把它们混进 [ERROR] 会让告警被 404 风暴刷屏 —— 那等于没告警。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	for _, code := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusBadRequest} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
		runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}, req)
		if out := buf.String(); strings.Contains(out, "[ERROR]") {
			t.Errorf("客户端错误 %d 不该记成 [ERROR]：%q", code, out)
		}
	}
}

func TestLoggingMiddleware_SlowRequestIsLogged(t *testing.T) {
	buf := captureLogs(t)
	s := newLoggingTestServer()

	// 用极小阈值制造「慢」，不必真的 sleep 500ms。
	t.Setenv("POCKET_SLOW_REQUEST_MS", "1")
	req := httptest.NewRequest(http.MethodGet, "/api/slow", nil)
	runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}, req)

	out := buf.String()
	if !strings.Contains(out, "[SLOW]") {
		t.Errorf("超阈值请求应记 [SLOW]：%q", out)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Errorf("200 慢请求不应同时记 [ERROR]：%q", out)
	}
}

func TestLoggingMiddleware_SlowServerErrorLoggedOnceAsError(t *testing.T) {
	// 又慢又 5xx：**只记一条 [ERROR]**。
	// 两行会让「按请求统计错误数」的口径翻倍，排障时数出来的数是错的。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	t.Setenv("POCKET_SLOW_REQUEST_MS", "1")
	req := httptest.NewRequest(http.MethodGet, "/api/slow-and-broken", nil)
	runThroughLogging(s, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}, req)

	out := buf.String()
	if !strings.Contains(out, "[ERROR]") {
		t.Fatalf("又慢又 5xx 至少要记 [ERROR]：%q", out)
	}
	if strings.Contains(out, "[SLOW]") {
		t.Errorf("又慢又 5xx 不应重复记 [SLOW]（会��错误数翻倍）：%q", out)
	}
}

func TestLoggingMiddleware_MissingRequestIDDegradesToDash(t *testing.T) {
	// 中间件顺序被改动、或单测直接调用时取不到 ID。
	// 退化成 "-" 而不是空串 —— 空串会让 `grep 'req='` 匹配到所有行。
	buf := captureLogs(t)
	s := newLoggingTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey{}, ""))

	rec := httptest.NewRecorder()
	s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})).ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, "req=-") {
		t.Errorf("取不到 request id 时应退化成 req=-（不能是空串）：%q", out)
	}
	if strings.Contains(out, "req= ") || strings.HasSuffix(strings.TrimSpace(out), "req=") {
		t.Errorf("req= 后面不得为空（grep 'req=' 会匹配到所有行）：%q", out)
	}
}

// ── slowRequestThreshold 的解析判据 ───────────────────────────────────────

func TestSlowRequestThreshold_DefaultWhenUnset(t *testing.T) {
	t.Setenv("POCKET_SLOW_REQUEST_MS", "")
	s := newLoggingTestServer()
	if got := s.slowRequestThreshold(); got != defaultSlowRequestThreshold {
		t.Errorf("未设置时应回落到默认 %v，实际 %v", defaultSlowRequestThreshold, got)
	}
}

func TestSlowRequestThreshold_ParsesValidValue(t *testing.T) {
	t.Setenv("POCKET_SLOW_REQUEST_MS", "1500")
	s := newLoggingTestServer()
	if got := s.slowRequestThreshold(); got != 1500*time.Millisecond {
		t.Errorf("合法值应被解析为 1500ms，实际 %v", got)
	}
}

func TestSlowRequestThreshold_InvalidValueFallsBackAndWarns(t *testing.T) {
	// 配错了必须**说出来**。静默回落到一个没人知道的阈值，
	// 正是本次审计在修的那类「悄悄生效的默认值」。
	for _, bad := range []string{"abc", "0", "-100", "12.5"} {
		t.Run(bad, func(t *testing.T) {
			buf := captureLogs(t)
			t.Setenv("POCKET_SLOW_REQUEST_MS", bad)
			s := newLoggingTestServer()

			got := s.slowRequestThreshold()
			if got != defaultSlowRequestThreshold {
				t.Errorf("非法值 %q 应回落到默认 %v，实际 %v", bad, defaultSlowRequestThreshold, got)
			}
			if !strings.Contains(buf.String(), "[WARN]") {
				t.Errorf("非法值 %q 必须发出 [WARN]，否则配错无人知晓", bad)
			}
		})
	}
}

// 量具自证：确认 captureLogs 真的接到了输出。
// 没有这条，上面所有「不该记」类的断言可能因为「根本没接上」而恒真通过。
func TestCaptureLogsHarnessActuallyCaptures(t *testing.T) {
	buf := captureLogs(t)
	log.Printf("MARKER-%s", "probe")
	if !strings.Contains(buf.String(), "MARKER-probe") {
		t.Fatalf("日志捕获夹具没接上输出，整道门会恒真通过。实际：%q", buf.String())
	}
	if buf.String() == "" && io.Discard == nil { // 保持 io 导入被真实使用
		t.Fatal("unreachable")
	}
}
