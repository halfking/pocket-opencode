package email

// diag_real_exports_test.go — **诊断**：用真实导出产物验 A4 网格几何。
//
// 门禁：必须显式设 POCKET_DIAG_REAL_EXPORTS=1 并让 POCKET_DIAG_EXPORT_DIR
// 指向真实的 exports 目录（只读，不写不改）。
//
// 为什么需要：7c287548 的几何断言跑在**单测夹具**上（夹具是自己造的发票
// PDF）。真实产物来自 pdfcpu NUp 对真实发票的处理，落点/缩放/页数都可能
// 与夹具不同。需求原文要求「打印后可直接剪裁」——只有对真实产物验过格子
// 才算兑现。
//
// 判据用**解析器**而不是扫字节：页尺寸走 api.PageDimsFile，网格落点走
// api.ExtractContentFile 取内容流再解析 `q a b c d e f cm /FmN Do Q`。
// （用正则扫 PDF 原始字节会先命中页面里内嵌 Form XObject 自带的 /MediaBox，
// 那是源发票自己的尺寸，不是输出页的。）

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

const (
	realA4Width  = 595.276
	realA4Height = 841.89
)

func TestDiagRealExportGridGeometry(t *testing.T) {
	if os.Getenv("POCKET_DIAG_REAL_EXPORTS") != "1" {
		t.Skip("set POCKET_DIAG_REAL_EXPORTS=1 and POCKET_DIAG_EXPORT_DIR=<dir> to run (read-only)")
	}
	dir := os.Getenv("POCKET_DIAG_EXPORT_DIR")
	if dir == "" {
		t.Skip("POCKET_DIAG_EXPORT_DIR not set")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "invoices-a4-*-*-*.pdf"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("glob in %s: n=%d err=%v", dir, len(matches), err)
	}
	sort.Strings(matches)
	t.Logf("found %d real export PDFs in %s", len(matches), dir)

	bad := 0
	for _, path := range matches {
		base0 := path
		base := filepath.Base(base0)
		// invoices-a4-<grid>x<grid>-<yyyymmdd>-<hhmmss>.pdf
		//   parts: [invoices, a4, 2x2, 20260930, 233446.pdf]
		parts := strings.Split(base, "-")
		if len(parts) < 5 {
			t.Errorf("%s: unexpected filename shape (parts=%d)", base, len(parts))
			bad++
			continue
		}
		gridPart := strings.SplitN(parts[2], "x", 2)
		if len(gridPart) != 2 || gridPart[0] != gridPart[1] {
			t.Errorf("%s: cannot read grid from %q", base, parts[2])
			bad++
			continue
		}
		grid, err := strconv.Atoi(gridPart[0])
		if err != nil || grid <= 0 {
			t.Errorf("%s: bad grid %q", base, parts[2])
			bad++
			continue
		}

		dim, err := api.PageDimsFile(path)
		if err != nil || len(dim) == 0 {
			t.Errorf("%s: page dims: %v", base, err)
			bad++
			continue
		}
		w, h := dim[0].Width, dim[0].Height
		// 有些导出按页旋转，尺寸顺序可能互换，先归一。
		sw, sh := math.Min(w, h), math.Max(w, h)
		if math.Abs(sw-realA4Width) > 0.5 || math.Abs(sh-realA4Height) > 0.5 {
			t.Errorf("%s: page %.2fx%.2f pt 不是 A4 (%.2fx%.2f)", base, w, h, realA4Width, realA4Height)
			bad++
			continue
		}

		pages, err := api.PageCountFile(path)
		if err != nil {
			t.Errorf("%s: page count: %v", base, err)
			bad++
			continue
		}
		for p := 1; p <= pages; p++ {
			ps := pagePlacements(t, path, p)
			if len(ps) == 0 {
				t.Errorf("%s p%d: 没有读出任何发票放置（网格为空）", base, p)
				bad++
				continue
			}
			// 缩放必须正好是 1/grid。
			wantScale := 1.0 / float64(grid)
			badScale := 0
			for _, pl := range ps {
				if math.Abs(pl.scale-wantScale) > 0.005 {
					badScale++
				}
			}
			// 落点必须互不相同（按 0.1pt 量化），否则就是叠印、剪裁不出来。
			cells := map[[2]int64]int{}
			for _, pl := range ps {
				cells[[2]int64{int64(math.Round(pl.x * 10)), int64(math.Round(pl.y * 10))}]++
			}
			overlap := 0
			for _, n := range cells {
				if n > 1 {
					overlap++
				}
			}
			// 列/行落点各自去重后不能超过 grid（否则不是 grid 宽的网格）。
			xs := map[int64]bool{}
			ys := map[int64]bool{}
			for _, pl := range ps {
				xs[int64(math.Round(pl.x*10))] = true
				ys[int64(math.Round(pl.y*10))] = true
			}
			t.Logf("%s p%d: grid=%d placed=%d distinctCells=%d distinctX=%d distinctY=%d scale=%.4f(want %.4f) A4=%.2fx%.2f",
				base, p, grid, len(ps), len(cells), len(xs), len(ys), ps[0].scale, wantScale, w, h)
			if badScale > 0 {
				t.Errorf("%s p%d: %d/%d 张发票缩放不是 1/%d", base, p, badScale, len(ps), grid)
				bad++
			}
			if overlap > 0 {
				t.Errorf("%s p%d: %d 个落点被多张发票共用 —— 剪裁后会重叠", base, p, overlap)
				bad++
			}
			if len(cells) != len(ps) {
				t.Errorf("%s p%d: 放置数 %d != 互异落点数 %d", base, p, len(ps), len(cells))
				bad++
			}
			if len(xs) > grid || len(ys) > grid {
				t.Errorf("%s p%d: 落点列数 %d / 行数 %d 超过 grid=%d", base, p, len(xs), len(ys), grid)
				bad++
			}
		}
	}
	if bad > 0 {
		t.Errorf("共 %d 项不符合预期（详见上面各条）", bad)
	} else {
		t.Logf("全部 %d 个真实导出产物的几何均符合预期", len(matches))
	}
	_ = fmt.Sprint
}
