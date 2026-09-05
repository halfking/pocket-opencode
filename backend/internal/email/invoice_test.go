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
