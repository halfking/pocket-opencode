package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// startFakePOP3 起一个最小 RFC 1939 服务器：greeting + USER/PASS/STAT/UIDL/RETR。
// 回归价值：此前状态行误用 textproto.ReadResponse(200)（只认 HTTP 数字 code），
// 真实 163 服务器的 "+OK Welcome..." greeting 直接报 invalid response code；
// 另有正文 bufio 与状态 bufio 双层缓冲互相吞数据的问题。两者都需要一个
// 会话级 fake server 才能暴露。
func startFakePOP3(t *testing.T, messages map[int]string) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn, messages)
		}
	}()
	return ln.Addr()
}

func handle(conn net.Conn, messages map[int]string) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	write := func(s string) { fmt.Fprint(conn, s) }
	readCmd := func() string {
		line, err := br.ReadString('\n')
		if err != nil {
			return ""
		}
		return strings.TrimSpace(line)
	}
	write("+OK Welcome to fake coremail Pop3 Server\r\n")
	for {
		switch cmd := readCmd(); {
		case cmd == "":
			return
		case strings.HasPrefix(cmd, "USER"), strings.HasPrefix(cmd, "PASS"):
			write("+OK\r\n")
		case cmd == "STAT":
			write(fmt.Sprintf("+OK %d 0\r\n", len(messages)))
		case cmd == "UIDL":
			write("+OK\r\n")
			for i := 1; i <= len(messages); i++ {
				write(fmt.Sprintf("%d uidl-%d\r\n", i, i))
			}
			write(".\r\n")
		case strings.HasPrefix(cmd, "RETR"):
			var idx int
			fmt.Sscanf(cmd, "RETR %d", &idx)
			body, ok := messages[idx]
			if !ok {
				write("-ERR no such message\r\n")
				continue
			}
			write("+OK\r\n")
			// 行首 . 转义按 RFC 1939 byte-stuffing 处理
			for _, line := range strings.Split(body, "\r\n") {
				if strings.HasPrefix(line, ".") {
					line = "." + line
				}
				write(line + "\r\n")
			}
			write(".\r\n")
		case cmd == "QUIT":
			write("+OK bye\r\n")
			return
		default:
			write("-ERR unknown\r\n")
		}
	}
}

func TestFetchPOP3MailboxFetchesNew(t *testing.T) {
	addr := startFakePOP3(t, map[int]string{
		1: "From: a@b.c\r\nSubject: invoice one\r\n\r\nbody one",
		2: "From: d@e.f\r\nSubject: dot escape\r\n\r\n.line starts with dot\r\n..two dots",
	})
	newUIDLs, payloads, err := FetchPOP3Mailbox(context.Background(), addr.String(), false, "u@163.com", "authcode", nil)
	if err != nil {
		t.Fatalf("FetchPOP3Mailbox: %v", err)
	}
	if len(newUIDLs) != 2 || len(payloads) != 2 {
		t.Fatalf("want 2 new messages, got uidls=%v payloads=%d", newUIDLs, len(payloads))
	}
	// dot-unstuffing：行首 ".." 还原为 "."（RFC 822 转义）
	if !strings.Contains(string(payloads[1]), "\r\n.line starts with dot\r\n") {
		t.Errorf("dot-escape line mismatch: %q", payloads[1])
	}

	// 增量：已见过的 UIDL 跳过
	seen := map[string]struct{}{"uidl-1": {}, "uidl-2": {}}
	newUIDLs, payloads, err = FetchPOP3Mailbox(context.Background(), addr.String(), false, "u@163.com", "authcode", seen)
	if err != nil {
		t.Fatalf("FetchPOP3Mailbox(seen): %v", err)
	}
	if len(newUIDLs) != 0 || len(payloads) != 0 {
		t.Fatalf("want 0 new, got %v", newUIDLs)
	}
}

func TestFetchPOP3MailboxAuthRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// greeting / USER 响应 / PASS 响应
		fmt.Fprint(conn, "+OK ready\r\n+OK\r\n-ERR Unable to log on\r\n")
	}()
	_, _, err = FetchPOP3Mailbox(context.Background(), ln.Addr().String(), false, "u", "bad", nil)
	if err == nil || !strings.Contains(err.Error(), "Unable to log on") {
		t.Fatalf("want server -ERR message surfaced, got %v", err)
	}
}
