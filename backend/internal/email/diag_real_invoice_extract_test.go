package email

import (
	"context"
	"os"
	"sort"
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
	// 【2026-10-02 修正】原来写的是
	//     cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("POCKET_REAL_MAIL_SCHEMA") + ",public"
	//     if cfg.ConnConfig.RuntimeParams["search_path"][0] == ',' { ... = "opencode_pocket,public" }
	// 那个兜底靠「拼完看首字符是不是逗号」来发现环境变量没设——脆弱且难读：
	// 变量真被设成 ",public" 或以逗号开头时同样会误判。
	// 改成先取值再判空，意图直白。
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket" // 本文件的缺省就是生产 schema
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 2026-10-02 补：当场验证 search_path 确实落在目标 schema 上。
	//
	// 覆盖式设置（RuntimeParams）只有单一来源，不像 DSN 拼接那样有
	// 「pgx 取第一个同名参数」的歧义；但「以为钉住了」正是本仓库
	// search_path 缺陷家族的特征（见 diag_merge_exec_test.go 与
	// reminder_notified_diag_test.go 的注释），所以读回来确认一次。
	//
	// 代价是每次诊断多一个 round trip；收益是打错库时**立刻**报出
	// schema 名，而不是扫到空集后输出误导性结论。
	var resolvedSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolvedSchema != schema {
		t.Fatalf("search_path 未生效：期望 %q，连接实际落在 %q。**拒绝继续**"+
			"——本诊断的全部价值在于「和实现看到同一批数据」，"+
			"打到别的库会输出误导性结论。", schema, resolvedSchema)
	}
	t.Logf("search_path verified: current_schema() = %q", resolvedSchema)
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

// TestDiagRealInvoiceExtractionBlastRadius —— 把真实库**全部**邮件灌进当前抽取器，
// 量化「被抽成发票」的数量与金额合计，并按金额降序列出。
//
// 上一条用例只证明了「工商银行信用卡对账单被抽成发票、金额取到信用额度
// 58,000.00」这一例。关键问题不是这一张，而是**有多少张**：若只有 1 张，
// 是个案；若成片出现，需求 3 的「汇总金额」在真实数据上就不可信。
//
// 只读。不写库。
func TestDiagRealInvoiceExtractionBlastRadius(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; 跳过真实发票抽取影响面统计")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	ctx := context.Background()
	cfg, perr := pgxpool.ParseConfig(dsn)
	if perr != nil {
		t.Fatalf("parse dsn: %v", perr)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, qerr := pool.Query(ctx, `
		SELECT id, COALESCE(subject,''), COALESCE(snippet,''),
		       COALESCE(from_address,''), COALESCE(from_name,'')
		FROM emails
		WHERE COALESCE(deleted_at,0)=0
		ORDER BY date DESC`)
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	defer rows.Close()

	type rec struct {
		id, subj, seller, cur, kind, date string
		amt                                float64
	}
	var total, hit int
	var sum float64
	var all []rec
	for rows.Next() {
		total++
		var e Email
		if serr := rows.Scan(&e.ID, &e.Subject, &e.Snippet, &e.FromAddress, &e.FromName); serr != nil {
			t.Fatalf("scan: %v", serr)
		}
		inv, ok := ExtractInvoiceLoose(e, e.Snippet, false)
		if !ok {
			continue
		}
		hit++
		sum += inv.Amount
		all = append(all, rec{e.ID, e.Subject, inv.Seller, inv.Currency, inv.Kind, inv.InvoiceDate, inv.Amount})
	}
	if rerr := rows.Err(); rerr != nil {
		t.Fatalf("rows: %v", rerr)
	}

	// 降序：谁在撑大合计，一眼就能看出来。
	sort.Slice(all, func(i, j int) bool { return all[i].amt > all[j].amt })

	t.Logf("=== 影响面 ===")
	t.Logf("真实邮件总数        : %d", total)
	t.Logf("被抽成「发票」的邮件 : %d", hit)
	t.Logf("这些发票的金额合计  : %.2f", sum)
	t.Logf("")
	t.Logf("=== 按金额降序（前 15 条） ===")
	for i := 0; i < len(all) && i < 15; i++ {
		r := all[i]
		t.Logf("%12.2f %-4s kind=%-9s date=%-10s seller=%q",
			r.amt, r.cur, r.kind, emptyAsDash(r.date), r.seller)
		t.Logf("    主题: %s", emptyAsDash(r.subj))
	}
	if len(all) > 15 {
		t.Logf("（其余 %d 条略）", len(all)-15)
	}
}
