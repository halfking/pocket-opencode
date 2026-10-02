package email

// invoice_currency_persist_test.go — 币种必须真的落库并读得回来。
//
// f09460d 给 XML 路径补了币种解析，但那只到内存为止。这里钉的是链路末端：
// UpdateInvoiceHarvest 把采集结果写回 email_invoices 时，**原先根本没写
// currency 列**——invoiceSelectCols 读得到 currency，可 UPDATE 不写它，
// 于是 mergeXMLFields 辛苦解析出来的 USD 落库后就没了，下一轮读回来仍是
// 空串，账本又按 CNY 计。
//
// 「读得到但不写」是最容易漏的一类断点：SELECT * 看起来字段齐全，
// 写回路径却少一列，单测若只断言内存里的 inv.Currency 就完全发现不了。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip）。
//
// 负控：把 UPDATE 里的 currency 赋值删掉 -> 本文件转红。

import (
	"context"
	"testing"
)

func seedInvoiceForCurrency(t *testing.T, store *Store, id string) {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_accounts WHERE id='acct-cur'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n == 0 {
		seedAccount(t, store, "acct-cur", "u", "ws-cur")
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1,'acct-cur','ws-cur',$1,'s@example.com','subject','snippet',1700000000,1700000000)`,
		"em-"+id); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            created_at, updated_at)
		VALUES ($1,$2,'acct-cur','u','ws-cur','','','AWS 账单','pending',0,1700000000,1700000000)`,
		id, "em-"+id); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
}

// XML 解析出的 USD 必须落库。
func TestUpdateInvoiceHarvest_PersistsCurrency(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-cur-usd")

	inv := &Invoice{
		ID: "inv-cur-usd", Status: "downloaded", Attempts: 1,
		InvoiceNo: "AWS-001", InvoiceDate: "2026-09-15",
		Seller: "Amazon Web Services", Amount: 100.00, Currency: "USD",
	}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	var cur string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(currency,'') FROM email_invoices WHERE id='inv-cur-usd'`).Scan(&cur); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cur != "USD" {
		t.Fatalf("库中 currency=%q, want USD —— 解析出的币种没落库，下一轮读回来还是空，账本会按 CNY 计", cur)
	}
}

// 端到端复核：XML -> 解析 -> 合并 -> 落库 -> 读回，币种一路不丢。
func TestXMLCurrencySurvivesFullHarvestRoundTrip(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-e2e-usd")

	fields := ParseInvoiceXML([]byte(xmlInvoiceUSD))
	if fields == nil {
		t.Fatal("ParseInvoiceXML 返回 nil")
	}
	inv := &Invoice{
		ID: "inv-e2e-usd", Status: "downloaded", Attempts: 1,
		Subject: fields.Seller, Amount: fields.Amount,
	}
	mergeXMLFields(inv, fields)
	if inv.Currency != "USD" {
		t.Fatalf("合并后 Currency=%q, want USD", inv.Currency)
	}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	// 用生产读路径读回来，而不是直接查库。
	got, err := store.GetInvoiceByIDScoped(ctx, "inv-e2e-usd", "u", "ws-cur")
	if err != nil {
		t.Fatalf("GetInvoiceByIDScoped: %v", err)
	}
	if got == nil {
		t.Fatal("读回的发票为 nil")
	}
	if got.Currency != "USD" {
		t.Fatalf("读回 Currency=%q, want USD", got.Currency)
	}
	// 读回来后进账本，必须归 USD 组。
	if c := currencyOrDefault(got.Currency); c != "USD" {
		t.Errorf("账本归组=%q, want USD", c)
	}
}

// 空币种不得覆盖库里已有的值（与其它字段同口径的「非空才写」）。
func TestUpdateInvoiceHarvest_EmptyCurrencyKeepsExisting(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-cur-keep")

	if _, err := store.pool.Exec(ctx,
		`UPDATE email_invoices SET currency='USD' WHERE id='inv-cur-keep'`); err != nil {
		t.Fatalf("preset: %v", err)
	}
	// 采集器这轮没解析出币种
	inv := &Invoice{ID: "inv-cur-keep", Status: "downloaded", Attempts: 1, Amount: 10, Currency: ""}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}
	var cur string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(currency,'') FROM email_invoices WHERE id='inv-cur-keep'`).Scan(&cur); err != nil {
		t.Fatalf("read: %v", err)
	}
	if cur != "USD" {
		t.Errorf("currency=%q, want USD（空币种不应把已有值抹成空）", cur)
	}
}
