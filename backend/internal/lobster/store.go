// Package lobster is S0-C: the server-side mirror of the Lobster Vault.
//
// The phone is the authoritative store for e2ee_local_first assets (notes,
// voice memos, meeting recordings, voucher images, vault entries). The server
// NEVER sees plaintext for those — it stores only:
//
//  1. An encrypted mirror row per asset (asset_id, ws_id, kind, client_rev,
//     server_rev, encrypted_title?, cipher_blob, deleted_at) so the same
//     workspace can sync across devices.
//  2. A sync_log entry per push/pull for conflict resolution auditing.
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

// ErrAssetScopeConflict 表示 asset_id 已属于**别的 workspace**。
//
// ⚠⚠ §944 D2：asset_id 是**客户端自选**的（请求体 pushes[].id）⇒ ⇒ ⇒
// 撞上一个**别的租户**的 id 时，修复前那条 ON CONFLICT 会把那一行
// **连 cipher_blob 一起搬进提交者的租户**（SET 里**有** workspace_id 和
// cipher_blob）⇒ ⇒⇒⇒ 后果是：受害者的加密资产**从自己的租户消失**、
// 出现在攻击者租户里，且内容被攻击者的密文**覆盖**。
//
//	而 handler 那一行注释自己写着「强制绑定 caller workspace，**防越权**」
//
// ⇒ ⇒⇒⇒ 也就是说：**SQL 并没有兑现 handler 承诺的那句「防越权」**。
var ErrAssetScopeConflict = errors.New("lobster: asset belongs to another workspace")

// AssetMirror is the server-side view of an e2ee asset. CipherTitle and
// CipherBlob are opaque ciphertext from the client; the server cannot decrypt.
type AssetMirror struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Kind        string `json:"kind"`
	ClientRev   int    `json:"client_rev"`
	ServerRev   int    `json:"server_rev"`
	CipherTitle string `json:"cipher_title,omitempty"` // optional encrypted title
	CipherBlob  string `json:"cipher_blob"`            // the encrypted payload (body+blobs+meta)
	DeletedAt   int64  `json:"deleted_at,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
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
CREATE INDEX IF NOT EXISTS idx_asset_mirrors_ws ON asset_mirrors(workspace_id, updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_asset_mirrors_rev ON asset_mirrors(workspace_id, server_rev ASC, id ASC);

CREATE TABLE IF NOT EXISTS asset_workspace_revisions (
	workspace_id  TEXT PRIMARY KEY,
	last_revision BIGINT NOT NULL DEFAULT 0
);
INSERT INTO asset_workspace_revisions (workspace_id, last_revision)
SELECT workspace_id, COALESCE(MAX(server_rev), 0)
FROM asset_mirrors
GROUP BY workspace_id
ON CONFLICT (workspace_id) DO UPDATE
SET last_revision = GREATEST(asset_workspace_revisions.last_revision, EXCLUDED.last_revision);

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
	AssetID   string `json:"asset_id"`
	ServerRev int    `json:"server_rev"`
	Conflict  bool   `json:"conflict,omitempty"`  // true if a newer rev existed
	PrevBlob  string `json:"prev_blob,omitempty"` // when conflict, the superseded blob for client merge

	// ⚠ §944 D2：单条 push 失败时**必须**让客户端看得见。
	//
	//	修复前 handler 把失败的那条 push 记成一个「只有 asset_id、
	//	server_rev=0」的结果 ⇒ ⇒ ⇒ 与「成功但 rev=0」**不可区分**
	//	⇒ ⇒ ⇒ 客户端会把本地资产标成已同步 ⇒ ⇒⇒⇒ **静默丢数据**
	//	（服务端根本没收到那份密文）。
	//
	//	⇒ 这里加一个**可选**字段（omitempty ⇒ 老客户端不受影响）
	//	⇒ ⇒ 「失败必须可区分」这条不变量在**响应形状**层面落地。
	Error string `json:"error,omitempty"`
}

// Push upserts an asset mirror. Every successful push receives a unique,
// monotonically increasing revision within its workspace, so Pull's `since`
// cursor cannot skip concurrent writes to different assets.
func (s *SyncStore) Push(ctx context.Context, m *AssetMirror) (*PushResult, error) {
	if m.WorkspaceID == "" {
		m.WorkspaceID = "default"
	}
	res := &PushResult{AssetID: m.ID}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_workspace_revisions (workspace_id)
		VALUES ($1)
		ON CONFLICT (workspace_id) DO NOTHING
	`, m.WorkspaceID); err != nil {
		return nil, err
	}
	var newServerRev int
	if err := tx.QueryRow(ctx, `
		UPDATE asset_workspace_revisions
		SET last_revision = last_revision + 1
		WHERE workspace_id = $1
		RETURNING last_revision
	`, m.WorkspaceID).Scan(&newServerRev); err != nil {
		return nil, err
	}

	var existingClientRev int
	var existingBlob string
	err = tx.QueryRow(ctx, `
		SELECT client_rev, cipher_blob
		FROM asset_mirrors
		WHERE id = $1
		FOR UPDATE
	`, m.ID).Scan(&existingClientRev, &existingBlob)
	if err == nil && existingClientRev > m.ClientRev {
		res.Conflict = true
		res.PrevBlob = existingBlob
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// ⚠⚠ §944 D2：ON CONFLICT **必须带归属守卫**。
	//
	//	asset_mirrors **没有**别的归属列（唯一的归属列 workspace_id 在
	//	SET 列表里 ⇒ 会被搬走）⇒ ⇒⇒ 所以「撞号」在这里就等于「换主人」
	//	⇒ ⇒⇒⇒ 守卫必须断言 workspace_id（代价：跨租户搬迁资产这条路被
	//	关掉 ⇒ 那是**产品口径**，已登记在简报待拍板，不在本节代拍）。
	//
	//	⚠⚠ **必须用 QueryRow + RETURNING，不能用 Exec**：
	//	守卫不成立时 PG **不报错**，只是**不返回行**（`Exec` 拿到的是一个
	//	成功的命令标签）⇒ ⇒ ⇒ 用 Exec 的话这道守卫就是**哑弹**
	//	（存在、但无声 ⇒ 等于没有 ⇒ §942/§943 连着踩过同族坑）。
	var mirroredID string
	if err := tx.QueryRow(ctx, `
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
	WHERE asset_mirrors.workspace_id = EXCLUDED.workspace_id
	RETURNING id
	`, m.ID, m.WorkspaceID, m.Kind, m.ClientRev, newServerRev, m.CipherTitle, m.CipherBlob, m.DeletedAt, m.UpdatedAt).Scan(&mirroredID); err != nil {
		// ErrNoRows ⇒ 守卫把「这个 asset_id 属于别的租户」挡掉了
		// ⇒ ⇒ ⇒ 绝不把别人的密文搬进自己的租户（宁可整笔回滚）。
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("push asset %s: %w", m.ID, ErrAssetScopeConflict)
		}
		return nil, err
	}
	res.ServerRev = newServerRev

	op := "push"
	if res.Conflict {
		op = "conflict"
	}
	if _, err := tx.Exec(ctx, `
	INSERT INTO asset_sync_log (workspace_id, asset_id, op, client_rev, server_rev, detail)
	VALUES ($1, $2, $3, $4, $5, $6)
	`, m.WorkspaceID, m.ID, op, m.ClientRev, newServerRev, ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
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
	ORDER BY server_rev ASC, id ASC
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
SELECT COALESCE(last_revision, 0) FROM asset_workspace_revisions WHERE workspace_id = $1
`, wsID).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return rev, err
}
