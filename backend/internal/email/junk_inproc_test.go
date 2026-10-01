package email

// junk_inproc_test.go — 需求 1「移到垃圾邮件箱」的真实 IMAP 协议路径。
//
// ## 为什么不用 Greenmail
//
// junk_greenmail_test.go（-tags=greenmail）覆盖的是同一条路径，但它需要
// Docker 起的 Greenmail 容器。本机 Docker daemon 未运行（§7ch 实测：
// `open //./pipe/docker_engine: The system cannot find the file specified`），
// 于是需求 1 这条**唯一不可逆**的操作在 CI 与本地都从未真正执行过 ——
// 而报告是「ok」。
//
// 本文件用 `net.Listen` 在进程内起一个**说 IMAP 协议**的最小服务器，
// 走真实 TCP + 真实 go-imap 客户端，不需要任何外部进程。
//
// ## 关键设计：断言「服务器收到了什么」，不是「调用没报错」
//
// MOVE 一旦发出，邮件就离开收件箱。断言 `moved == 3` 只能证明循环跑完了；
// 本文件让服务器**记录收到的每一条命令**，并断言：
//   - 服务器确实收到了 UID MOVE
//   - 目标信箱是**找出来的垃圾箱**，不是硬编码的 "Junk"
//   - 每封邮件都真的被移了（3 个 UID 一个不少）
//   - \Seen 没被置上（移动不该改变邮件的已读状态）
//
// 这四件事纯函数测不了，Greenmail 也只是间接覆盖。

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	imapclient "github.com/emersion/go-imap/v2/imapclient"
)

// ---------------------------------------------------------------------------
// 最小 IMAP 服务器
// ---------------------------------------------------------------------------

// testMailbox 是服务器 LIST 时报出的一个信箱。
type testMailbox struct {
	name  string
	attrs []imap.MailboxAttr
}

// imapServer 是够 junk.go 用的最小 IMAP 服务器。
//
// 它只实现被调用到的命令，其余一律回 BAD。**故意**不实现删除原邮件的能力 ——
// 这样一旦代码路径偏离预期，测试会**红**，而不是静悄悄通过。
type imapServer struct {
	ln net.Listener

	mu        sync.Mutex
	cmds      []string // 逐条收到的命令
	movedTo   []string // 每次 MOVE/COPY 的目标信箱
	movedUIDs []string // 每次 MOVE/COPY 带的 UID 串
	seenSet   bool     // 是否收到过会置 \Seen 的命令

	// mailboxes 是 LIST 返回的信箱列表。
	mailboxes []testMailbox
	// loginOK 控制 LOGIN 是否成功（用它反证「有没有真的去连」）。
	loginOK bool
	// createOK 控制 CREATE 是否成功（测「没有垃圾箱且建不出来」）。
	createOK bool

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newIMAPServer(t *testing.T, mailboxes []testMailbox) *imapServer {
	t.Helper()
	// 端口 0 = 随机空闲端口。isPlainIMAPPort 不认它，所以 dialer 里绕开
	// 端口判断直接 net.Dial（见 newJunkFixture）。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &imapServer{
		ln:        ln,
		mailboxes: mailboxes,
		loginOK:   true,
		createOK:  true,
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.shutdown)
	return s
}

func (s *imapServer) addr() string { return s.ln.Addr().String() }

func (s *imapServer) shutdown() {
	s.closeOnce.Do(func() { _ = s.ln.Close() })
	s.wg.Wait()
}

func (s *imapServer) record(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, cmd)
}

func (s *imapServer) recordMove(target string, uids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.movedTo = append(s.movedTo, target)
	s.movedUIDs = append(s.movedUIDs, uids...)
}

func (s *imapServer) hasCommandPrefix(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cmds {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	return false
}

func (s *imapServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

// sawCreate 判断服务器是否收到过针对指定信箱的 CREATE。
//
// 不用前缀匹配：go-imap 可能发 `CREATE "Junk"`（带引号），写死
// `HasPrefix(cmd, "CREATE Junk")` 会在加了引号时永远匹配不上 ——
// 第一版就是这么写的，红了才发现。
func (s *imapServer) sawCreate(mailbox string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cmds {
		rest, ok := strings.CutPrefix(c, "CREATE ")
		if !ok {
			continue
		}
		if strings.Trim(strings.TrimSpace(rest), `"`) == mailbox {
			return true
		}
	}
	return false
}

func (s *imapServer) serve() {
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

func (s *imapServer) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	writeln := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\r\n", args...)
		_ = w.Flush()
	}

	// 协议要求 server 先发 greeting。
	writeln("* OK [CAPABILITY IMAP4rev1 UIDPLUS MOVE] inproc test server ready")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		tag, rest, ok := strings.Cut(line, " ")
		if !ok {
			writeln("* BAD invalid command")
			continue
		}
		verb, args, _ := strings.Cut(rest, " ")

		switch strings.ToUpper(verb) {
		case "CAPABILITY":
			s.record("CAPABILITY")
			writeln("* CAPABILITY IMAP4rev1 UIDPLUS MOVE")
			writeln("%s OK CAPABILITY done", tag)

		case "LOGIN":
			s.record("LOGIN")
			if s.loginOK {
				writeln("%s OK LOGIN done", tag)
			} else {
				writeln("%s NO invalid credentials", tag)
			}

		case "LIST":
			s.record("LIST")
			for _, mb := range s.mailboxes {
				attrs := make([]string, 0, len(mb.attrs))
				for _, a := range mb.attrs {
					attrs = append(attrs, string(a))
				}
				writeln("* LIST (%s) \"/\" %s", strings.Join(attrs, " "), quoteIMAPString(mb.name))
			}
			writeln("%s OK LIST done", tag)

		case "SELECT", "EXAMINE":
			s.record(strings.ToUpper(verb))
			writeln("* 3 EXISTS")
			writeln("* 0 RECENT")
			writeln("* OK [UIDVALIDITY 1] UIDs valid")
			writeln("* OK [UIDNEXT 14] Predicted next UID")
			writeln("%s OK [READ-WRITE] SELECT done", tag)

		case "CREATE":
			s.record("CREATE " + args)
			if s.createOK {
				writeln("%s OK CREATE done", tag)
			} else {
				writeln("%s NO mailbox cannot be created", tag)
			}

		case "UID":
			s.handleUID(tag, args, writeln)

		default:
			s.record(strings.ToUpper(verb))
			writeln("%s BAD unsupported in this test server", tag)
		}
	}
}

// handleUID 处理所有 `UID <subcommand>` 形式。
func (s *imapServer) handleUID(tag, args string, writeln func(string, ...any)) {
	sub, rest, _ := strings.Cut(args, " ")

	switch strings.ToUpper(sub) {
	case "MOVE":
		uids, mailbox := parseUIDMove(rest)
		s.record("UID MOVE")
		s.recordMove(mailbox, uids...)
		writeln("* OK [COPYUID 1 1 %s] moved", strings.Join(uids, ","))
		writeln("%s OK MOVE done", tag)

	case "COPY":
		uids, mailbox := parseUIDMove(rest)
		s.record("UID COPY")
		s.recordMove(mailbox, uids...)
		writeln("%s OK COPY done", tag)

	case "STORE":
		// 无 MOVE 扩展时 go-imap 的回退路径之一。
		// 记录下来：它若带 \Seen 就是「读掉了邮件」，必须红。
		s.record("UID STORE " + rest)
		if strings.Contains(strings.ToUpper(rest), `\SEEN`) {
			s.mu.Lock()
			s.seenSet = true
			s.mu.Unlock()
		}
		writeln("%s OK STORE done", tag)

	case "SEARCH", "FETCH":
		s.record("UID " + strings.ToUpper(sub))
		writeln("* SEARCH")
		writeln("%s OK %s done", tag, sub)

	case "EXPUNGE":
		s.record("UID EXPUNGE")
		writeln("%s OK EXPUNGE done", tag)

	default:
		s.record("UID " + strings.ToUpper(sub))
		writeln("%s BAD unsupported", tag)
	}
}

// parseUIDMove 拆 `MOVE 11:13 Junk` / `COPY 11,12 "Junk E-mail"`。
//
// 必须**按引号切**而不是 strings.Fields：第一版用 Fields，把
// `COPY 11 "Other Folders/Junk"` 的信箱名截成 "Folders/Junk"，
// 于是断言「目标是完整名」失败。改完之后既能正确读出带空格的信箱名，
// 也能顺便验证 go-imap 确实加了引号。
func parseUIDMove(s string) (uids []string, mailbox string) {
	for _, f := range splitIMAPArgs(s) {
		f = strings.Trim(f, `"`)
		if f == "" {
			continue
		}
		if isUIDSet(f) {
			uids = append(uids, f)
			continue
		}
		mailbox = f
	}
	return uids, mailbox
}

// splitIMAPArgs 按空白切分，但把 "..." 里的空白当作普通字符。
func splitIMAPArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case !inQuote && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func isUIDSet(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != ':' && r != '*' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

// newJunkFixture 起一个指向测试服务器的 Fetcher（账户凭据用 testCrypto 加密）。
func newJunkFixture(t *testing.T, srv *imapServer) (*Fetcher, *Store, func()) {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	ctx := context.Background()

	seedAccount(t, store, "acct-junk", "u-junk", "ws-junk")
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET imap_host=$1, imap_port=$2 WHERE id='acct-junk'`,
		"127.0.0.1", portOf(srv.addr())); err != nil {
		t.Fatalf("point account at test server: %v", err)
	}

	// 凭据列必须是可解的密文，否则 login 之前就失败。
	c := testCrypto(t)
	enc, err := c.EncryptString("h8pass")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET credential_encrypted=$1 WHERE id='acct-junk'`, enc); err != nil {
		t.Fatalf("set credential: %v", err)
	}

	f := &Fetcher{
		store:  store,
		crypto: c,
		dialTLS: func(addr string, _ *imapclient.Options) (*imapclient.Client, error) {
			// 明文连：测试服务器不实现 TLS。isPlainIMAPPort 只认 143/1143，
			// 而这里是随机端口，所以绕开 dial 的端口判断直接连。
			conn, derr := net.DialTimeout("tcp", addr, 5*time.Second)
			if derr != nil {
				return nil, derr
			}
			return imapclient.New(conn, nil), nil
		},
	}
	return f, store, cleanup
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// standardJunkServer 是「一切正常」形态：INBOX + 带 \Junk 属性的 Junk。
func standardJunkServer(t *testing.T) *imapServer {
	t.Helper()
	return newIMAPServer(t, []testMailbox{
		{name: "INBOX"},
		{name: "Junk", attrs: []imap.MailboxAttr{imap.MailboxAttrJunk}},
		{name: "Sent"},
	})
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

// TestMoveUIDsToJunk_SendsRealMoveToDiscoveredMailbox 需求 1 的主用例。
//
// 断言的是**服务器侧观察到的协议事实**，不是返回值。
func TestMoveUIDsToJunk_SendsRealMoveToDiscoveredMailbox(t *testing.T) {
	srv := standardJunkServer(t)
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	moved, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{11, 12, 13})
	if err != nil {
		t.Fatalf("MoveUIDsToJunk: %v", err)
	}
	if len(moved) != 3 {
		t.Fatalf("moved %v, want 3 UIDs", moved)
	}

	// 1) 服务器确实收到了 MOVE。
	if !srv.hasCommandPrefix("UID MOVE") {
		t.Fatalf("server never received UID MOVE; it saw: %v", srv.commands())
	}

	srv.mu.Lock()
	targets := append([]string(nil), srv.movedTo...)
	uidSets := append([]string(nil), srv.movedUIDs...)
	seen := srv.seenSet
	srv.mu.Unlock()

	// 2) 目标是**发现**出来的垃圾箱，且每封一次。
	if len(targets) != 3 {
		t.Fatalf("server saw %d moves, want 3 (one per UID)", len(targets))
	}
	for i, tgt := range targets {
		if tgt != "Junk" {
			t.Errorf("move %d targeted %q, want the discovered \\Junk mailbox", i, tgt)
		}
	}

	// 3) 三封都移了。uidSets 每个元素是「一次 MOVE 带来的 UID 串」
	//    （可能是 "11" 或 "11:13"），所以要按分隔符摊平。
	seenUIDs := map[string]bool{}
	for _, set := range uidSets {
		for _, part := range strings.Split(set, ",") {
			for _, u := range strings.Split(part, ":") {
				seenUIDs[u] = true
			}
		}
	}
	for _, want := range []string{"11", "12", "13"} {
		if !seenUIDs[want] {
			t.Errorf("UID %s never reached the server; server saw %v", want, uidSets)
		}
	}

	// 4) MOVE 不该改 \Seen。
	if seen {
		t.Error("server received a STORE touching \\Seen; moving mail must not mark it read")
	}
}

// TestMoveUIDsToJunk_FallsBackToNameMatching 服务器不提供 \Junk 属性时，
// 必须靠**名字**找到垃圾箱。163/qq 都不发 \Junk 属性，这是真实形态。
func TestMoveUIDsToJunk_FallsBackToNameMatching(t *testing.T) {
	// 信箱名用 ASCII：IMAP 的 mailbox 名是 **modified UTF-7**，客户端
	// ExpectMailbox 会先解码。直接发 UTF-8 字节会让客户端报
	// `in LIST: invalid UTF-8` —— 那是协议要求，不是被测代码的问题。
	// 中文名的编码另有 §7ch 记录的 realprobe 覆盖，这里只验**层级剥离**。
	const hierarchicalJunk = "Other Folders/Junk"
	srv := newIMAPServer(t, []testMailbox{
		{name: "INBOX"},
		// 没有 \Junk 属性，只能靠名字 —— 而且带层级前缀。
		{name: hierarchicalJunk},
	})
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{11}); err != nil {
		t.Fatalf("MoveUIDsToJunk: %v", err)
	}
	srv.mu.Lock()
	targets := append([]string(nil), srv.movedTo...)
	srv.mu.Unlock()
	// 关键：目标是 **LIST 返回的完整名**，不是剥掉前缀后的 "Junk"。
	// 传 "Junk" 会被当作相对路径，邮件可能进错信箱。
	if len(targets) != 1 || targets[0] != hierarchicalJunk {
		t.Fatalf("moved to %v; must use the full hierarchical name returned by LIST", targets)
	}
}

// TestMoveUIDsToJunk_CreatesJunkWhenServerHasNone 一个垃圾箱都没有时
// 必须 CREATE 一个 —— 否则需求 1 在这些账户上永远不生效。
func TestMoveUIDsToJunk_CreatesJunkWhenServerHasNone(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{11}); err != nil {
		t.Fatalf("MoveUIDsToJunk: %v", err)
	}
	// CREATE 的信箱名可能被客户端加引号（`CREATE "Junk"`），所以判据不能
	// 写死 "CREATE Junk" —— 那是字符串前缀匹配，加了引号就永远匹配不上。
	// 第一版就是这么写的，测试红了才发现。
	if !srv.sawCreate("Junk") {
		t.Fatalf("server never received CREATE Junk; it saw: %v", srv.commands())
	}
	srv.mu.Lock()
	targets := append([]string(nil), srv.movedTo...)
	srv.mu.Unlock()
	if len(targets) != 1 || targets[0] != "Junk" {
		t.Errorf("moved to %v, want the newly created \"Junk\"", targets)
	}
}

// TestMoveUIDsToJunk_NoJunkAndCreateFails 两条路都走不通时必须**报错**，
// 不能假装成功。返回 moved=0 + err 才是对的 —— 若返回 nil error，
// 调用方会把「一封没移」当成「移完了」，然后把本地标记也写成已移。
func TestMoveUIDsToJunk_NoJunkAndCreateFails(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.createOK = false
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	moved, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{11})
	if err == nil {
		t.Fatal("expected an error when no junk mailbox exists and CREATE fails")
	}
	if len(moved) != 0 {
		t.Errorf("moved %v on a hard failure; must report 0", moved)
	}
	if srv.hasCommandPrefix("UID MOVE") {
		t.Error("a MOVE was issued even though the target mailbox could not be resolved")
	}
}

// TestMoveUIDsToJunk_EmptyUIDListIsNoOp 空列表不得建连接。
// 用 loginOK=false 反证：若它去连了，登录会失败并返回 error。
func TestMoveUIDsToJunk_EmptyUIDListIsNoOp(t *testing.T) {
	srv := standardJunkServer(t)
	srv.loginOK = false
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	moved, err := f.MoveUIDsToJunk(context.Background(), "acct-junk", nil)
	if err != nil {
		t.Fatalf("empty list must be a silent no-op, got %v", err)
	}
	if len(moved) != 0 {
		t.Errorf("moved %v on an empty list", moved)
	}
	if cmds := srv.commands(); len(cmds) != 0 {
		t.Errorf("empty list still touched the server: %v", cmds)
	}
}

// TestMoveUIDsToJunk_RejectsDisabledAccount disabled 账户必须拒绝。
//
// 不可逆操作上没有这层保护 = 一次误配置就把账户的邮件搬空。
func TestMoveUIDsToJunk_RejectsDisabledAccount(t *testing.T) {
	srv := standardJunkServer(t)
	f, store, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	if _, err := store.pool.Exec(context.Background(),
		`UPDATE email_accounts SET enabled=FALSE WHERE id='acct-junk'`); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, err := f.MoveUIDsToJunk(context.Background(), "acct-junk", []int64{11})
	if err == nil {
		t.Fatal("a disabled account must not be moved; that operation is irreversible")
	}
	if cmds := srv.commands(); len(cmds) != 0 {
		t.Errorf("disabled account still reached the server: %v", cmds)
	}
}

// TestMoveUIDsToJunk_SkipsNonPositiveUID 0/负数一定不是有效 IMAP UID
// （POP3 来源的邮件没有真正的 UID）。发出去必然失败，且**不能让一个坏 UID
// 拖垮整批** —— 好的那几封仍然要移走。
func TestMoveUIDsToJunk_SkipsNonPositiveUID(t *testing.T) {
	srv := standardJunkServer(t)
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	moved, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{0, -1, 11})
	if err == nil {
		t.Fatal("a batch containing invalid UIDs must surface an error, not report success")
	}
	if len(moved) != 1 || moved[0] != 11 {
		t.Errorf("moved %v, want only [11] (0 and -1 must be skipped)", moved)
	}
	srv.mu.Lock()
	n := len(srv.movedTo)
	srv.mu.Unlock()
	if n != 1 {
		t.Errorf("server saw %d moves, want 1 (invalid UIDs must not be sent)", n)
	}
}

// TestMoveUIDsToJunk_RejectsBadLogin 登录失败必须**在任何 MOVE 之前**停住。
//
// 这是不可逆操作的底线：认证没过就动邮件，等于把别人的邮件搬走。
func TestMoveUIDsToJunk_RejectsBadLogin(t *testing.T) {
	srv := standardJunkServer(t)
	srv.loginOK = false
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	moved, err := f.MoveUIDsToJunk(ctx, "acct-junk", []int64{11, 12})
	if err == nil {
		t.Fatal("a failed LOGIN must abort the move")
	}
	if len(moved) != 0 {
		t.Errorf("moved %v despite the login failure", moved)
	}
	if srv.hasCommandPrefix("UID MOVE") {
		t.Error("a MOVE was issued after LOGIN failed")
	}
}

// TestMoveEmailsToJunk_CountMatchesMoveUIDsToJunk 薄封装必须返回成功数。
func TestMoveEmailsToJunk_CountMatchesMoveUIDsToJunk(t *testing.T) {
	srv := standardJunkServer(t)
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	n, err := f.MoveEmailsToJunk(ctx, "acct-junk", []int64{11, 12})
	if err != nil {
		t.Fatalf("MoveEmailsToJunk: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}
