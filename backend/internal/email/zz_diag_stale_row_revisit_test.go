package email

import (
	"strings"
	"testing"
)

// 真实库那笔幽灵发票的「今天还会不会长回来」判定。
//
// 背景（时序已核实，不是推测）：
//   - 该行 inv_1790903383222583800_1 建档于 2026-10-02 09:09:43；
//   - 闸门 admitDebtNotice 的提交 f20ed001 是 2026-10-02 22:50:08，晚 13 小时。
// 所以它是**修复前**的存量行，而不是闸门漏放。
//
// 本测试要回答的是它长不长得回来。已知的判据缺口：
// diag_stale_debt_notice_row_test.go 只跑 envelope 那一腿
// （ExtractInvoice(e, "")），拿不到附件维度；而生产第 2 趟传的是
// HasInvoiceAttachment(parsed.Attachments)。本文件用真实 snippet 补上这一腿。
//
// 语料是**真实库原文**（em-1298896144-…-5 的 snippet 字段），不自造。
func TestDiagStaleDebtRowRevisit(t *testing.T) {
	const subject = "中国工商银行客户对账单(ICBC Peony Card Bank Statement)"
	// 真实 snippet 原文（关键片段）：应还款 12,838.93 / 信用额度 58,000.00
	const snippet = `信 用 卡 对 账 单 尊敬的黄旭涛先生,您好! 感谢您使用工商银行信用卡，我行24小时服务专线95588竭诚为您服务。 重要提示： 贷记卡到期还款日 2026年10月25日 尊敬的客户，为确保您还款准确，请您仔细阅读下面需还款明细栏目中各账户本期应还款金额及本期最低还款额等信息。 账单周期 2026年09月01日—2026年09月30日 对账单生成日 2026年09月30日 需 还 款 明 细（特别提示:请按照以下账户分别还款） 卡号后四位币种 应还款额 最低还款额信用额度 9097(牡丹贷记卡)人民币(本位币)12,838.93/RMB1,605.56/RMB58,000.00/RMB 合计人民币(本位币)12,838.93/RMB1,605.56/RMB/ 本 期 交 易 汇 总`

	e := Email{ID: "em-1298896144-acct-1790870162079171800-5", Subject: subject, Snippet: snippet}

	// 腿 1：envelope（幂等跳过之后实际会跑的那条）
	inv1, hit1 := ExtractInvoice(e, "")
	t.Logf("腿1 ExtractInvoice(envelope): hit=%v", hit1)
	if hit1 && inv1 != nil {
		t.Logf("  amount=%.2f date=%q  ← 58,000 是信用额度被当成支出", inv1.Amount, inv1.InvoiceDate)
	}

	// 腿 2：完整正文 + hasInvoiceAttachment=false（真实库 has_attachments=f）
	inv2, hit2 := ExtractInvoiceLoose(e, snippet, false)
	t.Logf("腿2 ExtractInvoiceLoose(正文, att=false): hit=%v", hit2)
	if hit2 && inv2 != nil {
		t.Logf("  amount=%.2f date=%q", inv2.Amount, inv2.InvoiceDate)
	}

	// 腿 3：只有带附件时才放行的那条（真实库无附件，此腿仅作对照）
	inv3, hit3 := ExtractInvoiceLoose(e, snippet, true)
	t.Logf("腿3 ExtractInvoiceLoose(正文, att=true) [对照]: hit=%v", hit3)
	if hit3 && inv3 != nil {
		t.Logf("  amount=%.2f date=%q", inv3.Amount, inv3.InvoiceDate)
	}

	// 断言：真实形态（无附件）下两道腿都必须拦。
	// 拦不住就意味着幂等跳过一旦失效（90 天回看重建），5.8 万会重新进台账。
	if hit1 {
		t.Errorf("腿1 仍会建档：amount=%.2f —— 幂等跳过一旦失效会重建幽灵行", inv1.Amount)
	}
	if hit2 {
		t.Errorf("腿2 仍会建档：amount=%.2f —— 生产第 2 趟会重建幽灵行", inv2.Amount)
	}

	// 前提自检：闸门必须真的被考验到。
	if !invoiceKeywordHit(subject + "\n" + snippet) {
		t.Fatal("前提不成立：invoiceKeywordHit 连它都不放行，闸门形同虚设")
	}
	if !reDebtNoticeShape.MatchString(subject + "\n" + snippet) {
		t.Fatal("前提不成立：reDebtNoticeShape 没把它当债务通知，闸门走的是直通分支")
	}
	if strings.Contains(snippet, "58,000.00") {
		t.Logf("前提：语料含信用额度 58,000.00，正是被误当支出的那个数")
	} else {
		t.Fatal("前提不成立：语料里没有 58,000.00")
	}
}
