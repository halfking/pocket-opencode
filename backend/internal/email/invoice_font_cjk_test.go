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
	cjk, cjkOK := firstExisting(cjkFontNames())
	if !latinOK || !cjkOK {
		t.Skipf("需要一台同时有 %v 与 %v 的机器才能判定", knownNonCJKFonts, cjkFontNames())
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
	cjk, ok := firstExisting(cjkFontNames())
	if !ok {
		t.Skipf("本机找不到任一中文字体夹具（试过 %v），跳过", cjkFontNames())
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

// ---- <dataDir>/fonts 这一级：目录里有多款字体时必须挑到有中文的那款 ----
//
// 这一级此前没有任何测试跑过。已用 grep 核对（2026-10-15）：全仓 8 处
// `FindChineseFont(...)` 调用点里，5 处传 `t.TempDir()`——那是**空目录**，
// glob 返回 0 条；剩下 3 处传 ""，dataDir 分支根本不进；唯一传真实
// dataDir 的 diag_xml_render_test.go 被 `POCKET_DIAG_XML_RENDER=1` 门控
// 且要求 `POCKET_REAL_DATA_DIR`。⇒ `invoice_pdf.go:46` 那行只试
// `matches[0]` 的代码，在测试里从未被执行过一次。
//
// 而 RenderInvoiceXMLPDF 的报错文案（invoice_pdf.go:171）恰恰把这一级
// 当成给操作员的正式补救手段：「请设置 POCKET_EMAIL_PDF_FONT_PATH 或
// 放置字体到 <dataDir>/fonts/>」。

// 在 dataDir/fonts 下造出多款字体，返回该 dataDir。
// latinName 必须排在 cjkName 之前（filepath.Glob 按字典序返回），
// 这样「只试 matches[0]」与「逐个试」两条路就会给出不同答案。
func dataDirWithFonts(t *testing.T, latinName, cjkName string) string {
	t.Helper()
	latin, ok := firstExisting(knownNonCJKFonts)
	if !ok {
		t.Skipf("本机找不到无中文字体夹具（试过 %v），跳过", knownNonCJKFonts)
	}
	cjk, ok := firstExisting(cjkFontNames())
	if !ok {
		t.Skipf("本机找不到中文字体夹具（试过 %v），跳过", cjkFontNames())
	}
	dir := filepath.Join(t.TempDir(), "fonts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("造 fonts 目录失败: %v", err)
	}
	for _, c := range []struct{ src, name string }{{latin, latinName}, {cjk, cjkName}} {
		raw, err := os.ReadFile(c.src)
		if err != nil {
			t.Fatalf("读夹具 %s 失败: %v", c.src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name), raw, 0o644); err != nil {
			t.Fatalf("写夹具 %s 失败: %v", c.name, err)
		}
	}
	return filepath.Dir(dir)
}

// 目录里同时有无中文与有中文两款字体、且无中文那款字典序在前时，
// 必须选中后者。这正是 glob 只试 matches[0] 会漏掉的一格。
func TestFindChineseFont_DataDirPicksCJKNotFirstMatch(t *testing.T) {
	t.Setenv("POCKET_EMAIL_PDF_FONT_PATH", "") // 只测 dataDir 这一级
	dataDir := dataDirWithFonts(t, "Arial.ttf", "SourceHanSansSC.ttf")

	// 前置断言：确认本用例真的造出了「matches[0] 是无中文那款」的目录。
	// 没有它，若将来 glob 排序变了，这个用例会静默退化成测另一件事。
	matches, err := filepath.Glob(filepath.Join(dataDir, "fonts", "*.ttf"))
	if err != nil || len(matches) != 2 {
		t.Fatalf("前置失败：glob 应返回 2 条，实际 %d 条（err=%v）", len(matches), err)
	}
	if !strings.EqualFold(filepath.Base(matches[0]), "Arial.ttf") {
		t.Fatalf("前置失败：matches[0] 应是无中文的 Arial.ttf，实际 %s —— "+
			"本用例依赖字典序，换名字就会测不到目标分支", filepath.Base(matches[0]))
	}
	if fontHasCJK(matches[0]) {
		t.Fatalf("前置失败：夹具 Arial.ttf 竟被判定为含中文字形")
	}

	got := FindChineseFont(dataDir)
	if got == "" {
		t.Fatalf("dataDir/fonts 里明明有含中文字形的 %s，FindChineseFont 却返回空 "+
			"⇒ XML 发票在本机恒记 failed", filepath.Join(dataDir, "fonts", "SourceHanSansSC.ttf"))
	}
	if !strings.EqualFold(filepath.Base(got), "SourceHanSansSC.ttf") {
		t.Fatalf("应选中部署方放在 dataDir/fonts 里的 SourceHanSansSC.ttf，实际选中 %q "+
			"⇒ 只试了 matches[0]（%s 无中文字形），把部署方放的字体当不存在，"+
			"静默改用系统字体", got, filepath.Base(matches[0]))
	}
}

// 负控：dataDir/fonts 里只有一款无中文字体时，绝不能把它选中。
// 它防的是「修过头」——把 matches[0] 无条件返回、或把 fontHasCJK
// 从这一级摘掉，两种改法都会让本条转红，而上面那条仍可能绿。
func TestFindChineseFont_DataDirRejectsLatinOnlyDir(t *testing.T) {
	t.Setenv("POCKET_EMAIL_PDF_FONT_PATH", "")
	dataDir := dataDirWithFonts(t, "Arial.ttf", "SourceHanSansSC.ttf")
	// 只留无中文那款
	dir := filepath.Join(dataDir, "fonts")
	if err := os.Remove(filepath.Join(dir, "SourceHanSansSC.ttf")); err != nil {
		t.Fatalf("移除 CJK 夹具失败: %v", err)
	}
	got := FindChineseFont(dataDir)
	if got != "" && !fontHasCJK(got) {
		t.Fatalf("dataDir/fonts 只有无中文字形时选中了 %q —— "+
			"fpdf 对缺字形不报错，会静默产出全空白的中文发票", got)
	}
	// 允许的两种结局：回退到系统里的中文字体，或如实返回空。
	if got != "" {
		t.Logf("回退到系统字体 %s（本机有中文字体，预期行为）", got)
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

// cjkFontNames 是「本机可用的中文字体」夹具名清单。
//
// 2026-10-15 补 macOS 两条，依据是实测读数而不是推测：本机
// `TestPickCJKFont_NeverReturnsNonCJKFont` 打出「候选 205 个 .ttf，
// 其中 4 个含中文，选中 /System/Library/Fonts/Supplemental/AppleGothic.ttf」
// —— 也就是说这台 mac 明确有含中文字形的字体，而原清单
// （simhei / Deng / uming，全是 Windows 与 Linux 的名字）在这台机器上
// 一个都找不到，于是下面两条测试**恒 skip**：
//
//	--- SKIP: TestPickCJKFont_SkipsLatinOnlyEntry  「需要一台同时有 … 的机器才能判定」
//	--- SKIP: TestFindChineseFont_AcceptsRealCJKFont「本机没有中文字体，跳过」
//
// 第二条那句 skip 理由本身就是假的（本机有 4 款）。更要紧的是
// TestPickCJKFont_SkipsLatinOnlyEntry 的注释写着「这一条是负控的载体：
// 把 fontHasCJK 的检查去掉，它必然转红」——而它在 macOS 上从未运行过，
// 负控在开发机上一直是哑的。
func cjkFontNames() []string {
	return []string{
		"simhei.ttf", "Deng.ttf", "uming.ttf", // Windows / Linux
		"Arial Unicode.ttf", // /Library/Fonts，macOS
		"AppleGothic.ttf",   // /System/Library/Fonts/Supplemental，macOS
	}
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
