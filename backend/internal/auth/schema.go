package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureSchema creates the users table and related indexes if they don't exist.
// Safe to call multiple times (idempotent).
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("pgxpool is nil")
	}
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at BIGINT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
	`)
	if err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}
	return nil
}
