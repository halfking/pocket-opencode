package email

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) migrateInboxPurge(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS deleted_at BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS body_purged BOOLEAN NOT NULL DEFAULT FALSE;
		CREATE INDEX IF NOT EXISTS idx_emails_alive ON emails(date DESC) WHERE deleted_at = 0;
	`)
	return err
}

type ClassifyItem struct {
	ID          string
	Subject     string
	Snippet     string
	FromAddress string
	FromName    string
}

func (s *Store) ListUnclassifiedScoped(ctx context.Context, userID, workspaceID string, limit int) ([]ClassifyItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, COALESCE(e.subject,''), COALESCE(e.snippet,''), e.from_address, COALESCE(e.from_name,'')
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE a.user_id=$1 AND a.workspace_id=$2
		  AND COALESCE(e.deleted_at, 0)=0
		  AND (e.category IS NULL OR e.category='')
		ORDER BY e.date DESC
		LIMIT $3
	`, userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ClassifyItem, 0)
	for rows.Next() {
		var it ClassifyItem
		if err := rows.Scan(&it.ID, &it.Subject, &it.Snippet, &it.FromAddress, &it.FromName); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CountUnclassifiedScoped(ctx context.Context, userID, workspaceID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE a.user_id=$1 AND a.workspace_id=$2
		  AND COALESCE(e.deleted_at, 0)=0
		  AND (e.category IS NULL OR e.category='')
	`, userID, workspaceID).Scan(&n)
	return n, err
}

func (s *Store) IsEmailBodyPurged(ctx context.Context, id, userID, workspaceID string) (bool, error) {
	var purged bool
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(e.body_purged, FALSE)
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE e.id=$1 AND a.user_id=$2 AND a.workspace_id=$3
	`, id, userID, workspaceID).Scan(&purged)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return purged, err
}

func (s *Store) SoftDeleteEmailsScoped(ctx context.Context, ids []string, userID, workspaceID string, now int64) (int64, []string, error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}
	if now <= 0 {
		now = time.Now().UnixMilli()
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, COALESCE(e.body_path,'')
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE a.user_id=$1 AND a.workspace_id=$2 AND e.id = ANY($3)
	`, userID, workspaceID, ids)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return 0, nil, err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE emails e
		SET deleted_at = $4,
		    body_purged = TRUE,
		    snippet = '',
		    body_path = NULL,
		    ai_summary = CASE
		      WHEN COALESCE(NULLIF(e.ai_summary, ''), '') <> '' THEN e.ai_summary
		      ELSE COALESCE(e.snippet, '')
		    END
		FROM email_accounts a
		WHERE e.account_id = a.id
		  AND a.user_id = $1 AND a.workspace_id = $2
		  AND e.id = ANY($3)
	`, userID, workspaceID, ids, now)
	if err != nil {
		return 0, nil, err
	}
	return tag.RowsAffected(), paths, nil
}
