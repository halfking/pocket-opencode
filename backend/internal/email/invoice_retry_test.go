package email

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
import (
	"context"
	"strings"
	"testing"
)

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
//
// 2026-10-02 更正：原来这个用例的邮件 ID 是 `em-cached-acct-cached`，
// **不带 `em-pop3-` 前缀**，于是 isPOP3SourcedEmail 为 false，BodyCache 根本
// 没被查——它走的是 else 分支的 Fetcher.FetchMessageRaw，在空 &Fetcher{} 上
// 立刻失败。也就是说：用例名字钉的是「缓存命中路径」，实际测的是
// 「fetcher 失败路径」。因为期望本来就是失败，两条路径都能让它变绿，
// 假绿就这么混过去了。
//
// 真实的两张 QQ Wallet 发票正是 POP3 来源，缓存命中才是生产里真正要走的那条
// 路，所以这里把 ID 改成 em-pop3- 前缀，并**断言 last_error 证明确实走了缓存**
// （缓存命中会一路解析到「邮件里没有可用 pdf/xml」；若哪天又退回 fetcher 分支，
// last_error 会变成 "fetch raw: ..."，用例立刻转红）。
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
	// ID 必须带 em-pop3- 前缀，否则不会走 BodyCache 分支（见函数注释）。
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-cached-acct-cached", AccountID: "acct-cached", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "发票通知",
		Snippet: body, Date: 1750000000, UID: 42,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-cached-acct-cached")
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
	// 必须真的建档：harvestOne 每轮结尾都调 UpdateInvoiceHarvest 落库，
	// 行不存在时它只会打一行 "update invoice: email: not found" 然后返回错误。
	// 用例原本只读内存里的 inv，于是这条落库失败完全不影响结果——
	// 声称在验「收敛到终态」，实际一次都没写进库。
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
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
		// 防假绿：必须真的走了 BodyCache 分支。走了缓存才会一路解析 MIME 到
		// 「邮件里没有可用 pdf/xml」；若退回 Fetcher 分支，last_error 会是
		// "fetch raw: ..."，说明这个用例已经名不副实。
		if strings.HasPrefix(inv.LastError, "fetch raw:") {
			t.Fatalf("第 %d 轮 LastError=%q —— 取原文走的是 Fetcher 而不是 BodyCache，"+
				"本用例已经不再测试它名字所声称的「缓存命中路径」", round, inv.LastError)
		}
	}
	if inv.Status != "failed" {
		t.Fatalf("第 %d 轮后 Status=%q, want \"failed\"（重试耗尽应收敛到终态）", MaxInvoiceAttempts, inv.Status)
	}
	// 终态必须真的落库，而不只是内存里改了变量。
	dbInv, err := store.GetInvoiceByEmailID(ctx, em.ID)
	if err != nil {
		t.Fatalf("读回落库结果: %v", err)
	}
	if dbInv.Status != "failed" {
		t.Fatalf("落库 status=%q, want \"failed\" —— 采集器每轮都调 UpdateInvoiceHarvest，"+
			"不落库的话下一轮又会从库里把这条 pending 捞出来重试", dbInv.Status)
	}
	if dbInv.Attempts != MaxInvoiceAttempts {
		t.Fatalf("落库 attempts=%d, want %d", dbInv.Attempts, MaxInvoiceAttempts)
	}
}
