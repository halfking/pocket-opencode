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
	Amount   float64
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
	page.Total, page.Filed, page.Amount = stats.Total, stats.Filed, stats.Amount

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
	Total  int
	Filed  int
	Amount float64
}

// InvoiceListStats 全量汇总（不受分页截断）。status 空 = 全部。
func (s *Store) InvoiceListStats(ctx context.Context, userID, workspaceID, status string) (invoiceListStats, error) {
	var st invoiceListStats
	q := `SELECT COUNT(*),
		COUNT(*) FILTER (WHERE status='filed'),
		COALESCE(SUM(amount), 0)
	FROM email_invoices WHERE workspace_id=$1 AND user_id=$2`
	args := []any{workspaceID, userID}
	if status != "" {
		q += ` AND status=$3`
		args = append(args, status)
	}
	err := s.pool.QueryRow(ctx, q, args...).Scan(&st.Total, &st.Filed, &st.Amount)
	return st, err
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
