package email

// invoice_date 回填链路的测试。
//
// 背景：IMAP 路径只落 envelope，开票日期在正文里。step1.5 因此有一条 "date"
// 分支专门为「已命中发票但缺开票日期」的邮件拉原文补日期。真库里的
// inv_1790785758514563200_1 就是这个症状——文件名
// 「其他-杭州创客家…-3500.00-2026-10-01.pdf」里的 2026-10-01 是采集当天，
// 该行 invoice_date 至今为空。
//
// 这段判定原先埋在 step1.5 的巨型循环里（invoiceCandidate 是函数内局部类型），
// 从外部完全无法测试。抽成纯函数后才有了这里的用例。
//
// 负控：
//   - invoiceBodyReason 去掉 "date" 分支 -> 本文件转红
//   - applyParsedBodyDate 改成无条件覆盖 -> 本文件转红

import (
	"strings"
	"testing"
)

// 命中发票但没有开票日期 -> 必须拉原文（否则规范文件名退化成采集当天）。
func TestInvoiceBodyReason_DateWhenHitWithoutDate(t *testing.T) {
	e := Email{ID: "em-1", Subject: "您收到来自某公司的发票，发票号码：123，金额：100.00元"}
	inv := &Invoice{InvoiceNo: "123", Amount: 100}
	if got := invoiceBodyReason(true, inv, e); got != "date" {
		t.Fatalf("invoiceBodyReason = %q, want \"date\" —— 命中发票却缺日期时不拉原文，"+
			"规范文件名会用采集当天冒充开票日期", got)
	}
}

// 已有开票日期 -> 不用拉（省一次完整 IMAP 会话）。
func TestInvoiceBodyReason_NoJobWhenDateAlreadyPresent(t *testing.T) {
	e := Email{ID: "em-1", Subject: "发票"}
	inv := &Invoice{InvoiceDate: "2026-05-01"}
	if got := invoiceBodyReason(true, inv, e); got != "" {
		t.Fatalf("invoiceBodyReason = %q, want \"\" —— 已有日期不该再开 IMAP 会话", got)
	}
}

// 未命中但关键词像发票 -> candidate。
func TestInvoiceBodyReason_CandidateWhenNotHit(t *testing.T) {
	e := Email{ID: "em-1", Subject: "对账单"}
	inv := &Invoice{}
	got := invoiceBodyReason(false, inv, e)
	if got != "candidate" && got != "" {
		t.Fatalf("invoiceBodyReason = %q, want \"candidate\" 或 \"\"", got)
	}
}

// 两者都不满足 -> 不拉原文。
func TestInvoiceBodyReason_NoJobWhenUnrelated(t *testing.T) {
	e := Email{ID: "em-1", Subject: "午餐券提醒", FromAddress: "hr@example.com"}
	inv := &Invoice{}
	if got := invoiceBodyReason(false, inv, e); got != "" {
		t.Fatalf("invoiceBodyReason = %q, want \"\" —— 无关邮件不该拉原文", got)
	}
}

// hit=true 但 inv 为 nil 不能 panic（ExtractInvoice 允许 hit 与指针不同步）。
func TestInvoiceBodyReason_NilInvoiceSafe(t *testing.T) {
	e := Email{ID: "em-1", Subject: "发票"}
	if got := invoiceBodyReason(true, nil, e); got != "" {
		t.Fatalf("invoiceBodyReason = %q, want \"\"", got)
	}
}

// 正文里有「开票日期」时必须补上，并且落到文件名上。
func TestApplyParsedBodyDate_FillsFromBody(t *testing.T) {
	inv := &Invoice{Seller: "某公司", Amount: 3500}
	body := "您好，附件为电子发票。\r\n开票日期：2026-05-01\r\n金额：3500.00元"
	if got := applyParsedBodyDate(inv, body); got != "2026-05-01" {
		t.Fatalf("applyParsedBodyDate = %q, want 2026-05-01", got)
	}
	if inv.InvoiceDate != "2026-05-01" {
		t.Fatalf("inv.InvoiceDate = %q, want 2026-05-01", inv.InvoiceDate)
	}
	// 这才是关键：文件名用真实开票日期，而不是采集当天。
	name := InvoiceFileName(inv)
	if !strings.Contains(name, "2026-05-01") {
		t.Fatalf("文件名 %q 未包含真实开票日期 2026-05-01", name)
	}
}

// 已有日期时**不覆盖**：正文里的散落日期未必是票面日期。
func TestApplyParsedBodyDate_DoesNotOverwriteExisting(t *testing.T) {
	inv := &Invoice{InvoiceDate: "2026-01-02"}
	body := "开票日期：2026-05-01"
	if got := applyParsedBodyDate(inv, body); got != "2026-01-02" {
		t.Fatalf("applyParsedBodyDate = %q, want 2026-01-02（已有日期不得被正文覆盖）", got)
	}
}

// 正文里没有日期 -> 保持空（由调用方决定后续，不要凭空造一个）。
func TestApplyParsedBodyDate_NoDateInBody(t *testing.T) {
	inv := &Invoice{}
	if got := applyParsedBodyDate(inv, "本邮件不含任何日期信息。"); got != "" {
		t.Fatalf("applyParsedBodyDate = %q, want \"\"", got)
	}
	if inv.InvoiceDate != "" {
		t.Fatalf("inv.InvoiceDate = %q, want 空", inv.InvoiceDate)
	}
}

// 空正文 / nil 都不能 panic。
func TestApplyParsedBodyDate_EmptyAndNilSafe(t *testing.T) {
	inv := &Invoice{}
	if got := applyParsedBodyDate(inv, ""); got != "" {
		t.Fatalf("applyParsedBodyDate(empty) = %q, want \"\"", got)
	}
	if got := applyParsedBodyDate(nil, "开票日期：2026-05-01"); got != "" {
		t.Fatalf("applyParsedBodyDate(nil) = %q, want \"\"", got)
	}
}
