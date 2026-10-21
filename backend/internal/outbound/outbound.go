// Package outbound 统一出站 HTTP 客户端的构造：**每一个出站客户端都必须有上界**。
//
// # 为什么（§950 的实测依据，不是推理）
//
//	Go 的 net/http 里，「客户端没有 Timeout」+「请求没有带 deadline 的 ctx」
//	⇒ 这个请求**没有任何时间上界** ⇒ 上游不返回就**永久挂住**：
//
//	  - 对端黑洞（连不上也不拒绝）⇒ 卡在 dial；
//	  - 连上了但迟迟不回响应头　　⇒ 卡在读响应头；
//	  - 响应头到了但 body 读不完　⇒ 卡在读 body（trickle / 不发 Content-Length）。
//
//	⇒⇒⇒ 在一个**长跑**的 pocketd 里，这样的请求 = 那个 goroutine 永久泄漏；
//	⇒⇒⇒ §949 管的「panic 打死进程」管不到这里 ——
//	⇒⇒⇒ **panic 能救，阻塞救不了**：recover 只在 panic 发生的那一刻有用，
//	　　 而「永远等不到响应」不是 panic，是**安静地停在那儿**。
//
// # 口径
//
//	① 受检范围内（backend 非测试代码）**不许**出现 `http.Client{…}` 复合字面量
//	   ⇒ 客户端只能由本包构造 ⇒ 上界由本包统一兜住（卡口见
//	   internal/repohygiene/outbound_http_bound_test.go）。
//	② NewClient **保证** Timeout > 0：timeout <= 0 一律回落 DefaultTimeout
//	   ⇒ 「配置没填 / 填成 0 / 算错了」都不会变成「没有上界」。
//	③ NewStreamingClient 是**唯一的例外口**：它没有总超时，只给 SSE 长连接用
//	   （上界由请求 ctx 提供）⇒ 每个调用点都要在卡口的台账里登记并写清理由。
//
// # 什么时候该用哪个
//
//	普通请求　　　　　　　→ NewClient（带硬超时）
//	SSE / 长连接　　　　 → NewStreamingClient + 请求 ctx 带 deadline
//	需要自定义 Transport　→ NewClient(..., WithTransport(tr))
//	需要改重定向策略　　　→ NewClient(..., WithCheckRedirect(fn))
package outbound

import (
	"net/http"
	"time"
)

// DefaultTimeout 是 NewClient 在 timeout <= 0 时回落的默认总超时。
//
// ⚠ 为什么要这个回落，而不是「让调用方自己保证」：
//
//	调用方拿到 timeout 的来源五花八门（配置项 / 环境变量 / 算出来的秒数），
//	任何一处漏判都会变成「客户端没有上界」⇒⇒⇒
//	⇒ 与其要求 30 个调用点各自记得判一次，不如**在唯一的构造口兜一次**。
const DefaultTimeout = 30 * time.Second

// Option 是客户端的可选配置项。
type Option func(*http.Client)

// WithTransport 指定 RoundTripper（SSRF 防护 / 自定义 TLS / 自定义拨号等）。
//
// ⚠ 注意：Transport **不提供**总上界 —— 总上界来自 http.Client.Timeout
// （它会覆盖到「响应体读完」为止）⇒⇒⇒ 只设 Transport 不设 Timeout 仍然是**无上界**。
func WithTransport(tr http.RoundTripper) Option {
	return func(c *http.Client) {
		c.Transport = tr
	}
}

// WithCheckRedirect 指定重定向策略。
func WithCheckRedirect(fn func(req *http.Request, via []*http.Request) error) Option {
	return func(c *http.Client) {
		c.CheckRedirect = fn
	}
}

// NewClient 构造一个**带硬超时**的出站客户端。
//
// ⚠ 口径（§950）：
//
//	timeout <= 0 ⇒ 回落 DefaultTimeout ⇒ **本函数不会返回 Timeout == 0 的客户端**。
//	⇒ 「配置漏填」不会变成「请求永远挂着」。
func NewClient(timeout time.Duration, opts ...Option) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	c := &http.Client{Timeout: timeout}
	applyOpts(c, opts)
	return c
}

// NewStreamingClient 构造一个**没有总超时**的客户端 —— 只给 SSE 长连接用。
//
// ⚠ 为什么它必须存在：SSE 长连接的寿命由**事件源**决定，不是由超时决定
// （§950 实测：facade / opencode / agent 三处 SSE 都要「一直挂着」）
// ⇒⇒⇒ 套上总超时会**掐断流**，那是另一种 bug。
//
// ⚠ 代价：调用方**必须**保证请求 ctx 带 deadline，否则这条流同样是无上界的
// ⇒⇒⇒ 每个调用点都要在 §950 卡口的台账里登记并写清「上界由谁提供」。
func NewStreamingClient(opts ...Option) *http.Client {
	c := &http.Client{}
	applyOpts(c, opts)
	return c
}

// applyOpts 施加选项；nil 选项忽略（避免调用方写 nil 时 panic）。
func applyOpts(c *http.Client, opts []Option) {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(c)
	}
}
