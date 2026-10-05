package email

// diag_mark_false_ledger_rows_test.go — 把「给台账里那 3 行假数据**打标注**」
// 变成一个**可审计、可回滚、默认只出计划**的操作。
//
// ## 授权（2026-10-04 round44）
//
// 用户在两个选项里选了**保留并标注**：不删行、不改 status，
// 只把 last_error 改写成「非发票凭证（营销横幅）」/「非发票（信用卡对账单）」。
// ⇒ 本文件严格照此执行，**不做**任何 DELETE、不改任何 status。
//
// ## 为什么必须专门写一个诊断，而不是一条 UPDATE
//
// 三个理由，缺一个都不该直接跑 SQL：
//
//  1. **选行口径要可复算。** 口径是「三个精确取值」而不是正则
//     （两个发票号 + 一个 58000.00 的工行行）。行数必须断言恰好 3——
//     少了说明数据变了，多了说明口径写宽了，两种都要停下来问人。
//  2. **要能回滚。** 执行阶段在**同一事务**里先建备份表再 UPDATE，
//     要么都成要么都回滚，不存在「标了一半」。
//  3. **只读阶段必须由数据库强制。** 不靠「代码里没写 UPDATE」这句话：
//     连接带 `default_transaction_read_only=on`，并当场用
//     「故意建一张临时表，它必须失败」自证。
//
// ## 这份诊断**不能**证明什么（必须一起说）
//
// 台账合计**不会**因为标注而改变。6354.20 CNY（横幅两行）与
// 58000.00 CNY（工行）仍然在 CNY 合计里，导出汇总单也照旧计入。
// 标注只是让「哪几行不可信」在台账里**看得见**，
// 要让汇总单不计入，得改导出口径——那是另一件事，本轮没做、也没授权。
// 本诊断会把「扣掉已标注行之后的合计」一并算出来，供决策使用。
//
// ## 闸门（四道，缺一不跑）
//
//	POCKET_DIAG_ANNOTATE=1          打开本诊断
//	POCKET_REAL_MAIL_DSN            显式传入，**无缺省值**（缺省会误打别的库）
//	POCKET_REAL_MAIL_SCHEMA         显式传入，**无缺省值**
//	POCKET_DIAG_ANNOTATE_EXEC=1     **额外**这一条才允许真的 UPDATE

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// bannerInvoiceNos 是两张百望横幅的**精确**发票号。
//
// 用精确取值而不是正则/前缀：已知取值集合时精确匹配永远比模式安全，
// 误匹配的代价是往一行**真发票**上贴「非发票」标注——那比不标坏得多。
// 另有一道断言兜底（行数必须恰好 3）。
var bannerInvoiceNos = []string{"26332000007943899111", "26112000003895678291"}

// icbcAmount 是工行那行的金额。round37 §35 已查实：这个数是**信用额度**，
// 不是开票金额，所以拿它当判据而不是拿 2026-10-25（那是到期还款日）。
const icbcAmount = 58000.00

// annotateTarget 是待标注的一行。
type annotateTarget struct {
	id          string
	seller      string
	amount      float64
	currency    string
	invoiceNo   string
	invoiceDate string
	status      string
	priorErr    string
	newErr      string
}

func TestDiagMarkFalseLedgerRows(t *testing.T) {
	if os.Getenv("POCKET_DIAG_ANNOTATE") != "1" {
		t.Skip("set POCKET_DIAG_ANNOTATE=1 (+ POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA); " +
			"add POCKET_DIAG_ANNOTATE_EXEC=1 to actually write the annotation")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN 未设置（本诊断无缺省值）")
	}
	if schema == "" {
		t.Fatal("POCKET_REAL_MAIL_SCHEMA 未设置（本诊断无缺省值）")
	}
	exec := os.Getenv("POCKET_DIAG_ANNOTATE_EXEC") == "1"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	if !exec {
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// search_path 钉死之后当场读回验证。「以为钉住了其实没钉住」
	// 在写操作上没有任何症状，所以这一步不能省。
	var resolved string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolved); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolved != schema {
		t.Fatalf("search_path 未生效：期望 %q，实际 %q。拒绝继续。", schema, resolved)
	}
	if !exec {
		if _, err := pool.Exec(ctx, `CREATE TEMP TABLE annotate_write_guard(x int)`); err == nil {
			t.Fatal("连接竟然可写，只读保护没生效，拒绝继续（否则「只读」只是我嘴上说的）")
		}
	}
	t.Logf("schema 校验通过：current_schema() = %q；执行模式 = %v", resolved, exec)

	// ── 选行 ──
	// 横幅两行：精确发票号 IN (...)。
	// 工行一行：seller 精确 + 金额精确 + 发票号为空（三条同时成立才算命中）。
	rows, err := pool.Query(ctx, `
		SELECT id, COALESCE(seller,''), amount::float8, COALESCE(currency,''),
		       COALESCE(invoice_no,''), COALESCE(invoice_date::text,''),
		       COALESCE(status,''), COALESCE(last_error,'')
		  FROM email_invoices
		 WHERE COALESCE(invoice_no,'') = ANY($1)
		    OR (COALESCE(seller,'') = '中国工商银行'
		        AND amount::float8 = $2
		        AND COALESCE(invoice_no,'') = '')
		 ORDER BY id`, bannerInvoiceNos, icbcAmount)
	if err != nil {
		t.Fatalf("select targets: %v", err)
	}
	var targets []annotateTarget
	for rows.Next() {
		var a annotateTarget
		if err := rows.Scan(&a.id, &a.seller, &a.amount, &a.currency,
			&a.invoiceNo, &a.invoiceDate, &a.status, &a.priorErr); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		a.newErr = composeMarkForInvoice(a)
		targets = append(targets, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// 行数硬断言。0 行和 4 行都必须停下来：
	// 前者说明数据已被别人改过，后者说明口径写宽了（贴错标注比不贴坏得多）。
	if len(targets) != 3 {
		t.Fatalf("待标注行数 = %d，期望恰好 3（2 横幅 + 1 工行）。拒绝继续：", len(targets))
		for _, a := range targets {
			t.Logf("  命中 %s seller=%q amount=%.2f no=%q", a.id, a.seller, a.amount, a.invoiceNo)
		}
	}

	// ── 计划（只读阶段也打，执行阶段也打）──
	t.Logf("")
	t.Logf("=== 标注计划（%d 行）===", len(targets))
	var annAmount float64
	for _, a := range targets {
		t.Logf("  %s  seller=%s  amount=%.2f %s  no=%q date=%q status=%s",
			a.id, a.seller, a.amount, a.currency, a.invoiceNo, a.invoiceDate, a.status)
		t.Logf("      旧 last_error: %s", orNone(a.priorErr))
		t.Logf("      新 last_error: %s", a.newErr)
		if a.currency == "CNY" {
			annAmount += a.amount
		}
	}
	// 合计：标注**不会**改合计。必须分两个口径报，因为它们相差一个数量级：
	//
	//	① 全表口径   ：email_invoices 全部行（含 pending/未核验）
	//	② 汇总单口径 ：status='downloaded' AND file_path<>''（导出单只计这些）
	//
	// 第一版只打了 ①，于是打出「台账 CNY 合计 68416.21」，
	// 而 invoices-summary-*.md 上写的是 10392.21 —— 同一句「合计」两个数，
	// 会被读成「标注把 58000 也算进去了」。差值来自 pending 行，不是标注造成的。
	// `diag_purge_injected_invoices_test.go` 用的就是 ② 那个口径。
	var totalAllCNY, totalSummaryCNY float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(sum(amount::float8),0) FROM email_invoices WHERE COALESCE(currency,'')='CNY'`,
	).Scan(&totalAllCNY); err != nil {
		t.Fatalf("sum all cny: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(sum(amount::float8),0) FROM email_invoices
		  WHERE COALESCE(currency,'')='CNY' AND COALESCE(status,'')='downloaded'
		    AND COALESCE(file_path,'')<>''`,
	).Scan(&totalSummaryCNY); err != nil {
		t.Fatalf("sum summary cny: %v", err)
	}
	// 被标注行里，落在汇总单口径内的是哪几行（横幅两行在，工行那行不在）。
	var annInSummary float64
	for _, a := range targets {
		var fp, st string
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(file_path,''), COALESCE(status,'') FROM email_invoices WHERE id = $1`,
			a.id).Scan(&fp, &st); err != nil {
			t.Fatalf("probe %s: %v", a.id, err)
		}
		if st == "downloaded" && fp != "" && a.currency == "CNY" {
			annInSummary += a.amount
		}
	}
	t.Logf("")
	t.Logf("① 全表 CNY 合计（全部 12 行，标注后**不变**）        : %.2f", totalAllCNY)
	t.Logf("② 汇总单 CNY 合计（downloaded+有文件，与 invoices-summary-*.md 同口径，标注后**不变**）: %.2f", totalSummaryCNY)
	t.Logf("   ② 里的已标注行金额 : %.2f", annInSummary)
	t.Logf("   ② 扣除已标注行后   : %.2f  ← 财务若剔除假数据该看到的数", totalSummaryCNY-annInSummary)
	t.Logf("   被标注行合计（跨口径）: %.2f", annAmount)

	// 幂等：已带标记的行不再叠标记（否则每跑一次就多一段）。
	var toWrite []annotateTarget
	for _, a := range targets {
		if strings.HasPrefix(a.priorErr, invoiceHumanMarkPrefix) {
			t.Logf("  [跳过-已标注] %s 已有标记，不再叠加", a.id)
			continue
		}
		toWrite = append(toWrite, a)
	}
	if len(toWrite) == 0 {
		t.Logf("全部 %d 行都已带标注，本轮无需写入（幂等）", len(targets))
		return
	}
	if !exec {
		t.Logf("只读模式：以上 %d 行**未被修改**。加 POCKET_DIAG_ANNOTATE_EXEC=1 才真的写。", len(toWrite))
		return
	}

	// ── 执行：单事务，备份表 + UPDATE 要么都成要么都回滚 ──
	backup := fmt.Sprintf("email_invoices_markbak_%s", time.Now().Format("20060102_150405"))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 正常路径已 Commit，这里是兜底

	if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s AS
		SELECT *, now() AS marked_at FROM email_invoices WHERE id = ANY($1)`, backup),
		idsOfTargets(toWrite)); err != nil {
		t.Fatalf("create backup table %s: %v", backup, err)
	}
	var backedUp int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, backup)).Scan(&backedUp); err != nil {
		t.Fatalf("count backup: %v", err)
	}
	if backedUp != len(toWrite) {
		t.Fatalf("备份表只有 %d 行，待写 %d 行 —— 事务回滚", backedUp, len(toWrite))
	}
	t.Logf("备份表 %s 已建，%d 行", backup, backedUp)

	for _, a := range toWrite {
		tag, err := tx.Exec(ctx,
			`UPDATE email_invoices SET last_error = $2, updated_at = $3 WHERE id = $1`,
			a.id, a.newErr, time.Now().Unix())
		if err != nil {
			t.Fatalf("update %s: %v", a.id, err)
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("update %s 影响 %d 行（期望 1）—— 事务回滚", a.id, tag.RowsAffected())
		}
	}
	// 同一事务内回读：不回读就等于「我以为写进去了」。
	for _, a := range toWrite {
		var got string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(last_error,'') FROM email_invoices WHERE id = $1`,
			a.id).Scan(&got); err != nil {
			t.Fatalf("readback %s: %v", a.id, err)
		}
		if got != a.newErr {
			t.Fatalf("回读不一致：%s 期望 %q 实际 %q —— 事务回滚", a.id, a.newErr, got)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Logf("已提交：%d 行标注写入，备份表 %s", len(toWrite), backup)
}

// composeMarkForInvoice 按行的实际特征生成标注文本。
//
// 用 switch 而不是 map：横幅与工行是**两类不同的假**，文案必须说清各自错在哪。
// 用 map + 缺省文案的话，将来多一种假就会静默吃到兜底文案。
func composeMarkForInvoice(a annotateTarget) string {
	switch {
	case a.invoiceNo == bannerInvoiceNos[0] || a.invoiceNo == bannerInvoiceNos[1]:
		return invoiceHumanMarkPrefix +
			"非发票凭证（营销横幅）：落盘件为 572×140（长宽比 4.09）平台宣传图，" +
			"且与另一横幅行字节 SHA256 相同（9ced44f4…），不可能是两笔不同发票的凭证。" +
			"本行金额与发票号系邮件正文解析所得，与图片内容无关。2026-10-04 人工复核。"
	case a.seller == "中国工商银行" && a.amount == icbcAmount:
		return invoiceHumanMarkPrefix +
			"非发票（信用卡对账单）：金额 58000.00 为**信用额度**、日期 2026-10-25 为**到期还款日**，" +
			"均非开票金额/开票日期（round37 §35 已查实）。2026-10-04 人工复核。"
	default:
		// 兜底文案必须**显式说不知道**，不能编一个像模像样的理由。
		return invoiceHumanMarkPrefix +
			"非发票（原因未登记）：该行不在已知假数据清单内却被选中，标注理由缺失，需人工复查。"
	}
}

func idsOfTargets(ts []annotateTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.id)
	}
	return out
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(空)"
	}
	return s
}
