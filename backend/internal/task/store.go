package task

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed task store (migrated from SQLite in Phase 0).
// It shares the pocketd Postgres pool with the other module stores.
type Store struct {
	pool *pgxpool.Pool
}

type SessionLink struct {
	TaskID     string `json:"taskId"`
	InstanceID string `json:"instanceId"`
	SessionID  string `json:"sessionId"`
	Role       string `json:"role"` // primary, supporting, exploratory, duplicate
}

// NewStore accepts the shared Postgres pool and runs idempotent migrations.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("task migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL,
		priority TEXT NOT NULL,
		workstream_id TEXT,
		source TEXT NOT NULL DEFAULT 'local',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		pending_approvals INTEGER DEFAULT 0,
		session_count INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS task_session_links (
		task_id TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		attached_at BIGINT NOT NULL,
		PRIMARY KEY (task_id, instance_id, session_id)
	);
	-- S0-A: workspace_id isolation (idempotent on existing DBs).
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	ALTER TABLE task_session_links ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	CREATE INDEX IF NOT EXISTS idx_tasks_workspace ON tasks(workspace_id);
	`)
	return err
}

// normalizeWorkspace applies the default tenant so an unset WorkspaceID never
// silently writes/reads NULL or an empty string.
func normalizeWorkspace(wsID string) string {
	if wsID == "" {
		return DefaultWorkspaceID
	}
	return wsID
}

// taskColumns is the shared SELECT list; workspace_id is included so the model
// round-trips its tenant instead of dropping it.
const taskColumns = `id, workspace_id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count`

// scanTask reads one row in taskColumns order.
func scanTask(row interface {
	Scan(dest ...any) error
}) (*Task, error) {
	t := &Task{}
	var createdAt, updatedAt int64
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.WorkstreamID, &t.Source, &createdAt, &updatedAt, &t.PendingApprovals, &t.SessionCount); err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	t.UpdatedAt = time.Unix(updatedAt, 0)
	return t, nil
}

func (s *Store) CreateTask(ctx context.Context, task *Task) error {
	now := time.Now().Unix()
	task.CreatedAt = time.Unix(now, 0)
	task.UpdatedAt = time.Unix(now, 0)
	if task.Source == "" {
		task.Source = "local"
	}
	task.WorkspaceID = normalizeWorkspace(task.WorkspaceID)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (id, workspace_id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, task.ID, task.WorkspaceID, task.Title, task.Description, task.Status, task.Priority, task.WorkstreamID, task.Source, now, now, task.PendingApprovals, task.SessionCount)

	return err
}

// GetTask fetches a task by ID with no tenant check.
//
// Deprecated: HTTP handlers must use GetTaskScoped.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	return t, nil
}

// GetTaskScoped fetches a task constrained to a workspace. A cross-tenant ID is
// reported the same as a missing one.
func (s *Store) GetTaskScoped(ctx context.Context, id, wsID string) (*Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1 AND workspace_id = $2`,
		id, normalizeWorkspace(wsID)))
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	return t, nil
}

// ListTasks returns every task across all tenants.
//
// Deprecated: HTTP handlers must use ListTasksScoped.
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	return s.queryTasks(ctx, `SELECT `+taskColumns+` FROM tasks ORDER BY updated_at DESC`)
}

// ListTasksScoped returns the tasks of one workspace.
func (s *Store) ListTasksScoped(ctx context.Context, wsID string) ([]Task, error) {
	return s.queryTasks(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE workspace_id = $1 ORDER BY updated_at DESC`,
		normalizeWorkspace(wsID))
}

func (s *Store) queryTasks(ctx context.Context, query string, args ...any) ([]Task, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}

	return tasks, rows.Err()
}

// AttachSession links a session to a task without a tenant check.
//
// Deprecated: HTTP handlers must use AttachSessionScoped so a session cannot be
// grafted onto another tenant's task.
func (s *Store) AttachSession(ctx context.Context, link SessionLink) error {
	return s.attachSession(ctx, link, "")
}

// AttachSessionScoped links a session to a task inside one workspace. The link
// row carries the same workspace_id, and the task must already belong to it.
func (s *Store) AttachSessionScoped(ctx context.Context, link SessionLink, wsID string) error {
	return s.attachSession(ctx, link, normalizeWorkspace(wsID))
}

func (s *Store) attachSession(ctx context.Context, link SessionLink, wsID string) error {
	now := time.Now().Unix()

	// When scoped, refuse up front if the task is not in this workspace.
	// Otherwise the link row would point at a task the caller cannot see.
	if wsID != "" {
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND workspace_id = $2)`,
			link.TaskID, wsID).Scan(&exists); err != nil {
			return fmt.Errorf("verify task workspace: %w", err)
		}
		if !exists {
			return fmt.Errorf("task not found: %s", link.TaskID)
		}
	}

	linkWS := wsID
	if linkWS == "" {
		linkWS = DefaultWorkspaceID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_session_links (task_id, workspace_id, instance_id, session_id, role, attached_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, instance_id, session_id) DO UPDATE SET role = EXCLUDED.role, attached_at = EXCLUDED.attached_at
	`, link.TaskID, linkWS, link.InstanceID, link.SessionID, link.Role, now)

	if err != nil {
		return err
	}

	// Update session_count
	_, err = s.pool.Exec(ctx, `
		UPDATE tasks SET session_count = (
			SELECT COUNT(*) FROM task_session_links WHERE task_id = $1
		), updated_at = $2 WHERE id = $3
	`, link.TaskID, now, link.TaskID)

	return err
}

// ListTasksCursor returns tasks with keyset pagination across all tenants.
//
// Deprecated: HTTP handlers must use ListTasksCursorScoped.
func (s *Store) ListTasksCursor(ctx context.Context, limit int, cursorCreatedAt int64, cursorID string) ([]Task, bool, error) {
	return s.listTasksCursor(ctx, "", limit, cursorCreatedAt, cursorID)
}

// ListTasksCursorScoped returns one workspace's tasks with keyset pagination.
// cursorCreatedAt/cursorID are from the last item of the previous page (0/"" for
// first page). Returns tasks + whether there are more items.
func (s *Store) ListTasksCursorScoped(ctx context.Context, wsID string, limit int, cursorCreatedAt int64, cursorID string) ([]Task, bool, error) {
	return s.listTasksCursor(ctx, normalizeWorkspace(wsID), limit, cursorCreatedAt, cursorID)
}

func (s *Store) listTasksCursor(ctx context.Context, wsID string, limit int, cursorCreatedAt int64, cursorID string) ([]Task, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	// Fetch limit+1 to detect hasMore
	query := `SELECT ` + taskColumns + ` FROM tasks`
	var args []interface{}
	var wheres []string
	argIdx := 1

	if wsID != "" {
		wheres = append(wheres, fmt.Sprintf("workspace_id = $%d", argIdx))
		args = append(args, wsID)
		argIdx++
	}
	if cursorCreatedAt > 0 && cursorID != "" {
		wheres = append(wheres, fmt.Sprintf(`((created_at < $%d) OR (created_at = $%d AND id < $%d))`,
			argIdx, argIdx, argIdx+1))
		args = append(args, cursorCreatedAt, cursorID)
		argIdx += 2
	}
	if len(wheres) > 0 {
		query += " WHERE " + joinStrings(wheres, " AND ")
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, argIdx)
	args = append(args, limit+1)

	tasks, err := s.queryTasks(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	return tasks, hasMore, nil
}

// ListSessionsForTask returns a task's session links without a tenant check.
//
// Deprecated: HTTP handlers must use ListSessionsForTaskScoped.
func (s *Store) ListSessionsForTask(ctx context.Context, taskID string) ([]SessionLink, error) {
	return s.listSessionsForTask(ctx, taskID, "")
}

// ListSessionsForTaskScoped returns a task's session links, constrained to a
// workspace. A task in another tenant yields an empty list rather than leaking
// its instance/session IDs.
func (s *Store) ListSessionsForTaskScoped(ctx context.Context, taskID, wsID string) ([]SessionLink, error) {
	return s.listSessionsForTask(ctx, taskID, normalizeWorkspace(wsID))
}

func (s *Store) listSessionsForTask(ctx context.Context, taskID, wsID string) ([]SessionLink, error) {
	query := `SELECT l.task_id, l.instance_id, l.session_id, l.role
		FROM task_session_links l WHERE l.task_id = $1`
	args := []interface{}{taskID}
	if wsID != "" {
		// Join through tasks so links written before S0-A (workspace_id
		// defaulted) are still filtered by their task's real owner.
		query = `SELECT l.task_id, l.instance_id, l.session_id, l.role
			FROM task_session_links l
			JOIN tasks t ON t.id = l.task_id
			WHERE l.task_id = $1 AND t.workspace_id = $2`
		args = append(args, wsID)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []SessionLink{}
	for rows.Next() {
		link := SessionLink{}
		if err := rows.Scan(&link.TaskID, &link.InstanceID, &link.SessionID, &link.Role); err != nil {
			return nil, err
		}
		links = append(links, link)
	}

	return links, rows.Err()
}

// UpdateTask updates a task's mutable fields without a tenant check.
//
// Deprecated: HTTP handlers must use UpdateTaskScoped.
func (s *Store) UpdateTask(ctx context.Context, id string, update TaskUpdate) (*Task, error) {
	return s.updateTask(ctx, id, "", update)
}

// UpdateTaskScoped updates a task's mutable fields (title, description, status,
// priority, workstream) constrained to a workspace. Only non-nil values in the
// update are applied; use an explicit empty string to clear.
func (s *Store) UpdateTaskScoped(ctx context.Context, id, wsID string, update TaskUpdate) (*Task, error) {
	return s.updateTask(ctx, id, normalizeWorkspace(wsID), update)
}

func (s *Store) updateTask(ctx context.Context, id, wsID string, update TaskUpdate) (*Task, error) {
	now := time.Now().Unix()

	// Build dynamic SET clause
	sets := []string{}
	args := []interface{}{}
	argIdx := 1

	if update.Title != nil {
		sets = append(sets, fmt.Sprintf("title = $%d", argIdx))
		args = append(args, *update.Title)
		argIdx++
	}
	if update.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *update.Description)
		argIdx++
	}
	if update.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, *update.Status)
		argIdx++
	}
	if update.Priority != nil {
		sets = append(sets, fmt.Sprintf("priority = $%d", argIdx))
		args = append(args, *update.Priority)
		argIdx++
	}
	if update.WorkstreamID != nil {
		sets = append(sets, fmt.Sprintf("workstream_id = $%d", argIdx))
		args = append(args, *update.WorkstreamID)
		argIdx++
	}

	// reread returns the post-update row through the same tenant boundary the
	// caller used, so a scoped update never echoes a foreign task back.
	reread := func() (*Task, error) {
		if wsID != "" {
			return s.GetTaskScoped(ctx, id, wsID)
		}
		return s.GetTask(ctx, id)
	}

	if len(sets) == 0 {
		// Nothing to update; return current task
		return reread()
	}

	// Always bump updated_at
	sets = append(sets, fmt.Sprintf("updated_at = $%d", argIdx))
	args = append(args, now)
	argIdx++

	// WHERE id = $N [AND workspace_id = $N+1]
	args = append(args, id)
	where := fmt.Sprintf("id = $%d", argIdx)
	argIdx++
	if wsID != "" {
		args = append(args, wsID)
		where += fmt.Sprintf(" AND workspace_id = $%d", argIdx)
	}

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE %s",
		joinStrings(sets, ", "), where)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	return reread()
}

// DeleteTask removes a task and its session links without a tenant check.
//
// Deprecated: HTTP handlers must use DeleteTaskScoped.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	return s.deleteTask(ctx, id, "")
}

// DeleteTaskScoped removes a task and its session links, constrained to a
// workspace. The task rows are deleted first so a cross-tenant call cannot
// destroy another workspace's links.
func (s *Store) DeleteTaskScoped(ctx context.Context, id, wsID string) error {
	return s.deleteTask(ctx, id, normalizeWorkspace(wsID))
}

func (s *Store) deleteTask(ctx context.Context, id, wsID string) error {
	// Delete the task first (it carries the tenant column). If it is not in
	// this workspace, nothing is removed and the links stay untouched.
	query := `DELETE FROM tasks WHERE id = $1`
	args := []interface{}{id}
	if wsID != "" {
		query += ` AND workspace_id = $2`
		args = append(args, wsID)
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM task_session_links WHERE task_id = $1`, id); err != nil {
		return fmt.Errorf("delete task sessions: %w", err)
	}
	return nil
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

func (s *Store) Close() error {
	// Pool is shared and closed by main.go; no-op here.
	return nil
}
