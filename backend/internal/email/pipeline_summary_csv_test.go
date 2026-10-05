package email

// pipeline_summary_csv_test.go — 需求 3 交付物的编码与内容守卫。
//
// ## 为什么先查这个
//
// 需求 3 明确要「整理一个列表，记录必要信息并汇总金额」，而这份列表是
// `BuildInvoiceSummaryDocs` 落盘的 CSV。2026-10-02 对**真实产物**做检查时
// 读到：文件首 3 字节 = E8 B4 B9（"费" 的 UTF-8 前三字节）——**没有 BOM**。
// 中文 Windows 的 Excel 打开无 BOM 的 UTF-8 CSV 会按 GBK 解码，整表中文变乱码。
// 拿打不开的列表去对账，等于这份交付物没做。
//
// Markdown 不加 BOM：它不由 Excel 打开，BOM 只会在第一行前多出不可见字符。

import (
	"os"
	"strings"
	"testing"
)

// TestBuildInvoiceSummaryDocs_CSVStartsWithUTF8BOM CSV 必须以 UTF-8 BOM 开头。
func TestBuildInvoiceSummaryDocs_CSVStartsWithUTF8BOM(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	p := &Pipeline{Store: store, DataDir: t.TempDir()}

	csvPath, _, err := p.BuildInvoiceSummaryDocs(t.Context(), "u", "ws-bom")
	if err != nil {
		t.Fatalf("BuildInvoiceSummaryDocs: %v", err)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(raw) < 3 || string(raw[:3]) != utf8BOM {
		t.Errorf("CSV 没有 UTF-8 BOM（首 3 字节 = % X）；Excel 会按 GBK 解码，中文全变乱码",
			raw[:min(3, len(raw))])
	}
	// BOM 之后必须紧跟表头，且表头第一个字段名不能被 BOM 污染。
	rest := string(raw[3:])
	if !strings.HasPrefix(rest, "费用类型,") {
		t.Errorf("BOM 之后不是表头，实际开头 = %q", firstLine(rest))
	}
}

// TestBuildInvoiceSummaryDocs_MDHasNoBOM Markdown 不得带 BOM。
func TestBuildInvoiceSummaryDocs_MDHasNoBOM(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	p := &Pipeline{Store: store, DataDir: t.TempDir()}

	_, mdPath, err := p.BuildInvoiceSummaryDocs(t.Context(), "u", "ws-bom")
	if err != nil {
		t.Fatalf("BuildInvoiceSummaryDocs: %v", err)
	}
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if strings.HasPrefix(string(raw), utf8BOM) {
		t.Error("Markdown 不该带 BOM：它不由 Excel 打开，BOM 只会在首行前留下不可见字符")
	}
	if !strings.HasPrefix(string(raw), "# 发票汇总") {
		t.Errorf("MD 开头不是标题，实际 = %q", firstLine(string(raw)))
	}
}

// TestBuildInvoiceSummaryDocs_CSVCarriesRequiredColumns 需求 3 要求的字段必须在表头里。
//
// 这是对「真实产物」的形状断言，不是对纯函数的断言——CSV 的列序是给用户
// 看的契约（下游有人按下标取第 8 列的金额，见 pipeline.go 合计行那段注释）。
//
// ## 为什么不走 newWorkspaceTestStore（2026-10-05 改）
//
// 这条用例原来经 `newWorkspaceTestStore` 拿 Store，于是被 `POCKET_TEST_POSTGRES_DSN`
// 门禁罩住：**连不上库时它整条 SKIP，而它恰好是唯一能发现「表头列数与代码不一致」
// 的用例**。实测代价：2026-10-05 加「备注」列那轮（cd1feae2）同时改了三个别的
// 测试文件（md_columns / ledger / total_parity，都已按 11 列更新），唯独漏了这里；
// 因为那几轮连不上库，这条从未被执行，于是「main 全绿」与「email 包在真库下 FAIL」
// 同时成立。绿是门禁给的，不是行为给的。
//
// 表头契约只由 `WriteInvoiceSummaryDocs` 决定，而这个函数吃的是切片、不碰库。
// 改成直接调它 ⇒ 这条护栏在**任何环境**（含无库的 CI）都执行。
func TestBuildInvoiceSummaryDocs_CSVCarriesRequiredColumns(t *testing.T) {
	csvPath, _, err := WriteInvoiceSummaryDocs(t.TempDir(), "ws-cols", []Invoice{{
		Category: "其他", Seller: "某公司", Amount: 1, Currency: "CNY",
		Status: "downloaded", FilePath: "a.pdf", FileName: "a.pdf",
	}})
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	header := firstLine(string(raw[3:]))
	// 11 列，末列「备注」是 2026-10-05 加的（人工标注理由原文）。必须与
	// ledger.go 的飞书表头、invoice_summary_md_columns_test.go 的
	// wantSharedWidth=11 一致——三处任一漂移，用户手里的两份额外清单就对不上。
	want := []string{"费用类型", "对方单位", "金额", "币种", "发票号", "日期", "状态", "核验", "文件名", "来源邮件", "备注"}
	got := strings.Split(header, ",")
	if len(got) != len(want) {
		t.Fatalf("表头列数 = %d，want %d：%q", len(got), len(want), header)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 列 = %q, want %q（列序是给用户的契约，不能随意改）", i+1, got[i], want[i])
		}
	}

	// 表头变宽而写入范围没跟上时，多出来的那列会**静默丢掉**
	// （纯文本输出没有任何症状；飞书侧同类缺陷见
	// TestInvoiceTotalParity_ColumnCountMatchesHeader）。这里对 CSV 做同形检查：
	// 每行列数都必须等于**实际读到的表头**列数。
	lines := strings.Split(strings.TrimRight(string(raw[3:]), "\r\n"), "\n")
	for i, ln := range lines {
		if n := len(strings.Split(strings.TrimSuffix(ln, "\r"), ",")); n != len(got) {
			t.Errorf("第 %d 行列数 = %d，表头 %d 列：%q", i+1, n, len(got), ln)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
