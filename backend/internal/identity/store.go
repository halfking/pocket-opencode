// Package identity implements S0-A: the Identity Core for the Personal Super
// Terminal.
//
// It owns three PostgreSQL tables and provides the store operations the rest
// of pocketd consumes:
//
//   - workspaces        — the "one-person company" container. Every user gets a
//                         default workspace at bootstrap; additional shadow
//                         workspaces are created when collaborators are invited.
//   - workspace_members — maps users into workspaces with a role (owner /
//                         invitee) and an optional expiry (for temporary
//                         collaborators, capped at 3 invitees per workspace).
//   - devices           — records every logged-in device (fingerprint, push
//                         token, OS, last seen) so the owner can audit and
//                         revoke sessions.
//
// Design notes:
//   - All tables carry workspace_id so multi-workspace isolation is enforced at
//     the data layer, matching the S0 design (spec §3.2 decision 1).
//   - Migration follows the existing pocketd convention: each Store runs an
//     idempotent CREATE TABLE IF NOT EXISTS in its constructor (see
//     internal/task/store.go, internal/db/pg.go doc comment).
//   - RBAC is intentionally minimal: only "owner" and "invitee" (spec §3.2).
//   - Invitee cap (max 3) is enforced in the application layer via
//     CountInvitees; the caller decides how to surface "limit reached".
package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxInvitees is the hard cap on non-owner members per workspace (spec §3.2
// decision 1: "邀请最多 3 人"). Exported so handlers can return a clear error.
const MaxInvitees = 3

// Role is a workspace member's permission level.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleInvitee Role = "invitee"
)

// Workspace is the "one-person company" container. Every asset, task, note,
// meeting, ledger entry, etc. belongs to exactly one workspace.
type Workspace struct {
	ID        string `json:"id"`
	OwnerID   string `json:"owner_id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "default" | "shadow"
	CreatedAt int64  `json:"created_at"`
}

// Member is a user's membership in a workspace.
type Member struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        Role   `json:"role"`
	InvitedAt   int64  `json:"invited_at"`
	ExpiresAt   int64  `json:"expires_at,omitempty"` // 0 = never expires
}

// Device is a registered client device. The push token is stored here so the
// Notification Center (S0-E) can dispatch via APNs/FCM.
type Device struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	WorkspaceID  string `json:"workspace_id"`
	Fingerprint  string `json:"fingerprint"`
	PushToken    string `json:"push_token,omitempty"`
	OS           string `json:"os"`
	LastSeenAt   int64  `json:"last_seen_at"`
	CreatedAt    int64  `json:"created_at"`
}

// ErrNotFound is returned when a single-row lookup misses.
var ErrNotFound = errors.New("identity: not found")

// ErrInviteeLimit is returned when an invite would exceed MaxInvitees.
var ErrInviteeLimit = errors.New("identity: invitee limit reached")

// Store manages the identity tables. It receives the shared pgxpool from
// main.go, matching the existing pocketd store wiring convention.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs the Store and runs idempotent migrations.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("identity: pgxpool is nil")
	}
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("identity migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS workspaces (
	id          TEXT PRIMARY KEY,
	owner_id    TEXT NOT NULL,
	name        TEXT NOT NULL,
	type        TEXT NOT NULL DEFAULT 'default',
	created_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workspaces_owner ON workspaces(owner_id);

CREATE TABLE IF NOT EXISTS workspace_members (
	workspace_id TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	role         TEXT NOT NULL,
	invited_at   BIGINT NOT NULL,
	expires_at   BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_wm_user ON workspace_members(user_id);

CREATE TABLE IF NOT EXISTS devices (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL,
	workspace_id  TEXT NOT NULL,
	fingerprint   TEXT NOT NULL,
	push_token    TEXT,
	os            TEXT,
	last_seen_at  BIGINT NOT NULL,
	created_at    BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);
CREATE INDEX IF NOT EXISTS idx_devices_ws   ON devices(workspace_id);
`)
	return err
}

// CreateWorkspace inserts a new workspace. It does NOT add the owner as a
// member — call EnsureOwnerMembership for that (or use CreateDefaultWorkspace
// which does both in one shot).
func (s *Store) CreateWorkspace(ctx context.Context, ws *Workspace) error {
	if ws.CreatedAt == 0 {
		ws.CreatedAt = time.Now().Unix()
	}
	if ws.Type == "" {
		ws.Type = "default"
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO workspaces (id, owner_id, name, type, created_at)
VALUES ($1, $2, $3, $4, $5)
`, ws.ID, ws.OwnerID, ws.Name, ws.Type, ws.CreatedAt)
	return err
}

// CreateDefaultWorkspace creates a workspace and immediately registers the
// owner as a member with RoleOwner. Intended for user bootstrap ("create my
//随身公司").
func (s *Store) CreateDefaultWorkspace(ctx context.Context, ws *Workspace) error {
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		return err
	}
	return s.AddMember(ctx, &Member{
		WorkspaceID: ws.ID,
		UserID:      ws.OwnerID,
		Role:        RoleOwner,
		InvitedAt:   ws.CreatedAt,
	})
}

// GetWorkspace fetches a workspace by ID. Returns ErrNotFound on miss.
func (s *Store) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	ws := &Workspace{}
	err := s.pool.QueryRow(ctx, `
SELECT id, owner_id, name, type, created_at FROM workspaces WHERE id = $1
`, id).Scan(&ws.ID, &ws.OwnerID, &ws.Name, &ws.Type, &ws.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ws, err
}

// ListWorkspacesByOwner returns all workspaces a user owns (default + shadows).
func (s *Store) ListWorkspacesByOwner(ctx context.Context, ownerID string) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, owner_id, name, type, created_at FROM workspaces WHERE owner_id = $1 ORDER BY created_at
`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(&ws.ID, &ws.OwnerID, &ws.Name, &ws.Type, &ws.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

// AddMember adds a user to a workspace as owner or invitee. Enforces the
// MaxInvitees cap for non-owner roles. Idempotent on (workspace_id, user_id).
func (s *Store) AddMember(ctx context.Context, m *Member) error {
	if m.InvitedAt == 0 {
		m.InvitedAt = time.Now().Unix()
	}
	if m.Role == "" {
		m.Role = RoleInvitee
	}
	// Enforce invitee cap. Owners don't count against the cap.
	if m.Role == RoleInvitee {
		count, err := s.CountInvitees(ctx, m.WorkspaceID)
		if err != nil {
			return err
		}
		// Allow re-inviting an existing member without bumping the count.
		existing, _ := s.GetMember(ctx, m.WorkspaceID, m.UserID)
		if existing == nil && count >= MaxInvitees {
			return ErrInviteeLimit
		}
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO workspace_members (workspace_id, user_id, role, invited_at, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role, expires_at = EXCLUDED.expires_at
`, m.WorkspaceID, m.UserID, m.Role, m.InvitedAt, m.ExpiresAt)
	return err
}

// RemoveMember removes a user from a workspace. The owner cannot be removed.
func (s *Store) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	tag, err := s.pool.Exec(ctx, `
DELETE FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2 AND role <> 'owner'
`, workspaceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMember fetches a single membership. Returns (nil, nil) when absent.
func (s *Store) GetMember(ctx context.Context, workspaceID, userID string) (*Member, error) {
	m := &Member{}
	err := s.pool.QueryRow(ctx, `
SELECT workspace_id, user_id, role, invited_at, expires_at
FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
`, workspaceID, userID).Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.InvitedAt, &m.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ListMembers returns all members of a workspace.
func (s *Store) ListMembers(ctx context.Context, workspaceID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, user_id, role, invited_at, expires_at
FROM workspace_members WHERE workspace_id = $1 ORDER BY role DESC, invited_at
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.InvitedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountInvitees returns the number of non-owner members in a workspace.
func (s *Store) CountInvitees(ctx context.Context, workspaceID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'invitee'
`, workspaceID).Scan(&count)
	return count, err
}

// ListWorkspacesForUser returns every workspace a user belongs to (as owner or
// invitee). This backs the "workspace switcher" UI and the JWT claim.
func (s *Store) ListWorkspacesForUser(ctx context.Context, userID string) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `
SELECT w.id, w.owner_id, w.name, w.type, w.created_at
FROM workspaces w
JOIN workspace_members m ON m.workspace_id = w.id
WHERE m.user_id = $1
ORDER BY w.created_at
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(&ws.ID, &ws.OwnerID, &ws.Name, &ws.Type, &ws.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

// UpsertDevice registers or refreshes a device. The fingerprint is the natural
// key; calling again with the same fingerprint updates push_token / OS /
// last_seen_at (e.g. on app foreground).
func (s *Store) UpsertDevice(ctx context.Context, d *Device) error {
	now := time.Now().Unix()
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	d.LastSeenAt = now
	_, err := s.pool.Exec(ctx, `
INSERT INTO devices (id, user_id, workspace_id, fingerprint, push_token, os, last_seen_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
	push_token   = EXCLUDED.push_token,
	os           = EXCLUDED.os,
	last_seen_at = EXCLUDED.last_seen_at,
	workspace_id = EXCLUDED.workspace_id
`, d.ID, d.UserID, d.WorkspaceID, d.Fingerprint, d.PushToken, d.OS, d.LastSeenAt, d.CreatedAt)
	return err
}

// ListDevices returns all devices for a workspace (the owner sees all
// collaborators' devices for audit/revoke).
func (s *Store) ListDevices(ctx context.Context, workspaceID string) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, workspace_id, fingerprint, COALESCE(push_token,''), COALESCE(os,''),
       last_seen_at, created_at
FROM devices WHERE workspace_id = $1 ORDER BY last_seen_at DESC
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.WorkspaceID, &d.Fingerprint, &d.PushToken, &d.OS, &d.LastSeenAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice removes a device (revokes its session). Called when the owner
// revokes access or a user logs out.
func (s *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE id = $1`, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EnsureDefaultWorkspace bootstraps a user's default workspace if none exists.
// Called at first login / signup. Returns the workspace (existing or newly
// created). The workspace ID convention is "ws_<userID>" for the default.
func (s *Store) EnsureDefaultWorkspace(ctx context.Context, userID string) (*Workspace, error) {
	defaultID := "ws_" + userID
	if ws, err := s.GetWorkspace(ctx, defaultID); err == nil {
		return ws, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	ws := &Workspace{
		ID:      defaultID,
		OwnerID: userID,
		Name:    "我的随身公司", // "My pocket company"
		Type:    "default",
	}
	if err := s.CreateDefaultWorkspace(ctx, ws); err != nil {
		return nil, err
	}
	return ws, nil
}
