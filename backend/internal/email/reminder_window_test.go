package email

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

import (
	"context"
	"testing"
	"time"
)

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
