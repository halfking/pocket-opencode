package email

// diag_invoice_backlog_test.go — **只读**诊断：需求 3 的发票积压到底有多大。
//
// ## 为什么需要它
//
// 真实库 2026-10-02 只有 2 张发票（3500 downloaded+有文件 / 58000 new+无文件），
// 128 封邮件。但「只有 2 张」这个数**分不清**下面两种：
//
//	「邮箱里就只收到过 2 张发票」——功能完成了；
//	「还有 N 张没被发现」——功能存在但没跑出来。
//
// 而「跑一次流水线会不会自动补上」是需求 3 的核心承诺，判据不该是
// 「代码看着像是对的」。这个诊断把第 1.5 步的判定链在真实数据上跑一遍，
// 直接给出待建档的封数、以及其中多少封需要拉 IMAP 原文（每封一次完整
// IMAP 会话，受 maxInvoiceBodyFetches 预算限制）。
//
// ## 复用生产判据，不重抄
//
// 判定链与 extractInvoiceCandidates 完全同源：
//
//	ListEmailsSince(90d, 2000) → 跳过 GetInvoiceByEmailID 无错的（已建档）
//	→ ExtractInvoice(e, "") → invoiceBodyReason(hit, inv, e)
//
// 抄一份判定的话，规则一改诊断就悄悄说谎——而它全部的价值就在于它说的
// 就是线上会做的事。
//
// ## 只读
//
// 连接上 `SET default_transaction_read_only = on`，写尝试直接报错。
// 绕开 NewStore（它会 migrate() 建表，那是写）。门禁
// POCKET_DIAG_INVOICE_BACKLOG=1。

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagInvoiceBacklog(t *testing.T) {
	if os.Getenv("POCKET_DIAG_INVOICE_BACKLOG") != "1" {
		t.Skip("set POCKET_DIAG_INVOICE_BACKLOG=1 to run (read-only)")
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

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	scope := make(map[string][2]string, len(accounts))
	for _, a := range accounts {
		if a.UserID != "" {
			scope[a.ID] = [2]string{a.UserID, defaultWorkspace(a.WorkspaceID)}
		}
	}

	// 与第 1.5 步同一窗口与同一行数上限。
	emails, _, err := store.ListEmailsSince(ctx,
		time.Now().AddDate(0, 0, -invoiceCandidateLookbackDays).Unix(), invoiceCandidateScanLimit)
	if err != nil {
		t.Fatalf("ListEmailsSince: %v", err)
	}
	if len(emails) == 0 {
		t.Fatal("窗口内一封邮件都没有：先修连接或窗口，别把空结果当成「没有发票」")
	}

	var filed, inScope, pending int
	reasonCount := map[string]int{}
	type cand struct {
		reason, subject, from string
		date                  time.Time
	}
	var cands []cand

	for i := range emails {
		e := emails[i]
		if _, err := store.GetInvoiceByEmailID(ctx, e.ID); err == nil {
			filed++
			continue // 已建档，幂等跳过（与生产同一条）
		}
		if _, ok := scope[e.AccountID]; !ok {
			continue
		}
		inScope++
		inv, hit := ExtractInvoice(e, "")
		reason := invoiceBodyReason(hit, inv, e)
		if reason != "" {
			reasonCount[reason]++
			cands = append(cands, cand{reason, e.Subject, e.FromAddress,
				time.Unix(e.Date, 0)})
		}
		pending++
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].date.After(cands[j].date) })

	t.Logf("=== 需求 3 发票积压（%d 天窗口，与第 1.5 步同参数）===",
		invoiceCandidateLookbackDays)
	t.Logf("窗口内邮件 %d 封；其中已建档发票 %d 封", len(emails), filed)
	t.Logf("**未建档**（幂等跳过后剩下的）: %d 封", pending)
	t.Logf("其中 envelope 已命中（hit）: 见下表；需要拉 IMAP 原文的按 reason 分：")
	for _, k := range []string{"date", "candidate"} {
		t.Logf("  reason=%-9s %d 封", k, reasonCount[k])
	}
	t.Logf("拉原文预算 maxInvoiceBodyFetches=%d/轮 → 本轮最多处理 %d 封，"+
		"其余顺延下一轮（顺延不是丢弃）",
		maxInvoiceBodyFetches, maxInvoiceBodyFetches)
	if len(reasonCount) == 0 {
		t.Logf("没有任何邮件被判为需要拉原文：未建档的 %d 封都在 envelope 阶段就能定案，"+
			"或根本不是发票候选", pending)
	}
	t.Logf("--- 待拉原文的邮件（最多列 30 封）---")
	shown := 0
	for _, c := range cands {
		if shown >= 30 {
			t.Logf("  ...（还有 %d 封未列）", len(cands)-shown)
			break
		}
		shown++
		t.Logf("  [%-9s] %s  %.40s  %.60s", c.reason, c.date.Format("MM-DD 15:04"), c.from, c.subject)
	}
}
