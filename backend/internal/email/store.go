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

var ErrNotFound = errors.New("email: not found")

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
	-- IMAP fallback 去重：仅当 message_id 缺失时按 (account_id, subject, date)
	-- 去重。用部分唯一索引而非全局 UNIQUE 约束，否则两封不同 message_id
	-- 但同主题同日期（如 "Daily report"、"Out of office"）的邮件会被
	-- ON CONFLICT DO NOTHING 静默丢弃，造成数据丢失。
	-- 兼容旧库：先删除第一轮审计误加的全局表级 UNIQUE 约束（约束名由
	-- PostgreSQL 自动生成）。新库此语句无副作用。
	ALTER TABLE emails DROP CONSTRAINT IF EXISTS emails_account_id_subject_date_key;
	CREATE UNIQUE INDEX IF NOT EXISTS idx_emails_subject_date
		ON emails(account_id, subject, date) WHERE message_id IS NULL;
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
	-- S0-A: workspace_id isolation (idempotent).
	ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	ALTER TABLE emails ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
		ALTER TABLE daily_summaries ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
		CREATE TABLE IF NOT EXISTS email_oauth_tokens (
			account_id TEXT PRIMARY KEY REFERENCES email_accounts(id) ON DELETE CASCADE,
			refresh_token_encrypted TEXT NOT NULL,
			access_token_encrypted TEXT,
			expires_at BIGINT NOT NULL DEFAULT 0,
			scope TEXT,
			updated_at BIGINT NOT NULL
		);
		ALTER TABLE email_oauth_tokens ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
		ALTER TABLE email_oauth_tokens ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS idx_email_oauth_tokens_expires ON email_oauth_tokens(expires_at);
		CREATE INDEX IF NOT EXISTS idx_email_oauth_tokens_ws ON email_oauth_tokens(workspace_id);

	CREATE INDEX IF NOT EXISTS idx_email_accounts_ws ON email_accounts(workspace_id);
	CREATE INDEX IF NOT EXISTS idx_emails_ws ON emails(workspace_id);
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

// MarkStarred 标记邮件星标状态（独立方法，方便客户端只更新一个字段）。
func (s *Store) MarkStarred(ctx context.Context, id string, starred bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE emails SET is_starred = $1 WHERE id = $2`, starred, id)
	return err
}

// GetEmailByID 获取单封邮件详情，返回 (nil, nil) 表示不存在。
//
// 用于 GET /api/emails/{id}；不在 ListEmails 的过滤条件下额外开新方法，
// 避免上层 handler 用 ListEmails + 客户端过滤这种 O(N) 写法。
func (s *Store) GetEmailByID(ctx context.Context, id string) (*Email, error) {
	var e Email
	var fromName, subject, snippet, category, importance, aiSummary, suggestedAction sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, from_address, from_name, subject, snippet, date, is_read, is_starred, category, importance, ai_summary, suggested_action, has_attachments
		FROM emails WHERE id = $1
	`, id).Scan(&e.ID, &e.AccountID, &e.FromAddress, &fromName, &subject, &snippet, &e.Date, &e.IsRead, &e.IsStarred, &category, &importance, &aiSummary, &suggestedAction, &e.HasAttachments)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
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
	return &e, nil
}

// ListSummaries 返回用户的每日邮件总结列表（按日期倒序，limit 限制）。
//
// 用于 GET /api/email/summaries；limit <= 0 或 > 200 时回退到 30。
func (s *Store) ListSummaries(ctx context.Context, userID string, limit int) ([]DailySummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, summary_date, total_count, important_count, content, action_items, created_at
		FROM daily_summaries
		WHERE user_id = $1
		ORDER BY summary_date DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DailySummary, 0)
	for rows.Next() {
		var s DailySummary
		var summaryDate time.Time
		var actionItems sql.NullString
		if err := rows.Scan(&s.ID, &s.UserID, &summaryDate, &s.TotalCount, &s.ImportantCount, &s.Content, &actionItems, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.SummaryDate = summaryDate.Format("2006-01-02")
		if actionItems.Valid {
			s.ActionItems = actionItems.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetClassification updates AI-generated classification fields for an email.
func (s *Store) SetClassification(ctx context.Context, id, category, importance, aiSummary, suggestedAction string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE emails SET category = $1, importance = $2, ai_summary = $3, suggested_action = $4 WHERE id = $5`,
		category, importance, aiSummary, suggestedAction, id)
	return err
}

// InsertEmail inserts a fetched email (IMAP sync). Returns error on conflict (duplicate).
//
// ON CONFLICT DO NOTHING 不指定冲突目标，PostgreSQL 会自动匹配任一唯一
// 约束/索引：(account_id, message_id) 全局唯一约束，或 message_id IS NULL
// 时的 (account_id, subject, date) 部分唯一索引。这样无论哪种冲突都不会
// 抛错中断同步。
func (s *Store) InsertEmail(ctx context.Context, e Email) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO emails (id, account_id, workspace_id, from_address, from_name, subject, snippet, date, is_read, is_starred, category, importance, ai_summary, suggested_action, has_attachments)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			 ON CONFLICT DO NOTHING`,
		e.ID, e.AccountID, defaultWorkspace(e.WorkspaceID), e.FromAddress, e.FromName, e.Subject, e.Snippet, e.Date,
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

// ListEmailsByDay 返回某用户指定日期（YYYY-MM-DD）所有已抓取的邮件。
//
// 给 email.Scheduler.runDailySummary 用：每日 21:00 拉当天的邮件，调
// kxmemory.DailySummary 生成总结，写回 daily_summaries 表。
//
// 性能：date 是 BIGINT（Unix 秒）所以用 `date >= start AND date < end` 范围查
// 询（避免时区问题），命中 idx_emails_date 索引。
func (s *Store) ListEmailsByDay(ctx context.Context, userID, date string, tzOffsetSec int) ([]Email, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	loc := time.FixedZone("user", tzOffsetSec)
	t = t.In(loc)
	startUnix := t.Unix()
	endUnix := t.Add(24 * time.Hour).Unix()

	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.account_id, e.from_address, e.from_name, e.subject, e.snippet, e.date, e.is_read, e.is_starred, e.category, e.importance, e.ai_summary, e.suggested_action, e.has_attachments
		FROM emails e
		JOIN email_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1 AND e.date >= $2 AND e.date < $3
		ORDER BY e.date DESC
		LIMIT 500
	`, userID, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Email
	for rows.Next() {
		var e Email
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

// --- Extended CRUD methods for PR #6 ---

// InsertAccount 插入新账户。
func (s *Store) InsertAccount(ctx context.Context, a *Account, credentialEncrypted string) error {
	_, err := s.pool.Exec(ctx, `
			INSERT INTO email_accounts
				(id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
				 credential_encrypted, sync_interval_min, rules, enabled, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		`,
		a.ID, a.UserID, defaultWorkspace(a.WorkspaceID), a.DisplayName, a.EmailAddress, a.IMAPHost, a.IMAPPort,
		a.AuthType, credentialEncrypted, a.SyncIntervalMin,
		nullStr(a.Rules), a.Enabled, a.CreatedAt)

	return err
}

// UpdateAccount 更新账户元数据（不包括 credential）。
func (s *Store) UpdateAccount(ctx context.Context, a *Account) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE email_accounts SET
			display_name = $2, imap_host = $3, imap_port = $4,
			sync_interval_min = $5, rules = $6, enabled = $7
		WHERE id = $1
	`, a.ID, a.DisplayName, a.IMAPHost, a.IMAPPort, a.SyncIntervalMin,
		nullStr(a.Rules), a.Enabled)
	return err
}

// UpdateCredential 更新加密凭证。
func (s *Store) UpdateCredential(ctx context.Context, id, credentialEncrypted string) error {
	_, err := s.pool.Exec(ctx, `UPDATE email_accounts SET credential_encrypted = $2 WHERE id = $1`, id, credentialEncrypted)
	return err
}

// DeleteAccount 删除账户。
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM email_accounts WHERE id = $1`, id)
	return err
}

// GetAccountByID 返回账户 + 加密凭证（仅供 scheduler / OAuth 使用）。
func (s *Store) GetAccountByID(ctx context.Context, id string) (*Account, string, error) {
	var a Account
	var cred string
	var lastUID, lastAt sql.NullInt64
	var rules sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, display_name, email_address, imap_host, imap_port, auth_type,
		       credential_encrypted, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE id = $1
	`, id).Scan(&a.ID, &a.UserID, &a.DisplayName, &a.EmailAddress, &a.IMAPHost, &a.IMAPPort,
		&a.AuthType, &cred, &a.SyncIntervalMin, &lastUID, &lastAt, &rules, &a.Enabled, &a.CreatedAt)
	if err != nil {
		return nil, "", err
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
	return &a, cred, nil
}

// SetAccountAuthType 更新 auth_type（OAuth 回调后使用）。
func (s *Store) SetAccountAuthType(ctx context.Context, id, authType string) error {
	_, err := s.pool.Exec(ctx, `UPDATE email_accounts SET auth_type = $2 WHERE id = $1`, id, authType)
	return err
}

// UpdateSyncState 更新最后同步的 UID 和时间。
func (s *Store) UpdateSyncState(ctx context.Context, id string, lastUID int64, lastAt int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE email_accounts SET last_synced_uid = $2, last_synced_at = $3 WHERE id = $1`, id, lastUID, lastAt)
	return err
}

// ListEnabledAccounts 返回所有启用的账户（scheduler 使用）。
func (s *Store) ListEnabledAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, display_name, email_address, imap_host, imap_port, auth_type, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE enabled = TRUE ORDER BY created_at
	`)
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

// UpsertOAuthToken 插入或更新 OAuth token。
func (s *Store) UpsertOAuthToken(ctx context.Context, accountID, refreshEnc, accessEnc string, expiresAt int64, scope string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO email_oauth_tokens
			(account_id, refresh_token_encrypted, access_token_encrypted, expires_at, scope, updated_at)
		VALUES ($1, $2, $3, $4, $5, EXTRACT(EPOCH FROM NOW())::BIGINT)
		ON CONFLICT (account_id) DO UPDATE SET
			refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
			access_token_encrypted  = EXCLUDED.access_token_encrypted,
			expires_at              = EXCLUDED.expires_at,
			scope                   = EXCLUDED.scope,
			updated_at              = EXTRACT(EPOCH FROM NOW())::BIGINT
	`, accountID, refreshEnc, accessEnc, expiresAt, scope)
	return err
}

// RevokeOAuthToken marks the account as password-backed and disabled so the
// scheduler stops trying to login with the dead token. It also clears the
// token row (best-effort: leaving it would not hurt, but clean rows make
// debugging easier).
//
// Called only after we've already validated that the failure is permanent
// (invalid_grant / revoked consent).
func (s *Store) RevokeOAuthToken(ctx context.Context, accountID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM email_oauth_tokens WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_accounts SET auth_type='password', enabled=FALSE WHERE id=$1`, accountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetOAuthToken 返回加密的 OAuth token。
func (s *Store) GetOAuthToken(ctx context.Context, accountID string) (refreshEnc, accessEnc string, expiresAt int64, scope string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT refresh_token_encrypted, COALESCE(access_token_encrypted, ''), expires_at, COALESCE(scope, '')
		FROM email_oauth_tokens WHERE account_id = $1
	`, accountID).Scan(&refreshEnc, &accessEnc, &expiresAt, &scope)
	return
}

// ListExpiredOAuthTokens returns tokens that have already expired (or are
// within `leewaySec` of expiry) so the scheduler can refresh them in batch
// before the next IMAP login attempt.
func (s *Store) ListExpiredOAuthTokens(ctx context.Context, leewaySec int64) ([]OAuthTokenRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT account_id, refresh_token_encrypted, COALESCE(access_token_encrypted, ''), expires_at
		FROM email_oauth_tokens
		WHERE expires_at > 0 AND expires_at <= (EXTRACT(EPOCH FROM NOW())::BIGINT + $1)
	`, leewaySec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OAuthTokenRow, 0)
	for rows.Next() {
		var r OAuthTokenRow
		if err := rows.Scan(&r.AccountID, &r.RefreshEnc, &r.AccessEnc, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OAuthTokenRow is the lightweight projection used by the scheduler refresh
// worker. Decryption happens off the hot path (in scheduler.refresh loop).
type OAuthTokenRow struct {
	AccountID  string
	RefreshEnc string
	AccessEnc  string
	ExpiresAt  int64
}

// ListAccountsScoped returns only accounts owned by the user in the workspace.
func (s *Store) ListAccountsScoped(ctx context.Context, userID, workspaceID string) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
		       sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE user_id = $1 AND workspace_id = $2 ORDER BY created_at
	`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Account, 0)
	for rows.Next() {
		var a Account
		var lastUID, lastAt sql.NullInt64
		var rules sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress, &a.IMAPHost,
			&a.IMAPPort, &a.AuthType, &a.SyncIntervalMin, &lastUID, &lastAt, &rules, &a.Enabled, &a.CreatedAt); err != nil {
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

// GetAccountByIDScoped returns an account and credential only when it belongs
// to the requested user/workspace. A missing or foreign account is ErrNotFound.
func (s *Store) GetAccountByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Account, string, error) {
	var a Account
	var cred string
	var lastUID, lastAt sql.NullInt64
	var rules sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
		       credential_encrypted, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE id = $1 AND user_id = $2 AND workspace_id = $3
	`, id, userID, workspaceID).Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress,
		&a.IMAPHost, &a.IMAPPort, &a.AuthType, &cred, &a.SyncIntervalMin, &lastUID, &lastAt,
		&rules, &a.Enabled, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
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
	return &a, cred, nil
}

// UpdateAccountScoped atomically updates account metadata and, when provided,
// its encrypted credential. Ownership is part of the UPDATE predicate.
func (s *Store) UpdateAccountScoped(ctx context.Context, a *Account, userID, workspaceID, credential string, updateCredential bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `UPDATE email_accounts SET display_name=$1, imap_host=$2, imap_port=$3,
		auth_type=$4, sync_interval_min=$5, rules=$6, enabled=$7 WHERE id=$8 AND user_id=$9 AND workspace_id=$10`
	args := []any{a.DisplayName, a.IMAPHost, a.IMAPPort, a.AuthType, a.SyncIntervalMin, nullStr(a.Rules), a.Enabled, a.ID, userID, workspaceID}
	if updateCredential {
		query = `UPDATE email_accounts SET display_name=$1, imap_host=$2, imap_port=$3,
			auth_type=$4, sync_interval_min=$5, rules=$6, enabled=$7, credential_encrypted=$8 WHERE id=$9 AND user_id=$10 AND workspace_id=$11`
		args = []any{a.DisplayName, a.IMAPHost, a.IMAPPort, a.AuthType, a.SyncIntervalMin, nullStr(a.Rules), a.Enabled, credential, a.ID, userID, workspaceID}
	}
	res, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// DeleteAccountScoped deletes an account only when it belongs to the scope.
func (s *Store) DeleteAccountScoped(ctx context.Context, id, userID, workspaceID string) error {
	res, err := s.pool.Exec(ctx, `DELETE FROM email_accounts WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, userID, workspaceID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEmailsScoped lists mail belonging to the requested user/workspace.
func (s *Store) ListEmailsScoped(ctx context.Context, filter ListFilter, userID, workspaceID string) ([]Email, error) {
	q := `SELECT e.id, e.account_id, e.from_address, e.from_name, e.subject, e.snippet, e.date,
		e.is_read, e.is_starred, e.category, e.importance, e.ai_summary, e.suggested_action, e.has_attachments
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE a.user_id=$1 AND a.workspace_id=$2`
	args := []any{userID, workspaceID}
	if filter.AccountID != "" {
		q += fmt.Sprintf(" AND e.account_id=$%d", len(args)+1)
		args = append(args, filter.AccountID)
	}
	if filter.Category != "" {
		q += fmt.Sprintf(" AND e.category=$%d", len(args)+1)
		args = append(args, filter.Category)
	}
	if filter.Importance != "" {
		q += fmt.Sprintf(" AND e.importance=$%d", len(args)+1)
		args = append(args, filter.Importance)
	}
	if filter.UnreadOnly {
		q += " AND e.is_read=FALSE"
	}
	q += " ORDER BY e.date DESC LIMIT 100"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Email, 0)
	for rows.Next() {
		e, err := scanEmail(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetEmailByIDScoped returns a message only within the requested scope.
func (s *Store) GetEmailByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Email, error) {
	row := s.pool.QueryRow(ctx, `SELECT e.id, e.account_id, e.from_address, e.from_name, e.subject, e.snippet,
		e.date, e.is_read, e.is_starred, e.category, e.importance, e.ai_summary, e.suggested_action, e.has_attachments
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE e.id=$1 AND a.user_id=$2 AND a.workspace_id=$3`, id, userID, workspaceID)
	e, err := scanEmail(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// UpdateEmailFlagsScoped atomically applies the supplied PATCH fields.
func (s *Store) UpdateEmailFlagsScoped(ctx context.Context, id, userID, workspaceID string, isRead, isStarred *bool) error {
	if isRead == nil && isStarred == nil {
		return fmt.Errorf("email: no flags provided")
	}
	q := "UPDATE emails e SET "
	args := []any{}
	sets := []string{}
	if isRead != nil {
		args = append(args, *isRead)
		sets = append(sets, fmt.Sprintf("is_read=$%d", len(args)))
	}
	if isStarred != nil {
		args = append(args, *isStarred)
		sets = append(sets, fmt.Sprintf("is_starred=$%d", len(args)))
	}
	args = append(args, id, userID, workspaceID)
	q += joinStr(sets, ", ") + fmt.Sprintf(" FROM email_accounts a WHERE e.account_id=a.id AND e.id=$%d AND a.user_id=$%d AND a.workspace_id=$%d", len(args)-2, len(args)-1, len(args))
	res, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanEmail(row interface{ Scan(...any) error }) (*Email, error) {
	var e Email
	var fromName, subject, snippet, category, importance, aiSummary, suggestedAction sql.NullString
	err := row.Scan(&e.ID, &e.AccountID, &e.FromAddress, &fromName, &subject, &snippet, &e.Date, &e.IsRead,
		&e.IsStarred, &category, &importance, &aiSummary, &suggestedAction, &e.HasAttachments)
	if err != nil {
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
	return &e, nil
}

// ListSummariesScoped returns summaries in one user/workspace.
func (s *Store) ListSummariesScoped(ctx context.Context, userID, workspaceID string, limit int) ([]DailySummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, workspace_id, summary_date, total_count, important_count, content, action_items, created_at
		FROM daily_summaries WHERE user_id=$1 AND workspace_id=$2 ORDER BY summary_date DESC LIMIT $3`, userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DailySummary, 0)
	for rows.Next() {
		var d DailySummary
		var date time.Time
		var actions sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.WorkspaceID, &date, &d.TotalCount, &d.ImportantCount, &d.Content, &actions, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.SummaryDate = date.Format("2006-01-02")
		if actions.Valid {
			d.ActionItems = actions.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetSummaryByDateScoped fetches a single summary within the requested scope.
func (s *Store) GetSummaryByDateScoped(ctx context.Context, userID, workspaceID, date string) (*DailySummary, error) {
	var d DailySummary
	var summaryDate time.Time
	var actions sql.NullString
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, workspace_id, summary_date, total_count, important_count, content, action_items, created_at
		FROM daily_summaries WHERE user_id=$1 AND workspace_id=$2 AND summary_date=$3::DATE LIMIT 1`, userID, workspaceID, date).
		Scan(&d.ID, &d.UserID, &d.WorkspaceID, &summaryDate, &d.TotalCount, &d.ImportantCount, &d.Content, &actions, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.SummaryDate = summaryDate.Format("2006-01-02")
	if actions.Valid {
		d.ActionItems = actions.String
	}
	return &d, nil
}

func defaultWorkspace(workspaceID string) string {
	if workspaceID == "" {
		return "default"
	}
	return workspaceID
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
