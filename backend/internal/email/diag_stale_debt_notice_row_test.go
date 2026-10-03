package email

// diag_stale_debt_notice_row_test.go —— **只读**诊断：那张「工行对账单被当成发票」
// 的存量行，现在还会不会被判定链放行？
//
// ## 为什么要问这个
//
// 2026-10-02 已经定位并修过这个误判：`reDebtNoticeShape` / `admitDebtNotice` /
// `ParseInvoiceDate` 的三层优先级 + 跳过未来日期，全是为这封信加的。代码注释里
// 点名了这一行：
//
//	inv_1790903383222583800_1  amount=58000.00  invoice_date=2026-10-25
//	主题=中国工商银行客户对账单(ICBC Peony Card Bank Statement)
//	58000 是原文里的**信用额度**，10-25 是**贷记卡到期还款日**
//
// 但那一行是**修复前**建进库的，而流水线的幂等跳过
// （`GetInvoiceByEmailID` 有行就 `continue`）会让它永远不被重新判定。
//
// 于是有一个必须用证据回答、不能靠读注释回答的问题：
// **如果今天重新判定这封邮件，它还会被建档吗？**
//
//   - 还会 → 说明光有分类修复不够，得处理存量行，且是分类规则本身要再收紧；
//   - 不会 → 说明唯一障碍就是幂等跳过，存量清理是一个有界的小问题。
//
// 本诊断**只 SELECT**，不 UPDATE/DELETE。门禁 POCKET_DIAG_STALE_ROW=1。
//
// ## 判据的局限（先写清楚，免得它被当成比实际更强的证据）
//
// 它只跑 **envelope 形态**（主题 + 摘要），也就是 `ExtractInvoice(e, "")`
// 那一条腿。要走 `ExtractInvoiceLoose(..., hasInvoiceAttachment=true)`
// 才会被放行，而那个开关依赖**附件**信息 —— 本诊断拿不到（正文是加密缓存，
// 附件要从 IMAP/POP3 重新拉，属于写/取邮箱操作，本诊断不做）。
// 所以「不会被放行」的结论只覆盖「凭主题+摘要不足以建档」这一半。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagStaleDebtNoticeRow(t *testing.T) {
	if os.Getenv("POCKET_DIAG_STALE_ROW") != "1" {
		t.Skip("set POCKET_DIAG_STALE_ROW=1 to run this read-only diagnostic")
	}
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "opencode_pocket"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	const rowID = "inv_1790903383222583800_1"
	var (
		emailID, subject, snippet, fromName, fromAddr string
		amountText, invDate, seller, status, lastErr  string
	)
	err = pool.QueryRow(ctx, `
		SELECT i.email_id, i.amount::text, coalesce(i.invoice_date,''), coalesce(i.seller,''),
		       coalesce(i.status,''), coalesce(i.last_error,''),
		       coalesce(e.subject,''), coalesce(e.snippet,''),
		       coalesce(e.from_name,''), coalesce(e.from_address,'')
		  FROM opencode_pocket.email_invoices i
		  JOIN opencode_pocket.emails e ON e.id = i.email_id
		 WHERE i.id = $1`, rowID).
		Scan(&emailID, &amountText, &invDate, &seller, &status, &lastErr,
			&subject, &snippet, &fromName, &fromAddr)
	if err != nil {
		t.Fatalf("read the row (it may already be gone — that is itself an answer): %v", err)
	}

	t.Logf("=== 库里的现状 ===")
	t.Logf("  id=%s email_id=%s", rowID, emailID)
	t.Logf("  amount=%s invoice_date=%s seller=%s status=%s attempts见 DB",
		amountText, invDate, seller, status)
	t.Logf("  subject=%s", subject)
	t.Logf("  from=%s <%s>", fromName, fromAddr)
	t.Logf("  last_error=%s", lastErr)

	em := Email{ID: emailID, Subject: subject, Snippet: snippet, FromName: fromName, FromAddress: fromAddr}

	// 腿 1：envelope 形态 —— 正是流水线的 GetInvoiceByEmailID 幂等跳过之后
	// 唯一会跑的那条（ExtractInvoice(e, "")）。
	inv, hit := ExtractInvoice(em, "")
	t.Logf("=== 腿 1：ExtractInvoice(envelope) ===")
	t.Logf("  hit=%v", hit)
	if hit && inv != nil {
		t.Logf("  → 仍会被建档：amount=%.2f date=%q seller=%q", inv.Amount, inv.InvoiceDate, inv.Seller)
	} else {
		t.Logf("  → 不会被建档（envelope 层面证据不足）")
	}

	// 腿 2：关键词 —— 说明这封邮件当初为什么会进到「需要拉原文」的队列。
	t.Logf("=== 腿 2：关键词命中 ===")
	t.Logf("  invoiceKeywordHit(subject+snippet)=%v", invoiceKeywordHit(subject+"\n"+snippet))
	if reDebtNoticeShape.MatchString(subject + "\n" + snippet) {
		t.Logf("  reDebtNoticeShape 命中 —— 债务通知形态（对账单/还款/额度）")
	} else {
		t.Logf("  reDebtNoticeShape **未**命中")
	}
	t.Logf("  admitDebtNotice(joined,false)=%v", admitDebtNotice(subject+"\n"+snippet, false))
	t.Logf("  admitDebtNotice(joined,true )=%v", admitDebtNotice(subject+"\n"+snippet, true))

	// 腿 3：幂等跳过这一层本身 —— 它是不是唯一还挡着的那一层。
	t.Logf("=== 腿 3：幂等跳过 ===")
	var existing int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM opencode_pocket.email_invoices WHERE email_id = $1`, emailID).
		Scan(&existing); err != nil {
		t.Fatalf("count: %v", err)
	}
	t.Logf("  该 email_id 已有 %d 行发票记录；流水线看到 >0 就 continue，"+
		"因此上面的判定**永远不会被执行到**", existing)

	// 未来日期检查：即使这一行被重新判定，日期层自己也有一道独立防线。
	t.Logf("=== 腿 4：日期层独立防线 ===")
	t.Logf("  库里存的 invoice_date=%s，距今 %d 天",
		invDate, daysBetween(invDate, time.Now()))

	_ = strings.TrimSpace
}

func daysBetween(iso string, now time.Time) int {
	t2, err := time.ParseInLocation("2006-01-02", iso, time.Local)
	if err != nil {
		return 0
	}
	now = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return int(t2.Sub(now).Hours() / 24)
}
