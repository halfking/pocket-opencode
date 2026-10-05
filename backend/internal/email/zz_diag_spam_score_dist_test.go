package email

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 需求第 1 条「清理广告与垃圾邮件」的定性诊断（只读、需显式 DSN）。
//
// ## 它回答的是哪个问题
//
// 真实库 category 分布（2026-10-04）：空 411 / work 261 / notification 200 /
// **marketing 65** / bill 39 / **spam 2**。需求说「清理广告与垃圾邮件」，
// marketing（广告营销）恰恰是最该清的一类，却与 spam 分开统计。
//
// 第二十节只能说「可能是有意保留的」，**没定性**。本节把它问到底。
//
// ## 先查代码结构，再下结论
//
// spam.go:963-965 写明：真实邮件命中 0 封，但有 17 封拿到 30 分
// （newsletter/EDM 发件人特征），「就卡在门槛外侧。阈值该不该调、
// 这些订阅该不该留，只能由你看着具体主题决定」。
// spam.go:140-149 另记了一个**已知自洽性问题**：同一发件人会因单封模板
// 有没有退订链接而判定不一致（InfoQ 3 封里 2 封判垃圾、第 3 封只 30 分），
// 并注明「属于产品语义，没有拍板前不改」。
// spam.go:256 阈值 = **100 分**。
//
// 所以本诊断**不改阈值**（那是产品决定），只把「marketing 那 65 封
// 到底差多少分」量化出来，供拍板时判断。
//
// ## 门禁与隔离（照 diag_stale_debt_notice_row_test.go 的既定写法）
//
// POCKET_DIAG_SPAM_SCORE=1 + POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA
// **必须显式，无缺省值**（缺省会让人在不知情的情况下打到生产库）。
// 只读由**数据库强制**：连接上 default_transaction_read_only = on，
// 任何写尝试直接报错，而不是靠「代码里只有 SELECT」这句话。
func TestDiagMarketingSpamScoreDistribution(t *testing.T) {
	if os.Getenv("POCKET_DIAG_SPAM_SCORE") != "1" {
		t.Skip("set POCKET_DIAG_SPAM_SCORE=1 (+ POCKET_REAL_MAIL_DSN/SCHEMA)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; refusing to guess a database")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		t.Skip("POCKET_REAL_MAIL_SCHEMA not set; refusing to guess a schema")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT id, coalesce(subject,''), coalesce(snippet,''),
		       coalesce(from_address,''), coalesce(importance,'')
		FROM emails
		WHERE category = 'marketing' AND coalesce(deleted_at,0) = 0
		ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	buckets := map[int]int{}
	total, over := 0, 0
	for rows.Next() {
		var e Email
		if err := rows.Scan(&e.ID, &e.Subject, &e.Snippet, &e.FromAddress, &e.Importance); err != nil {
			t.Fatalf("scan: %v", err)
		}
		// 直接复用生产判据，不复刻一份 —— 复刻就是两份会各自漂移的代码。
		// senderVolume 传 1：单封视角。生产 pipeline.go:969 会统计同发件人
		// 批量传入，那条信号**只加不减**分，结论方向不变。
		v := LooksLikeSpam(e.FromAddress, e.Subject, e.Snippet,
			InvoiceCandidate(e), e.Importance == "high", 1)
		buckets[v.Score]++
		total++
		if v.Score >= 100 {
			over++
		}
		t.Logf("score=%3d spam=%v  %-44.44s  from=%s", v.Score, v.Spam, e.Subject, e.FromAddress)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if total == 0 {
		t.Fatal("查不到 marketing 行 —— 判据会输出「一切正常」的假结论")
	}

	t.Logf("=== marketing %d 封的评分分布（阈值 100）===", total)
	for s := 0; s <= 100; s += 10 {
		if n, ok := buckets[s]; ok {
			t.Logf("  %3d 分: %d 封", s, n)
		}
	}
	t.Logf("≥100 分（会被判垃圾）: %d 封", over)

	switch {
	case over > 0:
		t.Logf("有 %d 封 marketing 已达阈值却仍在库里 —— 要么清理没执行，要么这些是"+
			"后来才被判的。**这两者要分开**，本诊断只回答「规则会不会命中」。", over)
	default:
		t.Log("结论：规则一条都没命中这批 marketing，留在收件箱是**规则当前的行为**，" +
			"不是「清理没跑」。要不要收紧阈值是产品决定（见 spam.go:963-965）。")
	}
}
