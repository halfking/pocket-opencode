package email

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
		-- Workspace isolation for daily summaries. The original table carried
		-- UNIQUE(user_id, summary_date), so a user belonging to two workspaces
		-- had both workspaces collapse onto one row and overwrite each other.
		-- Order matters: collapse pre-existing duplicates first, otherwise
		-- creating the new unique index fails on legacy data.
		-- ctid breaks ties when created_at collides, so exactly one row per
		-- (user, workspace, date) survives regardless of legacy timestamps.
		DELETE FROM daily_summaries
		WHERE ctid NOT IN (
			SELECT DISTINCT ON (user_id, workspace_id, summary_date) ctid
			FROM daily_summaries
			ORDER BY user_id, workspace_id, summary_date, created_at DESC, ctid DESC
		);
		ALTER TABLE daily_summaries DROP CONSTRAINT IF EXISTS daily_summaries_user_id_summary_date_key;
		CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_summaries_user_ws_date
			ON daily_summaries(user_id, workspace_id, summary_date);
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
	-- Repair emails.workspace_id against its authoritative source.
	--
	-- GetAccountByID never selected workspace_id, so Fetcher.Sync saw an empty
	-- Account.WorkspaceID and every fetched email was persisted as 'default'.
	-- The account row is the source of truth, so realign the denormalized
	-- column from it. Read paths join on email_accounts and were never
	-- affected; this only repairs the column itself (and anything that may
	-- filter on it later).
	--
	-- Touches only rows that actually disagree, so it is idempotent and a
	-- no-op once converged.
	UPDATE emails e
	SET workspace_id = a.workspace_id
	FROM email_accounts a
	WHERE e.account_id = a.id AND e.workspace_id <> a.workspace_id;
	-- Phase 0.1: SMTP credentials (encrypted) + VacationReply + Email Message-ID/UID
	-- additions are idempotent. They don't break old rows.
	ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS smtp_host TEXT NOT NULL DEFAULT '';
	ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS smtp_port INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS smtp_credential_encrypted TEXT NOT NULL DEFAULT '';
	CREATE TABLE IF NOT EXISTS email_vacation_replies (
		id TEXT PRIMARY KEY,
		account_id TEXT NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL DEFAULT 'default',
		enabled BOOLEAN NOT NULL DEFAULT FALSE,
		start_at BIGINT NOT NULL,
		end_at BIGINT NOT NULL,
		subject TEXT NOT NULL,
		body_text TEXT NOT NULL,
		last_sent_at BIGINT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	);
		CREATE INDEX IF NOT EXISTS idx_vacation_replies_ws ON email_vacation_replies(workspace_id, enabled);
		CREATE TABLE IF NOT EXISTS email_vacation_deliveries (
			vacation_id TEXT NOT NULL REFERENCES email_vacation_replies(id) ON DELETE CASCADE,
			email_id TEXT NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			recipient TEXT NOT NULL,
			original_message_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL CHECK(status IN ('claimed','sent','failed')),
			error TEXT NOT NULL DEFAULT '',
			claimed_at BIGINT NOT NULL,
			sent_at BIGINT,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (vacation_id, email_id)
		);
		CREATE INDEX IF NOT EXISTS idx_vacation_deliveries_recipient
			ON email_vacation_deliveries(vacation_id, recipient, claimed_at DESC);
		CREATE INDEX IF NOT EXISTS idx_vacation_deliveries_ws
			ON email_vacation_deliveries(workspace_id, status);
		-- 规则意图：archive / route-folder / trigger-autoreply 等“副作用型”动作
		-- 不直接执行，先落表，由后续 job 消费 + 重试。
		CREATE TABLE IF NOT EXISTS email_action_intents (
			id TEXT PRIMARY KEY,
			email_id TEXT NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
			account_id TEXT NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			user_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			folder TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('pending','applied','failed','skipped')) DEFAULT 'pending',
			error TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			applied_at BIGINT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_action_intents_idem
			ON email_action_intents(idempotency_key);
		CREATE INDEX IF NOT EXISTS idx_action_intents_pending
			ON email_action_intents(status, created_at)
			WHERE status = 'pending';
		-- Mark-important audit trail: action_reason already exists, ensure NOT NULL
	-- constraint isn't enforced (legacy rows have NULL). We leave it permissive.
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

// SetClassificationScoped updates classification only within one user/workspace.
func (s *Store) SetClassificationScoped(ctx context.Context, id, userID, workspaceID, category, importance, aiSummary, suggestedAction string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE emails e SET category = $1, importance = $2, ai_summary = $3, suggested_action = $4
		FROM email_accounts a
		WHERE e.id = $5 AND e.account_id = a.id AND a.user_id = $6 AND a.workspace_id = $7
	`, category, importance, aiSummary, suggestedAction, id, userID, workspaceID)
	return err
}

// ON CONFLICT DO NOTHING 不指定冲突目标，PostgreSQL 会自动匹配任一唯一
// 约束/索引：(account_id, message_id) 全局唯一约束，或 message_id IS NULL
// 时的 (account_id, subject, date) 部分唯一索引。这样无论哪种冲突都不会
// 抛错中断同步。
func (s *Store) InsertEmail(ctx context.Context, e Email) error {
	_, err := s.pool.Exec(ctx,
		// Two defects used to make this statement fail on every call, so no
		// fetched email could ever be persisted:
		//   1. a stray $19 with only 18 target columns ("INSERT has more
		//      expressions than target columns");
		//   2. created_at is BIGINT NOT NULL with no default, but was never
		//      supplied ("null value in column created_at ... violates
		//      not-null constraint").
		// Column count and placeholder count must stay in sync (19 of each now).
		//
		// created_at vs date — 两者刻意不同：
		//   - date       = 邮件原始时间（IMAP envelope），排序/按日窗口/过滤都用它
		//   - created_at = 本行入库时间（time.Now）
		// 目前没有任何读路径消费 created_at，全部走 e.date；把 created_at 改成
		// e.Date 只会复制 date 并丢掉入库时间，因此保持 time.Now()。
		`INSERT INTO emails (id, account_id, workspace_id, message_id, uid, from_address, from_name, subject, snippet, date, is_read, is_starred, category, importance, ai_summary, suggested_action, action_reason, has_attachments, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
			 ON CONFLICT DO NOTHING`,
		e.ID, e.AccountID, defaultWorkspace(e.WorkspaceID), nullStr(e.MessageID), e.UID,
		e.FromAddress, e.FromName, e.Subject, e.Snippet, e.Date,
		e.IsRead, e.IsStarred, e.Category, e.Importance, e.AISummary, e.SuggestedAction,
		nullStr(e.ActionReason), e.HasAttachments, time.Now().Unix())

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

// GetSyncStatusScoped returns sync state for one user/workspace.
//
// 未读数只按 e.account_id=a.id 统计。归属由外层的
// a.user_id/a.workspace_id 谓词保证，account_id 已经唯一确定账户，因此不再
// 附加 e.workspace_id=a.workspace_id：那个谓词依赖 emails 上的反范式列，
// Fetcher 曾经把它一律写成 'default'，于是非 default workspace 的未读数恒为
// 0。改成只认 account_id 后，历史脏数据也不会再影响计数。
func (s *Store) GetSyncStatusScoped(ctx context.Context, userID, workspaceID string) ([]AccountSyncStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.display_name, a.email_address, a.last_synced_uid, a.last_synced_at, a.enabled,
		       COALESCE((SELECT COUNT(*) FROM emails e WHERE e.account_id=a.id AND e.is_read=FALSE), 0)
		FROM email_accounts a WHERE a.user_id=$1 AND a.workspace_id=$2 ORDER BY a.created_at`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AccountSyncStatus, 0)
	for rows.Next() {
		var st AccountSyncStatus
		var uid, at sql.NullInt64
		if err := rows.Scan(&st.AccountID, &st.DisplayName, &st.EmailAddress, &uid, &at, &st.Enabled, &st.PendingCount); err != nil {
			return nil, err
		}
		if uid.Valid {
			st.LastSyncedUID = uid.Int64
		}
		if at.Valid {
			st.LastSyncedAt = at.Int64
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

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
	var smtpHost sql.NullString
	var smtpPort sql.NullInt64
	var lastUID, lastAt sql.NullInt64
	var rules sql.NullString
	// workspace_id 必须选出来：Fetcher.Sync 用它给抓下来的邮件打
	// workspace 标记。之前这里没选，a.WorkspaceID 恒为 ""，导致所有邮件
	// 都以 'default' 落库（defaultWorkspace 的兜底），非 default workspace
	// 的账户在 GetSyncStatusScoped 里未读数恒为 0。
	//
	// smtp_host/smtp_port 同样是之前声明了变量却没进 Scan 的死代码。
	// smtp_credential_encrypted 仍然刻意不选——它只能经
	// GetSMTPCredentialScoped 取出，避免 SMTP 凭证混入 IMAP 路径。
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
		       credential_encrypted, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at,
		       smtp_host, smtp_port
		FROM email_accounts WHERE id = $1
	`, id).Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress, &a.IMAPHost, &a.IMAPPort,
		&a.AuthType, &cred, &a.SyncIntervalMin, &lastUID, &lastAt, &rules, &a.Enabled, &a.CreatedAt,
		&smtpHost, &smtpPort)
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
	if smtpHost.Valid && smtpHost.String != "" {
		a.SMTPHost = smtpHost.String
	}
	if smtpPort.Valid {
		a.SMTPPort = int(smtpPort.Int64)
	}
	return &a, cred, nil
}

// GetSMTPCredentialScoped 取出账户的 SMTP 加密凭证 + 账户 email，仅供
// /test-smtp 等内部用途；不要在 JSON 响应中暴露任何明文。
//
// 返回值顺序：host, accountEmail, port, encryptedCredential。
func (s *Store) GetSMTPCredentialScoped(ctx context.Context, id, userID, workspaceID string) (string, string, int, string, error) {
	var smtpHost, emailAddress, smtpCred sql.NullString
	var smtpPort sql.NullInt64
	err := s.pool.QueryRow(ctx, `
		SELECT smtp_host, smtp_port, email_address, smtp_credential_encrypted
		FROM email_accounts
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3
	`, id, userID, workspaceID).Scan(&smtpHost, &smtpPort, &emailAddress, &smtpCred)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, "", ErrNotFound
	}
	if err != nil {
		return "", "", 0, "", err
	}
	if !smtpHost.Valid {
		return "", "", 0, "", fmt.Errorf("smtp not configured")
	}
	cred := ""
	if smtpCred.Valid {
		cred = smtpCred.String
	}
	return smtpHost.String, emailAddress.String, int(smtpPort.Int64), cred, nil
}

// --- SMTP / Vacation CRUD ---

// UpsertSMTPSettingsScoped 保存 SMTP host/port/credential。
//
// credential 由主调代码负责加密。是否改写凭证只由 updateCredential 决定：
//   - updateCredential=false → 完全不碰 smtp_credential_encrypted（host/port-only 编辑）
//   - updateCredential=true  → 写入 credential，空字符串即"清空凭证"
//
// host 传空字符串表示清空 SMTP 配置，此时 port 允许为 0；host 非空时 port
// 必须在合法区间内。
func (s *Store) UpsertSMTPSettingsScoped(ctx context.Context, id, userID, workspaceID, host string, port int, credential string, updateCredential bool) error {
	host = strings.TrimSpace(host)
	if host != "" && (port < 1 || port > 65535) {
		return fmt.Errorf("smtp port out of range")
	}
	var setCred string
	args := []any{host, port, id, userID, workspaceID}
	query := `UPDATE email_accounts SET smtp_host=$1, smtp_port=$2 WHERE id=$3 AND user_id=$4 AND workspace_id=$5`
	if updateCredential {
		setCred = credential
		query = `UPDATE email_accounts SET smtp_host=$1, smtp_port=$2, smtp_credential_encrypted=$3 WHERE id=$4 AND user_id=$5 AND workspace_id=$6`
		args = []any{host, port, setCred, id, userID, workspaceID}
	}
	res, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Vacation replies: configuration CRUD + 投递队列。投递由 scheduler.vacationLoop --- //
// --- 消费（ClaimNextVacationDelivery → SMTP 发送 → MarkVacationDeliverySent）。 --- //

// UpsertVacationScoped 插入或更新一条 vacation reply。accountID 必须属
// 于 (userID, workspaceID)，否则返回 ErrNotFound（不暴露跨租户写入）。
//
// 第一步：若 record 已存在 (id 非空) 则校验其归属；不存在则要求
// accountID 同样落在 scope 内。这避免任意调用方 upsert 到别人账户下的
// vacation（之前 ON CONFLICT(id) 没有校验）。
func (s *Store) UpsertVacationScoped(ctx context.Context, v *VacationReply, userID, workspaceID string) error {
	if v == nil {
		return fmt.Errorf("vacation required")
	}
	if userID == "" {
		return fmt.Errorf("user required")
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	wasExisting := v.ID != ""
	if v.ID == "" {
		v.ID = randomID("vac")
	}
	if v.CreatedAt == 0 {
		v.CreatedAt = time.Now().Unix()
	}
	v.UpdatedAt = time.Now().Unix()

	// 1. 先确认目标 account 在 scope 内。
	var ok bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM email_accounts
			WHERE id = $1 AND user_id = $2 AND workspace_id = $3
		)
	`, v.AccountID, userID, workspaceID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}

	// 2. 如果 record 已存在，再校验它绑定的 account 是否在 scope 内。
	//    这一步阻止"创建 vacation 后修改 accountID 指向他人账户"的越权。
	if wasExisting {
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM email_vacation_replies r
				JOIN email_accounts a ON a.id = r.account_id
				WHERE r.id = $1 AND a.user_id = $2 AND a.workspace_id = $3
			)
		`, v.ID, userID, workspaceID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO email_vacation_replies
			(id, account_id, workspace_id, enabled, start_at, end_at, subject, body_text, last_sent_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			account_id  = EXCLUDED.account_id,
			enabled     = EXCLUDED.enabled,
			start_at    = EXCLUDED.start_at,
			end_at      = EXCLUDED.end_at,
			subject     = EXCLUDED.subject,
			body_text   = EXCLUDED.body_text,
			updated_at  = EXCLUDED.updated_at
	`,
		v.ID, v.AccountID, defaultWorkspace(workspaceID), v.Enabled, v.StartAt, v.EndAt,
		v.Subject, v.BodyText, v.LastSentAt, v.CreatedAt, v.UpdatedAt)
	return err
}

// ListVacationsScoped 列出该账户/workspace 的所有 vacation 配置。
func (s *Store) ListVacationsScoped(ctx context.Context, accountID, userID, workspaceID string) ([]VacationReply, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, workspace_id, enabled, start_at, end_at, subject, body_text,
		       last_sent_at, created_at, updated_at
		FROM email_vacation_replies
		WHERE account_id IN (SELECT id FROM email_accounts WHERE user_id=$1 AND workspace_id=$2)
		  AND (account_id=$3 OR $3='')
		ORDER BY start_at DESC
	`, userID, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]VacationReply, 0)
	for rows.Next() {
		var v VacationReply
		var lastSent sql.NullInt64
		if err := rows.Scan(&v.ID, &v.AccountID, &v.WorkspaceID, &v.Enabled,
			&v.StartAt, &v.EndAt, &v.Subject, &v.BodyText,
			&lastSent, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if lastSent.Valid {
			ts := lastSent.Int64
			v.LastSentAt = &ts
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ClaimNextVacationDelivery 原子领取一条待发送的自动回复。
//
// 约束：
//   - 每个账户只采用最近更新的一条有效 vacation，避免重叠配置重复回复；
//   - 仅处理 vacation 最近更新后新入库的邮件；
//   - 同一 vacation + 原邮件只领取一次，失败任务在 retryAfter 后可重试；
//   - 同一 vacation + 收件人 24 小时内最多领取一次；
//   - advisory lock 串行化同一 vacation/收件人的并发领取。
func (s *Store) ClaimNextVacationDelivery(ctx context.Context, now int64, retryAfter time.Duration) (*VacationDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	retryBefore := now - int64(retryAfter/time.Second)
	var d VacationDelivery
	err = tx.QueryRow(ctx, `
		WITH active_vacations AS (
			SELECT DISTINCT ON (r.account_id)
				r.id, r.account_id, r.workspace_id, r.subject, r.body_text, r.updated_at,
				a.user_id, a.email_address, a.smtp_host, a.smtp_port, a.smtp_credential_encrypted
			FROM email_vacation_replies r
			JOIN email_accounts a ON a.id = r.account_id AND a.workspace_id = r.workspace_id
			WHERE r.enabled = TRUE
			  AND r.start_at <= $1 AND r.end_at >= $1
			  AND a.enabled = TRUE AND a.smtp_host <> '' AND a.smtp_port > 0
			ORDER BY r.account_id, r.updated_at DESC, r.id DESC
		)
		SELECT v.id, e.id, v.account_id, v.workspace_id, v.user_id,
		       e.from_address, COALESCE(e.message_id, ''), COALESCE(e.subject, ''),
		       v.subject, v.body_text, v.smtp_host, v.smtp_port, v.email_address,
		       v.smtp_credential_encrypted
		FROM active_vacations v
		JOIN emails e ON e.account_id = v.account_id AND e.workspace_id = v.workspace_id
		LEFT JOIN email_vacation_deliveries d
		  ON d.vacation_id = v.id AND d.email_id = e.id
		WHERE e.created_at >= v.updated_at
		  AND e.from_address <> ''
		  AND LOWER(e.from_address) <> LOWER(v.email_address)
		  AND LOWER(e.from_address) NOT LIKE 'no-reply@%'
		  AND LOWER(e.from_address) NOT LIKE 'noreply@%'
		  AND LOWER(e.from_address) NOT LIKE 'do-not-reply@%'
		  AND LOWER(e.from_address) NOT LIKE 'mailer-daemon@%'
		  AND LOWER(e.from_address) NOT LIKE 'postmaster@%'
		  AND (d.vacation_id IS NULL OR (d.status IN ('claimed', 'failed') AND d.updated_at <= $2))
		  AND NOT EXISTS (
			SELECT 1 FROM email_vacation_deliveries recent
			WHERE recent.vacation_id = v.id
			  AND recent.email_id <> e.id
			  AND LOWER(recent.recipient) = LOWER(e.from_address)
			  AND recent.status IN ('claimed', 'sent')
			  AND recent.claimed_at > $1 - 86400
		  )
		ORDER BY e.created_at, e.id
		FOR UPDATE OF e SKIP LOCKED
		LIMIT 1
	`, now, retryBefore).Scan(
		&d.VacationID, &d.EmailID, &d.AccountID, &d.WorkspaceID, &d.UserID,
		&d.Recipient, &d.OriginalMessageID, &d.OriginalSubject,
		&d.VacationSubject, &d.VacationBody, &d.SMTPHost, &d.SMTPPort,
		&d.SenderAddress, &d.SMTPEncryptedCredential,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	lockKey := d.VacationID + "\x00" + strings.ToLower(d.Recipient)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return nil, err
	}

	var recentlyClaimed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM email_vacation_deliveries
			WHERE vacation_id = $1 AND LOWER(recipient) = LOWER($2)
			  AND status IN ('claimed', 'sent') AND claimed_at > $3 - 86400
		)
	`, d.VacationID, d.Recipient, now).Scan(&recentlyClaimed); err != nil {
		return nil, err
	}
	if recentlyClaimed {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}

	res, err := tx.Exec(ctx, `
		INSERT INTO email_vacation_deliveries
			(vacation_id, email_id, workspace_id, recipient, original_message_id,
			 status, error, claimed_at, sent_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'claimed', '', $6, NULL, $6)
		ON CONFLICT (vacation_id, email_id) DO UPDATE SET
			status = 'claimed', error = '', claimed_at = EXCLUDED.claimed_at,
			sent_at = NULL, updated_at = EXCLUDED.updated_at
		WHERE email_vacation_deliveries.status IN ('claimed', 'failed')
		  AND email_vacation_deliveries.updated_at <= $7
	`, d.VacationID, d.EmailID, d.WorkspaceID, d.Recipient, d.OriginalMessageID, now, retryBefore)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	d.ClaimedAt = now
	return &d, nil
}

// MarkVacationDeliverySent 将 claim 标记为成功，并更新 vacation 最近发送时间。
func (s *Store) MarkVacationDeliverySent(ctx context.Context, vacationID, emailID string, sentAt int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res, err := tx.Exec(ctx, `
		UPDATE email_vacation_deliveries
		SET status = 'sent', error = '', sent_at = $3, updated_at = $3
		WHERE vacation_id = $1 AND email_id = $2 AND status = 'claimed'
	`, vacationID, emailID, sentAt)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE email_vacation_replies SET last_sent_at = $2 WHERE id = $1
	`, vacationID, sentAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkVacationDeliveryFailed 记录脱敏后的发送失败，供退避后重试。
func (s *Store) MarkVacationDeliveryFailed(ctx context.Context, vacationID, emailID, message string, failedAt int64) error {
	if len(message) > 500 {
		message = message[:500]
	}
	res, err := s.pool.Exec(ctx, `
		UPDATE email_vacation_deliveries
		SET status = 'failed', error = $3, updated_at = $4
		WHERE vacation_id = $1 AND email_id = $2 AND status = 'claimed'
	`, vacationID, emailID, message, failedAt)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteVacationScoped 删除 vacation 配置。
func (s *Store) DeleteVacationScoped(ctx context.Context, vacationID, userID, workspaceID string) error {
	res, err := s.pool.Exec(ctx, `
		DELETE FROM email_vacation_replies
		WHERE id=$1
		  AND account_id IN (SELECT id FROM email_accounts WHERE user_id=$2 AND workspace_id=$3)
	`, vacationID, userID, workspaceID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAccountAuthType 更新 auth_type（OAuth 回调后使用）。
func (s *Store) SetAccountAuthType(ctx context.Context, id, authType string) error {
	_, err := s.pool.Exec(ctx, `UPDATE email_accounts SET auth_type = $2 WHERE id = $1`, id, authType)
	return err
}

// SetAccountAuthTypeScoped updates auth type only for an owned account.
func (s *Store) SetAccountAuthTypeScoped(ctx context.Context, id, userID, workspaceID, authType string) error {
	res, err := s.pool.Exec(ctx, `UPDATE email_accounts SET auth_type = $4 WHERE id = $1 AND user_id = $2 AND workspace_id = $3`, id, userID, workspaceID, authType)
	if err == nil && res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) UpdateSyncState(ctx context.Context, id string, lastUID int64, lastAt int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE email_accounts SET last_synced_uid = $2, last_synced_at = $3 WHERE id = $1`, id, lastUID, lastAt)
	return err
}

// ListEnabledAccounts 返回所有启用的账户。
//
// Deprecated: 该查询的 SELECT 不含 workspace_id 列，返回的 Account.WorkspaceID
// 恒为空，下游 fetcher/Scheduler 会把邮件/任务错误地归到 'default' workspace。
// 新代码应改用 ListEnabledAccountsWithWorkspace。保留此方法仅为兼容历史调用方。
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

// UpsertOAuthTokenScoped inserts or updates a token only for an owned account.
func (s *Store) UpsertOAuthTokenScoped(ctx context.Context, accountID, userID, workspaceID, refreshEnc, accessEnc string, expiresAt int64, scope string) error {
	res, err := s.pool.Exec(ctx, `
		INSERT INTO email_oauth_tokens
			(account_id, user_id, workspace_id, refresh_token_encrypted, access_token_encrypted, expires_at, scope, updated_at)
		SELECT $1, a.user_id, a.workspace_id, $4, $5, $6, $7, EXTRACT(EPOCH FROM NOW())::BIGINT
		FROM email_accounts a WHERE a.id=$1 AND a.user_id=$2 AND a.workspace_id=$3
		ON CONFLICT (account_id) DO UPDATE SET
			refresh_token_encrypted=EXCLUDED.refresh_token_encrypted,
			access_token_encrypted=EXCLUDED.access_token_encrypted, expires_at=EXCLUDED.expires_at,
			scope=EXCLUDED.scope, user_id=EXCLUDED.user_id, workspace_id=EXCLUDED.workspace_id,
			updated_at=EXCLUDED.updated_at`, accountID, userID, workspaceID, refreshEnc, accessEnc, expiresAt, scope)
	if err == nil && res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

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
			SELECT t.account_id, a.user_id, a.workspace_id, t.refresh_token_encrypted, COALESCE(t.access_token_encrypted, ''), t.expires_at
			FROM email_oauth_tokens t JOIN email_accounts a ON a.id=t.account_id
			WHERE t.expires_at > 0 AND t.expires_at <= (EXTRACT(EPOCH FROM NOW())::BIGINT + $1)
	`, leewaySec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OAuthTokenRow, 0)
	for rows.Next() {
		var r OAuthTokenRow
		if err := rows.Scan(&r.AccountID, &r.UserID, &r.WorkspaceID, &r.RefreshEnc, &r.AccessEnc, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OAuthTokenRow is the lightweight projection used by the scheduler refresh
// worker. Decryption happens off the hot path (in scheduler.refresh loop).
type OAuthTokenRow struct {
	AccountID   string
	UserID      string
	WorkspaceID string
	RefreshEnc  string
	AccessEnc   string
	ExpiresAt   int64
}

// ListAccountsScoped returns only accounts owned by the user in the workspace.
func (s *Store) ListAccountsScoped(ctx context.Context, userID, workspaceID string) ([]Account, error) {
	// smtp_host/smtp_port are selected so the account list can prefill the SMTP
	// editor without an extra per-account GET. smtp_credential_encrypted is
	// deliberately NOT selected — it is only reachable through
	// GetSMTPCredentialScoped.
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
		       sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at,
		       smtp_host, smtp_port
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
		var smtpHost sql.NullString
		var smtpPort sql.NullInt64
		if err := rows.Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress, &a.IMAPHost,
			&a.IMAPPort, &a.AuthType, &a.SyncIntervalMin, &lastUID, &lastAt, &rules, &a.Enabled, &a.CreatedAt,
			&smtpHost, &smtpPort); err != nil {
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
		if smtpHost.Valid {
			a.SMTPHost = smtpHost.String
		}
		if smtpPort.Valid {
			a.SMTPPort = int(smtpPort.Int64)
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
	var smtpHost sql.NullString
	var smtpPort sql.NullInt64
	// smtp_host/smtp_port are selected so the UI can render the saved SMTP
	// settings. smtp_credential_encrypted is deliberately NOT selected here —
	// it is only reachable through GetSMTPCredentialScoped.
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port, auth_type,
		       credential_encrypted, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at,
		       smtp_host, smtp_port
		FROM email_accounts WHERE id = $1 AND user_id = $2 AND workspace_id = $3
	`, id, userID, workspaceID).Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress,
		&a.IMAPHost, &a.IMAPPort, &a.AuthType, &cred, &a.SyncIntervalMin, &lastUID, &lastAt,
		&rules, &a.Enabled, &a.CreatedAt, &smtpHost, &smtpPort)
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
	if smtpHost.Valid {
		a.SMTPHost = smtpHost.String
	}
	if smtpPort.Valid {
		a.SMTPPort = int(smtpPort.Int64)
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
//
// 比 scanEmail 多读一列 body_path：handleEmailBody 用它判断完整正文是否已缓存，
// 避免每次都做一次文件探测。其余 list 路径不需要 body_path，仍走 scanEmail。
func (s *Store) GetEmailByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Email, error) {
	var e Email
	var fromName, subject, snippet, category, importance, aiSummary, suggestedAction, bodyPath sql.NullString
	err := s.pool.QueryRow(ctx, `SELECT e.id, e.account_id, e.from_address, e.from_name, e.subject, e.snippet,
		e.date, e.is_read, e.is_starred, e.category, e.importance, e.ai_summary, e.suggested_action, e.has_attachments,
		e.body_path
		FROM emails e JOIN email_accounts a ON a.id=e.account_id
		WHERE e.id=$1 AND a.user_id=$2 AND a.workspace_id=$3`, id, userID, workspaceID).
		Scan(&e.ID, &e.AccountID, &e.FromAddress, &fromName, &subject, &snippet, &e.Date, &e.IsRead,
			&e.IsStarred, &category, &importance, &aiSummary, &suggestedAction, &e.HasAttachments, &bodyPath)
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
	if bodyPath.Valid {
		e.BodyPath = bodyPath.String
	}
	return &e, nil
}

// InsertActionIntent 写入规则建议意图。idempotency_key 唯一约束保证
// 同一 (email_id, action) 只产生一行；冲突时视作成功（持久状态保持 pending）。
// workspace / user 透传 JWT 上下文，不接受 client 覆盖。
func (s *Store) InsertActionIntent(ctx context.Context, intent *ActionIntent) error {
	if intent == nil {
		return fmt.Errorf("intent required")
	}
	if intent.EmailID == "" || intent.AccountID == "" || intent.Action == "" || intent.IdempotencyKey == "" {
		return fmt.Errorf("intent missing required fields")
	}
	if intent.UserID == "" {
		return fmt.Errorf("user required")
	}
	if intent.WorkspaceID == "" {
		intent.WorkspaceID = "default"
	}
	if intent.CreatedAt == 0 {
		intent.CreatedAt = time.Now().Unix()
	}
	intent.UpdatedAt = time.Now().Unix()
	if intent.Status == "" {
		intent.Status = "pending"
	}
	if intent.ID == "" {
		intent.ID = randomID("act")
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO email_action_intents
			(id, email_id, account_id, workspace_id, user_id, action, folder, reason,
			 idempotency_key, status, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, '', $11, $11)
		ON CONFLICT (idempotency_key) DO NOTHING
	`,
		intent.ID, intent.EmailID, intent.AccountID, defaultWorkspace(intent.WorkspaceID),
		intent.UserID, intent.Action, intent.Folder, intent.Reason,
		intent.IdempotencyKey, intent.Status, intent.CreatedAt)
	return err
}

// UpdateActionIntentStatus 由消费方写回 applied/failed/skipped 状态。
// 仅在 (workspace, email, action) 命中且状态是 pending 时更新，避免竞争。
func (s *Store) UpdateActionIntentStatus(ctx context.Context, intentID, userID, workspaceID, status, errMsg string, appliedAt int64) error {
	if status == "" {
		return fmt.Errorf("status required")
	}
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	res, err := s.pool.Exec(ctx, `
		UPDATE email_action_intents
		SET status = $4, error = $5, updated_at = $6, applied_at = $6
		WHERE id = $1
		  AND user_id = $2
		  AND workspace_id = $3
		  AND status = 'pending'
	`, intentID, userID, defaultWorkspace(workspaceID), status, errMsg, appliedAt)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimActionIntents 拉取最多 limit 条 pending 意图，caller 主动通过
// UpdateActionIntentStatus 标记 applied/failed/skipped；不引入新 in_flight 状态。
func (s *Store) ClaimActionIntents(ctx context.Context, userID, workspaceID string, limit int) ([]ActionIntent, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, email_id, account_id, workspace_id, user_id, action, folder, reason,
		       idempotency_key, status, error, created_at, updated_at, applied_at
		FROM email_action_intents
		WHERE user_id = $1 AND workspace_id = $2 AND status = 'pending'
		ORDER BY created_at
		LIMIT $3
	`, userID, defaultWorkspace(workspaceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ActionIntent, 0)
	for rows.Next() {
		var i ActionIntent
		var appliedAt sql.NullInt64
		if err := rows.Scan(&i.ID, &i.EmailID, &i.AccountID, &i.WorkspaceID, &i.UserID,
			&i.Action, &i.Folder, &i.Reason, &i.IdempotencyKey, &i.Status, &i.Error,
			&i.CreatedAt, &i.UpdatedAt, &appliedAt); err != nil {
			return nil, err
		}
		if appliedAt.Valid {
			ts := appliedAt.Int64
			i.AppliedAt = &ts
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// MarkEmailBodyCached 记录正文缓存路径（相对路径，由 server 层拼接 dataDir）。
// 只在成功拿到 IMAP 完整正文后写入，未命中时 body_path 为空。
func (s *Store) MarkEmailBodyCached(ctx context.Context, id, bodyPath string, bodyBytes int) error {
	res, err := s.pool.Exec(ctx, `
		UPDATE emails
		SET body_path = $2, has_attachments = COALESCE(has_attachments, FALSE)
		WHERE id = $1
	`, id, bodyPath)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	_ = bodyBytes // reserved: future byte-count metadata
	return nil
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

// UpsertSummaryScoped writes a daily summary keyed by (user, workspace, date).
//
// The legacy UpsertSummary conflicts on (user_id, summary_date) only, so a user
// in two workspaces had the second write overwrite the first. Background jobs
// must use this variant.
func (s *Store) UpsertSummaryScoped(ctx context.Context, sum *DailySummary) error {
	if sum == nil {
		return fmt.Errorf("summary required")
	}
	if sum.UserID == "" {
		return fmt.Errorf("summary user required")
	}
	if sum.ID == "" {
		sum.ID = randomID("summary")
	}
	workspaceID := defaultWorkspace(sum.WorkspaceID)
	sum.WorkspaceID = workspaceID
	_, err := s.pool.Exec(ctx, `
		INSERT INTO daily_summaries
			(id, user_id, workspace_id, summary_date, total_count, important_count, content, action_items, created_at)
		VALUES ($1, $2, $3, $4::DATE, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, workspace_id, summary_date) DO UPDATE SET
			total_count     = EXCLUDED.total_count,
			important_count = EXCLUDED.important_count,
			content         = EXCLUDED.content,
			action_items    = EXCLUDED.action_items,
			created_at      = EXCLUDED.created_at
	`, sum.ID, sum.UserID, workspaceID, sum.SummaryDate, sum.TotalCount, sum.ImportantCount,
		sum.Content, nullStr(sum.ActionItems), sum.CreatedAt)
	return err
}

// ListEmailsByDayScoped returns one day's emails for a single (user, workspace).
//
// ListEmailsByDay joins on user_id only, so a multi-workspace user would get
// every workspace's mail mixed into one summary.
func (s *Store) ListEmailsByDayScoped(ctx context.Context, userID, workspaceID, date string, tzOffsetSec int) ([]Email, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	loc := time.FixedZone("user", tzOffsetSec)
	t = t.In(loc)
	startUnix := t.Unix()
	endUnix := t.Add(24 * time.Hour).Unix()

	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.account_id, e.from_address, e.from_name, e.subject, e.snippet, e.date,
		       e.is_read, e.is_starred, e.category, e.importance, e.ai_summary, e.suggested_action, e.has_attachments
		FROM emails e
		JOIN email_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1 AND a.workspace_id = $2 AND e.date >= $3 AND e.date < $4
		ORDER BY e.date DESC
		LIMIT 500
	`, userID, defaultWorkspace(workspaceID), startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Email
	for rows.Next() {
		var e Email
		var fromName, subject, snippet, category, importance, aiSummary, suggestedAction sql.NullString
		if err := rows.Scan(&e.ID, &e.AccountID, &e.FromAddress, &fromName, &subject, &snippet, &e.Date,
			&e.IsRead, &e.IsStarred, &category, &importance, &aiSummary, &suggestedAction, &e.HasAttachments); err != nil {
			return nil, err
		}
		e.WorkspaceID = defaultWorkspace(workspaceID)
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

// ListEnabledAccountsWithWorkspace is ListEnabledAccounts plus workspace_id.
//
// ListEnabledAccounts omits workspace_id from its SELECT, so every Account it
// returns has an empty WorkspaceID. Background workers that group or persist by
// workspace must use this variant or they silently fall back to "default".
func (s *Store) ListEnabledAccountsWithWorkspace(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, display_name, email_address, imap_host, imap_port,
		       auth_type, sync_interval_min, last_synced_uid, last_synced_at, rules, enabled, created_at
		FROM email_accounts WHERE enabled = TRUE ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Account, 0)
	for rows.Next() {
		var a Account
		var lastUID, lastAt sql.NullInt64
		var rules sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.WorkspaceID, &a.DisplayName, &a.EmailAddress,
			&a.IMAPHost, &a.IMAPPort, &a.AuthType, &a.SyncIntervalMin, &lastUID, &lastAt,
			&rules, &a.Enabled, &a.CreatedAt); err != nil {
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
		a.WorkspaceID = defaultWorkspace(a.WorkspaceID)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevokeOAuthTokenScoped revokes a token only when the account belongs to the
// given (user, workspace). Returns ErrNotFound otherwise, so a stale scheduler
// row can never disable another tenant's account.
func (s *Store) RevokeOAuthTokenScoped(ctx context.Context, accountID, userID, workspaceID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owned string
	err = tx.QueryRow(ctx, `SELECT id FROM email_accounts WHERE id=$1 AND user_id=$2 AND workspace_id=$3`,
		accountID, userID, defaultWorkspace(workspaceID)).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM email_oauth_tokens WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_accounts SET auth_type='password', enabled=FALSE WHERE id=$1`, accountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
