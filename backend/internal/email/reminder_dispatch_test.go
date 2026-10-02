package email

// reminder_dispatch_test.go — notifyImportant 的**派发**这一半（打真 PG）。
//
// 为什么补这一半：截至 2026-10-02，重要邮件提醒链路上已有的测试只钉住了
// 纯函数 splitReminderCandidates（判定「谁该被提醒」）与
// reminderUnclassifiedHint（诊断文案）。真正把邮件交给通知中心、再把
// 成功的那批写回 notified_at 的循环（pipeline.go 的 notifyImportant），
// grep NotifyImportantEmail 在全部 _test.go 里**零命中**。
//
// 缺这一半时，三种完全不同的情况在报告上长得一模一样（remindersSent=0
// 或一组看似正常的计数）：
//
//  1. 通知根本没派发出去（Notifier 被 nil 短路、循环没走到）——提醒功能形同虚设；
//  2. 派发出去了但 notified_at 没写回 —— 每轮重复推送，同一封邮件提醒 N 次；
//  3. **失败的那封也被记成已提醒** —— 永久漏提醒，且对 remindersSent 不可见
//     （它是按成功数记的，失败只进 rep.Errors，很容易被忽略）。
//
// 第 3 条最危险：一旦发生，邮件永远不会再来第二次提醒，而系统看起来完全正常。
// 下面每个用例都断言**数据库里的后果**（哪些 notified_at 被写、哪些没写），
// 而不是「有没有调到某个函数」。
import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingNotifier 记录真正被派发出去的邮件 id，并对指定 id 返回错误。
// 派发失败的不计入 delivered —— 因为「尝试过」和「送达了」在这条链路上
// 必须分开，否则测试自己就会把失败当成功。
type recordingNotifier struct {
	mu        sync.Mutex
	delivered []string
	attempted []string
	failOn    map[string]error
}

func (n *recordingNotifier) NotifyImportantEmail(ctx context.Context, e Email) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attempted = append(n.attempted, e.ID)
	if err, ok := n.failOn[e.ID]; ok {
		return err
	}
	n.delivered = append(n.delivered, e.ID)
	return nil
}

func (n *recordingNotifier) counts() (delivered, attempted int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.delivered), len(n.attempted)
}

func (n *recordingNotifier) deliveredIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.delivered...)
}

func seedReminderEmail(t *testing.T, store *Store, id, accountID, workspaceID, importance, category string) {
	t.Helper()
	now := time.Now().Unix()
	// 空串表示「还没有被分类过」：生产里 importance 就是 NULL（真实库 5 个账户
	// rules 全为 NULL 时 importance 恒空），所以这里必须落 NULL 而不是 ''，
	// 否则测的是另一种状态。
	var imp, cat *string
	if importance != "" {
		imp = &importance
	}
	if category != "" {
		cat = &category
	}
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address, from_name, subject,
		                    snippet, date, is_read, is_starred, has_attachments, created_at,
		                    importance, category)
		VALUES ($1, $2, $3, $4, 'boss@example.com', 'Boss', $5, 'body', $6, FALSE, FALSE, FALSE, $6, $7, $8)`,
		id, accountID, workspaceID, id+"@example.com", "subject "+id, now, imp, cat); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// 一行 NULL 文本列不得让整条扫描失败 —— 它的三个调用点（垃圾清理 / 发票候选
// 扫描 / 重要提醒）都是「err 就 AddError 后继续」，所以扫描一旦报错，这三步
// 会同时静默产出 0，而报告上只剩一个无法解释的 0。
//
// 现状说明：真库 124 行实测零 NULL（唯一写入者传 Go string），所以这条钉的是
// DDL 允许、手工改过列的库里可能出现的形态，不是当前已发生的故障。
func TestListEmailsSinceToleratesNullTextColumns(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-1", "user-1", "ws-a")
	now := time.Now().Unix()
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address, subject,
		                    snippet, date, is_read, is_starred, has_attachments, created_at, importance)
		VALUES ('em-nulls', 'acct-1', 'ws-a', 'em-nulls@example.com', 'boss@example.com',
		        NULL, NULL, $1, NULL, FALSE, FALSE, $1, 'high')`, now); err != nil {
		t.Fatalf("seed null email: %v", err)
	}

	emails, notified, err := store.ListEmailsSince(context.Background(), now-3600, 500)
	if err != nil {
		t.Fatalf("ListEmailsSince 因 NULL 列整条失败: %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("拿到 %d 行，want 1（NULL 文本列应按空串处理，而不是丢掉这行）", len(emails))
	}
	if emails[0].FromName != "" || emails[0].Subject != "" || emails[0].Snippet != "" {
		t.Fatalf("NULL 文本列未按空串处理: from_name=%q subject=%q snippet=%q",
			emails[0].FromName, emails[0].Subject, emails[0].Snippet)
	}
	if emails[0].Importance != "high" {
		t.Fatalf("importance=%q，want high（同一行里非 NULL 的列不能被牵连）", emails[0].Importance)
	}
	if len(notified) != 1 {
		t.Fatalf("notified 长度 %d，want 1（必须与 emails 等长，按下标配对）", len(notified))
	}
}

func reminderNotifiedAt(t *testing.T, store *Store, id string) int64 {
	t.Helper()
	var at int64
	if err := store.pool.QueryRow(context.Background(),
		`SELECT COALESCE(notified_at, 0) FROM emails WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read notified_at %s: %v", id, err)
	}
	return at
}

func runNotifyImportant(t *testing.T, store *Store, n ImportantNotifier) *PipelineReport {
	t.Helper()
	p := &Pipeline{Store: store, Notifier: n, DataDir: t.TempDir()}
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.notifyImportant(context.Background(), rep)
	return rep
}

// 一轮成功派发后，**只有真正送达的那封**被记为已提醒；垃圾邮件、非重要邮件、
// 未分类邮件都不得被写 notified_at。
//
// 判据钉在 notified_at 上：如果实现改成「先全记已提醒、再去派发」，
// 本用例会在 em-spam / em-low 上转红。
func TestNotifyImportantMarksOnlyDeliveredHighNonSpam(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-1", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-high", "acct-1", "ws-a", "high", "work")
	seedReminderEmail(t, store, "em-low", "acct-1", "ws-a", "low", "work")
	seedReminderEmail(t, store, "em-spam", "acct-1", "ws-a", "high", "spam")
	seedReminderEmail(t, store, "em-unclassified", "acct-1", "ws-a", "", "")

	notifier := &recordingNotifier{}
	rep := runNotifyImportant(t, store, notifier)

	delivered, _ := notifier.counts()
	if delivered != 1 {
		t.Fatalf("通知中心收到 %d 封（%v），want 1；scanned=%d unclassified=%d errors=%v",
			delivered, notifier.deliveredIDs(), rep.RemindersScanned, rep.RemindersUnclassified, rep.Errors)
	}
	if got := notifier.deliveredIDs(); got[0] != "em-high" {
		t.Fatalf("派发的是 %v，want [em-high]", got)
	}
	if at := reminderNotifiedAt(t, store, "em-high"); at <= 0 {
		t.Fatalf("em-high 已送达但 notified_at=%d，want >0（否则下轮会重复推送）", at)
	}
	for _, id := range []string{"em-low", "em-spam", "em-unclassified"} {
		if at := reminderNotifiedAt(t, store, id); at != 0 {
			t.Fatalf("%s 未被派发却被记为已提醒 notified_at=%d", id, at)
		}
	}
	if rep.RemindersSent != 1 {
		t.Fatalf("remindersSent=%d，want 1", rep.RemindersSent)
	}
	if rep.RemindersScanned != 4 {
		t.Fatalf("remindersScanned=%d，want 4（全部 4 封都进入判定）", rep.RemindersScanned)
	}
	if rep.RemindersUnclassified != 1 {
		t.Fatalf("remindersUnclassified=%d，want 1（em-unclassified 的 importance 为空）", rep.RemindersUnclassified)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("无失败却记了错误: %v", rep.Errors)
	}
}

// 派发失败的邮件**不得**被记成已提醒 —— 这是本文件最关键的一条。
// 记了的话这封邮件将永远不再提醒，而 remindersSent 仍按成功数记 1，
// 报告上看不出任何异常。
func TestNotifyImportantFailureIsNotRecordedAsNotified(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-1", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-ok", "acct-1", "ws-a", "high", "work")
	seedReminderEmail(t, store, "em-fail", "acct-1", "ws-a", "high", "work")

	notifier := &recordingNotifier{failOn: map[string]error{"em-fail": errors.New("notify center offline")}}
	rep := runNotifyImportant(t, store, notifier)

	if at := reminderNotifiedAt(t, store, "em-fail"); at != 0 {
		t.Fatalf("em-fail 派发失败却被记为已提醒 notified_at=%d —— 这封邮件将永远漏提醒", at)
	}
	if at := reminderNotifiedAt(t, store, "em-ok"); at <= 0 {
		t.Fatalf("em-ok 派发成功但 notified_at=%d，want >0（成功的不能被失败的一起连坐）", at)
	}
	if rep.RemindersSent != 1 {
		t.Fatalf("remindersSent=%d，want 1（只算成功的）", rep.RemindersSent)
	}
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "em-fail") {
		t.Fatalf("失败未进 rep.Errors 或没带上 email id: %v", rep.Errors)
	}
}

// 同一封邮件不得在第二轮再推一次；这正是 notified_at 存在的唯一理由。
func TestNotifyImportantDoesNotRepeatOnSecondRun(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-1", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-high", "acct-1", "ws-a", "high", "work")

	notifier := &recordingNotifier{}
	first := runNotifyImportant(t, store, notifier)
	if first.RemindersSent != 1 {
		t.Fatalf("第一轮 remindersSent=%d，want 1", first.RemindersSent)
	}
	second := runNotifyImportant(t, store, notifier)
	if second.RemindersSent != 0 {
		t.Fatalf("第二轮仍派发了 %d 封，want 0（重复提醒）", second.RemindersSent)
	}
	if _, attempted := notifier.counts(); attempted != 1 {
		t.Fatalf("两轮共尝试派发 %d 次，want 1", attempted)
	}
	if second.RemindersScanned != 1 {
		t.Fatalf("第二轮 remindersScanned=%d，want 1（邮件仍在 2 天窗口内）", second.RemindersScanned)
	}
}

// 未配置通知中心时必须安静地什么都不做，而不是把邮件记成已提醒。
// 记了的话，等通知中心配好之后这批邮件就再也不会被提醒了。
func TestNotifyImportantWithoutNotifierMarksNothing(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-1", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-high", "acct-1", "ws-a", "high", "work")

	rep := runNotifyImportant(t, store, nil)

	if at := reminderNotifiedAt(t, store, "em-high"); at != 0 {
		t.Fatalf("Notifier 为 nil 却把 em-high 记为已提醒 notified_at=%d", at)
	}
	if rep.RemindersSent != 0 || len(rep.Errors) != 0 {
		t.Fatalf("Notifier 为 nil 时 remindersSent=%d errors=%v，want 0 / 无", rep.RemindersSent, rep.Errors)
	}
}
