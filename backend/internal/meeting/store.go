package meeting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("meeting migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS meetings (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		title TEXT,
		location TEXT,
		participants JSONB DEFAULT '[]'::jsonb,
		started_at BIGINT NOT NULL DEFAULT 0,
		duration_ms INTEGER DEFAULT 0,
		summary TEXT,
		refined_transcript TEXT,
		note_id TEXT,
		status TEXT DEFAULT 'completed',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		deleted_at BIGINT
	);
	CREATE INDEX IF NOT EXISTS idx_meetings_user_started ON meetings(user_id, started_at DESC);
	CREATE INDEX IF NOT EXISTS idx_meetings_note ON meetings(note_id) WHERE note_id IS NOT NULL;
	`)
	return err
}

func (s *Store) Upsert(ctx context.Context, m *Meeting) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = NowUnix()
	}
	m.UpdatedAt = NowUnix()
	participants, _ := json.Marshal(m.Participants)
	if m.Participants == nil {
		participants = []byte("[]")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO meetings
			(id, user_id, title, location, participants, started_at, duration_ms,
			 summary, refined_transcript, note_id, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO UPDATE SET
			title = EXCLUDED.title,
			location = EXCLUDED.location,
			participants = EXCLUDED.participants,
			started_at = EXCLUDED.started_at,
			duration_ms = EXCLUDED.duration_ms,
			summary = EXCLUDED.summary,
			refined_transcript = EXCLUDED.refined_transcript,
			note_id = EXCLUDED.note_id,
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at
	`, m.ID, m.UserID, nullStr(m.Title), nullStr(m.Location), participants,
		m.StartedAt, m.DurationMs, nullStr(m.Summary), nullStr(m.RefinedTranscript),
		nullStr(m.NoteID), m.Status, m.CreatedAt, m.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, userID string, limit int) ([]Meeting, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, title, location, participants, started_at, duration_ms,
		       summary, refined_transcript, note_id, status, created_at, updated_at
		FROM meetings
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY started_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Meeting
	for rows.Next() {
		var m Meeting
		var title, location, summary, refined, noteID *string
		var participants []byte
		if err := rows.Scan(&m.ID, &m.UserID, &title, &location, &participants,
			&m.StartedAt, &m.DurationMs, &summary, &refined, &noteID, &m.Status,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if title != nil {
			m.Title = *title
		}
		if location != nil {
			m.Location = *location
		}
		if summary != nil {
			m.Summary = *summary
		}
		if refined != nil {
			m.RefinedTranscript = *refined
		}
		if noteID != nil {
			m.NoteID = *noteID
		}
		_ = json.Unmarshal(participants, &m.Participants)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, userID, id string) (*Meeting, error) {
	list, err := s.List(ctx, userID, 1000)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("meeting not found")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
