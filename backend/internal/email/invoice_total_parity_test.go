package email

// invoice_total_parity_test.go —— 「汇总金额」这条需求的三条实现必须给出同一个数。
//
// ## 这个护栏防的是什么
//
// 2026-10-02 真实库实测（2 行发票：3500 downloaded+有文件、58000 new+无文件）：
//
//	LedgerRows（ledger.go，飞书表格）              → CNY  3,500
//	WriteInvoiceSummaryDocs（pipeline.go，本地 CSV）→ CNY  3,500
//	InvoiceListStats（invoice_list.go，列表 API）   → CNY 61,500
//
// 发票页显示的是第三条（前端 resolveSummaryGroups 优先读列表 API 的
// amounts），飞书台账显示的是第一条。**同一份数据、两个差 17.6 倍的「合计」。**
//
// 病根不是第三处写错了，是同一条规则被**手写了三遍**：
// 前两处是逐字符相同的内联表达式，第三处干脆漏了过滤。而三处各自只测自己，
// 没有任何一个用例会让「三处不一致」这件事变红——所以这个缺陷能在两轮
// 「跨币种分组」的修复里三次被漏掉而不被发现。
//
// 现在判据收敛到 InvoiceCountsTowardTotal 一个函数，SQL 那处无法直接复用
// Go 函数，于是这里用**同一组夹具同时跑两边**来钉住它们相等。这是能钉住的：
// 谁改谁红。
//
// ## 为什么必须包含「downloaded 但没有文件」这一档
//
// 只测 new+无文件是不够的：那一条 status 过滤顺手就挡住了。真正容易被漏的是
// 「状态已经是 downloaded，但服务端磁盘上其实没有凭证」——采集流水线
// 先改 status 后落文件，中途失败就会留下这种行。它长得和正常发票一模一样，
// 却不该计入对账总额。

import (
	"context"
	"testing"
)

// parityFixture 是三条实现共用的夹具。金额刻意选成互不相同的值，
// 任何一处多算/少算都会让对不上，而不是「刚好抵消」。
func parityFixture(t *testing.T, store *Store) {
	t.Helper()
	// 计入合计的两张（其中一张是外币，用来同时验证按币种分组）
	seedInvoiceForStats(t, store, "inv-par-ok-cny", "CNY", "downloaded", 3500, "email-invoices/ok-cny.pdf")
	seedInvoiceForStats(t, store, "inv-par-ok-filed", "CNY", "filed", 500, "email-invoices/ok-filed.pdf")
	seedInvoiceForStats(t, store, "inv-par-ok-usd", "USD", "downloaded", 100, "email-invoices/ok-usd.pdf")
	// 不计入的四张，四个**不同**的不计入理由，逐个覆盖
	seedInvoiceForStats(t, store, "inv-par-bare", "CNY", "new", 58000, "")
	seedInvoiceForStats(t, store, "inv-par-nofile", "CNY", "downloaded", 8888, "")
	seedInvoiceForStats(t, store, "inv-par-failed-file", "CNY", "failed", 777, "email-invoices/failed.pdf")
	seedInvoiceForStats(t, store, "inv-par-pending", "CNY", "pending", 666, "")
}

// 三条路径必须给出同一个「按币种分组的合计」。
func TestInvoiceTotalParity_SQLMatchesGoPredicate(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	parityFixture(t, store)

	// 期望值由 Go 判据算出来——它是这条规则的**定义**。
	// 若有人改了 InvoiceCountsTowardTotal，这里的期望值会跟着变，
	// 而 SQL 那个数不会变，两者一比就会红；反过来改 SQL 也一样红。
	var want []CurrencyTotal
	acc := map[string]int64{}
	cnt := map[string]int{}
	for _, inv := range []Invoice{
		{Status: "downloaded", FilePath: "a", Currency: "CNY", Amount: 3500},
		{Status: "filed", FilePath: "b", Currency: "CNY", Amount: 500},
		{Status: "downloaded", FilePath: "c", Currency: "USD", Amount: 100},
		{Status: "new", FilePath: "", Currency: "CNY", Amount: 58000},
		{Status: "downloaded", FilePath: "", Currency: "CNY", Amount: 8888},
		{Status: "failed", FilePath: "d", Currency: "CNY", Amount: 777},
		{Status: "pending", FilePath: "", Currency: "CNY", Amount: 666},
	} {
		if !InvoiceCountsTowardTotal(inv) {
			continue
		}
		cur := currencyOrDefault(inv.Currency)
		acc[cur] += int64(round2(inv.Amount) * 100)
		cnt[cur]++
	}
	for cur, cents := range acc {
		want = append(want, CurrencyTotal{Currency: cur, Amount: float64(cents) / 100, Count: cnt[cur]})
	}
	// 直接从库读回来的发票切片：让 SQL 那条和 Go 那条吃**同一批行**。
	page, err := store.ListInvoicesPage(ctx, "u", "ws-stats", "", 200, 0)
	if err != nil {
		t.Fatalf("ListInvoicesPage: %v", err)
	}
	invs := page.Invoices

	st, err := store.InvoiceListStats(ctx, "u", "ws-stats", "")
	if err != nil {
		t.Fatalf("InvoiceListStats: %v", err)
	}
	if len(st.Amounts) != len(want) {
		t.Fatalf("SQL 合计的币种组数 = %d，判据算出 %d：\n SQL=%+v\n 判据=%+v\n（这正是 61,500 vs 3,500 那类漂移）",
			len(st.Amounts), len(want), st.Amounts, want)
	}
	for _, w := range want {
		var got *CurrencyTotal
		for i := range st.Amounts {
			if st.Amounts[i].Currency == w.Currency {
				got = &st.Amounts[i]
			}
		}
		if got == nil {
			t.Fatalf("SQL 合计缺 %s 组：%+v", w.Currency, st.Amounts)
		}
		if got.Amount != w.Amount || got.Count != w.Count {
			t.Errorf("%s 组 SQL=%v/%d 张，判据=%v/%d 张", w.Currency, got.Amount, got.Count, w.Amount, w.Count)
		}
	}

	// 第三条路径：LedgerRows 的 totals。它不吃 DB，只吃切片。
	_, ledgerTotals := LedgerRows(invs)
	if len(ledgerTotals) != len(want) {
		t.Fatalf("LedgerRows 合计组数 = %d，判据算出 %d：\n Ledger=%+v\n 判据=%+v",
			len(ledgerTotals), len(want), ledgerTotals, want)
	}
	for _, w := range want {
		var got *CurrencyTotal
		for i := range ledgerTotals {
			if ledgerTotals[i].Currency == w.Currency {
				got = &ledgerTotals[i]
			}
		}
		if got == nil || got.Amount != w.Amount || got.Count != w.Count {
			t.Errorf("LedgerRows 的 %s 组 = %+v，判据 = %+v", w.Currency, got, w)
		}
	}
}

// 反向：未核验的行**必须仍然出现在明细里**，只是不进合计。
//
// 这条护栏对应需求 3 的取舍（保留行、标记未核验、合计不含）。只断言合计
// 的话，一个「把未核验行直接删掉」的实现也能让上面的用例全绿——那会丢掉
// 「有一封 58,000 的东西需要人去追」这条诊断信号。
func TestInvoiceTotalParity_UnverifiedRowsStillListed(t *testing.T) {
	rows, totals := LedgerRows([]Invoice{
		{Status: "downloaded", FilePath: "a", Amount: 3500, Currency: "CNY", Subject: "真发票"},
		{Status: "new", FilePath: "", Amount: 58000, Currency: "CNY", Subject: "AWS 对账单"},
	})
	// 表头 + 2 明细 + 1 合计
	if len(rows) != 4 {
		t.Fatalf("行数 = %d，want 4（表头+2 明细+1 合计）：%+v", len(rows), rows)
	}
	// 合计不含那 58,000
	if len(totals) != 1 || totals[0].Amount != 3500 || totals[0].Count != 1 {
		t.Fatalf("合计 = %+v，want CNY 3500 / 1 张", totals)
	}
	// 两行明细都在，且「核验」列如实区分
	if got := rows[1][7]; got != "已核验" {
		t.Errorf("第 1 条明细的核验列 = %v，want 已核验", got)
	}
	if got := rows[2][7]; got != "未核验" {
		t.Errorf("第 2 条明细的核验列 = %v，want 未核验", got)
	}
	// 合计行必须说清「计入几张 / 共几张」——只写「共 2 张」会让读者以为
	// 3,500 是那两行的合计，而这正是本次缺陷在纸面上的表现。
	totalCell, ok := rows[3][8].(string)
	if !ok {
		t.Fatalf("合计行的张数单元格类型 = %T，want string", rows[3][8])
	}
	if totalCell != "计入 1 张 / 共 2 张" {
		t.Errorf("合计行张数 = %q，want 「计入 1 张 / 共 2 张」", totalCell)
	}
}

// 表头与明细行、合计行的列数必须一致，且写入范围跟着变宽。
//
// 飞书表格按 LedgerCellRange 给出的列数写；列数与表头对不上时，
// 表头多出来的那列会被静默丢掉——用户在表格里看不到「核验」列，
// 而代码里明明写了。纯文本输出里没有任何症状。
func TestInvoiceTotalParity_ColumnCountMatchesHeader(t *testing.T) {
	rows, _ := LedgerRows([]Invoice{
		{Status: "downloaded", FilePath: "a", Amount: 1, Currency: "CNY"},
		{Status: "new", FilePath: "", Amount: 2, Currency: "CNY"},
	})
	header := rows[0]
	for i, r := range rows {
		if len(r) != len(header) {
			t.Errorf("第 %d 行有 %d 列，表头 %d 列（行=%v）", i, len(r), len(header), r)
		}
	}
	if header[7] != "核验" {
		t.Errorf("表头第 8 列 = %v，want 核验", header[7])
	}
	// 列宽 9 → 10 后写入范围必须跟着变，否则最后一列写不出去
	if got := LedgerCellRange("sht", rows); got != "sht!A1:J4" {
		t.Errorf("LedgerCellRange = %q，want sht!A1:J4（4 行 × 10 列）", got)
	}
}
