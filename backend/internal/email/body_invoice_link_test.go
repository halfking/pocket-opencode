package email

import "testing"

// body_invoice_link_test.go — q3：正文里的发票下载链接也算附件。
//
// 需要分清的是「这封邮件有值得用户看一眼的附件吗」，不是「正文里有没有链接」。
// 判据与 Harvest 阶段真正会去抓的判据共用一份（extractInvoiceURLs +
// scoreInvoiceURL），所以不会出现「📎 亮了但采集器根本不去抓」这种自相矛盾。
//
// 负控（每条都实测过，见提交信息）：
//   - 阈值从 20 降到 10 -> TestBodyHasInvoiceLink_IgnoresWeakSignals 转红
//     （含 "inv" 子串的营销链接会被误判）
//   - 阈值从 20 提到 40 -> TestBodyHasInvoiceLink_AcceptsStrongSignals 转红
//     （.pdf 后缀这种最常见的形态被漏掉）
//   - 改用 DeriveSnippet 的返回值而不是原始字节 -> 转红
//     （htmlToText 会把 href 里的 URL 删掉）

// invoiceHTML 造一封只有正文链接、没有 MIME 附件的 HTML 邮件原文。
func invoiceHTML(body string) []byte {
	return []byte("Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + body)
}

func TestBodyHasInvoiceLink_AcceptsStrongSignals(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"pdf 后缀", "https://inv.example.com/download/abc123.pdf"},
		{"invoice 词 + 数字 id", "https://billing.example.com/invoice/20260928/8891"},
		{"fapiao 平台", "https://fapiao.example.cn/detail?id=7788"},
		{"download + pdf", "https://portal.example.com/download/invoice-2026-09.pdf"},
		{"etax 电子税务局", "https://etax.example.gov.cn/print/556677"},
	}
	for _, c := range cases {
		raw := invoiceHTML(`<p>发票已开具</p><a href="` + c.url + `">点击下载</a>`)
		if !bodyHasInvoiceLink(raw) {
			t.Errorf("%s: %q 应判为有发票链接（score=%d），实际 false",
				c.name, c.url, invoiceLinkScoreForTest(c.url))
		}
	}
}

func TestBodyHasInvoiceLink_IgnoresWeakSignals(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"纯营销链接", "https://promo.example.com/campaign/spring-sale"},
		{"社交媒体", "https://www.facebook.com/sharer/sharer.php?u=x"},
		{"退订链接", "https://list.example.com/unsubscribe?id=42"},
		{"站内路径无特征", "https://example.com/detail?id=42"},
		// 单个弱 hint：inv 出现在 inviter 里，不是发票。
		{"inviter 假阳性", "https://example.com/inviter/join"},
	}
	for _, c := range cases {
		raw := invoiceHTML(`<p>感谢参与</p><a href="` + c.url + `">了解更多</a>`)
		if bodyHasInvoiceLink(raw) {
			t.Errorf("%s: %q 不应判为发票链接（score=%d）—— 置位会让营销邮件也亮 📎",
				c.name, c.url, invoiceLinkScoreForTest(c.url))
		}
	}
}

// 纯文本正文里的裸 URL 同样要认：不少发票邮件是 text/plain。
func TestBodyHasInvoiceLink_AcceptsBareURLInPlainText(t *testing.T) {
	raw := []byte("Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"您的发票请下载：https://billing.example.com/invoice/8891.pdf 谢谢")
	if !bodyHasInvoiceLink(raw) {
		t.Error("纯文本正文里的发票 URL 应被判为有链接")
	}
}

// 关键回归：判定必须作用在**原始 MIME** 上。
// DeriveSnippet 对 HTML 会走 htmlToText，href 里的 URL 整个消失 —— 2026-10-02
// 实测：`<a href="https://inv.example.com/download/abc123.pdf">下载</a>` 经
// DeriveSnippet 后是 "下载"，URL 一个不剩。所以本条同时钉住「原始字节判 true」
// 与「snippet 判 false」这个反差，防止有人把入参悄悄换成 snippet。
func TestBodyHasInvoiceLink_UsesRawMIMENotSnippet(t *testing.T) {
	raw := invoiceHTML(`<p>发票已开具</p><a href="https://inv.example.com/download/abc123.pdf">点击下载发票</a>`)
	if !bodyHasInvoiceLink(raw) {
		t.Fatal("原始 MIME 上应判为有链接")
	}
	snippet := DeriveSnippet(raw, 500)
	if bodyHasInvoiceLink([]byte(snippet)) {
		t.Fatalf("snippet 上不该判为有链接（htmlToText 已删掉 href）—— snippet=%q", snippet)
	}
	if snippet == "" {
		t.Fatal("snippet 不该为空：本用例要证明的是「有 snippet 但里面没有 URL」")
	}
}

func TestBodyHasInvoiceLink_EmptyAndNil(t *testing.T) {
	if bodyHasInvoiceLink(nil) {
		t.Error("nil 不该判为有链接")
	}
	if bodyHasInvoiceLink([]byte{}) {
		t.Error("空字节不该判为有链接")
	}
	if bodyHasInvoiceLink(invoiceHTML("<p>只有文字，没有任何链接。</p>")) {
		t.Error("无链接的正文不该判为有链接")
	}
}

// 承重用例：区分「命中完整词」与「命中子串」。
//
// 这正是本文件存在的理由（2026-10-02 实测）：
//
//	fapiao.example.cn  score=10（专有平台）
//	example.com/inviter score=10（含 "inv" 子串，与上面同分）
//
// 分数这个维度区分不了它们，所以判据改用「词边界」而不是「调阈值」。
// 下面 6 条把这条边界钉住：前三必须为真，后三必须为假。
func TestHasStrongInvoiceHint_DistinguishesWordFromSubstring(t *testing.T) {
	positive := []string{
		"https://fapiao.example.cn/detail?id=7788",
		"https://etax.example.gov.cn/print/556677",
		"https://billing.example.com/invoice/20260928/8891",
		"https://portal.example.com/download/invoice_id=8",
	}
	for _, u := range positive {
		if !hasStrongInvoiceHint(u) {
			t.Errorf("%q 是真实发票平台形态，应命中强特征词", u)
		}
	}
	negative := []string{
		"https://example.com/inviter/join",   // "inv" 是 "inviter" 的前缀
		"https://example.com/inventory/list", // 同理
		"https://example.com/invite/abc",     // 同理
	}
	for _, u := range negative {
		if hasStrongInvoiceHint(u) {
			t.Errorf("%q 只是含 inv 子串，不该命中强特征词", u)
		}
	}
}

// 阈值口径本身要被钉住：scoreInvoiceURL 的行为一变（比如有人给 hint 加分），
// 阈值就该跟着复核，而不是悄悄改变「什么算发票链接」。
func TestInvoiceLinkScoreThreshold_MatchesScoring(t *testing.T) {
	cases := []struct {
		url       string
		atOrAbove bool
	}{
		{"https://inv.example.com/a.pdf", true},
		{"https://example.com/invoice/1", true},
		{"https://example.com/detail", false},
	}
	for _, c := range cases {
		score := invoiceLinkScoreForTest(c.url)
		got := score >= invoiceLinkScoreThreshold
		if got != c.atOrAbove {
			t.Errorf("%q score=%d 阈值=%d 判定=%v，want %v", c.url, score, invoiceLinkScoreThreshold, got, c.atOrAbove)
		}
	}
}
