package email

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gofpdf "github.com/go-pdf/fpdf"
	"golang.org/x/image/font/sfnt"
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
//
// 「可用」= 文件在、是 .ttf，**且真的有中文字形**。最后一条是 2026-10-02
// 补的：此前只判文件属性，于是任何 .ttf 都会被接受，而 fpdf 的
// AddUTF8Font 对「字体里没有这个字形」**不报错**——它照常输出 PDF，
// 只是每个中文字符都画成空白。产出一张只有空表格的「发票」，
// 比明确失败（harvest 记 failed、可见可重试）糟糕得多。
func FindChineseFont(dataDir string) string {
	if p := strings.TrimSpace(os.Getenv("POCKET_EMAIL_PDF_FONT_PATH")); p != "" {
		// 显式指定时不静默回退。POCKET_EMAIL_PDF_FONT_PATH 是操作员的明确
		// 选择：若指定了却因为缺中文字形而被悄悄换成系统里的另一款字体，
		// 那就是「配了不生效」——而它不会报错，只会让排版与预期不一致。
		// 返回空 ⇒ RenderInvoiceXMLPDF 报出可操作的错误 ⇒ harvest 记 failed。
		if resolved, ok := resolveFontFile(p); ok && fontHasCJK(resolved) {
			return resolved
		}
		return ""
	}
	if dataDir != "" {
		if matches, _ := filepath.Glob(filepath.Join(dataDir, "fonts", "*.ttf")); len(matches) > 0 {
			if resolved, ok := resolveFontFile(matches[0]); ok && fontHasCJK(resolved) {
				return resolved
			}
		}
	}
	return pickCJKFont(systemFontCandidates())
}

// pickCJKFont 按候选顺序返回第一个**既有中文字形**的字体；全落空返回 ""。
//
// 抽出来是为了能直接喂任意列表做判据：候选表是跨平台的，在本机上
// 大部分条目根本不存在，「从表里选」这件事在多数机器上无法证伪。
func pickCJKFont(candidates []string) string {
	for _, p := range candidates {
		if resolved, ok := resolveFontFile(p); ok && fontHasCJK(resolved) {
			return resolved
		}
	}
	return ""
}

// fontHasCJK 报告字体是否真的含中文字形。
//
// 判据读 TTF 的 cmap（sfnt.GlyphIndex），不是看文件名也不看字符串宽度——
// 前者对 arial.ttf 一样「通过」，后者在本仓库用的 fpdf 版本上对 UTF8 字体
// 恒返回 0，连 "AAAA" 都是 0（两次都实测过，都不能当判据）。
//
// 探针字符取常见字；任一命中即算有中文覆盖。GlyphIndex 返回 0 表示
// .notdef，必须连错误一起判——只判 err==nil 会把「字体里根本没有这个字」
// 误判成命中（arial.ttf 会全绿）。
var cjkProbeRunes = []rune{'中', '国', '发', '票', '金', '额'}

// fontHasCJK 读取失败或解析失败一律返回 false：探测失败就当不可用，
// 让调用方走「记 failed」这条看得见的路，而不是赌一把。
func fontHasCJK(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	f, err := sfnt.Parse(raw)
	if err != nil {
		return false
	}
	for _, r := range cjkProbeRunes {
		if gi, gerr := f.GlyphIndex(&sfnt.Buffer{}, r); gerr == nil && gi != 0 {
			return true
		}
	}
	return false
}

// systemFontCandidates 返回各平台常见中文字体候选（按优先级）。
//
// 只列 .ttf：go-pdf/fpdf 的 AddUTF8Font 不支持 .ttc 字体集合，而 Windows 的
// 微软雅黑（msyh.ttc）、宋体（simsun.ttc）恰好都是 ttc，所以这里用同为中文
// 可用的黑体（simhei.ttf）/等线（Deng.ttf）。之前候选表只有 macOS/Linux，
// 在 Windows 开发机和 Android 设备上一律探测失败 ⇒ XML 发票重渲染恒降级为
// failed（harvestOne 的 XML 分支），这是「XML 发票拿不到」的环境级原因。
//
// 2026-10-02 追加：候选表里曾有 `/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf`
// 并注明「非中文兜底」。那是**反向陷阱**——DejaVu 几乎存在于每个 Linux 容器，
// 而真正的中文字体（fonts-arphic-uming）常常没装，于是 Linux 部署会稳定选中它；
// 而 fpdf 对「字体没有该字形」不报错，照样输出一张全空白的 PDF。
// 探测已改为要求真有中文字形（见 fontHasCJK），该条目也一并删除：留着一个
// 必定被拒的条目，只会让下一个人以为「表里有它就等于能用」。
func systemFontCandidates() []string {
	winDir := os.Getenv("SystemRoot") // 通常是 C:\Windows
	if winDir == "" {
		winDir = `C:\Windows`
	}
	return []string{
		// macOS（fpdf 只吃 ttf；ttc 不支持，不列）
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/Library/Fonts/Arial Unicode.ttf",
		// Linux（常见发行版包）
		"/usr/share/fonts/truetype/arphic/uming.ttf",
		// Windows：黑体/等线/仿宋/楷体都是独立 ttf，含完整 CJK
		filepath.Join(winDir, "Fonts", "simhei.ttf"),
		filepath.Join(winDir, "Fonts", "Deng.ttf"),
		filepath.Join(winDir, "Fonts", "simfang.ttf"),
		filepath.Join(winDir, "Fonts", "simkai.ttf"),
		filepath.Join(winDir, "Fonts", "NotoSansSC-VF.ttf"),
		// Android（pocketd 跑在设备上时）：DroidSansFallback 是 CJK 兜底 ttf；
		// 系统里的 NotoSansCJK 只有 ttc/.otf，fpdf 都不吃，不列。
		"/system/fonts/DroidSansFallback.ttf",
	}
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
