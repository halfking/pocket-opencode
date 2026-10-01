package email

// pop3_inproc_test.go — 需求 1/2/3 依赖的 163 POP3 路径（真实协议）。
//
// ## 为什么不用 Greenmail
//
// 与 junk_inproc_test.go 同一理由：本机 Docker daemon 未运行（§7ch），
// 而 `-tags=greenmail` 那两条用例需要容器。pop3_fetcher.go 有 **306 条语句
// 0 覆盖**，是 internal/email 里最大的未覆盖面，且 163 三个账户全走这条路。
//
// POP3 侧比 IMAP 好注入得多：FetchPOP3MailboxWithIdle 收的是
// `(host string, useTLS bool, ...)` 包级函数 —— 传 `useTLS=false` +
// 含端口的 host 就能明文连本进程内的服务器，不需要任何注入缝。
//
// ## 重点：byte-stuffing（行首点填充）
//
// POP3 的 body 以单行 `.` 结束，而**正文里本来就有以 `.` 开头的行**
// （签名分隔线 `. `、`.oOo`、`.` 结尾等）。RFC 1939 §5.1 要求服务器把
// 行首的点**多写一个**，客户端剥掉**恰好一个** —— 于是原文的 N 个点必须
// 原样还原成 N 个，而不是被「归一化成一个」。
//
// 本文件为此覆盖了 1/2/3/4 个前导点、点开头带文字、空行与行尾点。
//
// **重要更正**：我第一版把这个写成「真 bug 修复」，并断言旧实现
// `HasPrefix(line, "..")` 是错的。**那是错的** —— 实测 10 种输入后两种写法
// 在 RFC 合规服务器上逐例完全一致，负控也不转红（那正是判据正确的证据）。
// 现在代码里的 `HasPrefix(line, ".")` 只是更贴规范的写法，不是修复。

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 最小 POP3 服务器
// ---------------------------------------------------------------------------

// pop3Message 是服务器上的一封邮件。
type pop3Message struct {
	uidl string
	// raw 是**未填充**的原文（按真实邮件的样子，行首可以有点）。
	raw string
}

// pop3Server 是够 pop3_fetcher.go 用的最小 POP3 服务器。
type pop3Server struct {
	ln net.Listener

	mu       sync.Mutex
	cmds     []string
	retrIdx  []int  // 被 RETR 的位置序号
	deleted  []int  // 被 DELE 的位置序号（不该出现）
	// msgs 是收件箱内容。
	msgs []pop3Message
	// authOK 控制 USER/PASS 是否通过。
	authOK bool
	// failRETR 让指定序号的 RETR 失败（测部分失败）。
	failRETR map[int]bool

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newPOP3Server(t *testing.T, msgs []pop3Message) *pop3Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &pop3Server{
		ln:       ln,
		msgs:     msgs,
		authOK:   true,
		failRETR: map[int]bool{},
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.shutdown)
	return s
}

// host 返回 "127.0.0.1:port"；FetchPOP3MailboxWithIdle 见到 ':' 就不会补端口。
func (s *pop3Server) host() string { return s.ln.Addr().String() }

func (s *pop3Server) shutdown() {
	s.closeOnce.Do(func() { _ = s.ln.Close() })
	s.wg.Wait()
}

func (s *pop3Server) record(c string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, c)
}

func (s *pop3Server) recordRetr(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retrIdx = append(s.retrIdx, idx)
}

func (s *pop3Server) recordDele(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, idx)
}

func (s *pop3Server) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *pop3Server) served() []pop3Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pop3Message(nil), s.msgs...)
}

func (s *pop3Server) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *pop3Server) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	say := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\r\n", args...)
		_ = w.Flush()
	}

	say("+OK inproc POP3 test server ready")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		verb, args, _ := strings.Cut(line, " ")
		s.record(verb)

		switch strings.ToUpper(verb) {
		case "USER":
			if s.authOK {
				say("+OK user accepted")
			} else {
				say("-ERR no such user")
			}
		case "PASS":
			if s.authOK {
				say("+OK maildrop locked and ready")
			} else {
				say("-ERR invalid password")
			}
		case "STAT":
			total := 0
			for _, m := range s.served() {
				total += len(m.raw)
			}
			say("+OK %d %d", len(s.served()), total)
		case "UIDL":
			say("+OK unique-id listing follows")
			for i, m := range s.served() {
				say("%d %s", i+1, m.uidl)
			}
			say(".")
		case "RETR":
			s.handleRetr(args, say)
		case "DELE":
			// 客户端**不应该**发 DELE（见 pop3_fetcher.go:221 的注释：
			// 避免「POP3 拉过 = IMAP 也丢」）。记下来供断言。
			var idx int
			fmt.Sscanf(args, "%d", &idx)
			s.recordDele(idx)
			say("+OK message deleted")
		case "QUIT":
			say("+OK bye")
			return
		case "NOOP":
			say("+OK")
		default:
			say("-ERR unsupported")
		}
	}
}

func (s *pop3Server) handleRetr(args string, say func(string, ...any)) {
	var idx int
	if _, err := fmt.Sscanf(args, "%d", &idx); err != nil || idx < 1 || idx > len(s.served()) {
		say("-ERR no such message")
		return
	}
	s.recordRetr(idx)
	if s.failRETR[idx] {
		say("-ERR message temporarily unavailable")
		return
	}
	msg := s.served()[idx-1]
	say("+OK %d octets", len(msg.raw))
	// 逐行输出，并做 byte-stuffing：行首的点填充成两个（RFC 1939 §5.1）。
	//
	// 必须去掉末尾的空元素：`strings.Split("a\r\n", "\n")` 得到
	// ["a", ""]，多发的那一空行会让客户端把下一个命令（QUIT）当正文读走
	// —— 症状是「服务器从没收到 QUIT」，看起来像客户端没收尾。
	lines := strings.Split(msg.raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if strings.HasPrefix(l, ".") {
			l = "." + l
		}
		say("%s", l)
	}
	say(".")
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

// simpleMsg 造一封普通邮件（无行首点）。
func simpleMsg(uidl, subject, body string) pop3Message {
	return pop3Message{
		uidl: uidl,
		raw: fmt.Sprintf("From: sender@example.com\r\nTo: me@example.com\r\n"+
			"Subject: %s\r\nMessage-ID: <%s@example.com>\r\n\r\n%s",
			subject, uidl, body),
	}
}

// TestFetchPOP3Mailbox_FetchesOnlyUnseen POP3 的主用例：只取 seen 里没有的。
//
// 断言的是**服务器收到的 RETR 序号**，而不只是返回的 UIDL 数量 ——
// 数量对了但取错了邮件，与没跑一样坏。
func TestFetchPOP3Mailbox_FetchesOnlyUnseen(t *testing.T) {
	srv := newPOP3Server(t, []pop3Message{
		simpleMsg("uid-a", "第一封", "正文 A"),
		simpleMsg("uid-b", "第二封", "正文 B"),
		simpleMsg("uid-c", "第三封", "正文 C"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seen := map[string]struct{}{"uid-b": {}} // 中间那封已拉过
	uidls, payloads, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "h8pass", seen, 10*time.Second)
	if err != nil {
		t.Fatalf("FetchPOP3MailboxWithIdle: %v", err)
	}

	// 服务器侧：RETR 的必须是 1 和 3，**不能**碰 2。
	srv.mu.Lock()
	retr := append([]int(nil), srv.retrIdx...)
	srv.mu.Unlock()
	if len(retr) != 2 || retr[0] != 1 || retr[1] != 3 {
		t.Fatalf("server saw RETR %v, want [1 3] (uid-b was already seen)", retr)
	}

	if len(uidls) != 2 || uidls[0] != "uid-a" || uidls[1] != "uid-c" {
		t.Fatalf("uidls = %v, want [uid-a uid-c]", uidls)
	}
	if len(payloads) != 2 {
		t.Fatalf("got %d payloads, want 2", len(payloads))
	}
	// 正文必须完整（含头）。
	if !strings.Contains(string(payloads[0]), "Subject: 第一封") {
		t.Errorf("payload 0 lost its headers:\n%s", payloads[0])
	}
}

// TestFetchPOP3Mailbox_UnstuffsLeadingDots byte-stuffing 的核心用例。
//
// 正文里有以 `.` 开头的行（签名分隔线是邮件里极常见的 `. ` 一行）。
// 服务器按 RFC 1939 填充成两个点，客户端必须剥掉一个 —— **剥多了正文就变了，
// 剥少了正文里会多出点**。两种都是静默的数据损坏。
func TestFetchPOP3Mailbox_UnstuffsLeadingDots(t *testing.T) {
	// 故意构造三种行首点：单独一行 "."、"..leading"、".trailing"
	body := "第一行\r\n" +
		".\r\n" + // 签名分隔线：服务器发 ".."，客户端要还原成 "."
		"..leading two dots\r\n" + // 服务器发 "..."
		"...three dots\r\n" + // 服务器发 "...."
		"....four dots\r\n" + // 服务器发 "....."
		".trailing\r\n" + // 服务器发 "..trailing"
		"最后一行"
	srv := newPOP3Server(t, []pop3Message{{uidl: "uid-dot", raw: body}})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	uidls, payloads, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "h8pass", map[string]struct{}{}, 10*time.Second)
	if err != nil {
		t.Fatalf("FetchPOP3MailboxWithIdle: %v", err)
	}
	if len(uidls) != 1 {
		t.Fatalf("uidls = %v, want 1", uidls)
	}

	got := string(payloads[0])
	// RFC 1939 §5.1：服务器把行首**任意**点填充成一个，客户端剥掉**恰好一个**。
	// 于是原文的 N 个点必须**原样**还原成 N 个 —— 不是「归一化成一个」。
	if !strings.Contains(got, "\r\n.\r\n") {
		t.Errorf("the lone-dot signature line was not restored; got:\n%q", got)
	}
	if strings.Contains(got, "\r\n..\r\n") {
		t.Errorf("byte-stuffing was not undone (found a literal \"..\" line); got:\n%q", got)
	}
	// 原文两个点 -> 还原后仍是两个点。
	//
	// 负控说明：把实现改回 `HasPrefix(line, "..")`（旧写法）**这条不会红**。
	// 我一度以为那是 bug，实测 10 种输入后两种写法在 RFC 合规服务器上
	// 逐例完全一致 —— 服务器必然把行首点填充成 >=2 个，`..` 前缀恒成立。
	// 负控不转红在这里是**判据正确**的证据，不是护栏失效。
	if !strings.Contains(got, "\r\n..leading two dots\r\n") {
		t.Errorf("'..leading' must survive byte-stuffing unchanged; got:\n%q", got)
	}
	// 三个点 / 四个点：验证「剥掉恰好一个」而不是「剥到只剩一个」。
	if !strings.Contains(got, "\r\n...three dots\r\n") {
		t.Errorf("'...three dots' must keep 3 dots; got:\n%q", got)
	}
	if !strings.Contains(got, "\r\n....four dots\r\n") {
		t.Errorf("'....four dots' must keep 4 dots; got:\n%q", got)
	}
	if !strings.Contains(got, "\r\n.trailing\r\n") {
		t.Errorf("'.trailing' should decode to '.trailing'; got:\n%q", got)
	}
	// 正文首尾不得丢。
	if !strings.HasPrefix(got, "第一行") || !strings.Contains(got, "最后一行") {
		t.Errorf("payload lost its first/last line:\n%q", got)
	}
}

// TestFetchPOP3Mailbox_NeverIssuesDELE 客户端**不得**发 DELE。
//
// DELE 会真的删掉服务器上的邮件，而 IMAP 端不会同步删 —— 于是「POP3 拉过
// = IMAP 也丢」。这是不可逆的数据损失。
func TestFetchPOP3Mailbox_NeverIssuesDELE(t *testing.T) {
	srv := newPOP3Server(t, []pop3Message{
		simpleMsg("uid-a", "A", "正文"),
		simpleMsg("uid-b", "B", "正文"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, _, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "h8pass", map[string]struct{}{}, 10*time.Second); err != nil {
		t.Fatalf("FetchPOP3MailboxWithIdle: %v", err)
	}

	srv.mu.Lock()
	deleted := append([]int(nil), srv.deleted...)
	srv.mu.Unlock()
	if len(deleted) != 0 {
		t.Fatalf("client issued DELE for %v; that permanently deletes mail on the server", deleted)
	}
	// 也要确认确实正常收尾了（QUIT），否则会话是被掐断的。
	if !containsCmd(srv.commands(), "QUIT") {
		t.Errorf("client never sent QUIT; saw %v", srv.commands())
	}
}

// TestFetchPOP3Mailbox_AuthFailureIsAnError 认证失败必须报错，
// 且**不得**继续 RETR。
func TestFetchPOP3Mailbox_AuthFailureIsAnError(t *testing.T) {
	srv := newPOP3Server(t, []pop3Message{simpleMsg("uid-a", "A", "正文")})
	srv.authOK = false

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "wrong", map[string]struct{}{}, 10*time.Second)
	if err == nil {
		t.Fatal("a failed login must surface as an error")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error lost the cause (want the -ERR text): %v", err)
	}
	srv.mu.Lock()
	n := len(srv.retrIdx)
	srv.mu.Unlock()
	if n != 0 {
		t.Errorf("RETR issued %d times despite failed auth", n)
	}
}

// TestFetchPOP3Mailbox_RetrFailureSkipsOnlyThatMessage 单封 RETR 失败
// 不能让整轮归零 —— 其余邮件必须仍然入库（这正是「重新登录会丢邮件」
// 那类事故的防线）。
func TestFetchPOP3Mailbox_RetrFailureSkipsOnlyThatMessage(t *testing.T) {
	srv := newPOP3Server(t, []pop3Message{
		simpleMsg("uid-a", "A", "正文 A"),
		simpleMsg("uid-b", "B", "正文 B"),
		simpleMsg("uid-c", "C", "正文 C"),
	})
	srv.failRETR[2] = true // 中间那封取不到

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	uidls, payloads, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "h8pass", map[string]struct{}{}, 10*time.Second)
	if err != nil {
		t.Fatalf("a per-message RETR failure must not fail the round: %v", err)
	}
	if len(uidls) != 2 || len(payloads) != 2 {
		t.Fatalf("got %d uidls / %d payloads, want 2/2 (one RETR failed)",
			len(uidls), len(payloads))
	}
	if uidls[0] != "uid-a" || uidls[1] != "uid-c" {
		t.Errorf("uidls = %v, want [uid-a uid-c]", uidls)
	}
}

// TestFetchPOP3Mailbox_EmptyMailbox 空的收件箱必须返回空且**不报错**。
func TestFetchPOP3Mailbox_EmptyMailbox(t *testing.T) {
	srv := newPOP3Server(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	uidls, payloads, err := FetchPOP3MailboxWithIdle(ctx, srv.host(), false,
		"me@example.com", "h8pass", map[string]struct{}{}, 10*time.Second)
	if err != nil {
		t.Fatalf("an empty mailbox must not be an error: %v", err)
	}
	if len(uidls) != 0 || len(payloads) != 0 {
		t.Errorf("empty mailbox returned %v / %d payloads", uidls, len(payloads))
	}
}

// TestFetchPOP3Mailbox_DialFailureIsAnError 连不上必须报错而不是静默返回空 ——
// 静默空会被上游当成「这轮没有新邮件」，于是问题永远不被发现。
func TestFetchPOP3Mailbox_DialFailureIsAnError(t *testing.T) {
	// 先起再关，拿到一个**确定没人监听**的端口。
	srv := newPOP3Server(t, nil)
	addr := srv.host()
	srv.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _, err := FetchPOP3MailboxWithIdle(ctx, addr, false,
		"me@example.com", "h8pass", map[string]struct{}{}, 5*time.Second)
	if err == nil {
		t.Fatal("dialling a closed port must be an error, not a silent empty result")
	}
}

// TestFetchPOP3MessagesByUIDLs_EmptyListSkipsDial 空列表不建连接。
func TestFetchPOP3MessagesByUIDLs_EmptyListSkipsDial(t *testing.T) {
	srv := newPOP3Server(t, nil)
	srv.authOK = false // 若真去连，必然失败

	got, err := FetchPOP3MessagesByUIDLs(context.Background(), srv.host(), false,
		"me@example.com", "h8pass", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("an empty UIDL list must be a no-op, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if cmds := srv.commands(); len(cmds) != 0 {
		t.Errorf("empty list still connected: %v", cmds)
	}
}

// TestFetchPOP3MessagesByUIDLs_FindsRequestedOnly 按 UIDL 取回指定几封。
//
// 这是回填存量 POP3 邮件的入口：位置序号会漂移，只有 UIDL 稳定
// （§7s 实测：库里 134/135 标注为 QQ Wallet 发票，实际位置坐的是别的邮件）。
func TestFetchPOP3MessagesByUIDLs_FindsRequestedOnly(t *testing.T) {
	srv := newPOP3Server(t, []pop3Message{
		simpleMsg("uid-a", "A", "正文 A"),
		simpleMsg("uid-b", "B", "正文 B"),
		simpleMsg("uid-c", "C", "正文 C"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 要第 1 和第 3 封，**不按位置**而是按 UIDL 找。
	got, err := FetchPOP3MessagesByUIDLs(ctx, srv.host(), false,
		"me@example.com", "h8pass", []string{"uid-a", "uid-c"}, 10*time.Second)
	if err != nil {
		t.Fatalf("FetchPOP3MessagesByUIDLs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if !strings.Contains(string(got["uid-a"]), "正文 A") {
		t.Errorf("uid-a carries the wrong body:\n%s", got["uid-a"])
	}
	if !strings.Contains(string(got["uid-c"]), "正文 C") {
		t.Errorf("uid-c carries the wrong body:\n%s", got["uid-c"])
	}
	if _, ok := got["uid-b"]; ok {
		t.Error("uid-b came back although it was not requested")
	}
	// 位置 2（uid-b）绝不能被 RETR。
	srv.mu.Lock()
	retr := append([]int(nil), srv.retrIdx...)
	srv.mu.Unlock()
	for _, idx := range retr {
		if idx == 2 {
			t.Fatalf("server saw RETR 2 although uid-b was not requested: %v", retr)
		}
	}
}

func containsCmd(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}
