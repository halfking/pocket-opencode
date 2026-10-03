package server

// http.Server.WriteTimeout 会把「慢请求」的响应整个掐掉（2026-10-01 实测）。
//
// 故障现象：POST /api/stt/transcribe-full 的服务端耗时 30.17 秒，
// 后端日志明明打了 `[SLOW] POST /api/stt/transcribe-full - 200 (30.178s)`，
// 客户端收到的却是**空响应**：
//
//	curl      → exit 52（Empty reply from server）
//	PowerShell → "The underlying connection was closed ... by the server"
//
// 机制：Go 在读完请求头时就给这条连接定了写 deadline = now + WriteTimeout。
// handler 在那之后才算出结果、才开始写，deadline 早已过期，
// 服务器直接关闭连接，**一个字节的响应都没发出去**。
//
// 之所以难查：日志写着 200（它记的是 handler 的产出，不是网络层是否送达）；
// 客户端拿到的是网络错误而不是 504；而且卡在 30 秒边界附近，时好时坏。

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// startSlowServer 起一个**带 WriteTimeout 的真 http.Server**。
//
// 为什么不能用 httptest.NewServer：它的默认配置没有 WriteTimeout，
// 测出来的永远是「慢请求也能拿到响应」——
// 这正是这个 bug 能活到现在的原因。
func startSlowServer(t *testing.T, writeTimeout time.Duration) (string, func()) {
	t.Helper()
	h := longLivedPathMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟「同步等上游 ASR 回来」的耗时
		time.Sleep(writeTimeout * 5)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler:      h,
		WriteTimeout: writeTimeout,
	}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), func() { _ = srv.Close() }
}

func getStatus(url string) (int, string, error) {
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// TestLongLivedPathsSurviveWriteTimeout 是正面用例：
// 白名单里的 STT 转写端点，耗时远超 WriteTimeout 也必须拿得到响应。
func TestLongLivedPathsSurviveWriteTimeout(t *testing.T) {
	base, stop := startSlowServer(t, 150*time.Millisecond)
	defer stop()

	for _, p := range []string{
		"/api/stt/transcribe",
		"/api/stt/transcribe-full",
		"/api/stt/transcribe-incremental",
		"/api/stt/probe",
		"/api/stt/discover",
	} {
		t.Run(p, func(t *testing.T) {
			code, body, err := getStatus(base + p)
			if err != nil {
				t.Fatalf("%s 应当豁免 WriteTimeout，实际请求失败：%v", p, err)
			}
			if code != http.StatusOK {
				t.Errorf("%s 状态码 = %d，body=%q", p, code, body)
			}
		})
	}
}

// TestNonLongLivedPathStillCutByWriteTimeout 是**对照组**。
//
// 没有它，上面那条用例就只是一句「我加了白名单所以好了」——
// 万一 WriteTimeout 根本没生效，两条都会绿。必须证明这个机制真的在咬人。
func TestNonLongLivedPathStillCutByWriteTimeout(t *testing.T) {
	base, stop := startSlowServer(t, 150*time.Millisecond)
	defer stop()

	// /api/stt/config 不在白名单：它是个普通的快请求，不该被放宽。
	code, body, err := getStatus(base + "/api/stt/config")
	if err == nil && code == http.StatusOK {
		t.Errorf("/api/stt/config 仍在白名单里（返回了 200 body=%q），"+
			"这说明 STT 路径被整段豁免了，而不是只豁免真正阻塞的那几个", body)
	}
}

// TestSTTLongRunningPathsAreAllWhitelisted 钉住「凡是服务端可能等上游的 STT
// 端点，都必须在白名单里」。这是防回归的结构断言：以后新增 STT 端点时，
// 忘了加白名单会在这里转红。
func TestSTTLongRunningPathsAreAllWhitelisted(t *testing.T) {
	// 这些端点的 handler 内部都用 context.WithTimeout 设了 ≥90s 的上限，
	// 远超 30s WriteTimeout。只要上游不返回，响应就一定写不出去。
	mustExempt := []string{
		"/api/stt/transcribe",
		"/api/stt/transcribe-full",
		"/api/stt/transcribe-incremental",
		"/api/stt/probe",
		"/api/stt/discover",
	}
	for _, p := range mustExempt {
		covered := false
		for _, prefix := range longLivedPaths {
			if len(p) >= len(prefix) && p[:len(prefix)] == prefix {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s 的服务端耗时上限 ≥90s，却不在 longLivedPaths 里 —— "+
				"它会在 30s 处被 WriteTimeout 掐断，客户端收到空响应", p)
		}
	}
}
