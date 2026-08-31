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

	// ctx cancel propagation
	dialDone := make(chan error, 1)
	type dialResult struct {
		conn *smtp.Client
		err  error
	}
	dialRes := make(chan dialResult, 1)
	go func() {
		conn, err := dialAndUpgrade(addr, c.cfg)
		dialRes <- dialResult{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-dialRes:
		if r.err != nil {
			return fmt.Errorf("smtp dial: %w", r.err)
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

// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。
func dialAndUpgrade(addr string, cfg Config) (*smtp.Client, error) {
	switch strings.ToLower(cfg.TLSMode) {
	case "tls":
		// 465 直连 TLS
		host, _, _ := net.SplitHostPort(addr)
		tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("tls dial: %w", err)
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return nil, fmt.Errorf("smtp new client: %w", err)
		}
		return c, nil
	case "none":
		c, err := smtp.Dial(addr)
		if err != nil {
			return nil, err
		}
		return c, nil
	default: // starttls
		c, err := smtp.Dial(addr)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
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
