package email

// invoice_harvest_all_test.go — 覆盖 `InvoiceHarvester.HarvestAll`
//（流水线第 4 步，需求 2/3 的「发票采集」入口）。
//
// ## 为什么这个文件存在
//
// `HarvestAll` 此前**唯一**的测试在 `fetcher_greenmail_test.go:181`，而那个文件
// 顶部是 `//go:build greenmail`。本机 Docker daemon 没跑起来，
// 于是「发票采集这一步」在**本机是零执行证据**的 —— 覆盖率表里
// `invoice_harvest.go:85 HarvestAll 0.0%` 就是这么来的。
//
// 这里改用 §7ci 那套进程内 IMAP 服务器，绕开 Docker。
//
// ## 测的是编排，不是下载算法
//
// `harvestOne` 的下载/解析逻辑另有测试（invoice_harvest_test.go 等）。
// `HarvestAll` 自己的职责只有四件：查库、**列库时**截断、逐张驱动、
// 收尾把重试耗尽的转 failed。这四件都在这里。
//
// 有一条要特别说明：下面多数用例让源邮件 `uid<=0`，于是 harvestOne 在
// 第一步就以 "no IMAP uid (pushed email)" 失败返回——**不碰网络**。
// 这是故意的：让「一张发票走完一轮」变成确定性的、可断言的。
// 真正走 IMAP 的那条路径由 TestHarvestAll_FetchesRawOverIMAP 覆盖。

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// harvestFixture 起一个指向进程内 IMAP 服务器的 InvoiceHarvester。
//
// 账户用 newJunkFixture 建的 'acct-junk'（凭据可解、host/port 指向测试服务器）。
func harvestFixture(t *testing.T, srv *imapServer) (*InvoiceHarvester, *Store, func()) {
	t.Helper()
	f, store, cleanup := newJunkFixture(t, srv)
	h := &InvoiceHarvester{
		Store:   store,
		Fetcher: f,
		DataDir: t.TempDir(),
		// 故意不给 XMLRenderer：XML 渲染要中文字体，本机不一定有。
		// 缺它时 XML 路径记 failed，是被设计的行为，不是本用例的目标。
	}
	return h, store, cleanup
}

// seedJunkInvoice 造一条挂在 'acct-junk' 下的发票 + 它对应的源邮件。
// uid<=0 表示「客户端推送的历史邮件」——没有 IMAP UID，拉不到原文。
func seedJunkInvoice(t *testing.T, store *Store, id, status string, attempts int) {
	t.Helper()
	seedJunkInvoiceAt(t, store, id, status, attempts, 1700000000)
}

// seedJunkInvoiceAt 同上，但可指定 created_at。
//
// ListHarvestableInvoices 是 `ORDER BY created_at LIMIT 100`，所以要确定性地
// 把某张发票排到预算之外，必须靠 created_at 区分，不能靠插入顺序。
func seedJunkInvoiceAt(t *testing.T, store *Store, id, status string, attempts int, createdAt int64) {
	t.Helper()
	ctx := context.Background()
	emailID := "em-" + id
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1,'acct-junk','ws-junk',$1,'s@example.com','发票主题','snippet',1700000000,$2)
		ON CONFLICT DO NOTHING`, emailID, createdAt); err != nil {
		t.Fatalf("seed email for %s: %v", id, err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            created_at, updated_at)
		VALUES ($1,$2,'acct-junk','u-junk','ws-junk','','','',$3,$4::int,$5,$5)`,
		id, emailID, status, attempts, createdAt); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

func invoiceStatus(t *testing.T, store *Store, id string) string {
	t.Helper()
	var s string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT status FROM email_invoices WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read status of %s: %v", id, err)
	}
	return s
}

// TestHarvestAll_UnconfiguredIsANoOp 缺配置必须返回空结果而不是 panic。
//
// 调度器装配顺序上 InvoiceHarvester 可能只填了一半字段
// （DataDir 没配、或 Fetcher 还没注入就被调）。
func TestHarvestAll_UnconfiguredIsANoOp(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	cases := map[string]*InvoiceHarvester{
		"nil harvester":  nil,
		"no store":       {},
		"no fetcher":     {Store: store, DataDir: t.TempDir()},
		"no datadir":     {Store: store, Fetcher: &Fetcher{}},
		"nothing at all": {},
	}
	for name, h := range cases {
		if got := h.HarvestAll(context.Background()); got.Processed != 0 || got.Downloaded != 0 ||
			got.Pending != 0 || got.Failed != 0 || got.Skipped != 0 {
			t.Errorf("%s: got %+v, want a zero HarvestResult", name, got)
		}
	}
}

// TestHarvestAll_OnlyPicksNewAndPending 已落盘的发票不得被重新采集。
//
// 捞取条件是 `status IN ('new','pending')`（invoice_store.go:201）。
// 若这条放宽到「全表捞」，已 downloaded 的发票会被反复重下、覆盖
// 已经发到飞书的同名文件——所以这里逐个状态各测一遍。
func TestHarvestAll_OnlyPicksNewAndPending(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	h, store, cleanup := harvestFixture(t, srv)
	defer cleanup()

	seedJunkInvoice(t, store, "inv-new", "new", 0)
	seedJunkInvoice(t, store, "inv-pending", "pending", 1)
	seedJunkInvoice(t, store, "inv-downloaded", "downloaded", 1)
	seedJunkInvoice(t, store, "inv-failed", "failed", 8)
	seedJunkInvoice(t, store, "inv-filed", "filed", 1)

	res := h.HarvestAll(context.Background())
	if res.Processed != 2 {
		t.Errorf("Processed=%d, want 2（只有 new + pending 该被这轮处理）: %+v", res.Processed, res)
	}
	if got := invoiceStatus(t, store, "inv-downloaded"); got != "downloaded" {
		t.Errorf("已 downloaded 的发票被改成了 %q；重下会覆盖已发飞书的同名文件", got)
	}
	if got := invoiceStatus(t, store, "inv-filed"); got != "filed" {
		t.Errorf("已 filed 的发票被改成了 %q", got)
	}
}

// TestHarvestAll_RoundBudgetTruncatesAtListTime 单轮预算必须在**列库时**生效。
//
// 这是 HarvestAll 相对 HarvestInvoices 的关键设计（invoice_harvest.go:94-101）：
// 它把 100 张截成 20 张再传下去，而不是塞满清单让下游靠 `i >= 预算` 跳。
// 两种写法行为不同：后者会把被跳过的计入 `Skipped`，让「本轮处理了多少张」
// 这个数字失真（真实邮箱上一轮 3~5 张，混进 80 个 Skipped 就没法看了）。
//
// 所以这里同时断言 Processed **和** Skipped：Skipped 必须是 0。
func TestHarvestAll_RoundBudgetTruncatesAtListTime(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	h, store, cleanup := harvestFixture(t, srv)
	defer cleanup()

	const total = MaxInvoicesPerHarvestRound + 5
	for i := 0; i < total; i++ {
		seedJunkInvoice(t, store, fmt.Sprintf("inv-bulk-%02d", i), "new", 0)
	}

	res := h.HarvestAll(context.Background())
	if res.Processed != MaxInvoicesPerHarvestRound {
		t.Errorf("Processed=%d, want %d（单轮预算）", res.Processed, MaxInvoicesPerHarvestRound)
	}
	if res.Skipped != 0 {
		t.Errorf("Skipped=%d, want 0；预算应在列库时截断，被跳过的张数不该混进本轮统计", res.Skipped)
	}
	// 超预算的那几张必须仍是**可采集状态**，而不是被判死。
	//
	// 注意它们停在 `new` 而不是 `pending`：没被处理过的行压根没被写过，
	// 状态压根没动过（只有 harvestOne 跑过才会写 pending/failed）。
	// 这正是「不会丢」的关键——顺延靠的是「本轮没碰它」，不是「写个状态」。
	deferred := 0
	for i := 0; i < total; i++ {
		switch invoiceStatus(t, store, fmt.Sprintf("inv-bulk-%02d", i)) {
		case "new", "pending":
			deferred++
		}
	}
	if deferred != 5 {
		t.Errorf("保持可采集的发票有 %d 张, want 5（超出单轮预算的 %d 张）: 超预算的发票被误判了终态",
			deferred, total-MaxInvoicesPerHarvestRound)
	}
}

// TestHarvestAll_ExhaustedRetriesBecomeFailed 重试耗尽必须在这轮收尾转 failed。
//
// `CleanupStalePendingInvoices` 本身在 invoice_retry_test.go 里单独测过；
// 这里测的是**接线**——HarvestAll 跑完一轮后，那条收尾逻辑到底有没有被调到。
// 曾经有个同款缺陷：Pipeline.Run 把 BuildInvoiceSummaryDocs 的两个返回值
// 丢进 `_`，导致定时路径的汇总文档恒为空（d8b5777e）。同一类错误。
//
// ## 关键：被测的发票必须**排在预算之外**
//
// 第一版我直接种一条 pending/exhausted，结果用例照样绿——因为它在预算内，
// harvestOne 跑一遍就把它置成 failed 了（uid<=0 走 "no IMAP uid" 分支），
// 收尾逻辑有没有被调**根本观察不到**。负控（把 maxAttempts 乘 1000000
// 让收尾永不命中）不转红才暴露出来。
//
// 正确做法：先种满 MaxInvoicesPerHarvestRound 张 new 把预算占掉，再种那条
// exhausted 的（created_at 更大，确定性排在最后）。它这轮不会被 harvestOne
// 碰到，于是状态的变化**只能**来自收尾逻辑。
func TestHarvestAll_ExhaustedRetriesBecomeFailed(t *testing.T) {
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	h, store, cleanup := harvestFixture(t, srv)
	defer cleanup()

	// 占满单轮预算。
	for i := 0; i < MaxInvoicesPerHarvestRound; i++ {
		seedJunkInvoiceAt(t, store, fmt.Sprintf("inv-filler-%02d", i), "new", 0, 1700000000)
	}
	// 重试耗尽的排在最后：这轮轮不到它。
	seedJunkInvoiceAt(t, store, "inv-exhausted", "pending", MaxInvoiceAttempts, 1700000001)
	// 对照：没耗尽的即使排在预算外也必须保持 pending。
	seedJunkInvoiceAt(t, store, "inv-alive", "pending", 2, 1700000002)

	res := h.HarvestAll(context.Background())
	if res.Processed != MaxInvoicesPerHarvestRound {
		t.Fatalf("Processed=%d, want %d（预算被 filler 占满）: %+v",
			res.Processed, MaxInvoicesPerHarvestRound, res)
	}

	if got := invoiceStatus(t, store, "inv-exhausted"); got != "failed" {
		t.Errorf("重试已达上限的发票收尾后是 %q, want failed；收尾逻辑没生效", got)
	}
	if got := invoiceStatus(t, store, "inv-alive"); got != "pending" {
		t.Errorf("未耗尽的发票被改成 %q；收尾逻辑不该误伤", got)
	}
}

// TestHarvestAll_NilFetcherStopsBeforeTouchingStore Fetcher 没装配时必须早退。
//
// 注意这条**测的不是** invoice_harvest.go:151 那个 `len(invoices)==0` 早退：
// `HarvestAll` 在第 86 行就先查 `h.Fetcher == nil` 并返回，压根走不到列库。
// 那个早退服务于**手动路径**（HarvestInvoices 被直接调用、调用方可能没有
// pool），由下面 TestHarvestInvoices_EmptyListIsANoOp 覆盖。
//
// 保留这条是因为早退顺序本身值得钉住：若哪天把 Fetcher 检查挪到列库之后，
// 这条就会开始扫库并把别人的 pending 收尾掉。
func TestHarvestAll_NilFetcherStopsBeforeTouchingStore(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	// seedJunkInvoice 要挂到 'acct-junk' 上，而本用例刻意不起 Fetcher，
	// 所以这里不像 harvestFixture 那样由 newJunkFixture 建账户。
	seedAccount(t, store, "acct-junk", "u-junk", "ws-junk")

	// Fetcher 为 nil -> HarvestAll 在查库前就返回，
	// 于是下面这条 pending 记录**不该**被收尾逻辑碰到。
	seedJunkInvoice(t, store, "inv-untouched", "pending", MaxInvoiceAttempts)

	h := &InvoiceHarvester{Store: store, DataDir: t.TempDir()}
	if got := h.HarvestAll(context.Background()); got.Processed != 0 {
		t.Fatalf("got %+v, want a zero result", got)
	}
	if got := invoiceStatus(t, store, "inv-untouched"); got != "pending" {
		t.Errorf("早退路径仍扫了全库：状态被改成 %q", got)
	}
}

// TestHarvestInvoices_EmptyListIsANoOp 空清单直接调用必须是无副作用的空操作。
//
// 这是 invoice_harvest.go:151-155 那个早退的真正场景：手动入口
// （POST /api/emails/invoices/harvest）已经按 user/workspace 取好清单，
// 用户点重试但一张都没选时就会传空列表进来。
//
// 关键在 `len(invoices)==0` 时**不**去调 CleanupStalePendingInvoices：
// 没有 pool 的调用方会直接炸在那里，而「本轮没处理任何东西」时
// 扫全库 pending 本来也没必要。
func TestHarvestInvoices_EmptyListIsANoOp(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-junk", "u-junk", "ws-junk")

	// 库里有一条重试耗尽的 pending：若空清单也去扫库收尾，它会被改成 failed。
	seedJunkInvoice(t, store, "inv-stale", "pending", MaxInvoiceAttempts)

	// Fetcher 故意为 nil：空清单路径不该需要它。真去调 CleanupStalePendingInvoices
	// 的话这条会因 nil Fetcher 之外的路径而暴露；更重要的是它能证明
	// 空清单时**没有**发生任何 store 写入。
	h := &InvoiceHarvester{Store: store, Fetcher: &Fetcher{}, DataDir: t.TempDir()}
	got := h.HarvestInvoices(context.Background(), nil)
	if got.Processed != 0 || got.Downloaded != 0 || got.Pending != 0 ||
		got.Failed != 0 || got.Skipped != 0 {
		t.Errorf("got %+v, want a zero HarvestResult", got)
	}
	if s := invoiceStatus(t, store, "inv-stale"); s != "pending" {
		t.Errorf("空清单仍然扫了全库 pending：状态被改成 %q", s)
	}
}

// TestHarvestAll_FetchesRawOverIMAP 真正走一次 IMAP 拉原文。
//
// 上面几条都靠 uid<=0 让 harvestOne 立刻失败，**没有一条碰过网络**。
// 这条补上真实链路：源邮件有 IMAP UID，服务器有一封带发票 PDF 链接的邮件，
// harvestOne 必须真的 FETCH 到原文并进入「找链接」阶段。
//
// 断言的是「服务器确实收到了整封原文的 BODY[] 取件」——
// 这是需求 2/3「多次操作才能下载到发票文件」的第一步。
func TestHarvestAll_FetchesRawOverIMAP(t *testing.T) {
	body := "From: billing@vendor.example\r\n" +
		"To: acct-junk@example.com\r\n" +
		"Subject: 电子发票\r\n" +
		"Message-ID: <real-msg-1@vendor.example>\r\n" +
		"Date: Tue, 30 Nov 2023 10:00:00 +0800\r\n" +
		"\r\n" +
		"发票链接：https://invoice.vendor.example/download/abc123.pdf\r\n"

	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.bodyByUID = map[int64]string{11: body}

	f, store, cleanup := newJunkFixture(t, srv)
	defer cleanup()
	ctx := context.Background()

	// 源邮件要真的有 UID，否则 harvestOne 在第一步就返回了。
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, uid, created_at)
		VALUES ('em-inv-real','acct-junk','ws-junk','<real-msg-1@vendor.example>',
		        'billing@vendor.example','电子发票','snippet',1700000000,11,1700000000)
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, subject, status, attempts,
		                            created_at, updated_at)
		VALUES ('inv-real','em-inv-real','acct-junk','u-junk','ws-junk','','电子发票','new',0,
		        1700000000,1700000000)`); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	h := &InvoiceHarvester{Store: store, Fetcher: f, DataDir: t.TempDir()}

	start := time.Now()
	res := h.HarvestAll(ctx)
	elapsed := time.Since(start)

	// 服务器必须收到过整封原文的取件。FetchMessageRaw 发的是 BODY[]
	// （无 section specifier）+ 部分取。
	sawWholeBody := false
	for _, c := range srv.fetchCmds() {
		if strings.Contains(c, "BODY") && !strings.Contains(c, "BODY[TEXT") {
			sawWholeBody = true
		}
	}
	if !sawWholeBody {
		t.Errorf("服务器没收到整封原文的 BODY[] 取件；harvestOne 根本没走到 IMAP: %v", srv.fetchCmds())
	}
	// 发票里的链接指向一个不存在的下载端点，harvestOne 会记 pending 等下一轮。
	// 这里只要求它**被处理过**且没有卡死（上界是采集器最关心的性质，
	// 历史上单张曾卡 13 分钟把整轮拖成无上界）。
	if res.Processed != 1 {
		t.Errorf("Processed=%d, want 1: %+v", res.Processed, res)
	}
	if elapsed > 60*time.Second {
		t.Errorf("单张采集耗时 %s，采集轮次必须是有上界的", elapsed)
	}
	if got := invoiceStatus(t, store, "inv-real"); got == "new" {
		t.Errorf("发票仍停在 new；处理过一轮后应当转入 pending/failed 以便下轮重试")
	}
}
