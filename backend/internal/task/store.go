package task

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type SessionLink struct {
	TaskID     string `json:"taskId"`
	InstanceID string `json:"instanceId"`
	SessionID  string `json:"sessionId"`
	Role       string `json:"role"` // primary, supporting, exploratory, duplicate
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db failed: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db failed: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migrate failed: %w", err)
	}

	return store, nil
}

func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL,
		priority TEXT NOT NULL,
		workstream_id TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		pending_approvals INTEGER DEFAULT 0,
		session_count INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS task_session_links (
		task_id TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		attached_at INTEGER NOT NULL,
		PRIMARY KEY (task_id, instance_id, session_id)
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) CreateTask(ctx context.Context, task *Task) error {
	now := time.Now().Unix()
	task.CreatedAt = time.Unix(now, 0)
	task.UpdatedAt = time.Unix(now, 0)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, title, description, status, priority, workstream_id, created_at, updated_at, pending_approvals, session_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.Title, task.Description, task.Status, task.Priority, task.WorkstreamID, now, now, task.PendingApprovals, task.SessionCount)

	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	task := &Task{}
	var createdAt, updatedAt int64

	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, description, status, priority, workstream_id, created_at, updated_at, pending_approvals, session_count
		FROM tasks WHERE id = ?
	`, id).Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.WorkstreamID, &createdAt, &updatedAt, &task.PendingApprovals, &task.SessionCount)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found")
	}
	if err != nil {
		return nil, err
	}

	task.CreatedAt = time.Unix(createdAt, 0)
	task.UpdatedAt = time.Unix(updatedAt, 0)
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, description, status, priority, workstream_id, created_at, updated_at, pending_approvals, session_count
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
		if err := rows.Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.WorkstreamID, &createdAt, &updatedAt, &task.PendingApprovals, &task.SessionCount); err != nil {
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
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO task_session_links (task_id, instance_id, session_id, role, attached_at)
		VALUES (?, ?, ?, ?, ?)
	`, link.TaskID, link.InstanceID, link.SessionID, link.Role, now)

	if err != nil {
		return err
	}

	// Update session_count
	_, err = s.db.ExecContext(ctx, `
		UPDATE tasks SET session_count = (
			SELECT COUNT(*) FROM task_session_links WHERE task_id = ?
		), updated_at = ? WHERE id = ?
	`, link.TaskID, now, link.TaskID)

	return err
}

func (s *Store) ListSessionsForTask(ctx context.Context, taskID string) ([]SessionLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, instance_id, session_id, role FROM task_session_links WHERE task_id = ?
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
	return s.db.Close()
}
