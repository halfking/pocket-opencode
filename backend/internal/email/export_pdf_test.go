package email

// export_pdf_test.go — A4 网格导出的真实产物校验。
//
// 需求原文：「单个 PDF 文件，包含多张发票；按照 A4 纸张规范排版，每页可容纳
// 多张发票（类似 2x2 或 3x3 网格）；打印后可直接剪裁。」
//
// 这里不只测「函数没报错」，而是打开产物数页：页数必须等于 ceil(n/grid²)，
// 且每页尺寸是 A4。混合清单（PDF + png/jpg）必须全部进网格——以前图片发票
// 在 MergeCreateFile 那一步会被丢掉，选 3 张含 1 张图片就只剩 2 张。

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func newTestFpdf(t *testing.T) *gofpdf.Fpdf {
	t.Helper()
	pdf := gofpdf.New("P", "pt", "A4", "")
	pdf.SetMargins(20, 20, 20)
	// 夹具只需要一个能写字的字体；CoreFont 足够（内容是 ASCII）。
	pdf.SetFont("Arial", "", 12)
	return pdf
}

// makeTestPDF 生成一张指定页数的最小合法 PDF。
func makeTestPDF(t *testing.T, path string, pages int) {
	t.Helper()
	pdf := newTestFpdf(t)
	for i := 0; i < pages; i++ {
		pdf.AddPage()
		pdf.CellFormat(0, 10, fmt.Sprintf("invoice page %d", i+1), "", 1, "C", false, 0, "")
	}
	if err := pdf.OutputFileAndClose(path); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	return img
}

func makeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(w, h)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeTestJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(w, h), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pdfPageCount(t *testing.T, path string) int {
	t.Helper()
	n, err := api.PageCountFile(path)
	if err != nil {
		t.Fatalf("page count %s: %v", path, err)
	}
	return n
}

func pdfFirstPageSize(t *testing.T, path string) (float64, float64) {
	t.Helper()
	dim, err := api.PageDimsFile(path)
	if err != nil || len(dim) == 0 {
		t.Fatalf("page dims %s: %v", path, err)
	}
	return dim[0].Width, dim[0].Height
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// 4 张发票 2x2 ⇒ 恰好 1 页 A4。
func TestExportInvoiceGrid_2x2FitsOneA4Page(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exports")
	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	got, err := ExportInvoiceGrid(out, files, 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n := pdfPageCount(t, got); n != 1 {
		t.Fatalf("2x2 with 4 invoices should be 1 page, got %d", n)
	}
	w, h := pdfFirstPageSize(t, got)
	if absf(w-a4WidthPt) > 1.5 || absf(h-a4HeightPt) > 1.5 {
		t.Fatalf("page is not A4: %.2fx%.2f pt", w, h)
	}
}

// 5 张发票 2x2 ⇒ 2 页；3x3 ⇒ 1 页。
func TestExportInvoiceGrid_PageCountFollowsCeiling(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	got2, err := ExportInvoiceGrid(filepath.Join(dir, "g2"), files, 2)
	if err != nil {
		t.Fatalf("grid2: %v", err)
	}
	if n := pdfPageCount(t, got2); n != 2 {
		t.Fatalf("2x2 with 5 invoices should be 2 pages, got %d", n)
	}
	got3, err := ExportInvoiceGrid(filepath.Join(dir, "g3"), files, 3)
	if err != nil {
		t.Fatalf("grid3: %v", err)
	}
	if n := pdfPageCount(t, got3); n != 1 {
		t.Fatalf("3x3 with 5 invoices should be 1 page, got %d", n)
	}
}

// 多页 PDF 发票：源页数 / grid² 决定输出页数。
func TestExportInvoiceGrid_MultiPageInvoiceFollowsCellCount(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 3) // 每张发票 3 页 ⇒ 共 12 格
		files = append(files, p)
	}
	got, err := ExportInvoiceGrid(filepath.Join(dir, "out"), files, 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n := pdfPageCount(t, got); n != 3 {
		t.Fatalf("12 cells on 2x2 should produce 3 A4 pages, got %d", n)
	}
}

// 混合清单：PDF + png + jpg 都必须进网格，一张都不能少。
func TestExportInvoiceGrid_IncludesImageInvoices(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "a.pdf")
	makeTestPDF(t, pdfPath, 1)
	pngPath := filepath.Join(dir, "b.png")
	makeTestPNG(t, pngPath, 800, 1100)
	jpgPath := filepath.Join(dir, "c.jpg")
	makeTestJPEG(t, jpgPath, 600, 800)

	files := []string{pdfPath, pngPath, jpgPath}
	got, err := ExportInvoiceGrid(filepath.Join(dir, "out"), files, 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n := pdfPageCount(t, got); n != 1 {
		t.Fatalf("3 invoices on 2x2 should be 1 page, got %d", n)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Fatalf("temp artifact left behind: %s", e.Name())
		}
	}
}

// 非 2/3 的 grid 必须被拒（否则会产出无法剪裁的版式）。
func TestExportInvoiceGrid_RejectsInvalidGrid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.pdf")
	makeTestPDF(t, p, 1)
	if _, err := ExportInvoiceGrid(filepath.Join(dir, "out"), []string{p}, 4); err == nil {
		t.Fatal("grid=4 should be rejected")
	}
}

// 畸形 PDF 附件不得让整个导出 500：pdfcpu 对没有页树的 PDF 是 panic
// （slice bounds out of range [-1:]），实测把 POST /api/emails/invoices/export
// 打成 500。修复后：坏文件跳过，好文件照常出网格。
func TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.pdf")
	// 与实测一致的退化 PDF：只有 Catalog、无页树
	if err := os.WriteFile(bad, []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.pdf")
	makeTestPDF(t, good, 1)

	got, err := ExportInvoiceGrid(filepath.Join(dir, "out"), []string{bad, good}, 2)
	if err != nil {
		t.Fatalf("export must survive a malformed invoice pdf, got: %v", err)
	}
	if n := pdfPageCount(t, got); n != 1 {
		t.Fatalf("the good invoice should still land in the grid, pages=%d", n)
	}
}

// 全是畸形件时必须返回可读错误，而不是 panic / 空文件。
func TestExportInvoiceGrid_AllMalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.pdf")
	if err := os.WriteFile(bad, []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ExportInvoiceGrid(filepath.Join(dir, "out"), []string{bad}, 2)
	if err == nil {
		t.Fatal("expected an error when every invoice file is unusable")
	}
}

// webp 走的是 x/image/webp 解码分支：这里只钉「分支被接上」——Go 生态没有// webp 编码器，无法在测试里造一张真 webp，所以断言的是错误来自解码器
// （unsupported invoice media 说明压根没进 webp 分支）。
func TestExportInvoiceGrid_WebPReachesWebPDecoder(t *testing.T) {
	dir := t.TempDir()
	// RIFF....WEBP 头 + 故意无效的负载
	body := append([]byte("RIFF"), 0, 0, 0, 0)
	body = append(body, []byte("WEBPVP8L")...)
	body = append(body, make([]byte, 32)...)
	p := filepath.Join(dir, "c.webp")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ExportInvoiceGrid(filepath.Join(dir, "out"), []string{p}, 2)
	if err == nil {
		t.Skip("fixture happened to decode; nothing to assert")
	}
	if strings.Contains(err.Error(), "unsupported invoice media") {
		t.Fatalf("webp did not reach the webp decoder: %v", err)
	}
}


