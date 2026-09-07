package usersetting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists user_settings in PostgreSQL (admin 主库).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("usersetting: nil pool")
	}
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("usersetting migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS user_settings (
		user_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL DEFAULT 'default',
		namespace TEXT NOT NULL,
		id TEXT NOT NULL,
		payload JSONB NOT NULL DEFAULT '{}',
		secret_encrypted TEXT NOT NULL DEFAULT '',
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (user_id, workspace_id, namespace, id)
	);
	CREATE INDEX IF NOT EXISTS idx_user_settings_ws
		ON user_settings(workspace_id, namespace);
	`)
	return err
}

func (s *Store) List(userID, workspaceID string) ([]Record, error) {
	userID, workspaceID = scope(userID, workspaceID)
	rows, err := s.pool.Query(context.Background(), `
		SELECT user_id, workspace_id, namespace, id, payload, secret_encrypted, updated_at
		FROM user_settings
		WHERE user_id = $1 AND workspace_id = $2
		ORDER BY namespace, id
	`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Record, 0)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) Get(userID, workspaceID, namespace, id string) (*Record, error) {
	userID, workspaceID = scope(userID, workspaceID)
	row := s.pool.QueryRow(context.Background(), `
		SELECT user_id, workspace_id, namespace, id, payload, secret_encrypted, updated_at
		FROM user_settings
		WHERE user_id = $1 AND workspace_id = $2 AND namespace = $3 AND id = $4
	`, userID, workspaceID, namespace, id)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) Put(rec Record) (*PutResult, error) {
	rec.UserID, rec.WorkspaceID = scope(rec.UserID, rec.WorkspaceID)
	if rec.Namespace == "" || rec.ID == "" {
		return nil, fmt.Errorf("usersetting: namespace and id required")
	}
	if rec.UpdatedAt <= 0 {
		rec.UpdatedAt = time.Now().Unix()
	}
	if len(rec.Payload) == 0 {
		rec.Payload = json.RawMessage(`{}`)
	}

	existing, err := s.Get(rec.UserID, rec.WorkspaceID, rec.Namespace, rec.ID)
	if err != nil {
		return nil, err
	}
	serverUpdated := int64(0)
	if existing != nil {
		serverUpdated = existing.UpdatedAt
		if rec.Secret == "" {
			rec.Secret = existing.Secret
		}
	}
	if existing != nil && DecidePut(rec.UpdatedAt, serverUpdated) == DecisionKeep {
		return &PutResult{Applied: false, Conflict: rec.UpdatedAt < serverUpdated, Record: *existing}, nil
	}

	_, err = s.pool.Exec(context.Background(), `
		INSERT INTO user_settings (user_id, workspace_id, namespace, id, payload, secret_encrypted, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, workspace_id, namespace, id) DO UPDATE SET
			payload = EXCLUDED.payload,
			secret_encrypted = EXCLUDED.secret_encrypted,
			updated_at = EXCLUDED.updated_at
	`, rec.UserID, rec.WorkspaceID, rec.Namespace, rec.ID, []byte(rec.Payload), rec.Secret, rec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rec.HasSecret = rec.Secret != ""
	rec.Secret = ""
	return &PutResult{Applied: true, Record: rec}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (Record, error) {
	var rec Record
	var payload []byte
	var secret string
	err := row.Scan(&rec.UserID, &rec.WorkspaceID, &rec.Namespace, &rec.ID, &payload, &secret, &rec.UpdatedAt)
	if err != nil {
		return rec, err
	}
	rec.Payload = json.RawMessage(payload)
	rec.HasSecret = secret != ""
	rec.Secret = secret
	return rec, nil
}

func scope(userID, workspaceID string) (string, string) {
	if userID == "" {
		userID = "local"
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	return userID, workspaceID
}
