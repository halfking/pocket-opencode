package email

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed persistence for the email assistant
// (accounts, emails, daily summaries). Migrated from SQLite in Phase 0.
//
// AI classification and daily summarization are delegated to the kxmemory
// FastAPI service — pocketd only persists and schedules. See
// docs/2026-07-02-email-assistant-design.md.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("email migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS email_accounts (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		display_name TEXT NOT NULL,
		email_address TEXT NOT NULL,
		imap_host TEXT NOT NULL,
		imap_port INTEGER DEFAULT 993,
		auth_type TEXT DEFAULT 'password' CHECK(auth_type IN ('password','oauth2')),
		credential_encrypted TEXT NOT NULL,
		sync_interval_min INTEGER DEFAULT 15,
		last_synced_uid BIGINT,
		last_synced_at BIGINT,
		rules TEXT,
		enabled BOOLEAN DEFAULT TRUE,
		created_at BIGINT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS emails (
		id TEXT PRIMARY KEY,
		account_id TEXT NOT NULL,
		message_id TEXT,
		uid BIGINT,
		from_address TEXT NOT NULL,
		from_name TEXT,
		to_addresses TEXT,
		subject TEXT,
		snippet TEXT,
		body_path TEXT,
		has_attachments BOOLEAN DEFAULT FALSE,
		attachments TEXT,
		date BIGINT NOT NULL,
		is_read BOOLEAN DEFAULT FALSE,
		is_starred BOOLEAN DEFAULT FALSE,
		category TEXT,
		importance TEXT,
		ai_summary TEXT,
		suggested_action TEXT,
		action_reason TEXT,
		processed_at BIGINT,
		created_at BIGINT NOT NULL,
		UNIQUE(account_id, message_id),
		FOREIGN KEY (account_id) REFERENCES email_accounts(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_emails_date ON emails(date DESC);
	CREATE INDEX IF NOT EXISTS idx_emails_category ON emails(category);
	CREATE INDEX IF NOT EXISTS idx_emails_importance ON emails(importance);
	CREATE INDEX IF NOT EXISTS idx_emails_unread ON emails(is_read) WHERE is_read = FALSE;

	CREATE TABLE IF NOT EXISTS daily_summaries (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		summary_date DATE NOT NULL,
		total_count INTEGER,
		important_count INTEGER,
		content TEXT NOT NULL,
		action_items TEXT,
		created_at BIGINT NOT NULL,
		UNIQUE(user_id, summary_date)
	);
	CREATE INDEX IF NOT EXISTS idx_daily_summaries_user ON daily_summaries(user_id);
	`)
	return err
}

// --- Accounts ---

func (s *Store) ListAccounts(ctx context.Context, userID string) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, display_name, email_address, imap_host, imap_port, auth_type, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE user_id = $1 ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var lastUID, lastAt sql.NullInt64
		var rules sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.DisplayName, &a.EmailAddress, &a.IMAPHost, &a.IMAPPort, &a.AuthType, &a.SyncIntervalMin, &lastUID, &lastAt, &rules, &a.Enabled, &a.CreatedAt); err != nil {
			return nil, err
		}
		if lastUID.Valid {
			a.LastSyncedUID = lastUID.Int64
		}
		if lastAt.Valid {
			a.LastSyncedAt = lastAt.Int64
		}
		if rules.Valid {
			a.Rules = rules.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Emails ---

func (s *Store) ListEmails(ctx context.Context, filter ListFilter) ([]Email, error) {
	q := `SELECT id, account_id, from_address, from_name, subject, snippet, date, is_read, is_starred, category, importance, ai_summary, suggested_action, has_attachments FROM emails`
	where := []string{}
	args := []any{}
	argIdx := 1
	addWhere := func(clause string, val any) {
		where = append(where, fmt.Sprintf("%s $%d", clause, argIdx))
		args = append(args, val)
		argIdx++
	}
	if filter.AccountID != "" {
		addWhere("account_id =", filter.AccountID)
	}
	if filter.Category != "" {
		addWhere("category =", filter.Category)
	}
	if filter.Importance != "" {
		addWhere("importance =", filter.Importance)
	}
	if filter.UnreadOnly {
		where = append(where, "is_read = FALSE")
	}
	if len(where) > 0 {
		q += " WHERE " + joinStr(where, " AND ")
	}
	q += " ORDER BY date DESC LIMIT 100"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Email
	for rows.Next() {
		var e Email
		// Several columns are nullable in the actual schema (from_name,
		// subject, snippet, category, importance, ai_summary,
		// suggested_action). Use sql.NullString to scan them and copy
		// into the in-Go struct when present.
		var fromName, subject, snippet, category, importance, aiSummary, suggestedAction sql.NullString
		if err := rows.Scan(&e.ID, &e.AccountID, &e.FromAddress, &fromName, &subject, &snippet, &e.Date, &e.IsRead, &e.IsStarred, &category, &importance, &aiSummary, &suggestedAction, &e.HasAttachments); err != nil {
			return nil, err
		}
		if fromName.Valid {
			e.FromName = fromName.String
		}
		if subject.Valid {
			e.Subject = subject.String
		}
		if snippet.Valid {
			e.Snippet = snippet.String
		}
		if category.Valid {
			e.Category = category.String
		}
		if importance.Valid {
			e.Importance = importance.String
		}
		if aiSummary.Valid {
			e.AISummary = aiSummary.String
		}
		if suggestedAction.Valid {
			e.SuggestedAction = suggestedAction.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) MarkRead(ctx context.Context, id string, read bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE emails SET is_read = $1 WHERE id = $2`, read, id)
	return err
}

// SetClassification updates AI-generated classification fields for an email.
func (s *Store) SetClassification(ctx context.Context, id, category, importance, aiSummary, suggestedAction string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE emails SET category = $1, importance = $2, ai_summary = $3, suggested_action = $4 WHERE id = $5`,
		category, importance, aiSummary, suggestedAction, id)
	return err
}

// InsertEmail inserts a fetched email (IMAP sync). Returns error on conflict (duplicate).
func (s *Store) InsertEmail(ctx context.Context, e Email) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO emails (id, account_id, from_address, from_name, subject, snippet, date, is_read, is_starred, category, importance, ai_summary, suggested_action, has_attachments)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (account_id, subject, date) DO NOTHING`,
		e.ID, e.AccountID, e.FromAddress, e.FromName, e.Subject, e.Snippet, e.Date,
		e.IsRead, e.IsStarred, e.Category, e.Importance, e.AISummary, e.SuggestedAction, e.HasAttachments)
	return err
}

func (s *Store) Close() error { return nil }

// --- Daily summaries ---

// GetSummaryByDate fetches the user's daily summary for the given date
// (YYYY-MM-DD). Returns (nil, nil) if no summary exists for that date so
// callers can map that to a 404.
func (s *Store) GetSummaryByDate(ctx context.Context, userID, date string) (*DailySummary, error) {
	var out DailySummary
	var summaryDate time.Time
	var actionItems sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, summary_date, total_count, important_count, content, action_items, created_at
		FROM daily_summaries
		WHERE user_id = $1 AND summary_date = $2::DATE
		LIMIT 1
	`, userID, date).Scan(
		&out.ID, &out.UserID, &summaryDate, &out.TotalCount, &out.ImportantCount,
		&out.Content, &actionItems, &out.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.SummaryDate = summaryDate.Format("2006-01-02")
	if actionItems.Valid {
		out.ActionItems = actionItems.String
	}
	return &out, nil
}

// UpsertSummary inserts or replaces a daily summary for (user_id, summary_date).
// Intended for the future email scheduler / kxmemory daily-summary writer;
// not used by Phase 0 handlers but kept on the store so callers don't bypass it.
func (s *Store) UpsertSummary(ctx context.Context, sum *DailySummary) error {
	if sum.ID == "" {
		sum.ID = randomID("summary")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO daily_summaries
			(id, user_id, summary_date, total_count, important_count, content, action_items, created_at)
		VALUES ($1, $2, $3::DATE, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, summary_date) DO UPDATE SET
			total_count     = EXCLUDED.total_count,
			important_count = EXCLUDED.important_count,
			content         = EXCLUDED.content,
			action_items    = EXCLUDED.action_items,
			created_at      = EXCLUDED.created_at
	`, sum.ID, sum.UserID, sum.SummaryDate, sum.TotalCount, sum.ImportantCount,
		sum.Content, sum.ActionItems, sum.CreatedAt)
	return err
}

// GetSyncStatus returns per-account sync state for the front-end status panel.
// pendingCount is the unread-email count for that account (used as a rough
// proxy for "how much is queued to sync" — Phase 0 keeps it simple).
func (s *Store) GetSyncStatus(ctx context.Context, userID string) ([]AccountSyncStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.display_name, a.email_address,
		       a.last_synced_uid, a.last_synced_at, a.enabled,
		       COALESCE((SELECT COUNT(*) FROM emails e
		                 WHERE e.account_id = a.id AND e.is_read = FALSE), 0)
		FROM email_accounts a
		WHERE a.user_id = $1
		ORDER BY a.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AccountSyncStatus
	for rows.Next() {
		var s AccountSyncStatus
		var lastUID, lastAt sql.NullInt64
		if err := rows.Scan(&s.AccountID, &s.DisplayName, &s.EmailAddress,
			&lastUID, &lastAt, &s.Enabled, &s.PendingCount); err != nil {
			return nil, err
		}
		if lastUID.Valid {
			s.LastSyncedUID = lastUID.Int64
		}
		if lastAt.Valid {
			s.LastSyncedAt = lastAt.Int64
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// randomID is a tiny ID helper local to the email package (mirrors
// server_assistant.randomID but avoids a cross-package import).
func randomID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func joinStr(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
