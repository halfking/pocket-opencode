package chatagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// SQLiteStore 是 Store 的 SQLite 实现（无 PG 时的本地降级方案）。
//
// 同一份 SQL 在 PG 和 SQLite 上 99% 兼容（不兼容点：JSONB → TEXT）。这里
// 我们走最简的"不依赖 JSONB 操作符"的 SQL，全部用 TEXT 存 JSON。
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("sqlite store: empty db path")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS chat_agents (
	id            TEXT PRIMARY KEY,
	workspace_id  TEXT NOT NULL,
	name          TEXT NOT NULL,
	description   TEXT NOT NULL DEFAULT '',
	department    TEXT NOT NULL,
	emoji         TEXT,
	color         TEXT,
	system_prompt TEXT NOT NULL,
	is_builtin    INTEGER NOT NULL DEFAULT 0,
	marketplace_id TEXT,
	skill_refs     TEXT DEFAULT '[]',
	publisher      TEXT,
	version        TEXT,
	tags           TEXT DEFAULT '[]',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);
CREATE INDEX IF NOT EXISTS idx_chat_agents_marketplace ON chat_agents(marketplace_id) WHERE marketplace_id IS NOT NULL;
`

func (s *SQLiteStore) Init(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("sqlite store: db not configured")
	}
	_, err := s.db.ExecContext(ctx, sqliteSchema)
	return err
}

func (s *SQLiteStore) Create(ctx context.Context, a *Agent) error {
	if s.db == nil {
		return fmt.Errorf("sqlite store: db not configured")
	}
	if a.ID == "" || a.Name == "" || a.Department == "" || a.SystemPrompt == "" {
		return fmt.Errorf("id, name, department, system_prompt are required")
	}
	now := time.Now().Unix()
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	skillRefsJSON := encodeStringSlice(a.SkillRefs)
	tagsJSON := encodeStringSlice(a.Tags)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_agents (id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, marketplace_id, skill_refs, publisher, version, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ID, a.WorkspaceID, a.Name, a.Description, a.Department, a.Emoji, a.Color, a.SystemPrompt, a.IsBuiltin, nullIfEmpty(a.MarketplaceID), string(skillRefsJSON), nullIfEmpty(a.Publisher), nullIfEmpty(a.Version), string(tagsJSON), a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, workspaceID, id string) (*Agent, error) {
	if s.db == nil {
		return nil, fmt.Errorf("sqlite store: db not configured")
	}
	var a Agent
	var skillRefsJSON, tagsJSON []byte
	var marketplaceID, publisher, version sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin,
		       marketplace_id, skill_refs, publisher, version, tags,
		       created_at, updated_at
		FROM chat_agents
		WHERE id = ? AND (workspace_id = '' OR workspace_id = ?)
	`, id, workspaceID).Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &a.Department, &a.Emoji, &a.Color, &a.SystemPrompt, &a.IsBuiltin,
		&marketplaceID, &skillRefsJSON, &publisher, &version, &tagsJSON,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("chatagent: agent not found: %s", id)
		}
		return nil, err
	}
	if marketplaceID.Valid {
		a.MarketplaceID = marketplaceID.String
	}
	if publisher.Valid {
		a.Publisher = publisher.String
	}
	if version.Valid {
		a.Version = version.String
	}
	if err := decodeStringSlice(skillRefsJSON, &a.SkillRefs); err != nil {
		return nil, fmt.Errorf("decode skill_refs: %w", err)
	}
	if err := decodeStringSlice(tagsJSON, &a.Tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	return &a, nil
}

func (s *SQLiteStore) List(ctx context.Context, workspaceID, department string) ([]*Agent, error) {
	if s.db == nil {
		return nil, fmt.Errorf("sqlite store: db not configured")
	}
	query := `
		SELECT id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin,
		       marketplace_id, skill_refs, publisher, version, tags,
		       created_at, updated_at
		FROM chat_agents
		WHERE (workspace_id = '' OR workspace_id = ?)
	`
	args := []interface{}{workspaceID}
	if department != "" {
		query += " AND department = ?"
		args = append(args, department)
	}
	query += " ORDER BY is_builtin DESC, department, name"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []*Agent
	for rows.Next() {
		var a Agent
		var skillRefsJSON, tagsJSON []byte
		var marketplaceID, publisher, version sql.NullString
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &a.Department, &a.Emoji, &a.Color, &a.SystemPrompt, &a.IsBuiltin,
			&marketplaceID, &skillRefsJSON, &publisher, &version, &tagsJSON,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if marketplaceID.Valid {
			a.MarketplaceID = marketplaceID.String
		}
		if publisher.Valid {
			a.Publisher = publisher.String
		}
		if version.Valid {
			a.Version = version.String
		}
		if err := decodeStringSlice(skillRefsJSON, &a.SkillRefs); err != nil {
			return nil, fmt.Errorf("decode skill_refs: %w", err)
		}
		if err := decodeStringSlice(tagsJSON, &a.Tags); err != nil {
			return nil, fmt.Errorf("decode tags: %w", err)
		}
		agents = append(agents, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 保证非 nil slice（避免 JSON 序列化成 null → 前端拿到 null 而不是空数组）
	if agents == nil {
		agents = []*Agent{}
	}
	return agents, nil
}

// Update 更新角色（自定义与内置均可——内置专家库允许维护）。自定义角色
// 仅允许所属 workspace 修改；内置行按其自身 workspace_id（”）定位。
func (s *SQLiteStore) Update(ctx context.Context, workspaceID string, a *Agent) error {
	if s.db == nil {
		return fmt.Errorf("sqlite store: db not configured")
	}
	existing, err := s.Get(ctx, workspaceID, a.ID)
	if err != nil {
		return err
	}
	if !existing.IsBuiltin && existing.WorkspaceID != workspaceID {
		return fmt.Errorf("agent does not belong to workspace %s", workspaceID)
	}
	a.UpdatedAt = time.Now().Unix()
	skillRefsJSON := encodeStringSlice(a.SkillRefs)
	tagsJSON := encodeStringSlice(a.Tags)
	_, err = s.db.ExecContext(ctx, `
		UPDATE chat_agents
		SET name = ?, description = ?, department = ?, emoji = ?, color = ?, system_prompt = ?,
		    marketplace_id = ?, skill_refs = ?, publisher = ?, version = ?, tags = ?,
		    updated_at = ?
		WHERE id = ? AND workspace_id = ?
	`, a.Name, a.Description, a.Department, a.Emoji, a.Color, a.SystemPrompt,
		nullIfEmpty(a.MarketplaceID), string(skillRefsJSON), nullIfEmpty(a.Publisher), nullIfEmpty(a.Version), string(tagsJSON),
		a.UpdatedAt, a.ID, existing.WorkspaceID)
	return err
}

// Delete 删除角色（自定义与内置均可——内置专家库允许维护性删除）。
func (s *SQLiteStore) Delete(ctx context.Context, workspaceID, id string) error {
	if s.db == nil {
		return fmt.Errorf("sqlite store: db not configured")
	}
	existing, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if !existing.IsBuiltin && existing.WorkspaceID != workspaceID {
		return fmt.Errorf("agent does not belong to workspace %s", workspaceID)
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM chat_agents WHERE id = ? AND workspace_id = ?", id, existing.WorkspaceID)
	return err
}

func (s *SQLiteStore) CountCustom(ctx context.Context, workspaceID string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("sqlite store: db not configured")
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_agents WHERE workspace_id = ? AND is_builtin = 0", workspaceID).Scan(&count)
	return count, err
}

// ImportBuiltinAgents 在 SQLiteStore 上：委托给通用 importBuiltin。
func (s *SQLiteStore) ImportBuiltinAgents(ctx context.Context, repoPath string) error {
	return importBuiltin(ctx, s, repoPath)
}
