package email

import (
	"strings"
	"testing"
)

// important_reminder_lookback_test.go —— 需求 4「重要邮件提醒」的回看窗口，
// 从 notifyImportant 里硬编码的 2 天放宽到 importantReminderLookbackDays。
//
// ## 为什么要锁
//
// 2 天窗口 + 每天跑一次定时任务 = 任何一次漏掉的邮件**永久**失去被提醒的机会
// （服务端重启、通知中心当时不可用、AI 分类器连续失败、机器关机）。这不是
// 「这轮没轮到」，是「不在扫描范围里」，而且报告上只有一个无法解释的 0。
//
// 这类「把一个数字调回去」的退化**不产生任何错误信号**，所以必须有护栏。
//
// ## 本文件只锁「窗口」这一个维度
//
// 行为断言（窗口外计数、窗口内照常提醒、已提醒不重推）在
// reminder_window_test.go / reminder_dispatch_test.go 里，本文件不重复。
//
// ## 两条护栏各锁一层，缺一不可
//
//	常量层：锁 importantReminderLookbackDays / importantReminderScanLimit 的取值
//	接线层：锁 notifyImportant 的调用点真的用上了这两个常量
//
// 只锁常量是不够的，这一点**已被实测过**：invoice_candidate_lookback_test.go
// 记录过「把 ListEmailsSince 的实参改回 rep.StartedAt-86400，常量仍是 90，
// 常量层护栏照样全绿，而缺陷已经装回去」。同一条教训，同一个包里，
// 只是换了个扫描步骤。

// 护栏（常量层）：窗口必须显著大于 2 天，且扫描上限与窗口配套。
//
// 这条防的是「有人为了省事把窗口调回两天」——那正好把缺陷装回去。
func TestImportantReminderWindowIsWiderThanTwoDays(t *testing.T) {
	if importantReminderLookbackDays <= 2 {
		t.Errorf("重要提醒回看窗口 %d 天 ≤ 2 天，等于把「重要但很老的邮件永远不被提醒」"+
			"这个原缺陷原样装回去", importantReminderLookbackDays)
	}
	// 上限必须与窗口配套。Store.ListEmailsSince 是 `ORDER BY date DESC LIMIT n`，
	// 500 行几乎必然被最近的邮件占满——被挤掉的恰好是窗口末端那批老邮件，
	// 也就是这个窗口本来要救的那批。窗口放宽而上限不动 = 白改。
	if importantReminderScanLimit <= 500 {
		t.Errorf("扫描上限 %d：ORDER BY date DESC 下 500 行几乎必然被最近邮件占满，"+
			"宽窗口形同虚设", importantReminderScanLimit)
	}
	if importantReminderScanLimit > 2000 {
		t.Errorf("扫描上限 %d 超过 Store.ListEmailsSince 的硬上限 2000，"+
			"会被静默重置成 500", importantReminderScanLimit)
	}
}

// 护栏（接线层）：notifyImportant 必须真的用上面两个常量。
//
// 用源码级匹配而不是「再跑一遍集成测试」：集成测试需要 PG，而这条护栏
// 必须在没有数据库的环境里也成立。判据匹配的是**调用形态**而不是
// 「文件里出现过这个词」。
func TestNotifyImportantUsesLookbackConstantAtCallSite(t *testing.T) {
	src := readPipelineSource(t)
	body := extractFuncBody(t, src, "func (p *Pipeline) notifyImportant(")

	if !strings.Contains(body, "AddDate(0, 0, -importantReminderLookbackDays)") {
		t.Errorf("调用点没有用 importantReminderLookbackDays —— 窗口又被写死成别的值了。\n"+
			"实际 since 计算：%s", firstLineContaining(body, "since :="))
	}
	if !strings.Contains(body, "importantReminderScanLimit") {
		t.Errorf("调用点没有用 importantReminderScanLimit —— 扫描上限与窗口不配套。\n"+
			"实际调用：%s", firstLineContaining(body, "ListEmailsSince"))
	}
	if strings.Contains(body, "AddDate(0, 0, -2)") {
		t.Errorf("调用点里仍残留硬编码的 2 天窗口：%s",
			firstLineContaining(body, "AddDate(0, 0, -2)"))
	}
}
