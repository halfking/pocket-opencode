package vault

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists end-to-end-encrypted password-vault blobs for cross-device
// sync. The server never sees plaintext: clients encrypt the entire vault
// with their own public key and upload only ciphertext.
//
// Phase 0 fix (audit D3): schema supports multi-version blobs so the client
// can implement real conflict resolution (newest wins, with two-version
// retention for manual merge) instead of the earlier last-write-wins only.
//
// See docs/2026-07-02-password-vault-design.md for the full crypto design.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("vault migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS vault_sync (
		user_id TEXT NOT NULL,
		blob_ciphertext TEXT NOT NULL,
		version INTEGER NOT NULL,
		is_current BOOLEAN DEFAULT TRUE,
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (user_id, version)
	);
	CREATE INDEX IF NOT EXISTS idx_vault_user ON vault_sync(user_id);
	`)
	return err
}

// PutLatest stores a new encrypted blob version for a user and marks all
// previous versions as non-current. Older versions are retained (not
// overwritten) so a client can surface conflicts for manual resolution.
func (s *Store) PutLatest(ctx context.Context, userID, ciphertext string, version int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Mark prior versions non-current.
	if _, err := tx.Exec(ctx, `
		UPDATE vault_sync SET is_current = FALSE WHERE user_id = $1
	`, userID); err != nil {
		return err
	}
	// Insert the new current version (UPSERT in case of a replayed version).
	if _, err := tx.Exec(ctx, `
		INSERT INTO vault_sync (user_id, blob_ciphertext, version, is_current, updated_at)
		VALUES ($1, $2, $3, TRUE, EXTRACT(EPOCH FROM NOW())::BIGINT)
		ON CONFLICT (user_id, version) DO UPDATE SET
			blob_ciphertext = EXCLUDED.blob_ciphertext,
			is_current = TRUE,
			updated_at = EXTRACT(EPOCH FROM NOW())::BIGINT
	`, userID, ciphertext, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetLatest returns the newest current blob for a user.
func (s *Store) GetLatest(ctx context.Context, userID string) (ciphertext string, version int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT blob_ciphertext, version FROM vault_sync
		WHERE user_id = $1 AND is_current = TRUE
		ORDER BY version DESC LIMIT 1
	`, userID).Scan(&ciphertext, &version)
	if err != nil {
		return "", 0, fmt.Errorf("no vault for user: %w", err)
	}
	return
}

// ListVersions returns all retained versions for a user (used for conflict
// surfacing). Newest first.
func (s *Store) ListVersions(ctx context.Context, userID string) ([]Version, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT version, is_current, updated_at FROM vault_sync
		WHERE user_id = $1 ORDER BY version DESC LIMIT 20
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.Version, &v.IsCurrent, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetByVersion returns the encrypted blob for a specific retained version of
// a user's vault. Used by the restore / version-detail endpoints.
func (s *Store) GetByVersion(ctx context.Context, userID string, version int) (string, error) {
	var blob string
	err := s.pool.QueryRow(ctx, `
		SELECT blob_ciphertext FROM vault_sync
		WHERE user_id = $1 AND version = $2
		LIMIT 1
	`, userID, version).Scan(&blob)
	if err != nil {
		return "", fmt.Errorf("vault version not found: %w", err)
	}
	return blob, nil
}

// MarkCurrent atomically clears the current flag on every existing version for
// the user, then sets it on the target version. Used by the restore endpoint
// when a user picks an older version to recover from.
func (s *Store) MarkCurrent(ctx context.Context, userID string, version int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE vault_sync SET is_current = FALSE WHERE user_id = $1`, userID); err != nil {
		return err
	}
	res, err := tx.Exec(ctx,
		`UPDATE vault_sync SET is_current = TRUE
		 WHERE user_id = $1 AND version = $2`, userID, version)
	if err != nil {
		return err
	}
	if n := res.RowsAffected(); n == 0 {
		return fmt.Errorf("vault version %d not found for user", version)
	}
	return tx.Commit(ctx)
}

func (s *Store) Close() error { return nil }
