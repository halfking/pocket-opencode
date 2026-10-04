package email

// invoice_date_english_test.go — 英文月份名的开票日期抽不出来。
//
// ## 缺陷（2026-10-04 08:00 定时流水线真实产出）
//
// 台账里唯一一行 `invoice_date` 为空的：
//
//	inv_1791072004984034300_1  amount=8.00  seller=X
//	file_name = 通信-X-8.00-2026-10-04.pdf
//
// 文件名里的日期是 **10-04 = 采集当天**，不是开票日期。来源邮件的 snippet
// 原文（逐字复制，未改写）：
//
//	X (https://about.x.com) X Receipt from X $8.00 Paid September 17, 2026
//	(invoice illustration […]) Download invoice (https://pay.stripe.com/…)
//
// 正文明写 **September 17, 2026**，而 `invoice_date` 是空的。
//
// ## 根因：`parseInvoiceDateAt` 的三层**全都只认数字日期**
//
//	reInvoiceDate     `(?:\d{4}[-/年.]\d{1,2}[-/月.]\d{1,2}|\d{8})`
//	reStatementDate   同上
//	reLooseCNDate     `\d{4}年\d{1,2}月\d{1,2}`
//
// 三条的**日期组**都不含英文月份名，所以 `September 17, 2026` 一个都匹配不上。
// 这不是「抽不出来」而是「压根没有这条通路」——
// 与词边界那类「判据过松」正好相反：**缺一条通路**。
//
// ## 修法边界：只加**带标签**的英文日期，且排在三层之后
//
// 两条刻意的限制，都写在这里以免后来者把它们当成 bug：
//
//  1. **必须带标签**（Date / Paid / Issued / Invoice Date / 开票日期…）。
//     裸英文日期太容易撞无关内容——CI 通知里的续订日、营销邮件里的活动日
//     都是 `September 17, 2026` 这种形态，而它们**不是开票日期**。
//     宁可漏抽（回落采集当天，与现状一致），不可抽错。
//  2. **排在现有三层之后**。这是**纯增量**：原来能抽出来的照旧抽，
//     只有原来返回 `""` 的才可能变成有值。⇒ 不可能改变任何已有抽取结果。
//
// 只支持 `Month DD, YYYY`（含 `Sept.`、`17th` 这类形态），不支持 `DD Month YYYY`：
// 真实库里唯一一例就是前者，为它扩到两种格式是凭空 generality。
import (
	"testing"
	"time"
)

// TestParseInvoiceDate_RealStripeReceipt 这条是**缺陷复现**：
// 期望值是独立字面量 "2026-09-17"，不调任何被测函数生成。
func TestParseInvoiceDate_RealStripeReceipt(t *testing.T) {
	// 真实 snippet 原文（Stripe receipt），URL 保留原样以确保
	// 「日期不会从 URL 里被误抽出来」这件事也被覆盖到。
	const snippet = "X (https://about.x.com) X Receipt from X $8.00 Paid September 17, 2026 " +
		"(invoice illustration [https://stripe-images.s3.amazonaws.com/emails/" +
		"invoices_invoice_illustration.png]) Download invoice " +
		"(https://pay.stripe.com/invoice/acct_1Ika5JA3KZ32dPo1/live_YWNjdF8xSWthNUpBM0taMzJk/" +
		"UG8xLF9WSEZXdmNtODhZZWdkSFRHYnFVTE9SMG5hajdyQUxVLDE4MDIwMjU5Ng0200eB22Ae5a/pdf?s=em) " +
		"Download receipt (https://dashboard.stripe.com/receipts/invoices/CAcQARoXChVhY2N0XzFJa2E1SkEzS1oz)"

	const want = "2026-09-17"
	got := parseInvoiceDateAt(snippet, time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local))
	if got != want {
		t.Fatalf("parseInvoiceDateAt=%q，want %q\n"+
			"  正文明写 \"Paid September 17, 2026\"，却抽不出日期 ⇒ invoice_date 空 ⇒\n"+
			"  文件名里的日期退化成**采集当天**，交财务时开票日期是错的。",
			got, want)
	}
}

// TestParseInvoiceDate_EnglishLabelVariants 钉住标签与月份名的各种写法。
func TestParseInvoiceDate_EnglishLabelVariants(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local)
	cases := []struct {
		name, text, want string
	}{
		{"月缩写带点", "Date: Sept. 3, 2026", "2026-09-03"},
		{"序数后缀", "Paid November 21st, 2025", "2025-11-21"},
		{"三字母缩写", "Issued Dec 1, 2025", "2025-12-01"},
		{"全小写月份", "date: january 9, 2026", "2026-01-09"},
		{"Issued 标签", "Issued March 5, 2026", "2026-03-05"},
		{"Invoice Date 标签", "Invoice Date: July 4, 2026", "2026-07-04"},
		{"中文标签接英文月", "开票日期：August 8, 2026", "2026-08-08"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseInvoiceDateAt(c.text, now); got != c.want {
				t.Errorf("parseInvoiceDateAt(%q)=%q，want %q", c.text, got, c.want)
			}
		})
	}
}

// TestParseInvoiceDate_EnglishUnlabeledIsIgnored 钉住上面第 1 条限制：
// **无标签**的英文日期不许被抽成开票日期。
//
// 这条是防止修复「顺手变宽」的反向保护。`September 17, 2026` 这种形态在
// 真实语料里大量出现在**与开票无关**的地方（续订日、活动日、CI 通知里的
// 日期），一旦放开裸匹配，信用卡/服务类邮件会拿到一个看着正常、实则无关的
// 开票日期——那比空值更坏，因为它看起来像正确答案。
func TestParseInvoiceDate_EnglishUnlabeledIsIgnored(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local)
	for _, text := range []string{
		"Your subscription renews on September 17, 2026",
		"Black Friday deals run November 28, 2025 through December 1, 2025",
		"Action required: please verify by September 17, 2026",
	} {
		if got := parseInvoiceDateAt(text, now); got != "" {
			t.Errorf("parseInvoiceDateAt(%q)=%q，want \"\"\n"+
				"  无标签的英文日期必须不抽：它更可能是续订日/活动日而不是开票日期。", text, got)
		}
	}
}

// TestParseInvoiceDate_EnglishFutureIsSkipped 钉住「跳过未来日期」在英文形态上
// 同样成立——否则英文层会成为绕过那道防线的新入口。
func TestParseInvoiceDate_EnglishFutureIsSkipped(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local)
	// 2026-10-25 是原文里的「到期还款日」那一类：比当天晚 21 天。
	if got := parseInvoiceDateAt("Due date: October 25, 2026", now); got != "" {
		t.Errorf("parseInvoiceDateAt=%q，want \"\"（未来日期必须跳过，不能当作开票日期）", got)
	}
}

// TestParseInvoiceDate_CNLabelStillWinsOverEnglish 钉住第 2 条限制：
// 既有三层**优先于**英文层 ⇒ 英文层是纯增量，不改变任何已有抽取结果。
func TestParseInvoiceDate_CNLabelStillWinsOverEnglish(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local)
	const text = "开票日期 2026年09月15日 开票方 浙江智谱新篇科技有限公司 Paid September 17, 2026"
	const want = "2026-09-15"
	if got := parseInvoiceDateAt(text, now); got != want {
		t.Errorf("parseInvoiceDateAt=%q，want %q\n"+
			"  既有中文标签层必须仍然优先；英文层只能在此之后兜底。", got, want)
	}
}

// TestParseInvoiceDate_EnglishDoesNotFireOnURLDateSegment 防止日期从 URL 的
// 路径段里被抽出来——词边界那次教训（`/billing/`）的同族，这里是路径形态。
func TestParseInvoiceDate_EnglishDoesNotFireOnURLDateSegment(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 5, 0, time.Local)
	const text = "Download invoice (https://pay.stripe.com/invoice/2026/09/17/pdf)"
	if got := parseInvoiceDateAt(text, now); got != "" {
		t.Errorf("parseInvoiceDateAt=%q，want \"\"（URL 路径段不是开票日期）", got)
	}
}

// TestEnglishMonthNames 单独钉住月份名→数字的映射表：
// 12 个月全列，且大小写不敏感，避免「只测了一个月」这种覆盖盲区。
//
// 这条是在实现**之后**才补进来的（原先引用尚不存在的 englishMonthNumber，
// 会让整包编译失败——那是假红，不能当「判据先转红」的证据）。
func TestEnglishMonthNames(t *testing.T) {
	want := map[string]int{
		"january": 1, "feb": 2, "February": 2, "MAR": 3, "apr": 4,
		"May": 5, "june": 6, "Jul": 7, "august": 8, "Sept": 9,
		"October": 10, "nov": 11, "december": 12,
	}
	for name, num := range want {
		if got := englishMonthNumber(name); got != num {
			t.Errorf("englishMonthNumber(%q)=%d，want %d", name, got, num)
		}
	}
	if got := englishMonthNumber("notamonth"); got != 0 {
		t.Errorf("englishMonthNumber(\"notamonth\")=%d，want 0（不能把非月份名映射成 1）", got)
	}
	// 兜底不是「取 1」：月份名无效时若返回 1，任何非月份名都会被静默当成 1 月。
	if got := englishMonthNumber(""); got != 0 {
		t.Errorf("englishMonthNumber(\"\")=%d，want 0", got)
	}
}

// TestNormalizeEnglishInvoiceDate_RejectsRolloverDates 钉住「不存在的日期不许
// 被静默进位」：`time.Date(2026, 2, 30)` 会变成 3 月 2 日——
// 那是一个看起来完全正常的日期，比返回空值危险得多。
func TestNormalizeEnglishInvoiceDate_RejectsRolloverDates(t *testing.T) {
	for _, c := range []struct {
		y, m, d int
		want    string
	}{
		{2026, 2, 30, ""},           // 2026 非闰年
		{2026, 13, 1, ""},           // 月份越界
		{2026, 1, 32, ""},           // 日越界
		{2026, 1, 0, ""},            // 日为 0
		{2024, 2, 29, "2024-02-29"}, // 闰年 2 月 29 日是合法日期，必须放过
		{2026, 9, 17, "2026-09-17"},
	} {
		if got := normalizeEnglishInvoiceDate(c.y, c.m, c.d); got != c.want {
			t.Errorf("normalizeEnglishInvoiceDate(%d,%d,%d)=%q，want %q", c.y, c.m, c.d, got, c.want)
		}
	}
}
