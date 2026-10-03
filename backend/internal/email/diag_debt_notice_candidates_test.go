package email

// diag_debt_notice_candidates_test.go —— **只读**诊断：平安/招商两张信用卡
// 电子账单，在 2026-10-04 08:00 那一轮会不会**再建一张幽灵发票**进台账？
//
// ## 为什么要问这个
//
// 2026-10-02 已经因为工行那封信踩过一次坑：`inv_1790903383222583800_1`
// amount=58000.00、invoice_date=2026-10-25，58000 是原文里的**信用额度**、
// 10-25 是**贷记卡到期还款日**——一笔没发生的 5.8 万支出进了台账。
// 随后加了 `reDebtNoticeShape` / `admitDebtNotice` / `ParseInvoiceDate` 三层收紧。
//
// 2026-10-03 22:4x 用只读探针在 967 封真实语料上重跑第 1 趟，发现语料里
// 还有**两张同形态**的信用卡电子账单尚未建档（envelope 抽不出金额，
// 要等第 2 趟拉正文才建档）：
//
//	平安信用卡电子账单   本期应还 ¥1,136.13  信用额度 ¥9x,xxx  还款日 2026-09-25
//	招商银行信用卡电子账单 账单周期 2026/08/08-2026/09/07  ¥80,000.00 / 25,440.34
//
// 也就是说：**明早那一轮，第 2 趟会对这两封信跑一遍判定链**，结果直接进
// 财务台账的合计。这不是可以等下一轮再看的事——建档是幂等写入，跑完就落库。
//
// ## 这个诊断的结论强度（先写清楚，别把它当成比实际更强的证据）
//
// 它**只跑 envelope 那一腿**：`ExtractInvoiceLoose(e, subject+snippet, …)`。
// 第 2 趟在生产里用的是**完整正文**（`b.parsed.TextBody+HTMLBody`），
// 正文比摘要长得多，`reInvoiceNo` 有更多机会被命中 ⇒ **正文那腿更容易放行**。
// 所以：
//
//   - 本诊断判「放行」 ⇒ 明早极可能真的会建档（幽灵风险已可判定）；
//   - 本诊断判「拦住」 ⇒ **不能**据此说安全，只说明「凭主题+摘要不足以建档」。
//
// 结论是**幽灵风险的下界**，不是上界。附件那一维同样取不到（正文加密在
// 缓存里，要重新拉取原文属取邮箱操作，本诊断不做）。
//
// ## 只读与隔离
//
// 全文件 0 写语句；只读由**数据库强制**（default_transaction_read_only = on，
// 写尝试直接报错），不靠「文件里没有写语句」这句话。DSN 与 schema 必须
// **显式**从 POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA 传入且**无缺省值**
// ——缺省会让人在不知情的情况下打到生产库。门禁 POCKET_DIAG_DEBT_SHAPE=1。
//
// 已登记在 internal/server/pg_test_isolation_guard_test.go 的
// pgSafeWithoutIsolation（不登记会让 internal/server 的护栏判红）。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// debtShapeSubjects 是本诊断关注的形态关键词（envelope 侧筛选用）。
// 用 SQL 侧 ILIKE 粗筛，再交给生产函数 reDebtNoticeShape 精判——
// 粗筛只用来少取行，**判定权不在这里**。
func TestDiagDebtNoticeCandidatesWouldBeFiled(t *testing.T) {
	if os.Getenv("POCKET_DIAG_DEBT_SHAPE") != "1" {
		t.Skip("set POCKET_DIAG_DEBT_SHAPE=1 (and POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA) " +
			"for the read-only debt-notice ghost-row diagnostic")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN 未设置（本诊断无缺省值：缺省会误打生产库）")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		t.Fatal("POCKET_REAL_MAIL_SCHEMA 未设置（本诊断无缺省值）")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.MaxConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// 只读自证：写尝试必须被 PG 拒绝。成功反而说明保护没生效。
	if _, err := pool.Exec(ctx, `CREATE TEMP TABLE diag_write_guard(x int)`); err == nil {
		t.Fatal("连接竟然可写，只读保护没生效，拒绝继续")
	}

	// 只取**尚未建档**的邮件：已建档的会被幂等跳过（pipeline.go:596），
	// 不在明早那一轮的判定范围内。混进来会让「会不会新建」这个问题失真。
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.subject, COALESCE(e.snippet,''), e.has_attachments,
		       COALESCE(e.category,''),
		       (SELECT count(*) FROM email_invoices i WHERE i.email_id = e.id) AS already
		  FROM emails e
		 WHERE COALESCE(e.deleted_at, 0) = 0
		   AND (e.subject ILIKE '%' || chr(36134) || chr(21333) || '%'
		        OR e.subject ILIKE '%statement%'
		        OR e.subject ILIKE '%credit card%'
		        OR e.subject ILIKE '%' || chr(20449) || chr(29992) || '%')
		 ORDER BY e.date DESC`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct {
		id, subject, snippet, category string
		hasAtt                         bool
		already                        int
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.subject, &r.snippet, &r.hasAtt, &r.category, &r.already); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("一行都没取到：粗筛条件写错了，本诊断会输出「无风险」的假结论")
	}
	t.Logf("[diag] 粗筛命中 %d 行（schema=%s，测量时刻 %s）",
		len(all), schema, time.Now().Format("15:04:05"))

	var ghostRisk, blocked, alreadyFiled int
	for _, r := range all {
		joined := r.subject + "\n" + r.snippet
		// 判定权在生产函数：形态正则 + admitDebtNotice 都不在这里重抄。
		if !reDebtNoticeShape.MatchString(joined) {
			continue
		}
		if r.already > 0 {
			alreadyFiled++
			t.Logf("[已有行] %-34s %s（明早会被幂等跳过）", debtTruncID(r.id), debtTrunc(r.subject, 40))
			continue
		}

		e := Email{ID: r.id, Subject: r.subject, Snippet: r.snippet}
		// 两列对照：hasInvoiceAttachment=false 是「没带发票类附件」，
		// true 是「带了」。生产第 2 趟用现场解析的附件，本诊断取不到，
		// 所以两列都给，只为看清「附件这一维会把结论推向哪边」。
		invNoAtt, hitNoAtt := ExtractInvoiceLoose(e, joined, false)
		invAtt, hitAtt := ExtractInvoiceLoose(e, joined, true)

		verdict := "拦住（凭主题+摘要不足以建档）"
		if hitNoAtt {
			verdict = "⚠ 放行 —— 明早极可能建成幽灵发票"
			ghostRisk++
		} else if hitAtt {
			verdict = "仅在「带发票类附件」时放行（附件证据本诊断取不到）"
		} else {
			blocked++
		}
		// ExtractInvoiceLoose 未命中时返回 **nil 指针**，不是零值 Invoice。
		// （第一版在这里无条件解引用，测试直接 panic——那说明判据自己写坏了，
		//  不是被测对象出错。）
		describe := func(inv *Invoice, hit bool) string {
			if !hit || inv == nil {
				return "hit=false（返回 nil）"
			}
			return fmt.Sprintf("hit=true amount=%.2f date=%q seller=%q no=%q",
				inv.Amount, inv.InvoiceDate, inv.Seller, inv.InvoiceNo)
		}
		t.Logf("[%s] db_has_attachments=%v\n    subject=%s\n    无附件腿: %s\n    有附件腿: %s",
			verdict, r.hasAtt, debtTrunc(r.subject, 60),
			describe(invNoAtt, hitNoAtt), describe(invAtt, hitAtt))
		// 参照：admitDebtNotice 三个信号各自在这封邮件上是否命中，
		// 便于事后判断「是哪一条判据在拦」。
		t.Logf("    信号: 发票号=%v 税号=%v 形态=%v",
			reInvoiceNo.MatchString(joined), reTaxNo.MatchString(joined), true)
	}
	t.Logf("[diag] 债务通知形态且未建档：放行 %d / 拦住 %d；另有 %d 封已有行（明早幂等跳过）",
		ghostRisk, blocked, alreadyFiled)
	if ghostRisk == 0 {
		t.Log("[diag] 结论：envelope 这一腿没有放行任何一封。**这不是「明早安全」的证明**——" +
			"生产第 2 趟用的是完整正文，正文里 reInvoiceNo 更容易命中，结论只覆盖 envelope 这一半。")
	}
}

func debtTruncID(s string) string {
	if len(s) <= 34 {
		return s
	}
	return s[:33] + "…"
}

func debtTrunc(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
