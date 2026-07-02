package notes

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed local cache of voice-note metadata.
// AI processing (classification, SSOT, graph) happens in kxmemory; pocketd
// only caches metadata for offline list rendering. Migrated from SQLite in
// Phase 0 alongside the other module stores.
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
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS notes (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		workspace_id TEXT,
		title TEXT,
		snippet TEXT NOT NULL,
		content_type TEXT DEFAULT 'voice',
		domain TEXT,
		tags TEXT,
		audio_path TEXT,
		audio_duration INTEGER DEFAULT 0,
		created_by_voice BOOLEAN DEFAULT TRUE,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_notes_user_domain ON notes(user_id, domain);
	CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC);
	`)
	return err
}

// Upsert caches or updates a note's metadata after kxmemory confirms it.
func (s *Store) Upsert(ctx context.Context, n *Note) error {
	now := time.Now().Unix()
	if n.CreatedAt == 0 {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notes (id, user_id, workspace_id, title, snippet, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			title = EXCLUDED.title,
			snippet = EXCLUDED.snippet,
			domain = EXCLUDED.domain,
			tags = EXCLUDED.tags,
			updated_at = EXCLUDED.updated_at
	`, n.ID, n.UserID, n.WorkspaceID, n.Title, n.Snippet, n.ContentType, n.Domain, n.Tags, n.AudioPath, n.AudioDuration, n.CreatedByVoice, n.CreatedAt, n.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, userID, domain string) ([]Note, error) {
	if domain != "" {
		rows, err := s.pool.Query(ctx, `
			SELECT id, user_id, workspace_id, title, snippet, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at
			FROM notes WHERE user_id = $1 AND domain = $2
			ORDER BY updated_at DESC LIMIT 200
		`, userID, domain)
		return scanNotes(rows, err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, title, snippet, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1
		ORDER BY updated_at DESC LIMIT 200
	`, userID)
	return scanNotes(rows, err)
}

func scanNotes(rows pgx.Rows, err error) ([]Note, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.WorkspaceID, &n.Title, &n.Snippet, &n.ContentType, &n.Domain, &n.Tags, &n.AudioPath, &n.AudioDuration, &n.CreatedByVoice, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notes WHERE id = $1`, id)
	return err
}

func (s *Store) Close() error { return nil }
