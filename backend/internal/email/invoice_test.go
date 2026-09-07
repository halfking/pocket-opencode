package email

import "testing"

func TestExtractInvoiceEInvoice(t *testing.T) {
	e := Email{
		ID:          "em1",
		AccountID:   "acc1",
		FromAddress: "billing@example.com",
		FromName:    "示例科技",
		Subject:     "【电子发票】您的增值税电子普通发票已开具",
		Snippet:     "开票日期：2026年09月01日\n发票号码：25612000000123456789\n价税合计（小写）：¥1,280.50\n销售方名称：示例科技有限公司",
	}
	inv, hit := ExtractInvoice(e, "")
	if !hit {
		t.Fatal("expected invoice hit")
	}
	if inv.Kind != "e-invoice" {
		t.Fatalf("kind = %q, want e-invoice", inv.Kind)
	}
	if inv.Amount != 1280.50 {
		t.Fatalf("amount = %v, want 1280.50", inv.Amount)
	}
	if inv.InvoiceNo != "25612000000123456789" {
		t.Fatalf("invoiceNo = %q", inv.InvoiceNo)
	}
	if inv.InvoiceDate != "2026-09-01" {
		t.Fatalf("invoiceDate = %q, want 2026-09-01", inv.InvoiceDate)
	}
	if inv.Seller != "示例科技有限公司" {
		t.Fatalf("seller = %q", inv.Seller)
	}
	if inv.Category != "其他" {
		t.Fatalf("category = %q, want 其他", inv.Category)
	}
}

func TestExtractInvoiceTransport(t *testing.T) {
	e := Email{
		ID:          "em2",
		AccountID:   "acc1",
		FromAddress: "receipt@didichuxing.com",
		FromName:    "滴滴出行",
		Subject:     "滴滴出行发票已开具",
		Snippet:     "您的行程发票已开具，金额￥86.00",
	}
	inv, hit := ExtractInvoice(e, "")
	if !hit {
		t.Fatal("expected invoice hit")
	}
	if inv.Amount != 86.0 {
		t.Fatalf("amount = %v, want 86", inv.Amount)
	}
	if inv.Category != "交通" {
		t.Fatalf("category = %q, want 交通", inv.Category)
	}
	if inv.Seller != "滴滴出行" {
		t.Fatalf("seller fallback to FromName, got %q", inv.Seller)
	}
}

func TestExtractInvoiceNotAMatch(t *testing.T) {
	// 营销邮件：无金额无发票号，不应误提取
	e := Email{
		ID:          "em3",
		AccountID:   "acc1",
		FromAddress: "promo@example.com",
		Subject:     "年度大促发票服务升级公告",
		Snippet:     "点击了解全新开票体验",
	}
	if _, hit := ExtractInvoice(e, ""); hit {
		t.Fatal("marketing email should not match")
	}
	// 普通邮件：无关键词
	e2 := Email{
		ID: "em4", AccountID: "acc1", FromAddress: "a@b.com",
		Subject: "周末聚餐", Snippet: "周六晚上老地方",
	}
	if _, hit := ExtractInvoice(e2, ""); hit {
		t.Fatal("normal email should not match")
	}
}

func TestParseInvoiceDateFormats(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"开票日期：2026年09月01日", "2026-09-01"},
		{"发票日期 2026/9/5", "2026-09-05"},
		{"开票时间:20260901", "2026-09-01"},
		{"电子发票 2026年8月3日 已开具", "2026-08-03"},
		{"Date: 2026-09-02", "2026-09-02"},
		{"no date here", ""},
	}
	for _, c := range cases {
		if got := ParseInvoiceDate(c.in); got != c.want {
			t.Fatalf("ParseInvoiceDate(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExtractInvoiceFillsDateFromLooseBody(t *testing.T) {
	e := Email{
		ID: "em-date", AccountID: "acc1", FromAddress: "a@b.com",
		Subject: "电子发票已开具",
		Snippet: "价税合计：¥12.00 发票号码：25612000000987654321",
	}
	inv, hit := ExtractInvoice(e, "本发票开具于2026年07月18日，请查收")
	if !hit {
		t.Fatal("expected hit")
	}
	if inv.InvoiceDate != "2026-07-18" {
		t.Fatalf("invoiceDate=%q want 2026-07-18", inv.InvoiceDate)
	}
}

func TestSortInvoicesByReceivedDesc(t *testing.T) {
	invoices := []Invoice{
		{ID: "old", EmailDate: 100, CreatedAt: 999},
		{ID: "new", EmailDate: 300, CreatedAt: 1},
		{ID: "mid", EmailDate: 0, CreatedAt: 200},
	}
	SortInvoicesByReceived(invoices)
	if invoices[0].ID != "new" || invoices[1].ID != "mid" || invoices[2].ID != "old" {
		t.Fatalf("order=%s,%s,%s", invoices[0].ID, invoices[1].ID, invoices[2].ID)
	}
}
