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

// pdfHasPages 判断字节内容是不是「至少有一页的可用 PDF」。
//
// 为什么必须单独判：isPDFBytes 只看 `%PDF` magic，而实测有一批邮件带来的
// 退化 PDF 能过 magic —— 只有 Catalog、没有页树，69 字节：
//
//	%PDF-1.4
//	1 0 obj<</Type/Catalog>>endobj
//	trailer<</Root 1 0 R>>
//	%%EOF
//
// 它们被 saveInvoiceFile 写盘并置 status=downloaded，于是台账里凭空多出一张
// 金额为 0 的「发票」，文件名也就成了 `其他-<单位>-0.00-<日期>.pdf`；
// 而导出时 pdfcpu 遍历页树会 panic（见 export_pdf.go 的 pdfPageCountSafe），
// 采集侧却在源源不断制造导出侧不得不防御性跳过的文件。
//
// 判据用 pdfcpu 的权威解析（api.PageCount），**不**用字节扫 `/Type /Page`：
// 压缩对象流（PDF 1.5+ 的 xref stream / object stream）里的页字典在明文里
// 根本扫不到，那样会把好件误杀——这正是本仓库 PDF 几何测量踩过的坑。
//
// pdfcpu 对畸形件是 panic 而不是 error，所以这里必须 recover。
func pdfHasPages(data []byte) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			ok, err = false, fmt.Errorf("unreadable pdf: %v", r)
		}
	}()
	n, perr := api.PageCount(bytes.NewReader(data), nil)
	if perr != nil {
		return false, perr
	}
	return n >= 1, nil
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
