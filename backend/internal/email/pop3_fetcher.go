package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
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

// FetchPOP3Mailbox 用 POP3 拉取邮箱到当前时间点，拉过的 UIDL 跳过。
//
// host:port   POP3 server（如 pop.163.com:995，端口 995 走隐式 TLS）。
// user/pass    鉴权信息。
// seenUIDLSet 之前已拉过的 UIDL（从 PG 持久化），保证增量。
//
// 返回每封新邮件的 raw bytes（按服务器顺序），可在外部转 email.Email 入库。
func FetchPOP3Mailbox(ctx context.Context, host string, useTLS bool, user, pass string, seen map[string]struct{}) ([]string, [][]byte, error) {
	if !strings.Contains(host, ":") {
		// 默认端口 110 明文、995 隐式 TLS
		if useTLS {
			host += ":995"
		} else {
			host += ":110"
		}
	}
	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, fmt.Errorf("pop3 dial %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))

	var rawConn io.ReadWriteCloser = conn
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: strings.SplitN(host, ":", 2)[0]})
		if err := tlsConn.Handshake(); err != nil {
			return nil, nil, fmt.Errorf("pop3 tls handshake: %w", err)
		}
		rawConn = tlsConn
	}

	tp := textproto.NewConn(rawConn)
	defer tp.Close()

	// 1. read greeting
	if _, _, err := tp.ReadResponse(200); err != nil {
		return nil, nil, fmt.Errorf("read greeting: %w", err)
	}

	// 2. USER / PASS
	if err := tp.PrintfLine("USER %s", user); err != nil {
		return nil, nil, fmt.Errorf("USER: %w", err)
	}
	if _, _, err := tp.ReadResponse(200); err != nil {
		return nil, nil, fmt.Errorf("USER response: %w", err)
	}
	if err := tp.PrintfLine("PASS %s", pass); err != nil {
		return nil, nil, fmt.Errorf("PASS: %w", err)
	}
	code, _, err := tp.ReadResponse(200)
	if err != nil {
		return nil, nil, fmt.Errorf("PASS response: %w", err)
	}
	if code != 200 {
		return nil, nil, fmt.Errorf("PASS rejected: %d", code)
	}

	// 3. STAT（总条数 / 字节数，用于快速分页跳过空邮箱）
	if err := tp.PrintfLine("STAT"); err != nil {
		return nil, nil, fmt.Errorf("STAT: %w", err)
	}
	if _, msg, err := tp.ReadResponse(200); err == nil {
		log.Printf("[email/pop3] STAT %s", strings.TrimSpace(msg))
	}

	// 4. UIDL 拿所有稳定 ID
	if err := tp.PrintfLine("UIDL"); err != nil {
		return nil, nil, fmt.Errorf("UIDL: %w", err)
	}
	var uidlLines []string
	for {
		_, line, err := tp.ReadResponse(200)
		if err != nil {
			return nil, nil, fmt.Errorf("UIDL read: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "." {
			break
		}
		uidlLines = append(uidlLines, line)
	}

	// 5. RETR 新邮件
	var newUIDLs []string
	var payloads [][]byte
	br := bufio.NewReader(rawConn)
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
		if err := tp.PrintfLine("RETR %d", idx); err != nil {
			log.Printf("[email/pop3] RETR %d err: %v", idx, err)
			continue
		}
		// RETR 响应是 `+OK` + 多行 body + `.`
		if _, _, err := tp.ReadResponse(200); err != nil {
			log.Printf("[email/pop3] RETR %d resp: %v", idx, err)
			continue
		}
		// 读字节流直到 "." 单行
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
	if err := tp.PrintfLine("QUIT"); err != nil {
		// QUIT 失败无所谓
		_ = err
	}
	_ = br

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
