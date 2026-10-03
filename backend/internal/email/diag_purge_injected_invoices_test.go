package email

// diag_purge_injected_invoices_test.go —— 把「剔除台账里那 3 行自注入发票」
// 变成一个**可审计、可回滚、默认只出计划**的操作。
//
// ## 为什么要专门写这个
//
// round37 查明台账里 513.40 CNY 是本系统自己注入的测试数据（发件人 == 收件邮箱
// 本身），但**剔除是写操作**，没有用户授权前一步都不能做。写这个诊断的目的
// 不是为了「方便我删」，而是把决策所需的全部事实**先算出来**：
//
//   · 到底会删哪几行、删多少金额、连带哪些文件；
//   · 删前 / 删后合计分别是多少（可复算）；
//   · **删完之后这些行会不会自己长回来** —— 这是最容易被忽略的一条。
//
// ## 「会不会长回来」为什么是关键问题
//
// `pipeline.go:596` 的幂等跳过是「该 email_id 已有台账行 ⇒ continue」。
// 反过来读：**没有行 ⇒ 继续走判定链**。而判定链
// `invoiceKeywordHit → admitDebtNotice`（`invoice.go:663/669`）
// 压根不看发件人是谁。
//
// ⇒ 删掉行之后，只要那 3 封自注入邮件**再被处理一次**（POP3 重拉、
//    backfill、或用户手动触发），同样的行会按同样的金额重新建档。
//
// 所以本诊断在只读阶段就用**生产判定函数**跑一遍那 3 封邮件，
// 直接回答「删了会不会回来」，而不是等删完过两天发现又冒出来。
//
// ## 闸门（三道，缺一不跑）
//
//	POCKET_DIAG_PURGE=1              打开本诊断
//	POCKET_REAL_MAIL_DSN             显式传入，**无缺省值**（缺省会误打别的库）
//	POCKET_REAL_MAIL_SCHEMA          显式传入，**无缺省值**
//	POCKET_DIAG_PURGE_EXEC=1         **额外**这一条才允许真的 DELETE；
//	                                 不设时全程只读，且由数据库强制
//	                                 （default_transaction_read_only = on）
//
// 执行阶段还要求：备份表在同��事务里先建后删 —— 要么都成、要么都回滚，
// 不存在「删了但没备份」。
//
// ## 只删行、不删文件
//
// 删文件是另一类不可逆动作，本诊断**不做**。它只在报告里列出「删完会变成孤儿」的
// 文件名与 sha256，由人决定。理由：行删错了可以从备份表回滚，文件删错了没有。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// injectedInvoiceSQL 是「自注入发票行」的唯一定义。
//
// ⚠ 与 `diag_invoice_handoff_integrity_test.go` 的判据 0a 是**同一条** SQL。
// 两处各写一遍是有意的重复：本文件是写库路径，必须自带判据，不能依赖
// 另一个测试文件的局部实现（那样它就只在那个文件跑过时才成立）。
// **改这一处必须同步改那一处**，反之亦然。
const injectedInvoiceSQL = `
	SELECT ci.id, COALESCE(ci.file_name,''), COALESCE(ci.file_path,''),
	       ci.amount::float8, COALESCE(ci.currency,''), COALESCE(ci.seller,''),
	       COALESCE(ci.invoice_no,''), COALESCE(ci.invoice_date::text,''),
	       COALESCE(ci.status,''), COALESCE(ci.file_source,''),
	       ci.email_id, COALESCE(e.from_address,''), COALESCE(e.subject,''),
	       COALESCE(e.snippet,''), COALESCE(e.has_attachments,false)
	  FROM email_invoices ci
	  JOIN emails e ON e.id = ci.email_id
	  JOIN email_accounts ac ON ac.id = e.account_id
	 WHERE lower(e.from_address) = lower(ac.email_address)
		   AND COALESCE(ci.status,'') = 'downloaded'
	 ORDER BY ci.created_at`

// purgeCand 是一行待剔除的自注入发票。
type purgeCand struct {
	id, fileName, filePath        string
	amount                        float64
	currency, seller, invNo, date string
	status, fileSource, emailID   string
	fromAddr, subject, snippet    string
	hasAtt                        bool
}

func sumOf(cs []purgeCand) float64 {
	var s float64
	for _, c := range cs {
		s += c.amount
	}
	return s
}

func idsOf(cs []purgeCand) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.id)
	}
	return out
}

func TestDiagPurgeInjectedInvoices(t *testing.T) {
	if os.Getenv("POCKET_DIAG_PURGE") != "1" {
		t.Skip("set POCKET_DIAG_PURGE=1 (+ POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA); " +
			"add POCKET_DIAG_PURGE_EXEC=1 to actually delete")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN 未设置（本诊断无缺省值）")
	}
	if schema == "" {
		t.Fatal("POCKET_REAL_MAIL_SCHEMA 未设置（本诊断无缺省值）")
	}
	exec := os.Getenv("POCKET_DIAG_PURGE_EXEC") == "1"
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR") // 可选：给了就顺带列孤儿文件

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// 覆盖而非拼接：pgx 走 url.Values.Get，取 query 里**第一个** search_path，
	// 追加产生的第二个会被忽略（diag_merge_exec_test.go 里实测过）。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	if !exec {
		// 只读阶段由**数据库强制**，不靠「代码里没写 UPDATE」这句话。
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// search_path 钉死之后当场读回验证。写操作前「以为钉住了其实没钉住」
	// 没有任何症状（diag_merge_exec_test.go 的原话），所以这一步不能省。
	var resolved string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolved); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolved != schema {
		t.Fatalf("search_path 未生效：期望 %q，实际 %q。拒绝继续。", schema, resolved)
	}
	if !exec {
		// 自证只读真的生效：故意建一张临时表，它**必须**失败。
		if _, err := pool.Exec(ctx, `CREATE TEMP TABLE purge_write_guard(x int)`); err == nil {
			t.Fatal("连接竟然可写，只读保护没生效，拒绝继续（否则「只读」只是我嘴上说的）")
		}
	}
	t.Logf("schema 校验通过：current_schema() = %q；执行模式 = %v", resolved, exec)

	rows, err := pool.Query(ctx, injectedInvoiceSQL)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var cands []purgeCand
	for rows.Next() {
		var c purgeCand
		if err := rows.Scan(&c.id, &c.fileName, &c.filePath, &c.amount, &c.currency, &c.seller,
			&c.invNo, &c.date, &c.status, &c.fileSource, &c.emailID, &c.fromAddr,
			&c.subject, &c.snippet, &c.hasAtt); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(cands) == 0 {
		t.Log("[diag] 没有自注入的 downloaded 行。**这不等于「已经干净」**——" +
			"也可能是它们已被剔除，或口径变了。两种都不该被本诊断读成「数据没问题」。")
		return
	}

	// 删前合计（可复算底数）
	var beforeAll, beforeInjected float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(sum(amount::float8),0) FROM email_invoices
		  WHERE COALESCE(status,'')='downloaded' AND COALESCE(file_path,'') <> ''`).Scan(&beforeAll); err != nil {
		t.Fatalf("sum before: %v", err)
	}

	t.Logf("=== 待剔除 %d 行，合计 %.2f ===", len(cands), sumOf(cands))
	for _, c := range cands {
		beforeInjected += c.amount
		t.Logf("  id=%s  %.2f %s  开票方=%s 票号=%s 日期=%s 来源=%s",
			c.id, c.amount, c.currency, c.seller, c.invNo, c.date, c.fileSource)
		t.Logf("     文件=%s", c.fileName)
		t.Logf("     邮件 id=%s 发件人=%s", c.emailID, c.fromAddr)

		// 关键一问：删掉之后，这封邮件**再被处理一次**会不会把行重建出来？
		// 用生产判定链实跑，不重抄判据。
		//
		// 两腿都要跑：正文腿（bodyText 空）与附件腿（有发票类附件）——
		// `admitDebtNotice` 对这两腿给的是**不同**答案，漏掉哪一腿都会
		// 把「会不会长回来」判反。
		e := Email{ID: c.emailID, Subject: c.subject, Snippet: c.snippet, HasAttachments: c.hasAtt}
		for _, leg := range []struct {
			name   string
			body   string
			hasAtt bool
		}{
			{"无附件腿", "", false},
			{"有附件腿", "", c.hasAtt},
		} {
			inv, hit := ExtractInvoiceLoose(e, leg.body, leg.hasAtt)
			verdict := "hit=false（这一腿不会建档）"
			if hit {
				verdict = fmt.Sprintf("hit=true amount=%.2f date=%q seller=%q **会重建**",
					inv.Amount, inv.InvoiceDate, inv.Seller)
			}
			t.Logf("     粘性检查 %s: %s", leg.name, verdict)
		}
	}

	t.Logf("[口径] downloaded 行合计 %.2f → 剔除后 %.2f（差额 %.2f）",
		beforeAll, beforeAll-beforeInjected, beforeInjected)

	// 删完会变成孤儿的文件：只列不删。
	if dataDir != "" {
		for _, c := range cands {
			if c.filePath == "" {
				continue
			}
			p := filepath.Join(dataDir, c.filePath)
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Logf("  [孤儿·读不到] %s：%v", c.filePath, rerr)
				continue
			}
			sum := sha256.Sum256(b)
			t.Logf("  [孤儿·本诊断不删] %s  %d B  sha256=%s…",
				filepath.Base(c.filePath), len(b), hex.EncodeToString(sum[:])[:16])
		}
	}

	if !exec {
		t.Log("[只读] 未设 POCKET_DIAG_PURGE_EXEC=1，以上全部只做了计划。" +
			"**本诊断不删文件**；行要删需用户显式授权。")
		return
	}

	// ---- 执行阶段：备份 + 删除在同一个事务里 ----
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	backup := fmt.Sprintf("email_invoices_purge_backup_%s", time.Now().Format("20060102_150405"))
	if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s AS
		SELECT ci.*, now() AS purged_at FROM email_invoices ci
		 JOIN emails e ON e.id = ci.email_id
		 JOIN email_accounts ac ON ac.id = e.account_id
		WHERE lower(e.from_address) = lower(ac.email_address)
		  AND COALESCE(ci.status,'') = 'downloaded'`, backup)); err != nil {
		t.Fatalf("建备份表 %s 失败（已回滚，无任何删除）：%v", backup, err)
	}
	var bk int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+backup).Scan(&bk); err != nil {
		t.Fatalf("校验备份表失败（已回滚）：%v", err)
	}
	if bk != len(cands) {
		t.Fatalf("备份行数 %d != 计划剔除 %d（已回滚，无任何删除）", bk, len(cands))
	}
	t.Logf("备份表 %s 就绪，%d 行", backup, bk)

	tag, err := tx.Exec(ctx, `DELETE FROM email_invoices WHERE id = ANY($1::text[])`, idsOf(cands))
	if err != nil {
		t.Fatalf("删除失败（已回滚，备份表也随事务消失）：%v", err)
	}
	if int(tag.RowsAffected()) != len(cands) {
		t.Fatalf("删除行数 %d != 计划 %d，主动回滚", tag.RowsAffected(), len(cands))
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	t.Logf("已删除 %d 行，合计 %.2f；回滚路径：INSERT INTO email_invoices SELECT * FROM %s",
		len(cands), beforeInjected, backup)
	t.Log("**磁盘文件未删**（本诊断只删行）。孤儿文件清单见上面，处置需另行决定。")
}
