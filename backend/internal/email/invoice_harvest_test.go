package email

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestLooksLikeSpam(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		subject string
		snippet string
		inv     bool
		imp     bool
		want    bool
	}{
		{"营销强信号", "promo@shop.com", "限时抢购 全场秒杀", "点击退订 Unsubscribe", false, false, true},
		{"中奖诈骗", "lucky@draw.cn", "恭喜您获得大奖", "免费领取", false, false, true},
		{"弱信号不足", "news@somewhere.com", "本周精选文章", "订阅更新 newsletter digest", false, false, false},
		{"正常账单不判垃圾", "billing@sftp.cn", "您有一张新发票", "电子发票 价税合计 ¥120.00", true, false, false},
		{"重要邮件不判垃圾", "boss@corp.com", "促销活动方案", "请查阅", false, true, false},
		{"白名单域不判垃圾", "noreply@meituan.com", "会员日大促秒杀", "限时抢购", false, false, false},
		{"退订特征+营销词", "edm@vendor.io", "回T退订", "促销 优惠", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LooksLikeSpam(c.from, c.subject, c.snippet, c.inv, c.imp)
			if got.Spam != c.want {
				t.Fatalf("spam=%v score=%d why=%q, want %v", got.Spam, got.Score, got.Why, c.want)
			}
		})
	}
}

func TestInvoiceFileName(t *testing.T) {
	inv := &Invoice{Category: "餐饮", Seller: "某某科技公司", Amount: 120.5, InvoiceDate: "2026/09/05"}
	got := InvoiceFileName(inv)
	want := "餐饮-某某科技公司-120.50-2026-09-05.pdf"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// 非法字符清洗
	inv.Seller = `A/B\C:D*E?F"G<H>I|J`
	if s := InvoiceFileName(inv); strings.ContainsAny(s, `/\:*?"<>|`) {
		t.Fatalf("filename has illegal chars: %q", s)
	}
	// 空字段兜底
	empty := &Invoice{}
	if s := InvoiceFileName(empty); !strings.HasPrefix(s, "其他-未知单位-") {
		t.Fatalf("empty invoice naming: %q", s)
	}
}

func TestParseMIMEMessageWithAttachment(t *testing.T) {
	raw := "From: =?utf-8?B?5rwG6ZW/?= <bill@shop.cn>\r\n" +
		"Subject: =?utf-8?B?" + b64("电子发票") + "?=\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<a href=\"https://fp.example.com/download?token=abc&file=invoice.pdf\">下载发票</a>\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64("%PDF-1.4 fake pdf body") + "\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/xml; name=\"invoice.xml\"\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.xml\"\r\n\r\n" +
		"<Invoice><InvoiceNo>12345678</InvoiceNo><TotalAmount>88.00</TotalAmount></Invoice>\r\n" +
		"--BOUND--\r\n"
	msg, err := ParseMIMEMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "电子发票" {
		t.Fatalf("subject=%q", msg.Subject)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("attachments=%d", len(msg.Attachments))
	}
	if !isPDFBytes(msg.Attachments[0].Data) {
		t.Fatal("pdf attachment not detected")
	}
	urls := extractInvoiceURLs(msg.HTMLBody)
	if len(urls) == 0 || !strings.Contains(urls[0], "fp.example.com") {
		t.Fatalf("urls=%v", urls)
	}
	fields := ParseInvoiceXML(msg.Attachments[1].Data)
	if fields == nil || fields.InvoiceNo != "12345678" || fields.Amount != 88 {
		t.Fatalf("xml fields=%+v", fields)
	}
}

func TestParseInvoiceXMLAttrs(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<EInvoice InvoiceNo="88889999" IssueDate="2026年09月01日">
  <Seller><Name>供应商甲</Name></Seller>
  <Buyer><Name>采购公司乙</Name></Buyer>
  <Item Name="云服务费" AmountTotal="256.90"/>
</EInvoice>`)
	f := ParseInvoiceXML(xmlData)
	if f == nil {
		t.Fatal("parse failed")
	}
	if f.InvoiceNo != "88889999" || f.InvoiceDate != "2026-09-01" || f.Amount != 256.90 {
		t.Fatalf("fields=%+v", f)
	}
	if f.Seller != "供应商甲" || f.BuyerTitle != "采购公司乙" {
		t.Fatalf("parties=%+v", f)
	}
}

func TestExtractInvoiceURLsSkipsNoise(t *testing.T) {
	body := `<a href="https://t.cn/unsubscribe?u=1">退订</a>
	<img src="https://cdn.cn/pic.png"/>
	<a href="https://fp.example.com/invoice/abc.pdf">发票</a>
	https://plain.example.com/file.pdf`
	urls := extractInvoiceURLs(body)
	if len(urls) != 2 {
		t.Fatalf("urls=%v", urls)
	}
	// 带发票特征的排前
	if !strings.Contains(urls[0], "fp.example.com") {
		t.Fatalf("order=%v", urls)
	}
}

// TestExportInvoiceGrid 用 fpdf 生成 4 张单页 PDF，导出 2x2 网格并验证输出。
func TestExportInvoiceGrid(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 4; i++ {
		pdf := gofpdf.New("P", "mm", "A5", "")
		pdf.AddPage()
		pdf.SetFont("helvetica", "", 10)
		pdf.CellFormat(0, 10, "invoice", "", 1, "C", false, 0, "")
		p := filepath.Join(dir, string(rune('a'+i))+".pdf")
		if err := pdf.OutputFileAndClose(p); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	out, err := ExportInvoiceGrid(dir, files, 2)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !isPDFBytes(data) {
		t.Fatal("export is not a pdf")
	}
	// 4 张单页发票 2x2 → 单页输出；页面应为 A4 竖版（595x842pt）
	pageCount, err := api.PageCount(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	if pageCount != 1 {
		t.Fatalf("page count = %d, want 1", pageCount)
	}
	pageDims, err := api.PageDims(bytes.NewReader(data), nil)
	if err != nil || len(pageDims) != 1 {
		t.Fatalf("page dims err=%v", err)
	}
	w, h := pageDims[0].Width, pageDims[0].Height
	if w < 590 || w > 600 || h < 838 || h > 846 {
		t.Fatalf("page size %.1fx%.1f, want A4 portrait 595x842", w, h)
	}
	// grid 非法值
	if _, err := ExportInvoiceGrid(dir, files, 5); err == nil {
		t.Fatal("expected error for grid=5")
	}
}

func TestWriteInvoiceSummaryDocs(t *testing.T) {
	dir := t.TempDir()
	invoices := []Invoice{
		{Category: "餐饮", Seller: "甲", Amount: 100, InvoiceDate: "2026-09-01", Status: "downloaded", FileName: "a.pdf"},
		{Category: "交通", Seller: "乙", Amount: 23.45, InvoiceDate: "2026-09-02", Status: "pending"},
	}
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "default", invoices)
	if err != nil {
		t.Fatal(err)
	}
	csvData, _ := os.ReadFile(csvPath)
	if !strings.Contains(string(csvData), "123.45") {
		t.Fatalf("csv total missing: %s", csvData)
	}
	mdData, _ := os.ReadFile(mdPath)
	if !strings.Contains(string(mdData), "合计金额 **123.45**") {
		t.Fatalf("md total missing: %s", mdData)
	}
}

func TestCSVSafeCell(t *testing.T) {
	if got := csvSafeCell("=SUM(A1)"); !strings.HasPrefix(got, "'") {
		t.Fatalf("formula not neutralized: %q", got)
	}
	if got := csvSafeCell(`he said "hi", ok`); got != `"he said ""hi"", ok"` {
		t.Fatalf("quote escape wrong: %q", got)
	}
}

// TestRenderInvoiceXMLPDF 冒烟：有中文字体时 XML 渲染产物必须是合法 PDF。
// 无字体环境（CI）跳过——Harvest 对该场景会走 failed 路径并提示配置字体。
func TestRenderInvoiceXMLPDF(t *testing.T) {
	font := FindChineseFont(t.TempDir())
	if font == "" {
		t.Skip("no CJK font available on this host")
	}
	inv := &Invoice{
		Category: "通信", Seller: "云服务商", Amount: 256.9,
		InvoiceDate: "2026-09-01", InvoiceNo: "88889999",
		Subject: "阿里云账单", UpdatedAt: time.Now().Unix(),
	}
	data, err := RenderInvoiceXMLPDF(font, inv, []byte("<Invoice/>"))
	if err != nil {
		t.Fatal(err)
	}
	if !isPDFBytes(data) {
		t.Fatal("render output is not pdf")
	}
}
