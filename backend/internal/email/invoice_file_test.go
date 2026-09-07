package email

import (
	"bytes"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
)

func TestDetectInvoiceMedia(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	pdf := []byte("%PDF-1.4 rest")
	if kind, ext := DetectInvoiceMedia(jpeg); kind != "jpeg" || ext != ".jpg" {
		t.Fatalf("jpeg: kind=%s ext=%s", kind, ext)
	}
	if kind, ext := DetectInvoiceMedia(png); kind != "png" || ext != ".png" {
		t.Fatalf("png: kind=%s ext=%s", kind, ext)
	}
	if kind, ext := DetectInvoiceMedia(pdf); kind != "pdf" || ext != ".pdf" {
		t.Fatalf("pdf: kind=%s ext=%s", kind, ext)
	}
}

func TestExtractInvoiceThumbImagePassthrough(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	thumb, ct, ok := ExtractInvoiceThumb(png)
	if !ok || ct != "image/png" || !bytes.Equal(thumb, png) {
		t.Fatalf("ok=%v ct=%s len=%d", ok, ct, len(thumb))
	}
}

func TestExtractInvoiceThumbPDFWithoutImage(t *testing.T) {
	pdf := gofpdf.New("P", "mm", "A5", "")
	pdf.AddPage()
	pdf.SetFont("helvetica", "", 10)
	pdf.CellFormat(0, 10, "invoice", "", 1, "C", false, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ExtractInvoiceThumb(buf.Bytes()); ok {
		t.Fatal("text-only pdf should not yield a thumb")
	}
}

func TestInvoiceFileNameKeepsImageExt(t *testing.T) {
	inv := &Invoice{Category: "餐饮", Seller: "甲", Amount: 10, InvoiceDate: "2026-09-01"}
	got := InvoiceFileNameWithExt(inv, ".jpg")
	if got != "餐饮-甲-10.00-2026-09-01.jpg" {
		t.Fatalf("got %q", got)
	}
}

func TestParseInvoiceDateFromPDFBytes(t *testing.T) {
	raw := []byte("%PDF-1.4\n(开票日期：2026年04月12日)\n%%EOF")
	if got := ParseInvoiceDateFromBytes(raw); got != "2026-04-12" {
		t.Fatalf("got %q", got)
	}
}