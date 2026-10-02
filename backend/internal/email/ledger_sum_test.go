package email

// ledger_sum_test.go — 需求 3「汇总金额」的精度契约。
//
// 2026-10-01 实测发现的缺陷：LedgerRows 原本用裸 `total += inv.Amount` 累加
// float64，而金额会原样 json.Marshal 后写进飞书表格。实测 100 张 0.07 的发票：
//
//	float64 累加 = 7.00000000000000888178
//	JSON 字面量   = 7.000000000000009
//
// 也就是说**表格里会直接显示 7.000000000000009**。金额是财务数据，
// 对账时这就是错账。修复：改用整数分累加 + round2。
//
// 这里钉死三条：
//  1. 合计在 JSON 里必须是干净的最短表示（"7" 而不是 "7.000000000000009"）；
//  2. 明细行的单张金额同样要 round2（解析器可能给出 126.005 这类值）；
//  3. 真实场景组合（126.00 + 328.50 = 454.50）必须精确。
//
// 负控对照：把 cents 累加改回 `total += inv.Amount` -> TestLedgerRows_TotalIsExactInJSON 转红。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLedgerRows_TotalIsExactInJSON(t *testing.T) {
	// 100 张 0.07：float64 累加必然出噪声
	invs := make([]Invoice, 0, 100)
	for i := 0; i < 100; i++ {
		invs = append(invs, Invoice{
			Category: "办公", Seller: "某供应商", Amount: 0.07,
			Currency: "CNY", InvoiceNo: "X", InvoiceDate: "2026-10-01", Status: "downloaded",
			// FilePath 是合计判据的另一半（与 status 缺一不可）。合并后判据收紧，
			// 多行字面量没法用脚本批量补，这里手工跟上同文件其它夹具。
			FilePath: "ledger-fixture.pdf",
		})
	}
	rows, totals := LedgerRows(invs)
	if len(totals) != 1 {
		t.Fatalf("expected 1 currency total, got %+v", totals)
	}
	total := totals[0].Amount
	if total != 7.00 {
		t.Fatalf("total = %v, want 7.00", total)
	}
	b, err := json.Marshal(rows[len(rows)-1])
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "7.0000") {
		t.Fatalf("total row must be a clean number in JSON, got %s", got)
	}
	if !strings.Contains(got, `7`) {
		t.Fatalf("total row must contain 7, got %s", got)
	}
}

// 明细行的单张金额也要规整：解析器可能给出 126.005 这类三位小数值。
func TestLedgerRows_DetailAmountRoundedToCents(t *testing.T) {
	invs := []Invoice{{
		Category: "办公", Seller: "供应商", Amount: 126.005,
		Currency: "CNY", InvoiceNo: "A", InvoiceDate: "2026-10-01",
		Status: "downloaded", FilePath: "ledger-fixture.pdf",
	}}
	rows, totals := LedgerRows(invs)
	detail, err := json.Marshal(rows[1])
	if err != nil {
		t.Fatal(err)
	}
	// 126.005 四舍五入到分 = 126.01（或 126.00，取决于浮点表示），
	// 但绝不能是 126.005 这种三位小数混进金额列。
	if strings.Contains(string(detail), "126.005") {
		t.Fatalf("detail amount must be rounded to cents, got %s", detail)
	}
	// 合计必须与规整后的明细一致
	want := rows[1][2].(float64)
	if len(totals) != 1 || totals[0].Amount != want {
		t.Fatalf("total %+v must equal the rounded detail %v", totals, want)
	}
}

// 真实发票组合：126.00 + 328.50 = 454.50（本轮真实租户验证用过的两个数）。
func TestLedgerRows_RealInvoiceTotals(t *testing.T) {
	cases := []struct {
		name string
		invs []Invoice
		want float64
	}{
		{"qq wallet pair", []Invoice{
			{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00, Category: "其他", Seller: "Tencent-Cloud-Computing-Co-Ltd"},
			{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 328.50, Category: "其他", Seller: "Tencent-Cloud-Computing-Co-Ltd"},
		}, 454.50},
		{"single", []Invoice{{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00}}, 126.00},
		{"mixed decimals", []Invoice{{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.1}, {Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.2}}, 0.30},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, totals := LedgerRows(tc.invs)
			// 空清单没有币种可归组，返回 0 个合计（合计行仍会写进表格，
			// 由 TestLedgerRows_TotalRowAlwaysPresent 负责）。
			if tc.invs == nil {
				if len(totals) != 0 {
					t.Fatalf("empty list must produce 0 currency totals, got %+v", totals)
				}
				return
			}
			if len(totals) != 1 {
				t.Fatalf("expected 1 currency total, got %+v", totals)
			}
			if totals[0].Amount != tc.want {
				t.Fatalf("total = %v, want %v", totals[0].Amount, tc.want)
			}
		})
	}
}

// 合计行必须始终存在（需求：「整理一个列表…并汇总金额」）。
func TestLedgerRows_TotalRowAlwaysPresent(t *testing.T) {
	rows, _ := LedgerRows(nil)
	if len(rows) != 2 {
		t.Fatalf("header + total row expected, got %d rows", len(rows))
	}
	if rows[1][0] != "合计" {
		t.Fatalf("last row must be the total row, got %v", rows[1][0])
	}
}
