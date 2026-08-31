package notify

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mockSMTPServer 最小 SMTP 服务器（无 TLS 路径，仅用于 plain/无认证场景）。
type mockSMTPServer struct {
	ln     net.Listener
	mu     sync.Mutex
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
