package email

import "testing"

// 临时诊断：验证 2026-10-02 收紧后的 admitDebtNotice 是否真能拦住
// 真实库里那笔幽灵发票（inv_1790903383222583800_1 / amount=58000）。
// 语料全部取自真实库的主题原文，不自造。
func TestDiagAdmitProbe(t *testing.T) {
	const icbc = "中国工商银行客户对账单(ICBC Peony Card Bank Statement)"
	const ckjj = "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元，请注意查收！"
	const qq = "[QQ Wallet] Electronic Invoice Issuance Notice"
	const toll = "通行费电子发票"

	cases := []struct {
		name   string
		joined string
		att    bool
		want   bool
	}{
		{"工行对账单 真实主题/无附件", icbc, false, false},
		{"工行对账单 真实主题/带附件", icbc, true, true},
		{"真发票 创客家 真实主题", ckjj, false, true},
		{"QQ钱包 真实主题", qq, false, true},
		{"通行费 真实主题", toll, false, true},
	}
	for _, c := range cases {
		got := admitDebtNotice(c.joined, c.att)
		if got != c.want {
			t.Errorf("%s: admitDebtNotice=%v want %v", c.name, got, c.want)
		} else {
			t.Logf("OK  %s -> admit=%v", c.name, got)
		}
	}

	// 前提自检：闸门必须真的被考验到。若 invoiceKeywordHit 连对账单都不放行，
	// admitDebtNotice 就永远走「不是债务通知」的直通分支，上面那条期望就不作数。
	if !invoiceKeywordHit(icbc) {
		t.Error("前提不成立：invoiceKeywordHit 竟没放行工行对账单")
	} else {
		t.Log("OK  前提：invoiceKeywordHit 放行工行对账单")
	}
}
