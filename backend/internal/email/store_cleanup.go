package email

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// store_cleanup.go — 用户批量清垃圾的 scoped 读写。
// 列序与 $n 必须与 emails / email_accounts 现有 DDL 对齐（rule 49/57）。
// 不改表结构。DELETE 走 emails.id + 账户作用域；email_invoices /
// vacation_deliveries 有 ON DELETE CASCADE。

const cleanupEmailCols = `e.id, e.account_id, e.uid, e.from_address, e.from_name, e.subject, e.date`

func scanCleanupItem(row interface{ Scan(dest ...any) error }) (CleanupItem, error) {
	var it CleanupItem
	var uid sql.NullInt64
	var fromName, subject sql.NullString
	err := row.Scan(&it.ID, &it.AccountID, &uid, &it.From, &fromName, &subject, &it.Date)
	if err != nil {
		return it, err
	}
	if uid.Valid {
		it.UID = uid.Int64
	}
	if fromName.Valid && fromName.String != "" {
		it.From = fromName.String + " <" + it.From + ">"
	}
	if subject.Valid {
		it.Subject = subject.String
	}
	return it, nil
}

// ListEmailsForCleanupScoped 按主题/来源/日期列出待清理邮件（含 UID）。
func (s *Store) ListEmailsForCleanupScoped(ctx context.Context, f CleanupFilter, userID, workspaceID string) ([]CleanupItem, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	q := `SELECT ` + cleanupEmailCols + `
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE a.user_id=$1 AND a.workspace_id=$2`
	args := []any{userID, workspaceID}
	if f.AccountID != "" {
		q += fmt.Sprintf(" AND e.account_id=$%d", len(args)+1)
		args = append(args, f.AccountID)
	}
	if sub := strings.TrimSpace(f.Subject); sub != "" {
		q += fmt.Sprintf(" AND strpos(lower(COALESCE(e.subject,'')), lower($%d)) > 0", len(args)+1)
		args = append(args, sub)
	}
	if src := strings.TrimSpace(f.From); src != "" {
		n := len(args) + 1
		q += fmt.Sprintf(" AND (strpos(lower(e.from_address), lower($%d)) > 0 OR strpos(lower(COALESCE(e.from_name,'')), lower($%d)) > 0)", n, n)
		args = append(args, src)
	}
	if f.Since > 0 {
		q += fmt.Sprintf(" AND e.date >= $%d", len(args)+1)
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q += fmt.Sprintf(" AND e.date <= $%d", len(args)+1)
		args = append(args, f.Until)
	}
	q += fmt.Sprintf(" ORDER BY e.date DESC LIMIT $%d", len(args)+1)
	args = append(args, f.cappedLimit())

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CleanupItem, 0)
	for rows.Next() {
		it, err := scanCleanupItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteEmailsByIDsScoped 只删当前 user/workspace 下的邮件。
func (s *Store) DeleteEmailsByIDsScoped(ctx context.Context, ids []string, userID, workspaceID string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
DELETE FROM emails e
USING email_accounts a
WHERE e.account_id = a.id
  AND a.user_id = $1
  AND a.workspace_id = $2
  AND e.id = ANY($3)
`, userID, workspaceID, ids)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
