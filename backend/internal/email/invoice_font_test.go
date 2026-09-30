package email

// invoice_font_test.go — 中文字体探测的平台覆盖。
//
// BUG-AN：systemFontCandidates 只列了 macOS/Linux 路径，于是 Windows 开发机
// 和 Android 设备上 FindChineseFont 一律返回空 → RenderInvoiceXMLPDF 直接
// 报错 → harvestOne 的 XML 分支恒为 failed。「XML 发票拿不到」有一半是
// 环境问题，不是邮件本身的问题。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 候选表必须覆盖本仓主要部署形态：C:\Windows（开发/桌面）、/system/fonts（Android）。
func TestSystemFontCandidates_CoversWindowsAndAndroid(t *testing.T) {
	cands := systemFontCandidates()
	joined := strings.Join(cands, "|")
	for _, want := range []string{`\Fonts\simhei.ttf`, "/system/fonts/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("systemFontCandidates missing %q: %v", want, cands)
		}
	}
	// Windows 路径要用 SystemRoot 而不是硬编码盘符：服务跑在 D:\ 或容器里也会变。
	t.Setenv("SystemRoot", `D:\Win`)
	if got := systemFontCandidates(); !strings.Contains(strings.Join(got, "|"), `D:\Win\Fonts\simhei.ttf`) {
		t.Fatalf("SystemRoot not honored: %v", got)
	}
}

// 候选里出现的每个路径都必须能被 resolveFontFile 接受（.ttf 且存在），
// 否则是死条目——曾经就有一条 wqy-zenhei.ttc 挂在表里永远匹配不上。
func TestSystemFontCandidates_NoUnusableEntries(t *testing.T) {
	for _, p := range systemFontCandidates() {
		if !strings.EqualFold(filepath.Ext(p), ".ttf") {
			t.Fatalf("candidate %q is not .ttf; fpdf cannot load it", p)
		}
		if _, err := os.Stat(p); err == nil {
			continue // 本机存在即有效
		}
		// 本机不存在是正常的（跨平台候选表），只要求路径形状合理。
		if strings.TrimSpace(p) == "" {
			t.Fatalf("empty candidate path in list")
		}
	}
}

// 有字体时必须真的渲染出合法 PDF（端到端一格，不只是"找到文件"）。
func TestFindChineseFont_ThenRenderXMLInvoice(t *testing.T) {
	font := FindChineseFont(t.TempDir())
	if font == "" {
		t.Skip("no CJK font available on this host")
	}
	inv := &Invoice{Category: "交通", Seller: "某某出行", Amount: 42.5,
		InvoiceDate: "2026-09-30", InvoiceNo: "12345678", Subject: "行程单"}
	data, err := RenderInvoiceXMLPDF(font, inv, []byte("<Invoice/>"))
	if err != nil {
		t.Fatalf("render failed with detected font %s: %v", font, err)
	}
	if !isPDFBytes(data) {
		t.Fatalf("render output is not a pdf (font=%s)", font)
	}
}
