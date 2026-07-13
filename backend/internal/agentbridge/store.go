// Package agentbridge implements S0-D: the unified Agent Bridge for the
// Personal Super Terminal.
//
// It wraps the existing opencode adapter / registry / task store into a single
// "Agent" abstraction so S1-Task can dispatch work to remote opencode CLI
// instances without each caller repeating the create-session + send-prompt +
// attach-task dance.
//
// What lives here (S0 scope):
//   - Agent entity: id, workspace_id, instance_id, name, role, status,
//     capabilities[]. Persisted in the `agents` table (PG).
//   - Store: CRUD + list-by-workspace.
//   - Bridge: Send(agentID, prompt, opts) → creates a session on the agent's
//     underlying instance, sends the prompt, and (the S0 fix) auto-attaches
//     the new session to task_session_links when a task_id is supplied. This
//     closes the gap flagged in spec §6.2.
//
// What is OUT of S0 scope (deferred to S1-Task):
//   - Multi-agent DAG orchestration (planner → developer → tester → reviewer).
//     S0 only provides single-agent dispatch; the DAG layer composes on top.
//   - ACP-over-HTTP gateway. S0-D uses the existing opencode HTTP adapter
//     (CreateSession + SendPrompt). ACP wiring is a later sprint once we
//     confirm the protocol shape against opencode CLI's `acp` subcommand.
package agentbridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxAgentsPerWorkspace caps how many agents a workspace can register. Keeps
// the "one-person company" mental model manageable.
const MaxAgentsPerWorkspace = 16

// Role is the agent's job within a task DAG. S0 only uses "generic"; S1-Task
// introduces planner/developer/tester/reviewer.
type Role string

const (
	RoleGeneric    Role = "generic"    // S0 default — no special DAG semantics
	RolePlanner    Role = "planner"    // S1
	RoleDeveloper  Role = "developer"  // S1
	RoleTester     Role = "tester"     // S1
	RoleReviewer   Role = "reviewer"   // S1
)

// Status is the agent's current reachability.
type Status string

const (
	StatusUnknown  Status = "unknown"
	StatusOnline   Status = "online"
	StatusOffline  Status = "offline"
	StatusBusy     Status = "busy"
)

// Agent is a registered remote opencode instance bound to a workspace.
//
// InstanceID references a PocketInstance in the registry (the actual HTTP
// endpoint). The Bridge resolves instance_id → API base URL at dispatch time
// via the InstanceResolver dependency, so this package doesn't import registry.
type Agent struct {
	ID           string   `json:"id"`
	WorkspaceID  string   `json:"workspace_id"`
	InstanceID   string   `json:"instance_id"` // registry instance id
	Name         string   `json:"name"`
	Role         Role     `json:"role"`
	Status       Status   `json:"status"`
	Capabilities []string `json:"capabilities"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
}

// ErrNotFound is returned on single-row miss.
var ErrNotFound = errors.New("agentbridge: agent not found")

// ErrLimitReached is returned when MaxAgentsPerWorkspace is exceeded.
var ErrLimitReached = errors.New("agentbridge: agent limit reached for workspace")

// Store manages the agents table.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs the Store and runs idempotent migrations.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("agentbridge: pgxpool is nil")
	}
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("agentbridge migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS agents (
	id            TEXT PRIMARY KEY,
	workspace_id  TEXT NOT NULL DEFAULT 'default',
	instance_id   TEXT NOT NULL,
	name          TEXT NOT NULL,
	role          TEXT NOT NULL DEFAULT 'generic',
	status        TEXT NOT NULL DEFAULT 'unknown',
	capabilities  JSONB DEFAULT '[]',
	created_at    BIGINT NOT NULL,
	updated_at    BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_ws ON agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_agents_instance ON agents(instance_id);
`)
	return err
}

// Create inserts a new agent. Enforces MaxAgentsPerWorkspace.
func (s *Store) Create(ctx context.Context, a *Agent) error {
	if a.WorkspaceID == "" {
		a.WorkspaceID = "default"
	}
	if a.Role == "" {
		a.Role = RoleGeneric
	}
	if a.Status == "" {
		a.Status = StatusUnknown
	}
	if a.Capabilities == nil {
		a.Capabilities = []string{}
	}
	now := time.Now().Unix()
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	// Cap check.
	count, err := s.CountByWorkspace(ctx, a.WorkspaceID)
	if err != nil {
		return err
	}
	if count >= MaxAgentsPerWorkspace {
		return ErrLimitReached
	}

	caps := fmt.Sprintf("[%s]", joinQuoted(a.Capabilities))
	_, err = s.pool.Exec(ctx, `
INSERT INTO agents (id, workspace_id, instance_id, name, role, status, capabilities, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
`, a.ID, a.WorkspaceID, a.InstanceID, a.Name, a.Role, a.Status, caps, a.CreatedAt, a.UpdatedAt)
	return err
}

// Get fetches one agent.
func (s *Store) Get(ctx context.Context, id string) (*Agent, error) {
	a := &Agent{}
	var caps []byte
	err := s.pool.QueryRow(ctx, `
SELECT id, workspace_id, instance_id, name, role, status, capabilities, created_at, updated_at
FROM agents WHERE id = $1
`, id).Scan(&a.ID, &a.WorkspaceID, &a.InstanceID, &a.Name, &a.Role, &a.Status, &caps, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	a.Capabilities = parseCaps(caps)
	return a, err
}

// ListByWorkspace returns all agents in a workspace.
func (s *Store) ListByWorkspace(ctx context.Context, wsID string) ([]Agent, error) {
	if wsID == "" {
		wsID = "default"
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, instance_id, name, role, status, capabilities, created_at, updated_at
FROM agents WHERE workspace_id = $1 ORDER BY created_at
`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var caps []byte
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.InstanceID, &a.Name, &a.Role, &a.Status, &caps, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Capabilities = parseCaps(caps)
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateStatus refreshes an agent's status (online/offline/busy). Called by
// the Bridge after a dispatch attempt or by a health poller.
func (s *Store) UpdateStatus(ctx context.Context, id string, status Status) error {
	_, err := s.pool.Exec(ctx, `
UPDATE agents SET status = $1, updated_at = $2 WHERE id = $3
`, status, time.Now().Unix(), id)
	return err
}

// Delete removes an agent registration (does not touch the underlying instance).
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountByWorkspace returns the agent count for cap enforcement.
func (s *Store) CountByWorkspace(ctx context.Context, wsID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM agents WHERE workspace_id = $1`, wsID).Scan(&count)
	return count, err
}

// ---- helpers ----

// joinQuoted turns ["a","b"] into `"a","b"` for JSONB cast. Empty → empty.
func joinQuoted(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += `"` + jsonEscape(s) + `"`
	}
	return out
}

func jsonEscape(s string) string {
	// Minimal JSON string escaping for capability tags (alphanumeric in practice).
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

func parseCaps(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	// Light parse: strip [ ] and split on , — capabilities are simple tags.
	s := string(raw)
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return []string{}
	}
	parts := splitCSV(s)
	for i, p := range parts {
		parts[i] = unquote(p)
	}
	return parts
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func unquote(s string) string {
	s = trimSpaces(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
