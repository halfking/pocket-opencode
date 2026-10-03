package email

// invoice_retry_backoff_test.go —— 「反复下载失败」的发票还会不会每轮都被重试。
//
// ## 要守的四条边界
//
//  1. 已经试够次数且仍无文件的 → 本轮**不再**被捞（止血：不再每轮白抓
//     几百 KB 的 HTML）；
//  2. 冷却期过后 → **必须重新回来**。这一条与第 1 条同样承重：只做第 1 条
//     就等于「永久搁死」，一次持续 5 天的服务抖动会把真发票永远锁死，而
//     URL 恢复后没有任何入口能把它捞回来。
//  3. **已有文件的**发票即使 attempts 超标也照常处理 —— 它已经成功过，
//     cap 与它无关；
//  4. 还没试够次数的 → 一律照常重试。
//
// ## 负控（实测可转红）
//
//  - 把 ListHarvestableInvoices 里的 `AND (...)` 整块删掉（退回无条件捞取）
//    → TestListHarvestableInvoices_ExhaustedStaysOut 转红。
//  - 去掉「已有文件一律照常」那一支
//    → TestListHarvestableInvoices_AlreadyDownloadedIsNeverCapped 转红。
//  - 把冷却期条件去掉（只剩 attempts 上限，即永久搁死）
//    → TestListHarvestableInvoices_ExhaustedReturnsAfterBackoff 转红。

import (
	"context"
	"testing"
	"time"
)

func seedRetryCandidate(t *testing.T, store *Store, id string, status string,
	attempts int, filePath string, updatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	acctID := "acct-retry-" + id
	if err := store.InsertAccount(ctx, &Account{
		ID: acctID, UserID: "u-retry", WorkspaceID: "ws-retry",
		DisplayName: "retry", EmailAddress: id + "@example.com",
		AuthType: "password", Enabled: true, CreatedAt: time.Now().Unix(),
	}, "enc"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := store.InsertEmail(ctx, Email{
		ID: "em-" + id, AccountID: acctID, WorkspaceID: "ws-retry",
		FromAddress: "noreply@example.com", Subject: "发票", Date: time.Now().Unix(), UID: 1,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices
			(id, email_id, account_id, workspace_id, user_id, status, attempts, file_path, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
		id, "em-"+id, acctID, "ws-retry", "u-retry", status, attempts, filePath, updatedAt.Unix())
	if err != nil {
		t.Fatalf("insert invoice %s: %v", id, err)
	}
}

func harvestIDs(t *testing.T, store *Store) map[string]bool {
	t.Helper()
	invs, err := store.ListHarvestableInvoices(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListHarvestableInvoices: %v", err)
	}
	out := map[string]bool{}
	for _, inv := range invs {
		out[inv.ID] = true
	}
	return out
}

func TestListHarvestableInvoices_ExhaustedStaysOut(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	now := time.Now()

	seedRetryCandidate(t, store, "inv-fresh-attempts", "pending", 1, "", now)
	seedRetryCandidate(t, store, "inv-exhausted", "pending",
		maxInvoiceHarvestAttempts+3, "", now)

	got := harvestIDs(t, store)
	if !got["inv-fresh-attempts"] {
		t.Error("只试了 1 次的发票被跳过了 —— 重试上限把正常重试也掐掉了")
	}
	if got["inv-exhausted"] {
		t.Errorf("已试 %d 次仍无文件的发票仍被捞取 —— 它每轮都会重抓同一批 HTML"+
			"（真实实例：每轮 635KB），这条就是本次要治的", maxInvoiceHarvestAttempts+3)
	}
}

func TestListHarvestableInvoices_ExhaustedReturnsAfterBackoff(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	// 两行**只差 updated_at**，其余完全相同（都是试够次数、仍无文件）：
	//   old   冷却期已过 → 应当重新回来
	//   fresh 刚失败不久 → 应当继续被挡住
	//
	// 为什么必须成对：只断言「旧的回来了」的话，把 cutoff 推到未来
	// （= 冷却期永不过期 = 永久搁死）这一变异**照样绿**——因为
	// `updated_at <= <未来>` 对任何一行都成立。那样这条判据就恒真了，
	// 它证明不了冷却期真的在起作用。成对之后，「没有冷却期」会让 fresh 那行
	// 漏进来而转红。
	old := time.Now().Add(-time.Duration(exhaustedInvoiceRetryBackoff+3600) * time.Second)
	fresh := time.Now().Add(-time.Duration(exhaustedInvoiceRetryBackoff/2) * time.Second)
	seedRetryCandidate(t, store, "inv-exhausted-old", "pending",
		maxInvoiceHarvestAttempts+3, "", old)
	seedRetryCandidate(t, store, "inv-exhausted-fresh", "pending",
		maxInvoiceHarvestAttempts+3, "", fresh)

	got := harvestIDs(t, store)
	if !got["inv-exhausted-old"] {
		t.Fatalf("冷却期已过的发票没有被重新捞回 —— 这是**永久搁死**：一次持续 %d 天的"+
			"服务抖动会让真发票永远不再被采集，而 URL 恢复后没有任何入口能捞它。",
			maxInvoiceHarvestAttempts)
	}
	if got["inv-exhausted-fresh"] {
		t.Errorf("刚失败不久（冷却期内）的发票被捞取了 —— 冷却期没有真正生效，" +
			"每一行都会退化成「每轮都重试」")
	}
}

func TestListHarvestableInvoices_AlreadyDownloadedIsNeverCapped(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	now := time.Now()

	// 已经拿到文件、但状态仍是 pending 的行：cap 与它无关。
	seedRetryCandidate(t, store, "inv-has-file", "pending",
		maxInvoiceHarvestAttempts+5, "invoices/x.pdf", now)

	got := harvestIDs(t, store)
	if !got["inv-has-file"] {
		t.Error("已落盘的发票因 attempts 超标被跳过 —— cap 只该管「从没成功过」的行，" +
			"否则已成功的凭证会被这条规则误伤")
	}
}

func TestListHarvestableInvoices_NewStatusIsUntouched(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	now := time.Now()

	// status='new' 从未采集过，attempts 为 0：必须照常。
	seedRetryCandidate(t, store, "inv-brand-new", "new", 0, "", now)
	// status='downloaded' 根本不在捞取范围里（既有行为，本次不许被改）。
	seedRetryCandidate(t, store, "inv-done", "downloaded", 1, "invoices/y.pdf", now)

	got := harvestIDs(t, store)
	if !got["inv-brand-new"] {
		t.Error("status='new' 的发票没有被捞取")
	}
	if got["inv-done"] {
		t.Error("status='downloaded' 的发票被捞取了 —— 既有契约是只捞 new/pending")
	}
}

// 常量自身的边界：上限必须 ≥2（一次就放弃等于没有重试），冷却期必须为正
// 且不超过发票候选的回看窗口（否则一封该被重试的票会跨过整个窗口再也回不来）。
func TestInvoiceRetryBackoffConstantsAreBounded(t *testing.T) {
	if maxInvoiceHarvestAttempts < 2 {
		t.Errorf("maxInvoiceHarvestAttempts = %d；一次就放弃等于没有重试", maxInvoiceHarvestAttempts)
	}
	if exhaustedInvoiceRetryBackoff <= 0 {
		t.Errorf("冷却期 = %d 秒；非正值意味着「试够了就永久搁死」", exhaustedInvoiceRetryBackoff)
	}
	if exhaustedInvoiceRetryBackoff > invoiceCandidateLookbackDays*86400 {
		t.Errorf("冷却期 = %d 秒，超过 %d 天的发票候选回看窗口 —— "+
			"一封该被重试的票会跨过整个窗口，之后再也没人看它",
			exhaustedInvoiceRetryBackoff, invoiceCandidateLookbackDays)
	}
}
