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
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// --- 网格几何（真正的「2x2 / 3x3」而不是「页数对」） ---
//
// 上面 8 条用例断言的是**页数**与**纸张尺寸**。这两样即使网格排版整个坏掉
// 也照样成立：把 4 张发票叠在同一格、缩放到整页、或者排成 4×1，页数仍是 1、
// 纸张仍是 A4，全部绿灯。而需求原文是「每页可容纳多张发票（类似 2x2 或 3x3
// 网格）；打印后可直接剪裁」——「可剪裁」恰恰要求每张发票真的落在**各自独立
// 的格子**里。所以这里改从产物页的内容流里读出 pdfcpu NUp 写的放置矩阵，
// 直接验格子。

// gridPlacement 是一次 `q a b c d e f cm /FmN Do Q` 放置。
type gridPlacement struct {
	form        int
	x, y, scale float64
}

// cmOpRe 匹配 NUp 放置一个 Form XObject 的那一条指令。
var cmOpRe = regexp.MustCompile(
	`(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+cm\s+/Fm(\d+)\s+Do`)

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return f
}

// pagePlacements 取出产物第 page 页里所有发票放置的 (x, y, scale)。
func pagePlacements(t *testing.T, pdfPath string, page int) []gridPlacement {
	t.Helper()
	cd := t.TempDir()
	if err := api.ExtractContentFile(pdfPath, cd, []string{fmt.Sprint(page)}, nil); err != nil {
		t.Fatalf("extract content of page %d: %v", page, err)
	}
	ents, err := os.ReadDir(cd)
	if err != nil || len(ents) == 0 {
		t.Fatalf("no content stream extracted for page %d (ents=%d err=%v)", page, len(ents), err)
	}
	raw, err := os.ReadFile(filepath.Join(cd, ents[0].Name()))
	if err != nil {
		t.Fatalf("read content stream: %v", err)
	}
	var out []gridPlacement
	for _, m := range cmOpRe.FindAllStringSubmatch(string(raw), -1) {
		out = append(out, gridPlacement{
			form:  int(mustFloat(t, m[7])),
			scale: mustFloat(t, m[1]),
			x:     mustFloat(t, m[5]),
			y:     mustFloat(t, m[6]),
		})
	}
	return out
}

// assertGridPage 断言该页确实排成 grid×grid 个互不重叠的格子，且格子铺满整页。
//
// 关键判据是「互不重叠」：把所有落点按 0.1pt 量化后必须恰好有 grid² 个不同
// 格子，且每个格子只被占一次。叠印/共用原点会立刻掉到 1 个格子上。
func assertGridPage(t *testing.T, pdfPath string, page, grid int) {
	t.Helper()
	ps := pagePlacements(t, pdfPath, page)
	want := grid * grid
	if len(ps) != want {
		t.Fatalf("page %d: 放了 %d 张发票，期望 %d（%dx%d 网格每页 %d 格）",
			page, len(ps), want, grid, grid, want)
	}

	// 缩放：整页被切成 grid×grid，单张发票应缩到 1/grid。
	wantScale := 1.0 / float64(grid)
	for _, p := range ps {
		if absf(p.scale-wantScale) > 0.01 {
			t.Fatalf("page %d: 发票 /Fm%d 的缩放是 %.4f，期望 %.4f（=1/%d）。"+
				"缩放不是 1/%d 意味着格子尺寸算错了，剪出来的每张大小会不一致",
				page, p.form, p.scale, wantScale, grid, grid)
		}
	}

	// 落点去重：必须占满 grid² 个互不相同的格子。
	cell := map[[2]int64]int{}
	for _, p := range ps {
		k := [2]int64{int64(math.Round(p.x * 10)), int64(math.Round(p.y * 10))}
		cell[k]++
	}
	if len(cell) != want {
		t.Fatalf("page %d: 只占了 %d 个不同格子，期望 %d —— 有发票叠在同一位置上了。落点=%v",
			page, len(cell), want, cell)
	}
	for k, n := range cell {
		if n != 1 {
			t.Fatalf("page %d: 格子 (%.1f, %.1f) 上叠了 %d 张发票，期望 1", page,
				float64(k[0])/10, float64(k[1])/10, n)
		}
	}

	// 铺满：列数/行数各自等于 grid，且最外一列/行贴到页边（不留悬空也不溢出）。
	w, h := pdfFirstPageSize(t, pdfPath)
	xs, ys := map[int64]bool{}, map[int64]bool{}
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range ps {
		xs[int64(math.Round(p.x))] = true
		ys[int64(math.Round(p.y))] = true
		maxX = math.Max(maxX, p.x)
		maxY = math.Max(maxY, p.y)
	}
	if len(xs) != grid || len(ys) != grid {
		t.Fatalf("page %d: 落点构成 %d 列 × %d 行，期望 %d × %d（xs=%v ys=%v）",
			page, len(xs), len(ys), grid, grid, xs, ys)
	}
	// 每格宽 = 页宽/grid；最右列的左边界 + 格宽 应等于页宽。
	if m := maxX + w/float64(grid); absf(m-w) > 1.5 {
		t.Fatalf("page %d: 横向没铺满 —— 最右列左边界 %.2f + 格宽 %.2f = %.2f，页宽 %.2f",
			page, maxX, w/float64(grid), m, w)
	}
	if m := maxY + h/float64(grid); absf(m-h) > 1.5 {
		t.Fatalf("page %d: 纵向没铺满 —— 最上行下边界 %.2f + 格高 %.2f = %.2f，页高 %.2f",
			page, maxY, h/float64(grid), m, h)
	}
}

// 需求：「按照 A4 纸张规范排版，每页可容纳多张发票（类似 2x2 或 3x3 网格）」。
// 页数与纸张尺寸已有用例覆盖；这里验的是**每张发票各自落在独立格子里**。
func TestExportInvoiceGrid_2x2PlacesEachInvoiceInItsOwnCell(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	got, err := ExportInvoiceGrid(filepath.Join(dir, "exports"), files, 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	assertGridPage(t, got, 1, 2)
}

func TestExportInvoiceGrid_3x3PlacesEachInvoiceInItsOwnCell(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 9; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	got, err := ExportInvoiceGrid(filepath.Join(dir, "exports"), files, 3)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	assertGridPage(t, got, 1, 3)
}

// 末页只放了剩下的那几张：不能把不足一整页的残余也铺成满格，更不能叠在一起。
func TestExportInvoiceGrid_LastPartialPageKeepsCellsApart(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("inv%d.pdf", i))
		makeTestPDF(t, p, 1)
		files = append(files, p)
	}
	got, err := ExportInvoiceGrid(filepath.Join(dir, "exports"), files, 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n := pdfPageCount(t, got); n != 2 {
		t.Fatalf("5 张 2x2 应为 2 页，got %d", n)
	}
	ps := pagePlacements(t, got, 2)
	if len(ps) != 1 {
		t.Fatalf("第 2 页应只放剩下的 1 张，实际放了 %d 张", len(ps))
	}
	// 它必须落在某个合法格子的原点上（x、y 都是 0 或整格宽/高）。
	w, h := pdfFirstPageSize(t, got)
	cellW, cellH := w/2, h/2
	okX := absf(ps[0].x) < 1.5 || absf(ps[0].x-cellW) < 1.5
	okY := absf(ps[0].y) < 1.5 || absf(ps[0].y-cellH) < 1.5
	if !okX || !okY {
		t.Fatalf("第 2 页的落点 (%.2f, %.2f) 不在任何格子原点上（列 %v / 行 %v）",
			ps[0].x, ps[0].y, []float64{0, cellW}, []float64{0, cellH})
	}
}
