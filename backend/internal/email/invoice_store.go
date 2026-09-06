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
	status TEXT NOT NULL DEFAULT 'new' CHECK(status IN ('new','pending','downloaded','failed','filed')),
	extracted_by TEXT NOT NULL DEFAULT 'rule',
	created_at BIGINT NOT NULL,
	updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_invoices_ws ON email_invoices(workspace_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_email_invoices_status ON email_invoices(workspace_id, status);
`)
	if err != nil {
		return err
	}
	// —— 发票文件采集列（Harvest 流水线）。旧库幂等补列。——
	// status 约束从 ('new','filed') 扩到含 pending/downloaded/failed：
	// pending=等下一轮重试下载（部分平台要多次操作才拿得到文件），
	// downloaded=文件已落盘，failed=重试次数耗尽。
	_, err = s.pool.Exec(ctx, `
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS file_name TEXT NOT NULL DEFAULT '';
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS file_source TEXT NOT NULL DEFAULT '';
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS exported_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE email_invoices ADD COLUMN IF NOT EXISTS feishu_sent_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE email_invoices DROP CONSTRAINT IF EXISTS email_invoices_status_check;
ALTER TABLE email_invoices ADD CONSTRAINT email_invoices_status_check
	CHECK (status IN ('new','pending','downloaded','failed','filed'));
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
	// 存储层兜底：调用方未赋 Status 时落 new，避免违反 status check 约束
	//（该约束违规会让整条提取静默失败）。
	if inv.Status == "" {
		inv.Status = "new"
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
	amount, currency, invoice_no, invoice_date, subject, status, extracted_by, created_at, updated_at,
	file_name, file_path, file_source, attempts, last_error, exported_at, feishu_sent_at`

func scanInvoice(row pgx.Row) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(
		&inv.ID, &inv.EmailID, &inv.AccountID, &inv.WorkspaceID, &inv.UserID,
		&inv.Kind, &inv.Category, &inv.Title, &inv.Seller,
		&inv.Amount, &inv.Currency, &inv.InvoiceNo, &inv.InvoiceDate, &inv.Subject,
		&inv.Status, &inv.ExtractedBy, &inv.CreatedAt, &inv.UpdatedAt,
		&inv.FileName, &inv.FilePath, &inv.FileSource, &inv.Attempts, &inv.LastError,
		&inv.ExportedAt, &inv.FeishuSentAt,
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

// SetInvoiceStatusScoped 更新发票状态（new → pending → downloaded → filed，
// 失败路径 pending/failed；取值与 email_invoices 的 status check 约束一致）。
func (s *Store) SetInvoiceStatusScoped(ctx context.Context, id, userID, workspaceID, status string) error {
	switch status {
	case "new", "pending", "downloaded", "failed", "filed":
	default:
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

// ListHarvestableInvoices 列出需要采集发票文件的记录（status IN new/pending，
// 即尚未落盘或上一轮下载失败待重试的），供 InvoiceHarvester 消费。
// limit<=0 时默认 100。
func (s *Store) ListHarvestableInvoices(ctx context.Context, limit int) ([]Invoice, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+invoiceSelectCols+` FROM email_invoices WHERE status IN ('new','pending') ORDER BY created_at LIMIT $1`, limit)
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

// ListInvoicesByIDScoped 按 id 集合批量取发票（workspace 隔离），用于导出/推送。
func (s *Store) ListInvoicesByIDScoped(ctx context.Context, ids []string, userID, workspaceID string) ([]Invoice, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+invoiceSelectCols+` FROM email_invoices
		 WHERE workspace_id=$1 AND user_id=$2 AND id = ANY($3)`,
		workspaceID, userID, ids)
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

// UpdateInvoiceHarvest 写回 Harvest 流水线对一条发票的采集结果：文件字段、
// 状态、尝试次数与最近错误。一次 UPDATE 落库，不改变 created_at。
func (s *Store) UpdateInvoiceHarvest(ctx context.Context, inv *Invoice) error {
	if inv == nil || inv.ID == "" {
		return fmt.Errorf("email: invoice id required")
	}
	inv.UpdatedAt = time.Now().Unix()
	tag, err := s.pool.Exec(ctx, `
UPDATE email_invoices SET
	status=$1, file_name=$2, file_path=$3, file_source=$4,
	attempts=$5, last_error=$6, updated_at=$7
WHERE id=$8`,
		inv.Status, inv.FileName, inv.FilePath, inv.FileSource,
		inv.Attempts, inv.LastError, inv.UpdatedAt, inv.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkInvoicesFeishuSent 批量标记发票已推送飞书（workspace 隔离）。
func (s *Store) MarkInvoicesFeishuSent(ctx context.Context, ids []string, userID, workspaceID string, sentAt int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE email_invoices SET feishu_sent_at=$1, updated_at=$1
		 WHERE workspace_id=$2 AND user_id=$3 AND id = ANY($4)`,
		sentAt, workspaceID, userID, ids)
	return err
}

// MarkInvoiceExported 记录单张发票进入 A4 网格导出的时间（workspace 隔离）。
func (s *Store) MarkInvoiceExported(ctx context.Context, id, userID, workspaceID string, exportedAt int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE email_invoices SET exported_at=$1, updated_at=$1
		 WHERE id=$2 AND workspace_id=$3 AND user_id=$4`,
		exportedAt, id, workspaceID, userID)
	return err
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

// ListPOP3SeenUIDLs 返回某账户之前已拉过的 UIDL 集合。
func (s *Store) ListPOP3SeenUIDLs(ctx context.Context, accountID string) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `SELECT uidl FROM email_pop3_seen WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var uidl string
		if err := rows.Scan(&uidl); err != nil {
			return nil, err
		}
		out[uidl] = struct{}{}
	}
	return out, rows.Err()
}

// MarkPOP3UIDLSeen 把一批 UIDL 写入已读集合，幂等（ON CONFLICT DO NOTHING）。
func (s *Store) MarkPOP3UIDLSeen(ctx context.Context, accountID string, uidls []string, seenAt int64) error {
	if len(uidls) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, u := range uidls {
		if _, err := tx.Exec(ctx,
			`INSERT INTO email_pop3_seen (account_id, uidl, seen_at) VALUES ($1, $2, $3)
			 ON CONFLICT (account_id, uidl) DO NOTHING`,
			accountID, u, seenAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
