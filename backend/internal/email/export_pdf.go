package email

import (
	"bytes"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/webp"
)

// export_pdf.go — 发票 A4 网格合并导出（对应需求「单个 PDF 文件包含多张
// 发票，按 A4 规范排版，每页 2x2 / 3x3 网格，打印后可直接剪裁作为凭证」）。
//
// pdfcpu 的 NUpFile 对 PDF 输入只读第一个文件（多文件入参仅用于图片），
// 因此两步走：
//  1. MergeCreateFile 把全部发票 PDF 合并为临时文档；
//  2. NUp（PageGrid 模式）把合并文档的每 grid² 页排成一张 A4 竖版网格页。
//
// 输出落在 <dataDir>/email-invoices/exports/<workspace>/，HTTP handler 按
// 作用域提供下载。

// a4 竖版尺寸（pt，72dpi）。
const (
	a4WidthPt  = 595.28
	a4HeightPt = 841.89
)

// GridExport 是一次 A4 网格导出的结果。Count 是**真正进入网格**的张数，
// 不是请求里勾选的张数——畸形/不可解析的附件会被跳过，报请求数会骗人。
type GridExport struct {
	Path    string   `json:"-"`
	Count   int      `json:"count"`
	Skipped []string `json:"skipped,omitempty"`
}

// ErrNoUsableInvoiceFile 表示选中的发票文件一个都用不了（全是畸形 PDF/图片）。
// 调用方应回 400 而不是 500：这是用户选择/上游附件的问题，不是服务故障。
var ErrNoUsableInvoiceFile = errors.New("email: no usable invoice file to export")

// ExportInvoiceGrid 把 invoiceFiles 合并为 A4 网格 PDF，返回输出文件绝对路径。
// grid 只接受 2（2x2，每页 4 张）或 3（3x3，每页 9 张）。
//
// 容错：发票附件是外部输入，实测畸形 PDF（只有 Catalog、没有页树，69 字节）
// 会让 pdfcpu 的合并直接 panic（slice bounds out of range [-1:]），把整个导出
// 请求打成 500。因此这里做两件事——
//  1. 每个 PDF 先 Validate，坏文件跳过并计入 skipped（好文件照样导出）；
//  2. 兜底 recover，把 pdfcpu 的 panic 转成普通 error，绝不让它冒到 handler。
func ExportInvoiceGrid(outDir string, invoiceFiles []string, grid int) (string, error) {
	res, err := ExportInvoiceGridDetailed(outDir, invoiceFiles, grid)
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

// ExportInvoiceGridDetailed 同 ExportInvoiceGrid，但额外返回实际入网格的张数与被跳过的文件名。
func ExportInvoiceGridDetailed(outDir string, invoiceFiles []string, grid int) (*GridExport, error) {
	return exportNUp(outDir, invoiceFiles, grid, true)
}

// exportNUp 是 ExportInvoiceGridDetailed 的实现，Border 可指定。
// 生产路径恒传 true（需求「打印后可直接剪裁」）；参数化只为让测试能导出
// 「不画线」的对照产物，用字节数证明线确实被画进 PDF 了。
func exportNUp(outDir string, invoiceFiles []string, grid int, border bool) (res *GridExport, err error) {
	if len(invoiceFiles) == 0 {
		return nil, fmt.Errorf("no invoice files to export")
	}
	if grid != 2 && grid != 3 {
		return nil, fmt.Errorf("grid must be 2 (2x2) or 3 (3x3), got %d", grid)
	}
	for _, f := range invoiceFiles {
		if !isRegularFile(f) {
			return nil, fmt.Errorf("invoice file missing: %s", filepath.Base(f))
		}
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("export aborted on malformed invoice pdf: %v", r)
		}
	}()

	// 1) 归一化 + 校验：图片发票（jpg/png/gif/webp）转成单页 PDF；
	//    PDF 先过 Validate，畸形件跳过而不是拖垮整批。
	normalized, skipped, cleanup, err := normalizeInvoiceFilesToPDF(invoiceFiles, outDir)
	defer cleanup()
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		// %w is load-bearing: server_email_pipeline.go maps this sentinel to a
		// 400 ("your selection is unusable"), not a 500. Without the wrap the
		// handler's errors.Is never matches and a user who picked only
		// malformed files gets "服务故障" instead of which files were skipped.
		return nil, fmt.Errorf("%w (skipped %d: %s)",
			ErrNoUsableInvoiceFile, len(skipped), strings.Join(skipped, ", "))
	}
	if len(skipped) > 0 {
		log.Printf("[email/export] skipped %d malformed/unusable invoice file(s): %s",
			len(skipped), strings.Join(skipped, ", "))
	}

	// 2) 合并（pdfcpu 对 PDF 的 NUp 只吃单文件）
	merged := filepath.Join(outDir, fmt.Sprintf(".merge-%d.pdf", time.Now().UnixNano()))
	defer os.Remove(merged)
	if err := api.MergeCreateFile(normalized, merged, false, nil); err != nil {
		return nil, fmt.Errorf("merge invoices: %w", err)
	}

	// 3) 网格化：PageGrid 模式下输出页 = PageDim × Grid，即 PageDim 是单格
	//    尺寸。要输出整张 A4，PageDim 取 A4 的 1/grid，每张发票缩放进格子。
	//    复核提示（2026-10-01）：PageGrid 语义容易被读反——pdfcpu 先把
	//    PageDim 乘 Grid 算输出页 MediaBox（nup.go:800-803），再按 cols/rows
	//    把 PageDim 切格（nup.go:126-130），两处相消。只看 RectsForGrid 会误判
	//    PageDim 是整页尺寸、进而误以为 grid=2 输出 A5。差点把没问题的代码改坏。
	//
	// Border=true：需求原文「打印后可直接剪裁」。不加裁切线的话，打印出来的
	// A4 上 4/9 张发票没有可对齐的切割依据，只能凭发票白边目测，剪歪是必然的。
	// pdfcpu 的 Border 会在每个格子四边各画一条线，页边界处会与相邻格重合，
	// 正好形成完整的裁切网格。
	nup := &model.NUp{
		PageDim:  &types.Dim{Width: a4WidthPt / float64(grid), Height: a4HeightPt / float64(grid)},
		UserDim:  true,
		Grid:     &types.Dim{Width: float64(grid), Height: float64(grid)},
		PageGrid: true,
		Border:   border,
	}
	// 文件名用纳秒而非秒：同一秒内连续导出两次（导出对照、或并发导出）
	// 会撞名并让后一次覆盖前一次。UnixNano 已在 merge 临时文件里用过。
	outFile := filepath.Join(outDir, fmt.Sprintf("invoices-a4-%dx%d-%s.pdf",
		grid, grid, time.Now().Format("20060102-150405.000000000")))
	if err := api.NUpFile([]string{merged}, outFile, nil, nup, nil); err != nil {
		_ = os.Remove(outFile)
		return nil, fmt.Errorf("pdfcpu nup: %w", err)
	}
	return &GridExport{Path: outFile, Count: len(normalized), Skipped: skipped}, nil
}

// normalizeInvoiceFilesToPDF 把混合清单归一成「全是可合并的 PDF」：
//   - PDF：先 Validate，畸形件跳过（记入 skipped）；
//   - 图片：转成 A4 单页 PDF（等比缩放居中，留 5mm 白边方便剪裁）。
// 返回的 cleanup 会删掉中间产物。
func normalizeInvoiceFilesToPDF(files []string, outDir string) ([]string, []string, func(), error) {
	out := make([]string, 0, len(files))
	var skipped []string
	var created []string
	cleanup := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".pdf" {
			// 合并前先验：pdfcpu 对畸形 PDF 是 panic 而不是 error，而且
			// ValidateFile 对「有 Catalog 无页树」的退化文件是放行的 ——
			// 必须再确认它真的至少有 1 页。
			if n, verr := pdfPageCountSafe(f); verr != nil || n < 1 {
				skipped = append(skipped, filepath.Base(f))
				continue
			}
			out = append(out, f)
			continue
		}
		dst := filepath.Join(outDir, fmt.Sprintf(".img%d-%d.pdf", time.Now().UnixNano(), i))
		if err := imageFileToA4PDF(f, dst); err != nil {
			skipped = append(skipped, filepath.Base(f))
			continue
		}
		created = append(created, dst)
		out = append(out, dst)
	}
	return out, skipped, cleanup, nil
}

// pdfPageCountSafe 读一个 PDF 的页数，并把 pdfcpu 内部可能出现的 panic 转成 error。
// 退化文件（只有 Catalog、没有页树）会让页树遍历越界 panic，必须在这里挡住，
// 否则它会一路冒到 HTTP handler 把导出接口打成 500。
func pdfPageCountSafe(path string) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n, err = 0, fmt.Errorf("unreadable pdf: %v", r)
		}
	}()
	// PageCountFile 走的是文件级 API：内部会自行 Read + Validate。
	return api.PageCountFile(path)
}

// imageFileToA4PDF 把一张发票图片铺到 A4 单页 PDF 上。
//
// fpdf 原生只吃 JPEG/PNG/GIF，webp（国内很多电子发票截图就是 webp）先用
// x/image/webp 解码再编码成 PNG 交给 fpdf。
func imageFileToA4PDF(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	kind, _ := DetectInvoiceMedia(data)
	switch kind {
	case "webp":
		img, derr := webp.Decode(bytes.NewReader(data))
		if derr != nil {
			return derr
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		data = buf.Bytes()
		kind = "png"
	case "jpeg", "png", "gif":
		// fpdf 直接支持
	default:
		return fmt.Errorf("unsupported invoice media: %s", kind)
	}
	// 从 reader 注册图片时 fpdf 要求显式声明类型（它不做 magic 探测）。
	imgType := map[string]string{"jpeg": "jpg", "png": "png", "gif": "gif"}[kind]

	// A4（pt）+ 5mm 边距；fpdf 的 Image 按 w/h 缩放，这里先按像素比例算。
	const marginPt = 5 * 72 / 25.4
	pageW, pageH := a4WidthPt-marginPt*2, a4HeightPt-marginPt*2

	pdf := gofpdf.New("P", "pt", "A4", "")
	pdf.AddPage()
	pdf.SetMargins(0, 0, 0)
	info := pdf.RegisterImageOptionsReader("inv", gofpdf.ImageOptions{ImageType: imgType, ReadDpi: false}, bytes.NewReader(data))
	if info == nil {
		if err := pdf.Error(); err != nil {
			return err
		}
		return fmt.Errorf("cannot decode image %s", filepath.Base(src))
	}
	w, h := pageW, pageH
	if float64(info.Width()) > 0 && float64(info.Height()) > 0 {
		ratio := float64(info.Width()) / float64(info.Height())
		pageRatio := pageW / pageH
		if ratio > pageRatio {
			h = pageW / ratio
		} else {
			w = pageH * ratio
		}
	}
	pdf.Image("inv", (a4WidthPt-w)/2, (a4HeightPt-h)/2, w, h, false, "", 0, "")
	if err := pdf.Error(); err != nil {
		return err
	}
	return pdf.OutputFileAndClose(dst)
}
