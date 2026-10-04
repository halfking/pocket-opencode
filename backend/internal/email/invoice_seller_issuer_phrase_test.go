package email

// invoice_seller_issuer_phrase_test.go — 钉住「XX 为您开具了电子发票」这个语序。
//
// ## 缺陷（2026-10-04 08:00 定时流水线真实产出发现）
//
// 新建档的一张票：`其他-系统服务-6071.00-2026-09-15-26332000007943899111.jpg`
// —— 需求原文 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 里的「对方单位」是
// **「系统服务」**，交给财务时等于没有供应商。
//
// 来源邮件正文（真实 snippet 原文，未改写）：
//
//	尊敬的 杭州开轩科技有限公司 用户，您好： 浙江智谱新篇科技有限公司为您开具了电子发票
//	点击链接查看，如点击无效可复制到浏览器中查看 …
//	发票金额 6071.00 开票日期 2026-09-15 购方名称 杭州开轩科技有限公司
//
// 正文里明明写着开票方「浙江智谱新篇科技有限公司」，但：
//   · 不含「销售方」「开票方」⇒ reSeller 不匹配；
//   · 不含「来自」「由」      ⇒ reSellerFromSubject 不匹配
//     （而且它**只作用于 subject**，这一形态在**正文**里）；
//   · ⇒ 落到 FromName 兜底，而那封邮件的 from_name 字面就是
//     「系统服务」（百望平台的发件人显示名），from_address=yun1@vip.baiwang.com。
//
// 这与 `Invoice.Seller` 字段注释里记的 2026-10-03 那一例**同族**
// （正文没写「销售方」时 seller 退化成路由痕迹），但那次的补救是
// 「让发票 XML 能覆盖」——而这张票来自 URL 下载的 **jpg**，**没有 XML 可覆盖**。
//
// ## 期望值是独立字面量
//
// 「浙江智谱新篇科技有限公司」直接写在用例里，**不**调任何被测函数生成。
import "testing"

func TestExtractInvoiceLoose_SellerFromIssuerPhrase(t *testing.T) {
	// 真实 snippet 原文（百望 pis.baiwang.com 的「电子发票下载」邮件）。
	const snippet = "尊敬的 杭州开轩科技有限公司 用户，您好： 浙江智谱新篇科技有限公司为您开具了电子发票 " +
		"点击链接查看，如点击无效可复制到浏览器中查看 " +
		"https://pis.baiwang.com/smkp-vue/previewInvoiceAllEle?param=5EA9 " +
		"发票金额 6071.00 开票日期 2026-09-15 购方名称 杭州开轩科技有限公司"

	e := Email{
		Subject:     "电子发票下载",
		Snippet:     snippet,
		FromName:    "系统服务", // 百望平台的发件人显示名 —— 兜底会把 seller 变成它
		FromAddress: "yun1@vip.baiwang.com",
	}

	// 照 diag_debt_notice_candidates_test.go:153 的既有惯例：bodyText 传 subject+snippet。
	inv, ok := ExtractInvoiceLoose(e, e.Subject+"\n"+e.Snippet, false)
	if !ok || inv == nil {
		t.Fatalf("ExtractInvoiceLoose 返回 ok=%v inv=%v；这张票明明有开票方", ok, inv)
	}

	const want = "浙江智谱新篇科技有限公司"
	if inv.Seller != want {
		t.Fatalf("seller=%q，want %q\n"+
			"  正文里有权威开票方，却退化成发件人显示名「%s」⇒ 规范文件名的"+
			"「对方单位」一段是废的，财务看不出是谁开的票。",
			inv.Seller, want, e.FromName)
	}
	if inv.sellerIsFallback {
		t.Errorf("SellerIsFallback()=true，但这张票的开票方是从正文抽到的，" +
			"标记成「路由痕迹」会让下游以为这个值不可信而忽略它")
	}
}

// TestSellerFromIssuerPhrase_PrefersIssuerOverFromNameFallback 把兜底链的顺序也钉住：
// 即使 reSeller 命中了别的列头，只要正文里有「XX 为您开具了」，
// 那个值也必须赢过 FromName 兜底。
func TestSellerFromIssuerPhrase_PrefersIssuerOverFromNameFallback(t *testing.T) {
	const snippet = "浙江智谱新篇科技有限公司为您开具了电子发票 发票金额 6071.00"
	e := Email{Subject: "电子发票下载", Snippet: snippet, FromName: "系统服务", FromAddress: "yun1@vip.baiwang.com"}
	inv, ok := ExtractInvoiceLoose(e, e.Subject+"\n"+e.Snippet, false)
	if !ok || inv == nil {
		t.Fatalf("ExtractInvoiceLoose: ok=%v inv=%v", ok, inv)
	}
	if inv.Seller == "系统服务" {
		t.Fatalf("seller 仍是发件人显示名「系统服务」，而正文里有开票方 —— 兜底链顺序错了")
	}
}
