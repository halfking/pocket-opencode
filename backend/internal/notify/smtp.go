// Package notify 提供 SMTP 邮件发送等通知能力。
package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/background"
)

// Config 描述 SMTP 连接与认证信息。
type Config struct {
	Host     string // SMTP 主机（如 smtp.example.com）
	Port     int    // SMTP 端口（如 587）
	User     string // 认证用户名（空 = 无认证）
	Password string // 认证密码
	From     string // 发件人邮箱（默认 = User）
	TLSMode  string // none | starttls | tls（默认 starttls）
}

// Message 单封邮件。
type Message struct {
	To      string // 收件人
	Subject string
	Text    string // 纯文本正文
	HTML    string // 可选 HTML 正文（空则 multipart/alternative 仅含 text）
}

// ⚠⚠ **等一个不会自己结束的东西，必须自带 deadline。**
//
// 这两个预算与 internal/server/smtp_probe.go 的 smtpDialTimeout / smtpProbeTimeout
// 同款（同一个仓里已有的正确先例）：**net/smtp 没有 context 支持**
// ⇒ 上界只能落在 socket deadline 上，而不是靠 ctx 取消。
//
// 变更前这一族是**完全裸的**：
// · tls.Dial / smtp.Dial ⇒ `net.Dial` 没有 Timeout ⇒ 一个被黑洞掉的 SYN
// 就能把建连永久钉住；
// · 之后的 AUTH / MAIL / RCPT / DATA / QUIT **全程没有 deadline**
// ⇒ 一台「accept 了 TCP 之后就不说话」的 SMTP 服务器能把 Send 永久钉住。
//
// 而这条路径是**邮箱验证码**（internal/server/server_auth_extended.go 的
// sendCodeEmail）与后台通知共用的 ⇒ 挂住 = 邮件通知永久失效且无声。
//
// 声明成 var 而不是 const：测试要把它们缩短（否则「上界生效」这条断言
// 要等 30 秒）。生产代码永不重写 —— 与 internal/server/smtp_probe.go 同款。
var (
	// smtpDialTimeout 限「一次 TCP 建连」的时间。
	smtpDialTimeout = 10 * time.Second
	// smtpSessionTimeout 限**整场 SMTP 会话**（banner / EHLO / STARTTLS /
	// AUTH / MAIL / RCPT / DATA / QUIT）。用**一个绝对时刻**而不是每阶段重置：
	// 只有绝对 deadline 才能盖住「每一阶段都慢一点」的服务器。
	smtpSessionTimeout = 30 * time.Second
)

// smtpBudget 是一次 SMTP 发送的**上界**。
type smtpBudget struct {
	// dialTimeout 限建连。
	dialTimeout time.Duration
	// deadline 是整场会话的**绝对**上界（落成 socket deadline）。
	deadline time.Time
}

// newSMTPBudget 算出这次发送的上界。
//
// ⚠ ctx 的 deadline 比 smtpSessionTimeout 更早时**取更早的那个**：
// 调用方（server_auth_extended.go 的 sendCodeEmail）给了 10s，
// 那这次发送就只该有 10s ⇒ 不能因为「我们自己还有 30s」就多等 20s。
//
// ⚠ ctx 没有 deadline 时（context.Background()）才回落到 smtpSessionTimeout
// ⇒ **上界不依赖调用方是否给 deadline** —— 这正是本次要治的病。
//
// ⚠⚠ 预算在**调用方 goroutine** 里算好再传下去，而不是在拨号 goroutine 里读
// 那两个 var：否则测试把 var 改短/改回时，后台 goroutine 可能正在读它
// ⇒ `-race` 直接报数据竞争（这正是 smtp_probe_test.go 那种「改 var」的写法
// 容易踩的坑，notify 这条路径还要多一层后台 goroutine）。
func newSMTPBudget(ctx context.Context) smtpBudget {
	deadline := time.Now().Add(smtpSessionTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return smtpBudget{dialTimeout: smtpDialTimeout, deadline: deadline}
}

// Client SMTP 客户端。
type Client struct {
	cfg Config
}

// NewClient 构造 SMTP 客户端。Host 为空返回 nil（调用方须 nil-safe）。
func NewClient(cfg Config) *Client {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "starttls"
	}
	if cfg.From == "" {
		cfg.From = cfg.User
	}
	return &Client{cfg: cfg}
}

// Send 发送单封邮件。
//
// 返回的 error 在以下场景非 nil：
//   - To 非邮箱
//   - 拨号 / TLS / AUTH 失败
//   - SMTP 协议返回非 250
func (c *Client) Send(ctx context.Context, m Message) error {
	if c == nil {
		return errors.New("smtp client not configured")
	}
	if !looksLikeEmail(m.To) {
		return fmt.Errorf("invalid recipient: %q", m.To)
	}
	if strings.TrimSpace(c.cfg.From) == "" {
		return errors.New("from address is empty")
	}
	if m.Text == "" && m.HTML == "" {
		return errors.New("message body is empty")
	}

	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	body := buildMime(m, c.cfg.From)
	auth := smtpAuth(c.cfg)
	budget := newSMTPBudget(ctx)

	// ctx cancel propagation
	dialDone := make(chan error, 1)
	type dialResult struct {
		conn *smtp.Client
		err  error
	}
	dialRes := make(chan dialResult, 1)
	background.Go("notify/smtp-send", func() {
		conn, err := dialAndUpgrade(ctx, addr, c.cfg, budget)
		dialRes <- dialResult{conn: conn, err: err}
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-dialRes:
		if r.err != nil {
			return fmt.Errorf("smtp dial: %w", r.err)
		}
		// ⚠ ctx 可能在建连期间就被放弃了 ⇒ 别再往下开一场没人等的会话。
		//   （会话阶段本身由 socket deadline 兜住，见 dialAndUpgrade。）
		if err := smtpCtxErr(ctx); err != nil {
			return err
		}
		conn := r.conn
		defer func() { _ = conn.Quit() }()

		if auth != nil {
			if ok, _ := conn.Extension("AUTH"); ok {
				if err := conn.Auth(auth); err != nil {
					return fmt.Errorf("smtp auth: %w", err)
				}
			}
		}
		if err := conn.Mail(c.cfg.From); err != nil {
			return fmt.Errorf("smtp MAIL FROM: %w", err)
		}
		if err := conn.Rcpt(m.To); err != nil {
			return fmt.Errorf("smtp RCPT TO: %w", err)
		}
		// ⚠ DATA 是最长的一个阶段（写正文 + 等对端的 250）⇒ 写之前再查一次 ctx。
		if err := smtpCtxErr(ctx); err != nil {
			return err
		}
		w, err := conn.Data()
		if err != nil {
			return fmt.Errorf("smtp DATA: %w", err)
		}
		if _, err := w.Write(body); err != nil {
			return fmt.Errorf("smtp DATA write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("smtp DATA close: %w", err)
		}
		dialDone <- nil
		return nil
	}
}

// smtpCtxErr 在 SMTP 的阶段之间查一次 ctx。
//
// 为什么需要：socket deadline 兜的是「**对端**不说话」，
// 而 ctx 兜的是「**调用方**不等了」（例如 HTTP 请求已经超时返回）。
// 两者独立 ⇒ 只做前者会让已经放弃的调用方还在等 socket。
func smtpCtxErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。
//
// ⚠⚠ **这里不再用 smtp.Dial / tls.Dial**：那两个是 net/smtp 与 crypto/tls
// 提供的**无超时**拨号口（内部就是 `net.Dial`，没有 Timeout）
// ⇒ 一个被黑洞掉的 SYN 就能把建连永久钉住。
// ⇒ 改成自己拨号：`net.Dialer{Timeout}` 管建连，
// ⇒ 再用**一个绝对 deadline** 盖住之后**每一个**阶段（含 QUIT）。
//
// ⚠ serverName 必须与 smtp.Dial 一致：net/smtp 的 smtp.Dial 内部是
// `host, _, _ := net.SplitHostPort(addr)` 再 `NewClient(conn, host)`
// ⇒ 这里照抄。若图省事直接传 addr（含 ":587"），smtp.PlainAuth 的
// host 校验（`server.Name != a.host`）会**每次都失败**。
func dialAndUpgrade(ctx context.Context, addr string, cfg Config, budget smtpBudget) (*smtp.Client, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("smtp address %q: %w", addr, err)
	}
	dialer := &net.Dialer{Timeout: budget.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	// 一个绝对 deadline 盖住**每一个**后续阶段（banner / EHLO / STARTTLS /
	// AUTH / MAIL / RCPT / DATA / QUIT）。net/smtp 没有 ctx 支持
	// ⇒ 上界只能落在 socket 上。
	if err := conn.SetDeadline(budget.deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smtp set deadline: %w", err)
	}
	switch strings.ToLower(cfg.TLSMode) {
	case "tls":
		// 465 直连 TLS
		tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		// ⚠ 必须显式 Handshake：tls.Dial 内部做的那一步现在由我们自己做，
		//   而它也落在上面那个 deadline 里（握手慢也会被掐断）。
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("smtp tls handshake: %w", err)
		}
		// ⚠ 必须传 *tls.Conn：net/smtp 用 `conn.(*tls.Conn)` 判断是否已加密，
		//   而 PlainAuth 在未加密时**拒绝**交出密码（"unencrypted connection"）。
		c, err := smtp.NewClient(tlsConn, host)
		if err != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("smtp new client: %w", err)
		}
		return c, nil
	case "none":
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("smtp new client: %w", err)
		}
		return c, nil
	default: // starttls
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("smtp new client: %w", err)
		}
		if ok, _ := c.Extension("STARTTLS"); !ok {
			_ = c.Close()
			return nil, errors.New("server does not support STARTTLS")
		}
		tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if err := c.StartTLS(tlsCfg); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
		return c, nil
	}
}

func smtpAuth(cfg Config) smtp.Auth {
	if cfg.User == "" {
		return nil
	}
	return smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
}

func buildMime(m Message, from string) []byte {
	boundary := "pocket_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var b strings.Builder
	// 防 header injection：拒绝任何 CR/LF，否则可注入 Bcc/Cc 等额外 header。
	from = sanitizeHeader(from)
	to := sanitizeHeader(m.To)
	subject := sanitizeHeader(m.Subject)
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	if m.HTML != "" {
		b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\r\n")
		// boundary 自身是 hex 编码，安全；但仍走 sanitizeHeader 兜底
		b.WriteString("\"\r\n")
		b.WriteString("\r\n--" + sanitizeHeader(boundary) + "\r\n")
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(m.Text)
		b.WriteString("\r\n--" + sanitizeHeader(boundary) + "\r\n")
		b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		b.WriteString(m.HTML)
		b.WriteString("\r\n--" + sanitizeHeader(boundary) + "--\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(m.Text)
	}
	return []byte(b.String())
}

// sanitizeHeader 移除 RFC 5322 header 字段值中不允许的 CR/LF，防止 header injection。
// Subject 中文场景下编码由调用方负责（RFC 2047），此处只做防御性净化。
func sanitizeHeader(s string) string {
	if s == "" {
		return s
	}
	// 折叠所有 \r 与 \n 为单空格，保留其它可打印字符。
	r := strings.NewReplacer("\r", " ", "\n", " ")
	return r.Replace(s)
}

func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	if strings.IndexByte(s[at+1:], '.') < 0 {
		return false
	}
	return true
}
