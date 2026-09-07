package email

import (
	"bytes"
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

func firstPDFEmbeddedImage(data []byte) ([]byte, string, error) {
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
