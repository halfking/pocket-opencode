package email

// diag_reminder_backlog_test.go — **只读**诊断：需求 4 的提醒积压里到底有什么。
//
// ## 为什么需要它
//
// 90 天回看窗口（importantReminderLookbackDays）已经上线，但**从未真正跑过**。
// 真实库上「32 封 importance=high 从未提醒」这个数，单独看不足以支撑
// 「要不要限流」这个产品决定 —— 32 是一个聚合量，它可能是：
//
//	32 封都是真要紧的（那就该全推）；
//	32 封里 15 封是 category=notification 的例行通知（那限流就该按
//	category 分层，而不是按总数截断）；
//	32 封分属 5 个不同账户（那按账户限流和全局限流的含义完全不同）。
//
// 三种情况的处置完全相反，而这个区别**只看聚合数永远看不出来**。
//
// ## 复用生产判据，不重抄
//
// 判定链与 notifyImportant 的第 1.5 步到推送前完全同源：
//
//	ListEmailsSince(now-90d, 2000) → splitReminderCandidates(emails, notified)
//	→ CountHighImportanceOutside(since, 2000)
//
// splitReminderCandidates 是生产函数本身，不是本文件重写的近似；
// 抄一份的话，规则一改诊断就悄悄说谎 —— 而它全部的价值就在于
// 它说的就是线上会做的事。
//
// ## 只读
//
// 连接上 `SET default_transaction_read_only = on`，写尝试直接报错。
// 绕开 NewStore（它会 migrate() 建表，那是写）。门禁
// POCKET_DIAG_REMINDER_BACKLOG=1。

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagReminderBacklog(t *testing.T) {
	if os.Getenv("POCKET_DIAG_REMINDER_BACKLOG") != "1" {
		t.Skip("set POCKET_DIAG_REMINDER_BACKLOG=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	if schema == "" {
		t.Skip("POCKET_REAL_MAIL_SCHEMA not set")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	safeSchema := pgx.Identifier{schema}.Sanitize()
	cfg.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(c, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := conn.Exec(c, "SET search_path TO "+safeSchema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	store := &Store{pool: pool}

	// 账户 id → 显示地址，让清单可读。
	addr := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT id, COALESCE(email_address,'') FROM email_accounts`)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	for rows.Next() {
		var id, a string
		if err := rows.Scan(&id, &a); err != nil {
			rows.Close()
			t.Fatalf("scan account: %v", err)
		}
		addr[id] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("accounts rows: %v", err)
	}

	since := time.Now().AddDate(0, 0, -importantReminderLookbackDays).Unix()
	outWindow, err := store.CountHighImportanceOutside(ctx, since, 2000)
	if err != nil {
		t.Fatalf("CountHighImportanceOutside: %v", err)
	}
	emails, notified, err := store.ListEmailsSince(ctx, since, importantReminderScanLimit)
	if err != nil {
		t.Fatalf("ListEmailsSince: %v", err)
	}
	if len(emails) == 0 {
		t.Fatal("窗口内一封邮件都没有：先修连接或窗口，别把空结果当成「没有积压」")
	}
	// 扫描被截断时，积压数就只是「至少这么多」而不是确切值 —— 必须说出来，
	// 否则报一个假的精确数。
	truncated := len(emails) >= importantReminderScanLimit

	candidates, unclassified := splitReminderCandidates(emails, notified)

	t.Logf("=== 需求 4 积压画像 ===")
	t.Logf("回看窗口            = %d 天", importantReminderLookbackDays)
	t.Logf("扫描行数            = %d%s", len(emails), map[bool]string{true: "  <-- 已触顶，下面所有计数都只是下界"}[truncated])
	t.Logf("importance 为空     = %d（未分类，不进提醒但也不是「不重要」）", unclassified)
	t.Logf("窗口外永不提醒的    = %d（date < since 且 high 且从未提醒）", outWindow)
	t.Logf("本轮将推送 RemindersPending = %d", len(candidates))
	if len(candidates) == 0 {
		t.Logf("没有积压：需求 4 这一轮不会有推送发生。")
		return
	}

	// candidates 的顺序就是生产推送顺序（ListEmailsSince 是 date DESC）。
	type row struct {
		id, account, subject, category string
		date                           int64
		ageDays                        int
	}
	now := time.Now()
	list := make([]row, 0, len(candidates))
	for _, e := range candidates {
		list = append(list, row{
			id:       e.ID,
			account:  addr[e.AccountID],
			subject:  e.Subject,
			category: e.Category,
			date:     e.Date,
			ageDays:  int(now.Sub(time.Unix(e.Date, 0)).Hours() / 24),
		})
	}

	// 按 category 分桶：限流策略该不该按类别分层，取决于这个分布。
	byCat := map[string]int{}
	byAcct := map[string]int{}
	byAge := map[string]int{}
	for _, r := range list {
		byCat[r.category]++
		byAcct[r.account]++
		switch b := r.ageDays; {
		case b <= 2:
			byAge["0-2d"]++
		case b <= 7:
			byAge["3-7d"]++
		case b <= 30:
			byAge["8-30d"]++
		default:
			byAge[">30d"]++
		}
	}
	t.Logf("\n--- 按 category 分桶（限流该不该分层，看这行） ---")
	for _, k := range sortedKeys(byCat) {
		t.Logf("  %-14s %3d  (%s)", k, byCat[k], bar(byCat[k], len(list)))
	}
	t.Logf("\n--- 按账户分桶（全局限流 vs 按账户限流的含义，看这行） ---")
	for _, k := range sortedKeys(byAcct) {
		t.Logf("  %-40s %3d", k, byAcct[k])
	}
	t.Logf("\n--- 按账龄分桶（积压有多老） ---")
	for _, k := range []string{"0-2d", "3-7d", "8-30d", ">30d"} {
		if n := byAge[k]; n > 0 {
			t.Logf("  %-8s %3d", k, n)
		}
	}

	t.Logf("\n--- 逐条清单（顺序＝生产推送顺序，date DESC） ---")
	for i, r := range list {
		t.Logf("%3d. [%s] age=%2dd cat=%-12s %s", i+1, time.Unix(r.date, 0).Format("2006-01-02 15:04"), r.ageDays, r.category,
			diagTruncate(r.subject, 60)+"  <- "+r.account)
	}

	// 如果按「只推最近 N 条」限流，剩下的是什么构成 —— 让拍板的人
	// 能对比 N=5/10/20 的后果，而不是凭感觉。
	t.Logf("\n--- 若按「只推最近 N 条」限流，会剩下什么 ---")
	for _, n := range []int{5, 10, 20, 32} {
		if n > len(list) {
			n = len(list)
		}
		keep := map[string]int{}
		for _, r := range list[:n] {
			keep[r.category]++
		}
		parts := make([]string, 0, len(keep))
		for _, k := range sortedKeys(keep) {
			parts = append(parts, fmt.Sprintf("%s=%d", k, keep[k]))
		}
		t.Logf("  N=%-3d 推 %s，丢掉 %d 封", n, strings.Join(parts, " "), len(list)-n)
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func bar(n, total int) string {
	if total <= 0 {
		return ""
	}
	const width = 24
	filled := n * width / total
	if filled == 0 && n > 0 {
		filled = 1
	}
	return strings.Repeat("#", filled)
}

// diagTruncate 压平空白后按 rune 截断。包内已有同名 truncate
// （invoice_sources_e2e_test.go），诊断文件不与之共用名字。
func diagTruncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
