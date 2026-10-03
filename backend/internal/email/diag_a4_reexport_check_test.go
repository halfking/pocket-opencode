package email

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// diag_a4_reexport_check_test.go —— **只读**诊断：拿**真实票面**跑一遍
// **当前生产代码**的导出路径，再用量墨迹判据检查产物。
//
// ## 它回答的是哪个问题
//
// 第十一节已测出：磁盘上那批历史 A4 里 4 张票越线（31.1mm / 7.7mm）。
// 但那批产物是**修复前**的二进制生成的。用户真正要决定的是
// 「重出一次到底会不会好」。
//
// 答案不能靠「代码里写了修复」来给——必须拿同一批真实票面过一遍
// 当前的 `ExportInvoiceGridDetailed`，再量它的输出。
//
// 本诊断**不写生产目录、不碰数据库**：产物全部落在 `t.TempDir()`。
//
// ## 门禁
//
// POCKET_DIAG_REEXPORT=1 + POCKET_DIAG_INVOICE_DIR（真实票面目录，无缺省值）。
// grid 走 POCKET_DIAG_REEXPORT_GRID，默认 "2,3"。
//
// ## 判红条件
//
// 任何一处墨迹越界 ⇒ 红。页框越界但墨迹不出格 ⇒ 也红（那说明
// 修复只解决了一半：票面还有一整条白边挂在格外，裁切仍不可靠）。

func TestA4ReexportWithCurrentCodeIsClean(t *testing.T) {
	if os.Getenv("POCKET_DIAG_REEXPORT") != "1" {
		t.Skip("set POCKET_DIAG_REEXPORT=1 (+ POCKET_DIAG_INVOICE_DIR)")
	}
	dir := os.Getenv("POCKET_DIAG_INVOICE_DIR")
	if dir == "" {
		t.Fatal("POCKET_DIAG_INVOICE_DIR 需显式传入（无缺省值）")
	}
	grids := []int{2, 3}
	if v := os.Getenv("POCKET_DIAG_REEXPORT_GRID"); v != "" {
		grids = nil
		for _, s := range splitNonEmpty(v, ",") {
			g := 0
			for _, c := range s {
				if c < '0' || c > '9' {
					t.Fatalf("POCKET_DIAG_REEXPORT_GRID 里有非数字：%q", v)
				}
				g = g*10 + int(c-'0')
			}
			if g < 1 {
				t.Fatalf("grid 至少为 1：%q", v)
			}
			grids = append(grids, g)
		}
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读不到 %s：%v", dir, err)
	}
	var files []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".pdf" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("%s 里没有 PDF —— 判据会输出「一切正常」的假结论", dir)
	}
	t.Logf("真实票面 %d 个：", len(files))
	for _, f := range files {
		st, _ := os.Stat(f)
		t.Logf("  %8d B  %s", st.Size(), filepath.Base(f))
	}

	for _, grid := range grids {
		t.Run(fmt.Sprintf("grid%dx%d", grid, grid), func(t *testing.T) {
			out := t.TempDir()
			res, err := ExportInvoiceGridDetailed(out, files, grid)
			if err != nil {
				t.Fatalf("导出失败：%v", err)
			}
			t.Logf("导出 %s：入网 %d 张，跳过 %d 张（%v）",
				filepath.Base(res.Path), res.Count, len(res.Skipped), res.Skipped)

			f, err := os.Open(res.Path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := inkOpenXRef(f)
			st, _ := f.Stat()
			f.Close()
			if err != nil {
				t.Fatalf("打开产物：%v", err)
			}
			placed, frameOver, inkOver, bounded, unplaced := 0, 0, 0, 0, 0
			for pn := 1; pn <= ctx.PageCount; pn++ {
				pg := inkCheckA4(ctx, pn, grid)
				if !pg.parseOK {
					t.Errorf("p%d 判据没能解析产物：%s", pn, pg.parseMsg)
					continue
				}
				for _, v := range pg.verdicts {
					placed++
					bounded += v.bounded
					if v.col < 0 {
						unplaced++
						t.Errorf("p%d 有一张票的中心落在所有格子之外：frame=%+v", pn, v.frameBox)
						continue
					}
					t.Logf("  p%d 格(%d,%d) 页框越界=%.2f 墨迹越界=%.2f 字宽上界=%d",
						pn, v.col, v.row, v.frameOver, v.inkOver, v.bounded)
					if v.frameOver > inkTolPt {
						frameOver++
					}
					if v.inkOver > inkTolPt {
						inkOver++
					}
				}
			}
			t.Logf("grid%dx%d 汇总（%d 字节，%d 页）：放置 %d，页框越界 %d，墨迹越界 %d，字宽上界 %d，无法归格 %d",
				grid, grid, st.Size(), ctx.PageCount, placed, frameOver, inkOver, bounded, unplaced)
			if inkOver > 0 {
				t.Errorf("当前代码的导出产物里仍有 %d 张票的墨迹越过裁切线", inkOver)
			}
			if frameOver > inkOver {
				t.Errorf("页框越界 %d 处但墨迹越界只有 %d 处 —— 裁切线仍压在白边上，剪裁不可靠",
					frameOver, inkOver)
			}
			if unplaced > 0 {
				t.Errorf("有 %d 张票无法归格，判据覆盖不完整", unplaced)
			}
		})
	}
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if string(c) == sep {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
