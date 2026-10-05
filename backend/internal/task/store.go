package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed task store (migrated from SQLite in Phase 0).
// It shares the pocketd Postgres pool with the other module stores.
type Store struct {
	pool *pgxpool.Pool
}

var ErrPendingApprovals = errors.New("task has pending approvals")

type SessionLink struct {
	TaskID     string `json:"taskId"`
	InstanceID string `json:"instanceId"`
	SessionID  string `json:"sessionId"`
	Role       string `json:"role"` // primary, supporting, exploratory, duplicate
}

// TaskRunBinding is the durable association between a Pocket task and the
// canonical ACC run that owns its execution. Workspace is part of every key.
type TaskRunBinding struct {
	WorkspaceID string    `json:"workspace_id"`
	TaskID      string    `json:"task_id"`
	RunID       string    `json:"run_id"`
	OperationID string    `json:"operation_id,omitempty"`
	TenantID    string    `json:"tenant_id"`
	Watermark   uint64    `json:"watermark"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewStore accepts the shared Postgres pool and runs idempotent migrations.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("task migrate: %w", err)
	}
	return s, nil
}

func validateBinding(b TaskRunBinding) error {
	if strings.TrimSpace(b.WorkspaceID) == "" || strings.TrimSpace(b.TaskID) == "" || strings.TrimSpace(b.RunID) == "" || strings.TrimSpace(b.TenantID) == "" {
		return errors.New("task run binding requires workspace, task, run, and tenant")
	}
	return nil
}

// PutTaskRunBinding is retained only for compatibility; unverified bindings are forbidden.
func (s *Store) PutTaskRunBinding(ctx context.Context, binding TaskRunBinding) error {
	return errors.New("unverified task run binding rejected; use BindVerifiedRun")
}

func (s *Store) GetTaskRunBinding(ctx context.Context, workspaceID, taskID string) (*TaskRunBinding, error) {
	var b TaskRunBinding
	var ts int64
	err := s.pool.QueryRow(ctx, `SELECT workspace_id,task_id,run_id,operation_id,tenant_id,watermark,created_at FROM task_run_bindings WHERE workspace_id=$1 AND task_id=$2`, normalizeWorkspace(workspaceID), taskID).Scan(&b.WorkspaceID, &b.TaskID, &b.RunID, &b.OperationID, &b.TenantID, &b.Watermark, &ts)
	if err != nil {
		return nil, err
	}
	b.CreatedAt = time.Unix(ts, 0).UTC()
	return &b, nil
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
		CREATE TABLE IF NOT EXISTS task_run_bindings (
			workspace_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			operation_id TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			PRIMARY KEY (workspace_id, task_id),
			UNIQUE (workspace_id, run_id)
		);
			ALTER TABLE task_run_bindings ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE task_run_bindings ADD COLUMN IF NOT EXISTS watermark BIGINT NOT NULL DEFAULT 0;
			ALTER TABLE task_run_bindings DROP CONSTRAINT IF EXISTS task_run_bindings_workspace_id_run_id_key;
			CREATE INDEX IF NOT EXISTS idx_task_run_bindings_run ON task_run_bindings(workspace_id, run_id);
			CREATE TABLE IF NOT EXISTS pocket_run_events (tenant_id TEXT NOT NULL, workspace_id TEXT NOT NULL, run_id TEXT NOT NULL, event_id TEXT NOT NULL, event_type TEXT NOT NULL, task_id TEXT NOT NULL, sequence BIGINT NOT NULL, raw JSONB NOT NULL, PRIMARY KEY (workspace_id, run_id, sequence), UNIQUE (workspace_id, run_id, event_id));
			CREATE TABLE IF NOT EXISTS pocket_run_cursors (workspace_id TEXT NOT NULL, task_id TEXT NOT NULL, user_id TEXT NOT NULL, consumer_id TEXT NOT NULL, sequence BIGINT NOT NULL, PRIMARY KEY(workspace_id, task_id, user_id, consumer_id));

	-- S0-A: workspace_id isolation (idempotent on existing DBs).
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	ALTER TABLE task_session_links ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	CREATE INDEX IF NOT EXISTS idx_tasks_workspace ON tasks(workspace_id);

	-- Acceptance evidence (idempotent on existing DBs). All four columns are
	-- nullable; status='accepted' is the only writer of accepted_at/_by/bundle.
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS accepted_at BIGINT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS accepted_by TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acceptance_criteria JSONB;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS evidence_bundle JSONB;
	CREATE INDEX IF NOT EXISTS idx_tasks_accepted_status ON tasks(workspace_id, status) WHERE status = 'accepted';

	-- Pocket↔ACC canonical ID binding (idempotent on existing DBs). All five
	-- columns are nullable; the only writer is SetACCBinding (authoritative
	-- local state — remote task sync must never clobber it).
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_task_id TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_run_id TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_dispatch_id TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_source_ref TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_correlation_id TEXT;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS acc_holder_id TEXT;
	CREATE INDEX IF NOT EXISTS idx_tasks_acc_dispatch ON tasks(acc_dispatch_id) WHERE acc_dispatch_id IS NOT NULL;

	CREATE TABLE IF NOT EXISTS approval_observations (
		workspace_id TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		request_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		state TEXT NOT NULL,
		version BIGINT NOT NULL,
		decision TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (workspace_id, instance_id, session_id, request_id, kind)
	);
	CREATE INDEX IF NOT EXISTS idx_approval_observations_session
		ON approval_observations(workspace_id, instance_id, session_id);

	CREATE TABLE IF NOT EXISTS task_approval_projections (
		workspace_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		request_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		state TEXT NOT NULL,
		version BIGINT NOT NULL,
		decision TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (workspace_id, task_id, instance_id, session_id, request_id, kind),
		FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_task_approval_projections_pending
		ON task_approval_projections(workspace_id, task_id, state);
	CREATE INDEX IF NOT EXISTS idx_task_approval_projections_session
		ON task_approval_projections(workspace_id, instance_id, session_id);

	-- Work = task (docs/学习muse/03-架构方案.md §1). One entity, classified by
	-- the type column; the collaboration and due-date columns ride the same
	-- row so a work item never needs a second table to render.
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS type        TEXT NOT NULL DEFAULT 'other';
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS owner_id    TEXT NOT NULL DEFAULT '';
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS assignees   JSONB NOT NULL DEFAULT '[]'::jsonb;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS due_at      BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS remind_at   BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS parent_id   TEXT NOT NULL DEFAULT '';
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS origin_kind TEXT NOT NULL DEFAULT '';
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS origin_ref  TEXT NOT NULL DEFAULT '';
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS tags        JSONB NOT NULL DEFAULT '[]'::jsonb;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS visibility  TEXT NOT NULL DEFAULT 'private';
	CREATE INDEX IF NOT EXISTS idx_tasks_type ON tasks(workspace_id, type);
	CREATE INDEX IF NOT EXISTS idx_tasks_due ON tasks(workspace_id, due_at) WHERE status <> 'completed';

	-- Collaboration: who is on the work item, and what happened to it.
	CREATE TABLE IF NOT EXISTS work_item_participants (
		workspace_id TEXT NOT NULL,
		task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		user_id      TEXT NOT NULL,
		role         TEXT NOT NULL,
		created_at   BIGINT NOT NULL,
		PRIMARY KEY (workspace_id, task_id, user_id)
	);
	CREATE INDEX IF NOT EXISTS idx_work_item_participants_user
		ON work_item_participants(workspace_id, user_id, role);

	CREATE TABLE IF NOT EXISTS work_item_events (
		workspace_id  TEXT NOT NULL,
		task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		event_id      TEXT NOT NULL,
		event_type    TEXT NOT NULL,
		actor_user_id TEXT NOT NULL DEFAULT '',
		payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at    BIGINT NOT NULL,
		PRIMARY KEY (workspace_id, task_id, event_id)
	);
	CREATE INDEX IF NOT EXISTS idx_work_item_events_type
		ON work_item_events(workspace_id, event_type, created_at);
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
// round-trips its tenant instead of dropping it. The work-item columns
// (type/owner/due/...) are appended last so the scan order below stays stable.
const taskColumns = `id, workspace_id, title, description, status, priority, COALESCE(workstream_id, ''), source, created_at, updated_at, pending_approvals, session_count, accepted_at, accepted_by, evidence_bundle, type, owner_id, assignees, due_at, remind_at, parent_id, origin_kind, origin_ref, tags, visibility, acc_task_id, acc_run_id, acc_dispatch_id, acc_source_ref, acc_correlation_id, acc_holder_id`

// scanTask reads one row in taskColumns order.
func scanTask(row interface {
	Scan(dest ...any) error
}) (*Task, error) {
	t := &Task{}
	var createdAt, updatedAt int64
	var acceptedAt *int64
	var acceptedBy *string
	// description / workstream_id 在 schema 上可空（UpsertTask 的
	// NULLIF 语义会写 NULL），必须用指针接，否则 NULL 行读取直接报错。
	var description, workstreamID *string
	var evidenceBundleRaw []byte
	// acc_* 绑定列全部可空（SetACCBinding 之外的写入路径不会填它们）。
	var accTaskID, accRunID, accDispatchID, accSourceRef, accCorrelationID, accHolderID *string
	var assigneesRaw, tagsRaw []byte
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.Title, &description, &t.Status, &t.Priority,
		&workstreamID, &t.Source, &createdAt, &updatedAt, &t.PendingApprovals, &t.SessionCount,
		&acceptedAt, &acceptedBy, &evidenceBundleRaw,
		&t.Type, &t.OwnerID, &assigneesRaw, &t.DueAt, &t.RemindAt, &t.ParentID,
		&t.OriginKind, &t.OriginRef, &tagsRaw, &t.Visibility,
		&accTaskID, &accRunID, &accDispatchID, &accSourceRef, &accCorrelationID, &accHolderID); err != nil {
		return nil, err
	}
	if description != nil {
		t.Description = *description
	}
	if workstreamID != nil {
		t.WorkstreamID = *workstreamID
	}
	t.Assignees = decodeStringList(assigneesRaw)
	t.Tags = decodeStringList(tagsRaw)
	if t.Type == "" {
		t.Type = TypeOther
	}
	t.TypeGroup = TypeGroup(t.Type)
	t.CreatedAt = time.Unix(createdAt, 0)
	t.UpdatedAt = time.Unix(updatedAt, 0)
	t.AcceptedAt = acceptedAt
	t.AcceptedBy = acceptedBy
	if len(evidenceBundleRaw) > 0 {
		var bundle EvidenceBundle
		if err := json.Unmarshal(evidenceBundleRaw, &bundle); err != nil {
			return nil, fmt.Errorf("decode evidence_bundle: %w", err)
		}
		t.EvidenceBundle = &bundle
	}
	if accTaskID != nil {
		t.ACCTaskID = *accTaskID
	}
	if accRunID != nil {
		t.ACCRunID = *accRunID
	}
	if accDispatchID != nil {
		t.ACCDispatchID = *accDispatchID
	}
	if accSourceRef != nil {
		t.ACCSourceRef = *accSourceRef
	}
	if accCorrelationID != nil {
		t.ACCCorrelationID = *accCorrelationID
	}
	if accHolderID != nil {
		t.ACCHolderID = *accHolderID
	}
	return t, nil
}

// encodeStringList serialises a []string for a JSONB column. nil becomes an
// empty array so the column is never SQL NULL.
func encodeStringList(values []string) []byte {
	if values == nil {
		values = []string{}
	}
	b, err := json.Marshal(values)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// decodeStringList is the read counterpart; a NULL or malformed column yields
// an empty slice rather than an error, so one bad row cannot break a list page.
func decodeStringList(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// taskInsertColumns and taskInsertValues are the shared shape of both INSERT
// statements (plain insert and upsert). They are consts rather than inline
// literals so store_contract_test.go can assert that the column list, the
// placeholder list, and taskColumns stay in agreement without a database.
//
// The number of Go arguments is NOT checkable statically — pgx takes them
// variadically. That case is covered by the Postgres-gated tests instead
// (see workitem_pg_test.go and docs/学习muse/如何验证真实数据库.md).
const taskInsertColumns = `id, workspace_id, title, description, status, priority, workstream_id, source,
	created_at, updated_at, pending_approvals, session_count,
	type, owner_id, assignees, due_at, remind_at, parent_id, origin_kind, origin_ref, tags, visibility,
  	acc_task_id, acc_run_id, acc_dispatch_id, acc_source_ref, acc_correlation_id, acc_holder_id`

const taskInsertValues = `VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
	$13, $14, $15, $16, $17, $18, $19, $20, $21, $22),
  	NULLIF($23, ''), NULLIF($24, ''), NULLIF($25, ''), NULLIF($26, ''), NULLIF($27, ''), NULLIF($28, ''))`

// taskUpsertValues is the same arity but wraps the nullable text columns in
// NULLIF, so an empty string becomes SQL NULL on upsert. That is why it cannot
// simply reuse taskInsertValues.
const taskUpsertValues = `VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12,
	$13, $14, $15, $16, $17, $18, $19, $20, $21, $22),
  	NULLIF($23, ''), NULLIF($24, ''), NULLIF($25, ''), NULLIF($26, ''), NULLIF($27, ''), NULLIF($28, ''))`

func (s *Store) CreateTask(ctx context.Context, task *Task) error {
	now := time.Now().Unix()
	task.CreatedAt = time.Unix(now, 0)
	task.UpdatedAt = time.Unix(now, 0)
	if task.Source == "" {
		task.Source = "local"
	}
	task.WorkspaceID = normalizeWorkspace(task.WorkspaceID)
	// Pending approvals are derived by server-owned approval workflows. A client
	// cannot create a task with a forged approval state.
	task.PendingApprovals = 0
	normalizeWorkItem(task)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (`+taskInsertColumns+`)
		`+taskInsertValues+`
	`, task.ID, task.WorkspaceID, task.Title, task.Description, task.Status, task.Priority, task.WorkstreamID, task.Source, now, now, task.PendingApprovals, task.SessionCount,
		task.Type, task.OwnerID, encodeStringList(task.Assignees), task.DueAt, task.RemindAt, task.ParentID,
		task.OriginKind, task.OriginRef, encodeStringList(task.Tags), task.Visibility,
		task.ACCTaskID, task.ACCRunID, task.ACCDispatchID, task.ACCSourceRef, task.ACCCorrelationID, task.ACCHolderID)
	return err
}

// normalizeWorkItem fills the defaults for the work-item columns so every write
// path (CreateTask, UpsertTask, the HTTP layer) lands the same values. It
// deliberately does NOT validate: the HTTP layer rejects bad enums with 400
// before we get here, and internal writers (tasksync) must not be able to
// wedge the store with an error.
func normalizeWorkItem(t *Task) {
	if t.Type == "" {
		t.Type = TypeOther
	}
	t.TypeGroup = TypeGroup(t.Type)
	if t.Visibility == "" {
		t.Visibility = VisibilityPrivate
	}
	if t.Assignees == nil {
		t.Assignees = []string{}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
}

// UpsertTask writes a remote task into the local cache, creating or updating.
//
// tasksync 每个周期都会重放同一批远程任务；CreateTask 的纯 INSERT 会在
// 第二个周期起持续触发 PG duplicate key（tasks_pkey）错误——错误虽被调用方
// 按 23505 吞掉，但 PostgreSQL 服务端日志每次都记录。同步路径改走本方法：
// ON CONFLICT (id) 只刷新远程拥有的列（title/status/priority/updated_at），
// 不动 workspace_id、accepted_*、evidence_bundle 等本地状态。
// 远端不携带的列保持原值；description/workstream_id 由 COALESCE 语义保护：
// 传空串视为"远端没有该字段"，不覆盖本地值。
func (s *Store) UpsertTask(ctx context.Context, task *Task) error {
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("upsert task: id is required")
	}
	// 不要改写调用者传入的 CreatedAt/UpdatedAt——调用者可能携带了上游时间戳
	// （如 tasksync 拉到的 ACC 远端字段），改写会破坏语义并把这种隐式副作用
	// 扩散到调用方。仅当调用者未设置（零值）时，使用本地时钟兜底，避免
	// SQL 拿到 0 触发下游排序/索引异常。
	now := time.Now().Unix()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Unix(now, 0)
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Unix(now, 0)
	}
	if task.Source == "" {
		task.Source = "local"
	}
	task.WorkspaceID = normalizeWorkspace(task.WorkspaceID)
	task.PendingApprovals = 0
	// tasksync replays remote ACC rows. The work-item columns (type/owner/
	// due/...) are locally owned, so they are written on INSERT but deliberately
	// NOT refreshed in the DO UPDATE branch below — a replayed remote snapshot
	// must never clobber a classification the user set by hand.
	normalizeWorkItem(task)

	// 跨 workspace 同 ID 守卫：ACC 任务 ID 全局唯一，tasks 表主键也是全局
	// (id)。若同 ID 已存在于另一 workspace，说明数据异常——拒绝写入并保留
	// 原行，避免 ON CONFLICT 把另一租户任务的远端字段（title/status 等）
	// 静默改写造成跨租户污染。同 workspace 重放仍走 ON CONFLICT 更新。
	var existingWS string
	wsErr := s.pool.QueryRow(ctx,
		`SELECT workspace_id FROM tasks WHERE id = $1`, task.ID).Scan(&existingWS)
	switch {
	case wsErr == nil && existingWS != task.WorkspaceID:
		return fmt.Errorf("upsert task %s: workspace mismatch (existing=%s incoming=%s)",
			task.ID, existingWS, task.WorkspaceID)
	case wsErr != nil && !errors.Is(wsErr, pgx.ErrNoRows):
		return fmt.Errorf("upsert task %s: check existing workspace: %w", task.ID, wsErr)
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (`+taskInsertColumns+`)
		`+taskUpsertValues+`
		ON CONFLICT (id) DO UPDATE SET
			title         = EXCLUDED.title,
			description   = COALESCE(NULLIF(EXCLUDED.description, ''), tasks.description),
			status        = EXCLUDED.status,
			priority      = EXCLUDED.priority,
			workstream_id = COALESCE(NULLIF(EXCLUDED.workstream_id, ''), tasks.workstream_id),
			updated_at    = EXCLUDED.updated_at
	`, task.ID, task.WorkspaceID, task.Title, task.Description, task.Status, task.Priority, task.WorkstreamID, task.Source, now, now, task.PendingApprovals, task.SessionCount,
		task.Type, task.OwnerID, encodeStringList(task.Assignees), task.DueAt, task.RemindAt, task.ParentID,
		task.OriginKind, task.OriginRef, encodeStringList(task.Tags), task.Visibility,
		task.ACCTaskID, task.ACCRunID, task.ACCDispatchID, task.ACCSourceRef, task.ACCCorrelationID, task.ACCHolderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// ON CONFLICT DO UPDATE 理论上不会 0 行受影响；防御性兜底。
		return fmt.Errorf("upsert task %s: no rows affected", task.ID)
	}
	return nil
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
	workspaceID := wsID
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin attach session: %w", err)
	}
	defer tx.Rollback(ctx)
	if workspaceID == "" {
		if err := tx.QueryRow(ctx, `SELECT workspace_id FROM tasks WHERE id = $1`, link.TaskID).Scan(&workspaceID); err != nil {
			return fmt.Errorf("resolve task workspace: %w", err)
		}
	}

	if err := lockApprovalSession(ctx, tx, workspaceID, link.InstanceID, link.SessionID); err != nil {
		return err
	}
	if err := lockTask(ctx, tx, workspaceID, link.TaskID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND workspace_id = $2 FOR UPDATE)`,
		link.TaskID, workspaceID).Scan(&exists); err != nil {
		return fmt.Errorf("verify task workspace: %w", err)
	}
	if !exists {
		return fmt.Errorf("task not found: %s", link.TaskID)
	}
	now := time.Now().Unix()
	// attached_at 用毫秒：同一 session 先后挂到两个任务时秒级会并列，
	// FindTaskIDBySessionID 的 ORDER BY attached_at DESC 在并列下顺序不定
	// （store_session_lookup_test 曾因此 flaky）。旧秒级行值恒小于毫秒行，
	// 排序语义不受影响。
	attachedAt := time.Now().UnixMilli()
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_session_links (task_id, workspace_id, instance_id, session_id, role, attached_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, instance_id, session_id) DO UPDATE SET role = EXCLUDED.role, attached_at = EXCLUDED.attached_at`,
		link.TaskID, workspaceID, link.InstanceID, link.SessionID, link.Role, attachedAt); err != nil {
		return fmt.Errorf("insert task session link: %w", err)
	}
	if err := applyObservedApprovalsForTask(ctx, tx, link.TaskID, workspaceID, link.InstanceID, link.SessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET session_count = (
			SELECT COUNT(*) FROM task_session_links WHERE task_id = $1 AND workspace_id = $2
		), updated_at = $3 WHERE id = $1 AND workspace_id = $2`, link.TaskID, workspaceID, now); err != nil {
		return fmt.Errorf("update task session count: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit attach session: %w", err)
	}
	return nil
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

// FindTaskIDBySessionID returns the most recently attached task for a session.
func (s *Store) FindTaskIDBySessionID(ctx context.Context, sessionID string) (string, error) {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return "", nil
	}
	var taskID string
	err := s.pool.QueryRow(ctx, `
		SELECT task_id FROM task_session_links
		 WHERE session_id = $1
		 ORDER BY attached_at DESC
		 LIMIT 1`, sessionID).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return taskID, nil
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

// ApplyApprovalProjection materializes one upstream approval event into every
// task linked to its session in the trusted workspace. An older or replayed
// version cannot overwrite a newer state.
func (s *Store) ApplyApprovalProjection(ctx context.Context, event ApprovalProjectionEvent) error {
	if event.WorkspaceID == "" || event.InstanceID == "" || event.SessionID == "" || event.RequestID == "" {
		return fmt.Errorf("approval projection identity is required")
	}
	if !isValidApprovalKind(event.Kind) || !isValidApprovalState(event.State) || event.Version <= 0 {
		return fmt.Errorf("invalid approval projection event")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin approval projection: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockApprovalSession(ctx, tx, event.WorkspaceID, event.InstanceID, event.SessionID); err != nil {
		return err
	}
	if err := upsertApprovalObservation(ctx, tx, event); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT l.task_id
		FROM task_session_links l
		JOIN tasks t ON t.id = l.task_id
		WHERE l.workspace_id = $1 AND l.instance_id = $2 AND l.session_id = $3
			AND t.workspace_id = $1`, event.WorkspaceID, event.InstanceID, event.SessionID)
	if err != nil {
		return fmt.Errorf("find linked tasks for approval projection: %w", err)
	}
	defer rows.Close()

	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return fmt.Errorf("scan linked task: %w", err)
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate linked tasks: %w", err)
	}

	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		if err := lockTask(ctx, tx, event.WorkspaceID, taskID); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, taskID, event.WorkspaceID).Scan(&status); err != nil {
			return fmt.Errorf("read task approval state: %w", err)
		}
		// Late-pending anti-regression: completed and accepted are terminal,
		// so a pending projection event must not reopen either.
		if (status == "completed" || status == "accepted") && event.State == ApprovalStatePending {
			continue
		}
		if err := applyApprovalProjectionForTask(ctx, tx, taskID, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit approval projection: %w", err)
	}
	return nil
}

// CompleteTaskScoped linearizes completion with approval projection updates for
// one task. It owns the derived pending_approvals value used by legacy readers.
func (s *Store) CompleteTaskScoped(ctx context.Context, id, wsID string, update TaskUpdate) (*Task, error) {
	wsID = normalizeWorkspace(wsID)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin complete task: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockTask(ctx, tx, wsID, id); err != nil {
		return nil, err
	}

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND workspace_id = $2 FOR UPDATE)`, id, wsID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("lock task for completion: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	pending, err := pendingApprovalCount(ctx, tx, id, wsID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(ctx, `UPDATE tasks SET pending_approvals = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`, pending, now, id, wsID); err != nil {
		return nil, fmt.Errorf("update derived pending approvals: %w", err)
	}
	if pending > 0 {
		return nil, ErrPendingApprovals
	}

	sets, args := taskUpdateSets(update, now)
	status := "completed"
	sets = append(sets, fmt.Sprintf("status = $%d", len(args)+1))
	args = append(args, status)
	args = append(args, id, wsID)
	query := fmt.Sprintf(`UPDATE tasks SET %s WHERE id = $%d AND workspace_id = $%d`, joinStrings(sets, ", "), len(args)-1, len(args))
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("complete task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit complete task: %w", err)
	}
	return s.GetTaskScoped(ctx, id, wsID)
}

// AcceptTaskScoped transitions a `completed` task to `accepted`, recording the
// authenticated actor and the evidence bundle as the verdict. It linearizes
// under the same task advisory lock as CompleteTaskScoped so a late-pending
// approval projection event cannot race with the verdict. Returns
// ErrTaskNotCompletable for any non-completed source state (including
// already-accepted, which is terminal).
func (s *Store) AcceptTaskScoped(ctx context.Context, id, wsID, actorUserID string, bundle EvidenceBundle) (*Task, error) {
	wsID = normalizeWorkspace(wsID)
	if actorUserID == "" {
		return nil, fmt.Errorf("accept task: missing actor user id")
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encode evidence_bundle: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin accept task: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockTask(ctx, tx, wsID, id); err != nil {
		return nil, err
	}
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM tasks WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		id, wsID).Scan(&status); err != nil {
		return nil, fmt.Errorf("lock task for acceptance: %w", err)
	}
	if status != "completed" {
		return nil, ErrTaskNotCompletable
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(ctx,
		`UPDATE tasks SET status = 'accepted', accepted_at = $1, accepted_by = $2,
		    evidence_bundle = $3, updated_at = $4
		 WHERE id = $5 AND workspace_id = $6`,
		now, actorUserID, bundleJSON, now, id, wsID); err != nil {
		return nil, fmt.Errorf("accept task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accept task: %w", err)
	}
	return s.GetTaskScoped(ctx, id, wsID)
}

func isValidApprovalKind(kind ApprovalKind) bool {
	return kind == ApprovalKindPermission || kind == ApprovalKindQuestion
}

func isValidApprovalState(state ApprovalState) bool {
	switch state {
	case ApprovalStatePending, ApprovalStateApproved, ApprovalStateRejected, ApprovalStateAnswered, ApprovalStateExpired, ApprovalStateFailed, ApprovalStateResolved:
		return true
	default:
		return false
	}
}

func lockTask(ctx context.Context, tx pgx.Tx, workspaceID, taskID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, workspaceID+":"+taskID); err != nil {
		return fmt.Errorf("lock task approval state: %w", err)
	}
	return nil
}

func lockApprovalSession(ctx context.Context, tx pgx.Tx, workspaceID, instanceID, sessionID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, workspaceID+":"+instanceID+":"+sessionID); err != nil {
		return fmt.Errorf("lock approval session state: %w", err)
	}
	return nil
}

func upsertApprovalObservation(ctx context.Context, tx pgx.Tx, event ApprovalProjectionEvent) error {
	now := time.Now().Unix()
	_, err := tx.Exec(ctx, `
		INSERT INTO approval_observations
			(workspace_id, instance_id, session_id, request_id, kind, state, version, decision, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (workspace_id, instance_id, session_id, request_id, kind) DO UPDATE
		SET state = EXCLUDED.state, version = EXCLUDED.version, decision = EXCLUDED.decision, updated_at = EXCLUDED.updated_at
		WHERE approval_observations.version < EXCLUDED.version
		  AND NOT (approval_observations.state <> 'pending' AND EXCLUDED.state = 'pending')`,
		event.WorkspaceID, event.InstanceID, event.SessionID, event.RequestID, event.Kind, event.State, event.Version, event.Decision, now)
	if err != nil {
		return fmt.Errorf("upsert approval observation: %w", err)
	}
	return nil
}

func applyObservedApprovalsForTask(ctx context.Context, tx pgx.Tx, taskID, workspaceID, instanceID, sessionID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO task_approval_projections
			(workspace_id, task_id, instance_id, session_id, request_id, kind, state, version, decision, created_at, updated_at)
		SELECT workspace_id, $1, instance_id, session_id, request_id, kind, state, version, decision, created_at, updated_at
		FROM approval_observations
		WHERE workspace_id = $2 AND instance_id = $3 AND session_id = $4
		  /* Late-pending anti-regression extended for accepted: terminal tasks
		     do not get a fresh projection row inserted from a late event. */
		  AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND workspace_id = $2 AND status IN ('completed','accepted'))
		ON CONFLICT (workspace_id, task_id, instance_id, session_id, request_id, kind) DO UPDATE
		SET state = EXCLUDED.state, version = EXCLUDED.version, decision = EXCLUDED.decision, updated_at = EXCLUDED.updated_at
		WHERE task_approval_projections.version < EXCLUDED.version
		  AND NOT (task_approval_projections.state <> 'pending' AND EXCLUDED.state = 'pending')`, taskID, workspaceID, instanceID, sessionID)
	if err != nil {
		return fmt.Errorf("project observed approvals onto task: %w", err)
	}
	pending, err := pendingApprovalCount(ctx, tx, taskID, workspaceID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET pending_approvals = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`, pending, time.Now().Unix(), taskID, workspaceID); err != nil {
		return fmt.Errorf("update derived pending approvals: %w", err)
	}
	return nil
}

func applyApprovalProjectionForTask(ctx context.Context, tx pgx.Tx, taskID string, event ApprovalProjectionEvent) error {
	now := time.Now().Unix()
	_, err := tx.Exec(ctx, `
		INSERT INTO task_approval_projections
			(workspace_id, task_id, instance_id, session_id, request_id, kind, state, version, decision, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (workspace_id, task_id, instance_id, session_id, request_id, kind) DO UPDATE
		SET state = EXCLUDED.state, version = EXCLUDED.version, decision = EXCLUDED.decision, updated_at = EXCLUDED.updated_at
		WHERE task_approval_projections.version < EXCLUDED.version
		  AND NOT (task_approval_projections.state <> 'pending' AND EXCLUDED.state = 'pending')`,
		event.WorkspaceID, taskID, event.InstanceID, event.SessionID, event.RequestID, event.Kind, event.State, event.Version, event.Decision, now)
	if err != nil {
		return fmt.Errorf("upsert approval projection: %w", err)
	}
	pending, err := pendingApprovalCount(ctx, tx, taskID, event.WorkspaceID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET pending_approvals = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`, pending, now, taskID, event.WorkspaceID); err != nil {
		return fmt.Errorf("update derived pending approvals: %w", err)
	}
	return nil
}

func pendingApprovalCount(ctx context.Context, tx pgx.Tx, taskID, workspaceID string) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM task_approval_projections
		WHERE task_id = $1 AND workspace_id = $2 AND state = $3`, taskID, workspaceID, ApprovalStatePending).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending approval projections: %w", err)
	}
	return count, nil
}

// taskUpdateSets renders the SET clause shared by the completion path
// (CompleteTaskScoped) and any other caller that updates a task without going
// through updateTask.
//
// It must cover **every** field of TaskUpdate. It previously handled only the
// four legacy fields, so completing a task silently discarded type, ownerId,
// assignees, dueAt, remindAt, parentId, tags and visibility — and because
// CompleteTaskScoped runs *after* the handler's validateReparent, the cycle
// check ran, passed, and the parentId it validated was then thrown away. The
// client got 200 with the old values and had no way to tell.
//
// TestTaskUpdateSetsCoverEveryField (store_contract_test.go) fails if a field
// is added to TaskUpdate without being handled here, so this cannot regress
// silently again.
func taskUpdateSets(update TaskUpdate, now int64) ([]string, []any) {
	sets := []string{}
	args := []any{}
	if update.Title != nil {
		sets = append(sets, fmt.Sprintf("title = $%d", len(args)+1))
		args = append(args, *update.Title)
	}
	if update.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)+1))
		args = append(args, *update.Description)
	}
	if update.Priority != nil {
		sets = append(sets, fmt.Sprintf("priority = $%d", len(args)+1))
		args = append(args, *update.Priority)
	}
	if update.WorkstreamID != nil {
		sets = append(sets, fmt.Sprintf("workstream_id = $%d", len(args)+1))
		args = append(args, *update.WorkstreamID)
	}
	// Work-item fields. Column names and encoding must match updateTask exactly
	// (assignees/tags go through encodeStringList so a client can clear the
	// list by sending an explicit []).
	if update.Type != nil {
		sets = append(sets, fmt.Sprintf("type = $%d", len(args)+1))
		args = append(args, *update.Type)
	}
	if update.OwnerID != nil {
		sets = append(sets, fmt.Sprintf("owner_id = $%d", len(args)+1))
		args = append(args, *update.OwnerID)
	}
	if update.Assignees != nil {
		sets = append(sets, fmt.Sprintf("assignees = $%d", len(args)+1))
		args = append(args, encodeStringList(*update.Assignees))
	}
	if update.DueAt != nil {
		sets = append(sets, fmt.Sprintf("due_at = $%d", len(args)+1))
		args = append(args, *update.DueAt)
	}
	if update.RemindAt != nil {
		sets = append(sets, fmt.Sprintf("remind_at = $%d", len(args)+1))
		args = append(args, *update.RemindAt)
	}
	if update.ParentID != nil {
		sets = append(sets, fmt.Sprintf("parent_id = $%d", len(args)+1))
		args = append(args, *update.ParentID)
	}
	if update.Tags != nil {
		sets = append(sets, fmt.Sprintf("tags = $%d", len(args)+1))
		args = append(args, encodeStringList(*update.Tags))
	}
	if update.Visibility != nil {
		sets = append(sets, fmt.Sprintf("visibility = $%d", len(args)+1))
		args = append(args, *update.Visibility)
	}
	sets = append(sets, fmt.Sprintf("updated_at = $%d", len(args)+1))
	args = append(args, now)
	return sets, args
}

func (s *Store) SetPendingApprovalsScoped(ctx context.Context, id, wsID string, count int) error {
	if count < 0 {
		return fmt.Errorf("pending approvals cannot be negative")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE tasks SET pending_approvals = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`,
		count, time.Now().Unix(), id, normalizeWorkspace(wsID))
	if err != nil {
		return fmt.Errorf("set pending approvals: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
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
	// Work-item fields (docs/学习muse/03-架构方案.md §1.1). They follow the
	// same nil-means-absent contract as the legacy ones; JSONB columns are
	// encoded through encodeStringList so a client can clear the list by
	// sending an explicit [].
	if update.Type != nil {
		sets = append(sets, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, *update.Type)
		argIdx++
	}
	if update.OwnerID != nil {
		sets = append(sets, fmt.Sprintf("owner_id = $%d", argIdx))
		args = append(args, *update.OwnerID)
		argIdx++
	}
	if update.Assignees != nil {
		sets = append(sets, fmt.Sprintf("assignees = $%d", argIdx))
		args = append(args, encodeStringList(*update.Assignees))
		argIdx++
	}
	if update.DueAt != nil {
		sets = append(sets, fmt.Sprintf("due_at = $%d", argIdx))
		args = append(args, *update.DueAt)
		argIdx++
	}
	if update.RemindAt != nil {
		sets = append(sets, fmt.Sprintf("remind_at = $%d", argIdx))
		args = append(args, *update.RemindAt)
		argIdx++
	}
	if update.ParentID != nil {
		sets = append(sets, fmt.Sprintf("parent_id = $%d", argIdx))
		args = append(args, *update.ParentID)
		argIdx++
	}
	if update.Tags != nil {
		sets = append(sets, fmt.Sprintf("tags = $%d", argIdx))
		args = append(args, encodeStringList(*update.Tags))
		argIdx++
	}
	if update.Visibility != nil {
		sets = append(sets, fmt.Sprintf("visibility = $%d", argIdx))
		args = append(args, *update.Visibility)
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
		argIdx++
	}
	if update.Status != nil && *update.Status == "completed" {
		where += " AND pending_approvals = 0"
	}

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE %s",
		joinStrings(sets, ", "), where)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if update.Status != nil && *update.Status == "completed" {
			current, readErr := reread()
			if readErr == nil && current.PendingApprovals > 0 {
				return nil, ErrPendingApprovals
			}
		}
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

// SetACCBinding writes the authoritative Pocket↔ACC canonical ID binding for
// one task inside a workspace. This is the only writer of the acc_* columns
// besides CreateTask: remote task sync (UpsertTask) deliberately never
// touches them, so a remote replay cannot sever a live binding. Pass a zero
// Binding to clear all five fields.
func (s *Store) SetACCBinding(ctx context.Context, workspaceID, taskID string, binding Binding) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks SET
			acc_task_id        = NULLIF($3, ''),
			acc_run_id         = NULLIF($4, ''),
			acc_dispatch_id    = NULLIF($5, ''),
			acc_source_ref     = NULLIF($6, ''),
			acc_correlation_id = NULLIF($7, ''),
			acc_holder_id      = NULLIF($8, ''),
			updated_at         = $9
		WHERE id = $1 AND workspace_id = $2`,
		taskID, normalizeWorkspace(workspaceID),
		binding.TaskID, binding.RunID, binding.DispatchID, binding.SourceRef, binding.CorrelationID, binding.HolderID,
		time.Now().Unix())
	if err != nil {
		return fmt.Errorf("set acc binding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// FindTaskBySessionScoped resolves the most recently attached task for an
// (instance, session) pair inside one workspace — the trusted join used by
// the approval path to decide whether a reply must be gated by ACC. A
// missing link returns (nil, nil); only genuine store failures return an
// error so callers can distinguish "unbound" from "cannot know".
func (s *Store) FindTaskBySessionScoped(ctx context.Context, wsID, instanceID, sessionID string) (*Task, error) {
	wsID = normalizeWorkspace(wsID)
	var taskID string
	err := s.pool.QueryRow(ctx, `
		SELECT l.task_id
		FROM task_session_links l
		JOIN tasks t ON t.id = l.task_id
		WHERE l.workspace_id = $1 AND l.instance_id = $2 AND l.session_id = $3
		  AND t.workspace_id = $1
		ORDER BY l.attached_at DESC
		LIMIT 1`, wsID, instanceID, sessionID).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find task by session: %w", err)
	}
	return s.GetTaskScoped(ctx, taskID, wsID)
}

func (s *Store) Close() error {
	// Pool is shared and closed by main.go; no-op here.
	return nil
}
