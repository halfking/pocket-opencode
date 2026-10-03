package email

// diag_nup_scale_repro_test.go —— **只读/离线**诊断：用**生产函数**复现
// A4 网格导出里「缩放不是 fit-to-cell」的那一类放置。
//
// ## 要复现什么
//
// round37 第八节在真实产物上抓到 4 处出格（横向 8.3–12.4mm），它们的共同点是
// 缩放 = fit-to-cell 的 1.118 倍。真实产物是历史文件、根因未知，本诊断改成
// **构造输入**：把已知尺寸的源页喂给生产的 `ExportInvoiceGrid`，再把产物里
// 实际写出的 cm 读回来比对。
//
// 判定不依赖 pdfcpu 的实现，只依赖两条可独立验证的常量：
//   · 输出的格子尺寸 = A4 / grid（Border 画出来的裁切线就是它）；
//   · 源页放进格子应当用 min(cellW/srcW, cellH/srcH)。
// 两者一旦不成立，就是出格。
//
// ## 为什么必须走生产函数而不是直接调 pdfcpu
//
// 直接调 `api.NUpFile` 会绕过 `normalizeInvoiceFilesToPDF` 与合并那两步，
// 而真实产物是走完整链路的。若缺陷其实出在归一/合并那一步，
// 直接调 NUpFile 就复现不出来，结论会是「pdfcpu 没问题」——
// 那是把搜索面缩窄后得出的假阴性。
//
// 门禁：POCKET_DIAG_NUP_SCALE=1。不设就 skip（它要真的写临时文件）。
// 全程只写 t.TempDir()，不碰生产目录、不连库。

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// mkSizedPDF 生成一个指定尺寸的单页 PDF。
func mkSizedPDF(t *testing.T, path string, w, h float64) {
	t.Helper()
	f := gofpdf.NewCustom(&gofpdf.InitType{
		UnitStr: "pt",
		Size:    gofpdf.SizeType{Wd: w, Ht: h},
	})
	f.AddPage()
	f.SetFont("Helvetica", "", 12)
	// 画一条横线，保证页面上真有墨迹（空白页会被当成退化件）。
	f.Line(10, 10, w-10, 10)
	f.CellFormat(40, 12, "x", "", 1, "L", false, 0, "")
	if err := f.OutputFileAndClose(path); err != nil {
		t.Fatalf("写 %s 失败：%v", path, err)
	}
}

func TestDiagNUpScaleRepro(t *testing.T) {
	if os.Getenv("POCKET_DIAG_NUP_SCALE") != "1" {
		t.Skip("set POCKET_DIAG_NUP_SCALE=1 to reproduce the n-up scale defect offline")
	}

	// 源页尺寸矩阵：前两个是真实语料里出现过的形态，后几个是刻意扫的边界。
	// 重点是 (595.28, 396.85) —— 通行费那类「比 A4 矮一半」的票面。
	sizes := []struct{ w, h float64 }{
		{595.28, 841.89},  // 常规 A4
		{595.28, 396.85},  // 通行费实测形态（比 A4 矮一半）
		{595.28, 595.28},  // 正方形
		{595.28, 200},     // 极扁
		{420, 841.89},     // 比 A4 窄
		{595.28, 841.89},  // 重复 A4，用来确认同页多张时缩放是否一致
		{595.28, 396.85},  // 再来一张矮票，与 A4 同页
		{595.28, 300},     // 介于两者之间
		{595.28, 420.945}, // 恰好等于格高
		{595.28, 421.5},   // 略大于格高
	}

	const grid = 2
	cellW := a4WidthPt / grid
	cellH := a4HeightPt / grid

	dir := t.TempDir()
	var files []string

	// 真实票面模式：直接把生产目录里**真的那张票**复制进临时目录跑。
	//
	// 为什么必须走这一条：上面的合成源页是 gofpdf 造的，结构上很干净
	//（CropBox == MediaBox、无 /Rotate、无嵌入字体）。真实票面是 XML 渲染器
	// 产出的，带嵌入 CJK 字体子集，可能有不同 CropBox 或旋转。
	// 合成输入全绿而真实产物出格，恰恰指向这类结构差异 ——
	// 那种情况下「合成输入没复现」不能当��缺陷不存在的证据。
	if real := os.Getenv("POCKET_DIAG_NUP_SCALE_DIR"); real != "" {
		// ⚠ 必须把 sizes 清空。第一版忘了清，于是 minSrcW 用了合成页的 420，
		// 而真正喂进导出的是真实票面（最窄 594.96）—— 判据的输入和被测对象
		// 不是同一批，「出格」判定因此失真（真实数据里那次它就漏报了）。
		sizes = nil
		ents, rerr := os.ReadDir(real)
		if rerr != nil {
			t.Fatalf("读真实发票目录失败：%v", rerr)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
				continue
			}
			src := filepath.Join(real, e.Name())
			d, _, pErr := safePageDims(src)
			if pErr != nil {
				t.Logf("[跳过·pdfcpu panic] %s：%v", e.Name(), pErr)
				continue
			}
			if len(d) == 0 {
				t.Logf("[跳过·读不到页尺寸] %s", e.Name())
				continue
			}
			dst := filepath.Join(dir, e.Name())
			if _, cerr := os.Stat(dst); cerr != nil {
				b, rerr2 := os.ReadFile(src)
				if rerr2 != nil || os.WriteFile(dst, b, 0o600) != nil {
					t.Logf("[跳过·复制失败] %s", e.Name())
					continue
				}
			}
			files = append(files, dst)
			sizes = append(sizes, struct{ w, h float64 }{d[0].Width, d[0].Height})
			t.Logf("[真实票面] %-64s %.2fx%.2f", e.Name(), d[0].Width, d[0].Height)
		}
		if len(files) == 0 {
			t.Fatal("真实目录里没有可用 PDF")
		}
		t.Logf("[diag] **真实票面模式**：%d 张，全部来自 %s", len(files), real)
	} else {
		// 可用 POCKET_DIAG_NUP_SIZES 覆盖尺寸表（"594.96x841.92,595.28x841.89"），
		// 用来区分「是尺寸触发」还是「是文件结构触发」——
		// 前者可以用合成页复现，后者只能靠真实票面。
		if spec := os.Getenv("POCKET_DIAG_NUP_SIZES"); spec != "" {
			sizes = nil
			for _, one := range strings.Split(spec, ",") {
				one = strings.TrimSpace(one)
				var w, h float64
				if _, serr := fmt.Sscanf(one, "%fx%f", &w, &h); serr != nil {
					t.Fatalf("解析尺寸 %q 失败：%v", one, serr)
				}
				sizes = append(sizes, struct{ w, h float64 }{w, h})
			}
			t.Logf("[diag] 尺寸表被 POCKET_DIAG_NUP_SIZES 覆盖为 %v", sizes)
		}
		for i, s := range sizes {
			p := filepath.Join(dir, fmt.Sprintf("src-%02d-%.2fx%.2f.pdf", i, s.w, s.h))
			mkSizedPDF(t, p, s.w, s.h)
			files = append(files, p)
		}
	}

	out, err := ExportInvoiceGrid(dir, files, grid)
	if err != nil {
		t.Fatalf("ExportInvoiceGrid 失败：%v", err)
	}
	t.Logf("[diag] 源页 %d 张（%v）→ 产物 %s", len(sizes), sizes, filepath.Base(out))

	dim, derr, pErr := safePageDims(out)
	if pErr != nil {
		t.Fatalf("读产物页尺寸时 panic：%v", pErr)
	}
	if derr != nil || len(dim) == 0 {
		t.Fatalf("读产物页尺寸失败：%v", derr)
	}
	pw, ph := dim[0].Width, dim[0].Height
	t.Logf("[diag] 产物页 %.2fx%.2f pt，格子应为 %.3fx%.3f", pw, ph, cellW, cellH)
	if math.Abs(pw-a4WidthPt) > 0.5 || math.Abs(ph-a4HeightPt) > 0.5 {
		t.Errorf("产物页不是 A4：%.2fx%.2f（期望 %.2fx%.2f）", pw, ph, a4WidthPt, a4HeightPt)
	}

	pages, _ := api.PageCountFile(out)
	checked, bad := 0, 0
	for p := 1; p <= pages; p++ {
		pls := fullPlacements(t, out, p)
		if len(pls) == 0 {
			t.Errorf("p%d: 没读出任何放置", p)
			bad++
			continue
		}
		for _, pl := range pls {
			// fit-to-cell 的必要条件：源页宽 ≤ cellW/a。
			// 这里不猜是哪张源页（猜不准也没关系），改用**最宽松**的问法：
			// 目录里**最宽**的源页都装不下 ⇒ 必然出格。
			maxSrcW := 0.0
			minSrcW := math.MaxFloat64
			for _, s := range sizes {
				if s.w > maxSrcW {
					maxSrcW = s.w
				}
				if s.w < minSrcW {
					minSrcW = s.w
				}
			}
			need := cellW / pl.a
			checked++
			// 一致性检查：同一页里各放置的缩放是否相同（同页不同缩放
			// 意味着 pdfcpu 对不同源页走了不同分支，是可疑信号）。
			t.Logf("p%d Fm%d: a=%.5f e=%.3f f=%.3f | 源页宽 %d 张，范围 %.2f–%.2f"+
				" ⇒ 装下需要 ≤ %.2f", p, pl.form, pl.a, pl.e, pl.f,
				len(sizes), minSrcW, maxSrcW, need)
			if minSrcW > need {
				// 连最窄的源页都装不下 ⇒ 一定出格
				bad++
				t.Errorf("[出格·必然] p%d Fm%d: a=%.5f ⇒ 源页宽须 ≤ %.2f，"+
					"而本次最窄的源页就有 %.2f ⇒ 越界 %.2fpt（%.1fmm）",
					p, pl.form, pl.a, need, minSrcW, minSrcW*pl.a-cellW,
					(minSrcW*pl.a-cellW)*25.4/72)
			}
		}
	}
	t.Logf("[diag] 汇总：核对 %d 次放置 / 出格 %d", checked, bad)
	if bad == 0 {
		t.Log("[diag] 全部放置的页框都装得进格子 —— **未复现**出格缺陷。" +
			"注意这不等于「缺陷不存在」：若真实语料里的触发条件没有被 sizes 覆盖到，" +
			"这里同样会全绿。真要下结论，得把 sizes 扩到含真实票面尺寸。")
	}
}
