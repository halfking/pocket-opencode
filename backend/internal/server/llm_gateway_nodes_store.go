package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GatewayNode 是一个已注册的 llm-gateway-go 控制面节点。
//
// 与 llm_gateway_configs（只保存数据面 baseURL + sk-* key，用于给 OpenCode 下发
// 模型配置）不同，本表保存的是访问网关 **admin API** 所需的凭据。网关自
// 2026-07-10 起 admin API 只认 JWT（POST /api/auth/token 换 access_token），
// 数据面 sk-* key 无法访问 /api/providers、/api/credentials/* 等端点，因此必须
// 单独存一份 admin 账号。
//
// AdminPassword 只在写入方向出现：Store 的读取方法永不回填该字段，调用方需要
// 明文时必须走 LoadWithSecret。这样普通的列表/详情响应不可能泄露密码。
type GatewayNode struct {
	ID          int64  `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	BaseURL     string `json:"baseURL"`
	// AdminUsername 是网关 users 表里的用户名。要覆盖 /api/providers 与
	// /api/routing/probe 需要 super_admin 角色，其余端点 admin 即可。
	AdminUsername string `json:"adminUsername"`
	// AdminPasswordSet 让前端知道"已配置"而不暴露值本身。
	AdminPasswordSet bool `json:"adminPasswordSet"`
	// DataAPIKeySet 同理，对应可选的数据面 sk-* key。
	DataAPIKeySet bool   `json:"dataApiKeySet"`
	Enabled       bool   `json:"enabled"`
	HealthStatus  string `json:"healthStatus"`
	HealthError   string `json:"healthError,omitempty"`
	// HealthRole 记录上次探测时 /api/auth/me 返回的角色，便于前端提示
	// "该账号不是 super_admin，供应商页会 403"。
	HealthRole string     `json:"healthRole,omitempty"`
	HealthAt   *time.Time `json:"healthAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// GatewayNodeSecret 携带解密后的凭据，仅供 admin client 内部使用。
// 不带 json tag —— 这个结构体不应该被序列化到任何响应里。
type GatewayNodeSecret struct {
	Node          GatewayNode
	AdminPassword string
	DataAPIKey    string
}

const (
	gatewayHealthUnknown = "unknown"
	gatewayHealthOK      = "ok"
	gatewayHealthError   = "error"
)

// ErrGatewayNodeNotFound 由调用方翻译成 404。
var ErrGatewayNodeNotFound = errors.New("gateway node not found")

// GatewayNodeStore 持久化网关节点注册表。
// 表：llm_gateway_nodes
type GatewayNodeStore struct {
	pool   *pgxpool.Pool
	cipher apiKeyCipher
}

// NewGatewayNodeStore 创建 store 并跑幂等迁移。cipher 必须非 nil：admin 密码
// 绝不允许在密钥不可用时静默降级成明文入库（与 NewLLMGatewayStore 同一约定）。
func NewGatewayNodeStore(pool *pgxpool.Pool, cipher apiKeyCipher) (*GatewayNodeStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("gateway node store requires a database pool")
	}
	if cipher == nil {
		return nil, fmt.Errorf("gateway node credential encryption is not configured")
	}
	s := &GatewayNodeStore{pool: pool, cipher: cipher}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("llm_gateway_nodes migrate: %w", err)
	}
	return s, nil
}

func (s *GatewayNodeStore) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS llm_gateway_nodes (
		id BIGSERIAL PRIMARY KEY,
		workspace_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL,
		base_url TEXT NOT NULL,
		admin_username TEXT NOT NULL DEFAULT '',
		admin_password_encrypted TEXT NOT NULL DEFAULT '',
		data_api_key_encrypted TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		last_health_status TEXT NOT NULL DEFAULT 'unknown',
		last_health_error TEXT NOT NULL DEFAULT '',
		last_health_role TEXT NOT NULL DEFAULT '',
		last_health_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	ALTER TABLE llm_gateway_nodes ADD COLUMN IF NOT EXISTS last_health_role TEXT NOT NULL DEFAULT '';
	CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_gw_nodes_ws_name ON llm_gateway_nodes(workspace_id, name);
	`)
	return err
}

const gatewayNodeColumns = `id, workspace_id, name, base_url, admin_username,
	admin_password_encrypted <> '' AS admin_password_set,
	data_api_key_encrypted <> '' AS data_api_key_set,
	enabled, last_health_status, last_health_error, last_health_role,
	last_health_at, created_at, updated_at`

func scanGatewayNode(row pgx.Row) (*GatewayNode, error) {
	var n GatewayNode
	var healthAt *time.Time
	err := row.Scan(&n.ID, &n.WorkspaceID, &n.Name, &n.BaseURL, &n.AdminUsername,
		&n.AdminPasswordSet, &n.DataAPIKeySet, &n.Enabled,
		&n.HealthStatus, &n.HealthError, &n.HealthRole, &healthAt,
		&n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	n.HealthAt = healthAt
	return &n, nil
}

// List 返回该 workspace 的所有节点（不含密码）。
func (s *GatewayNodeStore) List(ctx context.Context, workspaceID string) ([]GatewayNode, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+gatewayNodeColumns+` FROM llm_gateway_nodes
		 WHERE workspace_id = $1 ORDER BY name ASC`, normalizeWorkspace(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GatewayNode, 0)
	for rows.Next() {
		n, err := scanGatewayNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// Get 按 (workspace, id) 取单个节点。workspace 条件不可省 —— 否则任意用户可以
// 用猜到的自增 id 读到别的 workspace 的节点元数据。
func (s *GatewayNodeStore) Get(ctx context.Context, workspaceID string, id int64) (*GatewayNode, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+gatewayNodeColumns+` FROM llm_gateway_nodes
		 WHERE workspace_id = $1 AND id = $2`, normalizeWorkspace(workspaceID), id)
	n, err := scanGatewayNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGatewayNodeNotFound
	}
	return n, err
}

// LoadWithSecret 取节点并解密凭据。仅 admin client 调用。
func (s *GatewayNodeStore) LoadWithSecret(ctx context.Context, workspaceID string, id int64) (*GatewayNodeSecret, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, name, base_url, admin_username,
		       admin_password_encrypted, data_api_key_encrypted, enabled,
		       last_health_status, last_health_error, last_health_role,
		       last_health_at, created_at, updated_at
		FROM llm_gateway_nodes WHERE workspace_id = $1 AND id = $2`,
		normalizeWorkspace(workspaceID), id)

	var n GatewayNode
	var encPass, encKey string
	var healthAt *time.Time
	err := row.Scan(&n.ID, &n.WorkspaceID, &n.Name, &n.BaseURL, &n.AdminUsername,
		&encPass, &encKey, &n.Enabled,
		&n.HealthStatus, &n.HealthError, &n.HealthRole, &healthAt,
		&n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGatewayNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	n.HealthAt = healthAt
	n.AdminPasswordSet = encPass != ""
	n.DataAPIKeySet = encKey != ""

	out := &GatewayNodeSecret{Node: n}
	if out.AdminPassword, err = s.decrypt(encPass); err != nil {
		return nil, fmt.Errorf("decrypt admin password: %w", err)
	}
	if out.DataAPIKey, err = s.decrypt(encKey); err != nil {
		return nil, fmt.Errorf("decrypt data api key: %w", err)
	}
	return out, nil
}

// GatewayNodeInput 是创建/更新的入参。指针字段为 nil 表示"不修改"，
// 这样 PUT 可以只改 name 而不必回传密码。
type GatewayNodeInput struct {
	Name          *string
	BaseURL       *string
	AdminUsername *string
	AdminPassword *string
	DataAPIKey    *string
	Enabled       *bool
}

// Create 新增节点。name/baseURL/adminUsername/adminPassword 为必填。
func (s *GatewayNodeStore) Create(ctx context.Context, workspaceID string, in GatewayNodeInput) (*GatewayNode, error) {
	name := strings.TrimSpace(derefString(in.Name))
	baseURL := strings.TrimSpace(derefString(in.BaseURL))
	username := strings.TrimSpace(derefString(in.AdminUsername))
	password := derefString(in.AdminPassword)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	if username == "" {
		return nil, fmt.Errorf("adminUsername is required")
	}
	if password == "" {
		return nil, fmt.Errorf("adminPassword is required")
	}

	encPass, err := s.encrypt(password)
	if err != nil {
		return nil, fmt.Errorf("encrypt admin password: %w", err)
	}
	encKey, err := s.encrypt(derefString(in.DataAPIKey))
	if err != nil {
		return nil, fmt.Errorf("encrypt data api key: %w", err)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO llm_gateway_nodes
			(workspace_id, name, base_url, admin_username,
			 admin_password_encrypted, data_api_key_encrypted, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+gatewayNodeColumns,
		normalizeWorkspace(workspaceID), name, baseURL, username, encPass, encKey, enabled)
	return scanGatewayNode(row)
}

// Update 部分更新。nil 字段保持原值；空字符串的 adminPassword 同样视为"不修改"，
// 因为前端的密码框留空就是"保留现有密码"的语义。
func (s *GatewayNodeStore) Update(ctx context.Context, workspaceID string, id int64, in GatewayNodeInput) (*GatewayNode, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{normalizeWorkspace(workspaceID), id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, fmt.Errorf("name must not be empty")
		}
		sets = append(sets, "name = "+next(name))
	}
	if in.BaseURL != nil {
		baseURL := strings.TrimSpace(*in.BaseURL)
		if baseURL == "" {
			return nil, fmt.Errorf("baseURL must not be empty")
		}
		sets = append(sets, "base_url = "+next(baseURL))
	}
	if in.AdminUsername != nil {
		username := strings.TrimSpace(*in.AdminUsername)
		if username == "" {
			return nil, fmt.Errorf("adminUsername must not be empty")
		}
		sets = append(sets, "admin_username = "+next(username))
	}
	if in.AdminPassword != nil && *in.AdminPassword != "" {
		enc, err := s.encrypt(*in.AdminPassword)
		if err != nil {
			return nil, fmt.Errorf("encrypt admin password: %w", err)
		}
		sets = append(sets, "admin_password_encrypted = "+next(enc))
	}
	if in.DataAPIKey != nil {
		// 与密码不同：显式传空串表示清除 data key（它是可选字段）。
		enc, err := s.encrypt(*in.DataAPIKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt data api key: %w", err)
		}
		sets = append(sets, "data_api_key_encrypted = "+next(enc))
	}
	if in.Enabled != nil {
		sets = append(sets, "enabled = "+next(*in.Enabled))
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE llm_gateway_nodes SET `+strings.Join(sets, ", ")+`
		 WHERE workspace_id = $1 AND id = $2
		 RETURNING `+gatewayNodeColumns, args...)
	n, err := scanGatewayNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGatewayNodeNotFound
	}
	return n, err
}

// Delete 删除节点。
func (s *GatewayNodeStore) Delete(ctx context.Context, workspaceID string, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM llm_gateway_nodes WHERE workspace_id = $1 AND id = $2`,
		normalizeWorkspace(workspaceID), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGatewayNodeNotFound
	}
	return nil
}

// RecordHealth 写回探测结果。status 为 gatewayHealthOK / gatewayHealthError。
func (s *GatewayNodeStore) RecordHealth(ctx context.Context, workspaceID string, id int64, status, role, errMsg string) error {
	// 上游错误信息可能很长（含 HTML 错误页），截断避免把整页塞进表里。
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE llm_gateway_nodes
		SET last_health_status = $3, last_health_role = $4,
		    last_health_error = $5, last_health_at = NOW(), updated_at = NOW()
		WHERE workspace_id = $1 AND id = $2`,
		normalizeWorkspace(workspaceID), id, status, role, errMsg)
	return err
}

// ImportLegacyConfig 把 llm_gateway_configs 的 active 行迁成第一个节点，
// 只在该 workspace 还没有任何节点时执行。admin 凭据无法从旧表推导（旧表只有
// 数据面 sk-* key），所以导入后的节点需要用户补录 admin 账号 —— 这也是为什么
// 导入的节点 health 初始为 unknown。
//
// 返回是否真的导入了。
func (s *GatewayNodeStore) ImportLegacyConfig(ctx context.Context, workspaceID string) (bool, error) {
	ws := normalizeWorkspace(workspaceID)

	var existing int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_gateway_nodes WHERE workspace_id = $1`, ws).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}

	var baseURL, encKey string
	err := s.pool.QueryRow(ctx, `
		SELECT base_url, api_key_encrypted FROM llm_gateway_configs
		WHERE is_active = true AND workspace_id = $1
		ORDER BY created_at DESC LIMIT 1`, ws).Scan(&baseURL, &encKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		// 旧表可能还不存在（全新部署）——那就没什么可导入的。
		if strings.Contains(err.Error(), "llm_gateway_configs") {
			return false, nil
		}
		return false, err
	}

	// 数据面 base_url 通常带 /v1 后缀（OpenAI 兼容端点），而 admin API 挂在
	// 根路径上，所以剥掉 /v1 再存。
	adminBase := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")

	// api_key_encrypted 是同一个 cipher 加的密，直接原样搬过去，不必解密再加密。
	_, err = s.pool.Exec(ctx, `
		INSERT INTO llm_gateway_nodes
			(workspace_id, name, base_url, admin_username,
			 admin_password_encrypted, data_api_key_encrypted, enabled, last_health_status)
		VALUES ($1, $2, $3, '', '', $4, TRUE, 'unknown')
		ON CONFLICT (workspace_id, name) DO NOTHING`,
		ws, "default", adminBase, encKey)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *GatewayNodeStore) encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("credential encryption is not configured")
	}
	return s.cipher.EncryptString(plain)
}

func (s *GatewayNodeStore) decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("credential decryption is not configured")
	}
	return s.cipher.DecryptString(enc)
}

func normalizeWorkspace(id string) string {
	if strings.TrimSpace(id) == "" {
		return "default"
	}
	return id
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
