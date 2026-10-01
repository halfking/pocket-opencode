package email

// invoice_dedup_test.go — 发票去重的真实边界。
//
// ## 为什么要钉这个
//
// 需求 2 要「清理广告与垃圾邮件」+ 需求 3 要「汇总金额」。汇总的前提是
// **同一张发票只算一次**。仓库里对此有两个机制，此前都没有测试：
//
//  1. `email_invoices.email_id` 是 UNIQUE（真实库实测确认，见下），
//     且 upsert 用 `ON CONFLICT (email_id) DO UPDATE`
//     ⇒ **同一封邮件**重复建档是幂等的；
//  2. `invoice_no` **没有任何唯一约束**（真实库 pg_indexes 实测：只有
//     pkey(id) / email_id_key / idx_ws / idx_status 四条索引）
//     ⇒ **不同邮件**携带同一张发票时，会各记一行。
//
// 第 2 条不是「缺陷」还是「设计」，取决于需求怎么定。我不替用户决定，
// 但**必须把它变成有证据的事实**而不是待查项 —— 本文件就是那份证据。
//
// 真实库当前 `email_invoices` 是 0 行，所以这在真实数据上尚未发生。

import (
	"context"
	"testing"
)

// seedInvoiceFor 造一条指定发票号/金额的发票，挂到给定邮件上。
func seedInvoiceFor(t *testing.T, store *Store, id, emailID, accountID, userID, ws, invoiceNo string, amount float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            amount, currency, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'2026-10-01','invoice','downloaded',1,$7,'CNY',1700000000,1700000000)
		ON CONFLICT (id) DO NOTHING`,
		id, emailID, accountID, userID, ws, invoiceNo, amount); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

// TestUpsertInvoice_SameEmailIsIdempotent 同一封邮件重复建档 → 只一行。
//
// 这是 `ON CONFLICT (email_id) DO UPDATE` 保证的。断言的是**行数**，
// 因为「第二次调用返回了一个不同的 ID」正是曾经踩过的坑：
// 复用旧行 ID 是对的（新 ID 会让旧行变孤儿，见 scheduler.go:848 的注释）。
func TestUpsertInvoice_SameEmailIsIdempotent(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-dedup", "u-dedup", "ws-dedup")
	seedEmail(t, store, "em-dedup", "acct-dedup", "ws-dedup", "发票邮件")

	first, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-dedup", AccountID: "acct-dedup",
		Kind: "e-invoice", Category: "办公", Title: "测试",
		Seller: "供应商", Amount: 100, Currency: "CNY", InvoiceNo: "INV-1",
	}, "u-dedup", "ws-dedup")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-dedup", AccountID: "acct-dedup",
		Kind: "e-invoice", Category: "办公", Title: "测试",
		Seller: "供应商", Amount: 100, Currency: "CNY", InvoiceNo: "INV-1",
	}, "u-dedup", "ws-dedup")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// 关键：两次必须落同一行（ID 复用），否则旧行变孤儿。
	if first.ID != second.ID {
		t.Errorf("second upsert created a new row (id %s -> %s); the old row becomes an orphan",
			first.ID, second.ID)
	}

	var n int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_invoices WHERE email_id='em-dedup'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d rows for one email, want exactly 1", n)
	}
}

// TestUpsertInvoice_SecondPassDoesNotWipeKnownFields 空值不得覆盖已有值。
//
// `ON CONFLICT ... DO UPDATE` 里有 4 个 CASE 表达式专门做这件事：第二轮
// 提取往往只从 envelope 拿到部分字段（金额/日期在正文里），若直接覆盖，
// 第一轮辛苦解析出的发票号与日期就被抹成空串。
func TestUpsertInvoice_SecondPassDoesNotWipeKnownFields(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-keep", "u-keep", "ws-keep")
	seedEmail(t, store, "em-keep", "acct-keep", "ws-keep", "发票邮件")

	// 第一轮：信息完整。
	if _, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-keep", AccountID: "acct-keep", Kind: "e-invoice",
		Category: "办公", Title: "抬头", Seller: "供应商",
		Amount: 250, Currency: "CNY", InvoiceNo: "INV-KEEP", InvoiceDate: "2026-09-15",
	}, "u-keep", "ws-keep"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// 第二轮：只剩 envelope 信息，金额/发票号/日期/对方单位都是空的。
	if _, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-keep", AccountID: "acct-keep", Kind: "e-invoice",
		Category: "办公", // 标题/销售方/金额/币种/发票号/日期全空
	}, "u-keep", "ws-keep"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := store.GetInvoiceByEmailID(ctx, "em-keep")
	if err != nil || got == nil {
		t.Fatalf("invoice not found: %v (err=%v)", got, err)
	}
	if got.InvoiceNo != "INV-KEEP" {
		t.Errorf("InvoiceNo = %q, want INV-KEEP; the second pass wiped it", got.InvoiceNo)
	}
	if got.InvoiceDate != "2026-09-15" {
		t.Errorf("InvoiceDate = %q, want 2026-09-15; the second pass wiped it", got.InvoiceDate)
	}
	if got.Seller != "供应商" {
		t.Errorf("Seller = %q, want 供应商; the second pass wiped it", got.Seller)
	}
	if got.Title != "抬头" {
		t.Errorf("Title = %q, want 抬头; the second pass wiped it", got.Title)
	}
	if got.Amount != 250 {
		t.Errorf("Amount = %v, want 250; the second pass wiped it", got.Amount)
	}
}

// TestInvoiceNoHasNoUniqueConstraintAcrossEmails 记录一个**已证实的限制**：
// 同一张发票（同一 invoice_no）出现在**两封不同邮件**里时，会记两行、
// 且金额被汇总两次。
//
// 这条不是断言「应该不重复」——那需要先有产品决策。它断言的是
// **当前真实行为**，好让「要不要加唯一约束」这个决策建立在事实之上。
// 若将来决定加约束，这条会红，那时它就变成了需求的守卫。
func TestInvoiceNoHasNoUniqueConstraintAcrossEmails(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-same", "u-same", "ws-same")
	// 两封不同邮件（常见：供应商先发确认函、后发正式发票）
	seedEmail(t, store, "em-same-1", "acct-same", "ws-same", "发票已开具")
	seedEmail(t, store, "em-same-2", "acct-same", "ws-same", "发票重发")

	if _, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-same-1", AccountID: "acct-same", Kind: "e-invoice",
		Category: "办公", Seller: "供应商", Amount: 500, Currency: "CNY",
		InvoiceNo: "INV-DUP", InvoiceDate: "2026-10-01",
	}, "u-same", "ws-same"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-same-2", AccountID: "acct-same", Kind: "e-invoice",
		Category: "办公", Seller: "供应商", Amount: 500, Currency: "CNY",
		InvoiceNo: "INV-DUP", InvoiceDate: "2026-10-01",
	}, "u-same", "ws-same"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var rows int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_invoices WHERE invoice_no='INV-DUP'`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("got %d rows for invoice_no=INV-DUP, want 2", rows)
	}

	// 汇总会把 500 算两遍 —— 这正是需求 3「汇总金额」会多算的地方。
	page, err := store.ListInvoicesPage(ctx, "u-same", "ws-same", "downloaded", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("total = %d, want 2", page.Total)
	}
	if page.Amount != 1000 {
		t.Errorf("Amount = %v, want 1000 (the same invoice counted twice)", page.Amount)
	}
	t.Logf("已证实：同一发票号跨两封邮件 -> 2 行、合计 %v（应为 500）", page.Amount)
}
