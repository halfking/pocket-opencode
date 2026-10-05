package email

import (
	"strings"
	"testing"
)

// 自发自收闸门的候选判据评估（只读、无门禁、无外部依赖）。
//
// ## 为什么需要这个诊断
//
// round37 第十五节曾把「lower(from_address)=lower(email_address) 命中」
// 读成「系统自己注入的假数据」，据此推出剔除方案；第十六节查发票内容后
// 证明那是**腾讯云开具的真实发票**（QQ 钱包消费后自动开票、发到本人邮箱）。
// 判据本身没错，错的是对它的解释。
//
// 于是真正的问题变成：**能不能既拦掉系统自发邮件，又不误伤这类真实发票？**
//
// ## 本诊断的边界（先写清楚，免得读数被当成比实际更强的证据）
//
// 它只覆盖 17 封**真实库里的具体邮件**，不是对任意输入的性质证明。
// 真实库之外的自发邮件形态（换品牌名、换主题模板）不在覆盖范围内，
// 所以「拦得干净」这个结论**不能外推**成「对所有自发邮件都成立」。
//
// 语料全部取自生产库原文（emails.subject / snippet），不自造。
func TestDiagSelfSendGateCandidates(t *testing.T) {
	// 真库原文：自发邮件（应拦）
	const ownTest = "【开轩启圭】SMTP 配置测试邮件"
	const ownCode = "【开轩启圭】验证验证码：359663"
	const e2eAlert = "[urgent-e2e] 数据库延迟告警"
	const e2eProd = "[urgent-e2e] 生产环境告警"

	// 真库原文：真实发票（应保）
	const tencent = "[QQ Wallet] Electronic Invoice Issuance Notice"
	const ckjj = "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元，请注意查收！"
	const toll = "通行费电子发票"

	// 候选闸门：自发邮件的**两个**已知形态。
	//   ownBrand —— 主题带自有品牌前缀（【开轩启圭】）
	//   e2e      —— 主题带 e2e 测试标记
	// 注意这是两个形态，不是一个：只用 ownBrand 会漏掉 urgent-e2e 那两封。
	gate := func(subject string) bool {
		return strings.HasPrefix(subject, "【开轩启圭】") || strings.Contains(subject, "[urgent-e2e]")
	}

	block := []struct{ name, subj string }{
		{"自有品牌-测试邮件", ownTest},
		{"自有品牌-验证码", ownCode},
		{"e2e-数据库告警", e2eAlert},
		{"e2e-生产告警", e2eProd},
	}
	keep := []struct{ name, subj string }{
		{"腾讯云发票 QQ钱包", tencent},
		{"创客家发票", ckjj},
		{"通行费发票", toll},
	}

	t.Log("=== 应拦：自发邮件 ===")
	for _, c := range block {
		g := gate(c.subj)
		// 关键联动：即便闸门放行，现有关键词链是否已经拦得住？
		// 若已拦得住，闸门在该样本上是冗余的（不是错，只是没必要）。
		kw := invoiceKeywordHit(c.subj)
		inv, hit := ExtractInvoiceLoose(Email{ID: "x", Subject: c.subj}, "", false)
		amt := 0.0
		if hit && inv != nil {
			amt = inv.Amount
		}
		if !g {
			t.Errorf("闸门漏拦：%s（%s）", c.name, c.subj)
		}
		t.Logf("  %-16s gate=%v keywordHit=%v 建档=%v amount=%.2f", c.name, g, kw, hit, amt)
	}

	t.Log("=== 应保：真实发票 ===")
	for _, c := range keep {
		g := gate(c.subj)
		inv, hit := ExtractInvoiceLoose(Email{ID: "x", Subject: c.subj}, "", false)
		amt := 0.0
		if hit && inv != nil {
			amt = inv.Amount
		}
		if g {
			t.Errorf("闸门误伤真发票：%s（%s）", c.name, c.subj)
		}
		t.Logf("  %-16s gate=%v 建档=%v amount=%.2f", c.name, g, hit, amt)
	}

	// 前提自检：闸门必须真的拦得住全部自发形态。
	// 少一个形态就意味着「拦得干净」是假的。
	for _, c := range block {
		if !gate(c.subj) {
			t.Errorf("前提不成立：自发形态「%s」未被闸门覆盖", c.name)
		}
	}
}
