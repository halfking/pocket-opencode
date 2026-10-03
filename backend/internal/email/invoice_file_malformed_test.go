package email

// invoice_file_malformed_test.go — 畸形发票 PDF 不得让缩略图接口变成 500。
//
// ## 背景
//
// pdfcpu v0.11.0 对退化 PDF（只有 Catalog、没有页树）会 panic：
// model.skipStringLit（pkg/pdfcpu/model/parse.go:1273）报
// `slice bounds out of range [-1:]`，经 api.ExtractImagesRaw 冒到调用方。
//
// 同一批代码里的**导出**路径早就为这件事定过规矩并做了防御
// （export_pdf.go 的 exportNUp / pdfPageCountSafe 里有两处 recover，
// 配套用例是 TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes）。
// 缩略图路径当时漏了，于是 GET /api/emails/invoices/{id}/thumb
// 遇到畸形发票时返回 500 而不是 404「thumbnail unavailable」。
//
// 这些用例的形状刻意与导出那两条对齐，便于对照。
//
// ## 负控（实测过，见 handoff §7di）
//
// 去掉 firstPDFEmbeddedImage 里的 recover → TestExtractInvoiceThumb_MalformedPDFDoesNotPanic 转红（panic）。

import (
	"os"
	"path/filepath"
	"testing"
)

// malformedInvoicePDF 是与实测一致的退化 PDF：只有 Catalog、无页树。
func malformedInvoicePDF() []byte {
	return []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
}

// 缩略图是**展示性**功能：抽不出图就应回「没有」，绝不能把整个请求打成 500。
func TestExtractInvoiceThumb_MalformedPDFDoesNotPanic(t *testing.T) {
	// 显式 recover：万一将来 recover 被删掉，用例要以「可读的方式」失败，
	// 而不是让整个测试二进制 panic、看起来像环境问题。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ExtractInvoiceThumb panicked on a malformed pdf: %v", r)
		}
	}()
	thumb, ct, ok := ExtractInvoiceThumb(malformedInvoicePDF())
	if ok {
		t.Fatalf("畸形 PDF 不该抽出缩略图，却拿到了 %d 字节 (ct=%s)", len(thumb), ct)
	}
	if thumb != nil || ct != "" {
		t.Fatalf("失败时应返回零值，得到 thumb=%d ct=%q", len(thumb), ct)
	}
}

// 另一个畸形形态：连 trailer 都没有的截断文件。
func TestExtractInvoiceThumb_TruncatedPDFDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ExtractInvoiceThumb panicked on a truncated pdf: %v", r)
		}
	}()
	for _, body := range [][]byte{
		[]byte("%PDF-1.4"),
		[]byte("%PDF-1.4\n"),
		[]byte("not a pdf at all"),
		{},
	} {
		if _, _, ok := ExtractInvoiceThumb(body); ok {
			t.Errorf("内容 %q 不该抽出缩略图", string(body))
		}
	}
}

// firstPDFEmbeddedImage 层：panic 要被转成**普通 error**，而不是静默成功。
// 判据是 error 非 nil —— 只断言「没 panic」会让「返回 nil,nil,nil」的写法蒙混过关。
func TestFirstPDFEmbeddedImage_MalformedReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("firstPDFEmbeddedImage panicked: %v", r)
		}
	}()
	img, fileType, err := firstPDFEmbeddedImage(malformedInvoicePDF())
	if err == nil {
		t.Fatalf("畸形 PDF 应返回 error，却得到 img=%d ft=%q（说明 recover 被吞成了 nil）", len(img), fileType)
	}
	if img != nil || fileType != "" {
		t.Errorf("出错时零值应为空，得到 img=%d ft=%q", len(img), fileType)
	}
}

// 反向确认：合法 PDF 仍然走正常路径，别把 recover 写成「一律失败」。
// 复用仓库里现成的合法 PDF 生成器（与导出用例同一个）。
func TestFirstPDFEmbeddedImage_ValidPDFStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.pdf")
	makeTestPDF(t, path, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 合法 PDF 没有嵌入图，所以是 (nil, "", nil) —— 既不报错也不 panic。
	// 这里钉的是「不报错」：recover 误伤会把 err 填上。
	_, _, err = firstPDFEmbeddedImage(data)
	if err != nil {
		t.Fatalf("合法 PDF 不该报错: %v", err)
	}
}
