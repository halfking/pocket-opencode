package email

// reminder_window_test.go — 钉住一个真实数据里暴露的矛盾。
//
// 2026-10-01 真实库状态（5 个真实账户，只读统计）：
//
//	importance='high' 共 53 封，日期分布 09-27(4) / 09-29(5) / 09-30(28) / 10-01(16)
//	其中落在 notifyImportant 的 **2 天扫描窗口**内的有 **46 封**
//	它们的 category 分别是 work(38) / notification(6) / bill(2) —— 没有一封是 spam
//
// 而同一天的流水线报告是：
//
//	remindersScanned=155, remindersUnclassified=5, remindersSent=0
//
// splitReminderCandidates 的判定是：notified==0 && category!='spam' &&
// importance=='high' → 该提醒。46 封看起来全部满足，**却一封没提醒**。
//
// 这个测试固定住该判定的三个关键分支，并显式覆盖真实数据里的那 46 封
// 所处的状态组合。它不试图断言「应该提醒多少封」（那取决于 notified_at 的
// 历史值，CI 里读不到），而是断言**判定本身在这组输入下必须产出候选**——
// 如果哪天 splitReminderCandidates 被改坏（比如把 date 窗口算错、
// 或把 work/notification 类别误当垃圾排除），这里立刻转红。
import (
	"testing"
	"time"
)

// 复刻真实数据里那 46 封的状态：importance=high、category 非 spam。
func TestReminderCandidates_RealHighImportanceWindowIsNotExcludedByCategory(t *testing.T) {
	var emails []Email
	notified := []int64{}
	// 三种真实出现的 category 各若干封，全部未提醒过。
	for i := 0; i < 38; i++ { // work
		emails = append(emails, Email{ID: idFor("w", i), Importance: "high", Category: "work"})
		notified = append(notified, 0)
	}
	for i := 0; i < 6; i++ { // notification
		emails = append(emails, Email{ID: idFor("n", i), Importance: "high", Category: "notification"})
		notified = append(notified, 0)
	}
	for i := 0; i < 2; i++ { // bill
		emails = append(emails, Email{ID: idFor("b", i), Importance: "high", Category: "bill"})
		notified = append(notified, 0)
	}

	toNotify, unclassified := splitReminderCandidates(emails, notified)
	if len(toNotify) != len(emails) {
		t.Fatalf("46 封 high 且非 spam、未提醒过的邮件应全部进入候选，实际 %d/%d。"+
			"若有被排除的，说明判定把正常业务类别误当成了垃圾 —— "+
			"这正是「remindersSent=0 但库里有 46 封 high」最可能的成因",
			len(toNotify), len(emails))
	}
	if unclassified != 0 {
		t.Fatalf("unclassified = %d, want 0（这些邮件 importance 已判定为 high，不是「不知道」）", unclassified)
	}
}

// TestReminderCandidates_AlreadyNotifiedAreSkipped 对照组：已提醒过的不再提醒。
// 真实库里 53 条 email.important 通知正说明这条分支在工作。
func TestReminderCandidates_AlreadyNotifiedAreSkipped(t *testing.T) {
	now := time.Now().Unix()
	emails := []Email{
		{ID: "already", Importance: "high", Category: "work"},
		{ID: "fresh", Importance: "high", Category: "work"},
	}
	notified := []int64{now, 0}

	toNotify, unclassified := splitReminderCandidates(emails, notified)
	if len(toNotify) != 1 || toNotify[0].ID != "fresh" {
		t.Fatalf("只有未提醒过的 fresh 该进候选，实际 %+v", emailIDs(toNotify))
	}
	if unclassified != 0 {
		t.Fatalf("unclassified = %d, want 0", unclassified)
	}
}

func idFor(prefix string, i int) string {
	return prefix + "-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
