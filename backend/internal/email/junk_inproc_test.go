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
	"log"
	"net"
	"regexp"
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
	fetchCmds_ []string // 每次 UID FETCH 的取件项（供 fetcher_inproc_test 断言）
	seenSet   bool     // 是否收到过会置 \Seen 的命令

	// bodyByUID 是各 UID 的正文（fetcher_inproc_test 用；junk 用例留空）。
	bodyByUID map[int64]string

	// ignorePartial 让服务器**无视**部分取、照发全文。
	//
	// 为什么需要它：handleFetch 默认会按 `<off.size>` 截断，于是
	// FetchBody 里那句客户端兜底 `body[:maxBytes]` 永远走不到 ——
	// 实测（negctl：删掉那句兜底，6 个用例仍然全绿）。要证明
	// **客户端自己也会截**，就必须有一个不配合的服务器。
	// 真实服务器里存在这种不配合者（各家对部分取的实现有差异），
	// 所以这不是人造的边界情况。
	ignorePartial bool

	// searchUIDs 是 `UID SEARCH` 回的命中集合（供 imap_resolve 的自愈路径）。
	// 空 = 0 命中。
	searchUIDs []int64

	// mailboxes 是 LIST 返回的信箱列表。
	mailboxes []testMailbox
	// loginOK 控制 LOGIN 是否成功（用它反证「有没有真的去连」）。
	loginOK bool
	// createOK 控制 CREATE 是否成功（测「没有垃圾箱且建不出来」）。
	createOK bool

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// fetchCmds 返回收到过的 UID FETCH 取件项。
func (s *imapServer) fetchCmds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.fetchCmds_...)
}

// sawFetchPartial 判断服务器是否收到过 `<offset.size>` 形式的部分取。
func (s *imapServer) sawFetchPartial(offset, size int) bool {
	want := fmt.Sprintf("<%d.%d>", offset, size)
	for _, c := range s.fetchCmds() {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// sawFetchPeek 判断服务器是否收到过带 `.PEEK` 的 BODY 取件。
//
// 判「有没有置 \Seen」的**正确**判据在这里：非 PEEK 的 BODY 取件由
// **服务器**置 \Seen，客户端不会为此发 STORE，所以查 STORE 查不出来。
func (s *imapServer) sawFetchPeek() bool {
	for _, c := range s.fetchCmds() {
		if strings.Contains(strings.ToUpper(c), "BODY.PEEK") {
			return true
		}
	}
	return false
}

// sawFetchPartialAny 判断是否收到过任何部分取。
func (s *imapServer) sawFetchPartialAny() bool {
	for _, c := range s.fetchCmds() {
		if strings.Contains(c, "<") && strings.Contains(c, ".") && strings.Contains(c, ">") {
			return true
		}
	}
	return false
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

		case "ID":
			// RFC 2971 客户端身份。FetchBody 开头会发 `ID (name "...")`。
			//
			// 第一版没实现它，服务器回 BAD —— 而客户端把 BAD 当成**致命**，
			// 于是**根本不发 FETCH**，症状是「uid not found」，
			// 看起来像 UID 解析错。真因在协议层，差了一层就断。
			s.record("ID")
			writeln("* ID NIL")
			writeln("%s OK ID done", tag)

		case "UID":
			s.handleUID(tag, args, writeln, w)

		default:
			s.record(strings.ToUpper(verb))
			writeln("%s BAD unsupported in this test server", tag)
		}
	}
}

// handleUID 处理所有 `UID <subcommand>` 形式。
func (s *imapServer) handleUID(tag, args string, writeln func(string, ...any), w *bufio.Writer) {
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

	case "SEARCH":
		s.record("UID SEARCH " + rest)
		// 必须真的回 UID 列表：`UIDSearch` 的结果集来自这里，
		// 永远回空 `* SEARCH` 的话 pickUniqueUID 只会走「0 命中」分支，
		// 于是 imap_resolve 那条自愈路径根本测不到成功情形。
		s.mu.Lock()
		searchUIDs := append([]int64(nil), s.searchUIDs...)
		s.mu.Unlock()
		parts := make([]string, 0, len(searchUIDs))
		for _, u := range searchUIDs {
			parts = append(parts, " "+strconv.FormatInt(u, 10))
		}
		writeln("* SEARCH%s", strings.Join(parts, ""))
		writeln("%s OK SEARCH done", tag)

	case "FETCH":
		s.handleFetch(tag, rest, w)

	case "EXPUNGE":
		s.record("UID EXPUNGE")
		writeln("%s OK EXPUNGE done", tag)

	default:
		s.record("UID " + strings.ToUpper(sub))
		writeln("%s BAD unsupported", tag)
	}
}

// handleFetch 回一个真实的 BODY[] 响应。
//
// 关键：**按请求里的部分取 <offset.size> 截断**再发。
// 若服务器无视部分取把全文发出来，客户端仍会截（它有兜底 `body[:maxBytes]`），
// 但那样就测不出「服务器侧少发了」——而这正是读取上限要保证的事。
func (s *imapServer) handleFetch(tag, rest string, w *bufio.Writer) {
	s.record("UID FETCH")

	s.mu.Lock()
	s.fetchCmds_ = append(s.fetchCmds_, rest)
	bodies := make(map[int64]string, len(s.bodyByUID))
	for k, v := range s.bodyByUID {
		bodies[k] = v
	}
	s.mu.Unlock()

	// FETCH 的第一个字段才是序号集：`FETCH 11 (BODY[TEXT]<0.64>)`。
	// 不能用 parseUIDMove —— 它把**最后一个**非数字 token 当 mailbox，
	// 而 FETCH 没有 mailbox，那个 token 会是取件项。
	uids := parseSeqSet(rest)
	// 找第一个本地有的 UID。
	var found int64 = -1
	for _, u := range uids {
		for _, cand := range strings.Split(u, ":") {
			n, err := strconv.ParseInt(cand, 10, 64)
			if err != nil {
				continue
			}
			if _, ok := bodies[n]; ok {
				found = n
				break
			}
		}
		if found > 0 {
			break
		}
	}
	if found < 0 {
		// 故意**不回** NO：真实服务器对不存在的 UID 也回 OK + 空结果集，
		// 由客户端去判断「没取到」。这才能测出调用方是否处理了空结果。
		log.Printf("[imapServer] no local body: FETCH rest=%q seqset=%v known=%v", rest, uids, bodies)
		_, _ = w.WriteString(tag + " OK FETCH done\r\n")
		_ = w.Flush()
		return
	}

	body := bodies[found]
	// 应用部分取 <offset.size>（除非服务器被要求无视部分取）。
	ignorePartial := s.ignorePartial
	if m := partialRe.FindStringSubmatch(rest); m != nil && !ignorePartial {
		off, _ := strconv.Atoi(m[1])
		size, _ := strconv.Atoi(m[2])
		if off > len(body) {
			off = len(body)
		}
		end := off + size
		if end > len(body) {
			end = len(body)
		}
		body = body[off:end]
	}

	// section 必须**原样回显**客户端请求的那个，不能写死。
	//
	//	FetchBody        请求 BODY.PEEK[TEXT]<0.64>  -> 响应 BODY[TEXT]<0>
	//	FetchMessageRaw  请求 BODY.PEEK[]<0.8388608> -> 响应 BODY[]<0>
	//
	// 客户端 matchFetchItemBodySection 会比对 Specifier 与 Part：写死成
	// BODY[TEXT] 时，`BODY[]` 的请求永远匹配不上，整封邮件取不回来。
	// 写成 `BODY[<0> UID 11]` 则会被客户端报 "section-spec: expected ']'"
	// —— 那是**响应格式**错，不是被测代码的缺陷。
	//
	// 部分取在**请求和响应里语法不同**，这是本文件最难的一个坑：
	//
	//	请求  BODY[TEXT]<0.64>     （offset.size，见 writeSectionPartial）
	//	响应  BODY[TEXT]<0>        （**只有 offset**，见 readPartialOffset）
	//
	// 客户端的 readSectionSpec 先 ExpectSpecial(']')、**再** readPartialOffset，
	// 而 readPartialOffset 是 `ExpectNumber` + `ExpectSpecial('>')` ——
	// 中间多一个 `.size` 就直接解码失败。我先前两个版本分别写成
	// `BODY[TEXT]<0.64>` 和手工多包一层的 `TEXT<<0.64>>`，两次都表现为
	// 「0 条消息 → uid not found」，症状完全指错方向（看起来像被测代码
	// 没把 UID 取回来，其实是服务器没按 RFC 写响应）。
	// 响应里 offset 必须回显：matchFetchItemBodySection 用
	// `(cmd.Partial == nil) != (resp.Partial == nil)` 判不匹配，
	// Size 反而**不能**回显（注释：not echoed back by the server）。
	section := requestedSectionSpec(rest)
	partial := ""
	if m := partialRe.FindStringSubmatch(rest); m != nil {
		partial = "<" + m[1] + ">"
	}

	// PEEK 语义：非 PEEK 的 BODY 取件会让服务器给这封邮件打上 \Seen。
	// 客户端**不会**为它发 STORE（STORE 是另一回事），所以「有没有
	// 收到 STORE」根本不是判断依据 —— 我第一版就只断言了 STORE，
	// 结果把 Peek 改成 false 用例照样全绿（实测 negctl-2）。
	// 真正的判据是取件项里有没有 `.PEEK`。
	if strings.Contains(strings.ToUpper(rest), "BODY") &&
		!strings.Contains(strings.ToUpper(rest), "BODY.PEEK") {
		s.mu.Lock()
		s.seenSet = true
		s.mu.Unlock()
	}
	// literal 语法：`{N}` 之后**必须紧接 N 字节内容**，中间不能有 CRLF
	// （我第一版用 writeln 分两行写，客户端报 expected SP, got "\r"）。
	// 注意部分取写在 `]` **之后**：`BODY[TEXT]<0>`，不是 `BODY[TEXT<0>]`。
	//
	// 最后一处、也是最隐蔽的一处：响应**必须带 `UID <uid>` 取件项**。
	// 客户端 writeFetchItems 会「Ensure we request UID as the first data item
	// for UID FETCH」，而路由回包靠的是 FetchCommand.recvUID ——
	// recvSeqNum 对 UIDSet 直接 `set, ok := cmd.numSet.(imap.SeqSet)` 返回
	// false。所以一个不带 UID 的 FETCH 回包会被**静默丢弃**：不报错、
	// Collect() 返回 0 条、调用方只看到 `uid not found`。
	// 这是本次三个用例卡了两天、症状完全指错方向的根因。
	//
	// `* N FETCH` 里的 N 是**序号**不是 UID；UID 只出现在数据项里。
	// 本服务器单邮箱单封，两者数值恰好相同，序号按 1 起算。
	var sb strings.Builder
	fmt.Fprintf(&sb, "* 1 FETCH (UID %d BODY[%s]%s {%d}\r\n", found, section, partial, len(body))
	sb.WriteString(body)
	sb.WriteString(")\r\n")
	sb.WriteString(tag + " OK FETCH done\r\n")
	log.Printf("[imapServer] FETCH response for uid=%d section=%q partial=%q len=%d", found, section, partial, len(body))
	_, _ = w.WriteString(sb.String())
	_ = w.Flush()
}

// parseSeqSet 取 FETCH/STORE/SEARCH 参数里的**第一个**序号集
// （`FETCH 11 (...)` / `FETCH 1,3:5 (...)`）。
func parseSeqSet(rest string) []string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil
	}
	first := rest
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		first = rest[:i]
	}
	var out []string
	for _, f := range strings.Split(first, ",") {
		if isUIDSet(strings.TrimSpace(f)) {
			out = append(out, strings.TrimSpace(f))
		}
	}
	return out
}

// bodySectionRe 匹配请求里的 BODY 取件项：`BODY[.PEEK][<spec>]`。
// 捕获组 1 是可选的 `.PEEK`，组 2 是 section spec（可能为空，即 `BODY[]`）。
var bodySectionRe = regexp.MustCompile(`(?i)BODY(\.PEEK)?\[([^\]]*)\]`)

// requestedSectionSpec 从 FETCH 取件项里取出要回显的 section spec。
//
// 取不到就回退成 "TEXT"：那是 FetchBody 的用法，且总比空串更容易看出
// 「回显失败」而不是「服务器没数据」。
func requestedSectionSpec(rest string) string {
	if m := bodySectionRe.FindStringSubmatch(rest); m != nil {
		return strings.TrimSpace(m[2])
	}
	return "TEXT"
}

// partialRe 匹配 IMAP 部分取 `<offset.size>`。
var partialRe = regexp.MustCompile(`<(\d+)\.(\d+)>`)

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
