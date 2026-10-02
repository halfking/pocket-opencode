package email

// ledger_multicurrency_test.go — 需求 3「汇总金额」的多币种分组。
//
// 2026-10-01：币种字段修好后（§7ae），合计仍把所有币种**直接相加**。
// USD 126 + CNY 126 = "252" 在财务上不成立——需求要的是「汇总金额」，
// 而跨币种的算术和不是金额。
//
// 规则（经用户确认）：**按币种分组，各出一个合计**。
//   - 单一币种（当前真实数据：7 张全是 CNY）时只出一行，且**不带币种标签**，
//     与旧输出逐字节一致，避免为了多币种而改变既有单币种表现；
//   - 多币种时每币种一行合计，且必须标出币种与该币种的张数——
//     否则两行「合计」摆在一起仍然没法用。
//
// 负控对照：把分组累加改回单一累加器
//          -> TestLedgerRows_MultiCurrencyEmitsOneTotalPerCurrency 转红。

import (
	"encoding/json"
	"strings"
	"testing"
)

// 收集所有「合计」行。
func totalRows(rows [][]any) [][]any {
	var out [][]any
	for _, r := range rows {
		if len(r) > 0 && r[0] == "合计" {
			out = append(out, r)
		}
	}
	return out
}

// onlyTotal 取唯一的那个币种合计；清单不是单币种时直接判失败。
//
// 2026-10-01 起 LedgerRows 的第二个返回值是 []CurrencyTotal（按币种分组），
// 不再是一个可以跨币种相加的标量。凡是「只关心单币种总额」的用例
// （本文件外还有 ledger_sum_test.go 等）都改走这个辅助函数，
// 这样单币种断言不会因为签名变化而悄悄变成「把多币种加起来」的错误断言。
func onlyTotal(t *testing.T, invs []Invoice) float64 {
	t.Helper()
	_, totals := LedgerRows(invs)
	if len(totals) != 1 {
		t.Fatalf("expected exactly 1 currency total, got %d: %+v", len(totals), totals)
	}
	return totals[0].Amount
}

// 单币种：必须只有一行合计、且不带币种标签（与旧行为一致）。
func TestLedgerRows_SingleCurrencyKeepsLegacyShape(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00, Currency: "CNY", Category: "其他", Seller: "腾讯"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 328.50, Currency: "CNY", Category: "其他", Seller: "腾讯"},
	}
	rows, totals := LedgerRows(invs)
	if len(totals) != 1 {
		t.Fatalf("single currency must produce 1 CurrencyTotal, got %+v", totals)
	}
	total := totals[0].Amount
	if total != 454.50 {
		t.Fatalf("total = %v, want 454.50", total)
	}
	trs := totalRows(rows)
	if len(trs) != 1 {
		t.Fatalf("single currency must produce exactly 1 total row, got %d", len(trs))
	}
	if trs[0][2].(float64) != 454.50 {
		t.Errorf("total amount = %v, want 454.50", trs[0][2])
	}
	// 单币种时币种列为空——旧行为如此
	if trs[0][3] != "" {
		t.Errorf("single-currency total must leave the currency cell empty, got %v", trs[0][3])
	}
	if trs[0][8] != "计入 2 张 / 共 2 张" {
		t.Errorf("count = %v, want 计入 2 张 / 共 2 张", trs[0][8])
	}
}

// 多币种：每个币种一行合计，且**不能**把它们相加。
func TestLedgerRows_MultiCurrencyEmitsOneTotalPerCurrency(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00, Currency: "CNY", Category: "其他", Seller: "腾讯"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 328.50, Currency: "CNY", Category: "其他", Seller: "腾讯"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100.00, Currency: "USD", Category: "其他", Seller: "AWS"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 50.00, Currency: "USD", Category: "其他", Seller: "AWS"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 20.00, Currency: "EUR", Category: "其他", Seller: "EU Vendor"},
	}
	rows, totals := LedgerRows(invs)
	trs := totalRows(rows)
	if len(trs) != 3 {
		t.Fatalf("3 currencies must produce 3 total rows, got %d: %v", len(trs), trs)
	}

	// 按币种索引出合计
	byCur := map[string]float64{}
	countByCur := map[string]string{}
	for _, r := range trs {
		byCur[r[3].(string)] = r[2].(float64)
		countByCur[r[3].(string)] = r[8].(string)
	}
	if byCur["CNY"] != 454.50 {
		t.Errorf("CNY total = %v, want 454.50", byCur["CNY"])
	}
	if byCur["USD"] != 150.00 {
		t.Errorf("USD total = %v, want 150.00", byCur["USD"])
	}
	if byCur["EUR"] != 20.00 {
		t.Errorf("EUR total = %v, want 20.00", byCur["EUR"])
	}
	if countByCur["CNY"] != "计入 2 张 / 共 2 张" {
		t.Errorf("CNY count = %q, want 计入 2 张 / 共 2 张", countByCur["CNY"])
	}
	if countByCur["USD"] != "计入 2 张 / 共 2 张" {
		t.Errorf("USD count = %q, want 计入 2 张 / 共 2 张", countByCur["USD"])
	}
	if countByCur["EUR"] != "计入 1 张 / 共 1 张" {
		t.Errorf("EUR count = %q, want 计入 1 张 / 共 1 张", countByCur["EUR"])
	}

	// 返回值必须按币种分开，不能再是一个跨币种的标量总额
	// （2026-10-01 改签名的原因：624.50 = 454.50 CNY + 150 USD + 20 EUR，
	//  这个数字被写进任何报表都是错账）。
	if len(totals) != 3 {
		t.Fatalf("expected 3 CurrencyTotal entries, got %d: %+v", len(totals), totals)
	}
	byReturned := map[string]float64{}
	for _, ct := range totals {
		byReturned[ct.Currency] = ct.Amount
	}
	if byReturned["CNY"] != 454.50 || byReturned["USD"] != 150.00 || byReturned["EUR"] != 20.00 {
		t.Fatalf("per-currency returned totals wrong: %v", byReturned)
	}
	// 返回值与写进表格的行必须一致（不能一个分币种一个不分）。
	if len(totals) != len(trs) {
		t.Fatalf("returned totals (%d) and total rows (%d) must agree", len(totals), len(trs))
	}
	for _, ct := range totals {
		if byCur[ct.Currency] != ct.Amount {
			t.Errorf("%s: row says %v but returned total says %v", ct.Currency, byCur[ct.Currency], ct.Amount)
		}
		if ct.Count == 0 {
			t.Errorf("%s: returned Count must be set, got 0", ct.Currency)
		}
	}
}

// 明细行的币种列必须与发票一致（分组后仍能逐行核对）。
func TestLedgerRows_DetailKeepsItsOwnCurrency(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100.00, Currency: "USD", Category: "其他", Seller: "AWS"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 200.00, Currency: "CNY", Category: "其他", Seller: "腾讯"},
	}
	rows, _ := LedgerRows(invs)
	if rows[1][3] != "USD" || rows[2][3] != "CNY" {
		t.Fatalf("detail rows must keep their own currency: got %v and %v", rows[1][3], rows[2][3])
	}
}

// 币种为空的行归入 CNY（与 currencyOrDefault 一致），不能凭空多出一个空币种合计。
func TestLedgerRows_EmptyCurrencyFoldsIntoCNY(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100.00, Currency: "", Category: "其他", Seller: "A"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 50.00, Currency: "CNY", Category: "其他", Seller: "B"},
	}
	rows, _ := LedgerRows(invs)
	trs := totalRows(rows)
	if len(trs) != 1 {
		t.Fatalf("empty currency must fold into CNY (1 total row), got %d: %v", len(trs), trs)
	}
	if trs[0][2].(float64) != 150.00 {
		t.Errorf("total = %v, want 150.00", trs[0][2])
	}
	if trs[0][8] != "计入 2 张 / 共 2 张" {
		t.Errorf("count = %v, want 计入 2 张 / 共 2 张", trs[0][8])
	}
}

// 合计行在 JSON 里必须是干净数字（§7ac 的契约在多币种下同样成立）。
func TestLedgerRows_MultiCurrencyTotalsAreExactInJSON(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.07, Currency: "USD"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.07, Currency: "USD"},
	}
	// 100 x 0.07 才会暴露浮点噪声，这里用 2 张 + 另一个币种
	for i := 0; i < 98; i++ {
		invs = append(invs, Invoice{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.07, Currency: "USD"})
	}
	invs = append(invs, Invoice{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.07, Currency: "CNY"})
	rows, _ := LedgerRows(invs)
	for _, r := range totalRows(rows) {
		b, err := json.Marshal(r[2])
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, "0000000") {
			t.Errorf("total %s must be a clean number, got %s", r[3], s)
		}
	}
}
