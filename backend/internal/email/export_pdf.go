package email

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
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

// ExportInvoiceGrid 把 invoiceFiles 合并为 A4 网格 PDF，返回输出文件绝对路径。
// grid 只接受 2（2x2，每页 4 张）或 3（3x3，每页 9 张）。
func ExportInvoiceGrid(outDir string, invoiceFiles []string, grid int) (string, error) {
	if len(invoiceFiles) == 0 {
		return "", fmt.Errorf("no invoice files to export")
	}
	if grid != 2 && grid != 3 {
		return "", fmt.Errorf("grid must be 2 (2x2) or 3 (3x3), got %d", grid)
	}
	for _, f := range invoiceFiles {
		if !isRegularFile(f) {
			return "", fmt.Errorf("invoice file missing: %s", filepath.Base(f))
		}
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", err
	}

	// 1) 合并（pdfcpu 对 PDF 的 NUp 只吃单文件）
	merged := filepath.Join(outDir, fmt.Sprintf(".merge-%d.pdf", time.Now().UnixNano()))
	defer os.Remove(merged)
	if err := api.MergeCreateFile(invoiceFiles, merged, false, nil); err != nil {
		return "", fmt.Errorf("merge invoices: %w", err)
	}

	// 2) 网格化：PageGrid 模式下输出页 = PageDim × Grid，即 PageDim 是单格
	// 尺寸。要输出整张 A4，PageDim 取 A4 的 1/grid，每张发票缩放进格子。
	nup := &model.NUp{
		PageDim:  &types.Dim{Width: a4WidthPt / float64(grid), Height: a4HeightPt / float64(grid)},
		UserDim:  true,
		Grid:     &types.Dim{Width: float64(grid), Height: float64(grid)},
		PageGrid: true,
		Border:   false,
	}
	outFile := filepath.Join(outDir, fmt.Sprintf("invoices-a4-%dx%d-%s.pdf",
		grid, grid, time.Now().Format("20060102-150405")))
	if err := api.NUpFile([]string{merged}, outFile, nil, nup, nil); err != nil {
		_ = os.Remove(outFile)
		return "", fmt.Errorf("pdfcpu nup: %w", err)
	}
	return outFile, nil
}
