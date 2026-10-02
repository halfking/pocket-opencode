package email

// export_pdf_geometry_test.go —— 补上需求 5 几何验收里**唯一没被覆盖**的一档。
//
// ## 已被覆盖的部分（不要重复造）
//
// export_pdf_test.go 已有：
//   - TestExportInvoiceGrid_2x2FitsOneA4Page：4 张 2x2 ⇒ 1 页，且首屏是 A4
//   - TestExportInvoiceGrid_PageCountFollowsCeiling：5 张 2x2⇒2 页 / 3x3⇒1 页
//   - TestExportInvoiceGrid_DrawsCutLines / 3x3AlsoDrawn / ProductionPathDrawsLines
//     ：裁切线确实画进了 PDF
// 它们用的 `pdfPageCount` / `pdfFirstPageSize` 是正经的 PDF 读取器。
//
// ## 本文件补的：源发票**不是** A4 的那一档
//
// 真实数据里确实有：`data/email-invoices/ws_user-admin/` 下那张
// 「其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf」是
// **210 x 140mm**（MediaBox 与 CropBox 同为 `0 0 595.2756 396.8504`、
// /Rotate 0），而同目录的「云服务开票中心-1280.00」才是 A4。
// 也就是说**同一批导出里混着两种尺寸的源件**。
//
// 若实现只是把源页原样拼接，输出会跟着变成 210x140mm——
// 而上面那两条已有测试**照样全绿**：4 张源页 2x2 仍是 1 页，
// 只看首屏尺寸时也可能恰好命中 A4。页数对、尺寸错，是最难被发现的形态。
//
// ## 2026-10-02 的一次误报（留档，防止重犯）
//
// 我手工用原始字节正则 `/Type\s*/Page[^s]` 数页，得到 4，于是判定
// 「网格没生效，是缺陷」并差点写进报告。**是误报**：
// pdfcpu 的 NUp 把源页面转成 Form XObject，源页面对象仍留在文件里
// （连同各自的 /MediaBox），原始字节扫描会把这些**未被页树引用的残留对象**
// 一并数进去；页树 /Count 又写在压缩对象流里，正则根本找不到。
// 权威判据是 `api.PageCountFile` / `api.PageDimsFile`——它们读页树。
// 同一份产物的正确读数是：**1 页、A4 竖版 210x297mm**，网格是好的。
//
// 教训：判据必须指向被断言的对象。扫字节指向的是文件内容，不是页面树；
// 「产物里的 MediaBox 集合」同样不可用（Form XObject 会把源页尺寸带进来）。
// 这与仓库里其他判据失效同源。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// makeA4InvoicePDF 造一张 A4 单页「发票」，内容里带一个可辨认的编号。
func makeA4InvoicePDF(t *testing.T, dir, name, marker string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	raw := fmt.Sprintf(`%%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595.28 841.89]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 62>>stream
BT /F1 18 Tf 60 760 Td (%s) Tj ET
endstream
endobj
trailer<</Root 1 0 R/Size 6>>
%%%%EOF
`, marker)
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatalf("写夹具 %s: %v", name, err)
	}
	return p
}

func absDiff(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func assertA4(t *testing.T, file string, pageIdx int, w, h float64) {
	t.Helper()
	// 复用生产代码里的 A4 常量，而不是在这里另抄一份 595.28/841.89：
	// 抄一份就多一处会各自漂移的真相，而本文件要断言的正是「输出等于
	// 生产代码声称的那个 A4」。
	const tol = 1.0 // pt
	if absDiff(w-a4WidthPt) > tol || absDiff(h-a4HeightPt) > tol {
		t.Errorf("%s 第 %d 页尺寸 = %.2f x %.2f pt，want A4 %.2f x %.2f pt"+
			"（%.1fmm x %.1fmm）——非 A4 就无法「按 A4 打印后剪裁」",
			filepath.Base(file), pageIdx+1, w, h, a4WidthPt, a4HeightPt,
			w/72*25.4, h/72*25.4)
	}
}

// 5 张发票、grid=2 ⇒ ceil(5/4)=2 页、grid=3 ⇒ 1 页，且**每一页**（不是只有首屏）
// 都必须是 A4。已有测试只查了首屏尺寸。
func TestExportInvoiceGrid_OutputPagesAreA4AndPagedByGrid(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 1; i <= 5; i++ {
		files = append(files, makeA4InvoicePDF(t, dir, fmt.Sprintf("inv%d.pdf", i), fmt.Sprintf("INVOICE-%d", i)))
	}

	for _, grid := range []int{2, 3} {
		out := t.TempDir()
		res, err := ExportInvoiceGridDetailed(out, files, grid)
		if err != nil {
			t.Fatalf("grid=%d 导出失败: %v", grid, err)
		}
		if res.Count != len(files) {
			t.Errorf("grid=%d 入网格 %d 张，want %d", grid, res.Count, len(files))
		}

		wantPages := (len(files) + grid*grid - 1) / (grid * grid)
		got, err := api.PageCountFile(res.Path)
		if err != nil {
			t.Fatalf("grid=%d PageCountFile: %v", grid, err)
		}
		if got != wantPages {
			t.Errorf("grid=%d 产物页数 = %d，want ceil(%d/%d)=%d"+
				"（页数不对说明「每页容纳 grid² 张」这条需求没被满足）",
				grid, got, len(files), grid*grid, wantPages)
		}

		dims, err := api.PageDimsFile(res.Path)
		if err != nil {
			t.Fatalf("grid=%d PageDimsFile: %v", grid, err)
		}
		if len(dims) != got {
			t.Errorf("grid=%d PageDims 返回 %d 个尺寸，页数却是 %d", grid, len(dims), got)
		}
		for i, d := range dims {
			assertA4(t, res.Path, i, d.Width, d.Height)
		}
		t.Logf("grid=%d: %d 张发票 -> %d 页，每页 %.1fmm x %.1fmm",
			grid, len(files), got, dims[0].Width/72*25.4, dims[0].Height/72*25.4)
	}
}

// 源发票**不是** A4 时，输出仍必须是 A4。
//
// 真实数据里就有这种源件：杭州创客家那张 3500 元的发票是 210x140mm
// （CropBox 与 MediaBox 同值、/Rotate 0），云服务那张才是 A4。
// 若代码只是把源页原样拼接，输出就会跟着变成 210x140mm——
// 那正是「按 A4 排版」失败的形态，而它在只数页数的测试里看不出来
// （页数照样是 1）。
func TestExportInvoiceGrid_NormalizesNonA4SourceToA4(t *testing.T) {
	dir := t.TempDir()
	// 故意用 210x140mm 的源页（与真实数据里那张 3500 元发票同尺寸）
	raw := `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595.2756 396.8504]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 45>>stream
BT /F1 12 Tf 40 300 Td (SMALL SOURCE PAGE) Tj ET
endstream
endobj
trailer<</Root 1 0 R/Size 6>>
%%EOF
`
	p := filepath.Join(dir, "small.pdf")
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatalf("写夹具: %v", err)
	}

	out := t.TempDir()
	res, err := ExportInvoiceGridDetailed(out, []string{p}, 2)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	dims, err := api.PageDimsFile(res.Path)
	if err != nil {
		t.Fatalf("PageDimsFile: %v", err)
	}
	if len(dims) != 1 {
		t.Fatalf("页数 = %d，want 1", len(dims))
	}
	assertA4(t, res.Path, 0, dims[0].Width, dims[0].Height)
	if dims[0].Height == 396.8504 {
		t.Errorf("输出页高仍是源页的 396.8504pt（140mm）——源页被原样拼接了，没有按 A4 规范化")
	}
}
