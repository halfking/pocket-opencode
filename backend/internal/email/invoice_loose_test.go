package email

// invoice_loose_test.go — 「金额只印在附件里」的账单邮件必须能进采集流程。
//
// BUG-AO（实测）：主题「9 月度对账单（附件 + 内嵌图表）」、正文只有一句
// 「见附件」的邮件，规则层在 ExtractInvoice 末尾因「金额=0 且发票号为空」
// 直接丢弃，采集器根本没机会看那个 PDF 附件 —— 流水线 invoices.Processed=0、
// 发票列表 0 条。这里钉住放宽后的边界：带发票类附件就建档，不带附件的
// 营销「账单提醒」仍然丢弃。

import "testing"

func mailWithStatement() Email {
	return Email{
		ID:        "em-stmt",
		AccountID: "acct-1",
		Subject:   "9 月度对账单（附件 + 内嵌图表）",
		Snippet:   "9 月度对账单见附件，图表见正文。",
		FromName:  "云服务商账单中心",
	}
}

var stmtPDF = append([]byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"), 0x00)

func TestExtractInvoiceLoose_AttachmentCarriesAmountKeepsRecord(t *testing.T) {
	e := mailWithStatement()

	// 旧行为：没有附件证据 → 丢弃（防营销邮件灌满发票列表）
	if _, hit := ExtractInvoice(e, ""); hit {
		t.Fatal("no-attachment bill mail must stay rejected (anti-spam guard)")
	}

	inv, hit := ExtractInvoiceLoose(e, "见附件", true)
	if !hit {
		t.Fatal("bill mail with a PDF attachment must be filed for harvesting")
	}
	if inv == nil || inv.EmailID != e.ID {
		t.Fatalf("bad invoice record: %+v", inv)
	}
	if inv.Status != "new" {
		t.Fatalf("freshly filed record must be 'new' so the harvester picks it up, got %q", inv.Status)
	}
	if inv.Subject != e.Subject {
		t.Fatalf("subject lost: %q", inv.Subject)
	}
	// 金额/日期允许留空，交给采集器从附件补；但类目与销售方要先有值，
	// 否则规范文件名会退化成 -未知-0-0000-00-00.pdf。
	if inv.Category == "" {
		t.Fatal("category must be inferred even without an amount")
	}
	if inv.Seller == "" {
		t.Fatal("seller must fall back to the sender when the body has none")
	}
}

func TestExtractInvoiceLoose_PureMarketingMailStillDropped(t *testing.T) {
	e := Email{
		ID:          "em-ad",
		Subject:     "账单提醒：本月账单可查",
		Snippet:     "点击查看你的账单，点击即可领券。",
		FromAddress: "promo@example.com",
	}
	if _, hit := ExtractInvoiceLoose(e, e.Snippet, false); hit {
		t.Fatal("marketing mail without attachment must be dropped")
	}
	// 弱关键词 + 无附件 = 不建档；强关键词（发票号）仍照常建档
	e2 := Email{ID: "em-real", Subject: "发票号码 12345678 已开具", Snippet: "发票号码：12345678"}
	if _, hit := ExtractInvoiceLoose(e2, "", false); !hit {
		t.Fatal("real invoice mail with an invoice number must still be filed")
	}
}

func TestHasInvoiceAttachment(t *testing.T) {
	cases := []struct {
		name string
		atts []ParsedAttachment
		want bool
	}{
		{"pdf", []ParsedAttachment{{Filename: "statement.pdf", Data: stmtPDF}}, true},
		{"png", []ParsedAttachment{{Filename: "shot.png", Data: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}}}, true},
		{"xml", []ParsedAttachment{{Filename: "invoice.xml", Data: []byte("<Invoice/>")}}, true},
		{"txt", []ParsedAttachment{{Filename: "readme.txt", Data: []byte("hello")}}, false},
		{"empty", []ParsedAttachment{{Filename: "statement.pdf"}}, false},
		{"none", nil, false},
	}
	for _, c := range cases {
		if got := HasInvoiceAttachment(c.atts); got != c.want {
			t.Fatalf("%s: HasInvoiceAttachment = %v, want %v", c.name, got, c.want)
		}
	}
}
