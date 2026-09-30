package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// maxListResults is the maximum number of notes returned by List to prevent
	// unbounded result sets in offline list rendering.
	maxListResults = 200
)

// Store is the PostgreSQL-backed local cache of voice-note metadata.
// AI processing (classification, SSOT, graph) happens in kxmemory; pocketd
// only caches metadata for offline list rendering. Migrated from SQLite in
// Phase 0 alongside the other module stores.
//
// The actual `notes` table in PG was created by a separate migration
// (docs/appendix-a-pg-migration.sql) and uses different types from what
// this store originally assumed: created_at / updated_at are
// `timestamp without time zone DEFAULT CURRENT_TIMESTAMP`, `tags` is
// `jsonb DEFAULT '[]'::jsonb`, and there are extra columns (`content`,
// `ai_summary`, `confidence_score`, `deleted_at`). This store now adapts
// to that schema: timestamps are converted at the boundary, tags are
// marshalled to / unmarshalled from jsonb arrays, and the `content`
// column is filled from `Note.Snippet` on insert (the Go-side Note model
// only carries Snippet today; full content lives in kxmemory).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("notes migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	// Idempotent: table already exists in the DB (from appendix-a), so
	// CREATE TABLE IF NOT EXISTS is a no-op. ADD COLUMN IF NOT EXISTS
	// covers the (rare) fresh-install case.
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS notes (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		workspace_id TEXT DEFAULT 'default',
		title TEXT,
		content TEXT NOT NULL DEFAULT '',
		snippet TEXT,
		content_type TEXT DEFAULT 'voice',
		domain TEXT,
		tags JSONB DEFAULT '[]'::jsonb,
		audio_path TEXT,
		audio_duration INTEGER DEFAULT 0,
		created_by_voice BOOLEAN DEFAULT TRUE,
		ai_summary TEXT,
		confidence_score REAL,
		created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMP WITHOUT TIME ZONE
	);
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS content TEXT NOT NULL DEFAULT '';
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS snippet TEXT;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS tags JSONB DEFAULT '[]'::jsonb;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS ai_summary TEXT;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS confidence_score REAL;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITHOUT TIME ZONE;
	CREATE INDEX IF NOT EXISTS idx_notes_user_domain ON notes(user_id, domain);
	CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("notes store migration failed: %w", err)
	}
	return nil
}

// Upsert caches or updates a note's metadata after kxmemory confirms it.
// allowedDomains mirrors the schema's CHECK (domain IN (...)).
// Package-level because both Upsert and UpdateNoteScoped must agree; a second
// copy in UpdateNoteScoped is exactly the kind of drift that lets an invalid
// domain through one path and not the other.
var allowedDomains = map[string]bool{"work": true, "study": true, "life": true, "idea": true}

// ErrStoreUnavailableNotes is returned when the store has no pool, i.e. the
// handler is wired but the backing store is not configured.
var ErrStoreUnavailableNotes = errors.New("notes: store not configured")

func (s *Store) Upsert(ctx context.Context, n *Note) error {
	// Code-side Note has Snippet only; actual table has a separate
	// NOT NULL `content` column. Fall back to Snippet for content so
	// the row never violates the NOT NULL constraint. Full content
	// lives in kxmemory; this local cache only mirrors metadata.
	content := n.Snippet

	// Convert epoch-second timestamps (Note.CreatedAt / UpdatedAt are int64
	// seconds) to time.Time for PG `timestamp` columns. CreatedAt == 0
	// means "not set" → let DB default kick in.
	var createdAt, updatedAt any
	if n.CreatedAt > 0 {
		createdAt = time.Unix(n.CreatedAt, 0).UTC()
	} else {
		createdAt = nil // NULL → DEFAULT CURRENT_TIMESTAMP
	}
	if n.UpdatedAt > 0 {
		updatedAt = time.Unix(n.UpdatedAt, 0).UTC()
	} else {
		updatedAt = nil
	}

	// tags: Note model holds a JSON-encoded array string. Actual column is
	// jsonb. Pass the []string form via pgx (it knows how to encode []string
	// into jsonb). If the JSON is malformed, fall back to empty array.
	var tagsVal any = []string{}
	if n.Tags != "" {
		var arr []string
		if err := json.Unmarshal([]byte(n.Tags), &arr); err == nil {
			tagsVal = arr
		}
	}

	// domain: schema has CHECK (domain IN ('work','study','life','idea')).
	// The Go Note.Domain defaults to "" when not set, which the CHECK
	// rejects. Only pass domain when it matches one of the allowed values;
	// otherwise pass NULL.
	var domainVal any
	if allowedDomains[n.Domain] {
		domainVal = n.Domain
	} else {
		domainVal = nil
	}

	// content_type: same — schema has CHECK (content_type IN
	// ('voice','text','mixed')). Default to "voice" if unspecified so
	// the column's NOT NULL + CHECK constraints both pass.
	contentType := n.ContentType
	if contentType != "voice" && contentType != "text" && contentType != "mixed" {
		contentType = "voice"
	}
	var contentTypeVal any = contentType

	_, err := s.pool.Exec(ctx, `
		INSERT INTO notes (id, user_id, workspace_id, title, content, snippet, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($13, CURRENT_TIMESTAMP), COALESCE($14, CURRENT_TIMESTAMP))
		ON CONFLICT (id) DO UPDATE SET
			title         = EXCLUDED.title,
			content       = EXCLUDED.content,
			snippet       = EXCLUDED.snippet,
			content_type  = EXCLUDED.content_type,
			domain        = EXCLUDED.domain,
			tags          = EXCLUDED.tags,
			updated_at    = COALESCE($14, CURRENT_TIMESTAMP)
	`,
		n.ID, n.UserID, n.WorkspaceID, n.Title, content, n.Snippet, contentTypeVal, domainVal, tagsVal, n.AudioPath, n.AudioDuration, n.CreatedByVoice, createdAt, updatedAt)
	if err != nil {
		return fmt.Errorf("upsert note %s: %w", n.ID, err)
	}
	return nil
}

// ListScoped returns notes for one (user, workspace) pair. Use this instead of
// List on request paths: List filters on user_id only, so the same user in two
// workspaces would see both workspaces' notes.
func (s *Store) ListScoped(ctx context.Context, userID, workspaceID, domain string) ([]Note, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	q := `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	args := []any{userID, workspaceID}
	if domain != "" {
		q += " AND domain = $3"
		args = append(args, domain)
	}
	q += " ORDER BY updated_at DESC LIMIT " + fmt.Sprintf("%d", maxListResults)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query notes for user %s workspace %s: %w", userID, workspaceID, err)
	}
	defer rows.Close()
	return scanNoteRows(rows)
}

// Deprecated: List ignores workspace_id. Use ListScoped on any path that serves
// an authenticated request.
func (s *Store) List(ctx context.Context, userID, domain string) ([]Note, error) {
	// SELECT 现在包含 content 和 snippet — 修复 v1.0 期间遗留的字段缺失
	// bug（sync classify 路径曾因此拿到空 snippet 让真实 kxmemory 返回 400）。
	// 这两个列都加 NOT NULL DEFAULT '' 在 migrate() 里，所以向后兼容旧行。
	q := `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1 AND deleted_at IS NULL`
	args := []any{userID}
	if domain != "" {
		q += " AND domain = $2"
		args = append(args, domain)
	}
	q += " ORDER BY updated_at DESC LIMIT " + fmt.Sprintf("%d", maxListResults)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query notes for user %s: %w", userID, err)
	}
	defer rows.Close()
	return scanNoteRows(rows)
}

// scanNoteRows decodes the shared note SELECT column list. List and ListScoped
// both use it so the nullable-column handling stays in one place.
func scanNoteRows(rows pgx.Rows) ([]Note, error) {
	var out []Note
	for rows.Next() {
		var (
			n           Note
			workspaceID sql.NullString
			title       sql.NullString
			content     sql.NullString
			snippet     sql.NullString
			domain      sql.NullString
			tags        []byte // raw jsonb
			audioPath   sql.NullString
			createdAt   sql.NullTime
			updatedAt   sql.NullTime
		)
		if err := rows.Scan(&n.ID, &n.UserID, &workspaceID, &title, &content, &snippet,
			&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan note row: %w", err)
		}
		if workspaceID.Valid {
			n.WorkspaceID = workspaceID.String
		}
		if title.Valid {
			n.Title = title.String
		}
		if content.Valid {
			n.Snippet = content.String // Content字段已移除，用Snippet代替
		}
		if snippet.Valid {
			n.Snippet = snippet.String
		}
		if domain.Valid {
			n.Domain = domain.String
		}
		if audioPath.Valid {
			n.AudioPath = audioPath.String
		}
		if createdAt.Valid {
			n.CreatedAt = createdAt.Time.Unix()
		}
		if updatedAt.Valid {
			n.UpdatedAt = updatedAt.Time.Unix()
		}
		// tags: jsonb array → JSON string (matches Note model convention)
		if len(tags) > 0 {
			var arr []string
			if err := json.Unmarshal(tags, &arr); err == nil {
				b, _ := json.Marshal(arr)
				n.Tags = string(b)
			}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate note rows: %w", err)
	}
	return out, nil
}

// GetByID 按 ID 查找单条笔记（不含软删除）。
//
// 返回 (nil, nil) 表示不存在（与 email.Store.GetEmailByID 行为一致），让
// handler 用 `if found == nil` 判断 404 而非依赖 error 类型。
//
// 用于替换 handleNoteClassify / handleNoteOperations 的 O(N) List + linear
// scan 反模式，避免每次 sync classify 都扫整张 notes 表。
// Deprecated: Use GetByIDScoped for production code with ownership checks.
func (s *Store) GetByID(ctx context.Context, id string) (*Note, error) {
	var (
		n           Note
		workspaceID sql.NullString
		title       sql.NullString
		content     sql.NullString
		snippet     sql.NullString
		domain      sql.NullString
		tags        []byte
		audioPath   sql.NullString
		createdAt   sql.NullTime
		updatedAt   sql.NullTime
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&n.ID, &n.UserID, &workspaceID, &title, &content, &snippet,
		&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by id %s: %w", id, err)
	}
	if workspaceID.Valid {
		n.WorkspaceID = workspaceID.String
	}
	if title.Valid {
		n.Title = title.String
	}
	if content.Valid {
		n.Snippet = content.String // Content字段已移除，用Snippet代替
	}
	if snippet.Valid {
		n.Snippet = snippet.String
	}
	if domain.Valid {
		n.Domain = domain.String
	}
	if audioPath.Valid {
		n.AudioPath = audioPath.String
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time.Unix()
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time.Unix()
	}
	if len(tags) > 0 {
		var arr []string
		if err := json.Unmarshal(tags, &arr); err == nil {
			b, _ := json.Marshal(arr)
			n.Tags = string(b)
		}
	}
	return &n, nil
}

// GetByIDScoped 按 ID 和 workspace 查找单条笔记（不含软删除）。
func (s *Store) GetByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Note, error) {
	var (
		n         Note
		wsID      sql.NullString
		title     sql.NullString
		content   sql.NullString
		snippet   sql.NullString
		domain    sql.NullString
		tags      []byte
		audioPath sql.NullString
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, id, userID, workspaceID).Scan(
		&n.ID, &n.UserID, &wsID, &title, &content, &snippet,
		&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by id %s: %w", id, err)
	}
	if wsID.Valid {
		n.WorkspaceID = wsID.String
	}
	if title.Valid {
		n.Title = title.String
	}
	if content.Valid {
		n.Snippet = content.String
	}
	if snippet.Valid {
		n.Snippet = snippet.String
	}
	if domain.Valid {
		n.Domain = domain.String
	}
	if audioPath.Valid {
		n.AudioPath = audioPath.String
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time.Unix()
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time.Unix()
	}
	if len(tags) > 0 {
		var arr []string
		if err := json.Unmarshal(tags, &arr); err == nil {
			b, _ := json.Marshal(arr)
			n.Tags = string(b)
		}
	}
	return &n, nil
}

// NotePatch is a partial update for a note. Only non-nil fields are written,
// so an explicit empty string clears a value while nil means "leave as is".
// Mirrors scheduledtask.TaskInput's convention.
type NotePatch struct {
	Title         *string
	Content       *string // also refreshes Snippet
	ContentType   *string
	Domain        *string
	Tags          *string // JSON array string
	AudioPath     *string
	AudioDuration *int
}

// ErrNoteNotFound is returned by UpdateNoteScoped when no row matches the
// (id, user, workspace, not-deleted) tuple. Distinct from a DB error so the
// HTTP layer can answer 404 instead of 500.
var ErrNoteNotFound = errors.New("notes: note not found")

// snippetRunes is the snippet length the local cache uses for list rendering.
const snippetRunes = 200

// UpdateNoteScoped applies a partial update to one note, constrained to the
// (id, user, workspace) tuple and to non-deleted rows.
//
// BUG-N（2026-09-30 由 scripts/probe-write-methods.mjs 的 method 级探测发现）：
// 前端 frontend/src/api/notes.ts 的 `update()` 打 PUT /api/notes/:id，但后端
// handleNoteOperations 此前只有 GET/DELETE，notes.Store 也没有任何更新方法。
// 结果是**编辑笔记在真机上恒 405**，功能完全不可用。
//
// 两个刻意的行为约定：
//   - Content 变更时同步重算 Snippet。列表摘要读的是 snippet；不同步的话
//     "编辑后标题还在列表"这种断言会恒真（标题没动），掩盖正文根本没存上。
//     断言必须能看见新正文。
//   - 所有权进 UPDATE 谓词，不用先查后写。跨用户/跨 workspace 改不动。
func (s *Store) UpdateNoteScoped(ctx context.Context, id, userID, workspaceID string, patch NotePatch) (*Note, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailableNotes
	}

	sets := []string{}
	args := []any{id, userID, workspaceID}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if patch.Title != nil {
		add("title", *patch.Title)
	}
	if patch.Content != nil {
		add("content", *patch.Content)
		// Snippet mirrors the head of content; DB stores seconds, we store the
		// same truncated preview the list view renders.
		snip := []rune(*patch.Content)
		if len(snip) > snippetRunes {
			snip = snip[:snippetRunes]
		}
		add("snippet", string(snip))
	}
	if patch.ContentType != nil {
		ct := *patch.ContentType
		// schema CHECK (content_type IN ('voice','text','mixed'))
		if ct != "voice" && ct != "text" && ct != "mixed" {
			return nil, fmt.Errorf("notes: invalid contentType %q", ct)
		}
		add("content_type", ct)
	}
	if patch.Domain != nil {
		d := *patch.Domain
		// schema CHECK (domain IN ('work','study','life','idea'))
		if d == "" {
			add("domain", nil)
		} else if allowedDomains[d] {
			add("domain", d)
		} else {
			return nil, fmt.Errorf("notes: invalid domain %q", d)
		}
	}
	if patch.Tags != nil {
		// column is jsonb; pass []string so pgx encodes it natively.
		arr := []string{}
		if *patch.Tags != "" {
			if err := json.Unmarshal([]byte(*patch.Tags), &arr); err != nil {
				return nil, fmt.Errorf("notes: tags must be a JSON array string: %w", err)
			}
		}
		add("tags", arr)
	}
	if patch.AudioPath != nil {
		add("audio_path", *patch.AudioPath)
	}
	if patch.AudioDuration != nil {
		add("audio_duration", *patch.AudioDuration)
	}

	if len(sets) == 0 {
		// Nothing to change: return the current row rather than erroring, so
		// an empty PATCH is a harmless no-op instead of a 4xx the UI can't
		// distinguish from a real failure.
		return s.GetByIDScoped(ctx, id, userID, workspaceID)
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	sql := fmt.Sprintf(`
		UPDATE notes SET %s
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, strings.Join(sets, ", "))

	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("update note %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNoteNotFound
	}
	return s.GetByIDScoped(ctx, id, userID, workspaceID)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	// Soft-delete: keep the row, set deleted_at. Avoids breaking FK
	// relationships in other tables that may reference notes.id in the
	// future, and matches the actual schema's idx_notes_* `WHERE
	// deleted_at IS NULL` partial-index design.
	// Deprecated: Use DeleteScoped for production code with ownership checks.
	_, err := s.pool.Exec(ctx, `UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete note %s: %w", id, err)
	}
	return nil
}

// DeleteScoped soft-deletes a note with ownership verification
func (s *Store) DeleteScoped(ctx context.Context, id, userID, workspaceID string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE notes SET deleted_at = CURRENT_TIMESTAMP 
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, id, userID, workspaceID)
	if err != nil {
		return fmt.Errorf("delete note %s: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("note not found or already deleted")
	}
	return nil
}

func (s *Store) Close() error { return nil }

// ListDeletedIDsScoped 返回 deleted_at 晚于 sinceSec（epoch 秒）的软删除
// 笔记 id（墓碑清单），供客户端把「其他端已删除」的行从本地缓存移除。
// 见 docs/2026-09-09-list-sync-rules.md §3.2。
func (s *Store) ListDeletedIDsScoped(ctx context.Context, userID, workspaceID string, sinceSec int64, limit int) ([]string, error) {
	if sinceSec <= 0 {
		return nil, nil
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM notes
		WHERE user_id = $1 AND workspace_id = $2
		  AND deleted_at IS NOT NULL AND deleted_at > to_timestamp($3)
		ORDER BY deleted_at ASC LIMIT $4`,
		userID, workspaceID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("query deleted note ids: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
