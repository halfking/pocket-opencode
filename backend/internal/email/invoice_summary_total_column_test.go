package email

// invoice_summary_total_column_test.go — 汇总 CSV 的合计必须落在「金额」列。
//
// 2026-10-02 实测缺陷：合计行写死成 `"合计,,,,,,,%.2f,\n"`，7 个逗号把金额推到了
// **第 8 列「文件名」**。用 CSV 解析器读真实产物确认过：
//
//	col1=费用类型 col2=对方单位 col3=金额 col4=币种 col5=发票号
//	col6=日期 col7=状态 col8=文件名 col9=来源邮件
//	合计行 → 费用类型=合计、文件名=3500.00
//
// 需求原文要的是「整理一个列表，记录必要信息并**汇总金额**」。金额落在文件名列里，
// 在 Excel 里金额列是空的，对不上账，而且没有任何报错。
//
// 判据用 encoding/csv 解析，而不是数字符串——这正是本项目反复吃过的亏：
// 靠数逗号/扫源码文本的判据会被自己的注释和格式串喂饱。

import (
	"encoding/csv"
	"os"
	"strconv"
	"strings"
	"testing"
)

func readSummaryCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return recs
}

func colIndex(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("表头里找不到列 %q，实际表头=%v", name, header)
	return -1
}

// 合计金额必须写在「金额」列，且只出现一次。
func TestInvoiceSummaryCSV_TotalLandsInAmountColumn(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Category: "其他", Seller: "杭州创客家投资管理有限公司", Amount: 3500, Currency: "CNY",
			InvoiceNo: "26332000008261110741", InvoiceDate: "2026-09-24",
			Status: "downloaded", FilePath: "email-invoices/ws/a.pdf", Subject: "发票已开具"},
		// 这条 failed 且金额非零：不进合计，但**要出现在列表里**。
		{Category: "其他", Seller: "误抽取", Amount: 999, Status: "failed", Subject: "x"},
	}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}

	recs := readSummaryCSV(t, csvPath)
	if len(recs) < 3 {
		t.Fatalf("期望 表头+2 条发票+合计 共 >=4 行，得到 %d：%v", len(recs), recs)
	}
	header := recs[0]
	amtCol := colIndex(t, header, "金额")
	fileCol := colIndex(t, header, "文件名")

	// 找合计行：首列为「合计」。
	sumRow := -1
	for i, r := range recs {
		if len(r) > 0 && r[0] == "合计" {
			sumRow = i
			break
		}
	}
	if sumRow < 0 {
		t.Fatalf("没找到合计行，实际内容=%v", recs)
	}
	row := recs[sumRow]

	if len(row) != len(header) {
		t.Fatalf("合计行列数=%d，表头列数=%d：%v", len(row), len(header), row)
	}
	got, err := strconv.ParseFloat(row[amtCol], 64)
	if err != nil {
		t.Fatalf("「金额」列不是数字：%q（整行=%v）", row[amtCol], row)
	}
	if got != 3500 {
		t.Errorf("合计金额=%v，want 3500（failed 且金额非零的那条不计入）", got)
	}
	// 回归钉：原来金额落在「文件名」列。
	if v := strings.TrimSpace(row[fileCol]); v != "" {
		t.Errorf("「文件名」列不应有内容，却有 %q —— 合计又跑错列了", v)
	}
}

// 合计行不得混进 Markdown 表格：MD 按 7 列渲染，塞进去会多一张空壳行。
func TestInvoiceSummaryCSV_TotalRowNotLeakedIntoMarkdown(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Category: "其他", Seller: "单位甲", Amount: 120, Currency: "CNY", Status: "downloaded",
			FilePath: "email-invoices/ws/a.pdf", Subject: "发票"},
	}
	_, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, "合计") {
			t.Errorf("Markdown 表格里混进了合计行：%q", line)
		}
	}
	if !strings.Contains(string(data), "合计金额 **120.00**") {
		t.Errorf("Markdown 缺少合计金额：\n%s", data)
	}
}

// Markdown 头部必须说清「列了几张」和「几张进了合计」。
//
// 2026-10-02 实测：头部用的是 len(invoices)（全部发票），而合计只累加
// status ∈ {downloaded, filed} 且 FilePath 非空的。于是 pending/failed 发票
// 一旦存在，头部会写「共 3 张 · 合计金额 3500.00」——读者自然以为这 3 张都
// 算进了 3500，实际只有 1 张。和 2026-10-01 修过的 LedgerTotal 口径是同一类
// 问题：同一个数字在两处用不同口径，且没有任何提示。
func TestInvoiceSummaryCSV_MarkdownHeaderDistinguishesListedVsCounted(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Category: "其他", Seller: "甲", Amount: 100, Status: "downloaded",
			FilePath: "email-invoices/ws/a.pdf", Subject: "发票"},
		{Category: "交通", Seller: "乙", Amount: 23.45, Status: "pending", Subject: "待下载"},
		{Category: "其他", Seller: "丙", Amount: 999, Status: "failed", Subject: "抽取错误"},
	}
	_, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	head := strings.SplitN(string(data), "\n", 4)[2]
	// 断言必须精确到「计入合计 N 张」这个措辞。不能只判 Contains(head, "1")——
	// 日期里的 2026-10-02 就带 1，那样缺陷在、断言照样绿（我自己先写错过一次）。
	if !strings.Contains(head, "共 3 张") {
		t.Errorf("头部应说明共列出 3 张，实际：%q", head)
	}
	if !strings.Contains(head, "计入合计 1 张") {
		t.Errorf("头部未说明只有 1 张计入合计（3 张里 1 张 pending、1 张 failed），"+
			"读者会以为 3500/100 覆盖了全部 3 张；实际头部：%q", head)
	}
	// 合计必须只等于已落盘的那张。
	if !strings.Contains(string(data), "合计金额 **100.00**") {
		t.Errorf("合计应为 100.00（pending/failed 不计入）：\n%s", data)
	}
	t.Logf("头部实测：%s", head)
}
