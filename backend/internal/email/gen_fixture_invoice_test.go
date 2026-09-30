package email

// 一个一次性夹具生成器：把一张**真格式**的发票 PDF 导出成 base64，
// 供 scripts/imap-fixture-mails.mjs 当附件用（IMAP 夹具需要真实可解析的 PDF，
// 否则采集器落盘的是退化文件，A4 网格导出会被跳过）。
//
// 用法：go test ./internal/email -run TestGenerateFixtureInvoicePDFBase64
// 然后把 <dataDir>/email-invoices/exports/fixture-invoice.b64 的内容贴进夹具。
// 默认不参与常规测试（需要显式加 -run 或设 POCKET_EMAIL_GEN_FIXTURE=1）。

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
)

func TestGenerateFixtureInvoicePDFBase64(t *testing.T) {
	if os.Getenv("POCKET_EMAIL_GEN_FIXTURE") != "1" {
		t.Skip("set POCKET_EMAIL_GEN_FIXTURE=1 to regenerate the IMAP fixture invoice")
	}
	pdf := gofpdf.New("P", "pt", "A4", "")
	pdf.SetMargins(40, 40, 40)
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 18)
	pdf.CellFormat(0, 16, "VAT E-INVOICE (IMAP fixture)", "", 1, "C", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 12)
	rows := [][2]string{
		{"Invoice No", "25332000000123456789"},
		{"Issue Date", "2026-09-28"},
		{"Seller", "Fixture Cloud Services Co., Ltd."},
		{"Buyer", "Openpocket Demo Co., Ltd."},
		{"Total (VAT incl.)", "CNY 1280.00"},
	}
	for _, r := range rows {
		pdf.CellFormat(150, 11, r[0], "1", 0, "L", false, 0, "")
		pdf.CellFormat(0, 11, r[1], "1", 1, "L", false, 0, "")
	}
	out := filepath.Join(os.TempDir(), "fixture-invoice.pdf")
	if err := pdf.OutputFileAndClose(out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// 分块输出，方便直接贴进 JS 模板
	b64 := base64.StdEncoding.EncodeToString(raw)
	const chunk = 96
	var sb strings.Builder
	for i := 0; i < len(b64); i += chunk {
		end := i + chunk
		if end > len(b64) {
			end = len(b64)
		}
		sb.WriteString("  '" + b64[i:end] + "' +\n")
	}
	dst := filepath.Join(os.TempDir(), "fixture-invoice.b64")
	if err := os.WriteFile(dst, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("pdf bytes=%d base64 chunks written to %s", len(raw), dst)
}
