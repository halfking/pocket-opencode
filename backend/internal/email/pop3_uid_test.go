package email

// pop3_uid_test.go — POP3 降级路径的 UID 不是 IMAP UID，采集器必须拒绝使用。
//
// 背景（2026-10-01 真实数据）：QQ 账户 444 封邮件里 284 封走的是 POP3 降级
// 路径（`syncPOP3Fallback`），而它把 `UID` 写成**位置序号**（第几封），
// 不是 IMAP UID。`harvestOne` 却无条件拿 `em.UID` 去 `UID FETCH`，于是：
//
//  1. **可能下载到完全错误的文件**——IMAP uid=264 是另一封毫不相干的邮件，
//     解析后会被存成这封发票的 PDF。
//  2. 解释了第 4 步那 13 分钟的卡死：uid=134/135 恰好是大邮件时，那次
//     BODY[] literal 读取要把整轮采集拖到分钟级。
//
// 这里钉死两件事：识别得出 POP3 来源、且采集器宁可失败也绝不去 FETCH。
// 「宁可失败」是重点——静默下载错文件比采集成败糟得多。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPOP3SourcedEmail(t *testing.T) {
	if !isPOP3SourcedEmail(Email{ID: "em-pop3-acct-1-ABC"}) {
		t.Fatal("em-pop3- 前缀必须被识别为 POP3 来源")
	}
	if isPOP3SourcedEmail(Email{ID: "em-10436-acct-1"}) {
		t.Fatal("IMAP 落库的 id 不该被误判成 POP3 来源")
	}
}

// TestParseMIMEMessage_ExposesRealMessageID 钉住 POP3 落库改用真实 Message-ID
// 的前提：解析器必须能取出这个头。取不到的话 POP3 路径只能继续用 UIDL 合成，
// 于是同一封邮件走 IMAP/POP3 两条路径各落一条（UNIQUE(account_id,message_id)
// 拦不住，实测 47 组重复副本）。
func TestParseMIMEMessage_ExposesRealMessageID(t *testing.T) {
	raw := []byte("From: a@b.com\r\n" +
		"To: c@d.com\r\n" +
		"Subject: hi\r\n" +
		"Message-ID: <abc.123@example.com>\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"body\r\n")
	p, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.MessageID != "abc.123@example.com" {
		t.Fatalf("MessageID=%q, want %q（尖括号应被去掉）", p.MessageID, "abc.123@example.com")
	}
}

func TestParseMIMEMessage_MessageIDAbsentIsEmpty(t *testing.T) {
	raw := []byte("From: a@b.com\r\nSubject: hi\r\nContent-Type: text/plain\r\n\r\nbody\r\n")
	p, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.MessageID != "" {
		t.Fatalf("MessageID=%q, want empty（没有该头时必须回退到 UIDL 合成）", p.MessageID)
	}
}

// TestHarvestOne_RefusesPOP3PositionalUID 是本文件的核心安全性质。
//
// 判据不是「返回了 failed」这么笼统，而是 **Fetcher 是 nil 而采集仍然平安返回**
// —— 若实现退化成去调 FetchMessageRaw，这个 nil 一定会被解引用并 panic。
// 这是直接判据，不用代理指标。
//
// （判据原先用的是 `Attempts == 0`，因为 Attempts++ 紧挨着 FetchMessageRaw。
// 2026-10-01 修「缓存命中路径不计数」时把 Attempts++ 提到了取原文之前统一计数，
// 代理指标随之失效 —— 意图不变，换成直接判据。）
func TestHarvestOne_RefusesPOP3PositionalUID(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-pop3-1", "user-1", "ws-1")

	// 一封 POP3 降级路径落库的邮件：UID 是位置序号 264，不是 IMAP UID。
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-acct-pop3-1-ABC", AccountID: "acct-pop3-1", WorkspaceID: "ws-1",
		MessageID: "real-msg-id@example.com", UID: 264,
		FromAddress: "noreply@vendor.com", Subject: "增值税电子发票",
		Date: 1750000000,
	}); err != nil {
		t.Fatalf("insert pop3 email: %v", err)
	}

	em, err := store.GetEmailByID(ctx, "em-pop3-acct-pop3-1-ABC")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	if !isPOP3SourcedEmail(*em) {
		t.Fatalf("用例前提不成立：%q 应被识别为 POP3 来源", em.ID)
	}

	// Fetcher 刻意留 nil：任何真的去 FetchMessageRaw 的路径都会 panic。
	// 采集平安返回即证明没有拿合成 UID 去 FETCH。
	h := &InvoiceHarvester{Store: store, Fetcher: nil, DataDir: t.TempDir()}
	inv := &Invoice{ID: "inv-pop3-1", EmailID: em.ID, Status: "pending"}

	got := h.harvestOne(ctx, inv)
	if got != "failed" {
		t.Fatalf("harvestOne = %q, want \"failed\"（合成 UID 绝不能拿去 IMAP FETCH）", got)
	}
	if inv.Status != "failed" {
		t.Fatalf("Status=%q, want \"failed\"", inv.Status)
	}
	if !strings.Contains(inv.LastError, "POP3") {
		t.Fatalf("LastError=%q, want 包含 POP3 的明确原因（运维要能看懂为什么失败）", inv.LastError)
	}
}

// TestHarvestOne_POP3UsesRawCacheInsteadOfIMAPFetch 是 BUG-AV 的**功能侧**验证：
// 缓存命中时，POP3 来源的发票必须能真正被采集（而不是一律 failed）。
//
// 关键在于 Fetcher 刻意留空——如果实现退化成去调 FetchMessageRaw，
// 这里会因为 Fetcher 无效配置而失败；同时 Fetcher.BodyCache 也没配，
// 任何「偷偷走 IMAP」的路径都走不通。
func TestHarvestOne_POP3UsesRawCacheInsteadOfIMAPFetch(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-pop3-2", "user-1", "ws-1")

	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-acct-pop3-2-XYZ", AccountID: "acct-pop3-2", WorkspaceID: "ws-1",
		MessageID: "real-2@example.com", UID: 12,
		FromAddress: "noreply@vendor.com", Subject: "增值税电子发票",
		Date: 1750000000, HasAttachments: true,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	cache := newTestBodyCache(t)
	// 缓存里放一封带 PDF 附件的原文：采集器应当直接用它，不再碰 IMAP。
	// 结构照抄 invoice_harvest_test.go 里已验证能解析的形态（要 Content-Disposition，
	// 且附件内容用 7-bit 直传而不是 base64——自己手拼 base64 附件解析不出附件，
	// 那种失败会掩盖「缓存到底走没走通」这个真正要验的点）。
	raw := []byte("From: =?utf-8?B?5rwG6ZW/?= <noreply@vendor.cn>\r\n" +
		"Subject: =?utf-8?B?" + b64("电子发票") + "?=\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"发票金额 3500.00\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		// 附件正文也必须是**真有页的** PDF（原因同 XMLRenderer 那处）：
		// 采集器现在会在落盘前用 pdfcpu 确认「至少有 1 页」。原来的
		// "%PDF-1.4 fake pdf body" 只有 magic 没有页树，会被拒收，
		// 于是本用例测的「POP3 走原文缓存」被夹具自身的退化件盖住。
		b64(e2eInvoicePDF) + "\r\n" +
		"--BOUND--\r\n")
	if _, err := cache.Put("em-pop3-acct-pop3-2-XYZ", 12, raw); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	dataDir := t.TempDir()
	h := &InvoiceHarvester{
		Store: store,
		// Fetcher 故意留空 store/crypto：任何走 IMAP 的尝试都会立刻失败，
		// 于是「用缓存采集成功」这件事只能由缓存路径完成。
		Fetcher:   &Fetcher{},
		DataDir:   dataDir,
		BodyCache: cache,
		// 必须返回**真有页的** PDF：采集器现在会用 pdfcpu 确认「至少有 1 页」
		// 才肯落盘（见 invoice_stub_pdf_rejected_test.go）。这里原来返回的是
		// "%PDF-1.4 fake rendered pdf"——只有 magic 没有页树，正是本次要拒的
		// 那种退化件，于是本用例被自己的夹具绊倒（LastError:
		// "unusable pdf: ... no header version available"）。
		// 本用例的主题是「POP3 来源走原文缓存而不是 IMAP FETCH」，渲染器的
		// 字节内容本不该决定成败；换成 e2eInvoicePDF 让夹具不再自相矛盾。
		XMLRenderer: func(name string, inv *Invoice, xmlRaw []byte) ([]byte, error) {
			return []byte(e2eInvoicePDF), nil
		},
	}
	// 先把发票行建出来：saveInvoiceFile 最后要 UpdateInvoiceHarvest，
	// 行不存在时 UPDATE 影响 0 行 → 返回 failed（且 LastError 已被清空，
	// 排查起来很误导）。这不是被测行为，是测试前提。
	seedInv := &Invoice{
		EmailID: "em-pop3-acct-pop3-2-XYZ", AccountID: "acct-pop3-2",
		Seller: "杭州创客家投资管理有限公司", Amount: 3500, InvoiceDate: "2026-05-01",
	}
	stored, err := store.UpsertInvoice(ctx, seedInv, "user-1", "ws-1")
	if err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	inv := &Invoice{
		ID: stored.ID, EmailID: stored.EmailID, AccountID: stored.AccountID,
		WorkspaceID: stored.WorkspaceID, Status: "pending",
		Seller: "杭州创客家投资管理有限公司", Amount: 3500, InvoiceDate: "2026-05-01",
	}

	got := h.harvestOne(ctx, inv)
	if got != "downloaded" {
		t.Fatalf("harvestOne = %q, want \"downloaded\"（POP3 来源应走原文缓存）。LastError=%s", got, inv.LastError)
	}
	if inv.FilePath == "" {
		t.Fatal("downloaded 但没有落盘路径")
	}
	// FilePath 是相对 DataDir 的（存进 emails/invoices 表的就是相对路径）。
	abs := filepath.Join(dataDir, inv.FilePath)
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("发票文件未落盘: %v", err)
	}
	// 顺带钉住需求里的规范文件名：{费用类型}-{对方单位}-{金额}-{日期}.pdf
	if !strings.HasSuffix(inv.FileName, "其他-杭州创客家投资管理有限公司-3500.00-2026-05-01.pdf") {
		t.Fatalf("FileName=%q 不符合 {费用类型}-{对方单位}-{金额}-{日期}.pdf", inv.FileName)
	}
	t.Cleanup(func() { _ = os.Remove(abs) })
}
