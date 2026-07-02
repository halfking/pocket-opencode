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

func (s *Store) Close() error {
	// Pool is shared and closed by main.go; no-op here.
	return nil
}
