package email

// invoice_qqwallet_test.go — 真实 QQ Wallet 发票邮件的字段提取。
//
// 背景（2026-10-01 真实数据，56551681@qq.com）：
// 两张真实发票邮件的正文是**英文**格式：
//
//	Invoice number: 24317200000907012698
//	Total tax-inclusive amount: CNY 126.00
//	Seller name: Tencent Cloud Computing Co Ltd
//
// 而主题是 `[QQ Wallet] Electronic Invoice Issuance Notice`。提取器把
// subject+snippet 拼在一起跑正则，三个字段**全部提取错误**（库里的实际值）：
//
//	invoice_no = "Issuance"   （主题里 "Invoice" + "Issuance" 被当成发票号）
//	seller     = "name:"      （snippet 的 "Seller name:" 被截成 "name:"）
//	amount     = 0.00         （"CNY 126.00" 不认 CNY 前缀，金额丢失）
//
// 后果直击需求 3：规范文件名是 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`，
// 金额丢失 + 单位变成 "name:" 会让产物变成 `其他-name:-0.00-….pdf`，
// 完全不可用于对账。
//
// 这组用例锁住修复后的行为。每个用例都配负控说明：把对应正则的修复撤掉，
// 该用例必须转红。

import (
	"strings"
	"testing"
)

func qqWalletInvoiceEmail(no, amount string) Email {
	return Email{
		FromName:    "QQ Wallet",
		FromAddress: "56551681@qq.com",
		Subject:     "[QQ Wallet] Electronic Invoice Issuance Notice",
		Snippet: "Dear user, your electronic invoice has been issued.\r\n" +
			"Invoice number: " + no + "\r\n" +
			"Total tax-inclusive amount: CNY " + amount + "\r\n" +
			"Seller name: Tencent Cloud Computing Co Ltd\r\n" +
			"Invoice details please see attachment (PDF).",
		HasAttachments: true,
	}
}

func TestExtractInvoice_QQWalletExtractsRealInvoiceNo(t *testing.T) {
	// 主题里的 "Invoice Issuance" 不该被当成发票号；真实发票号只能来自
	// snippet 的 "Invoice number: 24317200000907012698"。
	em := qqWalletInvoiceEmail("24317200000907012698", "126.00")
	inv, ok := ExtractInvoice(em, "")
	if !ok {
		t.Fatal("QQ Wallet invoice with attachment must be extracted")
	}
	if inv.InvoiceNo != "24317200000907012698" {
		t.Fatalf("InvoiceNo=%q, want 24317200000907012698 (must not be the subject word \"Issuance\")", inv.InvoiceNo)
	}
}

func TestExtractInvoice_QQWalletExtractsAmountWithCNYPrefix(t *testing.T) {
	// "Total tax-inclusive amount: CNY 126.00" —— CNY 是 ISO 4217 货币代码，
	// 不是货币符号，旧正则的 [¥￥$€£] 白名单不认它，于是金额整个丢失。
	em := qqWalletInvoiceEmail("24317200000907012698", "126.00")
	inv, ok := ExtractInvoice(em, "")
	if !ok {
		t.Fatal("must extract")
	}
	if inv.Amount != 126.00 {
		t.Fatalf("Amount=%v, want 126.00 (CNY-prefixed amount must be parsed)", inv.Amount)
	}
}

func TestExtractInvoice_QQWalletSecondInvoiceHasOwnAmount(t *testing.T) {
	// 第二张真实发票金额不同（CNY 328.50）。它必须被独立提取出正确的金额，
	// 否则两封同主题邮件会得到同一份错误金额。
	em := qqWalletInvoiceEmail("24317200000907012703", "328.50")
	inv, ok := ExtractInvoice(em, "")
	if !ok {
		t.Fatal("must extract")
	}
	if inv.Amount != 328.50 {
		t.Fatalf("Amount=%v, want 328.50", inv.Amount)
	}
}

func TestExtractInvoice_QQWalletExtractsSeller(t *testing.T) {
	// "Seller name: Tencent Cloud Computing Co Ltd" -> seller 应该是
	// "Tencent Cloud Computing Co Ltd"，而不是被截断的 "name:"。
	em := qqWalletInvoiceEmail("24317200000907012698", "126.00")
	inv, ok := ExtractInvoice(em, "")
	if !ok {
		t.Fatal("must extract")
	}
	if strings.HasPrefix(inv.Seller, "name") {
		t.Fatalf("Seller=%q looks like a truncated 'Seller name:' label, want the company name", inv.Seller)
	}
	if !strings.Contains(strings.ToLower(inv.Seller), "tencent") {
		t.Fatalf("Seller=%q, want it to contain 'Tencent'", inv.Seller)
	}
}

func TestExtractInvoice_QQWalletFilenameIsUsable(t *testing.T) {
	// 端到端：提取结果喂给规范文件名，必须是能对账的形态
	// {费用类型}-{对方单位}-{金额}-{日期}.pdf —— 而不是 `其他-name:-0.00-….pdf`。
	em := qqWalletInvoiceEmail("24317200000907012698", "126.00")
	inv, ok := ExtractInvoice(em, "")
	if !ok {
		t.Fatal("must extract")
	}
	name := InvoiceFileName(inv)
	if strings.Contains(name, "name:") {
		t.Fatalf("filename %q still contains the truncated seller label", name)
	}
	if strings.Contains(name, "-0.00-") {
		t.Fatalf("filename %q lost the amount", name)
	}
	if !strings.HasSuffix(name, ".pdf") {
		t.Fatalf("filename %q must end with .pdf", name)
	}
}
