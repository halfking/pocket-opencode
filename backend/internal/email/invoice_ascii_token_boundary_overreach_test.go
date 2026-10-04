package email

// invoice_ascii_token_boundary_overreach_test.go — 钉住英文词元词边界的**两级**规则，
// 尤其是「URL 标点只在两侧同时出现时才算路径段」这一条。
//
// ## 根因（2026-10-04 本轮审计实测，不是推演）
//
// e54d797d / 193ed70c 把「词边界」实现成一个字符类，把 URL 标点
// `- . / : ? & = # @ + %` 与字母数字**一起**算进「词内」。本轮逐条实测发现
// 这修掉了一批子串假阳性，同时**静默造出了一批真发票的假阴性**：
//
//	invoiceKeywordHit("Invoice: ACME Corp")               = false  ← 漏判
//	invoiceKeywordHit("Your invoice: https://…/inv.pdf")  = false  ← 漏判
//	invoiceKeywordHit("Receipt: #2662-4636-8457")         = false  ← 漏判
//	invoiceKeywordHit("Invoice#INV-2026-0001")            = false  ← 漏判
//	invoiceKeywordHit("Invoice/Receipt for September")    = false  ← 漏判
//
//	classifyInvoiceCategory("AWS: 您的账单")               = "其他"  ← 原为「通信」
//	classifyInvoiceCategory("Stripe receipt for SaaS. Thanks!") = "其他" ← 原为「通信」
//	classifyInvoiceCategory("invoice from a hotel.com partner") = "其他" ← 原为「住宿」
//
// 「Invoice:」是发票邮件最常见的英文主题形态。这些漏判的代价与被修掉的
// 假阳性**同量级**：假阳性白占 maxInvoiceBodyFetches=24 的一格，
// 假阴性则让真发票永远进不了候选队列。
//
// ## 为什么上一轮的判据没抓到
//
// invoice_keyword_wordboundary_test.go 的反向保护用例
// （TestInvoiceKeywordHit_StillAcceptsRealInvoiceSemantics）只用了
// **空格分隔**的形态（"Your invoice is ready for download"），
// 恰好绕开了所有出问题的标点。
// ⇒ 「反向保护存在」不等于「反向保护覆盖了修复动过的那一维」。
//
// ## 期望值从哪来
//
// 全部是**独立字面量**：`mustHit` / `mustNotHit` 两张表都是本轮修复前
// 实测出来的返回值（见文件头），不用被测函数生成，也不调 classifyInvoiceCategory
// 之类的被测函数反推期望。

import "testing"

// asciiTokenPunctuationAdjacentMustHit 是「关键词紧邻正常标点」的真发票形态。
//
// 这些是修复前**实测为 false** 的输入，即回归证据本身。
var asciiTokenPunctuationAdjacentMustHit = []struct {
	text string
	why  string
}{
	{"Invoice: ACME Corp", "冒号是最常见的发票主题分隔符"},
	{"Your invoice: https://example.com/inv.pdf", "冒号后直接跟下载链接"},
	{"Receipt: #2662-4636-8457", "Receipt 后接单号，冒号分隔"},
	{"Invoice#INV-2026-0001", "# 号分隔（部分平台主题格式）"},
	{"Invoice/Receipt for September", "斜杠分隔的复合词"},
	{"Invoice - ACME", "空格-连字符（修复前已通过，留作对照）"},
	{"Tax invoice & receipt attached", "& 连接（修复前已通过，留作对照）"},
	{"Invoice, please pay", "逗号（修复前已通过，留作对照）"},
}

// TestASCIITokenHit_PunctuationAdjacentKeywordsStillHit 是本轮的核心回归护栏：
// 关键词紧邻**单个** URL 标点时必须仍然命中。
func TestASCIITokenHit_PunctuationAdjacentKeywordsStillHit(t *testing.T) {
	for _, c := range asciiTokenPunctuationAdjacentMustHit {
		if !invoiceKeywordHit(c.text) {
			t.Errorf("invoiceKeywordHit(%q) = false，但 %s —— 词边界收紧过头，"+
				"真发票被挡在候选队列外（修复前实测为 false）", c.text, c.why)
		}
	}
}

// asciiTokenMustNotHit 是两类**必须**被否决的形态：更长词的一部分、URL 路径段。
//
// 保留这些是为了钉住「修正边界规则」没有把 e54d797d 修掉的东西放回来——
// 收紧过头和放松过头都是缺陷。
var asciiTokenMustNotHit = []struct {
	text string
	why  string
}{
	{"Click to activate your account now", "activation 里的 vat 子串"},
	{"This is a private repository", "private 里的 vat 子串"},
	{"You are invited to the conference", "invite 里的 vat 子串"},
	{"see https://console.aws.amazon.com/billing/home for details", "billing 是 URL 路径段（两侧都是 /）"},
	{"open https://x.io/invoice/download now", "invoice 是 URL 路径段（两侧都是 /）"},
	{"https://my-account.aws.billing.example.com/x", "aws 两侧都是 URL 标点"},
}

// TestASCIITokenHit_RejectsSubstringsAndURLSegments 钉住否决方向不被放松。
func TestASCIITokenHit_RejectsSubstringsAndURLSegments(t *testing.T) {
	for _, c := range asciiTokenMustNotHit {
		if invoiceKeywordHit(c.text) {
			t.Errorf("invoiceKeywordHit(%q) = true，但 %s —— 词边界被放松了", c.text, c.why)
		}
	}
}

// TestASCIITokenHit_TwoSidedURLRule 直接钉「两侧」这条规则本身，
// 避免它只在上面两张表的样本上恰好成立。
//
// 同一批字符，一侧出现 ⇒ 命中（正常标点），两侧出现 ⇒ 否决（URL 路径段）。
// 这是本轮修复的全部内容，用最小对照把它钉住。
func TestASCIITokenHit_TwoSidedURLRule(t *testing.T) {
	cases := []struct {
		text string
		want bool
		why  string
	}{
		{"billing", true, "独立出现的词"},
		{"see billing now", true, "两侧都是空格"},
		{"billing: update", true, "右侧单个冒号 = 正常标点"},
		{"/billing now", true, "左侧单个斜杠 = 正常标点"},
		{"a/billing", true, "右侧是文本结尾（零宽），单侧判不出路径段 —— 如实记的限制"},
		{"x/billing/y", false, "两侧都是斜杠 = URL 路径段"},
		{"see: billing, ok", true, "冒号与逗号都不是成对的 URL 标点"},
	}
	for _, c := range cases {
		if got := invoiceKeywordHit(c.text); got != c.want {
			t.Errorf("invoiceKeywordHit(%q) = %v，want %v（%s）", c.text, got, c.want, c.why)
		}
	}
}

// TestClassifyInvoiceCategory_PunctuationAdjacentTokens 是**同一个缺陷的第二个入口**。
//
// 费用类型侧曾把同一段有缺陷的字符类「刻意写成同一个」以图两处一致，
// 结果是同一个假阴性也复制了一份。现在两侧共用 asciiTokenHit。
func TestClassifyInvoiceCategory_PunctuationAdjacentTokens(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"AWS: 您的账单", "通信"},
		{"Stripe receipt for SaaS. Thanks!", "通信"},
		{"invoice from a hotel.com partner", "住宿"},
		{"Amazon Web Services (AWS) 发票", "通信"},
		{"saas subscription", "通信"},
		{"azure portal invoice", "通信"},
		{"https://stripe-images.s3.amazonaws.com/logo.png", "其他"},
	}
	for _, c := range cases {
		if got := classifyInvoiceCategory(c.text); got != c.want {
			t.Errorf("classifyInvoiceCategory(%q) = %q，want %q", c.text, got, c.want)
		}
	}
}

// TestExtractInvoiceURLs_IgnoresInlineResourceAttributes 钉住内联资源属性的覆盖面。
//
// reHTMLSrcs 原实现的注释声称覆盖 `background`，正则却只匹配 `src=`，
// 于是 `background=` 里的营销横幅仍被当成下载候选——
// **注释与代码不一致，而那正是本函数要堵的同一个泄漏**。
//
// 每条都同时断言「横幅没被收进来」和「同体内的真下载链接仍被收进来」，
// 后半句是为了让这条判据不能靠「一律返回 nil」蒙混过关。
func TestExtractInvoiceURLs_IgnoresInlineResourceAttributes(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantAbsent string
	}{
		{
			name:       "img src 无扩展名的横幅",
			body:       `<img src="https://cdn.baiwang.com/mail/banner?w=750&h=200&t=abc">`,
			wantAbsent: "https://cdn.baiwang.com/mail/banner?w=750&h=200&t=abc",
		},
		{
			name:       "div background 无扩展名的横幅",
			body:       `<div background="https://cdn.baiwang.com/mail/bg?w=750&h=200&t=abc">x</div>`,
			wantAbsent: "https://cdn.baiwang.com/mail/bg?w=750&h=200&t=abc",
		},
		{
			name:       "poster 属性",
			body:       `<video poster="https://cdn.baiwang.com/mail/p?w=750&t=abc"></video>`,
			wantAbsent: "https://cdn.baiwang.com/mail/p?w=750&t=abc",
		},
		{
			name:       "data-src 懒加载属性",
			body:       `<img data-src="https://cdn.baiwang.com/mail/lazy?w=750&t=abc">`,
			wantAbsent: "https://cdn.baiwang.com/mail/lazy?w=750&t=abc",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractInvoiceURLs(c.body)
			for _, u := range got {
				if u == c.wantAbsent {
					t.Errorf("内联资源 URL 被当成下载候选收进来了：%q\n  body: %s", u, c.body)
				}
			}
		})
	}

	// 反向：同一体里的真下载链接必须仍然被收集，否则上面的断言可以靠「全都不收」蒙混。
	body := `<img src="https://cdn.x.com/banner?w=750&t=abc">` +
		`<a href="https://invoice.example.com/download/INV-2026-0001.pdf">下载发票</a>`
	got := extractInvoiceURLs(body)
	found := false
	for _, u := range got {
		if u == "https://invoice.example.com/download/INV-2026-0001.pdf" {
			found = true
		}
	}
	if !found {
		t.Errorf("真下载链接被误伤了，extractInvoiceURLs(%q) = %#v", body, got)
	}
}
