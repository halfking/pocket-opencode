package email

// imap_clientid_test.go — 网易 Coremail（163/126）要求的 RFC 2971 `ID` 头。
//
// 背景（§7d）：FetchMessageRaw 原先**不发** ID 头，163 直接回
// `NO SELECT Unsafe Login. Please contact kefu@188.com`。于是同一个 163 账户
// 「常规同步成功、拉原文 100% 失败」——发票二次提取与采集器都走这条路径，
// 等于 163 邮箱的发票功能整体不可用。
//
// 为什么必须写这个测试：handoff §7b 明确记着「163 的特殊头这次没被触发，
// 代码路径未被真实流量覆盖」。也就是说**修复本身没有任何自动化证据**——
// 真实邮箱只做过只读同步，163 那次也没触发风控。谁把 sendClientID 这一行
// 删掉，测试全绿、163 发票功能静默瘫掉。
//
// 下面的假服务器把 163 的真实行为编码成断言：没收到 ID 就拒绝 SELECT。
//
// 负控：删掉 mime.go 的 sendClientID 调用 -> 本文件转红。

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// idRequiringIMAPServer 是一个「像网易 Coremail 一样要求 ID 头」的假 IMAP 服务器。
//
// requireID=false 时退化成普通服务器（SELECT 一律通过），用于对照：
// 证明测试转红的原因是「没发 ID」，而不是服务器写错了。
type idRequiringIMAPServer struct {
	ln net.Listener

	mu        sync.Mutex
	sawID     bool
	idCommand string
	sawSelect bool
}

// startPlainIMAP 起一个明文 IMAP 假服务器（供 imapclient.DialInsecure 连接），
// 并把它挂到一个包级变量上供断言观察。
var srv *idRequiringIMAPServer

func startPlainIMAP(t *testing.T, requireID bool) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &idRequiringIMAPServer{ln: ln}
	srv = s
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn, requireID)
		}
	}()
	return ln
}

func (s *idRequiringIMAPServer) sawIDFlag() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawID
}

func (s *idRequiringIMAPServer) sawSelectFlag() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawSelect
}

func startIDRequiringIMAP(t *testing.T, requireID bool) (addr string, s *idRequiringIMAPServer) {
	t.Helper()
	ln := startPlainIMAP(t, requireID)
	return ln.Addr().String(), srv
}

func (s *idRequiringIMAPServer) state() (sawID bool, idCmd string, sawSelect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawID, s.idCommand, s.sawSelect
}

func (s *idRequiringIMAPServer) handle(conn net.Conn, requireID bool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(conn)
	fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 ID] fake coremail ready\r\n")

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag := line
		cmd := line
		if sp := strings.IndexByte(line, ' '); sp > 0 {
			tag = line[:sp]
			cmd = line[sp+1:]
		}
		// 必须剥掉 tag 再匹配：`a1 SELECT INBOX` 若整行匹配 HasPrefix("SELECT")
		// 永远不成立，会掉进 default 分支回 OK——假服务器「永远通过」，
		// 测试就变成了永远绿的摆设（写这个文件时被自检用例当场抓到）。
		upper := strings.ToUpper(strings.TrimSpace(cmd))

		switch {
		case strings.HasPrefix(upper, "ID "), upper == "ID":
			s.mu.Lock()
			s.sawID = true
			s.idCommand = line
			s.mu.Unlock()
			fmt.Fprintf(conn, "%s OK ID completed\r\n", tag)

		case strings.HasPrefix(upper, "LOGIN"):
			fmt.Fprintf(conn, "%s OK LOGIN completed\r\n", tag)

		case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "EXAMINE"):
			s.mu.Lock()
			s.sawSelect = true
			hasID := s.sawID
			s.mu.Unlock()
			// 163 的真实行为：缺客户端标识就拒绝 SELECT。
			if requireID && !hasID {
				fmt.Fprintf(conn, "%s NO SELECT Unsafe Login. Please contact kefu@188.com\r\n", tag)
				return
			}
			fmt.Fprintf(conn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1]\r\n%s OK [READ-WRITE] SELECT completed\r\n", tag)

		case strings.HasPrefix(upper, "FETCH"):
			fmt.Fprintf(conn, "* 1 FETCH (UID 1 BODY[] {2}\r\nhi)\r\n%s OK FETCH completed\r\n", tag)

		case strings.HasPrefix(upper, "LOGOUT"):
			fmt.Fprintf(conn, "* BYE\r\n%s OK LOGOUT completed\r\n", tag)
			return

		case strings.HasPrefix(upper, "CAPABILITY"):
			fmt.Fprintf(conn, "* CAPABILITY IMAP4rev1 ID\r\n%s OK CAPABILITY completed\r\n", tag)

		default:
			// 未知命令一律 OK：本测试只关心 ID 与 SELECT 的先后关系，
			// 不应该因为别的命令没实现而误报。
			fmt.Fprintf(conn, "%s OK completed\r\n", tag)
		}
	}
}

// dialAndSelect 用**真实的 go-imap v2 客户端**连上假服务器，然后调用生产函数
// selectInboxWithClientID（mime.go）——与 FetchMessageRaw 走的是同一行代码。
func dialAndSelect(t *testing.T, addr, email string) error {
	t.Helper()
	client, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		t.Fatalf("DialTLS: %v", err)
	}
	defer client.Close()
	if err := client.Login("user", "pass").Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return selectInboxWithClientID(client, email)
}

// 核心契约：生产路径 selectInboxWithClientID 必须在 SELECT 之前发 ID，
// 且在「像 163 一样要求 ID」的服务器上 SELECT 必须成功。
//
// 负控：把 mime.go 里 selectInboxWithClientID 的 sendClientID 删掉 -> 本条转红
// （SELECT 会收到 `NO ... Unsafe Login`）。这一条曾经**无效**：早期版本让测试
// 自己手搓 ID 命令，测的是测试代码，删生产代码照样全绿。
func TestSelectInboxWithClientID_SendsIDOnCoremailLikeServer(t *testing.T) {
	// 假服务器用明文，DialInsecure 更合适；用 DialTLS 会因 TLS 握手失败。
	ln := startPlainIMAP(t, true)
	addr := ln.Addr().String()

	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("DialInsecure: %v", err)
	}
	defer client.Close()
	if err := client.Login("user", "pass").Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := selectInboxWithClientID(client, "a@163.com"); err != nil {
		t.Fatalf("发了 ID 后 SELECT 仍失败: %v", err)
	}
	if !srv.sawIDFlag() {
		t.Fatal("服务器没有观察到 ID 命令")
	}
	if !srv.sawSelectFlag() {
		t.Fatal("服务器没有观察到 SELECT")
	}
}

// 对照组：服务器不要求 ID 时，即使生产代码照发 ID 也必须正常 SELECT。
// 证明上一条不是因为「ID 本身把服务器搞坏了」才通过的。
func TestSelectInboxWithClientID_WorksOnServerIgnoringID(t *testing.T) {
	ln := startPlainIMAP(t, false)
	addr := ln.Addr().String()
	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("DialInsecure: %v", err)
	}
	defer client.Close()
	if err := client.Login("user", "pass").Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := selectInboxWithClientID(client, "a@qq.com"); err != nil {
		t.Fatalf("不要求 ID 的服务器上 SELECT 失败: %v", err)
	}
}
