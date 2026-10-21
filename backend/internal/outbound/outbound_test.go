package outbound

import (
	"net/http"
	"testing"
	"time"
)

// TestNewClientAlwaysHasBound 是本包**最关键**的一条断言：
// NewClient 永远不返回 Timeout == 0 的客户端。
//
// ⚠ 为什么这条比别的都重要（§950 的教训）：
//
//	「调用方记得传一个正数」是**约定**，不是**保证**：
//	timeout 的来源五花八门（配置项 / 环境变量 / 算出来的秒数 / 乘出来的毫秒），
//	任何一处漏判 ⇒ 客户端 Timeout 变成 0 ⇒ 请求**永久挂住** ⇒
//	⇒ 在长跑的 pocketd 里就是一个永久泄漏的 goroutine。
//	⇒⇒⇒ 所以兜底必须发生在**唯一的构造口**，而不是散落在 30 个调用点。
func TestNewClientAlwaysHasBound(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"正常值原样保留", 3 * time.Second, 3 * time.Second},
		{"零值回落默认", 0, DefaultTimeout},
		{"负值回落默认", -5 * time.Second, DefaultTimeout},
		{"极小正值原样保留", time.Nanosecond, time.Nanosecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NewClient(c.in).Timeout
			if got != c.want {
				t.Errorf("NewClient(%v).Timeout = %v（期望 %v）\n"+
					"⇒ Timeout <= 0 意味着这个出站请求**没有任何时间上界**\n"+
					"⇒ ⇒⇒ 上游不返回就永久挂住 ⇒ 长跑的 pocketd 里那是个永久泄漏的 goroutine",
					c.in, got, c.want)
			}
		})
	}
}

// TestNewStreamingClientHasNoTotalTimeout 钉住例外口的语义：
// 流式客户端**故意**没有总超时（套上会掐断 SSE 长连接）。
//
// ⚠ 它的上界由**请求 ctx** 提供 ⇒ 调用点必须在 §950 卡口的台账里登记。
func TestNewStreamingClientHasNoTotalTimeout(t *testing.T) {
	c := NewStreamingClient()
	if c.Timeout != 0 {
		t.Errorf("NewStreamingClient().Timeout = %v（期望 0）\n"+
			"⇒ SSE 长连接的寿命由事件源决定 ⇒ 套上总超时会**掐断流**\n"+
			"⇒ ⇒⇒ 这个客户端的上界必须由请求 ctx 提供（调用点要登记）", c.Timeout)
	}
}

// TestOptionsAreApplied 验证选项真的落到客户端上。
func TestOptionsAreApplied(t *testing.T) {
	tr := &http.Transport{}
	called := false
	c := NewClient(time.Second,
		WithTransport(tr),
		WithCheckRedirect(func(*http.Request, []*http.Request) error {
			called = true
			return http.ErrUseLastResponse
		}),
	)
	if c.Transport != tr {
		t.Errorf("WithTransport 没有把 RoundTripper 装到客户端上")
	}
	if c.CheckRedirect == nil {
		t.Fatalf("WithCheckRedirect 没有把重定向策略装到客户端上")
	}
	if err := c.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Errorf("重定向策略返回 %v（期望 %v）", err, http.ErrUseLastResponse)
	}
	if !called {
		t.Errorf("重定向策略没有被调用到")
	}
}

// TestNilOptionIsIgnored 验证 nil 选项不会 panic（调用方可能写 `outbound.WithTransport(nil)`）。
func TestNilOptionIsIgnored(t *testing.T) {
	c := NewClient(time.Second, nil)
	if c.Timeout != time.Second {
		t.Errorf("nil 选项影响了 Timeout：%v", c.Timeout)
	}
}

// TestEachCallReturnsFreshClient 验证每次调用都返回**独立**的客户端：
// 共享一个 *http.Client 会让某处的 CheckRedirect/Transport 改动影响全仓。
func TestEachCallReturnsFreshClient(t *testing.T) {
	a := NewClient(time.Second)
	b := NewClient(time.Second)
	if a == b {
		t.Errorf("NewClient 两次返回了同一个客户端指针 ⇒ 调用方之间会互相污染配置")
	}
}

// TestRawClientHasNoBound 是**自证臂**：把「我们要防的那件事」写出来。
//
// ⚠ 没有这一条，下一个读代码的人会以为「net/http 默认就带超时」：
//
//	实际上 `&http.Client{}` 的 Timeout 是 0 ⇒ **没有上界**。
func TestRawClientHasNoBound(t *testing.T) {
	raw := &http.Client{}
	if raw.Timeout != 0 {
		t.Skipf("net/http 的默认行为变了（Timeout=%v）⇒ 本卡的假设需要重新评估", raw.Timeout)
	}
	t.Logf("裸 &http.Client{} 的 Timeout = %d ⇒ 这就是 §950 要消灭的「无上界客户端」", raw.Timeout)
}
