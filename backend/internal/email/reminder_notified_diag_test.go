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
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		t.Skip("POCKET_REAL_MAIL_SCHEMA not set; this diagnostic MUST know which schema " +
			"it is reading — see the search_path note below")
	}
	ctx := context.Background()
	// 【2026-10-02 修正】原来是 `pool, err := pgxpool.New(ctx, dsn)`，
	// 全程不设 search_path，靠 DSN 自带的那个。
	//
	// 为什么这是**会产出假结论**的缺陷而不只是「不够严谨」：
	// 本诊断的全部价值在于「和实现看到同一批邮件」（见上方注释），
	// 而下面的判据是
	//     if highUnnotified == 0 { 结论：不是缺陷 }
	// 若 DSN 的 search_path 指向 public 而非生产 schema，查询扫到 0 行，
	// highUnnotified 自然是 0，于是它会输出
	//     「46 封 high 全部已提醒过 —— remindersSent=0 符合设计，不是缺陷」
	// ——一个**由打错库产生的「不是缺陷」结论**。查空库永远「符合设计」。
	//
	// 与 diag_merge_exec_test.go / diag_rest_dupes_test.go 是同一类缺陷
	// （那两处在 round15 修掉了），本文件当时漏了。
	// 姊妹文件 diag_snippet_leak_test.go / spam_realdata_test.go 都显式钉了
	// schema，只有这个没有。
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// 覆盖式设置，不拼接：pgx 取 query 里**第一个** search_path，
	// 拼接产生的第二个会被忽略（实测 2026-10-02）。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// 当场验证连接确实落在目标 schema。「以为钉住了」正是本次缺陷的特征，
	// 而打错库的后果是**输出「不是缺陷」的假结论**——比报错危险得多。
	var resolvedSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolvedSchema != schema {
		t.Fatalf("search_path 未生效：期望 %q，连接实际落在 %q。**拒绝继续**——"+
			"下面的判据会在空库上得出「不是缺陷」的假结论。", schema, resolvedSchema)
	}
	t.Logf("search_path verified: current_schema() = %q", resolvedSchema)

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
