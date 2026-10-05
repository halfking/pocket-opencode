package email

// diag_real_voucher_geometry_test.go — **只读**诊断：量真实发票票面的页面几何，
// 为 `bannerAspectRatio` 提供「不是算出来、也不是横幅自证」的第三个数据点。
//
// ## 为什么需要它（round44 遗留风险 3 的正面回答）
//
// round43 把 `bannerAspectRatio = 2.5` 定下来时，支撑它的实测数据点**只有一个**：
// 那张 572×140 的百望宣传横幅（比例 4.086）。A4 的 0.707/1.414 是**按纸张规格算的**，
// 不是从真实票面量出来的。于是这个阈值的处境是：
//
//	拒的一侧：572×140 = 4.086（真实观测，1 个点）
//	放的一侧：A4 竖 0.707 / A4 横 1.414（**推算**，不是观测）
//
// 遗留问题因此是具体的：「什么样的**真票**会超过 2.5」没有真实观测支撑。
//
// ## ⚠ 本诊断能证明什么、不能证明什么（先说清，避免被读成「标定完成」）
//
// 能：真实**电子发票 PDF** 的票面长宽比实测分布 ⇒ 检验 2.5 在真票上的余量。
//
// **不能**：**像素尺寸下限**。PDF 的 MediaBox 单位是**点（pt，1/72 英寸）**，
// 不存在「这张票有多少像素」这件事——同一张 A4 可以被渲染成 72dpi 的
// 595×842，也可以是 300dpi 的 2480×3508，两者都是同一张票。
// ⇒ 由本诊断推不出任何像素下限，`minVoucherPixels` 这类阈值**仍然没有标定依据**。
// 要标定它必须有**拍照**样本（手机/扫描仪输出的栅格图）。
//
// 这一点是本诊断存在的一半理由：把「PDF 量得出来、量不出来什么」钉死，
// 免得下一轮看到一份 PDF 几何报告就以为下限可以加了。
//
// ## 为什么用 pdfcpu 而不是正则扫字节
//
// 仓库里 `diag_real_exports_test.go` 已经踩过：正则扫 PDF 原始字节会**先命中**
// 页面里内嵌 Form XObject 自带的 /MediaBox，量到的是内部表单不是票面。
// 本诊断走 `api.PageMap` 的页面配置，取的是**页面级** MediaBox。
//
// ## 只读
//
// 只 t.Log；不写数据目录、不写 exports、不连数据库。门控一个环境变量 + 目录，
// 目录**无缺省值**（缺省会静默量到 0 个文件然后「全绿」）。
//
//	go test ./internal/email/ -run DiagRealVoucherGeometry -v \
//	  （先设 POCKET_DIAG_VOUCHER_GEOM=1 与 POCKET_REAL_INVOICE_DIR）

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDiagRealVoucherGeometry(t *testing.T) {
	if os.Getenv("POCKET_DIAG_VOUCHER_GEOM") != "1" {
		t.Skip("set POCKET_DIAG_VOUCHER_GEOM=1 (+ POCKET_REAL_INVOICE_DIR) to measure real voucher page geometry (read-only)")
	}
	dir := os.Getenv("POCKET_REAL_INVOICE_DIR")
	if dir == "" {
		t.Fatal("POCKET_REAL_INVOICE_DIR 未设置（本诊断无缺省值）——" +
			"没有它会量到 0 个文件然后「全绿」，那正是本诊断要防的失败模式")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var pdfs []string
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
			continue
		}
		pdfs = append(pdfs, e.Name())
	}
	sort.Strings(pdfs)
	// 0 个文件 ⇒ **必须**报红。「目录里没有票」和「票都合格」在输出上长得一样，
	// 而这两种情况的处置完全相反，所以这里只认「量到了东西」为通过。
	if len(pdfs) == 0 {
		t.Fatalf("目录 %s 里没有 .pdf —— 无法标定。请检查 POCKET_REAL_INVOICE_DIR 是否指向真实发票落盘目录", dir)
	}

	type row struct {
		name  string
		w, h  float64
		ratio float64
		perr  string
	}
	rows := make([]row, 0, len(pdfs))
	badParse := 0
	for _, name := range pdfs {
		p := filepath.Join(dir, name)
		// 复用 diag_a4_cell_fit_test.go 里已有的 safePageDims（3 返回值，含 panic）。
		// **不要**在本文件再写一份：本轮已经因为重造它撞了一次 redeclared，
		// 而那份已有实现的注释恰好记录了同一个 pdfcpu panic —— 两处会分叉。
		//
		// 实测（2026-10-04，真实落盘目录 12 个 PDF）：2 个 panic 的文件是
		// `其他-财务部-0.00-2026-09-30.pdf` 与 `…-2026-10-01.pdf`，
		// 各 **69 字节**、SHA256 相同（cfa3181c1ee36e8b…）——退化件，
		// 正是 round37 那批自注入数据，**不是真实发票**。
		// ⇒ 「真实电子票面会触发 pdfcpu panic」这个说法是错的，不要外传。
		// 而且生产侧早已防住：`pdfHasPages`（invoice_file.go）与
		// `pdfPageCountSafe`（export_pdf.go）都带 recover，注释也记着这件事，
		// 所以这里 panic **不是**新的生产风险，只是让「量不到 2 个文件」。
		dims, derr, panicked := safePageDims(p)
		switch {
		case panicked != nil:
			rows = append(rows, row{name: name, perr: fmt.Sprintf("PANIC in pdfcpu: %v", panicked)})
			badParse++
			continue
		case derr != nil:
			rows = append(rows, row{name: name, perr: fmt.Sprintf("PageDimsFile: %v", derr)})
			badParse++
			continue
		case len(dims) == 0:
			rows = append(rows, row{name: name, perr: "PageDimsFile returned 0 pages (no /Type /Page found?)"})
			badParse++
			continue
		}
		// 逐页量，而不是只量第一页：真票可能是多页/拼接的，
		// 只看第一页等于默认「所有真票都是单页」这个没被验证的前提。
		for i, d := range dims {
			w, h := d.Width, d.Height
			lo, hi := w, h
			if hi < lo {
				lo, hi = hi, lo
			}
			ratio := 1.0
			if lo > 0 {
				ratio = hi / lo
			}
			rows = append(rows, row{
				name:  fmt.Sprintf("%s#p%d", name, i+1),
				w:     w,
				h:     h,
				ratio: ratio,
			})
		}
	}

	t.Logf("=== 真实票面几何：目录 %s，共 %d 个 PDF，%d 个页面 ===", dir, len(pdfs), len(rows))
	t.Logf("%-58s %10s %10s %8s", "page", "MediaBoxW", "MediaBoxH", "ratio")
	measured := 0
	var minR, maxR float64
	for _, r := range rows {
		if r.perr != "" {
			t.Logf("%-58s  PARSE-FAIL: %s", r.name, r.perr)
			continue
		}
		measured++
		if minR == 0 || r.ratio < minR {
			minR = r.ratio
		}
		if r.ratio > maxR {
			maxR = r.ratio
		}
		t.Logf("%-58s %10.2f %10.2f %8.3f", r.name, r.w, r.h, r.ratio)
	}
	if measured == 0 {
		t.Fatalf("%d 个 PDF 全部解析失败 —— 本诊断没有量到任何东西，结论不成立", len(pdfs))
	}
	t.Logf("")
	t.Logf("可解析页面 %d / 总页面 %d（解析失败 %d）", measured, len(rows), badParse)
	t.Logf("真实电子票面长宽比：min=%.3f  max=%.3f", minR, maxR)

	// 参照物：round43 的唯一实测数据点（百望宣传横幅，非发票）。
	// 放在同一张表里对照，才看得出 2.5 落在「真票分布」与「横幅」之间的什么位置。
	const bannerObserved = 4.086
	const threshold = bannerAspectRatio
	t.Logf("")
	t.Logf("=== 阈值定位 ===")
	t.Logf("bannerAspectRatio 阈值            = %.2f", threshold)
	t.Logf("真票实测最大长宽比               = %.3f", maxR)
	t.Logf("余量 = 阈值 - 真票最大值          = %.3f  （>0 表示实测真票全部放行）", threshold-maxR)
	t.Logf("横幅实测长宽比（参照，非发票）    = %.3f", bannerObserved)
	t.Logf("横幅余量 = 横幅 - 阈值             = %.3f", bannerObserved-threshold)
	if maxR >= threshold {
		t.Errorf("真实电子票里出现了长宽比 %.3f ≥ 阈值 %.2f 的页面：电子票路径会被误拒。"+
			"这说明 bannerAspectRatio 的取值不能只用「横幅 vs A4」两端定。", maxR, threshold)
	}
	if bannerObserved <= threshold {
		t.Errorf("横幅实测长宽比 %.3f ≤ 阈值 %.2f：横幅不会被拒，闸门对已知真实横幅失效。",
			bannerObserved, threshold)
	}
	t.Logf("")
	t.Logf("⚠ 像素尺寸下限：本诊断**测不出来**。MediaBox 单位是 pt（1/72 英寸），不是像素。")
	// 用变量而不是常量表达式：常量里的浮点运算在这里会被当成可转换常量而报错，
	// 而这里的意图本来就是「跑一次算术」，不是要一个编译期常量。
	var a4w, a4h = 595.28, 841.89
	t.Logf("  同一张 A4 渲染成 72dpi 是 %d×%d、300dpi 是 %d×%d，都是同一张票。",
		int(a4w/72*72), int(a4h/72*72), int(a4w/72*300), int(a4h/72*300))
	t.Logf("  ⇒ minVoucherPixels 类阈值仍然**没有标定依据**，本轮不得新增。")
}
