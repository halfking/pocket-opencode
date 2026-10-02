package email

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDiagRealInvoiceExtraction —— 用**当前抽取代码**跑真实邮件，看它从
// 中国工商银行信用卡对账单里抽出什么。
//
// 起因：真库 email_invoices 有一行
//
//	inv_1790903383222583800_1  amount=58000.00  invoice_date=2026-10-25
//	kind=bill  extracted_by=rule  invoice_no=''
//
// 来源邮件是「中国工商银行客户对账单(ICBC Peony Card Bank Statement)」，
// 原文里的三列是：应还款额 12,838.93 / 最低还款额 1,605.56 / **信用额度 58,000.00**
// 而 2026-10-25 是「贷记卡到期还款日」，对账单生成日其实是 2026-09-30。
//
// 疑似根因：invoice.go 的兜底分支用 reAnyAmount（无关键词）扫全文后
// **取最大值**（`if v > best { best = v }`）。信用额度是全文最大的数字，
// 于是被当成发票金额。
//
// 后果直击需求 3 的「汇总金额」：一笔**根本没发生**的 5.8 万元支出进了台账。
//
// 本测试只读真实库并调用生产函数，不写任何东西。
func TestDiagRealInvoiceExtraction(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; 跳过真实发票抽取诊断")
	}
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dataDir == "" {
		t.Skip("POCKET_REAL_DATA_DIR not set; 跳过")
	}
	_ = dataDir

	ctx := context.Background()
	cfg, perr := pgxpool.ParseConfig(dsn)
	if perr != nil {
		t.Fatalf("parse dsn: %v", perr)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("POCKET_REAL_MAIL_SCHEMA") + ",public"
	if cfg.ConnConfig.RuntimeParams["search_path"][0] == ',' {
		cfg.ConnConfig.RuntimeParams["search_path"] = "opencode_pocket,public"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, qerr := pool.Query(ctx, `
		SELECT e.id, COALESCE(e.subject,''), COALESCE(e.snippet,''), COALESCE(e.from_address,'')
		FROM emails e
		WHERE e.id IN (SELECT email_id FROM email_invoices)
		ORDER BY e.id`)
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	defer rows.Close()

	for rows.Next() {
		var e Email
		if serr := rows.Scan(&e.ID, &e.Subject, &e.Snippet, &e.FromAddress); serr != nil {
			t.Fatalf("scan: %v", serr)
		}
		inv, ok := ExtractInvoiceLoose(e, e.Snippet, false)
		if !ok {
			t.Logf("%s → 判定为非发票（正确）: %s", e.ID, e.Subject)
			continue
		}
		t.Logf("%s → 判定为发票", e.ID)
		t.Logf("    主题     : %s", e.Subject)
		t.Logf("    seller   : %q", inv.Seller)
		t.Logf("    amount   : %.2f %s", inv.Amount, inv.Currency)
		t.Logf("    date     : %s", emptyAsDash(inv.InvoiceDate))
		t.Logf("    invoiceNo: %q", inv.InvoiceNo)
		t.Logf("    kind     : %s / extractedBy=%s", inv.Kind, inv.ExtractedBy)
	}
	if rerr := rows.Err(); rerr != nil {
		t.Fatalf("rows: %v", rerr)
	}
}

// emptyAsDash 只做展示。Invoice.InvoiceDate 已是归一化后的 YYYY-MM-DD
// 字符串（invoice.go:32 / normalizeInvoiceDate），不是 unix 时间戳——
// 本文件原先按 int64 写，整个 email 包编译不过。
func emptyAsDash(d string) string {
	if d == "" {
		return "(空)"
	}
	return d
}
