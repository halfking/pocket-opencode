package email

// 真实形态回归：国内邮箱里那张电子发票邮件（2026-09-30 实测暴露两个缺陷）。
//
// ⚠️ 夹具已全部合成：发票号码、开票单位、发票链接域名/发件地址都换成了
// 明显虚构的值（原值是真实发票号、真实开票公司名、真实租户的 OSS 链接与发件
// 域名，测试文件不该也不需要承载真实凭据/PII）。合成值保持了原邮件的**结构**：
// 20 位数字发票号 → 带连字符的 `INV-TEST-*`（reInvoiceNo 同样匹配）、
// 中文单位名、`.invalid` 保留域（RFC 6761 保证永不可解析）。
//
// 合成后仍要复现的形态：主题形态（单位 + 发票号码 + 「金额：…元」）与
// IMAP 路径（只拿得到主题 + BODY[TEXT] 头部摘要，正文没拉下来）。
//
// 当初实测出的两个缺陷：
//  1. `金额：3500.00元` 抽不出金额（reAmountTotal 的关键词表里没有「金额」），
//     结果 amount=0 —— 汇总金额直接少算，需求里的「汇总金额」就废了；
//  2. 销售方退化成发件地址的域名片段（实测是发票平台域名 dzfp 那一段），
//     而不是主题里的开票单位。
//
// 这两个都不是「规则没覆盖到」的小事：金额为 0 会让共享台账的合计行失真。

import "testing"

// 合成夹具（形状照抄真实邮件，值全部虚构）。
const (
	syntheticSeller     = "示例虚构科技有限公司"
	syntheticInvoiceNo  = "INV-TEST-0001"
	realInvoiceSubject  = "您收到来自" + syntheticSeller + "的发票，发票号码：" + syntheticInvoiceNo + "，金额：3500.00元，请注意查收！"
	realInvoiceFromAddr = "noreply@mail.example.invalid"
)

// 电子发票平台邮件的典型正文片段（用于复现「销售方退化成发件域名片段」）。
// 发票链接是 .invalid 保留域上的合成 URL，不是任何真实对象存储地址。
const realInvoiceSnippet = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=GBK

尊敬的用户：
您已成功开具电子发票，发票信息如下：
发票号码：INV-TEST-0001
开票日期：2026-05-24
销售方名称：示例虚构科技有限公司
价税合计：￥3500.00
发票链接：https://oss-example.invalid/invoice/INV-TEST-0001.pdf`

// 真实 IMAP 路径下 envelope 只带主题 + BODY[TEXT] 的**头部**摘要（正文没被拉下来），
// 线上实测就是这一种：amount=0、seller 退化成发件域名片段。
const realInvoiceLiveSnippet = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=GBK
Content-Transfer-Encoding: base64

LS0tLS0tLV9QYXJ0XzM5NzExMA==`

func TestRealInvoice_Amount3500(t *testing.T) {
	e := Email{
		ID:          "em-real",
		Subject:     realInvoiceSubject,
		Snippet:     realInvoiceLiveSnippet,
		FromAddress: realInvoiceFromAddr,
	}
	inv, hit := ExtractInvoice(e, "")
	if !hit {
		t.Fatal("invoice-shaped mail must be recognized")
	}
	if inv.InvoiceNo != syntheticInvoiceNo {
		t.Fatalf("invoice no = %q, want %q", inv.InvoiceNo, syntheticInvoiceNo)
	}
	if inv.Amount != 3500.00 {
		t.Fatalf("amount = %v, want 3500.00（主题里写的是「金额：3500.00元」）", inv.Amount)
	}
	if inv.Seller != syntheticSeller {
		t.Fatalf("seller = %q, want %s（正文没拉下来时应退回主题里的开票单位，而不是发件地址）", inv.Seller, syntheticSeller)
	}
}

// 主题里没有金额、只有正文有「价税合计」时也要抽到（两种来源都要覆盖）。
func TestRealInvoice_AmountFromBodyOnly(t *testing.T) {
	e := Email{ID: "em-real-2", Subject: "您的电子发票已开具", Snippet: realInvoiceSnippet}
	inv, hit := ExtractInvoice(e, "")
	if !hit || inv.Amount != 3500.00 {
		t.Fatalf("body amount not extracted: hit=%v inv=%+v", hit, inv)
	}
}

// 「金额」不能引入误伤：不含数值的「金额」提示语不该造出金额。
func TestAmountKeyword_NoFalsePositiveOnProse(t *testing.T) {
	e := Email{ID: "em-prose", Subject: "您本月的金额已超出套餐额度，请及时充值", Snippet: "点击查看详情"}
	if inv, hit := ExtractInvoice(e, ""); hit && inv.Amount > 0 {
		t.Fatalf("prose must not produce an amount: %+v", inv)
	}
}
