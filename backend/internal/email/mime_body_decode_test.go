package email

// mime_body_decode_test.go — 正文解码链路（真实 QQ 发票邮件是 base64 + GBK）。
//
// 线上现象：那封真发票的「开票日期」回填没生效。先确认是**解码链路坏了**
// 还是**这封邮件正文里本来就没有日期**——两者的处理完全不同。
// 判据：构造一封 base64+GBK 的真实形态邮件，看 ParseMIMEMessage 能否还原中文与日期。

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func b64GBK(t *testing.T, s string) string {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestParseMIMEMessage_Base64GBKBody(t *testing.T) {
	body := "尊敬的用户：\r\n开票日期：2026-05-24\r\n价税合计：￥3500.00\r\n发票链接：https://dzfp-oss.oss-cn-hangzhou.aliyuncs.com/invoice/2633.pdf\r\n"
	raw := strings.Join([]string{
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=GBK",
		"Content-Transfer-Encoding: base64",
		"",
		b64GBK(t, body),
		"",
	}, "\r\n")

	parsed, err := ParseMIMEMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(parsed.TextBody, "开票日期") {
		t.Fatalf("base64+GBK body not decoded: %q", parsed.TextBody)
	}
	if d := ParseInvoiceDate(parsed.TextBody); d != "2026-05-24" {
		t.Fatalf("invoice date from decoded body = %q, want 2026-05-24", d)
	}
}

// multipart + quoted-printable + 中文（另一类真实形态）也要能取到日期。
func TestParseMIMEMessage_MultipartQuotedPrintableDate(t *testing.T) {
	raw := strings.Join([]string{
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="B"`,
		"",
		"--B",
		`Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"=E5=BC=80=E6=98=A5=E6=97=A5=E6=9C=9F=EF=BC=9A2026-07-01",
		"--B--",
		"",
	}, "\r\n")
	parsed, err := ParseMIMEMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d := ParseInvoiceDate(parsed.TextBody); d != "2026-07-01" {
		t.Fatalf("date = %q (body=%q)", d, parsed.TextBody)
	}
}

// HTML 正文（有的发票邮件只有 HTML）也要能取到日期。
func TestParseMIMEMessage_HTMLOnlyBody(t *testing.T) {
	raw := strings.Join([]string{
		"MIME-Version: 1.0",
		`Content-Type: text/html; charset="utf-8"`,
		"",
		"<html><body><p>开票日期：2026-08-15</p><p>金额：99.00元</p></body></html>",
		"",
	}, "\r\n")
	parsed, err := ParseMIMEMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	text := parsed.TextBody
	if text == "" {
		text = parsed.HTMLBody
	}
	if d := ParseInvoiceDate(text); d != "2026-08-15" {
		t.Fatalf("date from html = %q (text=%q html=%q)", d, parsed.TextBody, parsed.HTMLBody)
	}
	// 金额也要能从 HTML 里抽到（HTML 发票邮件同样常见）
	e := Email{ID: "x", Subject: "电子发票", Snippet: ""}
	inv, hit := ExtractInvoice(e, text)
	if !hit || inv.Amount != 99.00 {
		t.Fatalf("amount from html body = %v hit=%v", inv, hit)
	}
	if inv.InvoiceDate != "2026-08-15" {
		t.Fatalf("invoice date = %q", inv.InvoiceDate)
	}
}

// 没有任何日期时必须返回空串，不能编一个出来（编日期 = 归档凭证对不上账）。
func TestParseInvoiceDate_NoDateReturnsEmpty(t *testing.T) {
	if d := ParseInvoiceDate("感谢您的支持，本邮件不含日期信息。"); d != "" {
		t.Fatalf("invented a date: %q", d)
	}
	if d := ParseInvoiceDateFromBytes(bytes.Repeat([]byte("no date here "), 100)); d != "" {
		t.Fatalf("invented a date from bytes: %q", d)
	}
}
