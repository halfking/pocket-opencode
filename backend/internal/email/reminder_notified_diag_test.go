package email

// reminder_notified_diag_test.go — 一次性诊断：为什么 46 封 importance='high'
// 的真实邮件没有产生任何提醒（真实流水线 remindersSent=0）。
//
// 2026-10-01 只读统计得到的一组互相矛盾的数字：
//
//	importance='high' 共 53 封，46 封落在 notifyImportant 的 2 天窗口内，
//	category 全是 work(38)/notification(6)/bill(2)，没有一封是 spam；
//	而流水线报告 remindersScanned=155 / remindersUnclassified=5 /
//	remindersSent=0。
//
// splitReminderCandidates 判定「该提醒」的条件是
// notified_at == 0 && category != 'spam' && importance == 'high'。
// reminder_window_test.go 已经证明这组 category 不会被排除，
// 所以剩下的唯一解释是 **notified_at 已经非零**。
//
// 这个诊断直接读真库把 notified_at 的分布打出来，确认这个解释。
// 诊断用，默认跳过：需要 POCKET_REAL_MAIL_DSN 指向真实 schema。
import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagnoseReminderNotifiedAt(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; point it at the real schema to run this diagnostic")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	since := time.Now().AddDate(0, 0, -importantReminderLookbackDays).Unix()

	// 与 ListEmailsSince 完全一致的扫描窗口与过滤条件。
	// 天数取自常量而非写死：这个诊断的价值全在「和实现看到同一批邮件」，
	// 窗口一改这里若还写死 -2 天，它会拿另一个集合去解释 remindersSent=0。
	const cols = `id, category, importance, notified_at`
	rows, err := pool.Query(ctx, `SELECT `+cols+`
		FROM emails
		WHERE date >= $1 AND COALESCE(deleted_at,0)=0
		ORDER BY date DESC LIMIT 2000`, since)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var scanned, highTotal, highNotified, highUnnotified int
	for rows.Next() {
		var id, cat, imp string
		var notifiedAt int64
		if err := rows.Scan(&id, &cat, &imp, &notifiedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		scanned++
		if imp != "high" || cat == "spam" {
			continue
		}
		highTotal++
		if notifiedAt > 0 {
			highNotified++
		} else {
			highUnnotified++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	t.Logf("扫描窗口 %s 起：", time.Unix(since, 0).Format("2006-01-02 15:04"))
	t.Logf("  扫描总数        = %d", scanned)
	t.Logf("  importance=high = %d", highTotal)
	t.Logf("    其中已提醒    = %d  (notified_at > 0)", highNotified)
	t.Logf("    其中未提醒    = %d  (notified_at == 0)", highUnnotified)

	// 关键判据：若 highUnnotified == 0，则 remindersSent=0 是**正确行为**
	//（都已提醒过，按设计不重复提醒），不是缺陷。若 > 0，说明提醒链路
	// 确实漏发，需要继续查。
	if highUnnotified == 0 {
		t.Logf("结论：46 封 high 全部已提醒过 —— remindersSent=0 符合设计，不是缺陷。")
	} else {
		t.Logf("结论：%d 封 high 未被提醒却没产生通知 —— 提醒链路漏发，需排查。", highUnnotified)
	}
}
