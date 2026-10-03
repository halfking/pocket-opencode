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
func TestBuildInvoiceSummaryDocs_CSVCarriesRequiredColumns(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	p := &Pipeline{Store: store, DataDir: t.TempDir()}

	csvPath, _, err := p.BuildInvoiceSummaryDocs(t.Context(), "u", "ws-cols")
	if err != nil {
		t.Fatalf("BuildInvoiceSummaryDocs: %v", err)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	header := firstLine(string(raw[3:]))
	want := []string{"费用类型", "对方单位", "金额", "币种", "发票号", "日期", "状态", "核验", "文件名", "来源邮件"}
	got := strings.Split(header, ",")
	if len(got) != len(want) {
		t.Fatalf("表头列数 = %d，want %d：%q", len(got), len(want), header)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 列 = %q, want %q（列序是给用户的契约，不能随意改）", i+1, got[i], want[i])
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
