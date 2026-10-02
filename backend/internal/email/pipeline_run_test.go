package email

// pipeline_run_test.go — 需求 1/2/3/4 的编排核心：Pipeline.Run。
//
// ## 为什么测这里
//
// 覆盖率实测（cov.email.out，go 自带 coverprofile）：
// `internal/email` 总体 53.5%，其中 **pipeline.go 的 Run 主干 5 个 stepStart
// 区块（298/302/306/310/314）全部 0 覆盖**。也就是说「每天定时收信 →
// 清垃圾 → 提醒 → 采发票 → 推飞书」这条主链路**从未被任何测试整体跑过**，
// 每次只测了单步的纯函数。
//
// 单步纯函数绿 ≠ 编排正确。这里钉的是**顺序、降级、隔离**三件事。
//
// ## 安全性
//
// 本文件全程 SpamDryRun=true（预演），**不发任何 IMAP MOVE**；
// Fetcher / Pusher / Notifier / Ledger 全部留 nil，验证的是
// 「缺依赖时优雅降级」——这本身就是需求 6 设备本地化必须成立的前提：
// 设备上此刻没配飞书，流水线不能因此崩掉。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 假依赖
// ---------------------------------------------------------------------------

// fakePusher 记录推送调用。它必须可注入，否则第 5 步完全测不到。
type fakePusher struct {
	available bool
	err       error
	pushed    []string // 发票 ID
}

func (f *fakePusher) PushInvoice(_ context.Context, inv Invoice, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.pushed = append(f.pushed, inv.ID)
	return nil
}

func (f *fakePusher) Available() bool { return f.available }

// fakeNotifier 记录提醒派发。
type fakeNotifier struct {
	notified []string // 邮件 ID
	err      error
}

func (f *fakeNotifier) NotifyImportantEmail(_ context.Context, e Email) error {
	if f.err != nil {
		return f.err
	}
	f.notified = append(f.notified, e.ID)
	return nil
}

// ---------------------------------------------------------------------------
// 构造
// ---------------------------------------------------------------------------

// newPipelineFixture 造一个可跑通全流程的 Pipeline：
//   - 隔离 schema 的 Store
//   - t.TempDir() 作为 DataDir（汇总文档真的落盘，才能验证「离线兜底」）
//   - SpamDryRun=true：**绝不发 IMAP MOVE**
//
// Fetcher 用 syncHook 注入一个「立刻成功」的假实现 —— 真 Fetcher 会去连
// IMAP，测试里既慢又不稳定。用钩子而不是改断言：`AccountsSynced` 只在
// `r.err == nil` 时递增，让 Fetcher 报错去断言「同步失败」是在测降级路径，
// 不是这里要的「五步编排正确」。降级路径另有 TestPipelineRun_NilFetcher*。
func newPipelineFixture(t *testing.T) (*Pipeline, *Store, func()) {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	p := &Pipeline{
		Store: store,
		Fetcher: &Fetcher{syncHook: func(context.Context, string) (int, error) {
			return 0, nil
		}},
		DataDir:            t.TempDir(),
		SpamDryRun:         true, // 安全阀：只判定不移动
		AccountSyncTimeout: time.Second,
	}
	return p, store, cleanup
}

// seedScopedAccount 造一个 (user, workspace) 的启用账户。
func seedScopedAccount(t *testing.T, store *Store, id, user, ws string) {
	t.Helper()
	seedAccount(t, store, id, user, ws)
}

// seedScoredEmail 造一封带分类/重要度的邮件。
func seedScoredEmail(t *testing.T, store *Store, id, accountID, ws, from, subject, category, importance string) {
	t.Helper()
	seedEmail(t, store, id, accountID, ws, subject)
	if _, err := store.pool.Exec(context.Background(), `
		UPDATE emails SET from_address=$1, category=$2, importance=$3,
		       date=extract(epoch from now())::bigint, uid=$4
		WHERE id=$5`, from, category, importance, uidFor(id), id); err != nil {
		t.Fatalf("score email %s: %v", id, err)
	}
}

// uidFor 给测试邮件造一个稳定的正 UID（>0 才会进入部分分支）。
func uidFor(id string) int64 {
	var n int64
	for _, c := range id {
		n = n*31 + int64(c)
	}
	if n < 0 {
		n = -n
	}
	return n%100000 + 1
}

// ---------------------------------------------------------------------------
// Run 主干
// ---------------------------------------------------------------------------

// TestPipelineRun_CompletesAllSteps 走完 5 步（第 4 步需 Harvest，非 nil 时才跑）。
//
// 断言依据是报告里各步留下的可观测计数，而不是「没报错」——
// 0 步和 5 步在 nil 依赖下都可能是「不报错」，只有计数能区分。
func TestPipelineRun_CompletesAllSteps(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-1", "u1", "ws1")
	// 一封重要的普通邮件（走第 3 步提醒判定）。
	seedScoredEmail(t, store, "em-important", "acct-1", "ws1",
		"boss@corp.example", "关于季度预算的确认", "work", "high")
	// 一封已下载的发票（走第 5/6 步汇总）。
	seedDownloadedInvoice(t, store, p.DataDir, "inv-1", "em-important", "acct-1", "u1", "ws1", "2026-10-01", 128.50, "CNY")

	rep := p.Run(ctx)

	// 第 1 步：账户被枚举到。
	if rep.AccountsSynced != 1 {
		t.Errorf("AccountsSynced = %d, want 1", rep.AccountsSynced)
	}
	// 第 2 步：预演计数。boss@corp 不该被判垃圾，但 0 也合法 ——
	// 关键是 SpamMoved 必须为 0（dryRun 下绝不许移动）。
	if rep.SpamMoved != 0 {
		t.Errorf("SpamMoved = %d under SpamDryRun; dry-run must never MOVE", rep.SpamMoved)
	}
	// 第 3 步：Notifier 为 nil 时按设计整体早退，所以 Scanned 必为 0。
	// 这里钉的是「早退而非 panic」——第 3 步的真正判定在下面
	// TestPipelineRun_NotifiesImportantHighOnly 里用注入的 Notifier 单独测。
	if rep.RemindersScanned != 0 {
		t.Errorf("RemindersScanned = %d with a nil Notifier; step 3 must skip cleanly", rep.RemindersScanned)
	}
	if rep.RemindersSent != 0 {
		t.Errorf("RemindersSent = %d with a nil Notifier", rep.RemindersSent)
	}
	// 第 5/6 步：本地汇总文档必须真的落盘（飞书不可用时的兜底）。
	if rep.ShareDocCSV == "" || rep.ShareDocMD == "" {
		t.Errorf("summary docs missing: csv=%q md=%q", rep.ShareDocCSV, rep.ShareDocMD)
	}
	for _, path := range []string{rep.ShareDocCSV, rep.ShareDocMD} {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("summary doc not on disk: %v", err)
		}
	}
	// 收尾字段必须被填上（deferred 块跑了）。
	if rep.FinishedAt == 0 || rep.DurationMs < 0 {
		t.Errorf("report not finalized: finishedAt=%d durationMs=%d", rep.FinishedAt, rep.DurationMs)
	}
	// 预演模式下不允许出现任何「非致命错误」掩盖失败。
	if len(rep.Errors) > 0 {
		t.Errorf("unexpected errors with no optional deps configured: %v", rep.Errors)
	}
}

// TestPipelineRun_PushesOnlyItsOwnScopeInvoices 钉住跨 workspace 隔离。
//
// 第 5 步对每个 (user, workspace) 独立推送。若隔离失效，ws1 的
// 发票会被推到 ws2 的用户那里 —— 这是数据泄漏，不只是计数问题。
func TestPipelineRun_PushesOnlyItsOwnScopeInvoices(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	// 同一个用户，两个 workspace。
	seedScopedAccount(t, store, "acct-w1", "u-shared", "ws-one")
	seedScopedAccount(t, store, "acct-w2", "u-shared", "ws-two")

	seedScoredEmail(t, store, "em-one", "acct-w1", "ws-one",
		"a@vendor.example", "发票已开具", "invoice", "low")
	seedScoredEmail(t, store, "em-two", "acct-w2", "ws-two",
		"b@vendor.example", "发票已开具", "invoice", "low")

	seedDownloadedInvoice(t, store, p.DataDir, "inv-w1", "em-one", "acct-w1", "u-shared", "ws-one", "2026-10-01", 10.00, "CNY")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-w2", "em-two", "acct-w2", "u-shared", "ws-two", "2026-10-01", 20.00, "CNY")

	p.Pusher = &fakePusher{available: true}
	rep := p.Run(ctx)

	pushed := p.Pusher.(*fakePusher).pushed
	if len(pushed) != 2 {
		t.Fatalf("pushed %v, want exactly 2 (one per scope)", pushed)
	}
	// 两次推送必须分别来自各自 scope 的清单。
	got := map[string]bool{}
	for _, id := range pushed {
		got[id] = true
	}
	if !got["inv-w1"] || !got["inv-w2"] {
		t.Errorf("pushed %v; each scope's own invoice must be pushed", pushed)
	}
	if rep.FeishuPushed != 2 {
		t.Errorf("FeishuPushed = %d, want 2", rep.FeishuPushed)
	}
}

// TestPipelineRun_DuplicateScopePushesOnce 同一 (user, workspace) 下多个账户
// **只推一次**。否则挂 5 个邮箱的用户每轮会收到 5 份同样的清单。
func TestPipelineRun_DuplicateScopePushesOnce(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	for _, id := range []string{"acct-a", "acct-b", "acct-c"} {
		seedScopedAccount(t, store, id, "u-dup", "ws-dup")
	}
	seedScoredEmail(t, store, "em-dup", "acct-a", "ws-dup",
		"a@vendor.example", "发票", "invoice", "low")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-dup", "em-dup", "acct-a", "u-dup", "ws-dup", "2026-10-01", 5.00, "CNY")

	p.Pusher = &fakePusher{available: true}
	p.Run(ctx)

	if n := len(p.Pusher.(*fakePusher).pushed); n != 1 {
		t.Fatalf("pushed %d times for 3 accounts in one scope, want 1", n)
	}
}

// TestPipelineRun_PusherFailureIsCountedNotFatal 飞书不可用时流水线必须跑完
// ——需求 3 明说「发不出去就建共享文档+列表+金额汇总」。
// 断言重点：FeishuFailed > 0 **且** 汇总文档仍然生成。
func TestPipelineRun_PusherFailureIsCountedNotFatal(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-f", "u-fail", "ws-fail")
	seedScoredEmail(t, store, "em-f", "acct-f", "ws-fail",
		"a@vendor.example", "发票", "invoice", "low")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-f", "em-f", "acct-f", "u-fail", "ws-fail", "2026-10-01", 9.99, "CNY")

	p.Pusher = &fakePusher{available: true, err: context.DeadlineExceeded}
	rep := p.Run(ctx)

	if rep.FeishuFailed == 0 {
		t.Error("FeishuFailed = 0 despite the pusher failing every call")
	}
	if rep.ShareDocCSV == "" {
		t.Error("no CSV fallback after feishu failure; requirement 3 demands the offline list")
	}
}

// TestPipelineRun_NotifierFailureIsNonFatal 提醒失败不阻断后续步骤。
func TestPipelineRun_NotifierFailureIsNonFatal(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-n", "u-notif", "ws-notif")
	seedScoredEmail(t, store, "em-n", "acct-n", "ws-notif",
		"boss@corp.example", "重要", "work", "high")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-n", "em-n", "acct-n", "u-notif", "ws-notif", "2026-10-01", 1.00, "CNY")

	p.Notifier = &fakeNotifier{err: context.Canceled}
	rep := p.Run(ctx)

	if rep.ShareDocCSV == "" {
		t.Error("a notifier failure stopped the pipeline before step 5/6")
	}
}

// ---------------------------------------------------------------------------
// 降级
// ---------------------------------------------------------------------------

// TestPipelineRun_NoAccountsIsANoOp 没有启用账户时不该产生任何副作用。
func TestPipelineRun_NoAccountsIsANoOp(t *testing.T) {
	p, _, cleanup := newPipelineFixture(t)
	defer cleanup()

	rep := p.Run(context.Background())
	if rep.AccountsSynced != 0 || rep.NewEmails != 0 {
		t.Errorf("empty workspace produced activity: %+v", rep)
	}
	if len(rep.ShareDocCSV) != 0 {
		t.Errorf("CSV generated with no accounts: %q", rep.ShareDocCSV)
	}
}

// TestPipelineRun_DisabledAccountIsSkipped enabled=false 的账户不参与。
func TestPipelineRun_DisabledAccountIsSkipped(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-off", "u-off2", "ws-off2")
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET enabled=FALSE WHERE id=$1`, "acct-off"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	rep := p.Run(ctx)
	if rep.AccountsSynced != 0 {
		t.Errorf("AccountsSynced = %d, want 0 (account is disabled)", rep.AccountsSynced)
	}
}

// ---------------------------------------------------------------------------
// 安全阀
// ---------------------------------------------------------------------------

// TestPipelineRun_DryRunNeverMoves 负控式的安全断言：预演模式下
// SpamMoved 恒为 0，判定结果只进 SpamDryRun/SpamNearMiss。
//
// 这条护的是**不可逆操作**（真实邮箱 IMAP MOVE）。真机上第一次跑必须
// 只看到预演报告。
func TestPipelineRun_DryRunNeverMoves(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-spam", "u-spam", "ws-spam")
	// 一封典型的营销邮件。
	seedScoredEmail(t, store, "em-spam", "acct-spam", "ws-spam",
		"deals@newsletter.example", "【限时】全场五折 今日最后一天", "promotion", "low")

	rep := p.Run(ctx)

	if rep.SpamMoved != 0 {
		t.Fatalf("SpamMoved = %d under dry-run — a real IMAP MOVE may have been issued", rep.SpamMoved)
	}
	if rep.SpamLocalOnly != 0 {
		t.Errorf("SpamLocalOnly = %d under dry-run; the local marker must not be written either", rep.SpamLocalOnly)
	}
	// 预演计数只统计「会移多少」，具体样本在 SpamDryRunSamples。
	if rep.SpamDryRun == 0 && len(rep.SpamDryRunSamples) == 0 {
		t.Log("no spam hit and no near-miss; the marketing rule may not fire on this fixture")
	}
}

// TestPipelineRun_SummaryDocsContainAmount 汇总文档必须带金额 ——
// 需求 3 要求「记录必要信息并汇总金额」。
//
// 这里刻意不校验金额格式，只校验**文件非空且含数字**，避免把测试
// 绑死在具体 CSV 排版上。
func TestPipelineRun_SummaryDocsContainAmount(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-sum", "u-sum", "ws-sum")
	seedScoredEmail(t, store, "em-sum", "acct-sum", "ws-sum",
		"vendor@shop.example", "电子发票", "invoice", "low")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-sum", "em-sum", "acct-sum", "u-sum", "ws-sum", "2026-10-01", 1234.56, "CNY")

	rep := p.Run(ctx)

	for name, path := range map[string]string{"csv": rep.ShareDocCSV, "md": rep.ShareDocMD} {
		if path == "" {
			t.Fatalf("%s path empty", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty on disk", name)
		}
		text := string(data)
		if !strings.Contains(text, "1234.56") {
			t.Errorf("%s does not contain the invoice amount; got:\n%s", name, truncate(text, 400))
		}
	}
}

// TestPipelineRun_WritesIntoDataDir 汇总文档必须落在配置的 DataDir 下，
// 不能写到别处（用户会找不到）。
func TestPipelineRun_WritesIntoDataDir(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-dir", "u-dir", "ws-dir")
	seedScoredEmail(t, store, "em-dir", "acct-dir", "ws-dir",
		"vendor@shop.example", "电子发票", "invoice", "low")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-dir", "em-dir", "acct-dir", "u-dir", "ws-dir", "2026-10-01", 7.00, "CNY")

	rep := p.Run(ctx)

	abs, err := filepath.Abs(p.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{rep.ShareDocCSV, rep.ShareDocMD} {
		if path == "" {
			continue
		}
		if !strings.HasPrefix(path, abs) {
			t.Errorf("summary doc %q is outside DataDir %q", path, abs)
		}
	}
}

// seedDownloadedInvoice 造一条 status='downloaded' 的发票，并**真的在
// dataDir 下写一个文件**。
//
// 为什么必须落文件：pushInvoiceSet 用 `filepath.Join(p.DataDir, inv.FilePath)`
// 拼绝对路径，汇总文档也要读它。只插库不落文件会让「推送」和「汇总」两处
// 静默跳过，用例变成空转却仍然全绿。
//
// dataDir 由调用方传（不能从 Store 反推——Store 不知道 DataDir）。
func seedDownloadedInvoice(t *testing.T, store *Store, dataDir string, id, emailID, accountID, userID, ws, date string, amount float64, currency string) {
	t.Helper()
	ctx := context.Background()

	name := id + ".pdf"
	rel := "invoices/" + name
	abs := filepath.Join(dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", id, err)
	}
	if err := os.WriteFile(abs, []byte("%PDF-1.4\n% test fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture pdf for %s: %v", id, err)
	}

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            amount, currency, kind, category, seller, title,
		                            file_name, file_path, file_source,
		                            created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'downloaded',1,
		        $9,$10,'e-invoice','办公','测试供应商','测试抬头',
		        $11,$12,'attachment',1700000000,1700000000)
		ON CONFLICT (id) DO NOTHING`,
		id, emailID, accountID, userID, ws, "INV-"+id, date, "发票已开具", amount, currency, name, rel); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

// ---------------------------------------------------------------------------
// 降级：缺依赖不得 panic / 不得中断
// ---------------------------------------------------------------------------

// TestPipelineRun_NilFetcherDegradesGracefully 是 fetcher.go 里 `if f == nil`
// 那道守卫的守卫用例。
//
// 这条护的是**进程级**故障：syncAccounts 把 p.Fetcher.Sync 放在**独立
// goroutine** 里调，goroutine 内 panic 会直接崩掉整个进程，主流程的
// defer recover 拦不住。修复前它就是 `panic: nil pointer dereference`。
//
// 场景不是纯理论：需求 6 走设备本地执行时，客户端推送路径本来就不建 Fetcher。
func TestPipelineRun_NilFetcherDegradesGracefully(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	p.Fetcher = nil // 模拟「Fetcher 还没就绪」
	seedScopedAccount(t, store, "acct-nofetch", "u-nofetch", "ws-nofetch")
	seedScoredEmail(t, store, "em-nofetch", "acct-nofetch", "ws-nofetch",
		"vendor@shop.example", "电子发票", "invoice", "low")
	seedDownloadedInvoice(t, store, p.DataDir, "inv-nofetch", "em-nofetch",
		"acct-nofetch", "u-nofetch", "ws-nofetch", "2026-10-01", 3.00, "CNY")

	// 关键：能跑完、且第 5/6 步仍然交付。
	rep := p.Run(ctx)

	if rep.AccountsSynced != 0 {
		t.Errorf("AccountsSynced = %d, want 0 (no fetcher means nothing was synced)", rep.AccountsSynced)
	}
	if rep.ShareDocCSV == "" {
		t.Error("a missing Fetcher stopped the pipeline before the summary docs step")
	}
	// 同步失败应记进 errors —— 静默吞掉会让「0 封同步」看起来像「没有新邮件」。
	if len(rep.Errors) == 0 {
		t.Error("missing Fetcher produced no error entry; a silent 0 is indistinguishable from 'no new mail'")
	}
}

// TestFetcherSyncNilReceiverDoesNotPanic 直接钉住 Sync 的 nil 守卫。
//
// 单独一条而不是只依赖上面的集成用例：集成用例的失败现场是
// 「整个测试进程 panic」，看不出是哪一行；而这里能精确定位。
func TestFetcherSyncNilReceiverDoesNotPanic(t *testing.T) {
	var f *Fetcher // 故意为 nil
	if _, err := f.Sync(context.Background(), "acct-x"); err == nil {
		t.Fatal("Sync on a nil Fetcher returned nil error; it must report 'not configured'")
	}
}

// TestPipelineRun_NoHarvestSkipsStep4 未注入 Harvest 时第 4 步必须跳过
// 而不是 panic（Harvest 内部要 DataDir + Fetcher，条件更苛刻）。
func TestPipelineRun_NoHarvestSkipsStep4(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	p.Harvest = nil
	p.Notifier = &fakeNotifier{} // 让第 3 步真的跑，才能观察到它
	seedScopedAccount(t, store, "acct-nh", "u-nh", "ws-nh")
	seedScoredEmail(t, store, "em-nh", "acct-nh", "ws-nh",
		"boss@corp.example", "重要", "work", "high")

	rep := p.Run(ctx)
	if rep.RemindersScanned == 0 {
		t.Error("step 3 did not run")
	}
	if rep.Invoices.Processed != 0 {
		t.Errorf("step 4 processed %d invoices with a nil Harvest; it must skip", rep.Invoices.Processed)
	}
	if rep.ShareDocMD == "" {
		t.Error("step 5/6 did not run")
	}
}

// ---------------------------------------------------------------------------
// 第 3 步：重要邮件提醒（需求 4 的核心判定）
// ---------------------------------------------------------------------------

// TestPipelineRun_NotifiesOnlyHighImportance 需求 4 的主判定。
//
// 只有 importance='high' 才提醒。这条同时钉住三件事：
//   1. high 被提醒、medium/low 不被提醒（不是「全提醒」也不是「都不提醒」）
//   2. 提醒过的邮件被标记，**下一轮不再重复提醒**（否则每轮轰炸用户）
//   3. RemindersUnclassified 统计 importance 为空的邮件 —— 这个计数是
//      需求 4 能否排查的关键：kxmemory 没配时它会告诉你「这批邮件根本没
//      被分类过」，而不是让你对着恒为 0 的 RemindersSent 猜（见
//      PipelineReport 字段注释里记录的那次踩坑）。
func TestPipelineRun_NotifiesOnlyHighImportance(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-rem", "u-rem", "ws-rem")
	// 三封 importance 各异。
	seedScoredEmail(t, store, "em-hi", "acct-rem", "ws-rem",
		"boss@corp.example", "季度预算已确认", "work", "high")
	seedScoredEmail(t, store, "em-mid", "acct-rem", "ws-rem",
		"team@corp.example", "周会纪要", "work", "medium")
	seedScoredEmail(t, store, "em-lo", "acct-rem", "ws-rem",
		"news@corp.example", "订阅更新", "newsletter", "low")
	// 一封**未经 AI 分类**的：importance 为空。
	seedEmail(t, store, "em-unc", "acct-rem", "ws-rem", "未知来源")
	if _, err := store.pool.Exec(ctx, `
		UPDATE emails SET from_address='unknown@x.example', importance='', category='',
		       date=extract(epoch from now())::bigint, uid=$1
		WHERE id='em-unc'`, uidFor("em-unc")); err != nil {
		t.Fatalf("mark unclassified: %v", err)
	}

	n := &fakeNotifier{}
	p.Notifier = n

	rep := p.Run(ctx)

	if len(n.notified) != 1 || n.notified[0] != "em-hi" {
		t.Fatalf("notified %v, want exactly [em-hi] (only importance=high)", n.notified)
	}
	if rep.RemindersSent != 1 {
		t.Errorf("RemindersSent = %d, want 1", rep.RemindersSent)
	}
	if rep.RemindersScanned != 4 {
		t.Errorf("RemindersScanned = %d, want 4", rep.RemindersScanned)
	}
	if rep.RemindersUnclassified != 1 {
		t.Errorf("RemindersUnclassified = %d, want 1 (the email with empty importance)", rep.RemindersUnclassified)
	}

	// 第二轮：已提醒过的那封不得重复提醒。
	n.notified = nil
	rep2 := p.Run(ctx)
	if len(n.notified) != 0 {
		t.Errorf("second round re-notified %v; already-notified mail must not be repeated", n.notified)
	}
	if rep2.RemindersSent != 0 {
		t.Errorf("second round RemindersSent = %d, want 0", rep2.RemindersSent)
	}
	// 但未分类计数依然要报 —— 那是「为什么 RemindersSent 是 0」的解释。
	if rep2.RemindersUnclassified != 1 {
		t.Errorf("second round RemindersUnclassified = %d, want 1", rep2.RemindersUnclassified)
	}
}

// TestPipelineRun_NotificationFailureDoesNotMarkAsNotified 提醒失败时
// **不能**标记为已通知，否则这封重要邮件会被永久漏掉 —— 失败必须可重试。
func TestPipelineRun_NotificationFailureDoesNotMarkAsNotified(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedScopedAccount(t, store, "acct-nfail", "u-nfail", "ws-nfail")
	seedScoredEmail(t, store, "em-nfail", "acct-nfail", "ws-nfail",
		"boss@corp.example", "重要", "work", "high")

	failing := &fakeNotifier{err: context.DeadlineExceeded}
	p.Notifier = failing
	rep := p.Run(ctx)

	if rep.RemindersSent != 0 {
		t.Errorf("RemindersSent = %d despite every notify failing", rep.RemindersSent)
	}
	var notified int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM emails WHERE id='em-nfail' AND notified_at > 0`).Scan(&notified); err != nil {
		t.Fatalf("query notified: %v", err)
	}
	if notified != 0 {
		t.Error("a failed reminder was marked as notified; that email would never be retried")
	}
}

// 合并说明：本文件原来自己有一个固定 400 字节的 `truncate(s string)`，
// 而 invoice_sources_e2e_test.go 有一个通用的 `truncate(s string, n int)`。
// 同包同名两个定义无法编译（redeclared），取通用的那个，本文件的调用点补上 400。
