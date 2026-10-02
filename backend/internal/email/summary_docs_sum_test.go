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
// summaryCSVColumn 返回指定列名在 CSV 里的下标。
//
// 合并修订：本文件原先把金额下标写死成 7（「合计,,,,,,,1454.50,」的第 8 列）。
// 但 main 侧（243cda44）修掉的正是这个缺陷——7 个逗号让金额落到了**「文件名」**
// 列上，于是这个测试读的是文件名列（空串），报的错是
// `parse total "": invalid syntax`，看起来像 CSV 没写出来，实际是**读错了列**。
//
// 按列名定位后，表头顺序调整也不会再错位；而且它与生产代码
// （invoiceSummaryTotalRow）用的是同一份 invoiceSummaryHeader，
// 判据与实现不会各走各的。
func summaryCSVColumn(t *testing.T, name string) int {
	t.Helper()
	for i, col := range invoiceSummaryHeader {
		if col == name {
			return i
		}
	}
	t.Fatalf("表头里没有 %q 列: %v", name, invoiceSummaryHeader)
	return -1
}

func readCSVAmounts(t *testing.T, path string) (detail []float64, total float64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// CSV 带 UTF-8 BOM（Excel 兼容，见 pipeline.go 里的说明），第一行的
	// 第一个单元格会带上 BOM 前缀，比较前要去掉，否则表头永远匹配不上。
	text := strings.TrimPrefix(string(b), string(utf8BOM))
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		t.Fatalf("csv too short: %q", string(b))
	}
	header := strings.Split(strings.TrimSpace(lines[0]), ",")
	// 断言解析用的表头与生产代码用的是同一份（防止测试和生产各写一份列名）。
	if len(header) != len(invoiceSummaryHeader) {
		t.Fatalf("CSV 表头 %d 列，生产侧 invoiceSummaryHeader %d 列: %v",
			len(header), len(invoiceSummaryHeader), header)
	}
	amountCol := summaryCSVColumn(t, "金额")
	for i, ln := range lines {
		cells := strings.Split(strings.TrimSpace(ln), ",")
		if len(cells) <= amountCol {
			continue
		}
		if cells[0] == "合计" {
			v, err := strconv.ParseFloat(strings.TrimSpace(cells[amountCol]), 64)
			if err != nil {
				t.Fatalf("parse total %q（金额列=%d）: %v", cells[amountCol], amountCol, err)
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
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Category: "办公", Seller: "甲", Amount: 1.005, Currency: "CNY", InvoiceNo: "A", InvoiceDate: "2026-10-01"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Category: "办公", Seller: "乙", Amount: 2.675, Currency: "CNY", InvoiceNo: "B", InvoiceDate: "2026-10-01"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Category: "办公", Seller: "丙", Amount: 8.615, Currency: "CNY", InvoiceNo: "C", InvoiceDate: "2026-10-01"},
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
		invs[i] = Invoice{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 0.07, Currency: "CNY", Seller: "S", Category: "其他"}
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
	invs := []Invoice{{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.005, Currency: "CNY", Seller: "S", Category: "其他"}}
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
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00, Currency: "CNY", Seller: "Tencent", Category: "其他"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 328.50, Currency: "CNY", Seller: "Tencent", Category: "其他"},
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

// ---------------------------------------------------------------------------
// 跨币种：本地 CSV/MD 与飞书表格（LedgerRows）必须同口径
// ---------------------------------------------------------------------------

// readCSVRows 返回 CSV 的全部数据行（去掉表头），每行按逗号切分。
func readCSVRows(t *testing.T, path string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 同 readCSVAmounts：去掉 UTF-8 BOM，否则第一行的 cells[0] 带 BOM 前缀。
	text := strings.TrimPrefix(string(b), string(utf8BOM))
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		t.Fatalf("csv too short: %q", string(b))
	}
	out := make([][]string, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		out = append(out, strings.Split(strings.TrimSpace(ln), ","))
	}
	return out
}

// 合计行里的合计单元格。
func totalRowsOf(rows [][]string) [][]string {
	var out [][]string
	for _, r := range rows {
		if len(r) > 0 && r[0] == "合计" {
			out = append(out, r)
		}
	}
	return out
}

// 缺陷：WriteInvoiceSummaryDocs 把所有币种直接相加。本地 CSV 里
// 100.00 USD + 50.00 CNY 会写成一行「合计,,,,,,,150.00,」——币种列是空的，
// 读者无从判断 150 是什么币。而飞书那条路径（LedgerRows）早已按币种分组。
// 同一条需求的两条路径口径不一致 = 本地这份是错账。
func TestWriteInvoiceSummaryDocs_MultiCurrencyNotSummedTogether(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100.00, Currency: "USD", Seller: "AWS", Category: "云服务"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 50.00, Currency: "CNY", Seller: "腾讯", Category: "其他"},
	}
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	trs := totalRowsOf(readCSVRows(t, csvPath))
	if len(trs) != 2 {
		t.Fatalf("2 currencies must produce 2 total rows, got %d: %v", len(trs), trs)
	}
	// 币种列同样按表头定位，不写死 3（合并修订，理由同 summaryCSVAmountCol）。
	curCol := summaryCSVColumn(t, "币种")
	amountCol := summaryCSVColumn(t, "金额")
	byCur := map[string]float64{}
	for _, r := range trs {
		cur := strings.TrimSpace(r[curCol])
		if cur == "" {
			t.Fatalf("multi-currency total row must carry its currency label, got %v", r)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r[amountCol]), 64)
		if err != nil {
			t.Fatalf("parse total %q: %v", r[amountCol], err)
		}
		byCur[cur] = v
	}
	if byCur["USD"] != 100.00 || byCur["CNY"] != 50.00 {
		t.Fatalf("per-currency totals wrong: %v", byCur)
	}
	// 绝不能出现 150.00 这种跨币种的数
	for cur, v := range byCur {
		if v == 150.00 {
			t.Fatalf("%s total must not be the cross-currency sum 150.00: %v", cur, byCur)
		}
	}

	// Markdown 抬头同样不能给无币种的裸数字。
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(md)
	if strings.Contains(s, "**150.00**") {
		t.Fatalf("markdown total must not be the cross-currency sum:\n%s", s)
	}
	if !strings.Contains(s, "USD 100.00") || !strings.Contains(s, "CNY 50.00") {
		t.Fatalf("markdown must list per-currency totals:\n%s", s)
	}
}

// 单币种时输出形状必须与旧版逐字节一致：不带币种标签的合计行、抬头一个裸数字。
// 这条是「别顺手改坏既有对账习惯」的护栏。
func TestWriteInvoiceSummaryDocs_SingleCurrencyKeepsLegacyShape(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 126.00, Currency: "CNY", Seller: "腾讯", Category: "其他"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 328.50, Currency: "CNY", Seller: "腾讯", Category: "其他"},
	}
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	trs := totalRowsOf(readCSVRows(t, csvPath))
	if len(trs) != 1 {
		t.Fatalf("single currency must produce exactly 1 total row, got %d: %v", len(trs), trs)
	}
	// 币种列与金额列都按表头定位，不写死下标（合并修订，理由同 summaryCSVColumn）。
	curCol := summaryCSVColumn(t, "币种")
	amountCol := summaryCSVColumn(t, "金额")
	if strings.TrimSpace(trs[0][curCol]) != "" {
		t.Fatalf("single-currency total row must stay unlabeled (legacy shape), got %v", trs[0])
	}
	if strings.TrimSpace(trs[0][amountCol]) != "454.50" {
		t.Fatalf("total = %q, want 454.50", trs[0][amountCol])
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "合计金额 **454.50**") {
		t.Fatalf("single-currency markdown must keep the bare total:\n%s", string(md))
	}
	// 单币种时不许冒出「按币种」小节
	if strings.Contains(string(md), "按币种") {
		t.Fatalf("single-currency markdown must not have a per-currency section:\n%s", string(md))
	}
}

// 空清单仍然要有一行 0 合计（回归护栏：分组改造一度把它写没了）。
func TestWriteInvoiceSummaryDocs_EmptyStillHasTotalRow(t *testing.T) {
	dir := t.TempDir()
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	trs := totalRowsOf(readCSVRows(t, csvPath))
	if len(trs) != 1 {
		t.Fatalf("empty list must still have exactly 1 total row, got %d: %v", len(trs), trs)
	}
	if strings.TrimSpace(trs[0][summaryCSVColumn(t, "金额")]) != "0.00" {
		t.Fatalf("empty total = %q, want 0.00", trs[0][summaryCSVColumn(t, "金额")])
	}
}

// 币种为空的发票按 CNY 归组（与 currencyOrDefault 同源），不能凭空多一个空币种。
func TestWriteInvoiceSummaryDocs_EmptyCurrencyFoldsIntoCNY(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100.00, Currency: "", Seller: "A", Category: "其他"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 50.00, Currency: "CNY", Seller: "B", Category: "其他"},
	}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws-1", invs)
	if err != nil {
		t.Fatal(err)
	}
	trs := totalRowsOf(readCSVRows(t, csvPath))
	if len(trs) != 1 {
		t.Fatalf("empty currency must fold into CNY (1 total row), got %d: %v", len(trs), trs)
	}
	if strings.TrimSpace(trs[0][summaryCSVColumn(t, "金额")]) != "150.00" {
		t.Fatalf("total = %q, want 150.00", trs[0][summaryCSVColumn(t, "金额")])
	}
}
