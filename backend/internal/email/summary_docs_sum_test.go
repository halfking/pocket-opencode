package email

// summary_docs_sum_test.go — 需求 3「汇总金额」在**本地 CSV / Markdown** 这条
// 路径上的一致性契约。
//
// §7ac 修了飞书表格（LedgerRows）那条路径的浮点问题，但本地 CSV/MD
// （WriteInvoiceSummaryDocs）走的是另一段代码，本文件覆盖它。
//
// 实测发现的缺陷：明细行用 `%.2f` 格式化，合计行却用裸 float64 累加后再
// `%.2f`。两者口径不同 → 用户拿计算器逐行相加会对不上：
//
//	明细 1.005 / 2.675 / 8.615
//	逐行 %.2f 相加  = 12.30
//	合计（裸累加）  = 12.29      ← 差 1 分
//
// 注意 `%.2f` 本身**能**掩盖累加噪声（7.000000000000009 -> "7.00"），
// 所以飞书那条路径才会露出 `7.000000000000009` 字面量；本地这条路径的
// 问题不是噪声，而是**舍入口径不一致**。
//
// 负控对照：把合计改回 `total += inv.Amount`（明细仍 round2）
//          -> TestWriteInvoiceSummaryDocs_DetailSumsToTotal 转红。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// readCSVAmounts 解析生成的 CSV，返回金额列的数值与合计行数值。
//
// 列位差异（实测确认）：表头 `费用类型,对方单位,金额,...` → 明细金额在**索引 2**；
// 合计行 `合计,,,,,,,%.2f,` → 金额在**索引 7**。两处不同，解析时要分开取。
func readCSVAmounts(t *testing.T, path string) (detail []float64, total float64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		t.Fatalf("csv too short: %q", string(b))
	}
	for i, ln := range lines {
		cells := strings.Split(strings.TrimSpace(ln), ",")
		if len(cells) < 3 {
			continue
		}
		if cells[0] == "合计" {
			// 合计行的金额在第 8 列（索引 7）
			if len(cells) < 8 {
				t.Fatalf("total row has %d cells, want >=8: %q", len(cells), ln)
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(cells[7]), 64)
			if err != nil {
				t.Fatalf("parse total %q: %v", cells[7], err)
			}
			total = v
			continue
		}
		if i == 0 {
			continue // 表头
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(cells[2]), 64)
		if err != nil {
			t.Fatalf("parse detail %q: %v", cells[2], err)
		}
		detail = append(detail, v)
	}
	return detail, total
}

func TestWriteInvoiceSummaryDocs_DetailSumsToTotal(t *testing.T) {
	dir := t.TempDir()
	// 这三个值是「%.2f 舍入方向会翻转」的典型：.005 结尾
	invs := []Invoice{
		{Category: "办公", Seller: "甲", Amount: 1.005, Currency: "CNY", InvoiceNo: "A", InvoiceDate: "2026-10-01"},
		{Category: "办公", Seller: "乙", Amount: 2.675, Currency: "CNY", InvoiceNo: "B", InvoiceDate: "2026-10-01"},
		{Category: "办公", Seller: "丙", Amount: 8.615, Currency: "CNY", InvoiceNo: "C", InvoiceDate: "2026-10-01"},
	}
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatalf("write docs: %v", err)
	}

	detail, total := readCSVAmounts(t, csvPath)
	if len(detail) != 3 {
		t.Fatalf("expected 3 detail rows, got %d (%v)", len(detail), detail)
	}
	var sum float64
	for _, d := range detail {
		sum += d
	}
	// 关键契约：明细逐行相加必须等于合计行（差 1 分也是错账）
	if diff := sum - total; diff > 0.005 || diff < -0.005 {
		t.Fatalf("detail rows sum to %.2f but total row says %.2f — the ledger does not balance", sum, total)
	}

	// Markdown 里的合计必须与 CSV 一致（两条路径不能各算各的）
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), strconv.FormatFloat(total, 'f', 2, 64)) {
		t.Fatalf("markdown total must match csv total %.2f; md:\n%s", total, string(md))
	}
}

// 100 张 0.07：裸累加会出 7.000000000000009，但 %.2f 能掩盖。
// 这条钉住「%.2f 确实掩盖了累加噪声」，避免以后有人误以为本地路径也有
// §7ac 那个字面量问题。
func TestWriteInvoiceSummaryDocs_TwoDecimalHidesAccumulationNoise(t *testing.T) {
	dir := t.TempDir()
	invs := make([]Invoice, 100)
	for i := range invs {
		invs[i] = Invoice{Amount: 0.07, Currency: "CNY", Seller: "S", Category: "其他"}
	}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	_, total := readCSVAmounts(t, csvPath)
	if total != 7.00 {
		t.Fatalf("total = %.2f, want 7.00 (accumulation noise must be hidden)", total)
	}
}

// 明细行金额本身要规整到分：解析器可能给 126.005 这类三位小数。
func TestWriteInvoiceSummaryDocs_DetailAmountRoundedToCents(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{{Amount: 126.005, Currency: "CNY", Seller: "S", Category: "其他"}}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "126.005") {
		t.Fatalf("detail amount must be rounded to cents, csv:\n%s", string(raw))
	}
	detail, total := readCSVAmounts(t, csvPath)
	if len(detail) != 1 || detail[0] != total {
		t.Fatalf("single row: detail=%v total=%.2f must be equal", detail, total)
	}
}

// 空清单也要产出合法的合计行（0.00），不能崩也不能写空。
func TestWriteInvoiceSummaryDocs_EmptyStillHasTotal(t *testing.T) {
	dir := t.TempDir()
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws-1", nil)
	if err != nil {
		t.Fatalf("empty list must not error: %v", err)
	}
	if _, total := readCSVAmounts(t, csvPath); total != 0 {
		t.Fatalf("empty total = %.2f, want 0", total)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("markdown must still be written: %v", err)
	}
}

// 真实发票组合（本轮真实租户验证用过的两个数）。
func TestWriteInvoiceSummaryDocs_RealInvoiceTotals(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Amount: 126.00, Currency: "CNY", Seller: "Tencent", Category: "其他"},
		{Amount: 328.50, Currency: "CNY", Seller: "Tencent", Category: "其他"},
	}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	detail, total := readCSVAmounts(t, csvPath)
	var sum float64
	for _, d := range detail {
		sum += d
	}
	if total != 454.50 {
		t.Fatalf("total = %.2f, want 454.50", total)
	}
	if sum != total {
		t.Fatalf("detail sum %.2f != total %.2f", sum, total)
	}
	_ = filepath.Join(dir, "x")
}
