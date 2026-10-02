package email

// 「发票日期取到未来」这个缺陷的护栏。
//
// 2026-10-02 真实库实测（Redmi 真机审计同一条数据，diag_real_invoice_extract_test.go
// 已记录金额侧，本文件管日期侧）：
//
//	inv_1790903383222583800_1  amount=58000.00  invoice_date=2026-10-25
//	kind=bill  主题=中国工商银行客户对账单(ICBC Peony Card Bank Statement)
//
// 2026-10-25 是原文里的「贷记卡到期还款日」，比当天晚 23 天。发票不可能在未来
// 开具，「到期还款日」是债务通知的字段，不是凭证的日期。
//
// 同一封邮件里其实带着正确的「对账单生成日 2026年09月30日」，但 reLooseCNDate
// 只取**第一个**中文年月日，于是先撞上了还款日。
//
// 这条与「对账单要不要算进台账」那个产品决定**无关**：无论算不算，
// 未来日期都不是开票日期。
//
// 负控见文件末尾，实测转红。

import (
	"os"
	"strings"
	"testing"
	"time"
)

// icbcStatementText 是那封对账单里**真实出现过**的片段（标识符已合成化，
// 结构与字段名逐字取自生产库 snippet）。字段顺序也照原样：还款日在前、
// 生成日在后——这正是「取第一个」会撞错的形态。
const icbcStatementText = "信 用 卡 对 账 单 尊敬的客户,您好! 感谢您使用工商银行信用卡，我行24小时服务专线95588竭诚为您服务。 " +
	"重要提示： 贷记卡到期还款日 2026年10月25日 尊敬的客户，为确保您还款准确，请您仔细阅读下面需还款明细栏目中各账户本期应还款金额及本期最低还款额等信息。 " +
	"账单周期 2026年09月01日—2026年09月30日 对账单生成日 2026年09月30日 " +
	"需 还 款 明 细（特别提示:请按照以下账户分别还款） 卡号后四位币种 应还款额 最低还款额信用额度 9097(牡丹贷记卡)人民币(本位币)12,838.93/RMB1,605.56/RMB58,000.00/RMB " +
	"合计人民币(本位币)12,838.93/RMB1,605.56/RMB/ 本 期 交 易 汇 总"

func at(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.Local)
}

func TestParseInvoiceDate_SkipsFutureDueDate(t *testing.T) {
	// 评估日固定为 2026-10-02（实测当天），不依赖真实时钟。
	got := parseInvoiceDateAt(icbcStatementText, at(2026, 10, 2))
	if got != "2026-09-30" {
		t.Fatalf("日期应取到对账单生成日 2026-09-30，实际 %q", got)
	}
}

func TestParseInvoiceDate_NeverReturnsFutureDate(t *testing.T) {
	// 只有未来日期可选时，宁可返回空串也不返回未来。
	// 返回一个未来的「开票日期」比不返回更糟：它会进文件名、进台账排序。
	onlyFuture := "本期账单 2026年12月31日 出具"
	if got := parseInvoiceDateAt(onlyFuture, at(2026, 10, 2)); got != "" {
		t.Fatalf("只有未来日期时必须返回空串，实际 %q", got)
	}
}

func TestParseInvoiceDate_KeepsRealInvoiceDates(t *testing.T) {
	cases := []struct {
		name string
		text string
		now  time.Time
		want string
	}{
		{"带标签的开票日期（过去）", "开票日期：2026-09-24 销售方：杭州创客家", at(2026, 10, 2), "2026-09-24"},
		{"带标签的开票日期（当天）", "开票日期：2026-10-02", at(2026, 10, 2), "2026-10-02"},
		{"带标签的开票日期（明天，宽限内）", "开票日期：2026-10-03", at(2026, 10, 2), "2026-10-03"},
		{"英文 Date 标签", "Date: 2026-09-30", at(2026, 10, 2), "2026-09-30"},
		{"八个数字", "开票日期 20260924", at(2026, 10, 2), "2026-09-24"},
		{"第一个日期在未来时取第二个", "到期还款日 2026年10月25日 开票日期：2026-09-30", at(2026, 10, 2), "2026-09-30"},
		// 承重：同层内第一个匹配在未来时必须**继续往后找**，而不是整层放弃。
		// 只取第一个的实现对这条会返回空串（第一层撞到未来就 break，
		// 兜底层又撞到同一个未来日期），负控 neg3 就是靠这条钉住的。
		{"同层第一个在未来：继续找同层第二个", "开票日期：2026-12-31 开票日期：2026-09-30", at(2026, 10, 2), "2026-09-30"},
		// 账单层同理：账单出具日这一层自己出现两个，第一个是预排的未来周期。
		{"账单层第一个在未来：继续找第二个", "对账单生成日 2026年11月30日 账单日期 2026-09-30", at(2026, 10, 2), "2026-09-30"},
		{"没有任何日期", "金额：3500.00 元", at(2026, 10, 2), ""},
	}
	for _, c := range cases {
		if got := parseInvoiceDateAt(c.text, c.now); got != c.want {
			t.Errorf("%s：期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

func TestIsFutureInvoiceDate(t *testing.T) {
	now := at(2026, 10, 2)
	cases := []struct {
		d    string
		want bool
	}{
		{"2026-10-02", false}, // 当天
		{"2026-10-03", false}, // 明天（1 天宽限）
		{"2026-10-04", true},  // 宽限之外
		{"2026-12-31", true},
		{"2020-01-01", false}, // 过去很久
		{"", false},           // 空串不算未来，交给上层按原样处理
		{"not-a-date", false},
	}
	for _, c := range cases {
		if got := isFutureInvoiceDate(c.d, now); got != c.want {
			t.Errorf("isFutureInvoiceDate(%q) = %v，期望 %v", c.d, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 接线：对外的 ParseInvoiceDate 仍然走这条逻辑（不能只测私有函数）
// ---------------------------------------------------------------------------

func TestParseInvoiceDate_PublicEntryGoesThroughTheGuard(t *testing.T) {
	// 反面对照：只测 parseInvoiceDateAt 而不管 ParseInvoiceDate，判据就可能
	// 只是一条没人调用的旁路——本轮前面已经吃过一次「判据存在、循环没接」。
	src, err := os.ReadFile("invoice.go")
	if err != nil {
		t.Fatalf("read invoice.go: %v", err)
	}
	if !strings.Contains(string(src), "return parseInvoiceDateAt(text, time.Now())") {
		t.Fatal("ParseInvoiceDate 没有委托给 parseInvoiceDateAt —— 未来日期仍会漏出去")
	}
}

// ---------------------------------------------------------------------------
// 负控
// ---------------------------------------------------------------------------
// 1) 把 `!isFutureInvoiceDate(d, now)` 去掉 → 前两条用例转红
// 2) 把 reLooseCNDate 的循环改回「只取第一个」（FindStringSubmatch）→ 第一条转红
// 3) 把 ParseInvoiceDate 退回原样（不委托）→ 接线那条转红
