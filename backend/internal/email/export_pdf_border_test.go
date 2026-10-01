package email

// export_pdf_border_test.go — 需求 5「打印后可直接剪裁」的裁切线。
//
// 2026-10-01 复核发现：`export_pdf.go` 原本是 `Border: false`，输出网格
// **没有任何裁切标记**。需求写的是「打印后可直接剪裁」——4/9 张发票的 A4
// 上没有可对齐的切割依据，用户只能凭发票白边目测，剪歪是必然的。
//
// 这条不是「加不加线」的偏好问题，而是需求原文的直接要求。
//
// 验证方式：不只断言参数，而是**真正导出产物**并检查 PDF 里确实画了线。
// 方法是数内容流里的路径/线段绘制算子——Border=false 与 true 的产物字节不同，
// 且文件明显变大（线要占字节）。

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExportInvoiceGrid_DrawsCutLines(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exports")

	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}

	// 先用不画线的变体导出一次作为基线
	withBorder := exportWithBorder(t, out, files, 2, true)
	withoutBorder := exportWithBorder(t, out, files, 2, false) // 负控：对照产物不画线

	// 判据：描边算子（线宽 <n> w）必须更多。字节数不可靠——pdfcpu 每次导出
	// 都写入不同元数据，差值只有几十字节且方向随机（实测 5987 vs 5989）。
	withOps := countStrokeOps(t, withBorder)
	noOps := countStrokeOps(t, withoutBorder)
	t.Logf("stroke ops: with=%d, without=%d", withOps, noOps)
	if withOps <= noOps {
		t.Fatalf("cut lines must be drawn: with=%d stroke ops, without=%d (no growth => nothing drawn)",
			withOps, noOps)
	}

	// 页数与 A4 尺寸不能被加边框影响
	if n := pdfPageCount(t, withBorder); n != 1 {
		t.Fatalf("4 invoices on 2x2 should still be 1 page, got %d", n)
	}
	w, h := pdfFirstPageSize(t, withBorder)
	if absf(w-a4WidthPt) > 1.5 || absf(h-a4HeightPt) > 1.5 {
		t.Fatalf("border must not change page size: %.2fx%.2f pt", w, h)
	}
}

// 3x3 也必须有裁切线（9 格的裁切线更多，字节增长应更明显）。
func TestExportInvoiceGrid_3x3AlsoDrawn(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exports")

	var files []string
	for i := 0; i < 9; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	with := exportWithBorder(t, out, files, 3, true)
	without := exportWithBorder(t, out, files, 3, false)

	withOps := countStrokeOps(t, with)
	noOps := countStrokeOps(t, without)
	t.Logf("3x3 stroke ops: with=%d, without=%d", withOps, noOps)
	if withOps <= noOps {
		t.Fatalf("3x3 cut lines missing: with=%d without=%d", withOps, noOps)
	}
	if n := pdfPageCount(t, with); n != 1 {
		t.Fatalf("9 invoices on 3x3 should be 1 page, got %d", n)
	}
}

// countStrokeOps 解压 PDF 内容流，数「描边路径」算子（`<w>` 线宽 + `m`/`l` 画点连线 + `s` 描边）。
//
// 为什么不能比字节数：pdfcpu 每次导出会写入不同元数据（CreationDate 等），
// 大小差只有几十字节且方向随机（实测 5987 vs 5989）——靠字节判定会假绿。
//
// 为什么不能直接在原始字节里数：产物是 FlateDecode 压缩流（实测 11 处），
// 算子被压缩，原始字节里搜不到（实测为 0）。必须先 inflate。
//
// 判据取自真实产物内容（实测）：
//
//	有线: "[]0 d 0.1 w 0.00 420.94 m 297.64 420.94 l ... s"
//	无线: "q 0.50000 ... cm /Fm1 Do Q"        ← 只有放置发票，没有路径
func countStrokeOps(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ops := 0
	idx := 0
	for {
		i := bytes.Index(raw[idx:], []byte("stream"))
		if i < 0 {
			break
		}
		i += idx + len("stream")
		for i < len(raw) && (raw[i] == '\r' || raw[i] == '\n') {
			i++
		}
		j := bytes.Index(raw[i:], []byte("endstream"))
		if j < 0 {
			break
		}
		if zr, err := zlib.NewReader(bytes.NewReader(raw[i : i+j])); err == nil {
			data, _ := io.ReadAll(zr)
			// 线宽算子： "<n> w"（0.1 w / 0.5 w …）——只有画线才会出现
			for k := 0; k < len(data)-2; k++ {
				if data[k+1] == ' ' && data[k+2] == 'w' &&
					data[k] >= '0' && data[k] <= '9' {
					ops++
				}
			}
			zr.Close()
		}
		idx = i + j + len("endstream")
	}
	return ops
}

// TestExportInvoiceGrid_ProductionPathDrawsLines 直接走**生产入口**
// ExportInvoiceGrid（不是 exportNUp），否则测试只覆盖了带 border 参数的
// 那个变体——2026-10-01 的负控就栽在这里：把生产入口的 border 关掉，
// 其它用例照样绿。
//
// 判据：生产入口产物里的描边算子必须**多于**不画线变体。
func TestExportInvoiceGrid_ProductionPathDrawsLines(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exports")
	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	prodPath, err := ExportInvoiceGrid(out, files, 2) // 生产入口
	if err != nil {
		t.Fatalf("production export: %v", err)
	}
	noBorder := exportWithBorder(t, out, files, 2, false) // 明确不画线
	prodOps := countStrokeOps(t, prodPath)
	noOps := countStrokeOps(t, noBorder)
	t.Logf("stroke ops: production=%d, no-border=%d", prodOps, noOps)
	// 判据：必须**严格多于**不画线变体。
	//
	// 我一开始写成「>= 24（4 格 x 4 边）」并据此判失败——那个推算**是错的**：
	// 实测 2x2 画线只有 12、不画线 8（只 +4），3x3 是 27 vs 18（+9）。
	// pdfcpu 复用相邻格共享的边界，不是每格各画 4 条。
	// 正确判据就是「严格多于」：发票自身的边框线是固定基线，裁切线在其之上。
	if prodOps <= noOps {
		t.Fatalf("production path must draw cut lines, but it has no more stroke ops than the no-border variant (prod=%d noBorder=%d)", prodOps, noOps)
	}
}

// exportWithBorder 用指定 Border 设置导出一份网格 PDF，返回文件路径。
// Border 是 exportNUp 的参数——生产代码默认 true，这里只为对照。
func exportWithBorder(t *testing.T, outDir string, files []string, grid int, border bool) string {
	t.Helper()
	res, err := exportNUp(outDir, files, grid, border)
	if err != nil {
		t.Fatalf("export border=%v: %v", border, err)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil || len(b) == 0 {
		t.Fatalf("empty output %s: %v", res.Path, err)
	}
	// 文件名含时间戳，同一秒两次导出会撞名；确保读到的是本次内容
	if !bytes.Contains(b, []byte("%%EOF")) {
		t.Fatalf("output %s is not a complete pdf (%d bytes)", res.Path, len(b))
	}
	return res.Path
}
