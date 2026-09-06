package email

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gofpdf "github.com/go-pdf/fpdf"
)

// invoice_pdf.go — XML 发票数据重渲染为 PDF（对应需求「XML 数据格式可解析
// 后重新渲染」）。
//
// 用 go-pdf/fpdf 画一张 A4 简版发票版式。中文渲染必须嵌入 TTF 字体
// （fpdf 的 CoreFonts 不含 CJK）；字体按以下优先级探测，全部落空时渲染
// 能力降级为不可用（Harvest 把 XML 路径记 failed，不影响附件/直链下载）：
//  1. POCKET_EMAIL_PDF_FONT_PATH 显式指定
//  2. <dataDir>/fonts/*.ttf（部署方放置，Docker 镜像可选层）
//  3. 常见系统字体路径（macOS / Linux）

// FindChineseFont 探测可用的中文字体文件。dataDir 可为空。
// 返回路径已解析符号链接（fpdf 不跟随 /Library/Fonts 下的 symlink）。
func FindChineseFont(dataDir string) string {
	if p := strings.TrimSpace(os.Getenv("POCKET_EMAIL_PDF_FONT_PATH")); p != "" {
		if resolved, ok := resolveFontFile(p); ok {
			return resolved
		}
	}
	if dataDir != "" {
		if matches, _ := filepath.Glob(filepath.Join(dataDir, "fonts", "*.ttf")); len(matches) > 0 {
			if resolved, ok := resolveFontFile(matches[0]); ok {
				return resolved
			}
		}
	}
	candidates := []string{
		// macOS（fpdf 只吃 ttf；ttc 不支持，不列）
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/Library/Fonts/Arial Unicode.ttf",
		// Linux（常见发行版包）
		"/usr/share/fonts/truetype/arphic/uming.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", // 非中文兜底：至少数字/拉丁可用
	}
	for _, p := range candidates {
		if resolved, ok := resolveFontFile(p); ok {
			return resolved
		}
	}
	return ""
}

// resolveFontFile 确认是常规文件（含 symlink 目标解析）且为 .ttf。
func resolveFontFile(p string) (string, bool) {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(p), ".ttf") {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p, true
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// RenderInvoiceXMLPDF 把发票字段渲染为 A4 单页 PDF 字节。
// xmlRaw 保留参数供未来渲染逐行商品明细（当前只排版结构化字段）。
func RenderInvoiceXMLPDF(fontPath string, inv *Invoice, xmlRaw []byte) ([]byte, error) {
	if fontPath == "" {
		return nil, fmt.Errorf("中文字体不可用：请设置 POCKET_EMAIL_PDF_FONT_PATH 或放置字体到 <dataDir>/fonts/")
	}
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()
	// fpdf 的 AddUTF8Font 会 path.Join(fontDir, file)：fontDir 为空时默认 "."，
	// 绝对路径会被 Clean 掉前导斜杠。因此显式 SetFontLocation + 基名。
	pdf.SetFontLocation(filepath.Dir(fontPath))
	pdf.AddUTF8Font("cjk", "", filepath.Base(fontPath))
	if pdf.Err() {
		return nil, fmt.Errorf("load font %s: %s", fontPath, pdf.Error().Error())
	}

	pdf.SetFont("cjk", "", 16)
	pdf.CellFormat(0, 12, "电子发票（系统重渲染）", "", 1, "C", false, 0, "")
	pdf.SetFont("cjk", "", 9)
	pdf.CellFormat(0, 6, "由邮件 XML 数据解析生成，用于凭证归档；版式与原票可能存在差异", "", 1, "C", false, 0, "")
	pdf.Ln(4)

	amount := fmt.Sprintf("¥ %.2f", inv.Amount)
	rows := [][2]string{
		{"发票号码", inv.InvoiceNo},
		{"开票日期", inv.InvoiceDate},
		{"费用类型", inv.Category},
		{"销售方（对方单位）", inv.Seller},
		{"购买方（抬头）", inv.Title},
		{"价税合计", amount},
		{"来源邮件", truncateRunes(inv.Subject, 60)},
	}
	for _, r := range rows {
		pdf.SetFont("cjk", "", 11)
		pdf.CellFormat(52, 10, r[0], "1", 0, "L", false, 0, "")
		pdf.SetFont("cjk", "", 11)
		pdf.CellFormat(0, 10, r[1], "1", 1, "L", false, 0, "")
	}

	pdf.Ln(6)
	pdf.SetFont("cjk", "", 9)
	pdf.CellFormat(0, 6, fmt.Sprintf("生成时间 %s · OpenPocket 邮件发票整理",
		time.Now().Format("2006-01-02 15:04")), "", 1, "L", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func truncateRunes(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
