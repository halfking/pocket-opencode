package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

// pop3_fetcher.go — POP3 备用同步通道。
//
// 当 IMAP 链路被服务端拒绝（典型如 163 的 `NO SELECT Unsafe Login`、
// 网易/阿里/腾讯企业邮出于风控对陌生 IP 阻断 SELECT）时，IMAP 整个对话
// 在 SELECT 之前就死了，fetcher 无法只走 BODY[] 拿原文。
//
// POP3 是这些服务商的传统同步通道：网易/QQ/Outlook 都默认开 POP3（明文或
// SSL），且 POP3 协议只发 USER/PASS/STAT/RETR/DELE，不走 SELECT — 绕开
// 风控。本文件实现纯 stdlib 的 POP3 客户端（参考 RFC 1939）：
//
//   - USER/PASS 鉴权；
//   - STAT 取邮箱统计（条数 + 字节数）；
//   - UIDL 拿每封邮件的 stable ID（用于增量去重）；不依赖 UID/MessageID。
//   - RETR <n> 取整封 RFC 5322 字节流（同 IMAP BODY[]<0.size> 的用途）；
//   - DELE <n> 标记删除（用 store.SeenUIDLSet 持久化已取 ID 实现幂等）。
//
// 拉回来的字节流与 IMAP 路径完全相同 —— 仍走 ParseMIMEMessage →
// InvoiceHarvester → savePDF → A4 网格导出。
//
// 选 stdlib 而不是第三方 POP3 库（github.com/knieriem/pop 等已归档/不存在），
// 减少模块维护面。POP3 协议只 9 个命令，实现量小。

// POP3Result 拉邮箱后聚合的结果。
type POP3Result struct {
	UIDLs     []string // 邮箱里所有邮件的稳定 ID（按服务器顺序）
	UIDLToIdx map[string]int
	NewUIDLs  []string // 本次新抓到的（已拉过的就跳过）
}

// pop3IdleTimeout 单次 POP3 会话的默认读写上限，与 IMAP 侧的 imapIdleTimeout 对齐。
//
// 取值理由：必须小于 pipeline 的单账户 90s 上界（DefaultAccountSyncTimeout）。
// 原值 120s 比它还长，于是「IMAP login 已经挂满 60s → 转 POP3 → POP3 又挂
// 120s」整轮必然超时，而 POP3 那条连接因为 handler 已放弃等待，一直占着不放
// （实测 Sync 总耗时 1m40.137s）。需要更短预算时用 FetchPOP3MailboxWithIdle。
const pop3IdleTimeout = 60 * time.Second

// FetchPOP3Mailbox 用 POP3 拉取邮箱到当前时间点，拉过的 UIDL 跳过。
//
// 读写上限用默认值 pop3IdleTimeout。需要在更短预算内跑（IMAP 已经耗掉
// 一段时间后的降级路径）时用 FetchPOP3MailboxWithIdle。
func FetchPOP3Mailbox(ctx context.Context, host string, useTLS bool, user, pass string, seen map[string]struct{}) ([]string, [][]byte, error) {
	return FetchPOP3MailboxWithIdle(ctx, host, useTLS, user, pass, seen, pop3IdleTimeout)
}

// FetchPOP3MailboxWithIdle 是可注入读写上限的版本。
//
// host:port   POP3 server（如 pop.163.com:995，端口 995 走隐式 TLS）。
// user/pass   鉴权信息。
// seen        之前已拉过的 UIDL（从 PG 持久化），保证增量。
// idle        单次会话的读写上限（<=0 时用 pop3IdleTimeout）。
//
// 返回每封新邮件的 raw bytes（按服务器顺序），可在外部转 email.Email 入库。
func FetchPOP3MailboxWithIdle(ctx context.Context, host string, useTLS bool, user, pass string, seen map[string]struct{}, idle time.Duration) ([]string, [][]byte, error) {
	if idle <= 0 {
		idle = pop3IdleTimeout
	}
	if !strings.Contains(host, ":") {
		// 默认端口 110 明文、995 隐式 TLS
		if useTLS {
			host += ":995"
		} else {
			host += ":110"
		}
	}
	// 建连上限也不能超过会话预算，否则光拨号就能把预算耗光。
	dialTimeout := 30 * time.Second
	if idle < dialTimeout {
		dialTimeout = idle
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, fmt.Errorf("pop3 dial %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(idle))

	var rawConn io.ReadWriteCloser = conn
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: strings.SplitN(host, ":", 2)[0]})
		// 握手也要有上限：TCP 连上但 TLS 卡住同样会占着连接不放。
		_ = tlsConn.SetDeadline(time.Now().Add(idle))
		if err := tlsConn.Handshake(); err != nil {
			return nil, nil, fmt.Errorf("pop3 tls handshake: %w", err)
		}
		_ = tlsConn.SetDeadline(time.Now().Add(idle))
		rawConn = tlsConn
	}
	defer rawConn.Close()

	// 整个会话共用一个 bufio.Reader：此前状态行走 textproto.Conn 的内部
	// bufio、正文又另建一层 bufio 包 rawConn，两层缓冲会互相吞数据。
	br := bufio.NewReader(rawConn)
	writeLine := func(format string, args ...any) error {
		_, err := fmt.Fprintf(rawConn, format+"\r\n", args...)
		return err
	}
	readLine := func() (string, error) {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	// readStatus 读单行状态响应（RFC 1939 §4："+OK ..."/"-ERR ..."）。
	// 不能用 textproto.ReadResponse：它只认 HTTP 风格数字 code，对 POP3 的
	// "+OK greeting" 报 `invalid response code`（163 真实服务器即踩中）。
	readStatus := func(op string) (string, error) {
		line, err := readLine()
		if err != nil {
			return "", fmt.Errorf("%s: %w", op, err)
		}
		switch {
		case line == "+OK" || strings.HasPrefix(line, "+OK "):
			return strings.TrimPrefix(line, "+OK"), nil
		case line == "-ERR" || strings.HasPrefix(line, "-ERR "):
			return "", fmt.Errorf("%s rejected: %s", op, strings.TrimPrefix(line, "-ERR"))
		default:
			return "", fmt.Errorf("%s: unexpected response %q", op, line)
		}
	}

	// 1. greeting
	if _, err := readStatus("greeting"); err != nil {
		return nil, nil, fmt.Errorf("read greeting: %w", err)
	}

	// 2. USER / PASS
	if err := writeLine("USER %s", user); err != nil {
		return nil, nil, fmt.Errorf("USER: %w", err)
	}
	if _, err := readStatus("USER"); err != nil {
		return nil, nil, err
	}
	if err := writeLine("PASS %s", pass); err != nil {
		return nil, nil, fmt.Errorf("PASS: %w", err)
	}
	if _, err := readStatus("PASS"); err != nil {
		return nil, nil, err
	}

	// 3. STAT（总条数 / 字节数，用于快速分页跳过空邮箱）
	if err := writeLine("STAT"); err == nil {
		if msg, err := readStatus("STAT"); err == nil {
			log.Printf("[email/pop3] STAT %s", strings.TrimSpace(msg))
		}
	}

	// 4. UIDL 拿所有稳定 ID
	if err := writeLine("UIDL"); err != nil {
		return nil, nil, fmt.Errorf("UIDL: %w", err)
	}
	if _, err := readStatus("UIDL"); err != nil {
		return nil, nil, err
	}
	var uidlLines []string
	for {
		line, err := readLine()
		if err != nil {
			return nil, nil, fmt.Errorf("UIDL read: %w", err)
		}
		if line == "." {
			break
		}
		uidlLines = append(uidlLines, line)
	}

	// 5. RETR 新邮件
	var newUIDLs []string
	var payloads [][]byte
	for _, line := range uidlLines {
		// line 形如 "1 abcdef" — index + UIDL；只取 UIDL 段
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		uidl := parts[1]
		if _, ok := seen[uidl]; ok {
			continue
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		// RETR <idx>
		if err := writeLine("RETR %d", idx); err != nil {
			log.Printf("[email/pop3] RETR %d err: %v", idx, err)
			continue
		}
		// RETR 响应是 `+OK` + 多行 body + `.`
		if _, err := readStatus("RETR"); err != nil {
			log.Printf("[email/pop3] RETR %d resp: %v", idx, err)
			continue
		}
		// 读字节流直到 "." 单行（与状态行共用同一个 br）
		payload, err := readPOP3Message(br)
		if err != nil {
			log.Printf("[email/pop3] RETR %d read err: %v", idx, err)
			continue
		}
		newUIDLs = append(newUIDLs, uidl)
		payloads = append(payloads, payload)
	}

	// 6. QUIT（NOOP 不删；DELE 真正删，但 IMAP 同步不会自动删 IMAP 端，
	//    所以本客户端不调 DELE——避免 POP3 拉过 = IMAP 也丢的风险。）
	_ = writeLine("QUIT")

	return newUIDLs, payloads, nil
}

// readPOP3Message 读 POP3 RETR 后的多行 body（行首 . 标记 end）。
//   - 行首 "." 是 RFC 822 转义（POP3 字节填充），需要去掉一个 "."。
//   - 单行 "." 表示 end。
//   - 服务器可任意终止，conn.SetDeadline 已设。
func readPOP3Message(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return buf, err
		}
		// POP3 行结束是 \r\n
		if strings.HasSuffix(line, "\r\n") {
			line = line[:len(line)-2]
		} else if strings.HasSuffix(line, "\n") {
			line = line[:len(line)-1]
		}
		// 单行 . 终止
		if line == "." {
			return buf, nil
		}
		// 行首 . 转义（byte-stuffing）
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		buf = append(buf, line...)
		buf = append(buf, '\r', '\n')
	}
}

// FetchPOP3MessageByIndex 按**位置序号**（第几封）补取单封邮件原文。
//
// 用途（真实死结的自愈，2026-10-01 实测）：
// POP3 落库的邮件 `uid` 就是这个位置序号。发票采集器拿到 POP3 来源的邮件时，
// IMAP 侧未必有对应的邮件（实测 QQ 账户 IMAP 侧 50 封里零封发票，两张真实
// QQ Wallet 发票只存在于 POP3 路径的 279 封里），所以既不能 IMAP FETCH，
// 又没有原文缓存可读——死结。
//
// 这个函数提供的第三条路：**回到 POP3 用位置序号 RETR**。位置序号在 POP3
// 侧是有效的（它就是 POP3 自己的编号），不像 IMAP UID 那样跨协议无意义。
//
// wantUIDL 非空时会先 UIDL 校验该位置的 UIDL 是否等于 wantUIDL，不等就拒绝
// 返回——防止「位置序号已经漂移」（服务器重排/删除）导致取到**另一封**邮件，
// 那正是当初拒绝合成 IMAP UID 要防的事故。wantUIDL 为空则跳过校验。
//
// idle <=0 时用 pop3IdleTimeout。它与全量拉取共用同一套连接/读行/RETR 逻辑。
func FetchPOP3MessageByIndex(ctx context.Context, host string, useTLS bool, user, pass string, index int, wantUIDL string, idle time.Duration) ([]byte, error) {
	if idle <= 0 {
		idle = pop3IdleTimeout
	}
	if index <= 0 {
		return nil, fmt.Errorf("pop3: invalid index %d", index)
	}
	if !strings.Contains(host, ":") {
		if useTLS {
			host += ":995"
		} else {
			host += ":110"
		}
	}
	dialTimeout := 30 * time.Second
	if idle < dialTimeout {
		dialTimeout = idle
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("pop3 dial %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(idle))

	var rawConn io.ReadWriteCloser = conn
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: strings.SplitN(host, ":", 2)[0]})
		_ = tlsConn.SetDeadline(time.Now().Add(idle))
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("pop3 tls handshake: %w", err)
		}
		_ = tlsConn.SetDeadline(time.Now().Add(idle))
		rawConn = tlsConn
	}
	defer rawConn.Close()

	br := bufio.NewReader(rawConn)
	writeLine := func(format string, args ...any) error {
		_, err := fmt.Fprintf(rawConn, format+"\r\n", args...)
		return err
	}
	readLine := func() (string, error) {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	readStatus := func(op string) (string, error) {
		line, err := readLine()
		if err != nil {
			return "", fmt.Errorf("%s: %w", op, err)
		}
		switch {
		case line == "+OK" || strings.HasPrefix(line, "+OK "):
			return strings.TrimPrefix(line, "+OK"), nil
		case line == "-ERR" || strings.HasPrefix(line, "-ERR "):
			return "", fmt.Errorf("%s rejected: %s", op, strings.TrimPrefix(line, "-ERR"))
		default:
			return "", fmt.Errorf("%s: unexpected response %q", op, line)
		}
	}

	if _, err := readStatus("greeting"); err != nil {
		return nil, err
	}
	// APOP 不是必须；USER/PASS 即可（163/QQ 都支持）。用 writeLine 直接发，
	// 与全量拉取保持一致。
	if err := writeLine("USER %s", user); err != nil {
		return nil, err
	}
	if _, err := readStatus("USER"); err != nil {
		return nil, err
	}
	if err := writeLine("PASS %s", pass); err != nil {
		return nil, err
	}
	if _, err := readStatus("PASS"); err != nil {
		return nil, err
	}

	// UIDL 交叉校验：确认这个位置序号当前的 UIDL 就是目标邮件的。
	if wantUIDL != "" {
		if err := writeLine("UIDL %d", index); err != nil {
			return nil, err
		}
		if _, err := readStatus("UIDL"); err != nil {
			return nil, err
		}
		line, err := readLine()
		if err != nil {
			return nil, fmt.Errorf("pop3 uidl read: %w", err)
		}
		// 行形如 "134 ZL0007_xxx"
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("pop3: unexpected UIDL line %q for index %d", line, index)
		}
		if parts[0] != strconv.Itoa(index) {
			return nil, fmt.Errorf("pop3: UIDL index mismatch (want %d, got %s) — position drifted", index, parts[0])
		}
		if parts[1] != wantUIDL {
			return nil, fmt.Errorf("pop3: UIDL mismatch at index %d (want %q, got %q) — refusing to fetch the wrong message", index, wantUIDL, parts[1])
		}
	}

	// RETR <index>
	if err := writeLine("RETR %d", index); err != nil {
		return nil, err
	}
	if _, err := readStatus("RETR"); err != nil {
		return nil, err
	}
	payload, err := readPOP3Message(br)
	if err != nil {
		return nil, fmt.Errorf("pop3 retr %d read: %w", index, err)
	}
	_ = writeLine("QUIT")
	return payload, nil
}
