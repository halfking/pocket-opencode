package email

// reminder_diag_test.go — 重要邮件提醒的**可诊断性**：让 `remindersSent=0`
// 不再是一个含义不明的 0。
//
// 2026-10-01 真实数据诊断（只读 SQL，未改动任何业务数据）：
//   - 邮件表里 58 封 importance='high'，且**全部**带 ai_summary
//     → 它们是 AI 分类（kxmemory）的产物，不是规则标的。
//   - 通知中心有 53 条 email.important 记录，notified_at 集中在
//     01:40（42 封）与 04:04（9 封）→ **提醒链路确实跑通过**，
//     已提醒过的不重复提醒，符合设计。
//   - 但 04:44 之后入库的新邮件 importance **全是空**，包括
//     「企业微信邮箱授权码使用提醒」这种显然重要的。启动日志明写
//     `POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled`。
//
// 于是：提醒只对 importance='high' 触发，新邮件永远等不到 high，每轮都是 0。
// **这个 0 分不清「这批邮件确实没有重要的」和「邮件根本没被分类过」** ——
// 需求 4 看起来像没实现，其实只差一个依赖配置。
//
// 和 §spam 的 near-miss 是同一类问题：不是判定错，是**看不见**。
// 这里给 PipelineReport 补 RemindersScanned / RemindersUnclassified 两个
// 计数，并抽出纯函数 splitReminderCandidates 让这段判定可以脱离数据库验证。

import "testing"

// TestSplitReminderCandidates_SeparatesUnclassified 钉住三种状态必须被分开：
// 该提醒的、已提醒过的、以及「还不知道重不重要」的。
//
// 旧实现把这三种混在一个 `if` 里，报告上只剩一个 remindersSent，
// 于是「0」无法解释。
func TestSplitReminderCandidates_SeparatesUnclassified(t *testing.T) {
	emails := []Email{
		{ID: "e1", Importance: "high"},    // 该提醒
		{ID: "e2", Importance: "high"},    // 已提醒过 → 跳过
		{ID: "e3", Importance: ""},        // 未分类 → 单独计数
		{ID: "e4", Importance: "medium"},  // 已分类但不重要 → 静默跳过
		{ID: "e5", Importance: "", Category: "spam"}, // 垃圾，无论如何不提醒
		{ID: "e6", Importance: "high"},    // 该提醒
	}
	notified := []int64{0, 1, 0, 0, 0, 0}

	toNotify, unclassified := splitReminderCandidates(emails, notified)

	if len(toNotify) != 2 || toNotify[0].ID != "e1" || toNotify[1].ID != "e6" {
		t.Fatalf("该提醒的应恰好是 e1/e6，实际 %+v", emailIDs(toNotify))
	}
	if unclassified != 1 {
		t.Fatalf("unclassified = %d, want 1（只有 e3 是「没被分类过」）。"+
			"e4 是已分类为 medium、e5 是垃圾，都不该算进来 —— "+
			"这个计数是给人判断「该去配 AI 还是该调规则」的，算错就误导人",
			unclassified)
	}
}

// TestSplitReminderCandidates_AllUnclassifiedYieldsZeroButExplained 复刻真实
// 场景：kxmemory 没配时整批邮件 importance 全空。
//
// 此时 toNotify 为空（提醒确实发不出去），但 unclassified 必须等于总数 ——
// 报告上就变成「0 条提醒 / N 封未分类」，一眼看出是缺依赖而不是没重要邮件。
func TestSplitReminderCandidates_AllUnclassifiedYieldsZeroButExplained(t *testing.T) {
	emails := []Email{
		{ID: "a", Importance: ""},
		{ID: "b", Importance: ""},
		{ID: "c", Importance: ""},
	}
	toNotify, unclassified := splitReminderCandidates(emails, []int64{0, 0, 0})

	if len(toNotify) != 0 {
		t.Fatalf("未分类的邮件不该被当成重要邮件提醒，实际提醒了 %d 封", len(toNotify))
	}
	if unclassified != 3 {
		t.Fatalf("unclassified = %d, want 3", unclassified)
	}
}

// TestSplitReminderCandidates_HandlesShortNotifiedSlice 防越界：
// emails 与 notified 理论上等长，但别让一个长度不一致的输入把进程打崩。
func TestSplitReminderCandidates_HandlesShortNotifiedSlice(t *testing.T) {
	emails := []Email{{ID: "x", Importance: "high"}, {ID: "y", Importance: "high"}}
	toNotify, unclassified := splitReminderCandidates(emails, []int64{0})
	if len(toNotify) != 1 || toNotify[0].ID != "x" {
		t.Fatalf("notified 较短时只应处理前 %d 封，实际 %+v", 1, emailIDs(toNotify))
	}
	if unclassified != 0 {
		t.Fatalf("unclassified = %d, want 0", unclassified)
	}
}

func emailIDs(emails []Email) []string {
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		out = append(out, e.ID)
	}
	return out
}
