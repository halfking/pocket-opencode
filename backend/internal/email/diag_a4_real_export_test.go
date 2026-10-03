package email

// diag_a4_real_export_test.go — **只读**诊断：用**当前代码**对真实台账里的发票跑一遍
// A4 网格导出，并把磁盘上的历史产物逐个对照。
//
// ## 为什么需要它
//
// 需求写死的是「按照 A4 纸张规范排版，每页可容纳多张发票，打印后可直接剪裁」。
// 这一环此前只有代码级结论，缺一次拿**真实票面**的端到端核对。
//
// ## 三条判据的来历（都踩过坑才定成这样）
//
// 1. **落位数**走 `fullPlacements`（`api.ExtractContentFile` 取页级内容流再解析
//    `q a b c d e f cm /FmN Do Q`）。第一版用 `Do` 正则扫**整份 PDF 的所有流**，
//    结果报「落位 105」——源发票内部自带的 Form 也被算了进去。判据指向了容器。
// 2. **别用页数×每格容量**反推张数。那只是**容量**，空格子照样计入。
//    仓库里 `diag_real_exports_test.go` 早就因为「正则扫原始字节会先命中内嵌 Form
//    自带的 /MediaBox」踩过同一类坑，本判据与它同源。
// 3. **票号只认台账里的已知精确串**，不做 `\d{20}` 模糊匹配——第一版那么写，
//    匹配出 `00010002000300040005` 这种明显是 PDF 结构噪声的假票号。
//
// ⚠ 本诊断**不能**证明什么：票号只在 `inflateAllStreams` 能解出的流里找。
// 若某个票号没找到，只能说「本判据没覆盖到」，**不能**据此断言「产物里没有它」。
//
// ## 只读
//
// 输出只写 t.TempDir()；对生产数据目录与 exports 目录**只读**。
// 门控 POCKET_DIAG_A4_REAL=1 + 显式 DSN/SCHEMA/DATA_DIR（无缺省值）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// fixtureInvoiceNo 是 `gen_fixture_invoice_test.go` 造的那张假发票的票号。
// 它出现在 09-30/10-01 的老产物里；**出现在产物里 = 混进了测试夹具**。
const fixtureInvoiceNo = "25332000000123456789"

func TestDiagA4RealExport(t *testing.T) {
	if os.Getenv("POCKET_DIAG_A4_REAL") != "1" {
		t.Skip("set POCKET_DIAG_A4_REAL=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dsn == "" || schema == "" || dataDir == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN / SCHEMA / DATA_DIR 必须显式给全，不设缺省值")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	safe := pgx.Identifier{schema}.Sanitize()
	cfg.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(c, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := conn.Exec(c, "SET search_path TO "+safe)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT file_name, file_path, seller, amount, invoice_no
		   FROM email_invoices
		  WHERE file_path IS NOT NULL AND file_path <> ''
		  ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type inv struct {
		name, path, seller, no string
		amt                    float64
	}
	var list []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.name, &i.path, &i.seller, &i.amt, &i.no); err != nil {
			t.Fatalf("scan: %v", err)
		}
		list = append(list, i)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("台账里没有带文件的发票行：先确认数据，别把空结果当成「导出正常」")
	}

	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dataDir, p)
	}

	var known []string
	seenNo := map[string]bool{}
	for _, i := range list {
		if i.no != "" && !seenNo[i.no] {
			seenNo[i.no] = true
			known = append(known, i.no)
		}
	}
	sort.Strings(known)

	t.Logf("=== 台账有文件的发票 %d 张；file_path 是**相对** DataDir 的 ===", len(list))
	t.Logf("    已知真实票号 %d 个: %s", len(known), strings.Join(known, ", "))
	var files []string
	for _, i := range list {
		full := resolve(i.path)
		st, err := os.Stat(full)
		if err != nil {
			t.Logf("  [文件缺失] %s → %s err=%v", i.name, full, err)
			continue
		}
		dims, _ := api.PageDimsFile(full)
		ds, tag := "?", ""
		if len(dims) > 0 {
			ds = fmt.Sprintf("%.0fx%.0fmm", dims[0].Width*25.4/72, dims[0].Height*25.4/72)
			w, h := dims[0].Width, dims[0].Height
			if w > h {
				w, h = h, w
			}
			if absF(w-a4WidthPt) > 1.5 || absF(h-a4HeightPt) > 1.5 {
				tag = "  ← 源件非 A4（导出时会被归一）"
			}
		}
		files = append(files, full)
		t.Logf("  %-50s %7dB  %-10s 票号=%s%s", truncStr(i.name, 48), st.Size(), ds, i.no, tag)
	}
	if len(files) == 0 {
		t.Fatal("台账有文件的行没有一个能在磁盘上找到：先查 DataDir 口径")
	}

	// ---- 当前代码实跑 ----
	t.Logf("=== 当前代码实跑（输出只写临时目录）===")
	for _, grid := range []int{2, 3} {
		outDir := filepath.Join(t.TempDir(), fmt.Sprintf("g%d", grid))
		res, err := ExportInvoiceGridDetailed(outDir, files, grid)
		if err != nil {
			t.Errorf("grid=%d 导出失败: %v", grid, err)
			continue
		}
		st, _ := os.Stat(res.Path)
		dims, _ := api.PageDimsFile(res.Path)
		pages, _ := api.PageCountFile(res.Path)
		per := grid * grid
		want := (len(files) + per - 1) / per
		ds, tag := "?", ""
		if len(dims) > 0 {
			ds = fmt.Sprintf("%.2fx%.2fpt", dims[0].Width, dims[0].Height)
			if absF(dims[0].Width-a4WidthPt) > 0.5 || absF(dims[0].Height-a4HeightPt) > 0.5 {
				tag = " ← 首屏非 A4"
			}
		}
		placed, perPage := placedByParser(t, res.Path, pages)
		hit, miss := findKnownNos(res.Path, known)
		t.Logf("  grid=%d: 送入%d/入网格%d/跳过%v → %d页(容量上限%d页) %s%s  %dB",
			grid, len(files), res.Count, res.Skipped, pages, want, ds, tag, st.Size())
		t.Logf("      真实落位 %d 个（解析器逐页数 Do）每页=%v", placed, perPage)
		t.Logf("      命中真实票号 %d/%d: %s", len(hit), len(known), strings.Join(hit, ","))
		if len(miss) > 0 {
			t.Logf("      未命中(不代表不存在，只代表本判据没覆盖到): %s", strings.Join(miss, ","))
		}
	}

	// ---- 历史产物 ----
	expDir := filepath.Join(dataDir, "email-invoices", "exports", "ws_user-admin")
	ents, err := os.ReadDir(expDir)
	if err != nil {
		t.Logf("exports 目录读不到: %v", err)
		return
	}
	var a4s []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "invoices-a4-") &&
			strings.HasSuffix(e.Name(), ".pdf") {
			a4s = append(a4s, filepath.Join(expDir, e.Name()))
		}
	}
	sort.Strings(a4s)
	t.Logf("=== 历史 A4 产物 %d 个（落位用解析器数，不按页数×容量推）===", len(a4s))
	for _, p := range a4s {
		st, _ := os.Stat(p)
		pages, err := api.PageCountFile(p)
		if err != nil {
			t.Logf("  [读不出] %s: %v", filepath.Base(p), err)
			continue
		}
		dims, _ := api.PageDimsFile(p)
		ds := "?"
		if len(dims) > 0 {
			ds = fmt.Sprintf("%.2fx%.2fpt", dims[0].Width, dims[0].Height)
		}
		placed, perPage := placedByParser(t, p, pages)
		hit, _ := findKnownNos(p, known)
		fix := ""
		if hasFix := strings.Contains(readInflated(p), fixtureInvoiceNo); hasFix {
			fix = "  ← **含测试夹具**"
		}
		t.Logf("  %-42s %7dB %d页 %-15s 落位%2d 每页%v 真票号%d/%d%s",
			filepath.Base(p), st.Size(), pages, ds, placed, perPage,
			len(hit), len(known), fix)
	}
}

// placedByParser 用 fullPlacements（页级内容流 + CTM 解析）数真实落位数。
func placedByParser(t *testing.T, path string, pages int) (int, []int) {
	t.Helper()
	per := make([]int, 0, pages)
	total := 0
	for p := 1; p <= pages; p++ {
		n := len(fullPlacements(t, path, p))
		per = append(per, n)
		total += n
	}
	return total, per
}

// findKnownNos 只找**已知精确串**。第二返回值是「没找到的」——它只说明
// 本判据没覆盖到，不等于产物里没有那张票。
func findKnownNos(path string, known []string) (hit, miss []string) {
	hay := readInflated(path)
	for _, k := range known {
		if strings.Contains(hay, k) {
			hit = append(hit, k)
		} else {
			miss = append(miss, k)
		}
	}
	return
}

func readInflated(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b) + "\n" + inflateAllStreams(b)
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
