package scheduledtask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the shared pgxpool with scheduled-task-specific CRUD. It is
// safe for concurrent use; every method is nil-safe (a nil pool short-
// circuits to ErrStoreUnavailable so callers can gracefully degrade when
// Postgres is not configured).
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store and runs the idempotent schema migration. Returns
// ErrStoreUnavailable when pool is nil — callers (cmd/pocketd/main.go) treat
// this as "remote-only mode" and skip the entire subsystem.
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrStoreUnavailable
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("scheduledtask migrate: %w", err)
	}
	return s, nil
}

// Available reports whether the store can serve queries. Used by main.go to
// decide whether to construct a Scheduler.
func (s *Store) Available() bool { return s != nil && s.pool != nil }

// migrate creates both tables and indexes idempotently. Schema mirrors the
// S0-A convention used by other module stores (workspace_id + user_id on
// every row, JSONB for payloads, BIGINT epoch seconds for timestamps).
func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL DEFAULT 'default',
    user_id         TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL,
    schedule_expr   TEXT NOT NULL,
    timezone        TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at     BIGINT NOT NULL DEFAULT 0,
    lease_until     BIGINT NOT NULL DEFAULT 0,
    last_run_at     BIGINT NOT NULL DEFAULT 0,
    last_status     TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    run_count       INTEGER NOT NULL DEFAULT 0,
    max_runs        INTEGER NOT NULL DEFAULT 0,
    cooldown_sec    INTEGER NOT NULL DEFAULT 0,
    timeout_sec     INTEGER NOT NULL DEFAULT 120,
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL
);

ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS lease_until BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_due
    ON scheduled_tasks(next_run_at) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_owner
    ON scheduled_tasks(user_id, workspace_id);

CREATE TABLE IF NOT EXISTS scheduled_task_runs (
    id                  TEXT PRIMARY KEY,
    task_id             TEXT NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    workspace_id        TEXT NOT NULL DEFAULT 'default',
    user_id             TEXT NOT NULL,
    status              TEXT NOT NULL,
    started_at          BIGINT NOT NULL,
    finished_at         BIGINT NOT NULL DEFAULT 0,
    duration_ms         INTEGER NOT NULL DEFAULT 0,
    output              JSONB NOT NULL DEFAULT '{}'::jsonb,
    error               TEXT NOT NULL DEFAULT '',
    referenced_task_id  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_task ON scheduled_task_runs(task_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_owner ON scheduled_task_runs(user_id, workspace_id, started_at DESC);
`)
	return err
}

// --- Task CRUD ---

// CreateTask inserts a new row and returns the persisted Task (with
// GeneratedAt timestamps). The caller is responsible for setting ID, Owner
// fields, and computing NextRunAt via Schedule.ComputeNext.
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if t.ID == "" {
		return errors.New("scheduledtask: CreateTask requires ID")
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = unixNow()
	}
	t.UpdatedAt = unixNow()
	if t.Payload == nil {
		t.Payload = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO scheduled_tasks (
    id, workspace_id, user_id, name, description, kind,
    schedule_kind, schedule_expr, timezone, payload, enabled,
    next_run_at, lease_until, last_run_at, last_status, last_error, run_count,
    max_runs, cooldown_sec, timeout_sec, created_at, updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
    $12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22
	)`,
		t.ID, t.WorkspaceID, t.UserID, t.Name, t.Description, string(t.Kind),
		string(t.ScheduleKind), t.ScheduleExpr, t.Timezone, t.Payload, t.Enabled,
		t.NextRunAt, t.LeaseUntil, t.LastRunAt, string(t.LastStatus), t.LastError, t.RunCount,
		t.MaxRuns, t.CooldownSec, t.TimeoutSec, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

// GetTaskScoped returns the task only if it belongs to (userID, workspaceID).
// Cross-tenant reads always fail closed.
func (s *Store) GetTaskScoped(ctx context.Context, id, userID, workspaceID string) (*Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
	SELECT id, workspace_id, user_id, name, description, kind,
	       schedule_kind, schedule_expr, timezone, payload, enabled,
	       next_run_at, lease_until, last_run_at, last_status, last_error, run_count,
	       max_runs, cooldown_sec, timeout_sec, created_at, updated_at
  FROM scheduled_tasks
 WHERE id = $1 AND user_id = $2 AND workspace_id = $3
`, id, userID, workspaceID)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	return t, err
}

// ListTasksScoped returns all tasks owned by (userID, workspaceID), newest
// first. enabledOnly filters to enabled rows only.
func (s *Store) ListTasksScoped(ctx context.Context, userID, workspaceID string, enabledOnly bool, limit int) ([]*Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `
	SELECT id, workspace_id, user_id, name, description, kind,
	       schedule_kind, schedule_expr, timezone, payload, enabled,
	       next_run_at, lease_until, last_run_at, last_status, last_error, run_count,
	       max_runs, cooldown_sec, timeout_sec, created_at, updated_at
  FROM scheduled_tasks
 WHERE user_id = $1 AND workspace_id = $2`
	args := []any{userID, workspaceID}
	if enabledOnly {
		q += ` AND enabled = TRUE`
	}
	q += ` ORDER BY created_at DESC LIMIT $3`
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTaskScoped applies a partial update. Only non-zero fields in patch
// are written. Returns ErrTaskNotFound when no row matches the owner tuple.
func (s *Store) UpdateTaskScoped(ctx context.Context, id, userID, workspaceID string, patch TaskInput, nextRunAt int64) (*Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	patch.Defaults()
	current, err := s.GetTaskScoped(ctx, id, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if patch.Name != "" {
		current.Name = patch.Name
	}
	if patch.Description != "" {
		current.Description = patch.Description
	}
	if patch.Kind != "" {
		current.Kind = patch.Kind
	}
	if patch.ScheduleKind != "" {
		current.ScheduleKind = patch.ScheduleKind
	}
	if patch.ScheduleExpr != "" {
		current.ScheduleExpr = patch.ScheduleExpr
	}
	if patch.Timezone != "" {
		current.Timezone = patch.Timezone
	}
	if len(patch.Payload) > 0 {
		current.Payload = patch.Payload
	}
	if patch.Enabled != nil {
		current.Enabled = *patch.Enabled
	}
	current.MaxRuns = patch.MaxRuns
	current.CooldownSec = patch.CooldownSec
	current.TimeoutSec = patch.TimeoutSec
	current.NextRunAt = nextRunAt
	current.UpdatedAt = unixNow()
	_, err = s.pool.Exec(ctx, `
UPDATE scheduled_tasks SET
    name = $1, description = $2, kind = $3,
    schedule_kind = $4, schedule_expr = $5, timezone = $6, payload = $7,
    enabled = $8, max_runs = $9, cooldown_sec = $10, timeout_sec = $11,
    next_run_at = $12, updated_at = $13
 WHERE id = $14 AND user_id = $15 AND workspace_id = $16
`,
		current.Name, current.Description, string(current.Kind),
		string(current.ScheduleKind), current.ScheduleExpr, current.Timezone, current.Payload,
		current.Enabled, current.MaxRuns, current.CooldownSec, current.TimeoutSec,
		current.NextRunAt, current.UpdatedAt,
		current.ID, current.UserID, current.WorkspaceID,
	)
	if err != nil {
		return nil, err
	}
	return current, nil
}

// DeleteTaskScoped removes a task (cascading to runs via FK).
func (s *Store) DeleteTaskScoped(ctx context.Context, id, userID, workspaceID string) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id = $1 AND user_id = $2 AND workspace_id = $3`,
		id, userID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// --- Scheduler-facing queries ---

// ClaimDue atomically picks due tasks, reserves a bounded lease, and returns
// them. A crashed dispatcher can therefore be recovered after the lease while
// long-running tasks retain a lease at least as long as their configured
// timeout. The caller is expected to call
// FinishRun + UpdateTaskAfterRun for each claimed row.
//
// `nowSec` is the cutoff (unix seconds); rows with next_run_at <= nowSec and
// enabled = TRUE are claimed. `limit` caps how many rows are returned per
// scan. The advisory lock is per-scan (not per-row) so two pocketd instances
// taking turns on the same minute will not both claim the same row in the
// same instant — for stronger guarantees callers should also wrap the
// scheduler tick in a single pg_advisory_lock with the same key.
//
// We use UPDATE ... RETURNING rather than SELECT ... FOR UPDATE SKIP LOCKED
// because we also need to bump next_run_at to "in-flight" so a manual
// TriggerNow cannot race with the scheduler.
func (s *Store) ClaimDue(ctx context.Context, nowSec int64, limit int) ([]*Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	if limit <= 0 {
		limit = 32
	}
	// Bump next_run_at by a bounded lease so the same row cannot be claimed
	// again until the dispatcher rewrites it, while still recovering after a
	// crash.
	// Keep each lease longer than that task's configured timeout, with a
	// five-minute minimum for crash recovery. Computing this in SQL avoids
	// using one global lease that is either too short or too long.
	rows, err := s.pool.Query(ctx, `
WITH candidates AS (
    SELECT id
      FROM scheduled_tasks
     WHERE enabled = TRUE
       AND (next_run_at > 0 AND next_run_at <= $1 OR lease_until > 0 AND lease_until <= $1)
       AND (max_runs = 0 OR run_count < max_runs)
     ORDER BY next_run_at, id
     FOR UPDATE SKIP LOCKED
     LIMIT $2
)
UPDATE scheduled_tasks AS t
   SET next_run_at = $1 + GREATEST(300, t.timeout_sec + 60),
       lease_until = $1 + GREATEST(300, t.timeout_sec + 60)
  FROM candidates AS c
 WHERE t.id = c.id
RETURNING t.id, t.workspace_id, t.user_id, t.name, t.description, t.kind,
          t.schedule_kind, t.schedule_expr, t.timezone, t.payload, t.enabled,
          t.next_run_at, t.lease_until, t.last_run_at, t.last_status, t.last_error, t.run_count,
          t.max_runs, t.cooldown_sec, t.timeout_sec, t.created_at, t.updated_at
	`, nowSec, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTaskAfterRun records the terminal state of a run on the task row
// and writes the next due time. nextRunAt = 0 disables the task (e.g. one-shot
// `at` tasks that hit max_runs).
func (s *Store) UpdateTaskAfterRun(ctx context.Context, taskID string, status RunStatus, errMsg string, nextRunAt int64) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	_, err := s.pool.Exec(ctx, `
UPDATE scheduled_tasks SET
    last_run_at = $1, last_status = $2, last_error = $3,
    run_count = run_count + 1,
    next_run_at = $4,
    lease_until = 0,
    enabled = CASE WHEN $4 = 0 THEN FALSE ELSE enabled END,
    updated_at = $1
 WHERE id = $5
`, unixNow(), string(status), errMsg, nextRunAt, taskID)
	return err
}

// --- Runs ---

// InsertRun records the start of a Run in `running` state.
func (s *Store) InsertRun(ctx context.Context, r *Run) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.StartedAt == 0 {
		r.StartedAt = unixNow()
	}
	if r.Output == nil {
		r.Output = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO scheduled_task_runs (
    id, task_id, workspace_id, user_id, status, started_at,
    finished_at, duration_ms, output, error, referenced_task_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
`,
		r.ID, r.TaskID, r.WorkspaceID, r.UserID, string(r.Status), r.StartedAt,
		0, 0, r.Output, r.Error, r.ReferencedTaskID,
	)
	return err
}

// FinishRun transitions a Run to a terminal status, computes duration, and
// stores the executor output.
func (s *Store) FinishRun(ctx context.Context, runID string, status RunStatus, output json.RawMessage, errMsg string, referencedTaskID string) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if output == nil {
		output = json.RawMessage(`{}`)
	}
	now := unixNow()
	_, err := s.pool.Exec(ctx, `
UPDATE scheduled_task_runs SET
    status = $1, finished_at = $2,
    duration_ms = ($2 - started_at) * 1000,
    output = $3, error = $4, referenced_task_id = $5
 WHERE id = $6
`, string(status), now, output, errMsg, referencedTaskID, runID)
	return err
}

// ListRuns returns runs for a task, newest first.
func (s *Store) ListRuns(ctx context.Context, taskID, userID, workspaceID string, limit int) ([]*Run, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, task_id, workspace_id, user_id, status, started_at,
       finished_at, duration_ms, output, error, referenced_task_id
  FROM scheduled_task_runs
 WHERE task_id = $1 AND user_id = $2 AND workspace_id = $3
 ORDER BY started_at DESC
 LIMIT $4
`, taskID, userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- scanning helpers ---

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(r scanner) (*Task, error) {
	var t Task
	var kind, skind, lstatus string
	if err := r.Scan(
		&t.ID, &t.WorkspaceID, &t.UserID, &t.Name, &t.Description, &kind,
		&skind, &t.ScheduleExpr, &t.Timezone, &t.Payload, &t.Enabled,
		&t.NextRunAt, &t.LeaseUntil, &t.LastRunAt, &lstatus, &t.LastError, &t.RunCount,
		&t.MaxRuns, &t.CooldownSec, &t.TimeoutSec, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	t.Kind = Kind(kind)
	t.ScheduleKind = ScheduleKind(skind)
	t.LastStatus = RunStatus(lstatus)
	return &t, nil
}

func scanRun(r scanner) (*Run, error) {
	var run Run
	var status string
	if err := r.Scan(
		&run.ID, &run.TaskID, &run.WorkspaceID, &run.UserID, &status, &run.StartedAt,
		&run.FinishedAt, &run.DurationMs, &run.Output, &run.Error, &run.ReferencedTaskID,
	); err != nil {
		return nil, err
	}
	run.Status = RunStatus(status)
	return &run, nil
}

// NewID returns a 16-byte hex string. Used for Task and Run primary keys.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to time-based ID (never expected to fail in practice).
		now := time.Now().UnixNano()
		return fmt.Sprintf("ts-%016x", now)
	}
	return hex.EncodeToString(b[:])
}

// AdvisoryKey returns a stable int64 hash of taskID, suitable for
// pg_advisory_xact_lock in callers that want per-task locks. Stored as a
// helper to keep the lock key computation in one place.
func AdvisoryKey(taskID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(taskID))
	return int64(h.Sum64())
}
