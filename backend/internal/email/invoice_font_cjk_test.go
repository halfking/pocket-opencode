package email

// invoice_font_cjk_test.go —— 「有字体文件」不等于「有中文字形」。
//
// 起因（2026-10-02 实测）：FindChineseFont 此前只判「文件在、是 .ttf」，
// 于是任何 .ttf 都被接受。fpdf 的 AddUTF8Font 对「字体里没有这个字形」
// **不报错**——它照常输出 PDF，只是每个中文字符画成空白。实测：
//
//	arial.ttf  有中文字形=false  RenderInvoiceXMLPDF err=<nil>  → 产出 24623 字节 PDF
//	simhei.ttf 有中文字形=true   RenderInvoiceXMLPDF err=<nil>
//
// 而 RenderInvoiceXMLPDF 排版的那 7 行标签（发票号码/开票日期/费用类型/
// 销售方/购买方/价税合计/电子发票标题）**全是中文**，值也是中文。
// 也就是说这条路径会稳定产出一张「每格都是空的」的发票凭证，
// 并且因为不报错，harvest 记的是成功而不是 failed——比明确失败糟得多。
//
// 候选表里曾经还有一条 `/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf`
// 并注明「非中文兜底」：DejaVu 几乎存在于每个 Linux 容器，而真正的中文字体
// （fonts-arphic-uming）常常没装 ⇒ Linux 部署会稳定选中它。该条目已删除。
//
// 本文件分两层判据：与机器无关的一层锁住候选表，有本机字体的一层
// 锁住「选出来的字体一定有中文字形」这个不变量。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 非中文字体黑名单：这些字体在绝大多数发行版/容器里都存在，但没有中文字形。
// 出现在候选表里就是一个「稳定被选中并产出空白发票」的陷阱。
var knownNonCJKFonts = []string{
	"dejavusans.ttf", // Linux 容器标配
	"liberationsans-regular.ttf",
	"verdana.ttf",
	"tahoma.ttf",
	"arial.ttf",
}

// 候选表里不得出现黑名单字体。这条与机器无关，永远可判定。
func TestSystemFontCandidates_NoKnownNonCJKFallback(t *testing.T) {
	for _, p := range systemFontCandidates() {
		base := strings.ToLower(filepath.Base(p))
		for _, bad := range knownNonCJKFonts {
			if base == bad {
				t.Fatalf("候选表含无中文字形的字体 %q —— fpdf 对缺字形不报错，"+
					"会静默产出全空白的中文发票", p)
			}
		}
	}
}

// 本机上所有可解析的 .ttf 一起喂给 pickCJKFont，断言它选出来的那个
// **一定**有中文字形。
//
// 判据写成不变量而不是固定答案：候选表是跨平台的，在任何一台机器上
// 「应该选中哪一个」都不可移植，但「选中的必须有中文」永远成立。
// 若有人把 fontHasCJK 的检查去掉，本机同时存在中英文字体时它立刻转红。
func TestPickCJKFont_NeverReturnsNonCJKFont(t *testing.T) {
	all := collectHostTTFs(t)
	if len(all) == 0 {
		t.Skip("本机找不到任何 .ttf，跳过（无字形可判）")
	}
	var cjkCount int
	for _, f := range all {
		if fontHasCJK(f) {
			cjkCount++
		}
	}
	if cjkCount == 0 {
		t.Skip("本机没有含中文字形的字体，跳过")
	}
	got := pickCJKFont(all)
	if got == "" {
		t.Fatal("本机存在中文字体时 pickCJKFont 仍返回空 —— " +
			"意味着 XML 发票在本机也会恒记 failed")
	}
	if !fontHasCJK(got) {
		t.Fatalf("pickCJKFont 选中了没有中文字形的字体 %q（候选 %d 个，其中 %d 个含中文）",
			got, len(all), cjkCount)
	}
	t.Logf("候选 %d 个 .ttf，其中 %d 个含中文，选中 %s", len(all), cjkCount, got)
}

// 首个候选无中文、次候选有中文时，必须跳过前者选中后者。
// 这一条是负控的载体：把 fontHasCJK 的检查去掉，它必然转红。
func TestPickCJKFont_SkipsLatinOnlyEntry(t *testing.T) {
	latin, latinOK := firstExisting(knownNonCJKFonts)
	cjk, cjkOK := firstExisting([]string{"simhei.ttf", "Deng.ttf", "uming.ttf"})
	if !latinOK || !cjkOK {
		t.Skipf("需要一台同时有 %v 与 %v 的机器才能判定", knownNonCJKFonts, "simhei/Deng/uming")
	}
	got := pickCJKFont([]string{latin, cjk})
	if !fontHasCJK(got) {
		t.Fatalf("候选 [%s, %s] 选中了 %q；应跳过无中文的 %s 选中 %s",
			filepath.Base(latin), filepath.Base(cjk), got, latin, cjk)
	}
	if !strings.EqualFold(filepath.Base(got), filepath.Base(cjk)) {
		t.Fatalf("应选中 %s，实际选中 %s", cjk, got)
	}
}

// 显式指定一个无中文字形的字体时，FindChineseFont 必须当它不存在。
// 这条钉的是 env 变量这条最高优先级的路径——它过去是「无条件接受」。
func TestFindChineseFont_RejectsExplicitNonCJKFont(t *testing.T) {
	latin, ok := firstExisting(knownNonCJKFonts)
	if !ok {
		t.Skip("本机找不到可用的无中文字体夹具")
	}
	t.Setenv("POCKET_EMAIL_PDF_FONT_PATH", latin)
	if got := FindChineseFont(""); got != "" {
		t.Fatalf("显式指定无中文字形的 %s 仍被接受（返回 %q）—— "+
			"会静默产出全空白的中文发票", filepath.Base(latin), got)
	}
}

// 真有中文字体时必须被接受（否则就是矫枉过正：把所有 XML 发票都打成 failed）。
func TestFindChineseFont_AcceptsRealCJKFont(t *testing.T) {
	cjk, ok := firstExisting([]string{"simhei.ttf", "Deng.ttf", "uming.ttf"})
	if !ok {
		t.Skip("本机没有中文字体，跳过")
	}
	t.Setenv("POCKET_EMAIL_PDF_FONT_PATH", cjk)
	got := FindChineseFont("")
	if got == "" {
		t.Fatalf("显式指定含中文字形的 %s 却被拒绝", filepath.Base(cjk))
	}
	if !fontHasCJK(got) {
		t.Fatalf("FindChineseFont 返回的 %s 不含中文字形", got)
	}
	// 端到端一格：能选中还不够，还得真的渲染出 PDF。
	inv := &Invoice{Category: "交通", Seller: "某某出行", Amount: 42.5,
		InvoiceDate: "2026-09-30", InvoiceNo: "12345678", Subject: "行程单"}
	data, err := RenderInvoiceXMLPDF(got, inv, []byte("<Invoice/>"))
	if err != nil {
		t.Fatalf("用被接受的字体渲染失败: %v", err)
	}
	if !isPDFBytes(data) {
		t.Fatalf("渲染结果不是 pdf（font=%s）", got)
	}
}

// ---- 夹具辅助 ----

// firstExisting 在系统字体目录里找第一个存在的给定文件名。
func firstExisting(names []string) (string, bool) {
	for _, dir := range fontSearchDirs() {
		for _, n := range names {
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

// fontSearchDirs 覆盖本仓库部署到过的平台。macOS/Linux 的字体在 Windows 上
// 不存在，找不到就跳过对应断言——这是环境限制，如实跳过而不是假装判过。
func fontSearchDirs() []string {
	win := os.Getenv("SystemRoot")
	if win == "" {
		win = `C:\Windows`
	}
	return []string{
		filepath.Join(win, "Fonts"),
		"/usr/share/fonts/truetype",
		"/usr/share/fonts/truetype/dejavu",
		"/usr/share/fonts/truetype/arphic",
		"/System/Library/Fonts",
		"/System/Library/Fonts/Supplemental",
		"/Library/Fonts",
		"/system/fonts",
	}
}

// collectHostTTFs 收集本机所有 .ttf，用于不变量断言。
func collectHostTTFs(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for _, dir := range fontSearchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ttf") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
