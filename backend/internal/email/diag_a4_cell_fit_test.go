package email

// diag_a4_cell_fit_test.go —— **只读**诊断：每个票面的**页框**是不是完整落在
// 自己那一格里？
//
// ## 它回答的是哪个问题
//
// round37 之前，「A4 打印后能不能直接剪裁」一直挂在「未验证」上，理由是
// 要判墨迹就得栅格化，而 pdfcpu v0.11 没有渲染 API。
//
// **那个理由不成立。** 判定不需要墨迹，需要的是一个**充分条件**：
//
//	PDF 只有落在页面框（MediaBox/CropBox）内的内容才会被渲染/打印，
//	页框之外的部分要么被裁掉、要么根本不存在。
//	⇒ 可见墨迹 ⊆ 页框。
//	⇒ 只要**每个票面的页框 ⊆ 它自己那一格**，就必然不会跨格，
//	   打印后沿格线剪开不会切到邻格的票。
//
// 所以本诊断判的是「页框是否出格」，并把它当作墨迹不碰撞的**充分条件**。
// 这比直接算墨迹更强也更稳：算墨迹要字体宽度、要递归 XObject、要栅格化，
// 而算页框只需要一个矩形包含判定。
//
// ## 为什么这次不去算墨迹（写清楚边界）
//
// ① 若某张票的页框**确实**出格了，本诊断只会报红，**不会**告诉你墨迹有没有
//    真的越过去（那仍需栅格化）。出格是「需要进一步查」的信号，不是
//    「已经撞了」的证明。
// ② 票面页内常带旋转与白边（实测通行费票面首层变换是 a=0.24 d=-0.24 的
//    90° 旋转 + 0.24 缩放），所以「页框出格」很容易发生而「墨迹出格」
//    并不跟着发生。两者不可互相替代——这正是 round36 推翻过的那个混淆。
//
// ## 门禁
//
// 需 POCKET_DIAG_CELL_FIT=1 + POCKET_DIAG_EXPORT_DIR（真实 exports 目录）
// + POCKET_DIAG_INVOICE_DIR（真实发票目录，提供候选票面的页尺寸）。
// 两个目录都**无缺省值**：不设就只会扫到空目录、输出「一切正常」的假结论。
//
// ## 配套负控
//
// TestA4CellFitCheckerFiresOnOverflow 用构造输入证明判据不是恒暗：
// ① 故意把缩放调大 5%，必须判为出格；
// ② 缩放调小 5%，必须判为在格内。
// 前提由构造保证（rect 显式构造），不依赖任何真实文件。

import (
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
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// cellFitTolPt 是「页框 ⊆ 格子」的判定容差（pt）。
//
// 与 cellOriginTolPt 同量级（0.5pt ≈ 0.18mm）：远小于刀口公差，
// 又足以吸收页尺寸的舍入（实测票面页宽有 595.0 与 595.3 两种值）。
const cellFitTolPt = 0.5

// pagePlacementFull 是一次放置的**完整** CTM。
//
// 为什么不复用 pagePlacements：它只保留了 a/e/f（缩放与平移），把 b/c/d
// 丢掉了。fit-to-cell 的判定必须用完整矩阵——页框旋转 90° 时 b/c 决定
// 矩形往哪个方向长，只看 a 与 e/f 会算出一个转置过的矩形。
type pagePlacementFull struct {
	form             int
	a, b, c, d, e, f float64
}

var cmFullRe = regexp.MustCompile(
	`(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+cm\s+/Fm(\d+)\s+Do`)

// srcPage 是一张候选票面源文件的页尺寸。
type srcPage struct {
	name string
	w, h float64
}

// fitsCellInCell 判定一个页框（以放置原点 (x,y) 为左下角、按 a/d 缩放，
// 不考虑旋转——NUp 的放置是纯缩放+平移，b/c 为 0）是否完整落在 cell 内。
//
// cell 传的是格子矩形 [x0,x1]×[y0,y1]。
func rectInCell(x, y, w, h, cx0, cy0, cx1, cy1, tol float64) (bool, float64) {
	rx0, ry0 := x, y
	rx1, ry1 := x+w, y+h
	// 越界量取「四个方向里最严重的那个」，正值表示出格多少 pt。
	over := math.Max(math.Max(cx0-rx0, rx1-cx1), math.Max(cy0-ry0, ry1-cy1))
	return over <= tol, over
}

func TestDiagA4CellFit(t *testing.T) {
	if os.Getenv("POCKET_DIAG_CELL_FIT") != "1" {
		t.Skip("set POCKET_DIAG_CELL_FIT=1 (+ POCKET_DIAG_EXPORT_DIR / POCKET_DIAG_INVOICE_DIR)")
	}
	expDir := os.Getenv("POCKET_DIAG_EXPORT_DIR")
	invDir := os.Getenv("POCKET_DIAG_INVOICE_DIR")
	if expDir == "" || invDir == "" {
		t.Fatal("POCKET_DIAG_EXPORT_DIR / POCKET_DIAG_INVOICE_DIR 均需显式传入（无缺省值）")
	}

	// ---- 候选票面：真实发票目录里每个 PDF 的页尺寸 ----
	ents, err := os.ReadDir(invDir)
	if err != nil {
		t.Fatalf("读发票目录失败：%v", err)
	}
	var srcs []srcPage
	var skipped []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}
		p := filepath.Join(invDir, e.Name())
		dim, derr, panicked := safePageDims(p)
		if panicked != nil {
			// 不能只跳过：候选集少一个，后面「无候选能装下」的判红就无法区分
			// 「票面真的出格」与「那个文件没读进来」。所以显式记下来并计数。
			t.Logf("[⚠ 读页尺寸时 pdfcpu panic，已跳过] %s：%v", e.Name(), panicked)
			skipped = append(skipped, e.Name())
			continue
		}
		if derr != nil || len(dim) == 0 {
			t.Logf("[⚠ 读页尺寸失败，已跳过] %s：%v", e.Name(), derr)
			skipped = append(skipped, e.Name())
			continue
		}
		w, h := dim[0].Width, dim[0].Height
		// ⚠ **不要**为了「宽 ≤ 高」去交换 w/h。
		//
		// 第一版做了这个归一，结果在真实数据上**报了 16 条出格**，全是假的。
		// 原因很具体：通行费票面是 595.2756×396.8504（比 A4 矮一半），
		// 交换后变成 396.85×595.28，于是推算出的页框高度 0.5×595.28=297.6
		// 与实际 0.5×396.85=198.4 完全不符，越界量凭空多出 99pt。
		//
		// 形态是「判据把输入归一成了它不是的形状，然后自信地报红」——
		// 与「判据漏判」同样有害，而且更难发现：输出看着像真缺陷。
		//
		// pdfcpu 的 Dim.Width/Height 就是页面实际尺寸，无需归一。
		// （注意：diag_real_exports_test.go 里那个 w/h 交换是给**产物 A4 页**
		// 用的，因为那一页可能带 /Rotate；对象不同，不要照搬。）
		srcs = append(srcs, srcPage{name: e.Name(), w: w, h: h})
	}
	if len(srcs) == 0 {
		t.Fatalf("发票目录 %s 里没有可用的 PDF —— 本诊断会输出「全部在格内」的假结论", invDir)
	}
	sort.Slice(srcs, func(i, j int) bool { return srcs[i].name < srcs[j].name })
	t.Logf("[diag] 候选票面 %d 个（来自 %s）", len(srcs), invDir)
	if len(skipped) > 0 {
		// 这是「判据的覆盖面对不上被测对象」形态：跳过的文件如果恰好是某格里的
		// 那张票，本诊断会把「读不到」误报成「出格」。必须让读日志的人看见。
		t.Logf("[⚠ 候选集不完整] 目录里 %d 个 PDF，有 %d 个读不出页尺寸已被排除：%s"+
			" —— 若某个格子下面报「无候选能装下」，先排除是这个原因",
			len(srcs)+len(skipped), len(skipped), strings.Join(skipped, " | "))
	}

	matches, err := filepath.Glob(filepath.Join(expDir, "invoices-a4-*-*-*.pdf"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("glob in %s: n=%d err=%v", expDir, len(matches), err)
	}
	sort.Strings(matches)
	t.Logf("[diag] 导出产物 %d 个（来自 %s）", len(matches), expDir)

	checked, outOfCell, ambiguous := 0, 0, 0
	for _, path := range matches {
		base := filepath.Base(path)
		parts := strings.Split(base, "-")
		if len(parts) < 5 {
			t.Errorf("%s: 文件名形态异常", base)
			outOfCell++
			continue
		}
		gp := strings.SplitN(parts[2], "x", 2)
		if len(gp) != 2 || gp[0] != gp[1] {
			t.Errorf("%s: 读不出 grid（%q）", base, parts[2])
			outOfCell++
			continue
		}
		grid, _ := strconv.Atoi(gp[0])

		dim, err := api.PageDimsFile(path)
		if err != nil || len(dim) == 0 {
			t.Errorf("%s: 页尺寸：%v", base, err)
			outOfCell++
			continue
		}
		w, h := math.Min(dim[0].Width, dim[0].Height), math.Max(dim[0].Width, dim[0].Height)
		cellW, cellH := w/float64(grid), h/float64(grid)

		pages, _ := api.PageCountFile(path)
		for p := 1; p <= pages; p++ {
			pls := fullPlacements(t, path, p)
			if len(pls) == 0 {
				continue
			}
			for _, pl := range pls {
				// 格子：由放置原点落在第几格决定。
				cx := int(math.Floor(pl.e / cellW))
				cy := int(math.Floor(pl.f / cellH))
				if cx < 0 {
					cx = 0
				}
				if cy < 0 {
					cy = 0
				}
				cx0, cy0 := float64(cx)*cellW, float64(cy)*cellH
				cx1, cy1 := cx0+cellW, cy0+cellH

				// 配对：找哪个候选票面的页尺寸放进这一格是「刚好装下且居中」的。
				// 居中是判据的一半——只测「装得下」会有多个候选（小的都能装下）。
				var fits []srcPage
				var worstOverflow float64
				chosen := -1
				for i, s := range srcs {
					ok, over := rectInCell(pl.e, pl.f, pl.a*s.w, pl.d*s.h, cx0, cy0, cx1, cy1, cellFitTolPt)
					if !ok {
						if chosen < 0 || over < worstOverflow {
							worstOverflow = over
							chosen = i
						}
						continue
					}
					// 居中：页框中心与格子中心的偏差。
					dcx := math.Abs((pl.e + pl.a*s.w/2) - (cx0 + cellW/2))
					dcy := math.Abs((pl.f + pl.d*s.h/2) - (cy0 + cellH/2))
					if dcx <= cellFitTolPt && dcy <= cellFitTolPt {
						fits = append(fits, s)
					}
				}
				checked++
				switch {
				case len(fits) == 1:
					s := fits[0]
					t.Logf("%s p%d Fm%d → %-58s 页框 %.1fx%.1f 缩放%.4f cell[%d,%d] 居中偏差(%.2f,%.2f)pt **在格内**",
						base, p, pl.form, s.name, s.w, s.h, pl.a, cx, cy,
						math.Abs((pl.e+pl.a*s.w/2)-(cx0+cellW/2)),
						math.Abs((pl.f+pl.d*s.h/2)-(cy0+cellH/2)))
				case len(fits) == 0:
					// 这条判定**不依赖配对**：fits 为空意味着「发票目录里没有任何
					// 一张票，在该缩放下能装进这一格」。所以结论与「这是哪张票」无关，
					// 这是它能被当成真缺陷报告的唯一原因。
					outOfCell++
					maxSrcW := 0.0
					for _, s := range srcs {
						if s.w > maxSrcW {
							maxSrcW = s.w
						}
					}
					needW := cellW / pl.a
					t.Errorf("[出格·不依赖配对] %s p%d Fm%d: cm 缩放 a=%.5f，原点 e=%.3f f=%.3f；"+
						"格子 %.3f×%.3f ⇒ 源页宽必须 ≤ %.2fpt 才能装下，"+
						"而目录里最宽的票是 %.2fpt ⇒ 必然越界 %.2fpt（横向）",
						base, p, pl.form, pl.a, pl.e, pl.f, cellW, cellH,
						needW, maxSrcW, maxSrcW*pl.a-cellW)
					t.Logf("       越界 %.2fpt ≈ %.1fmm；该票面盒宽 %.2fpt，"+
						"格子只有 %.2fpt —— 沿格线剪裁会把邻格的票面切掉一块。"+
						"**墨迹是否也真的越过去仍未判定**（需栅格化），"+
						"但页框越界已足以说明「不能干净剪裁」。",
						maxSrcW*pl.a-cellW, (maxSrcW*pl.a-cellW)*25.4/72,
						maxSrcW*pl.a, cellW)
				default:
					ambiguous++
					t.Logf("[提示·不参与判定] %s p%d Fm%d：%d 个候选页尺寸都满足"+
						"「装下且居中」（%s）—— 判据只能确认「**至少有一张**票能装进这格」，"+
						"定位不到具体是哪张。这是候选集里同尺寸票面过多导致的，"+
						"不是异常（真实数据里 7 张 A4 尺寸票彼此无法区分）。",
						base, p, pl.form, len(fits), joinNames(fits))
				}
			}
		}
	}
	t.Logf("[diag] 汇总：核对 %d 次放置 / 出格 %d / 仅能确认「至少一张能装下」%d", checked, outOfCell, ambiguous)
	if outOfCell == 0 && checked > 0 {
		t.Logf("[diag] 全部 %d 次放置的**页框**都装得进各自格子。"+
			"由「可见墨迹 ⊆ 页框」⇒ 沿格线剪裁不会切到邻格的票面。", checked)
		t.Log("[diag] 结论边界：它保证的是「页框不越格」，不保证墨迹**贴**着格线" +
			"（贴线也不影响剪裁），也不保证票面内部排版美观。")
	}
}

// TestA4CellFitCheckerFiresOnOverflow —— 判据 0 的**负控**。
//
// 为什么必须有：真实产物上「全部在格内」与「判据压根没在算」产生的输出
// **完全一样**。所以要在构造输入上证明它真的会红。
//
// 构造前提由代码保证（矩形是显式写出来的），不依赖任何真实文件，
// 也不依赖 pdfcpu 的行为——测的是判据本身，不是产物。
func TestA4CellFitCheckerFiresOnOverflow(t *testing.T) {
	// 2×2 网格的 A4：595.276×841.89 ⇒ 格子 297.638×420.945
	const (
		cellW = 297.638
		cellH = 420.945
	)
	// 一张 595.0×842.0 的票面，fit-to-cell 的缩放正好是 0.5。
	const (
		srcW = 595.0
		srcH = 842.0
	)
	scaleOK := 0.5
	// 左上格：x∈[0,cellW]，y∈[cellH,2*cellH]
	cx0, cy0, cx1, cy1 := 0.0, cellH, cellW, 2*cellH

	t.Run("刚好装下应判为在格内", func(t *testing.T) {
		ok, over := rectInCell(0, cellH, scaleOK*srcW, scaleOK*srcH, cx0, cy0, cx1, cy1, cellFitTolPt)
		t.Logf("over=%.4fpt", over)
		if !ok {
			t.Fatalf("完美居中的 2×2 放置被判为出格（越界 %.3fpt）—— 判据过严，"+
				"会对完全正确的产物报红", over)
		}
	})

	t.Run("放大 5% 必须判为出格", func(t *testing.T) {
		// 5% × 297.638 = 14.88pt ≈ 5.2mm，远超 0.5pt 容差。
		ok, over := rectInCell(0, cellH, 0.525*srcW, 0.525*srcH, cx0, cy0, cx1, cy1, cellFitTolPt)
		t.Logf("over=%.4fpt", over)
		if ok {
			t.Fatal("缩放大 5% 仍被判为在格内 —— 判据失明，真实产物出格时它不会报")
		}
		if over <= cellFitTolPt {
			t.Fatalf("判为出格但越界量 %.4fpt 不大于容差 %.2fpt，自相矛盾",
				over, cellFitTolPt)
		}
	})

	t.Run("向左溢出必须判为出格", func(t *testing.T) {
		// 尺寸完全正常，只是整体左移 20pt ⇒ 右边缘撞到邻格。
		ok, over := rectInCell(-20, cellH, scaleOK*srcW, scaleOK*srcH, cx0, cy0, cx1, cy1, cellFitTolPt)
		t.Logf("over=%.4fpt", over)
		if ok {
			t.Fatal("整体左移 20pt 仍被判为在格内 —— 只测了尺寸没测位置")
		}
	})

	t.Run("缩小 5% 应判为在格内", func(t *testing.T) {
		ok, over := rectInCell(7, cellH+7, 0.475*srcW, 0.475*srcH, cx0, cy0, cx1, cy1, cellFitTolPt)
		t.Logf("over=%.4fpt", over)
		if !ok {
			t.Fatalf("缩到 95%% 仍被判出格（越界 %.3fpt）", over)
		}
	})
}

// safePageDims 包一层 recover 调 pdfcpu。
//
// 为什么必须：pdfcpu v0.11 的 `model.skipStringLit` 在畸形 PDF 上会
// `panic: slice bounds out of range [-1:]`（2026-10-04 实测，触发文件是
// 目录里某张退化件）。一个诊断因为一个坏输入整份挂掉，等于**零产出**；
// 更糟的是若只是静默跳过，它会用一个残缺的候选集给出「全部在格内」的结论。
// 所以这里把 panic 也收成返回值，由调用方决定怎么报。
func safePageDims(path string) (dim []types.Dim, err error, panicked any) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	dim, err = api.PageDimsFile(path)
	return dim, err, nil
}

func pickW(ss []srcPage, i int) float64 {
	if i < 0 || i >= len(ss) {
		return 0
	}
	return ss[i].w
}

func pickH(ss []srcPage, i int) float64 {
	if i < 0 || i >= len(ss) {
		return 0
	}
	return ss[i].h
}

func joinNames(ss []srcPage) string {
	var b []string
	for _, s := range ss {
		b = append(b, s.name)
	}
	return strings.Join(b, " | ")
}

// fullPlacements 取出该页所有放置的完整 CTM。
func fullPlacements(t *testing.T, pdfPath string, page int) []pagePlacementFull {
	t.Helper()
	cd := t.TempDir()
	if err := api.ExtractContentFile(pdfPath, cd, []string{fmt.Sprint(page)}, nil); err != nil {
		t.Fatalf("extract page %d content: %v", page, err)
	}
	es, err := os.ReadDir(cd)
	if err != nil || len(es) == 0 {
		t.Fatalf("page %d 没有解出内容流", page)
	}
	raw, err := os.ReadFile(filepath.Join(cd, es[0].Name()))
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	var out []pagePlacementFull
	for _, m := range cmFullRe.FindAllStringSubmatch(string(raw), -1) {
		out = append(out, pagePlacementFull{
			form: int(mustFloat(t, m[7])),
			a:    mustFloat(t, m[1]), b: mustFloat(t, m[2]),
			c: mustFloat(t, m[3]), d: mustFloat(t, m[4]),
			e: mustFloat(t, m[5]), f: mustFloat(t, m[6]),
		})
	}
	return out
}
