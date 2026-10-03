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

// cellOriginTolPt 是落点聚类容差（pt）。
//
// **不是拍脑袋的数**：票面页宽有 595.0 与 595.3 两种实测值（A4 舍入差），
// 居中后左边缘因此差约 0.15pt。原实现按 0.1pt 量化，0.15pt 活了下来，
// 于是 2×2 的四张票被读成「4 个不同列」，**对完全正确的产物报红**。
//
// 0.5pt = 0.18mm：远小于任何与剪裁相关的量（刀口公差以毫米计），
// 又足以吸收上面那点页宽抖动。
//
// ⚠ 这个容差只用于**落点是否构成网格**的判定。它**不能**用来判定
// 「票面有没有压到邻格」——那要看墨迹范围，需要栅格化，而 pdfcpu v0.11
// 没有渲染 API。已知的 0.55904 那个外层 cm 缩放也**不是**这个含义
// （见下面注释），因此本诊断不再据此判红。
const cellOriginTolPt = 0.5

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
			// 缩放：外层 cm 的 a/d **不是**「页面→格子」的比例。
			//
			// 实测（2026-10-03 23:31，用同一个文件的 4 份字节相同副本导出，
			// 落点实测 x∈{0,297.64}、y∈{532.375,111.43}，是标准 2×2）：
			// 外层 cm 恒为 a=d=0.55904，grid=3 时恒为 0.3727 —— 两者与
			// 1/grid 的比值都是 1.118，即这个数与「是否网格正确」无关。
			// 原实现按「缩放必须正好是 1/grid」判红，于是**对完全正确的产物
			// 报红**（我据此差点去改本来正常的导出代码）。
			//
			// 真正的「票面有没有压到邻格」取决于**墨迹**范围，而页框溢出 ≠
			// 墨迹碰撞：票面在页内常带旋转与大量白边（实测通行费票面内容流
			// 首层变换是 a=0.24 d=-0.24 的 90° 旋转 + 0.24 缩放）。
			// 要判墨迹需要栅格化，而 pdfcpu v0.11 没有渲染 API。
			// ⇒ 这里只**记录**缩放，不据此判红；把「能不能剪裁」如实留作未验证。
			wantScale := 1.0 / float64(grid)
			// 落点互不相同（按 cellOriginTolPt 聚类），否则就是叠印、剪裁不出来。
			cells := map[[2]int64]int{}
			for _, pl := range ps {
				cells[[2]int64{
					int64(math.Round(pl.x / cellOriginTolPt)),
					int64(math.Round(pl.y / cellOriginTolPt)),
				}]++
			}
			overlap := 0
			for _, n := range cells {
				if n > 1 {
					overlap++
				}
			}
			// 列/行：**列**按落点 x 聚类（票面页宽差 0.3pt 只造成 0.15pt 抖动，
			// 已被 cellOriginTolPt 吸收）；**行**不能按落点 y 聚类——票面高度
			// 不同、居中后 y 天然不同（实测 3500 那张只有 396.9 高，在 420.9
			// 的格子里居中，上下各留 6pt，于是 2×2 的一页能读出 4 个不同 y）。
			// 那不是「不在网格上」，恰恰是「每张票都待在自己格子的中央」。
			// 行数必须按**格高**把落点归到行号来数。
			xs := map[int64]bool{}
			for _, pl := range ps {
				xs[int64(math.Round(pl.x/cellOriginTolPt))] = true
			}
			cellH := h / float64(grid)
			yMin := ps[0].y
			for _, pl := range ps {
				if pl.y < yMin {
					yMin = pl.y
				}
			}
			rows := map[int64]bool{}
			for _, pl := range ps {
				rows[int64(math.Round((pl.y-yMin)/cellH))] = true
			}
			t.Logf("%s p%d: grid=%d placed=%d distinctCells=%d distinctX=%d rows=%d scale=%.4f(1/%d=%.4f, 仅记录不判定) A4=%.2fx%.2f",
				base, p, grid, len(ps), len(cells), len(xs), len(rows), ps[0].scale, grid, wantScale, w, h)
			if overlap > 0 {
				t.Errorf("%s p%d: %d 个落点被多张发票共用 —— 剪裁后会重叠", base, p, overlap)
				bad++
			}
			if len(cells) != len(ps) {
				t.Errorf("%s p%d: 放置数 %d != 互异落点数 %d", base, p, len(ps), len(cells))
				bad++
			}
			if len(xs) > grid {
				t.Errorf("%s p%d: 落点列数 %d 超过 grid=%d（聚类容差 %.2fpt）",
					base, p, len(xs), grid, cellOriginTolPt)
				bad++
			}
			if len(rows) > grid {
				t.Errorf("%s p%d: 落点行数 %d 超过 grid=%d（按格高 %.2fpt 归行）",
					base, p, len(rows), grid, cellH)
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
