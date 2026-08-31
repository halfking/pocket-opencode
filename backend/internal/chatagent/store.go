// Package chatagent 实现 AI 对话智能体角色管理。
//
// 智能体（Chat Agent）是预设的专家角色，每个角色包含：
//   - 基本信息：name, department, description, emoji, color
//   - systemPrompt：完整的角色设定（从 agency-agents-zh/*.md 提取）
//   - isBuiltin：true=内置角色（专家库种子，可维护修改/删除），false=用户自定义
//
// 存储：
//   - 本地：SQLite chat_agents 表（workspace 隔离）
//   - 云端（可选）：PostgreSQL chat_agent_sync 表（仅同步自定义角色）
//
// 使用场景：
//   - 用户在 AI 对话中选择角色（如「AI 工程师」「抖音策略师」）
//   - 会话绑定角色后，角色的 systemPrompt 自动注入到请求的第一条消息
//   - 支持会话级覆盖（customSystemPrompt 优先级最高）
package chatagent

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Agent 代表一个 AI 对话智能体角色。
type Agent struct {
	ID           string `json:"id" db:"id"`                       // 角色唯一标识（如 'engineering-ai-engineer'）
	WorkspaceID  string `json:"workspace_id" db:"workspace_id"`   // 所属 workspace；内置角色为空字符串（全局共享）
	Name         string `json:"name" db:"name"`                   // 角色名称（如 'AI 工程师'）
	Description  string `json:"description" db:"description"`     // 角色简介
	Department   string `json:"department" db:"department"`       // 所属部门（如 'engineering'）
	Emoji        string `json:"emoji,omitempty" db:"emoji"`       // 角色 emoji（如 '🤖'）
	Color        string `json:"color,omitempty" db:"color"`       // 角色主题色（如 'purple'）
	SystemPrompt string `json:"system_prompt" db:"system_prompt"` // 完整角色设定（Markdown 正文）
	IsBuiltin    bool   `json:"is_builtin" db:"is_builtin"`       // 是否为内置角色（true=不可修改/删除）

	// 市场化支持字段（可选）：从 marketplace 安装或用户自定义时填充。
	MarketplaceID string   `json:"marketplace_id,omitempty" db:"marketplace_id"` // 关联的市场包 ID
	SkillRefs     []string `json:"skill_refs,omitempty" db:"skill_refs"`         // 绑定的技能 ID 列表
	Publisher     string   `json:"publisher,omitempty" db:"publisher"`           // 发布者
	Version       string   `json:"version,omitempty" db:"version"`               // 版本号（semver）
	Tags          []string `json:"tags,omitempty" db:"tags"`                     // 标签

	CreatedAt int64 `json:"created_at" db:"created_at"`
	UpdatedAt int64 `json:"updated_at" db:"updated_at"`
}

// Store 管理智能体角色的持久化（SQLite）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store 实例。pool 为 nil 时返回 noop store（所有操作返回 not configured）。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const schema = `
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
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);
`

// Init 初始化 chat_agents 表。
func (s *Store) Init(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Create 创建一个新角色。自定义角色需要设置 workspace_id；内置角色 workspace_id 为空。
func (s *Store) Create(ctx context.Context, a *Agent) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}
	if a.ID == "" || a.Name == "" || a.Department == "" || a.SystemPrompt == "" {
		return fmt.Errorf("id, name, department, system_prompt are required")
	}
	now := time.Now().Unix()
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_agents (id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, a.ID, a.WorkspaceID, a.Name, a.Description, a.Department, a.Emoji, a.Color, a.SystemPrompt, a.IsBuiltin, a.CreatedAt, a.UpdatedAt)
	return err
}

// Get 根据 id 查询单个角色。内置角色（workspace_id=”）全局可见；自定义角色需 workspace 匹配。
func (s *Store) Get(ctx context.Context, workspaceID, id string) (*Agent, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("store not configured")
	}
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at
		FROM chat_agents
		WHERE id = $1 AND (workspace_id = '' OR workspace_id = $2)
	`, id, workspaceID).Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &a.Department, &a.Emoji, &a.Color, &a.SystemPrompt, &a.IsBuiltin, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// List 列出所有角色（内置 + 当前 workspace 的自定义角色）。可选按 department 筛选。
func (s *Store) List(ctx context.Context, workspaceID, department string) ([]*Agent, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("store not configured")
	}
	query := `
		SELECT id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at
		FROM chat_agents
		WHERE (workspace_id = '' OR workspace_id = $1)
	`
	args := []interface{}{workspaceID}
	if department != "" {
		query += " AND department = $2"
		args = append(args, department)
	}
	query += " ORDER BY is_builtin DESC, department, name"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &a.Department, &a.Emoji, &a.Color, &a.SystemPrompt, &a.IsBuiltin, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		agents = append(agents, &a)
	}
	return agents, rows.Err()
}

// Update 更新角色（自定义与内置均可——内置专家库允许维护：管理员可直接
// 修正提示词/名称/部门；更新不影响 is_builtin 与归属）。自定义角色仅允许
// 所属 workspace 修改。
func (s *Store) Update(ctx context.Context, workspaceID string, a *Agent) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}
	existing, err := s.Get(ctx, workspaceID, a.ID)
	if err != nil {
		return err
	}
	if !existing.IsBuiltin && existing.WorkspaceID != workspaceID {
		return fmt.Errorf("agent does not belong to workspace %s", workspaceID)
	}

	a.UpdatedAt = time.Now().Unix()
	_, err = s.pool.Exec(ctx, `
		UPDATE chat_agents
		SET name = $1, description = $2, department = $3, emoji = $4, color = $5, system_prompt = $6, updated_at = $7
		WHERE id = $8 AND workspace_id = $9
	`, a.Name, a.Description, a.Department, a.Emoji, a.Color, a.SystemPrompt, a.UpdatedAt, a.ID, existing.WorkspaceID)
	return err
}

// Delete 删除角色（自定义与内置均可——内置专家库允许维护性删除）。
// 自定义角色仅允许所属 workspace 删除。
func (s *Store) Delete(ctx context.Context, workspaceID, id string) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}
	existing, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if !existing.IsBuiltin && existing.WorkspaceID != workspaceID {
		return fmt.Errorf("agent does not belong to workspace %s", workspaceID)
	}

	_, err = s.pool.Exec(ctx, "DELETE FROM chat_agents WHERE id = $1 AND workspace_id = $2", id, existing.WorkspaceID)
	return err
}

// CountCustom 统计当前 workspace 的自定义角色数量（用于配额检查）。
func (s *Store) CountCustom(ctx context.Context, workspaceID string) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("store not configured")
	}
	var count int
	err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM chat_agents WHERE workspace_id = $1 AND is_builtin = 0", workspaceID).Scan(&count)
	return count, err
}
