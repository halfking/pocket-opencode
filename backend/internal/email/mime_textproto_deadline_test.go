//go:build !greenmail

// mime_textproto_deadline_test.go — 降级通道 fetchRawByTextproto 的三条契约。
//
// 背景：2026-10-01 用 Greenmail 跑通整条收信+发票流水线时，step1.5 取正文
// 每封恰好挂 150.06s 后失败（fetchFailed=4），报
// `imapwire: expected SP, got "{"; textproto fallback: read greeting: EOF`。
// 抓到根因是两个独立缺陷：
//
//	A. 建连之后**零读超时**。net.Dialer.Timeout 只管三次握手，握手之后
//	   br.ReadString('\n') 能挂多久全看服务器脸色，而且不看 ctx（ctx 只喂给
//	   了 DialContext）。于是「accept 了但一句话不说」的服务器能让单封邮件
//	   永久阻塞，整轮流水线失去上界 —— 违反 mime.go 里 maxMessageBytes 注释
//	   明确要求的「也不能让一轮流水线没有上界」。
//	B. TLS 判定写死 `acc.IMAPPort == 993`。对 qq/163 恰好正确，但对任何
//	   非 993 的明文端口是必然的协议错配：客户端说 TLS、服务端等明文
//	   greeting，双向互等。IMAP 端口是逐账户配置项，143/1143/993 之外的
//	   值（例如自建邮件网关）都会走错分支。
//
// 负控：
//   - 把 A 的 deadlineConn / ctx 取消看门狗删掉 -> TestUnblocksWhenServerNeverGreets
//     永久挂住，`go test -timeout 25s` 直接 panic（红）。
//   - 把 B 改回 `acc.IMAPPort == 993` -> TestNonPlainPortGetsImplicitTLS 拿到的是
//     `read greeting: EOF` 而不是 `tls handshake: ...`（红）。
package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// plainFetchListener 在 127.0.0.1:1143 起一个明文 IMAP 假服务器。
// 1143 是 isPlainIMAPPort 认可的两个明文端口之一（另一个是 143），
// 所以降级通道不会给它套 TLS。
func plainFetchListener(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:1143")
	if err != nil {
		t.Skipf("127.0.0.1:1143 不可绑定（端口被占用?），跳过：%v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
	return "127.0.0.1:1143"
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("atoi %q: %v", p, err)
	}
	return h, port
}

// A 的核心回归：服务器 accept 后一句话不说，也不关连接。
// 修复前这里永久阻塞；修复后 ctx 到期会把 deadline 钉到过去，读立刻返回。
func TestUnblocksWhenServerNeverGreets(t *testing.T) {
	addr := plainFetchListener(t, func(c net.Conn) {
		// 故意什么都不做：既不发 greeting，也不 Close。
		// 连接挂在 <-done 上，保证在整个测试期间服务端不会主动断开
		// （否则测到的是服务端 EOF，不是我们的上界）。
		defer c.Close()
		time.Sleep(20 * time.Second)
	})

	host, port := splitAddr(t, addr)
	f := &Fetcher{}
	acc := &Account{EmailAddress: "a@example.com", IMAPHost: host, IMAPPort: port}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.fetchRawByTextproto(ctx, acc, "pw", 1, 4096)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("服务器从不发 greeting，却返回了 nil error —— 说明读超时被绕过了")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("ctx 已到期 800ms，实际耗时 %s 才返回 —— 降级通道仍然不受 ctx 约束", elapsed)
	}
	// 超时必须落在读 greeting 这一步：如果换个更靠前的错误，说明是别的原因。
	if !strings.Contains(err.Error(), "read greeting") {
		t.Fatalf("期望在 read greeting 处超时，实际 %v", err)
	}
}

// B 的核心回归：非明文端口（这里是随机端口）必须按隐式 TLS 处理。
// 修复前 `acc.IMAPPort == 993` 为 false -> 走明文 -> 报 read greeting: EOF。
func TestNonPlainPortGetsImplicitTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// 纯明文服务器：一收到 ClientHello 就断开，让 TLS 握手立刻失败，
		// 而不是让它挂到超时（挂到超时会和上面的 A 用例混淆）。
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
		_ = c.Close()
	}()

	host, port := splitAddr(t, ln.Addr().String())
	if isPlainIMAPPort(ln.Addr().String()) {
		t.Fatalf("随机端口 %s 不应被判定为明文，本用例的前提失效", ln.Addr())
	}
	f := &Fetcher{}
	acc := &Account{EmailAddress: "a@example.com", IMAPHost: host, IMAPPort: port}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = f.fetchRawByTextproto(ctx, acc, "pw", 1, 4096)
	if err == nil {
		t.Fatal("对纯明文端口做隐式 TLS 竟然成功了")
	}
	if !strings.Contains(err.Error(), "tls handshake") {
		t.Fatalf("期望 tls handshake 失败（说明确实按隐式 TLS 处理了），实际 %v", err)
	}
}

// happy path：明文端口上完整跑一遍 greeting/LOGIN/ID/SELECT/UID FETCH。
// 这条的作用是证明套上 deadlineConn + ctx 看门狗之后正常路径没被弄坏。
func TestPlainPortHappyPathFetchesBody(t *testing.T) {
	const body = "Subject: hi\r\n\r\nhello-invoice\r\n"
	addr := plainFetchListener(t, func(c net.Conn) {
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(20 * time.Second))
		br := bufio.NewReader(c)
		fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1 ID] fake ready\r\n")
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			// 按 tag 前缀分派。注意**不能**用 strings.Contains(line, "ID ")：
			// `A2 UID FETCH ...` 里也含 "ID "，会把 FETCH 误判成 ID 命令
			//（我第一版就这么写的，症状是客户端在 FETCH 循环里读到
			//  `A0 BAD Invalid command.` 然后一直等，最后 20s i/o timeout）。
			switch {
			case strings.HasPrefix(line, "A0 "):
				fmt.Fprint(c, "A0 BAD Invalid command.\r\n")
			case strings.HasPrefix(line, "A1 "):
				fmt.Fprint(c, "A1 OK LOGIN completed.\r\n")
			case strings.HasPrefix(line, "A2 ") && strings.Contains(strings.ToUpper(line), "SELECT"):
				fmt.Fprint(c, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1]\r\nA2 OK [READ-WRITE] SELECT completed.\r\n")
			case strings.HasPrefix(line, "A2 ") && strings.Contains(strings.ToUpper(line), "FETCH"):
				// 刻意模仿 Greenmail：partial 与 literal 之间**没有空格**。
				// go-imap 主路径在这一点上会失败（expected SP, got "{"），
				// 正是降级通道存在的理由。
				fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[]<0>{%d}\r\n", len(body))
				fmt.Fprint(c, body)
				fmt.Fprint(c, ")\r\nA2 OK FETCH completed.\r\n")
			default:
				fmt.Fprint(c, "A2 BAD no such command.\r\n")
			}
		}
	})

	host, port := splitAddr(t, addr)
	f := &Fetcher{}
	acc := &Account{EmailAddress: "a@example.com", IMAPHost: host, IMAPPort: port}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := f.fetchRawByTextproto(ctx, acc, "pw", 1, 4096)
	if err != nil {
		t.Fatalf("fetchRawByTextproto: %v", err)
	}
	if string(got) != body {
		t.Fatalf("取回的正文不对：\n got=%q\nwant=%q", string(got), body)
	}
}
