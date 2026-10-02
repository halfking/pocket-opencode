package email

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// DetectInvoiceMedia 按 magic 判断发票文件种类（pdf / jpeg / png / webp）。
func DetectInvoiceMedia(data []byte) (kind, ext string) {
	if isPDFBytes(data) {
		return "pdf", ".pdf"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "jpeg", ".jpg"
	}
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "png", ".png"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp", ".webp"
	}
	return "unknown", ""
}

func invoiceMediaType(kind string) string {
	switch kind {
	case "pdf":
		return "application/pdf"
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func isImageBytes(data []byte) bool {
	kind, _ := DetectInvoiceMedia(data)
	return kind == "jpeg" || kind == "png" || kind == "webp"
}

// InvoiceFileNameWithExt 与 InvoiceFileName 相同，但允许 jpg/png 等扩展名。
func InvoiceFileNameWithExt(inv *Invoice, ext string) string {
	name := InvoiceFileName(inv)
	if ext == "" || ext == ".pdf" {
		return name
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return strings.TrimSuffix(name, ".pdf") + ext
}

// ExtractInvoiceThumb 给列表用：图片原样返回；PDF 抽第一张嵌入图。
func ExtractInvoiceThumb(data []byte) (thumb []byte, contentType string, ok bool) {
	kind, _ := DetectInvoiceMedia(data)
	if kind == "jpeg" || kind == "png" || kind == "webp" {
		return data, invoiceMediaType(kind), true
	}
	if kind != "pdf" {
		return nil, "", false
	}
	img, ft, err := firstPDFEmbeddedImage(data)
	if err != nil || len(img) == 0 {
		return nil, "", false
	}
	ct := "image/jpeg"
	switch strings.ToLower(ft) {
	case "png":
		ct = "image/png"
	case "webp":
		ct = "image/webp"
	case "tiff", "tif":
		ct = "image/tiff"
	}
	return img, ct, true
}

// firstPDFEmbeddedImage 抽出第一页里的第一张嵌入图。
//
// 发票附件是外部输入，而 pdfcpu v0.11.0 对退化 PDF（只有 Catalog、没有页树）
// 会 panic：model.skipStringLit（pkg/pdfcpu/model/parse.go:1273）报
// `slice bounds out of range [-1:]`，经 api.ExtractImagesRaw 冒到调用方。
//
// 同一批代码里的导出路径早就为这件事定过规矩并且做了防御
// （export_pdf.go 的 exportNUp / pdfPageCountSafe：「绝不让它冒到 handler」，
// 配套用例 TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes）。
// 这里当时漏了，于是 GET /api/emails/invoices/{id}/thumb 遇到畸形发票时
// 表现为 500 而不是 404 thumbnail unavailable。
//
// 现在按同一条规矩补上：panic 转成普通 error，由 ExtractInvoiceThumb 当成
// 「抽不出缩略图」。注意必须用**具名返回值**，否则 recover 里改不动 err。
func firstPDFEmbeddedImage(data []byte) (img []byte, fileType string, err error) {
	defer func() {
		if r := recover(); r != nil {
			img, fileType, err = nil, "", fmt.Errorf("unreadable invoice pdf: %v", r)
		}
	}()
	pages, err := api.ExtractImagesRaw(bytes.NewReader(data), []string{"1"}, nil)
	if err != nil {
		return nil, "", err
	}
	for _, mm := range pages {
		for _, img := range mm {
			if img.Reader == nil {
				continue
			}
			b, rerr := io.ReadAll(img.Reader)
			if rerr != nil || len(b) == 0 {
				continue
			}
			return b, img.FileType, nil
		}
	}
	return nil, "", nil
}
