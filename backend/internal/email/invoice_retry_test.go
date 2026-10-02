package email

import (
	"context"
	"strings"
	"testing"
)

// invoice_retry_test.go — 需求「有可能我们需要多次操作才能下载到发票文件」
// 的重试状态机。仓库里此前**没有任何测试覆盖它**。
//
// 状态机（invoice_harvest.go）：
//
//	download 成功                → downloaded（终态）
//	download 失败                → markRetry：Attempts >= 8 ? failed : pending
//	attempts >= 8 的 pending 记录 → CleanupStalePendingInvoices 置 failed（终态）
//
// pending 必须能在若干轮后收敛到 failed 终态。否则会有一张永远重试的发票
// 反复占用每轮采集预算（MaxInvoicesPerHarvestRound = 20），把正常发票挤掉。

// memBodyCache 是 BodyCache 接口的内存实现，用来让 harvestOne 走「缓存命中」
// 分支而不必真的连 IMAP。
type memBodyCache struct {
	data map[string][]byte
	puts int
}

func (c *memBodyCache) Get(emailID string, uid int64) ([]byte, error) {
	return c.data[emailID], nil
}

func (c *memBodyCache) Put(emailID string, uid int64, raw []byte) (string, error) {
	c.puts++
	if c.data == nil {
		c.data = map[string][]byte{}
	}
	c.data[emailID] = raw
	return "mem://" + emailID, nil
}

func (c *memBodyCache) Available() bool { return true }

// TestMarkRetry_ConvergesToFailedAfterMaxAttempts 固定状态机主路径：
// 未达上限 → pending；达到上限 → failed。
//
// 判据看**返回值与 Status 一致**，不是「没报错就算过」——调用方按返回值统计
// Failed、用户按 Status 看，两者不一致会让报告与界面各说各话。
func TestMarkRetry_ConvergesToFailedAfterMaxAttempts(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	h := &InvoiceHarvester{Store: store, DataDir: t.TempDir()}

	inv := &Invoice{ID: "inv-retry", EmailID: "em-retry", AccountID: "acct-retry",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "pending"}
	seedAccount(t, store, "acct-retry", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-retry", AccountID: "acct-retry", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "发票通知", Date: 1750000000,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < MaxInvoiceAttempts+3; i++ {
		inv.Attempts = i // 反映「已经试过几次」
		got := h.markRetry(ctx, inv, "link 503")
		seen[got] = true

		want := "pending"
		if i >= MaxInvoiceAttempts {
			want = "failed"
		}
		if got != want {
			t.Fatalf("第 %d 次重试：markRetry 返回 %q, want %q（Attempts=%d）", i, got, want, inv.Attempts)
		}
		if inv.Status != want {
			t.Fatalf("第 %d 次重试：Status=%q, want %q —— 返回值与落库状态不一致，"+
				"调用方按返回值统计、用户按 Status 看，两边会对不上", i, inv.Status, want)
		}
		if inv.LastError != "link 503" {
			t.Fatalf("LastError=%q, want 保留失败原因（运维要能看懂为什么失败）", inv.LastError)
		}
	}
	// 必须真的走过 pending 与 failed 两态，否则上面的循环可能是恒真的。
	if !seen["pending"] || !seen["failed"] {
		t.Fatalf("状态机没走完两个状态：%v", seen)
	}
}

// TestMarkRetry_SuccessPathIsTerminal 固定成功路径：downloaded 是终态，
// 文件名与来源被正确记录。
func TestMarkRetry_SuccessPathIsTerminal(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	h := &InvoiceHarvester{Store: store, DataDir: t.TempDir()}

	inv := &Invoice{
		ID: "inv-ok", EmailID: "em-ok", AccountID: "acct-ok",
		UserID: "user-1", WorkspaceID: "ws-1",
		Category: "其他", Seller: "云服务开票中心", Amount: 1280,
		InvoiceDate: "2026-09-28", Status: "downloaded", Attempts: 2,
	}
	// 发票记录必须先入库：saveInvoiceFile 末尾要 UpdateInvoiceHarvest，
	// 找不到行时它返回 "failed"，那会把这个用例变成测「外键缺失」而不是测成功路径。
	seedAccount(t, store, "acct-ok", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-ok", AccountID: "acct-ok", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Date: 1750000000,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}
	if got := h.saveInvoiceFile(ctx, inv, []byte(e2eInvoicePDF), "attachment"); got != "downloaded" {
		t.Fatalf("saveInvoiceFile = %q, want \"downloaded\"", got)
	}
	if inv.Status != "downloaded" {
		t.Fatalf("Status=%q, want \"downloaded\"", inv.Status)
	}
	if !strings.Contains(inv.FileName, "1280.00") || !strings.Contains(inv.FileName, "云服务开票中心") {
		t.Fatalf("FileName=%q 不符合 {费用类型}-{对方单位}-{金额}-{日期}.pdf", inv.FileName)
	}
	if inv.FileSource != "attachment" {
		t.Fatalf("FileSource=%q, want \"attachment\"", inv.FileSource)
	}
}

// TestHarvestOne_CachedRawStillCountsAttempt 钉住一个真实缺陷
// （2026-10-01 端到端验证需求 3 时发现）。
//
// 原来 Attempts++ 只写在「BodyCache 未命中、去 IMAP FETCH 原文」那一个分支里：
//
//	} else {
//		inv.Attempts++              // ← 只有这一条路径计数
//		raw, err = h.Fetcher.FetchMessageRaw(...)
//	}
//	...
//	return h.markRetry(ctx, inv, "no usable pdf/xml found in message")
//
// 于是**命中缓存**（以及 POP3 自愈）的发票虽然也会走到 markRetry，
// Attempts 却停在进入时的值 —— 状态机不再前进，pending → failed 的收敛
// 对它永久失效。这类发票每轮都进 MaxInvoicesPerHarvestRound=20 的预算，
// 把正常发票挤出去。
//
// 修法：Attempts++ 提到取原文之前，三条路径（缓存 / POP3 自愈 / IMAP FETCH）
// 统一计数。
func TestHarvestOne_CachedRawStillCountsAttempt(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-cached", "user-1", "ws-1")

	// 一封「有原文缓存但里面没有可用发票」的邮件：每轮都会失败重试。
	body := "正文里没有 PDF 也没有 XML"
	raw := buildE2EMIME(t, "发票通知", body, nil)
	// UID 必须 > 0：harvestOne 在 em.UID <= 0 时会直接 failed 返回
	// （客户端推送的历史邮件没有 UID），走不到取原文与计数那一步。
	if err := store.InsertEmail(ctx, Email{
		ID: "em-cached-acct-cached", AccountID: "acct-cached", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "发票通知",
		Snippet: body, Date: 1750000000, UID: 42,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-cached-acct-cached")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}

	cache := &memBodyCache{data: map[string][]byte{em.ID: raw}}
	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: t.TempDir(), BodyCache: cache,
	}
	inv := &Invoice{
		ID: "inv-cached", EmailID: em.ID, AccountID: "acct-cached",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "pending",
	}

	for round := 1; round <= MaxInvoiceAttempts; round++ {
		inv.Attempts = round - 1 // 进入本轮时的计数
		got := h.harvestOne(ctx, inv)
		if got == "downloaded" {
			t.Fatalf("第 %d 轮不该成功（正文里没有发票文件）", round)
		}
		if inv.Attempts != round {
			t.Fatalf("第 %d 轮后 Attempts=%d, want %d —— 缓存命中路径没有计数，"+
				"这张发票会永远停在 pending、永远到不了 failed 终态，"+
				"每轮还占采集预算挤掉正常发票", round, inv.Attempts, round)
		}
	}
	if inv.Status != "failed" {
		t.Fatalf("第 %d 轮后 Status=%q, want \"failed\"（重试耗尽应收敛到终态）", MaxInvoiceAttempts, inv.Status)

	}
}

// invoice_retry_test.go — 需求 3「有可能我们需要多次操作才能下载到发票文件」。
//
// 这条需求的实现是三段：
//   1. invoice_store.go:201 的 ListHarvestableInvoices 用
//      status IN ('new','pending') 捞起待采集记录 —— 下一轮仍能捡回来；
//   2. invoice_harvest.go:325 每尝试一次 Attempts++；
//   3. invoice_harvest.go:377 的 markRetry 按 Attempts 与 MaxInvoiceAttempts
//      的关系决定回到 pending（等下一轮）还是转 failed（终态，人工处理）。
//
// 三段任一断开，「多次操作」就退化成「只试一次」或「无限重试」：
//   - 捞取条件里漏掉 'pending' -> 第二轮就再也捡不回来，等于只试一次；
//   - markRetry 不看 Attempts -> 要么永远 pending 无限重试，要么第一次就 failed。
//
// 此前这三段**都没有测试**。这里补上，重点是状态机的边界。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip，与其它 email 集成测试一致）。
//
// 负控对照：
//   - 负控A：ListHarvestableInvoices 的条件去掉 'pending' -> 抓取不到已 pending 的发票；
//   - 负控B：markRetry 里的 Attempts 判断反转 -> 边界状态全错。

// seedInvoice 插入一条指定状态的发票记录。
//
// email_id 有 UNIQUE + 外键指向 emails(id)，account_id NOT NULL，
// 所以必须先建好对应的 account 与 email，不能凭空插。
// account 由 ensureInvoiceAccount 建一次（多次调用安全），本函数只补 email/invoice。
func ensureInvoiceAccount(t *testing.T, store *Store) {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_accounts WHERE id='acct-inv'`).Scan(&n); err != nil {
		t.Fatalf("count account: %v", err)
	}
	if n == 0 {
		seedAccount(t, store, "acct-inv", "u", "ws-inv")
	}
}

func seedInvoice(t *testing.T, store *Store, id, status string, attempts int) {
	t.Helper()
	ctx := context.Background()
	ensureInvoiceAccount(t, store)
	emailID := "em-" + id
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1,'acct-inv','ws-inv',$1,'s@example.com','subject','snippet',1700000000,1700000000)`,
		emailID); err != nil {
		t.Fatalf("seed email for %s: %v", id, err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            created_at, updated_at)
		VALUES ($1,$2,'acct-inv','u','ws-inv','','','',$3,$4::int,1700000000,1700000000)`,
		id, emailID, status, attempts); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

// 捞取条件必须同时包含 new 与 pending。
//
// 只捞 new 的话：第一次下载失败 → markRetry 置 pending → **下一轮再也不会
// 被捡回来**，需求「多次操作才能下载到」直接失效。
func TestListHarvestableInvoices_PicksUpPendingForRetry(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoice(t, store, "inv-new", "new", 0)
	seedInvoice(t, store, "inv-pending", "pending", 3)
	seedInvoice(t, store, "inv-downloaded", "downloaded", 1)
	seedInvoice(t, store, "inv-failed", "failed", 8)

	got, err := store.ListHarvestableInvoices(ctx, 100)
	if err != nil {
		t.Fatalf("ListHarvestableInvoices: %v", err)
	}
	seen := map[string]bool{}
	for _, inv := range got {
		seen[inv.ID] = true
	}
	for _, want := range []string{"inv-new", "inv-pending"} {
		if !seen[want] {
			t.Errorf("%s 未被捞起（status IN ('new','pending') 少了状态）", want)
		}
	}
	for _, dontWant := range []string{"inv-downloaded", "inv-failed"} {
		if seen[dontWant] {
			t.Errorf("%s 被捞起了（已终态的记录不该重新采集）", dontWant)
		}
	}
}

// markRetry 的状态机：未达上限回 pending 等下一轮，达到上限转 failed。
func TestMarkRetry_StatusMachine(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name      string
		attempts  int
		wantState string
		why       string
	}{
		{"第一次失败", 1, "pending", "还有余量，应等下一轮再试"},
		{"接近上限仍重试", MaxInvoiceAttempts - 1, "pending", "还差一次，不该提前判死"},
		{"刚好到上限", MaxInvoiceAttempts, "failed", "已达上限，应转终态"},
		{"远超上限", MaxInvoiceAttempts + 5, "failed", "早就该判死了"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seedInvoice(t, store, "inv-"+c.name, "pending", c.attempts)
			inv := &Invoice{ID: "inv-" + c.name, Status: "pending", Attempts: c.attempts}

			got := (&InvoiceHarvester{Store: store}).markRetry(ctx, inv, "模拟下载失败")
			if got != c.wantState {
				t.Errorf("markRetry(attempts=%d) = %q, want %q —— %s",
					c.attempts, got, c.wantState, c.why)
			}
			// 必须真的落库，否则下一轮读到的还是旧状态。
			var dbStatus string
			var dbErr string
			if err := store.pool.QueryRow(ctx,
				`SELECT status, COALESCE(last_error,'') FROM email_invoices WHERE id=$1`, inv.ID).
				Scan(&dbStatus, &dbErr); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if dbStatus != c.wantState {
				t.Errorf("库中 status=%q, want %q（markRetry 没有真正落库）", dbStatus, c.wantState)
			}
			if dbErr != "模拟下载失败" {
				t.Errorf("last_error=%q, want %q（失败原因要留给人看）", dbErr, "模拟下载失败")
			}
		})
	}
}

// 上限耗尽后必须可被收尾逻辑捞出来转 failed（CleanupStalePendingInvoices）。
// 上一条测的是 harvest 内部判定，这条测的是跨轮次的收尾，两者是不同的路径。
func TestCleanupStalePendingInvoices_FailsExhausted(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoice(t, store, "inv-exhausted", "pending", MaxInvoiceAttempts)
	seedInvoice(t, store, "inv-alive", "pending", 2)

	n, err := store.CleanupStalePendingInvoices(ctx, MaxInvoiceAttempts, 1700000000)
	if err != nil {
		t.Fatalf("CleanupStalePendingInvoices: %v", err)
	}
	if n != 1 {
		t.Errorf("n=%d, want 1（只有 attempts 已达上限的才该被转 failed）", n)
	}
	var status string
	if err := store.pool.QueryRow(ctx,
		`SELECT status FROM email_invoices WHERE id='inv-exhausted'`).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "failed" {
		t.Errorf("inv-exhausted status=%q, want failed", status)
	}
	if err := store.pool.QueryRow(ctx,
		`SELECT status FROM email_invoices WHERE id='inv-alive'`).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "pending" {
		t.Errorf("inv-alive status=%q, want pending（未达上限不应被动）", status)

	}
}
