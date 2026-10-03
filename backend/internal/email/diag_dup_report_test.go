package email

// diag_dup_report_test.go — **只读**报表：列出每一组重复副本两侧的完整字段差异。
//
// 目的（2026-10-01）：合并 43 组重复需要用户定策略，但定策略前必须先看清
// 「两侧到底差在哪」。本诊断**只 SELECT，不 UPDATE/DELETE**，输出可直接阅读的
// 字段对照表。
//
// 每组按 (account_id, from_address, subject, date) 聚类——这是当前的**疑似**
// 判据；要确认「真的是同一封」需要按 UIDL 回填拿到真实 Message-ID（§7u 已
// 实测可行），但本报表先给出结构性差异，不依赖网络。

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagDuplicateReport(t *testing.T) {
	if os.Getenv("POCKET_DIAG_DUP_REPORT") != "1" {
		t.Skip("set POCKET_DIAG_DUP_REPORT=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT e.id, e.account_id, e.message_id, e.uid, e.subject, e.from_address,
		       to_timestamp(e.date) at time zone 'Asia/Shanghai',
		       e.has_attachments, coalesce(e.body_path,'') <> '' AS has_body,
		       coalesce(e.category,''), coalesce(e.importance,''), e.is_read, e.is_starred,
		       e.notified_at IS NOT NULL AS notified,
		       (SELECT count(*) FROM `+schema+`.email_invoices i WHERE i.email_id = e.id) AS inv_cnt,
		       (SELECT count(*) FROM `+schema+`.email_action_intents t WHERE t.email_id = e.id) AS intent_cnt
		FROM `+schema+`.emails e
		WHERE COALESCE(e.deleted_at, 0) = 0
		ORDER BY e.account_id, e.subject, e.message_id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type rec struct {
		id, acct, msgID, subj, from, ts, cat, imp string
		uid                                        int64
		att, body, read, star, notified            bool
		inv, intent                                int
	}
	var all []rec
	for rows.Next() {
		var r rec
		var hasAtt, hasBody, read, star, notified bool
		var ts time.Time
		if err := rows.Scan(&r.id, &r.acct, &r.msgID, &r.uid, &r.subj, &r.from, &ts,
			&hasAtt, &hasBody, &r.cat, &r.imp, &read, &star, &notified, &r.inv, &r.intent); err != nil {
			t.Fatalf("scan: %v", err)
		}
		r.att, r.body, r.read, r.star, r.notified = hasAtt, hasBody, read, star, notified
		r.ts = ts.Format("2006-01-02 15:04:05")
		all = append(all, r)
	}
	rows.Close()
	t.Logf("total live emails: %d", len(all))

	// 聚类
	type key struct{ acct, from, subj, ts string }
	groups := map[key][]rec{}
	for _, r := range all {
		k := key{r.acct, strings.ToLower(r.from), r.subj, r.ts}
		groups[k] = append(groups[k], r)
	}
	var dupKeys []key
	for k, g := range groups {
		if len(g) > 1 {
			dupKeys = append(dupKeys, k)
		}
	}
	sort.Slice(dupKeys, func(i, j int) bool { return dupKeys[i].subj < dupKeys[j].subj })
	t.Logf("=== duplicate groups: %d ===", len(dupKeys))

	// 关键统计：有多少组会丢状态
	loseInvoice, loseNotified, mixed := 0, 0, 0
	for _, k := range dupKeys {
		g := groups[k]
		var imapSide, pop3Side []rec
		for _, r := range g {
			if strings.HasPrefix(r.id, "em-pop3-") {
				pop3Side = append(pop3Side, r)
			} else {
				imapSide = append(imapSide, r)
			}
		}
		if len(imapSide) == 0 || len(pop3Side) == 0 {
			mixed++
		}
		// 只保留 IMAP 侧会丢的状态（在 POP3 侧上的）
		invOnPop3, notifOnPop3 := false, false
		for _, r := range pop3Side {
			if r.inv > 0 {
				invOnPop3 = true
			}
			if r.notified {
				notifOnPop3 = true
			}
		}
		if invOnPop3 {
			loseInvoice++
		}
		if notifOnPop3 {
			loseNotified++
		}
	}
	t.Logf("groups where POP3 side carries invoices (lost if we keep only IMAP side): %d", loseInvoice)
	t.Logf("groups where POP3 side was notified (lost if we keep only IMAP side): %d", loseNotified)
	t.Logf("groups that are NOT imap+pop3 pairs: %d", mixed)

	// 打印前 12 组明细
	const show = 12
	for i, k := range dupKeys {
		if i >= show {
			t.Logf("... (%d more groups)", len(dupKeys)-show)
			break
		}
		g := groups[k]
		t.Logf("--- group %d/%d: %q from=%s @%s ---", i+1, len(dupKeys), trunc(k.subj, 40), k.from, k.ts)
		for _, r := range g {
			kind := "IMAP"
			if strings.HasPrefix(r.id, "em-pop3-") {
				kind = "POP3"
			}
			t.Logf("    [%s] %s uid=%d msgid=%.34s att=%v body=%v cat=%q imp=%q read=%v star=%v notified=%v inv=%d intent=%d",
				kind, trunc(r.id, 46), r.uid, r.msgID, r.att, r.body, r.cat, r.imp, r.read, r.star, r.notified, r.inv, r.intent)
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
