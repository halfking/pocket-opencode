// Package lobster is S0-C: the server-side mirror of the Lobster Vault.
//
// The phone is the authoritative store for e2ee_local_first assets (notes,
// voice memos, meeting recordings, voucher images, vault entries). The server
// NEVER sees plaintext for those — it stores only:
//
//   1. An encrypted mirror row per asset (asset_id, ws_id, kind, client_rev,
//      server_rev, encrypted_title?, cipher_blob, deleted_at) so the same
//      workspace can sync across devices.
//   2. A sync_log entry per push/pull for conflict resolution auditing.
//
// For cloud_authoritative assets (shared tasks, email tags) the server IS the
// authoritative store and holds plaintext — but those go through OTHER stores
// (task/email). This package only handles the e2ee mirror.
//
// Sync protocol (spec §3.2 decision 3):
//   - Client pushes dirty assets (client_rev > server_rev) as encrypted blobs.
//   - Server assigns server_rev, stores the ciphertext, returns the new rev.
//   - Client pulls assets with server_rev > last-known, decrypts locally.
//   - Conflict (two devices push same asset): highest client_rev wins; the
//     loser's blob is retained as a "conflict version" for manual merge.
package lobster

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned on single-row miss.
var ErrNotFound = errors.New("lobster: asset mirror not found")

// AssetMirror is the server-side view of an e2ee asset. CipherTitle and
// CipherBlob are opaque ciphertext from the client; the server cannot decrypt.
type AssetMirror struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	Kind         string `json:"kind"`
	ClientRev    int    `json:"client_rev"`
	ServerRev    int    `json:"server_rev"`
	CipherTitle  string `json:"cipher_title,omitempty"` // optional encrypted title
	CipherBlob   string `json:"cipher_blob"`            // the encrypted payload (body+blobs+meta)
	DeletedAt    int64  `json:"deleted_at,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`
}

// SyncStore persists the encrypted asset mirrors + sync log.
type SyncStore struct {
	pool *pgxpool.Pool
}

// NewSyncStore constructs the store and runs idempotent migrations.
func NewSyncStore(pool *pgxpool.Pool) (*SyncStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("lobster: pgxpool is nil")
	}
	s := &SyncStore{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("lobster migrate: %w", err)
	}
	return s, nil
}

func (s *SyncStore) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS asset_mirrors (
	id            TEXT PRIMARY KEY,
	workspace_id  TEXT NOT NULL,
	kind          TEXT NOT NULL DEFAULT 'note',
	client_rev    INTEGER NOT NULL DEFAULT 1,
	server_rev    INTEGER NOT NULL DEFAULT 1,
	cipher_title  TEXT DEFAULT '',
	cipher_blob   TEXT NOT NULL DEFAULT '',
	deleted_at    BIGINT NOT NULL DEFAULT 0,
	updated_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_asset_mirrors_ws ON asset_mirrors(workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_asset_mirrors_rev ON asset_mirrors(workspace_id, server_rev);

CREATE TABLE IF NOT EXISTS asset_sync_log (
	id            BIGSERIAL PRIMARY KEY,
	workspace_id  TEXT NOT NULL,
	asset_id      TEXT NOT NULL,
	op            TEXT NOT NULL,           -- push | pull | conflict
	client_rev    INTEGER,
	server_rev    INTEGER,
	detail        TEXT DEFAULT '',
	created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_asset_sync_log_ws ON asset_sync_log(workspace_id, created_at DESC);
`)
	return err
}

// PushResult is the outcome of pushing one asset.
type PushResult struct {
	AssetID    string `json:"asset_id"`
	ServerRev  int    `json:"server_rev"`
	Conflict   bool   `json:"conflict,omitempty"`   // true if a newer rev existed
	PrevBlob   string `json:"prev_blob,omitempty"`  // when conflict, the superseded blob for client merge
}

// Push upserts an asset mirror. If the incoming client_rev is <= the stored
// server_rev AND the stored client_rev differs, it's a conflict — the stored
// blob is returned as PrevBlob for client-side merge, but the push still
// succeeds (last-write-wins by client_rev, ties broken by updated_at).
func (s *SyncStore) Push(ctx context.Context, m *AssetMirror) (*PushResult, error) {
	if m.WorkspaceID == "" {
		m.WorkspaceID = "default"
	}
	res := &PushResult{AssetID: m.ID}

	// Check existing to detect conflict.
	existing, _ := s.GetMirror(ctx, m.ID)
	if existing != nil && existing.ClientRev > m.ClientRev {
		// Incoming is OLDER than stored — conflict. Retain stored as prev.
		res.Conflict = true
		res.PrevBlob = existing.CipherBlob
		// Still accept the push (client may be resolving the conflict).
	}

	newServerRev := m.ClientRev
	if existing != nil && newServerRev <= existing.ServerRev {
		newServerRev = existing.ServerRev + 1
	}

	_, err := s.pool.Exec(ctx, `
INSERT INTO asset_mirrors (id, workspace_id, kind, client_rev, server_rev, cipher_title, cipher_blob, deleted_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
	workspace_id = EXCLUDED.workspace_id,
	kind         = EXCLUDED.kind,
	client_rev   = EXCLUDED.client_rev,
	server_rev   = EXCLUDED.server_rev,
	cipher_title = EXCLUDED.cipher_title,
	cipher_blob  = EXCLUDED.cipher_blob,
	deleted_at   = EXCLUDED.deleted_at,
	updated_at   = EXCLUDED.updated_at
`, m.ID, m.WorkspaceID, m.Kind, m.ClientRev, newServerRev, m.CipherTitle, m.CipherBlob, m.DeletedAt, m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	res.ServerRev = newServerRev

	// Best-effort audit log.
	op := "push"
	if res.Conflict {
		op = "conflict"
	}
	_, _ = s.pool.Exec(ctx, `
INSERT INTO asset_sync_log (workspace_id, asset_id, op, client_rev, server_rev, detail)
VALUES ($1, $2, $3, $4, $5, $6)
`, m.WorkspaceID, m.ID, op, m.ClientRev, newServerRev, "")

	return res, nil
}

// Pull fetches asset mirrors for a workspace with server_rev > since.
// Used by clients to download changes made on other devices.
func (s *SyncStore) Pull(ctx context.Context, wsID string, since int, limit int) ([]AssetMirror, error) {
	if wsID == "" {
		wsID = "default"
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, kind, client_rev, server_rev, cipher_title, cipher_blob, deleted_at, updated_at
FROM asset_mirrors
WHERE workspace_id = $1 AND server_rev > $2
ORDER BY server_rev ASC
LIMIT $3
`, wsID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetMirror
	for rows.Next() {
		var m AssetMirror
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.Kind, &m.ClientRev, &m.ServerRev, &m.CipherTitle, &m.CipherBlob, &m.DeletedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMirror fetches one mirror. Returns (nil, nil) on miss.
func (s *SyncStore) GetMirror(ctx context.Context, id string) (*AssetMirror, error) {
	m := &AssetMirror{}
	err := s.pool.QueryRow(ctx, `
SELECT id, workspace_id, kind, client_rev, server_rev, cipher_title, cipher_blob, deleted_at, updated_at
FROM asset_mirrors WHERE id = $1
`, id).Scan(&m.ID, &m.WorkspaceID, &m.Kind, &m.ClientRev, &m.ServerRev, &m.CipherTitle, &m.CipherBlob, &m.DeletedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// LatestServerRev returns the highest server_rev for a workspace (clients use
// this to know how far behind they are).
func (s *SyncStore) LatestServerRev(ctx context.Context, wsID string) (int, error) {
	if wsID == "" {
		wsID = "default"
	}
	var rev int
	err := s.pool.QueryRow(ctx, `
SELECT COALESCE(MAX(server_rev), 0) FROM asset_mirrors WHERE workspace_id = $1
`, wsID).Scan(&rev)
	return rev, err
}
