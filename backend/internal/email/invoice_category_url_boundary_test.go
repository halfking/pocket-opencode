package email

// invoice_category_url_boundary_test.go — 费用类型分类的英文关键词撞 URL 子串。
//
// ## 缺陷（2026-10-04 08:00 定时流水线真实产出，交付物里肉眼可见）
//
// 产物汇总 `invoices-summary-20261004-080022.md` 第 11 行：
//
//	| 通信 | X | 8.00 USD |  |  | downloaded | 已核验 |
//
// 一个 **Stripe 软件订阅收据**被标成「**通信**」（话费/宽带类），
// 于是需求原文 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 产出的文件名是
// `通信-X-8.00-2026-10-04.pdf` —— 费用类型这一段是错的。
//
// ## 根因：与 `e54d797d` 同源，但当时只堵了一半
//
// `classifyInvoiceCategory`（invoice.go）对英文关键词也用 `strings.Contains`。
// 该邮件 snippet 原文里有：
//
//	(invoice illustration [https://stripe-images.s3.amazonaws.com/emails/…])
//
// `s3.amazonaws.com` 里的 **`aws`** 子串直接命中 → 判成「通信」。
//
// 这与第四十一节修的 `vat` 撞 `activation`、`billing` 撞 URL 路径段是
// **同一个缺陷模式：裸关键词撞子串**。那次我只给发票**候选**判定
// （`invoiceKeywordHit`）加了词边界，**漏了费用类型这条通路**。
// ⇒ 教训：同一个模式在不同入口要用同一套判据堵，
// 「修好了一个入口」不等于「这个缺陷没了」。
//
// ## 中文关键词为什么不动
//
// 汉字没有「词内含子词」这回事，「腾讯」两字连续出现就是腾讯。
// 与 41.3 对 `invoiceKeywordHit` 的处理一致。
import "testing"

// TestClassifyInvoiceCategory_RealStripeReceiptNotTelecom 这条是**缺陷复现**：
// 期望值「其他」是独立字面量，不调任何被测函数生成。
func TestClassifyInvoiceCategory_RealStripeReceiptNotTelecom(t *testing.T) {
	// 真实 snippet 原文（Stripe receipt）。
	const snippet = "X (https://about.x.com) X Receipt from X $8.00 Paid September 17, 2026 " +
		"(invoice illustration [https://stripe-images.s3.amazonaws.com/emails/" +
		"invoices_invoice_illustration.png]) Download invoice " +
		"(https://pay.stripe.com/invoice/acct_1Ika5JA3KZ32dPo1/live_YWNjdF8xSWthNUpBM0taMzJk/pdf?s=em) " +
		"Download receipt (https://dashboard.stripe.com/receipts/invoices/CAcQARoXChVhY2N0)"

	const want = "其他"
	if got := classifyInvoiceCategory(snippet); got != want {
		t.Fatalf("classifyInvoiceCategory=%q，want %q\n"+
			"  正文里的 `s3.amazonaws.com` 不是「AWS 云服务」，只是 CDN 域名；\n"+
			"  命中它 ⇒ 费用类型写成「通信」⇒ 文件名变成 通信-X-8.00-….pdf，财务按通信费入账。",
			got, want)
	}
}

// TestClassifyInvoiceCategory_ASCIIKeywordsInURLsAreNotSignals 把这一类
// 撞 URL 的形态一次钉全，避免只修 `aws` 一个、下次换个域名再来一遍。
func TestClassifyInvoiceCategory_ASCIIKeywordsInURLsAreNotSignals(t *testing.T) {
	for _, text := range []string{
		"Receipt https://stripe-images.s3.amazonaws.com/emails/a.png 已支付 8.00",
		"See https://cdn.example.com/aws/legal/terms.html",
		"See https://cdn.example.com/xaWSy/z.png",
		"Visit https://myhotels-search.example.com/deals",
		"镜像 https://mirror.example.com/restaurants/index.html 已续费",
		"https://example.com/?next=https://a.example/azure.png&t=1",
	} {
		if got := classifyInvoiceCategory(text); got == "通信" {
			t.Errorf("classifyInvoiceCategory(%q)=「通信」，应为「其他」\n"+
				"  URL 里的子串不是消费类目信号。", text)
		}
	}
}

// TestClassifyInvoiceCategory_RealSignalsStillWork 是**反向保护**：
// 收紧不能把真正的类目信号一起挡掉。
func TestClassifyInvoiceCategory_RealSignalsStillWork(t *testing.T) {
	cases := []struct {
		text, want string
	}{
		{"AWS 云服务账单 2026 年 9 月", "通信"},
		{"azure 订阅月费", "通信"},
		{"某 SaaS 工具年费", "通信"},
		{"阿里云服务器 8 月账单", "通信"},
		{"腾讯云 9 月账单", "通信"},
		{"美团外卖 32.00 元", "餐饮"},
		{"滴滴出行 15.00 元", "交通"},
		{"杭州某某酒店住宿费", "住宿"},
		{"京东办公用品采购", "办公"},
		{"某不认识的供应商 开票 100.00", "其他"},
	}
	for _, c := range cases {
		if got := classifyInvoiceCategory(c.text); got != c.want {
			t.Errorf("classifyInvoiceCategory(%q)=%q，want %q", c.text, got, c.want)
		}
	}
}

// TestClassifyInvoiceCategory_KnownEnglishVendorGap **钉住一个已知缺口**，
// 而不是假装它不存在。
//
// 现象：`Amazon Web Services`、`Tencent Cloud Computing Co Ltd` 判「其他」，
// 而词表里明明有「腾讯」「阿里云」。中文写法在、英文写法不在。
//
// 这两条断言写的是**当前真实行为**（即缺口本身），期望值是独立字面量
// 「其他」。它们的作用是：将来谁要补这个缺口，测试会转红提醒他
// 「这是有意改的，请同时补一条说明为什么这样改不会误伤营销邮件」。
//
// 为什么补不了（2026-10-04 真实库 978 封全量实测）：把 `amazon` 加进词元表，
// 一封会议促销邮件（正文含「亚马逊云科技 amazon quick 能力解读」）
// 就从「其他」被拉成「通信」。费用类型判定的输入是**邮件全文**，
// 品牌词一旦独立成词元，任何提到该品牌的营销内容都会命中。
// 要补需要的是发票专属线索（账单号/开票主体），不是品牌名。
func TestClassifyInvoiceCategory_KnownEnglishVendorGap(t *testing.T) {
	for _, text := range []string{
		"Amazon Web Services 费用单",
		"Tencent Cloud Computing Co Ltd 发票",
	} {
		if got := classifyInvoiceCategory(text); got != "其他" {
			t.Errorf("classifyInvoiceCategory(%q)=%q，want \"其他\"\n"+
				"  若你是在补英文厂商拼写的缺口：这确实是改进，但请先证明不会把\n"+
				"  「提到该品牌的营销邮件」一起带进来（实测 amazon 就踩了这个坑）。", text, got)
		}
	}
}
