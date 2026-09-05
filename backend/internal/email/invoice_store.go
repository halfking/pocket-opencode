package email

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 发票存储：email_invoices 表。每封邮件至多一条发票记录（email_id 幂等），
// 随 emails 删除级联清理。workspace/user 隔离与 emails 表一致。

func (s *Store) migrateInvoices(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS email_invoices (
	id TEXT PRIMARY KEY,
	email_id TEXT NOT NULL UNIQUE REFERENCES emails(id) ON DELETE CASCADE,
	account_id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT 'default',
	user_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'bill',
	category TEXT NOT NULL DEFAULT '其他',
	title TEXT NOT NULL DEFAULT '',
	seller TEXT NOT NULL DEFAULT '',
	amount NUMERIC(12,2) NOT NULL DEFAULT 0,
	currency TEXT NOT NULL DEFAULT 'CNY',
	invoice_no TEXT NOT NULL DEFAULT '',
	invoice_date TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'new' CHECK(status IN ('new','filed')),
	extracted_by TEXT NOT NULL DEFAULT 'rule',
	created_at BIGINT NOT NULL,
	updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_invoices_ws ON email_invoices(workspace_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_email_invoices_status ON email_invoices(workspace_id, status);
`)
	return err
}

func newInvoiceID() string {
	return fmt.Sprintf("inv_%d", time.Now().UnixNano())
}

// UpsertInvoice 幂等写入发票记录：同 email_id 重复提取时更新非空字段。
func (s *Store) UpsertInvoice(ctx context.Context, inv *Invoice, userID, workspaceID string) (*Invoice, error) {
	if inv == nil || inv.EmailID == "" {
		return nil, fmt.Errorf("email: invoice email_id required")
	}
	now := time.Now().Unix()
	if inv.ID == "" {
		inv.ID = newInvoiceID()
	}
	inv.UserID = userID
	inv.WorkspaceID = workspaceID
	inv.CreatedAt = now
	inv.UpdatedAt = now

	q := `
INSERT INTO email_invoices
	(id, email_id, account_id, workspace_id, user_id, kind, category, title, seller,
	 amount, currency, invoice_no, invoice_date, subject, status, extracted_by, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (email_id) DO UPDATE SET
	kind          = EXCLUDED.kind,
	category      = EXCLUDED.category,
	title         = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE email_invoices.title END,
	seller        = CASE WHEN EXCLUDED.seller <> '' THEN EXCLUDED.seller ELSE email_invoices.seller END,
	amount        = CASE WHEN EXCLUDED.amount > 0 THEN EXCLUDED.amount ELSE email_invoices.amount END,
	currency      = EXCLUDED.currency,
	invoice_no    = CASE WHEN EXCLUDED.invoice_no <> '' THEN EXCLUDED.invoice_no ELSE email_invoices.invoice_no END,
	invoice_date  = CASE WHEN EXCLUDED.invoice_date <> '' THEN EXCLUDED.invoice_date ELSE email_invoices.invoice_date END,
	subject       = EXCLUDED.subject,
	extracted_by  = EXCLUDED.extracted_by,
	updated_at    = EXCLUDED.updated_at
RETURNING id, created_at`
	err := s.pool.QueryRow(ctx, q,
		inv.ID, inv.EmailID, inv.AccountID, workspaceID, userID,
		inv.Kind, inv.Category, inv.Title, inv.Seller,
		inv.Amount, inv.Currency, inv.InvoiceNo, inv.InvoiceDate, inv.Subject,
		inv.Status, inv.ExtractedBy, inv.CreatedAt, inv.UpdatedAt,
	).Scan(&inv.ID, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("email: upsert invoice: %w", err)
	}
	return inv, nil
}

const invoiceSelectCols = `id, email_id, account_id, workspace_id, user_id, kind, category, title, seller,
	amount, currency, invoice_no, invoice_date, subject, status, extracted_by, created_at, updated_at`

func scanInvoice(row pgx.Row) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(
		&inv.ID, &inv.EmailID, &inv.AccountID, &inv.WorkspaceID, &inv.UserID,
		&inv.Kind, &inv.Category, &inv.Title, &inv.Seller,
		&inv.Amount, &inv.Currency, &inv.InvoiceNo, &inv.InvoiceDate, &inv.Subject,
		&inv.Status, &inv.ExtractedBy, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// ListInvoicesScoped 列出当前用户/工作区的发票记录，按创建时间倒序。
// status 为空时返回全部；limit<=0 时默认 200。
func (s *Store) ListInvoicesScoped(ctx context.Context, userID, workspaceID, status string, limit int) ([]Invoice, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("email: workspace_id required")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + invoiceSelectCols + ` FROM email_invoices WHERE workspace_id=$1 AND user_id=$2`
	args := []any{workspaceID, userID}
	if status != "" {
		q += ` AND status=$3`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	return out, rows.Err()
}

// GetInvoiceByIDScoped 按 id 取发票（workspace 隔离）。
func (s *Store) GetInvoiceByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Invoice, error) {
	inv, err := scanInvoice(s.pool.QueryRow(ctx,
		`SELECT `+invoiceSelectCols+` FROM email_invoices WHERE id=$1 AND workspace_id=$2 AND user_id=$3`,
		id, workspaceID, userID))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return inv, err
}

// GetInvoiceByEmailID 查某封邮件是否已提取过发票。
func (s *Store) GetInvoiceByEmailID(ctx context.Context, emailID string) (*Invoice, error) {
	inv, err := scanInvoice(s.pool.QueryRow(ctx,
		`SELECT `+invoiceSelectCols+` FROM email_invoices WHERE email_id=$1`, emailID))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return inv, err
}

// SetInvoiceStatusScoped 标记归档状态（new → filed）。
func (s *Store) SetInvoiceStatusScoped(ctx context.Context, id, userID, workspaceID, status string) error {
	if status != "new" && status != "filed" {
		return fmt.Errorf("email: invalid invoice status %q", status)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_invoices SET status=$1, updated_at=$2 WHERE id=$3 AND workspace_id=$4 AND user_id=$5`,
		status, time.Now().Unix(), id, workspaceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteInvoiceScoped 删除发票记录（不影响邮件本身）。
func (s *Store) DeleteInvoiceScoped(ctx context.Context, id, userID, workspaceID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM email_invoices WHERE id=$1 AND workspace_id=$2 AND user_id=$3`,
		id, workspaceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
