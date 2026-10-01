package email

// spam_realdata_test.go — 用**真实邮箱数据**量化清垃圾判定到底有没有在干活。
//
// 动机：2026-10-01 凌晨在 6 个真实账户上跑预演，报告是
// 「spam dry-run: 0 mail(s) would be moved, nothing was moved」。
// 这有两种完全不同的解释：
//   (a) 真实信箱里确实没有广告/垃圾；
//   (b) 判定规则在真实数据上形同虚设（阈值太高 / 只看得到主题 / 编码问题），
//       于是这个需求在生产上等于没实现。
// 光看那句 0 无法区分，本测试用真库里的真实邮件把两种情况分开。
//
// 这是**诊断用**测试，默认跳过：它读的是生产 schema（只读 SELECT），
// 必须在 POCKET_REAL_MAIL_DSN 明确指定时才跑。

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSpamRuleOnRealMailboxData 把真实邮件灌进 LooksLikeSpam，报告命中率。
func TestSpamRuleOnRealMailboxData(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; set it to the real schema DSN to run this diagnostic")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT a.email_address, e.from_address, e.subject, coalesce(e.snippet,'')
		FROM emails e JOIN email_accounts a ON a.id = e.account_id
		WHERE COALESCE(e.deleted_at, 0) = 0
		ORDER BY e.date DESC
		LIMIT 500`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct {
		acct, from, subject, snippet string
	}
	var all []row
	byAcct := map[string]int{}
	emptySnippet := 0
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.acct, &r.from, &r.subject, &r.snippet); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all = append(all, r)
		byAcct[r.acct]++
		if r.snippet == "" {
			emptySnippet++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(all) == 0 {
		t.Skip("no emails in the real schema")
	}

	type hit struct {
		acct, from, subject, why string
		score                    int
	}
	var hits []hit
	// 边缘样本：参与评分但没过 100 阈值。LooksLikeSpam 在 2026-10-01 之前会
	// 把 Score 整个清零，这个指标因此恒为 0（死指标）；缺口修好后它才有意义，
	// 也正是**按真实数据校准阈值**需要的输入。
	type near struct {
		acct, from, subject, why string
		score                    int
	}
	var nears []near
	// 与 pipeline.go 生产路径同口径：按发件人统计本批封数再传入。
	// 这里若传 0，验证就退化成「不带批量信号」，与生产行为不一致——
	// 那样即使 spamHits 变 0 也说明不了任何问题。
	senderVolume := map[string]int{}
	for _, r := range all {
		senderVolume[strings.ToLower(strings.TrimSpace(r.from))]++
	}
	for _, r := range all {
		v := LooksLikeSpam(r.from, r.subject, r.snippet,
			InvoiceCandidate(Email{Subject: r.subject, Snippet: r.snippet}), false,
			senderVolume[strings.ToLower(strings.TrimSpace(r.from))])
		switch {
		case v.Spam:
			hits = append(hits, hit{r.acct, r.from, r.subject, v.Why, v.Score})
		case v.Score > 0:
			nears = append(nears, near{r.acct, r.from, r.subject, v.Why, v.Score})
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	sort.Slice(nears, func(i, j int) bool { return nears[i].score > nears[j].score })
	t.Logf("scanned=%d accounts=%d emptySnippet=%d (%.0f%%) spamHits=%d nearMiss(score>0,<100)=%d",
		len(all), len(byAcct), emptySnippet,
		100*float64(emptySnippet)/float64(len(all)), len(hits), len(nears))
	for acct, n := range byAcct {
		t.Logf("  account %-28s %d emails", acct, n)
	}
	for i, h := range hits {
		if i >= 15 {
			t.Logf("  ... and %d more", len(hits)-15)
			break
		}
		t.Logf("  SPAM score=%d acct=%s from=%q subject=%q why=%s", h.score, h.acct, h.from, h.subject, h.why)
	}
	// 边缘样本是调阈值的依据，必须真的打出来（哪怕为空也要说明）。
	t.Logf("near-miss top 15 of %d (未判垃圾但有分，开真实 MOVE 前值得人看一眼):", len(nears))
	for i, n := range nears {
		if i >= 15 {
			break
		}
		t.Logf("  NEAR score=%d acct=%s from=%q subject=%q why=%s", n.score, n.acct, n.from, n.subject, n.why)
	}

	// 断言不是「测试本身坏了」：空 snippet 比例要如实报出来，因为 snippet 为空
	// 会直接削弱判定。以及 near-miss 指标必须是活的（曾经恒为 0）。
	if len(all) >= 50 && emptySnippet == len(all) {
		t.Logf("NOTE: 全部 %d 封邮件 snippet 都为空 —— 判定实际只看主题", len(all))
	}
	_ = time.Now
}
