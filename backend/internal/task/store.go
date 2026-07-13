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

func (s *Store) CreateTask(ctx context.Context, task *Task) error {
	now := time.Now().Unix()
	task.CreatedAt = time.Unix(now, 0)
	task.UpdatedAt = time.Unix(now, 0)
	if task.Source == "" {
		task.Source = "local"
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, task.ID, task.Title, task.Description, task.Status, task.Priority, task.WorkstreamID, task.Source, now, now, task.PendingApprovals, task.SessionCount)

	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	task := &Task{}
	var createdAt, updatedAt int64

	err := s.pool.QueryRow(ctx, `
		SELECT id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count
		FROM tasks WHERE id = $1
	`, id).Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.WorkstreamID, &task.Source, &createdAt, &updatedAt, &task.PendingApprovals, &task.SessionCount)

	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	task.CreatedAt = time.Unix(createdAt, 0)
	task.UpdatedAt = time.Unix(updatedAt, 0)
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count
		FROM tasks ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		task := Task{}
		var createdAt, updatedAt int64
		if err := rows.Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.WorkstreamID, &task.Source, &createdAt, &updatedAt, &task.PendingApprovals, &task.SessionCount); err != nil {
			return nil, err
		}
		task.CreatedAt = time.Unix(createdAt, 0)
		task.UpdatedAt = time.Unix(updatedAt, 0)
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

func (s *Store) AttachSession(ctx context.Context, link SessionLink) error {
	now := time.Now().Unix()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_session_links (task_id, instance_id, session_id, role, attached_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (task_id, instance_id, session_id) DO UPDATE SET role = EXCLUDED.role, attached_at = EXCLUDED.attached_at
	`, link.TaskID, link.InstanceID, link.SessionID, link.Role, now)

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

// ListTasksCursor returns tasks with keyset pagination.
// cursorCreatedAt/cursorID are from the last item of the previous page (0/"" for first page).
// Returns tasks + whether there are more items.
func (s *Store) ListTasksCursor(ctx context.Context, limit int, cursorCreatedAt int64, cursorID string) ([]Task, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	// Fetch limit+1 to detect hasMore
	query := `SELECT id, title, description, status, priority, workstream_id, source, created_at, updated_at, pending_approvals, session_count
		FROM tasks`
	var args []interface{}
	argIdx := 1

	if cursorCreatedAt > 0 && cursorID != "" {
		query += fmt.Sprintf(` WHERE (created_at < $%d) OR (created_at = $%d AND id < $%d)`,
			argIdx, argIdx, argIdx+1)
		args = append(args, cursorCreatedAt, cursorID)
		argIdx += 2
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		task := Task{}
		var createdAt, updatedAt int64
		if err := rows.Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.WorkstreamID, &task.Source, &createdAt, &updatedAt, &task.PendingApprovals, &task.SessionCount); err != nil {
			return nil, false, err
		}
		task.CreatedAt = time.Unix(createdAt, 0)
		task.UpdatedAt = time.Unix(updatedAt, 0)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	return tasks, hasMore, nil
}

func (s *Store) ListSessionsForTask(ctx context.Context, taskID string) ([]SessionLink, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT task_id, instance_id, session_id, role FROM task_session_links WHERE task_id = $1
	`, taskID)
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

// UpdateTask updates a task's mutable fields (title, description, status, priority).
// Only non-zero values in the update are applied; use explicit empty string to clear.
func (s *Store) UpdateTask(ctx context.Context, id string, update TaskUpdate) (*Task, error) {
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

	if len(sets) == 0 {
		// Nothing to update; return current task
		return s.GetTask(ctx, id)
	}

	// Always bump updated_at
	sets = append(sets, fmt.Sprintf("updated_at = $%d", argIdx))
	args = append(args, now)
	argIdx++

	// WHERE id = $N
	args = append(args, id)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = $%d",
		joinStrings(sets, ", "), argIdx)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	return s.GetTask(ctx, id)
}

// DeleteTask removes a task and its session links.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	// 先删关联
	_, err := s.pool.Exec(ctx, `DELETE FROM task_session_links WHERE task_id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task sessions: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task not found: %s", id)
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
