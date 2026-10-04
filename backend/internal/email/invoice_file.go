package email

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"strings"

	// 注册 JPEG / PNG 解码器，供 image.DecodeConfig 读图片尺寸用。
	// 只读配置不解码像素，所以这两个空导入的代价只是注册解码器。
	_ "image/jpeg"
	_ "image/png"

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

// bannerAspectRatio 是「这东西像横幅而不像一份文档」的长宽比阈值。
//
// 标定依据（两个实测值，不是一句「横幅都很宽」）：
//
//	观测到的营销横幅 572 x 140  → 4.09
//	A4 票面 竖版 0.707 / 横版 1.414
//
// 2.5 落在 1.414 与 4.09 之间，两侧余量都不小。
// 判别式是「像不像一份**文档**」，不是「是不是图」——后者对横幅
// 问一百遍答案也是 true（`isImageBytes` 就是栽在这里）。
const bannerAspectRatio = 2.5

// imagePlausibleAsVoucher 判断一份图片**有没有可能是发票凭证**。
//
// 与 isImageBytes 的分工：isImageBytes 回答「这是不是图片」，
// 本函数回答「这张图像不像一份票据」。两件事都要问——
// 只问前一个，营销横幅会被存成发票文件并标成 downloaded/已核验，
// 金额直接进财务合计（2026-10-04 08:00 那轮：6071.00 + 283.20 =
// 6354.20 CNY，占 CNY 合计 10392.21 的 61.1%，两个文件 SHA256 相同，
// 内容是印着「用心服务 贴心用户」的百望平台宣传横幅）。
//
// 只用几何、不用 OCR：判据必须能在**采信当场**跑完，且不许猜内容。
// 572x140 与 A4 的 0.707/1.414 差着一个数量级，这个差距不需要识别文字。
//
// 三个刻意取舍：
//
//  1. **不用最小尺寸下限。** 仓库里 `TestHarvestOne_ImageAttachmentKeepsImageExtension`
//     的夹具是一张 1x1 最小 PNG，而我没有一张真实拍照发票样本可用来标定下限。
//     「没有证据就不改」——加了会打红一条既有护栏，且那个下限是编的。
//  2. **解码不出尺寸就放行（fail-open）。** webp 没有 stdlib 解码器，
//     DecodeConfig 会报 unsupported format。宁可放过一张 webp 横幅，
//     也不要因为「测不出尺寸」把真票判死。代价是 webp 这条路仍有洞，
//     已记进 handoff 遗留风险。
//  3. **不动 isImageBytes。** 它还被 ExtractInvoiceThumb / DetectInvoiceMedia
//     用着；列表页缩略图场景下横幅**本来就该**能显示，把闸门加在
//     isImageBytes 上会把缩略图一起弄坏。
func imagePlausibleAsVoucher(data []byte) bool {
	if !isImageBytes(data) {
		// 不是图片：PDF / XML / zip 的准入另有其判，不归本函数管。
		return true
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		// 测不出尺寸 → 放行（见上文取舍 2）。
		return true
	}
	long, short := cfg.Width, cfg.Height
	if short > long {
		long, short = short, long
	}
	return float64(long)/float64(short) < bannerAspectRatio
}

// voucherRejectionReason 给出可写进 last_error 的原因文本，
// 便于运维在台账上直接看出「这一行是横幅不是票」。
func voucherRejectionReason(data []byte) string {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return fmt.Sprintf("image is banner-shaped (%dx%d, aspect %.2f >= %.1f), not a document",
			cfg.Width, cfg.Height, float64(max(cfg.Width, cfg.Height))/float64(min(cfg.Width, cfg.Height)),
			bannerAspectRatio)
	}
	return "image is banner-shaped, not a document"
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
