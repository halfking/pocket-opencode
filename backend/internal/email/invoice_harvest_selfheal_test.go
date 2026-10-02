package email

// invoice_harvest_selfheal_test.go — 覆盖 `recoverPOP3SourcedRaw`
//（invoice_harvest.go:226），POP3 来源发票的原文自愈。
//
// ## 为什么它重要
//
// 真实死结（2026-10-01 实测记在 invoice_harvest.go:305-307）：QQ 上 POP3 原文
// 缓存从未落盘，守卫又拒绝拿位置序号去 IMAP FETCH，两张真实 QQ Wallet 发票
// 因此**永远 failed**。自愈就是为解这个死结加的两条路，此前 0 覆盖。
//
// ## 一个必须说清的限制：POP3 那条腿**测不了**，原因是缺测试缝
//
// 自愈路径 1 走 `Fetcher.RefetchPOP3RawByIndex`，而它内部用
// `pop3EndpointFor(acc)` 从**邮箱域名**推导出 POP3 主机
// （163 → pop.163.com:995，qq → …），没有任何注入口。
//
// 对照 IMAP：`Fetcher.dialTLS`（fetcher.go:30）就是为此留的缝，注释写得很清楚
// 「仅为测试留缝……整条抓取链路因此无法自动化验证」。POP3 侧**没有**对应的缝：
// pop3_fetcher.go 的四个入口全部直接 `net.Dialer{}.DialContext`。
//
// 所以本文件只覆盖**路径 2（IMAP SEARCH 反查）**，以及路径 1 不可用时的降级。
// 「POP3 取回的是另一封邮件」这条判废逻辑本身在 `sameEmailMessage`
// （纯函数，已有 87% 覆盖）里，本文件用 IMAP 腿把同一道闸门再走一遍。
//
// 要补 POP3 腿需要先给 pop3_fetcher 加 dial 缝，那是生产代码改动，
// 已列为待决项，不在本轮擅自做。

import (
	"context"
	"strings"
	"testing"
)

// stubBodyCache 是一个可控的 BodyCache。
//
// miss=true 时 Get 永远未命中——这正是要触发自愈的前提（真实场景就是
// QQ 上缓存从未落盘）。它同时记录 Put，用来断言自愈成功后**顺手回填了缓存**。
type stubBodyCache struct {
	miss    bool
	puts    int
	lastID  string
	lastUID int64
}

func (s *stubBodyCache) Put(emailID string, uid int64, raw []byte) (string, error) {
	s.puts++
	s.lastID = emailID
	s.lastUID = uid
	return "stub/" + emailID, nil
}

func (s *stubBodyCache) Get(emailID string, uid int64) ([]byte, error) {
	if s.miss {
		return nil, nil
	}
	return nil, nil
}

// selfHealFixture 造一条 POP3 来源的发票邮件：id 带 `em-pop3-` 前缀、
// UID 是位置序号、缓存未命中，于是 harvestOne 必走自愈。
func selfHealFixture(t *testing.T, srv *imapServer, emUID int64, subject, msgID string) (*InvoiceHarvester, *Store, *stubBodyCache, func()) {
	t.Helper()
	f, store, cleanup := newJunkFixture(t, srv)
	ctx := context.Background()
	cache := &stubBodyCache{miss: true}

	emailID := "em-pop3-selfheal"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, uid, created_at)
		VALUES ($1,'acct-junk','ws-junk',$2,'billing@vendor.example',$3,'snippet',
		        1700000000,$4,1700000000)
		ON CONFLICT (id) DO UPDATE SET uid = EXCLUDED.uid, message_id = EXCLUDED.message_id,
		        subject = EXCLUDED.subject`,
		emailID, msgID, subject, emUID); err != nil {
		t.Fatalf("seed pop3 email: %v", err)
	}
	inv := &Invoice{
		EmailID:     emailID,
		AccountID:   "acct-junk",
		Subject:     subject,
		Status:      "new",
		InvoiceNo:   "",
		Title:       subject,
		Currency:    "CNY",
		Amount:      0,
		InvoiceDate: "",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "u-junk", "ws-junk"); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store:     store,
		Fetcher:   f,
		DataDir:   t.TempDir(),
		BodyCache: cache,
	}
	return h, store, cache, cleanup
}

// rawWithIDs 造一封带真实 Message-ID 的原始邮件。
func rawWithIDs(msgID, subject string) string {
	return "From: billing@vendor.example\r\n" +
		"To: acct-junk@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-ID: " + msgID + "\r\n" +
		"Date: Tue, 30 Nov 2023 10:00:00 +0800\r\n" +
		"\r\n" +
		"发票链接：https://invoice.vendor.example/download/abc.pdf\r\n"
}

const (
	selfHealMsgID = "<heal-1@vendor.example>"
	selfHealSubj  = "电子发票 2023-11"
)

// TestRecoverPOP3SourcedRaw_POP3UnavailableFallsBackToIMAP POP3 腿不可用时
// 必须能靠 IMAP 反查把原文找回来。
//
// 账户是 acct-junk@example.com，pop3EndpointFor 对 example.com 返回空，
// 于是 RefetchPOP3RawByIndex 报 "no POP3 endpoint" —— 这就是路径 1
// 不可用的真实形态（不是构造出来的）。
func TestRecoverPOP3SourcedRaw_POP3UnavailableFallsBackToIMAP(t *testing.T) {
	body := rawWithIDs(selfHealMsgID, selfHealSubj)
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}
	srv.searchUIDs = []int64{11} // 唯一命中

	h, store, cache, cleanup := selfHealFixture(t, srv, 7, selfHealSubj, selfHealMsgID)
	defer cleanup()
	ctx := context.Background()

	res := h.HarvestAll(ctx)
	if res.Processed != 1 {
		t.Fatalf("Processed=%d, want 1: %+v", res.Processed, res)
	}
	// 服务器确实被要求按头部反查了。
	sawSearch := false
	for _, c := range srv.commands() {
		if strings.Contains(c, "UID SEARCH") {
			sawSearch = true
		}
	}
	if !sawSearch {
		t.Errorf("自愈没有发起 IMAP SEARCH 反查: %v", srv.commands())
	}
	// 自愈成功后必须顺手回填缓存（同一封再被采集不必再付一次连接成本）。
	if cache.puts != 1 {
		t.Errorf("BodyCache.Put 调用了 %d 次, want 1；自愈拿到的原文应回填缓存", cache.puts)
	}
	// 状态不应停在 failed：找到原文了。
	var status string
	if err := store.pool.QueryRow(ctx,
		`SELECT status FROM email_invoices WHERE email_id='em-pop3-selfheal'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "failed" && strings.Contains(lastHarvestError(t, store), "self-heal failed") {
		t.Errorf("自愈明明成功了，发票仍被记为 failed：%q", lastHarvestError(t, store))
	}
}

func lastHarvestError(t *testing.T, store *Store) string {
	t.Helper()
	var s string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT COALESCE(last_error,'') FROM email_invoices WHERE email_id='em-pop3-selfheal'`).Scan(&s); err != nil {
		t.Fatalf("read last_error: %v", err)
	}
	return s
}

// TestRecoverPOP3SourcedRaw_SearchMissIsNotGuessed 0 命中必须报未解析，
// 绝不退化成「取最新一封」。
func TestRecoverPOP3SourcedRaw_SearchMissIsNotGuessed(t *testing.T) {
	body := rawWithIDs(selfHealMsgID, selfHealSubj)
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}
	srv.searchUIDs = nil // 0 命中

	h, _, _, cleanup := selfHealFixture(t, srv, 7, selfHealSubj, selfHealMsgID)
	defer cleanup()

	res := h.HarvestAll(context.Background())
	if res.Processed != 1 || res.Failed != 1 {
		t.Errorf("got %+v, want Processed=1 Failed=1（0 命中必须失败而不是猜）", res)
	}
	// 0 命中时绝不能去 FETCH：没有 UID 可用。
	if len(srv.fetchCmds()) != 0 {
		t.Errorf("0 命中却仍发了 FETCH: %v", srv.fetchCmds())
	}
}

// TestRecoverPOP3SourcedRaw_AmbiguousSearchIsNotGuessed 多于一条命中必须拒绝。
//
// 这是当初拒绝合成 IMAP UID 时最想守住的一条：同主题的邮件不止一封时，
// 取「最新」或「最旧」都是猜，猜错就是把别人的邮件存成这封发票。
func TestRecoverPOP3SourcedRaw_AmbiguousSearchIsNotGuessed(t *testing.T) {
	body := rawWithIDs(selfHealMsgID, selfHealSubj)
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}
	srv.searchUIDs = []int64{11, 12, 13} // 多命中

	h, _, _, cleanup := selfHealFixture(t, srv, 7, selfHealSubj, selfHealMsgID)
	defer cleanup()

	res := h.HarvestAll(context.Background())
	if res.Failed != 1 {
		t.Errorf("got %+v, want Failed=1（3 条命中必须拒绝，不能猜）", res)
	}
	if len(srv.fetchCmds()) != 0 {
		t.Errorf("多命中却仍发了 FETCH: %v", srv.fetchCmds())
	}
}

// TestRecoverPOP3SourcedRaw_ResolvedUIDReturningAnotherMessageIsDiscarded
// 反查到的 UID 取回**另一封**邮件时必须丢弃。
//
// 这是整段代码最要紧的一道闸门：位置序号/搜索结果都可能因为服务器重排
// 指向另一封，把它当这封的发票解析并存成 PDF 正是当初拒绝合成 UID 要防的事故。
func TestRecoverPOP3SourcedRaw_ResolvedUIDReturningAnotherMessageIsDiscarded(t *testing.T) {
	// 服务器在 uid=11 上放的是**另一封**邮件（不同 Message-ID、不同主题）。
	other := rawWithIDs("<someone-else@vendor.example>", "别人的发票")
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: other}
	srv.searchUIDs = []int64{11} // 唯一命中，但内容对不上

	h, store, cache, cleanup := selfHealFixture(t, srv, 7, selfHealSubj, selfHealMsgID)
	defer cleanup()

	res := h.HarvestAll(context.Background())
	if res.Failed != 1 {
		t.Errorf("got %+v, want Failed=1（取回另一封邮件必须判失败）", res)
	}
	if errStr := lastHarvestError(t, store); !strings.Contains(errStr, "DIFFERENT message") {
		t.Errorf("last_error=%q, want 记录「取回的是另一封」；失败原因要留给人看", errStr)
	}
	// 更要紧的是：绝不能把别���邮件当成发票存下来。
	var n int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_invoices WHERE file_path <> ''`).Scan(&n); err != nil {
		t.Fatalf("count invoices with files: %v", err)
	}
	if n != 0 {
		t.Errorf("有 %d 张发票被存了文件；取回的明明是别人的邮件", n)
	}
	if cache.puts != 0 {
		t.Errorf("被丢弃的原文仍被回填进 BodyCache（%d 次）；下一轮会拿到同一封错的", cache.puts)
	}
}

// TestRecoverPOP3SourcedRaw_UniqueHitWithMatchingMessageSucceeds 正常路径：
// 唯一命中且内容对得上，自愈成功。
//
// 与上面那条构成对照：同样唯一命中，唯一区别是内容。
func TestRecoverPOP3SourcedRaw_UniqueHitWithMatchingMessageSucceeds(t *testing.T) {
	body := rawWithIDs(selfHealMsgID, selfHealSubj)
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}
	srv.searchUIDs = []int64{11}

	h, store, cache, cleanup := selfHealFixture(t, srv, 7, selfHealSubj, selfHealMsgID)
	defer cleanup()

	h.HarvestAll(context.Background())

	if errStr := lastHarvestError(t, store); strings.Contains(errStr, "DIFFERENT message") {
		t.Errorf("取回的正是同一封（Message-ID 相等），却被判成不同邮件：%q", errStr)
	}
	if cache.puts != 1 {
		t.Errorf("BodyCache.Put 调用 %d 次, want 1", cache.puts)
	}
}

// TestResolveRealUIDByHeader_EmptySubjectIsRefused 空主题不得反查。
//
// 主题是 SEARCH 的必选条件，空主题发出去等于盲搜。
func TestResolveRealUIDByHeader_EmptySubjectIsRefused(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	f, _, cleanup := newJunkFixture(t, srv)
	defer cleanup()

	if _, err := f.ResolveRealUIDByHeader(context.Background(), "acct-junk", "a@b.example", "", 1700000000); err == nil {
		t.Error("空主题必须直接报错，不得发起 SEARCH")
	}
	for _, c := range srv.commands() {
		if strings.Contains(c, "SEARCH") {
			t.Errorf("空主题仍发起了 SEARCH: %v", srv.commands())
		}
	}
}
