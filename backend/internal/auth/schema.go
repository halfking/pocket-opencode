package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureSchema creates the users + email_verification_codes tables and related
// indexes if they don't exist. Safe to call multiple times (idempotent).
//
// 当前 UserStore 仅走 pgxpool（无 sqlite 路径），故 DDL 只维护 pg 版本。
// 旧 users 表无 email 列：通过 ALTER TABLE ... ADD COLUMN IF NOT EXISTS 兼容。
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
		ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT;
		ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;
		CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uq ON users (LOWER(email))
			WHERE email IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);

		CREATE TABLE IF NOT EXISTS email_verification_codes (
			id BIGSERIAL PRIMARY KEY,
			email TEXT NOT NULL,
			purpose TEXT NOT NULL,
			code_hash TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			request_ip TEXT
		);
		CREATE INDEX IF NOT EXISTS evc_lookup
			ON email_verification_codes (LOWER(email), purpose, expires_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}
	return nil
}
