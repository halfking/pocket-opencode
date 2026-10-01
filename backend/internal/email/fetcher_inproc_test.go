package email

// fetcher_inproc_test.go — Fetcher 的 IMAP 正文取回路径（需求 2/3 依赖）。
//
// ## 覆盖什么
//
// `fetcher.go` 有 240 条语句 0 覆盖，其中 `FetchBody` 的
// maxBytes 双重截断（407-418）此前**完全没测**。而这正是我在
// §7bw 修过的那个真 bug 的同一处：`downloadPDF` 原来用
// `io.LimitReader` 到 20MB 时**返回 nil error**，截断后头部完好，
// 调用方只判 `isPDFBytes` 就落盘 + 标记成功 + 零报错。
//
// 同样的失效模式如果发生在 `FetchBody` 上，后果是「正文被静默截短，
// 发票 XML 解析失败却没有任何报错」。所以这里要钉住的是
// **截断发生且可见**，而不是「截了多少」。
//
// 复用 junk_inproc_test.go 里的 imapServer —— 同一个进程内 IMAP 服务器。

import (
	"context"
	"strings"
	"testing"
	"time"
)

// imapServerWithBody 起一个「INBOX 里有 body 这封邮件」的服务器。
func imapServerWithBody(t *testing.T, body string) *imapServer {
	t.Helper()
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}
	return srv
}

// ---------------------------------------------------------------------------
// UID FETCH 的 BODY 响应
// ---------------------------------------------------------------------------

// TestFetchBody_TruncatesToMaxBytes maxBytes 必须在**两边**都生效。
//
// 断言两层：
//  1. 客户端返回的字节数 <= maxBytes（客户端兜底截断）
//  2. 服务器收到的 FETCH 里带 `<0.N>` 部分取（服务器侧少发）
//
// 只断言第 1 条不够：它可能是客户端截的，而服务器其实把 200MB 全发了过来
// —— 网络与内存都白费，正是「读取上限」这个参数要防的事。
func TestFetchBody_TruncatesToMaxBytes(t *testing.T) {
	const (
		uid      = int64(11)
		maxBytes = 64
	)
	// 正文 5000 字节，远超上限。
	body := strings.Repeat("x", 5000)
	srv := imapServerWithBody(t, body)
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := f.FetchBody(ctx, "acct-junk", uid, maxBytes)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if len(got) > maxBytes {
		t.Errorf("FetchBody returned %d bytes, want <= %d", len(got), maxBytes)
	}
	if len(got) != maxBytes {
		t.Errorf("FetchBody returned %d bytes, want exactly %d (truncation must be exact)", len(got), maxBytes)
	}

	// 服务器侧必须收到了部分取，而不是把 5000 字节全发过来。
	if !srv.sawFetchPartial(0, maxBytes) {
		t.Errorf("server never received a partial BODY fetch <0.%d>; it saw: %v", maxBytes, srv.fetchCmds())
	}
}

// TestFetchBody_ClampsWhenServerIgnoresPartial 覆盖**客户端自己**的那一半截断。
//
// 为什么单独一个用例：TestFetchBody_TruncatesToMaxBytes 的服务器会老实按
// `<off.size>` 截断，于是 FetchBody 里那句兜底 `body[:maxBytes]` **永远走不到**。
// 我做过负控验证：把那句兜底换成 `if false {}`，6 个用例仍然全绿 ——
// 也就是说「双重截断都被覆盖」是错的，实际只覆盖了服务器那一半。
//
// 这一半才是防超限内存的那一半：部分取是**请求**，服务器拒不执行时
// （各家对部分取实现有差异）只能靠客户端自己兜底。真实走这条路的
// 后果就是 §7bw 那个真 bug 的原型：正文被静默截短，发票 XML 解析失败
// 却零报错。
func TestFetchBody_ClampsWhenServerIgnoresPartial(t *testing.T) {
	const (
		uid      = int64(11)
		maxBytes = 64
	)
	body := strings.Repeat("x", 5000)
	srv := imapServerWithBody(t, body)
	srv.ignorePartial = true // 服务器无视部分取，照发 5000 字节
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := f.FetchBody(ctx, "acct-junk", uid, maxBytes)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	// 服务器确实无视了部分取（否则本用例测不到客户端兜底）。
	if !srv.sawFetchPartial(0, maxBytes) {
		t.Fatalf("server never received <0.%d>; the case is not exercising what it claims: %v",
			maxBytes, srv.fetchCmds())
	}
	if len(got) != maxBytes {
		t.Errorf("got %d bytes from a server that ignored <0.%d>; want the client to clamp to %d",
			len(got), maxBytes, maxBytes)
	}
}

// TestFetchBody_NoMaxBytesFetchesWholeBody maxBytes<=0 表示不限，
// 此时**不能**发部分取。
func TestFetchBody_NoMaxBytesFetchesWholeBody(t *testing.T) {
	const uid = int64(11)
	body := "From: a@b.example\r\nSubject: hi\r\n\r\n" + strings.Repeat("y", 300)
	srv := imapServerWithBody(t, body)
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := f.FetchBody(ctx, "acct-junk", uid, 0)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if len(got) != len(body) {
		t.Errorf("got %d bytes, want the whole body (%d)", len(got), len(body))
	}
	if srv.sawFetchPartialAny() {
		t.Errorf("a partial fetch was issued for an unlimited request: %v", srv.fetchCmds())
	}
}

// TestFetchBody_UsesPeekSoMailIsNotMarkedRead 必须是 PEEK。
//
// 非 PEEK 会给邮件打上 \Seen —— 用户打开收件箱会发现邮件全变成已读。
// 这是**对用户邮箱的可见副作用**，不是内部状态。
func TestFetchBody_UsesPeekSoMailIsNotMarkedRead(t *testing.T) {
	srv := imapServerWithBody(t, "From: a@b.example\r\nSubject: s\r\n\r\nbody")
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := f.FetchBody(ctx, "acct-junk", 11, 0); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	// 判据一（主）：取件项必须带 `.PEEK`。
	//
	// 我第一版只断言「服务器没收到 STORE」，那是不对的：Peek 改成 false 时
	// 客户端**依然不发** STORE（STORE 是独立命令），用例会照样全绿
	// —— 实测 negctl-2 就是这么假绿的。真正的判据是取件项本身。
	if !srv.sawFetchPeek() {
		t.Errorf("the BODY fetch was not issued with PEEK; the mail would be marked \\Seen: %v",
			srv.fetchCmds())
	}
	// 判据二（辅）：服务器侧确实没收到任何会置 \Seen 的命令。
	if srv.hasCommandPrefix("UID STORE") {
		t.Errorf("a STORE was issued; BODY must be fetched with PEEK: %v", srv.commands())
	}
}

// TestFetchBody_RejectsDisabledAccount disabled 账户必须拒绝。
func TestFetchBody_RejectsDisabledAccount(t *testing.T) {
	srv := imapServerWithBody(t, "body")
	f, store, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	if _, err := store.pool.Exec(context.Background(),
		`UPDATE email_accounts SET enabled=FALSE WHERE id='acct-junk'`); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, err := f.FetchBody(context.Background(), "acct-junk", 11, 0); err == nil {
		t.Fatal("a disabled account must not be read")
	}
}

// TestFetchBody_RejectsBadLogin 认证失败必须在任何 FETCH 之前停住。
func TestFetchBody_RejectsBadLogin(t *testing.T) {
	srv := imapServerWithBody(t, "body")
	srv.loginOK = false
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := f.FetchBody(ctx, "acct-junk", 11, 0); err == nil {
		t.Fatal("a failed LOGIN must abort the fetch")
	}
	if srv.hasCommandPrefix("UID FETCH") {
		t.Errorf("a FETCH was issued after LOGIN failed: %v", srv.commands())
	}
}

// TestFetchBody_MissingUIDIsAnError 取不到邮件必须报错，不能返回空 + nil。
//
// 空 + nil 会被上层当成「这封邮件正文为空」，于是发票解析静默跳过。
func TestFetchBody_MissingUIDIsAnError(t *testing.T) {
	srv := imapServerWithBody(t, "body")
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := f.FetchBody(ctx, "acct-junk", 999, 0)
	if err == nil {
		t.Fatal("fetching a UID the server does not have must be an error, not empty+nil")
	}
	if len(got) != 0 {
		t.Errorf("got %d bytes alongside the error; want none", len(got))
	}
}
