package email

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// diag_a4_ink_overflow_test.go —— **只读**诊断：磁盘上那批 A4 产物里，
// **墨迹本身**有没有越过格子裁切线。
//
// ## 它补的是哪个洞
//
// round37 第九节证明了「页框出格」并修掉了根因，但明确留了一个尾巴：
// 页框出格**不能**证明墨迹也出格 —— 票面右侧可能整条都是白边。
// 当时的理由是「判墨迹要栅格化，而 pdfcpu v0.11 没有渲染 API」。
//
// **那个理由不成立。** 墨迹的位置可以从内容流算出来：解析算子、
// 维护 CTM 栈与文本矩阵，把每个绘制变换到 A4 页空间即可。
// 本机也确实没有 mutool/pdftoppm/gs/PyMuPDF（2026-10-04 实测），
// 但**栅格化从来不是必需的**，只是当时手上没有别的路。
//
// ## 与 diag_a4_cell_fit_test.go 的分工（别混用两者的结论）
//
//	cell_fit 判**页框** ⊆ 格子 —— 「墨迹不越界」的**充分条件**。
//	本文件判**墨迹**   ⊆ 格子 —— 必要且充分的那一半。
//
// 页框出格 + 墨迹不出格 = 真有白边，安全；页框出格 + 墨迹出格 = 真撞了。
//
// ## 门禁
//
// POCKET_DIAG_A4_INK=1 + POCKET_DIAG_EXPORT_DIR（**磁盘上真实存在的** A4 目录）。
// 无缺省值：不显式传入时只会扫到空目录、输出「一切正常」的假结论。
//
// ## 负控
//
// TestA4InkOverflowCheckerOnSyntheticGrid 用**自建**的最小 A4 PDF 走完整条
// 链路（tokenizer → CTM 栈 → form XObject 放置 → 归格 → 判定），四个用例：
//
//	C-inside       缩放 0.5 恰好铺满格子          ⇒ 必须判「未越界」且墨迹非空
//	C-pastRight    缩放 0.559037 越过右界(0,0)   ⇒ 必须判「越界」
//	C-pastInCell11 同样越界但落在 (1,1)          ⇒ 必须判「越界」且格号=11
//	C-thin         缩放略小于铺满但**墨迹**仍在格内 ⇒ 必须判「未越界」
//
// 0.559037 是 round37 实测的坏缩放。它在**两个轴**上都溢出：横向
// 0.559037×595.2756 = 332.79 比格子宽 297.64 超出 **35.15pt**，
// 纵向 0.559037×841.8898 = 470.66 比格子高 420.94 超出 **49.72pt**
// ⇒ 期望的越界深度取两者的较大值 49.72，不是 35.15。
// 第一版只按横向写期望，判据报的 49.67 被我当成「判据算错了」——
// 实际上是夹具的期望值没算全。**先怀疑自己的期望值。**
//
// 后两条是**鉴别性**用例：它们证明判据既没恒判越界、也没恒判未越界，
// 且归格逻辑不是硬编码第 0 格（C-inside 恒绿的话只有 C-pastInCell11 会红）。

// ------------------------------------------------------------------ 夹具

// inkFixturePDF 造一张最小的 A4 PDF：可选地在指定格子放一个 form XObject。
// formMatrix 决定页框落在哪；formBody 是该 form 的内容流。
// 每个 form 自带一个 Helvetica（/FirstChar 32 + /Widths），
// 因为**文字宽度路径必须有覆盖**——第一版控制矩阵里一个字都没有，
// 把 hScale 置 0（所有文字宽度恒为 0）判据依然全绿。
func inkFixturePDF(a4w, a4h float64, forms []inkFixtureForm) []byte {
	// 对象号：1 Catalog / 2 Pages / 3 Page / 4 PageContent，
	// 之后每个 form 两项（dict + 无 filter 的 stream 合并成一项），
	// 再之后每个 form 一个字体。
	const firstFormObj = 5
	var objs []string
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>")
	objs = append(objs, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")

	xobjRes := ""
	for i := range forms {
		xobjRes += fmt.Sprintf("/Fm%d %d 0 R ", i, firstFormObj+i)
	}
	objs = append(objs, fmt.Sprintf(
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources << /XObject << %s>> >> /Contents 4 0 R >>",
		fTrim(a4w), fTrim(a4h), xobjRes))
	objs = append(objs, streamObj(forms[0].pageBody))

	fontObj := func(i int) int { return firstFormObj + len(forms) + i }
	// nestedRefTo：form i 的内容会 Do 这个下标的 form（用来造「票面内部还有
	// 嵌套 form」的真实形态）。返回该 form 的 XObject 名字与对象号。
	nestedName := func(i int) (string, int) {
		return fmt.Sprintf("Fn%d", i), firstFormObj + i
	}
	for i, f := range forms {
		fontRes := fmt.Sprintf("/Font << /F1 %d 0 R >> ", fontObj(i))
		if f.nestedRef >= 0 {
			nm, obj := nestedName(f.nestedRef)
			fontRes += fmt.Sprintf("/XObject << /%s %d 0 R >> ", nm, obj)
		}
		objs = append(objs, fmt.Sprintf(
			"<< /Type /XObject /Subtype /Form /BBox [0 0 %s %s] /Matrix [%s] /Resources << %s>> /Length %d >>\nstream\n%s\nendstream",
			fTrim(a4w), fTrim(a4h), fTrimMat(f.formMatrix), fontRes, len(f.formBody), f.formBody))
	}
	for range forms {
		objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /FirstChar 32 /Widths "+inkHelvWidths+" >>")
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		buf.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o))
	}
	xref := buf.Len()
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1))
	for i := 1; i <= len(objs); i++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref))
	return buf.Bytes()
}

// inkHelvWidths 是 Helvetica 从 ' '(32) 起的连续字宽，够解析 'M' 与空格。
// 'M'=889, 'A'=667, 其余按 500 占位。
const inkHelvWidths = "[278 278 355 556 556 889 667 191 333 333 389 584 278 333 278 278 " +
	"556 556 556 556 556 556 556 556 556 556 278 278 584 584 584 556 1015 " +
	"667 667 722 722 667 611 778 722 278 500 667 556 833 722 778 667 778 722 667 611 722 667 944 667 667 611 " +
	"278 278 278 469 556 333 556 556 500 556 556 278 556 556 222 222 500 222 833 556 556 556 556 333 500 278 " +
	"556 500 722 500 500 500 334 260 334 584]"

// inkHelvWidthM / inkHelvWidthA 是 Helvetica 里 'M' 与 'A' 的宽度（1/1000 em），
// 供期望值换算。必须是 **833 / 667**，不是 889 —— 889 是 '%' 的宽度。
// 我第一版凭印象写了 889，判据报 72.26、我期望 89.06，两边差 16.8pt。
// 查下来是**我的期望值**引了错的字宽表，不是判据算错。
// 字面量和 /Widths 数组必须逐位对齐，否则「期望值」就是个传说。
const (
	inkHelvWidthM = 833.0
	inkHelvWidthA = 667.0
)

type inkFixtureForm struct {
	formMatrix [6]float64
	formBody   string
	pageBody   string
	// nestedRef ≥ 0 时，本 form 的内容会 Do 第 nestedRef 个 form。
	nestedRef int
}

func streamObj(s string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
}

func fTrim(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) }

func fTrimMat(m [6]float64) string {
	parts := make([]string, 6)
	for i, v := range m {
		parts[i] = fTrim(v)
	}
	return strings.Join(parts, " ")
}

const (
	inkA4W = 595.2756
	inkA4H = 841.8898
	inkGrd = 2
)

// 铺满整个 form 页框的实心方块：墨迹 == 页框，于是「页框越界多少」
// 就是「墨迹越界多少」，这是真实越界最常见的形态。
func inkFullBlockBody() string {
	return fmt.Sprintf("0 0 0 rg\n0 0 %s %s re\nf\n", fTrim(inkA4W), fTrim(inkA4H))
}

// 复现 pdfcpu NUp 的放置：先按比例缩放，再平移到格子的左下角。
func inkPlaceInto(col, row int, scale float64) [6]float64 {
	cw := inkA4W / inkGrd
	ch := inkA4H / inkGrd
	return [6]float64{scale, 0, 0, scale, float64(col) * cw, float64(row) * ch}
}

func inkCellRect(col, row int) inkRect {
	cw := inkA4W / inkGrd
	ch := inkA4H / inkGrd
	return inkRect{float64(col) * cw, float64(row) * ch, float64(col+1) * cw, float64(row+1) * ch}
}

// inkCheckA4 是判据本体：给定一个已经是 A4 网格排版的 PDF 与 grid，
// 返回每张页上每个格子的越界情况。
type inkCellVerdict struct {
	col, row  int
	cell      inkRect
	frameBox  inkRect
	inkBox    inkRect
	frameOver float64
	inkOver   float64
	placed    bool
	bounded   int // 按上界算宽度的绘制次数（墨迹盒是上界：可误报，不漏报）

}

type inkCheckReport struct {
	pages   []inkCheckPage
	failed  bool
	failure string
}

type inkCheckPage struct {
	verdicts []inkCellVerdict
	pageBox  inkRect
	parseOK  bool
	parseMsg string
}

var inkA4NameRe = regexp.MustCompile(`invoices-a4-(\d+)x(\d+)-`)

func inkCheckA4(ctx *model.Context, pageNr int, grid int) inkCheckPage {
	out := inkCheckPage{}
	xt := ctx.XRefTable
	d, _, _, err := xt.PageDict(pageNr, false)
	if err != nil {
		out.parseMsg = fmt.Sprintf("PageDict: %v", err)
		return out
	}
	mb, found := d.Find("MediaBox")
	if !found || mb == nil {
		out.parseMsg = "page has no MediaBox"
		return out
	}
	arr, err := xt.DereferenceArray(mb)
	if err != nil || len(arr) != 4 {
		out.parseMsg = "MediaBox is not a 4-array"
		return out
	}
	var v [4]float64
	for i, e := range arr {
		f, err := xt.DereferenceNumber(e)
		if err != nil {
			out.parseMsg = "MediaBox element is not a number"
			return out
		}
		v[i] = f
	}
	out.pageBox = inkRect{v[0], v[1], v[2], v[3]}

	resObj, found := d.Find("Resources")
	var res types.Dict
	if found && resObj != nil {
		if rd, err := xt.DereferenceDict(resObj); err == nil {
			res = rd
		}
	}
	bb, err := xt.PageContent(d, pageNr)
	if err != nil && err != model.ErrNoContent {
		out.parseMsg = fmt.Sprintf("PageContent: %v", err)
		return out
	}
	var page inkPageResult
	page.pageInk = inkEmpty()
	w := &inkWalker{xt: xt, seen: map[int]bool{}}
	if !w.walk(res, bb, inkIdentity, nil, &page, 0, -1) {
		out.parseMsg = "content stream walk failed：" + w.at
		return out
	}

	cw := (out.pageBox.x1 - out.pageBox.x0) / float64(grid)
	chh := (out.pageBox.y1 - out.pageBox.y0) / float64(grid)
	// 格子表只用于**归位**，不用于存放判定。
	cells := make([]inkCellVerdict, 0, grid*grid)
	for r := 0; r < grid; r++ {
		for c := 0; c < grid; c++ {
			cells = append(cells, inkCellVerdict{
				col: c, row: r,
				cell: inkRect{out.pageBox.x0 + float64(c)*cw, out.pageBox.y0 + float64(r)*chh,
					out.pageBox.x0 + float64(c+1)*cw, out.pageBox.y0 + float64(r+1)*chh},
			})
		}
	}
	// **判定按顶层放置归组**，不是按「每一个 form」。
	// pdfcpu NUp 把一张票整体放成一个顶层 form，票面内部还有嵌套 form
	// （背景条、表格线、文字块）。实测一页 A4 上顶层只有 4 个放置，
	// 嵌套碎片却有 21 个 —— 按碎片各自归格会得到一堆 y 为负、
	// 归不到任何格子的小框，而「这张票被放在哪一格」只由顶层决定。
	//
	// 归组的存储单位必须等于**被测对象的单位**：被测对象是「一张票在一个格子」。
	byRoot := map[int]*inkCellVerdict{}
	var order []int
	for i := range page.placements {
		p := &page.placements[i]
		v, seen := byRoot[p.root]
		if !seen {
			v = &inkCellVerdict{frameBox: p.box, inkBox: inkEmpty(), placed: true}
			byRoot[p.root] = v
			order = append(order, p.root)
		}
		v.frameBox.unionInto(p.box)
		v.inkBox.unionInto(p.ink)
		v.bounded += p.boundedCnt
	}
	for _, root := range order {
		p := &page.placements[root]
		v := byRoot[root]
		idx := inkCellIndexOf(cells, p.box)
		if idx < 0 {
			// 顶层放置的中心落在所有格子之外：整块摆到了页面上方/下方之外。
			// **不静默跳过**，单列一条，汇总里单独计数。
			v.col, v.row = -1, -1
			v.frameOver, v.inkOver = -1, -1
			out.verdicts = append(out.verdicts, *v)
			continue
		}
		cell := cells[idx].cell
		v.col, v.row, v.cell = cells[idx].col, cells[idx].row, cell
		v.frameOver = v.frameBox.overflowDepth(cell)
		v.inkOver = v.inkBox.overflowDepth(cell)
		out.verdicts = append(out.verdicts, *v)
	}
	out.parseOK = true
	return out
}

// inkCellIndexOf 找「中心落在其内」的格子。用中心而不是左上角：
// 越界 35pt 的页框左上角仍在原格内，但整体右移后中心可能跨格。
func inkCellIndexOf(vs []inkCellVerdict, box inkRect) int {
	if !box.valid() {
		return -1
	}
	cx := (box.x0 + box.x1) / 2
	cy := (box.y0 + box.y1) / 2
	for i := range vs {
		c := vs[i].cell
		if !c.valid() {
			continue
		}
		if cx >= c.x0 && cx <= c.x1 && cy >= c.y0 && cy <= c.y1 {
			return i
		}
	}
	return -1
}

func inkOpenXRef(rs *os.File) (*model.Context, error) {
	conf := model.NewDefaultConfiguration()
	return api.ReadValidateAndOptimize(rs, conf)
}

// ------------------------------------------------------------------ 负控

func TestA4InkOverflowCheckerOnSyntheticGrid(t *testing.T) {
	cases := []struct {
		name       string
		col, row   int
		scale      float64
		wantInkOut bool
		wantCell   string
		wantDepth  float64
		nested     bool // 顶层 form 内部再套一层 form
		wantVerds  int
	}{
		{"C-inside", 0, 0, 0.5, false, "0,0", 0, false, 1},
		{"C-pastRight", 0, 0, 0.559037, true, "0,0", 49.72, false, 1},
		{"C-pastInCell11", 1, 1, 0.559037, true, "1,1", 49.72, false, 1},
		// 鉴别性：页框比格子小 1%（**不**出格），墨迹也只画到页框内 ⇒ 未越界。
		// 若判据被写成「页框小就一定不越界」之外的任何恒真分支，它会红。
		{"C-thin", 1, 0, 0.49, false, "1,0", 0, false, 1},
		// 鉴别性（嵌套）：顶层 form 里再 Do 一个子 form。
		// **判定必须只有 1 条** —— 一张票 = 一个被测对象。
		// 若嵌套不继承顶层 root，碎片会各自被当成顶层放置，判定条数变 2，
		// 而按碎片归格得到的格子/越界值都可能仍然「看着对」。
		{"C-nested", 0, 0, 0.559037, true, "0,0", 49.72, true, 1},
	}
	ran := 0
	for _, c := range cases {
		ran++
		t.Run(c.name, func(t *testing.T) {
			forms := []inkFixtureForm{{
				formMatrix: inkPlaceInto(c.col, c.row, c.scale),
				formBody:   inkFullBlockBody(),
				pageBody:   "q 1 0 0 1 0 0 cm /Fm0 Do Q",
			}}
			if c.nested {
				// 顶层 form 的内容改成 Do 子 form；子 form 才是真正画东西的。
				// 两者 BBox / Matrix 相同 ⇒ 越界量应与 C-pastRight 完全一致。
				forms[0].formBody = "q 1 0 0 1 0 0 cm /Fn1 Do Q"
				forms[0].nestedRef = 1
				forms = append(forms, inkFixtureForm{
					formMatrix: inkIdentity,
					formBody:   inkFullBlockBody(),
				})
			}
			raw := inkFixturePDF(inkA4W, inkA4H, forms)
			dir := t.TempDir()
			// 文件名必须带 grid，判据据此取切格数。
			p := filepath.Join(dir, fmt.Sprintf("invoices-a4-%dx%d-fixture.pdf", inkGrd, inkGrd))
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, err := inkOpenXRef(f)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			pg := inkCheckA4(ctx, 1, inkGrd)
			if !pg.parseOK {
				t.Fatalf("判据没能解析夹具：%s", pg.parseMsg)
			}
			if len(pg.verdicts) != c.wantVerds {
				t.Errorf("判据出了 %d 条判定，用例表期望 %d 条 —— "+
					"一张票 = 一个被测对象，嵌套 form 必须归并到顶层",
					len(pg.verdicts), c.wantVerds)
			}
			var got *inkCellVerdict
			for i := range pg.verdicts {
				if pg.verdicts[i].placed && pg.verdicts[i].col >= 0 {
					got = &pg.verdicts[i]
					break
				}
			}
			if got == nil {
				t.Fatal("没有把任何放置归到格子里 —— 归格逻辑挂了")
			}
			// 墨迹必须真的被算出来。这一条挡住「判据在跑但永远读到空墨迹」
			// 这类假绿：第一版零值矩形被判成 valid，C-inside 就是这么绿的。
			if !got.inkBox.valid() {
				t.Fatalf("格子 (%d,%d) 没算到任何墨迹 —— 判据读到的是空盒子，"+
					"「未越界」结论无意义（frame=%+v）", got.col, got.row, got.frameBox)
			}
			if gotCell := fmt.Sprintf("%d,%d", got.col, got.row); gotCell != c.wantCell {
				t.Errorf("归到格子 %s，期望 %s（归格逻辑可能写死了第 0 格）", gotCell, c.wantCell)
			}
			over := got.inkOver > inkTolPt
			if over != c.wantInkOut {
				t.Errorf("墨迹越界=%v（深度 %.2fpt），期望越界=%v；frameOver=%.2f ink=%v",
					over, got.inkOver, c.wantInkOut, got.frameOver, got.inkBox)
			}
			if c.wantInkOut && math.Abs(got.inkOver-c.wantDepth) > 1.0 {
				t.Errorf("越界深度 %.2fpt，期望约 %.2fpt", got.inkOver, c.wantDepth)
			}
		})
	}
	if ran != len(cases) {
		t.Fatalf("只跑了 %d 条，用例表有 %d 条 —— 有用例被吞掉了", ran, len(cases))
	}
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.name)
	}
	// 光比对数量不够：**用例表自己少一条**时数量照样相等（变异 T2 实测）。
	// 所以把必须存在的负控名钉死：负控被删必须让测试红。
	inkAssertControlSet(t, names, []string{"C-inside", "C-pastRight", "C-pastInCell11", "C-thin", "C-nested"})
}

// inkAssertControlSet 断言必需的负控都在用例表里。
//
// 为什么需要它：把一条负控整行注释掉，测试**照样全绿**，
// 因为「负控少一条」和「负控全过」在 go test 输出上完全一样。
// 这不是假想——本文件的 T-textPast 就被 PowerShell 的字面量 `r`n 吞进注释过，
// 而「把整条负控注释掉」这个变异当时确实报 ok。
func inkAssertControlSet(t *testing.T, got, want []string) {
	t.Helper()
	set := make(map[string]bool, len(got))
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("必需的负控 %q 不在用例表里（现有：%v）—— 负控被删时输出与全过无异", w, got)
		}
	}
}

// TestA4InkOverflowCheckerOnText 覆盖**文字**宽度路径，并给出本判据唯一的
// 鉴别性能力：**降级**。
//
// ## 这组用例想证明什么（以及为什么不能证明更多）
//
// 原来我写的是「页框在格内、墨迹越界」——**那是不可能的形态**：
// form 内容按 /BBox 裁剪（PDF 32000-1 8.10.1），所以
// 「页框不出格 ⇒ 墨迹必不出格」是定理。第一版夹具把文字画到 form BBox
// 之外去构造这个形态，被裁剪逻辑打回——**判据是对的，用例是假的**。
//
// 顺带一个被裁剪推翻的数：我以为「保守外扩 20pt 会让 0.5 缩放下的文字
// 越界 47.36pt」。外扩发生在 A4 空间、而 BBox 裁剪在其之后，所以墨迹
// 被夹回页框边界，**实际越界 0**。裁剪规则赢。
//
// 所以能证明的只有两件事，方向都是「页框出格 → 墨迹未必出格」：
// ① T-scaled559：页框越界 49.67，文字铺满页框 ⇒ 墨迹同样越界 49.67；
// ② T-frameOnly：**页框越界 49.67，文字只画在左半边 ⇒ 墨迹越界 0**。
//
//	这一条是判据的鉴别性所在：页框判据会报警，墨迹判据把它降级。
//	如果判据只是把 frameOver 抄成 inkOver，只有它会红。
func TestA4InkOverflowCheckerOnText(t *testing.T) {
	const size = 20.0
	// 用 "MA" 交替而不是一串 "M"：Helvetica 里 **M 和 m 同宽 833**，
	// 所以只画 M 时，「/FirstChar 读错」这个变异会撞出同一个宽度而
	// 完全测不出来（实测：firstChar 强制为 0，w1000 一模一样）。
	// 混两种宽度不同的字符，任何索引错位都会改变总推进量。
	body := func(startX float64, n int) string {
		return fmt.Sprintf("BT /F1 %s Tf 1 0 0 1 %s 100 Tm (%s) Tj ET",
			fTrim(size), fTrim(startX), strings.Repeat("MA", n))
	}
	cases := []struct {
		name      string
		scale     float64
		startX    float64
		n         int
		wantInk   float64
		wantFrame float64
	}{
		// 0.5 恰好铺满格子：页框与墨迹都零越界。
		{"T-inside", 0.5, 200, 5, 0, 0},
		// 坏缩放 + 文字**落在页框内、格子外的那条窄带**里。
		// 这个位置是刻意选的：文字若画到 /BBox 之外，会被 BBox 裁剪掉，
		// 而裁剪会把「CTM×Tm 顺序反了」这类坐标错误**掩盖**掉
		// （实测：n=15 时那条变异测不出来，因为两种顺序裁剪后结果相同）。
		// 11 组 "MA" 推进 = 330，起点 200 ⇒ 终点 530（< BBox 595.28），
		// ×0.559037 = 296.29，再加保守外扩 20 ⇒ 316.29，
		// 落在 (297.64, 332.76] 这条「框内格外」的窄带里。
		{"T-scaled559", 0.559037, 200, 11, 18.65, 49.67},
		// 坏缩放 + 文字只在左半边（终点 form x=170 ⇒ A4 x=95.04）：
		// 页框越界 49.67，墨迹越界 0 —— 降级。
		{"T-frameOnly", 0.559037, 20, 5, 0, 49.67},
	}
	// 保守外扩系数是**结论强度**的一部分（它让「未越界」偏保守），
	// 不是可调参数。改它必须同时改期望值，所以这里直接钉死。
	if inkInflateFs != 1.0 {
		t.Fatalf("inkInflateFs 被改成 %v —— 它是保守外扩系数，改动会让"+
			"「未越界」的结论强度悄悄变化，且期望值会跟着一起动", inkInflateFs)
	}
	ran := 0
	for _, c := range cases {
		ran++
		t.Run(c.name, func(t *testing.T) {
			form := inkFixtureForm{
				formMatrix: inkPlaceInto(0, 0, c.scale),
				formBody:   body(c.startX, c.n),
				pageBody:   "q 1 0 0 1 0 0 cm /Fm0 Do Q",
			}
			raw := inkFixturePDF(inkA4W, inkA4H, []inkFixtureForm{form})
			dir := t.TempDir()
			p := filepath.Join(dir, fmt.Sprintf("invoices-a4-%dx%d-fixture.pdf", inkGrd, inkGrd))
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, err := inkOpenXRef(f)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			pg := inkCheckA4(ctx, 1, inkGrd)
			if !pg.parseOK {
				t.Fatalf("判据没能解析夹具：%s", pg.parseMsg)
			}
			var got *inkCellVerdict
			for i := range pg.verdicts {
				if pg.verdicts[i].placed && pg.verdicts[i].col >= 0 {
					got = &pg.verdicts[i]
					break
				}
			}
			if got == nil || !got.inkBox.valid() {
				t.Fatalf("没算到墨迹（got=%+v）—— 文字路径没被走到", got)
			}
			// 本组用例的前提：字宽必须解析成功，否则「未越界」的结论没有意义。
			if got.bounded > 0 {
				t.Fatalf("有 %d 次绘制只拿到宽度上界，本组用例要求精确字宽", got.bounded)
			}
			if over := got.inkOver > inkTolPt; over != (c.wantInk > inkTolPt) {
				t.Errorf("墨迹越界=%v（深度 %.2fpt），期望 %.2fpt（frame=%+v ink=%+v）",
					over, got.inkOver, c.wantInk, got.frameBox, got.inkBox)
			}
			if math.Abs(got.inkOver-c.wantInk) > 1.0 {
				t.Errorf("墨迹越界深度 %.2fpt，期望 %.2fpt", got.inkOver, c.wantInk)
			}
			if math.Abs(got.frameOver-c.wantFrame) > 1.0 {
				t.Errorf("页框越界 %.2fpt，期望 %.2fpt（页框与墨迹是两个独立量，都得对）",
					got.frameOver, c.wantFrame)
			}
		})
	}
	// **子用例数必须与用例表一致。**
	// 这条不是形式主义：第一版 T-textPast 的整行被误写成注释（PowerShell
	// 把 `` `r`n `` 当字面量写进了文件），用例从表里消失、测试照样全绿。
	// 「少一条负控」与「负控全过」在 `go test` 输出上**完全一样**。
	if ran != len(cases) {
		t.Fatalf("只跑了 %d 条，用例表有 %d 条 —— 有用例被吞掉了", ran, len(cases))
	}
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.name)
	}
	inkAssertControlSet(t, names, []string{"T-inside", "T-scaled559", "T-frameOnly"})
}

// ------------------------------------------------------------------ 真数据

func TestA4InkOverflowOnRealExports(t *testing.T) {
	if os.Getenv("POCKET_DIAG_A4_INK") != "1" {
		t.Skip("set POCKET_DIAG_A4_INK=1 (+ POCKET_DIAG_EXPORT_DIR)")
	}
	dir := os.Getenv("POCKET_DIAG_EXPORT_DIR")
	if dir == "" {
		t.Fatal("POCKET_DIAG_EXPORT_DIR 需显式传入（无缺省值）")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读不到 %s：%v", dir, err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pdf") {
			continue
		}
		if inkA4NameRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("%s 里没有 invoices-a4-NxN-*.pdf —— 判据会输出「一切正常」的假结论", dir)
	}

	totalPlaced, totalInkOver, totalFrameOver, totalBounded, totalUnplaced := 0, 0, 0, 0, 0
	for _, name := range names {
		m := inkA4NameRe.FindStringSubmatch(name)
		grid, _ := strconv.Atoi(m[1])
		p := filepath.Join(dir, name)
		f, err := os.Open(p)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		ctx, err := inkOpenXRef(f)
		if err != nil {
			f.Close()
			t.Errorf("%s: open: %v", name, err)
			continue
		}
		st, _ := f.Stat()
		f.Close()
		t.Logf("== %s (%d bytes, grid=%d, %d 页)", name, st.Size(), grid, ctx.PageCount)
		for pn := 1; pn <= ctx.PageCount; pn++ {
			pg := inkCheckA4(ctx, pn, grid)
			if !pg.parseOK {
				// 解析不了就**报出来**，不能当成「这一页没问题」。
				t.Errorf("%s p%d: 判据没能解析：%s", name, pn, pg.parseMsg)
				continue
			}
			for _, v := range pg.verdicts {
				if v.col < 0 {
					totalUnplaced++
					t.Logf("   p%d 有一处放置的中心落在所有格子之外：frame=%+v", pn, v.frameBox)
					continue
				}
				if !v.placed {
					continue
				}
				totalPlaced++
				totalBounded += v.bounded
				if v.frameOver > inkTolPt {
					totalFrameOver++
				}
				if v.inkOver > inkTolPt {
					totalInkOver++
				}
				t.Logf("   p%d 格(%d,%d) 页框越界=%.2f 墨迹越界=%.2f 字宽上界=%d frame=%+v ink=%+v",
					pn, v.col, v.row, v.frameOver, v.inkOver, v.bounded, v.frameBox, v.inkBox)
			}
		}
	}
	t.Logf("汇总：%d 个 A4 文件，%d 处放置，页框越界 %d，墨迹越界 %d，按上界算宽度的绘制 %d 次，无法归格 %d",
		len(names), totalPlaced, totalFrameOver, totalInkOver, totalBounded, totalUnplaced)
	if totalUnplaced > 0 {
		t.Errorf("有 %d 处放置无法归格，判据覆盖不完整", totalUnplaced)
	}
	if totalInkOver > 0 {
		t.Errorf("墨迹真的越过了裁切线：%d 处（打印后沿格线剪开会切到邻格的票）", totalInkOver)
	}
}
