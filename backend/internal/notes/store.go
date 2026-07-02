package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	return err
}

// Upsert caches or updates a note's metadata after kxmemory confirms it.
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
	allowedDomains := map[string]bool{"work": true, "study": true, "life": true, "idea": true}
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
	return err
}

func (s *Store) List(ctx context.Context, userID, domain string) ([]Note, error) {
	// Only select columns that exist in BOTH schemas, so this query
	// works against either a fresh install or a DB created by the
	// appendix-a migration.
	q := `
		SELECT id, user_id, workspace_id, title, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1 AND deleted_at IS NULL`
	args := []any{userID}
	if domain != "" {
		q += " AND domain = $2"
		args = append(args, domain)
	}
	q += " ORDER BY updated_at DESC LIMIT 200"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var (
			n            Note
			workspaceID  sql.NullString
			title        sql.NullString
			domain       sql.NullString
			tags         []byte // raw jsonb
			audioPath    sql.NullString
			createdAt    sql.NullTime
			updatedAt    sql.NullTime
		)
		if err := rows.Scan(&n.ID, &n.UserID, &workspaceID, &title, &n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if workspaceID.Valid {
			n.WorkspaceID = workspaceID.String
		}
		if title.Valid {
			n.Title = title.String
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
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	// Soft-delete: keep the row, set deleted_at. Avoids breaking FK
	// relationships in other tables that may reference notes.id in the
	// future, and matches the actual schema's idx_notes_* `WHERE
	// deleted_at IS NULL` partial-index design.
	_, err := s.pool.Exec(ctx, `UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	return err
}

func (s *Store) Close() error { return nil }

