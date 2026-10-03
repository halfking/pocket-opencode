package email

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InvoiceListPage 是按来源邮件收到时间倒排的一页发票。
type InvoiceListPage struct {
	Invoices []Invoice
	HasMore  bool
	Total    int
	Filed    int
	// Amount 是**单一币种**时的合计额，供前端直接显示。
	//
	// 混入多种币种时它是 0，且 Amounts 非空——跨币种的算术和不是金额，
	// 给一个「看起来正常」的标量会直接误导（前端会把它渲染成 ¥）。
	// 调用方应当优先读 Amounts；只有在 len(Amounts) <= 1 时才用 Amount。
	Amount   float64
	Amounts  []CurrencyTotal
	// Currency 是 Amount 对应的币种（单币种时非空）。
	Currency string
}

const invoiceSelectListed = `inv.id, inv.email_id, inv.account_id, inv.workspace_id, inv.user_id, inv.kind, inv.category, inv.title, inv.seller,
	inv.amount, inv.currency, inv.invoice_no, inv.invoice_date, inv.subject, inv.status, inv.extracted_by, inv.created_at, inv.updated_at,
	inv.file_name, inv.file_path, inv.file_source, inv.attempts, inv.last_error, inv.exported_at, inv.feishu_sent_at,
	COALESCE(e.date, 0)`

func scanListedInvoice(row pgx.Row) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(
		&inv.ID, &inv.EmailID, &inv.AccountID, &inv.WorkspaceID, &inv.UserID,
		&inv.Kind, &inv.Category, &inv.Title, &inv.Seller,
		&inv.Amount, &inv.Currency, &inv.InvoiceNo, &inv.InvoiceDate, &inv.Subject,
		&inv.Status, &inv.ExtractedBy, &inv.CreatedAt, &inv.UpdatedAt,
		&inv.FileName, &inv.FilePath, &inv.FileSource, &inv.Attempts, &inv.LastError,
		&inv.ExportedAt, &inv.FeishuSentAt, &inv.EmailDate,
	)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// ListInvoicesPage 按 emails.date（无邮件则 created_at）倒排分页。
// 多取 1 条判断 hasMore；limit<=0 默认 30，上限 500。
func (s *Store) ListInvoicesPage(ctx context.Context, userID, workspaceID, status string, limit, offset int) (InvoiceListPage, error) {
	var page InvoiceListPage
	if workspaceID == "" {
		return page, fmt.Errorf("email: workspace_id required")
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	stats, err := s.InvoiceListStats(ctx, userID, workspaceID, "")
	if err != nil {
		return page, err
	}
	page.Total, page.Filed = stats.Total, stats.Filed
	page.Amount, page.Amounts, page.Currency = stats.Amount, stats.Amounts, stats.Currency

	q := `SELECT ` + invoiceSelectListed + `
FROM email_invoices inv
LEFT JOIN emails e ON e.id = inv.email_id
WHERE inv.workspace_id=$1 AND inv.user_id=$2`
	args := []any{workspaceID, userID}
	if status != "" {
		q += ` AND inv.status=$3`
		args = append(args, status)
	}
	q += ` ORDER BY COALESCE(e.date, inv.created_at) DESC, inv.id DESC LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	args = append(args, limit+1, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		inv, serr := scanListedInvoice(rows)
		if serr != nil {
			return page, serr
		}
		out = append(out, *inv)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(out) > limit {
		page.HasMore = true
		out = out[:limit]
	}
	page.Invoices = out
	return page, nil
}

type invoiceListStats struct {
	Total    int
	Filed    int
	Amount   float64
	Amounts  []CurrencyTotal
	Currency string
}

// InvoiceListStats 全量汇总（不受分页截断）。status 空 = 全部。
//
// 合计**按币种分组**（2026-10-01 补）。此前这里是裸 `SUM(amount)`，把 USD
// 与 CNY 直接相加后交给前端渲染成「¥xxx」——需求 3 要的是「汇总金额」，
// 而跨币种的算术和不是金额。这是同一规则的**第三处实现**（前两处：
// LedgerRows、WriteInvoiceSummaryDocs），此处此前漏网。
func (s *Store) InvoiceListStats(ctx context.Context, userID, workspaceID, status string) (invoiceListStats, error) {
	var st invoiceListStats
	q := `SELECT COUNT(*),
		COUNT(*) FILTER (WHERE status='filed')
		FROM email_invoices WHERE workspace_id=$1 AND user_id=$2`
	args := []any{workspaceID, userID}
	if status != "" {
		q += ` AND status=$3`
		args = append(args, status)
	}
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&st.Total, &st.Filed); err != nil {
		return st, err
	}

	// 逐币种合计。空币种归 CNY，与 currencyOrDefault 同源。
	//
	// 2026-10-02 修正：这里此前**完全没有过滤**，把整张表按 status 之外的
	// 口径求和。真实库 2 行发票（3500 downloaded+有文件、58000 new+无文件），
	// 这条 SQL 返回 CNY 61,500，而 LedgerRows / WriteInvoiceSummaryDocs
	// 返回 CNY 3,500 —— 同一个「汇总金额」需求，两个 17.6 倍差的结果。
	// 发票页显示的是这条（前端 resolveSummaryGroups 优先读 API 的 amounts），
	// 飞书台账显示的是另一条。
	//
	// 过滤条件与 InvoiceCountsTowardTotal 逐项对应：
	// status IN ('downloaded','filed') AND file_path 非空。
	// 这段 SQL 与那个 Go 函数是同一条规则的两种写法，无法真正共用一份代码；
	// 能做的是让 invoice_list_stats_guard_test.go 用同一组夹具同时跑两者，
	// 任何一侧漂移都会红。**不要再把它改回无过滤。**
	sumQ := `SELECT COALESCE(NULLIF(currency, ''), 'CNY'),
			COALESCE(SUM(ROUND(amount::numeric, 2)), 0),
			COUNT(*)
		FROM email_invoices
		WHERE workspace_id=$1 AND user_id=$2
		  AND status IN ('downloaded','filed')
		  AND COALESCE(file_path, '') <> ''`
	sumArgs := []any{workspaceID, userID}
	if status != "" {
		sumQ += ` AND status=$3`
		sumArgs = append(sumArgs, status)
	}
	sumQ += ` GROUP BY 1 ORDER BY 1`
	rows, err := s.pool.Query(ctx, sumQ, sumArgs...)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var cur string
		var amt float64
		var n int
		if err := rows.Scan(&cur, &amt, &n); err != nil {
			return st, err
		}
		st.Amounts = append(st.Amounts, CurrencyTotal{Currency: cur, Amount: round2(amt), Count: n})
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	// 单一币种时保留旧的标量 Amount，既有前端代码不改也能继续工作；
	// 多币种时 Amount 保持 0，调用方必须读 Amounts。
	if len(st.Amounts) == 1 {
		st.Amount = st.Amounts[0].Amount
		st.Currency = st.Amounts[0].Currency
	}
	return st, nil
}
// attachEmailDates 批量补来源邮件收到时间（Unix 秒）。
func (s *Store) attachEmailDates(ctx context.Context, invoices []Invoice) error {
	if len(invoices) == 0 {
		return nil
	}
	ids := make([]string, 0, len(invoices))
	seen := map[string]struct{}{}
	for _, inv := range invoices {
		if inv.EmailID == "" {
			continue
		}
		if _, ok := seen[inv.EmailID]; ok {
			continue
		}
		seen[inv.EmailID] = struct{}{}
		ids = append(ids, inv.EmailID)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, date FROM emails WHERE id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	dates := map[string]int64{}
	for rows.Next() {
		var id string
		var date int64
		if err := rows.Scan(&id, &date); err != nil {
			return err
		}
		dates[id] = date
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range invoices {
		invoices[i].EmailDate = dates[invoices[i].EmailID]
	}
	return nil
}
