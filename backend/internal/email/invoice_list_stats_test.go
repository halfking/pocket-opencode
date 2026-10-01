package email

// invoice_list_stats_test.go — 发票列表的合计必须按币种分组。
//
// 缺陷（2026-10-01 补齐「多币种分组」规则时发现的第三处漏网）：
// InvoiceListStats 此前是裸 `SUM(amount)`，把 USD 与 CNY 直接相加，
// 经 API 的 `amount` 字段送到前端，前端再渲染成「¥xxx」——错账。
//
// 这是同一条规则的前两处已修（LedgerRows、WriteInvoiceSummaryDocs），第三处。
// 与前两处的差别：这里在 **SQL 层**聚合，最容易被「看起来是个总数」骗过。
//
// 单币种时必须保持旧行为（amount 标量可用），否则前端全部要改。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip）。
//
// 负控：把 GROUP BY 去掉改回 SUM(amount) -> 本文件转红。

import (
	"context"
	"testing"
)

func seedInvoiceForStats(t *testing.T, store *Store, id, currency, status string, amount float64) {
	t.Helper()
	ctx := context.Background()
	// 同一测试里会插多张发票，账户/邮件只建一次（与 seedInvoiceForCurrency 同口径）
	var n int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_accounts WHERE id='acct-stats'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n == 0 {
		seedAccount(t, store, "acct-stats", "u", "ws-stats")
	}
	emailID := "em-" + id
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1,'acct-stats','ws-stats',$1,'s@example.com','subject','snippet',1700000000,1700000000)
		ON CONFLICT (id) DO NOTHING`, emailID); err != nil {
		t.Fatalf("seed email %s: %v", emailID, err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            amount, currency, status, attempts, created_at, updated_at)
		VALUES ($1,$2,'acct-stats','u','ws-stats',$3,$4,$5,0,1700000000,1700000000)`,
		id, emailID, amount, currency, status); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

// 多币种：必须分组，且绝不能出现跨币种的总额。
func TestInvoiceListStats_MultiCurrencyGrouped(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoiceForStats(t, store, "inv-st-usd", "USD", "downloaded", 100)
	seedInvoiceForStats(t, store, "inv-st-cny1", "CNY", "downloaded", 50)
	seedInvoiceForStats(t, store, "inv-st-cny2", "CNY", "downloaded", 50)

	st, err := store.InvoiceListStats(ctx, "u", "ws-stats", "")
	if err != nil {
		t.Fatalf("InvoiceListStats: %v", err)
	}
	if len(st.Amounts) != 2 {
		t.Fatalf("2 currencies must produce 2 groups, got %d: %+v", len(st.Amounts), st.Amounts)
	}
	byCur := map[string]float64{}
	for _, g := range st.Amounts {
		byCur[g.Currency] = g.Amount
	}
	if byCur["USD"] != 100 || byCur["CNY"] != 100 {
		t.Fatalf("per-currency totals wrong: %v", byCur)
	}
	// 绝不能出现 200 这种跨币种的数
	for cur, v := range byCur {
		if v == 200 {
			t.Fatalf("%s total must not be the cross-currency sum 200: %v", cur, byCur)
		}
	}
	// 多币种时标量 Amount 必须是 0 而不是 200（0 至少不会冒充成正确金额）
	if st.Amount != 0 {
		t.Fatalf("multi-currency scalar Amount must be 0, got %v", st.Amount)
	}
}

// 单币种：保持旧形状，标量 Amount 可用，且带币种。
func TestInvoiceListStats_SingleCurrencyKeepsScalar(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoiceForStats(t, store, "inv-sc-1", "CNY", "downloaded", 126.00)
	seedInvoiceForStats(t, store, "inv-sc-2", "CNY", "downloaded", 328.50)

	st, err := store.InvoiceListStats(ctx, "u", "ws-stats", "")
	if err != nil {
		t.Fatalf("InvoiceListStats: %v", err)
	}
	if len(st.Amounts) != 1 {
		t.Fatalf("single currency must produce 1 group, got %+v", st.Amounts)
	}
	if st.Amount != 454.50 {
		t.Fatalf("scalar Amount = %v, want 454.50（单币种必须保持旧行为）", st.Amount)
	}
	if st.Currency != "CNY" {
		t.Fatalf("Currency = %q, want CNY", st.Currency)
	}
	if st.Amounts[0].Count != 2 {
		t.Fatalf("group count = %d, want 2", st.Amounts[0].Count)
	}
}

// 币种为空归 CNY（与 currencyOrDefault 同源），不凭空多一个空币种组。
func TestInvoiceListStats_EmptyCurrencyFoldsIntoCNY(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoiceForStats(t, store, "inv-ec-1", "", "downloaded", 10)
	seedInvoiceForStats(t, store, "inv-ec-2", "CNY", "downloaded", 5)

	st, err := store.InvoiceListStats(ctx, "u", "ws-stats", "")
	if err != nil {
		t.Fatalf("InvoiceListStats: %v", err)
	}
	if len(st.Amounts) != 1 {
		t.Fatalf("empty currency must fold into CNY (1 group), got %+v", st.Amounts)
	}
	if st.Amounts[0].Currency != "CNY" || st.Amounts[0].Amount != 15 {
		t.Fatalf("got %+v, want CNY 15", st.Amounts[0])
	}
}

// 状态过滤仍生效，且过滤后的合计也按币种分组。
func TestInvoiceListStats_StatusFilterRespected(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoiceForStats(t, store, "inv-sf-1", "CNY", "filed", 100)
	seedInvoiceForStats(t, store, "inv-sf-2", "CNY", "downloaded", 999)

	st, err := store.InvoiceListStats(ctx, "u", "ws-stats", "filed")
	if err != nil {
		t.Fatalf("InvoiceListStats: %v", err)
	}
	if st.Total != 1 || st.Filed != 1 {
		t.Fatalf("total=%d filed=%d, want 1/1", st.Total, st.Filed)
	}
	if st.Amount != 100 {
		t.Fatalf("filtered Amount = %v, want 100", st.Amount)
	}
}
