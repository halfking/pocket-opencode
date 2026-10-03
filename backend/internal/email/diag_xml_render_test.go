package email

// diag_xml_render_test.go — **只读**诊断：需求「XML 数据格式（可解析后重新渲染）」
// 这条腿在**本机**到底能不能跑通。
//
// ## 为什么需要它
//
// 需求原文：「原邮件中有 PDF 下载地址（可直接下载已有 PDF），也有 XML 数据格式
// （可解析后重新渲染）」。附件/直链那条腿早就验过；XML 这条腿此前只有一句
// 「真实数据 0 条」就放过了。
//
// 而 `invoice_sources_e2e_test.go` 里有一条**如实 Skip**：
// 「环境缺中文字体 → XML 渲染按设计不可用」。`RenderInvoiceXMLPDF` 用
// go-pdf/fpdf 画版式，中文必须嵌 TTF（CoreFonts 不含 CJK）——「有没有字体」
// 是这条腿的硬前提。
//
// ## 判据 1：不能拿字面串搜 PDF 字节（我第一版就栽在这）
//
// fpdf 对**子集字体**是每个字形补一个前导空格（UTF-16 高位 0x00 显示成空格）：
//
//	BT 201.26 711.03 Td ( 2 4 3 1 7 2 0 0 0 0 9 0 7 0 1 2 6 9 8)Tj ET
//
// 搜 `24317200000907012698` 搜不到，**不代表没渲染**。
// 中文更搜不到——它是子集字形索引，不是可读文本。
//
// ## 判据 2：用**差分**回答「字段有没有被写进去」
//
// 同一字体渲染「有字段」与「空 Invoice」两份，比字节数与文本算子数。
// 差分与「字体真的可用」无关，因此不会被编码方式骗过——
// 这正是「已渲染 vs 全空白」唯一可靠的区分方式。
//
// ## 只读
//
// 只读 dataDir 与系统字体；输出只在内存与 t.TempDir()。不连库。
// 门控 POCKET_DIAG_XML_RENDER=1。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var reShowOp = regexp.MustCompile(`(?s)(TJ|Tj)`)

func TestDiagXMLRender(t *testing.T) {
	if os.Getenv("POCKET_DIAG_XML_RENDER") != "1" {
		t.Skip("set POCKET_DIAG_XML_RENDER=1 to run (read-only)")
	}
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dataDir == "" {
		t.Fatal("POCKET_REAL_DATA_DIR 必须显式给全，不设缺省值")
	}

	// ---- 前提 1：字体必须真能探测到 ----
	font := FindChineseFont(dataDir)
	t.Logf("FindChineseFont(%q) = %q", dataDir, font)
	if font == "" {
		t.Logf("  env POCKET_EMAIL_PDF_FONT_PATH = %q", os.Getenv("POCKET_EMAIL_PDF_FONT_PATH"))
		if _, err := os.Stat(filepath.Join(dataDir, "fonts")); err == nil {
			names, _ := os.ReadDir(filepath.Join(dataDir, "fonts"))
			t.Logf("  dataDir/fonts 存在，含 %d 个条目", len(names))
		} else {
			t.Logf("  dataDir/fonts 不存在（%v）", err)
		}
		t.Fatal("找不到中文字体 ⇒ XML 重渲染这条腿在本机不可用，" +
			"harvestOne 的 XML 分支会恒记 failed。修法：放一份 CJK ttf 到 " +
			"data/fonts/ 或设 POCKET_EMAIL_PDF_FONT_PATH")
	}
	if fi, serr := os.Stat(font); serr == nil {
		t.Logf("  字体 %.1f MB", float64(fi.Size())/(1<<20))
	}

	// ---- 前提 2：渲染两份做差分 ----
	const no = "24317200000907012698"
	const day = "2026-09-28"
	full := &Invoice{
		Category: "其他", Seller: "云服务开票中心", Amount: 1280.00,
		Currency: "CNY", InvoiceNo: no, InvoiceDate: day,
	}
	rf, err := RenderInvoiceXMLPDF(font, full, []byte(e2eInvoiceXML))
	if err != nil {
		t.Fatalf("RenderInvoiceXMLPDF(有字段) 失败: %v", err)
	}
	re, err := RenderInvoiceXMLPDF(font, &Invoice{}, nil)
	if err != nil {
		t.Fatalf("RenderInvoiceXMLPDF(空) 失败: %v", err)
	}
	if len(rf) < 1024 {
		t.Fatalf("产物仅 %d 字节，明显不是一张 A4", len(rf))
	}

	infF := inflateAllStreams(rf)
	infE := inflateAllStreams(re)
	nF, nE := len(reShowOp.FindAllString(infF, -1)), len(reShowOp.FindAllString(infE, -1))
	t.Logf("  有字段 %d 字节 / %d 个文本算子    空 %d 字节 / %d 个文本算子",
		len(rf), nF, len(re), nE)

	// 差分判据：字段必须真的被画进页面。fpdf 对「字体缺某字形」不报错、
	// 只画空白，所以「函数没报错」完全不能证明产物可用。
	if len(rf) <= len(re) {
		t.Errorf("有字段的产物(%d B) 不大于空的(%d B)：字段很可能没被写进去",
			len(rf), len(re))
	}
	if nF <= nE {
		t.Errorf("有字段的文本算子数(%d) 不多于空的(%d)：字段很可能没被画出来",
			nF, nE)
	}

	// 去分隔符后查数字。fpdf 子集字体每个字形写**两个字节**：高字节是
	// NUL(0x00)、低字节才是字形。终端里 NUL 显示成空，看起来像「空格」，
	// 但 strings.Fields 不把 NUL 当空白 —— 只去空格是搜不到的（我第一版就栽在这）。
	flat := strings.NewReplacer("\x00", "", " ", "", "\n", "", "\r", "", "\t", "").Replace(infF)
	digitsOK := true
	for _, want := range []string{no, day, "1280.00"} {
		if !strings.Contains(flat, want) {
			digitsOK = false
			t.Errorf("去分隔符后的内容流里找不到 %q", want)
		}
	}

	// 中文**不按文本断言**：它是子集字形索引（实测 `u5[PS…` 这类字节），
	// 无法按字符串搜。中文可用性由 fontHasCJK + 上述差分共同保证。
	t.Logf("  中文以子集字形索引写入内容流（不按文本断言，可用性由 fontHasCJK 保证）")
	if digitsOK && len(rf) > len(re) && nF > nE {
		t.Logf("  结论：XML → 重新渲染 这条腿在本机**可用**")
	} else {
		t.Logf("  结论：**尚不能判定可用**（见上面的断言）")
	}
}
