package notify

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockSMTPServer 最小 SMTP 服务器（无 TLS 路径，仅用于 plain/无认证场景）。
type mockSMTPServer struct {
	ln      net.Listener
	mu      sync.Mutex
	gotMail []string
	gotFrom string
	gotTo   string
}

func newMockSMTPServer(t *testing.T) *mockSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockSMTPServer{ln: ln}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *mockSMTPServer) addr() string { return s.ln.Addr().String() }

func (s *mockSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *mockSMTPServer) handle(c net.Conn) {
	defer c.Close()
	inData := false
	var from, to, body strings.Builder
	_, _ = c.Write([]byte("220 mock ESMTP\r\n"))
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		line := strings.TrimRight(string(buf[:n]), "\r\n")
		for _, l := range strings.Split(line, "\r\n") {
			upper := strings.ToUpper(l)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				_, _ = c.Write([]byte("250 mock\r\n"))
			case strings.HasPrefix(upper, "MAIL FROM:"):
				from.Reset()
				from.WriteString(strings.Trim(strings.TrimPrefix(l, "MAIL FROM:"), "<> "))
				_, _ = c.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(upper, "RCPT TO:"):
				to.Reset()
				to.WriteString(strings.Trim(strings.TrimPrefix(l, "RCPT TO:"), "<> "))
				_, _ = c.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(upper, "DATA"):
				_, _ = c.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
				inData = true
				body.Reset()
			case upper == ".":
				if inData {
					s.mu.Lock()
					s.gotMail = append(s.gotMail, body.String())
					s.gotFrom = from.String()
					s.gotTo = to.String()
					s.mu.Unlock()
					inData = false
					_, _ = c.Write([]byte("250 OK\r\n"))
				}
			case strings.HasPrefix(upper, "QUIT"):
				_, _ = c.Write([]byte("221 Bye\r\n"))
				return
			default:
				if inData {
					body.WriteString(l)
					body.WriteString("\r\n")
				} else {
					_, _ = c.Write([]byte("250 OK\r\n"))
				}
			}
		}
	}
}

func TestSMTPClient_NilSafe(t *testing.T) {
	var c *Client
	if err := c.Send(context.Background(), Message{To: "a@b.c", Subject: "x", Text: "y"}); err == nil {
		t.Fatal("expected error from nil client")
	}
}

func TestSMTPClient_PlainText(t *testing.T) {
	srv := newMockSMTPServer(t)
	c := NewClient(Config{
		Host:    "127.0.0.1",
		Port:    mustPort(t, srv.addr()),
		From:    "from@example.com",
		TLSMode: "none",
	})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if err := c.Send(context.Background(), Message{To: "to@example.com", Subject: "hi", Text: "body"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.gotMail) != 1 {
		t.Fatalf("expected 1 mail, got %d", len(srv.gotMail))
	}
	body := srv.gotMail[0]
	if !strings.Contains(body, "Subject: hi") || !strings.Contains(body, "body") {
		t.Errorf("missing expected content: %q", body)
	}
	if srv.gotFrom != "from@example.com" {
		t.Errorf("unexpected from: %q", srv.gotFrom)
	}
	if srv.gotTo != "to@example.com" {
		t.Errorf("unexpected to: %q", srv.gotTo)
	}
}

func TestSMTPClient_InvalidRecipient(t *testing.T) {
	c := NewClient(Config{Host: "127.0.0.1", Port: 25, From: "x@y.z", TLSMode: "none"})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if err := c.Send(context.Background(), Message{To: "not-an-email", Subject: "x", Text: "y"}); err == nil {
		t.Fatal("expected error for invalid recipient")
	}
}

func TestSMTPClient_EmptyBody(t *testing.T) {
	c := NewClient(Config{Host: "127.0.0.1", Port: 25, From: "x@y.z", TLSMode: "none"})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if err := c.Send(context.Background(), Message{To: "a@b.c", Subject: "x"}); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestSMTPClient_NilClientWhenNoHost(t *testing.T) {
	c := NewClient(Config{})
	if c != nil {
		t.Fatal("expected nil client when host empty")
	}
}

func TestSMTPClient_HTMLBodyMultipart(t *testing.T) {
	srv := newMockSMTPServer(t)
	c := NewClient(Config{
		Host:    "127.0.0.1",
		Port:    mustPort(t, srv.addr()),
		From:    "from@example.com",
		TLSMode: "none",
	})
	if err := c.Send(context.Background(), Message{
		To:      "to@example.com",
		Subject: "html",
		Text:    "plain",
		HTML:    "<p>html</p>",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	body := srv.gotMail[0]
	srv.mu.Unlock()
	if !strings.Contains(body, "multipart/alternative") {
		t.Errorf("expected multipart/alternative, got: %q", body)
	}
	if !strings.Contains(body, "<p>html</p>") {
		t.Errorf("expected HTML body, got: %q", body)
	}
}

func mustPort(t *testing.T, addr string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return p
}

// newStalledSMTPServer 起一台「accept 了 TCP 之后一句话都不说」的服务器。
//
// 这是最毒的一种对端：连接建得上，但 220 banner 永远不来。
// 变更前（smtp.Dial 无拨号超时 + 之后全程无 deadline）Send 会被**永久**钉住。
func newStalledSMTPServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 收下连接，但**不写 banner**。
			held = append(held, conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// newNoStartTLSServer 起一台只说 banner、**不宣告 STARTTLS** 的服务器。
//
// ⚠ 它会对 EHLO 回 "250 mock"（**不带任何扩展**）⇒ 对端确实不支持 STARTTLS
// ⇒ 这条测的是「分支选对了」，而不是「等到了 deadline」。
func newNoStartTLSServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Write([]byte("220 mock ESMTP\r\n"))
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					line := strings.TrimRight(string(buf[:n]), "\r\n")
					upper := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						// 单行 250，**不带扩展** ⇒ 没有 STARTTLS。
						_, _ = c.Write([]byte("250 mock\r\n"))
					case strings.HasPrefix(upper, "QUIT"):
						_, _ = c.Write([]byte("221 Bye\r\n"))
						return
					default:
						_, _ = c.Write([]byte("250 OK\r\n"))
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// TestSMTPClient_StalledServerIsBounded 是本节的核心行为断言：
// 一台「建得上连接但一句话不说」的服务器**不能**把 Send 永久钉住。
//
// ⚠⚠ 断言是**双边**的（既不许太快，也不许太慢）：
// · 太慢（> 5s）⇒ 上界没生效（去掉 SetDeadline 就会出现 ⇒ 被掐死或挂死）
// · 太快（< 100ms）⇒ 上界**算错了**（例如 deadline 被算成过去时刻 ⇒
// 请求还没开始就「超时」了 ⇒ 这条断言照样绿 ⇒ 假的）
// 单边断言会把「deadline 算错」当成通过 —— 那是本仓反复踩过的形态。
func TestSMTPClient_StalledServerIsBounded(t *testing.T) {
	addr := newStalledSMTPServer(t)

	origSession := smtpSessionTimeout
	smtpSessionTimeout = 300 * time.Millisecond
	defer func() { smtpSessionTimeout = origSession }()

	c := NewClient(Config{
		Host:    "127.0.0.1",
		Port:    mustPort(t, addr),
		From:    "from@example.com",
		TLSMode: "none",
	})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	start := time.Now()
	err := c.Send(context.Background(), Message{To: "to@example.com", Subject: "s", Text: "t"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("stalled server: expected an error, got nil")
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("stalled server: %v 就返回了 ⇒ 上界算错了（deadline 落在过去），不是「等满预算才掐断」", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("stalled server: Send 用了 %v ⇒ 上界没有生效（被永久钉住了）", elapsed)
	}
}

// TestSMTPClient_BudgetPrefersEarlierCtxDeadline 断言预算取「更早的那个」。
//
// ⚠ 这条是**纯函数**断言（不碰网络）⇒ 快且确定。
// 有了它，判据就不必依赖「挂住多久」来区分对错。
func TestSMTPClient_BudgetPrefersEarlierCtxDeadline(t *testing.T) {
	origSession := smtpSessionTimeout
	smtpSessionTimeout = 30 * time.Second
	defer func() { smtpSessionTimeout = origSession }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := newSMTPBudget(ctx)
	if left := time.Until(b.deadline); left > time.Second {
		t.Fatalf("ctx 的 200ms deadline 更早，却没被采纳：剩 %v", left)
	}
	if b.dialTimeout != smtpDialTimeout {
		t.Fatalf("dialTimeout 没有取 smtpDialTimeout：%v", b.dialTimeout)
	}
}

// TestSMTPClient_BudgetFallsBackToSessionTimeout 断言「调用方没给 deadline 时，
// 上界仍然存在」—— 这正是本节要治的病（原来整个 Send 没有任何上界）。
func TestSMTPClient_BudgetFallsBackToSessionTimeout(t *testing.T) {
	origSession := smtpSessionTimeout
	smtpSessionTimeout = 7 * time.Second
	defer func() { smtpSessionTimeout = origSession }()

	b := newSMTPBudget(context.Background())
	left := time.Until(b.deadline)
	if left < 6*time.Second || left > 8*time.Second {
		t.Fatalf("没有 ctx deadline 时的回落值不对：%v", left)
	}
	if b.dialTimeout != smtpDialTimeout {
		t.Fatalf("dialTimeout 没有取 smtpDialTimeout：%v", b.dialTimeout)
	}
}

// TestSMTPClient_StartTLSWithoutSupport 覆盖 starttls 分支：
// 对端不宣告 STARTTLS 时必须**报错**，而不是继续把明文发出去。
func TestSMTPClient_StartTLSWithoutSupport(t *testing.T) {
	addr := newNoStartTLSServer(t)
	c := NewClient(Config{
		Host:    "127.0.0.1",
		Port:    mustPort(t, addr),
		From:    "from@example.com",
		TLSMode: "starttls",
	})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	err := c.Send(context.Background(), Message{To: "to@example.com", Subject: "s", Text: "t"})
	if err == nil {
		t.Fatal("expected an error when the server does not advertise STARTTLS")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("错误里没有点名 STARTTLS：%v", err)
	}
}
