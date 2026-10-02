package email

import (
	"context"
	"testing"
	"time"
)

// ---- 以下两段文件头注释分别来自两侧：ours 记录真实库 46 封的实测状态，
// theirs 记录「2 天窗口让老邮件永远不被提醒」这个缺陷的来龙去脉。两侧都保留。 ----

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

// reminder_window_test.go —— 重要邮件提醒的「2 天扫描窗口」让老邮件**永远不被提醒**，
// 而报告上**看不出来**。
//
// ## 缺陷
//
// `notifyImportant` 里 `since := time.Now().AddDate(0, 0, -2).Unix()` 是硬编码的，
// 扫描只覆盖最近 2 天。落在窗口之外、importance=high、且从未提醒过的邮件，
// **不是「这轮没轮到」，是「不在扫描范围里」**——之后每一轮都不会再看到它。
//
// 报告里原本有 RemindersSent / RemindersScanned / RemindersUnclassified，
// 但 0 这个数**分不清**下面两种情况：
//
//	「这批邮件里确实没有重要的」
//	「有 20 封重要的，但它们太老了，永远不会被提醒」
//
// §7cu 在真实数据上量到过后者（25 封 high 里 20 封已被永久漏掉），但报告上
// 只能看到一个没有解释的 0。
//
// ## 本轮只补可见性，不动窗口
//
// 「窗口该多长 / 要不要过期 / 首次上线要不要限流」是产品取舍（已在待拍板项里），
// 本轮**不擅自改**。这里只把那个不可观测的缺口补上——让取舍能带着实时数字做。
// 这与 `reminder_diag_test.go` 记录的 RemindersUnclassified 是同一类问题的
// **时间维度**版本：同一个坑换一个方向又出现了一次。

// ---- 以下为 ours（HEAD）侧：钉住 splitReminderCandidates 的判定分支 ----

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

// ---- 以下为 theirs 侧：窗口外计数的可见性（pipeline 端到端，打真库） ----

// seedAccountOnce 只在第一次调用时建账户，重复调用是 no-op。
func seedAccountOnce(t *testing.T, store *Store, id, user, ws string) {
	t.Helper()
	var exists bool
	if err := store.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM email_accounts WHERE id=$1)`, id).Scan(&exists); err != nil {
		t.Fatalf("probe account %s: %v", id, err)
	}
	if exists {
		return
	}
	seedAccount(t, store, id, user, ws)
}

// seedEmailWithAge 造一封带分类/重要度、且 date 落在 daysAgo 天前的邮件。
func seedEmailWithAge(t *testing.T, store *Store, id, accountID, ws, subject, importance string, daysAgo int) {
	t.Helper()
	// 账户只建一次（同一测试里多封邮件共用一个账户）。
	seedAccountOnce(t, store, accountID, "user-w", ws)
	seedEmail(t, store, id, accountID, ws, subject)
	old := time.Now().AddDate(0, 0, -daysAgo).Unix()
	if _, err := store.pool.Exec(context.Background(), `
		UPDATE emails SET category='work', importance=$1, date=$2, uid=$3
		WHERE id=$4`, importance, old, uidFor(id), id); err != nil {
		t.Fatalf("seed aged email %s: %v", id, err)
	}
}

func runReminderScan(t *testing.T) (*PipelineReport, int) {
	t.Helper()
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()

	// 窗口内（1 天前）的高重要度：应该被提醒。
	seedEmailWithAge(t, store, "e-fresh-high", "acc-w", "ws-w", "窗口内重要", "high", 1)
	// 窗口外（9 天前）的高重要度：**永远不会被提醒**，但必须被计数。
	seedEmailWithAge(t, store, "e-old-high", "acc-w", "ws-w", "窗口外重要", "high", 9)
	// 窗口外但重要性不是 high：不计入。
	seedEmailWithAge(t, store, "e-old-medium", "acc-w", "ws-w", "窗口外一般", "medium", 9)
	// 窗口外、high，但已提醒过：不计入（不是漏掉，是已经做过了）。
	seedEmailWithAge(t, store, "e-old-done", "acc-w", "ws-w", "窗口外已提醒", "high", 9)
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE emails SET notified_at=$1 WHERE id=$2`, time.Now().Unix(), "e-old-done"); err != nil {
		t.Fatalf("mark notified: %v", err)
	}

	n := &fakeNotifier{}
	p.Notifier = n
	rep := &PipelineReport{}
	p.notifyImportant(context.Background(), rep)
	return rep, len(n.notified)
}

func TestReminders_OutOfWindowHighIsCounted(t *testing.T) {
	rep, _ := runReminderScan(t)
	if rep.RemindersOutOfWindow != 1 {
		t.Errorf("RemindersOutOfWindow = %d, want 1（只有 e-old-high 符合"+
			"「早于窗口 + high + 未提醒」；e-old-medium 重要性不对、e-old-done 已提醒）",
			rep.RemindersOutOfWindow)
	}
}

func TestReminders_OutOfWindowStillNotifiesTheFreshOne(t *testing.T) {
	rep, notified := runReminderScan(t)
	// 加了这个计数**不能**改变原有行为：窗口内的重要邮件照常提醒。
	if notified != 1 {
		t.Errorf("实际提醒 %d 封, want 1（窗口内那封 high 必须仍被提醒）", notified)
	}
	if rep.RemindersSent != 1 {
		t.Errorf("RemindersSent = %d, want 1", rep.RemindersSent)
	}
}

func TestReminders_OutOfWindowIsZeroWhenNothingIsOld(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	seedEmailWithAge(t, store, "e-fresh", "acc-w", "ws-w", "新鲜", "high", 0)

	p.Notifier = &fakeNotifier{}
	rep := &PipelineReport{}
	p.notifyImportant(context.Background(), rep)
	if rep.RemindersOutOfWindow != 0 {
		t.Errorf("RemindersOutOfWindow = %d, want 0（没有老邮件时必须是 0，"+
			"否则这个计数分不清「没有」和「有很多」）", rep.RemindersOutOfWindow)
	}
}
