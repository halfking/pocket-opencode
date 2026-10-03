package email

// ledger_realdata_diag_test.go — 真库上的台账合计口径诊断（2026-10-01）。
//
// 这个文件不是回归测试，是**核对**：收紧合计口径（只统计「状态已下载/已归档
// 且 FilePath 非空」）之后，真实库里的那张台账数字到底变没变。
//
// 为什么必须真库核对、不能靠推理：修复前的口径是无条件 `total += inv.Amount`。
// 我此前判断「两种口径在真实数据下结果相同」（3 张发票：failed 0 + failed 0
// + downloaded 3500 = 3500），但那是**推算**出来的。如果存量数据里其实有
// 某张 failed/pending 带着非零金额，口径收紧就会让线上台账的合计**当场变小**
// —— 那是对用户可见的行为变化，必须明说，而不是埋在提交信息里。
//
// 跑法：
//
//	POCKET_REAL_MAIL_DSN='postgresql://...' go test ./internal/email/ -run TestDiagnoseLedgerTotalOnRealData -v
//
// 不设 DSN 就跳过，不影响常规测试。

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagnoseLedgerTotalOnRealData(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; point it at the real schema to run this diagnostic")
	}
	ctx := context.Background()
	// 必须显式设 search_path：真实数据在 opencode_pocket schema，而 DSN 里的
	// 默认 search_path 是 public。少了这一步查询会**成功**并返回空结果 ——
	// 诊断跑出「一张发票都没有」看起来像数据没了，实际是查错了 schema。
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect real schema: %v", err)
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
	// 逐行读出真实发票，保留 file_path 以便区分两种口径。
	// currency 必须一起读出来：合并后 LedgerRows 的第二个返回值**按币种分组**
	// （本分支的 edff8086 改的，原先是标量 float64 总额），不分组就比不了。
	rows, err := pool.Query(ctx, `
SELECT status, COALESCE(file_path, ''), COALESCE(amount, 0), COALESCE(currency, '')
FROM email_invoices
ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query invoices: %v", err)
	}
	defer rows.Close()

	var all []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.Status, &inv.FilePath, &inv.Amount, &inv.Currency); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all = append(all, inv)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("真实库里一张发票都没有 —— 核对不了口径，请在有数据时再跑")
	}

	// 旧口径：无条件累加。同样按币种分组，否则跨币种相加不是金额，
	// 拿它跟新口径比就没有意义了。
	oldByCur := map[string]float64{}
	var order []string
	for _, inv := range all {
		cur := currencyOrDefault(inv.Currency)
		if _, seen := oldByCur[cur]; !seen {
			order = append(order, cur)
		}
		oldByCur[cur] += inv.Amount
	}
	// 新口径：与 LedgerRows / WriteInvoiceSummaryDocs / handleEmailInvoiceSummary 一致。
	_, newTotals := LedgerRows(all)
	newByCur := map[string]float64{}
	for _, ct := range newTotals {
		newByCur[ct.Currency] = ct.Amount
	}

	t.Logf("真实发票 %d 张：", len(all))
	for _, inv := range all {
		counted := (inv.Status == "downloaded" || inv.Status == "filed") && inv.FilePath != ""
		t.Logf("  status=%-10s amount=%8.2f cur=%-4s file=%-5v 计入合计=%v",
			inv.Status, inv.Amount, currencyOrDefault(inv.Currency), inv.FilePath != "", counted)
	}
	same := true
	for _, cur := range order {
		o, n := oldByCur[cur], newByCur[cur]
		t.Logf("币种 %s：旧口径（无条件累加）= %.2f  新口径（只计已下载且已落盘）= %.2f", cur, o, n)
		if round2(o) != round2(n) {
			same = false
			t.Logf("注意：%s 的两个口径在真实数据上**不一致**，合计会从 %.2f 变成 %.2f —— "+
				"这是对用户可见的变化，必须在发布说明里写清楚", cur, o, n)
		}
	}
	if same {
		t.Logf("两个口径在真实数据上一致：这次修复不改变线上台账数字，" +
			"只是把「将来 failed 发票带脏金额时会被静默算进去」这个隐患堵上")
	}
}
