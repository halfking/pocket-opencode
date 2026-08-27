package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncPayload 是云端同步的数据载荷。
//
// 内置角色（is_builtin=true）不参与同步——它们从 agency-agents-zh 仓库
// 派生，云端版本不会更"权威"。只上传用户在工作区内创建的自定义角色。
//
// Version 用于乐观锁：上传方在 request 中带上本地已知版本号，服务端
// 在 version > stored.version 时才允许覆盖（防止回写旧数据）。
type SyncPayload struct {
	Version int64    `json:"version"`
	Agents  []*Agent `json:"agents"`
}

// SyncResult 描述一次同步操作的结果。
type SyncResult struct {
	Version       int64    `json:"version"`
	UploadedCount int      `json:"uploaded_count"`
	Conflict      bool     `json:"conflict"`
	ServerVersion int64    `json:"server_version,omitempty"`
	SkippedIDs    []string `json:"skipped_ids,omitempty"`
}

// SyncStore 管理 chat_agent_sync 云端同步表（PostgreSQL）。
//
// 表结构：
//   workspace_id TEXT NOT NULL,
//   user_id      TEXT NOT NULL,
//   version      BIGINT NOT NULL,   -- 单调递增版本号（毫秒时间戳）
//   agents_json  TEXT NOT NULL,     -- JSON 序列化的 Agent 数组
//   updated_at   BIGINT NOT NULL,
//   PRIMARY KEY (workspace_id, user_id)
type SyncStore struct {
	pool *pgxpool.Pool
}

// NewSyncStore 创建 SyncStore。
func NewSyncStore(pool *pgxpool.Pool) *SyncStore {
	return &SyncStore{pool: pool}
}

const syncSchema = `
CREATE TABLE IF NOT EXISTS chat_agent_sync (
	workspace_id TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	version      BIGINT NOT NULL,
	agents_json  TEXT NOT NULL DEFAULT '[]',
	updated_at   BIGINT NOT NULL,
	PRIMARY KEY (workspace_id, user_id)
);
`

// Init 创建 chat_agent_sync 表。
func (s *SyncStore) Init(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("sync store not configured")
	}
	_, err := s.pool.Exec(ctx, syncSchema)
	return err
}

// ErrSyncConflict 表示客户端的 version 早于服务端版本，需要先下载再合并。
var ErrSyncConflict = errors.New("chatagent: sync version conflict")

// ErrSyncNotConfigured 表示 SyncStore 未初始化（pool 为 nil）。
var ErrSyncNotConfigured = errors.New("chatagent: sync store not configured")

// Upload 上传自定义角色列表。
//
// 行为：
//   - 如果服务端不存在记录 → 直接写入，使用客户端 version
//   - 如果服务端 version <= 客户端 version → 覆盖写入
//   - 如果服务端 version > 客户端 version → 返回 ErrSyncConflict + serverVersion
//
// 注意：本函数只上传 workspace_id == wsID 且 is_builtin=false 的角色。
// 服务端会基于客户端上传的 agents 列表 + 当前 (workspace_id, user_id) 行写入，
// 不会去服务端 SQLite 查全表（避免内置角色混入）。
func (s *SyncStore) Upload(
	ctx context.Context,
	workspaceID, userID string,
	payload *SyncPayload,
) (*SyncResult, error) {
	if s.pool == nil {
		return nil, ErrSyncNotConfigured
	}

	// 过滤：只保留 is_builtin=false 且 workspace_id 匹配的角色
	filtered := make([]*Agent, 0, len(payload.Agents))
	for _, a := range payload.Agents {
		if a.IsBuiltin {
			continue
		}
		// 强制重写 workspace_id（防客户端伪造）
		a.WorkspaceID = workspaceID
		a.IsBuiltin = false
		filtered = append(filtered, a)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 取服务端当前版本
	var serverVersion int64
	err = tx.QueryRow(ctx, `
		SELECT version FROM chat_agent_sync WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&serverVersion)

	now := time.Now().UnixMilli()
	clientVersion := payload.Version
	if clientVersion == 0 {
		clientVersion = now // 客户端首次上传：用当前时间作为基线
	}
	newVersion := now // 服务端写入时用更新的时间戳

	if err == nil && serverVersion > clientVersion {
		// 版本冲突
		tx.Rollback(ctx)
		return &SyncResult{
			Version:       serverVersion,
			Conflict:      true,
			ServerVersion: serverVersion,
		}, ErrSyncConflict
	}

	// 序列化 agents 列表
	agentsJSON, err := json.Marshal(filtered)
	if err != nil {
		return nil, fmt.Errorf("marshal agents: %w", err)
	}

	// upsert
	if err == pgx.ErrNoRows {
		// 首次插入
		_, err = tx.Exec(ctx, `
			INSERT INTO chat_agent_sync (workspace_id, user_id, version, agents_json, updated_at)
			VALUES ($1, $2, $3, $4, $5)
		`, workspaceID, userID, newVersion, string(agentsJSON), now)
	} else if err == nil {
		// 覆盖更新
		_, err = tx.Exec(ctx, `
			UPDATE chat_agent_sync SET version = $1, agents_json = $2, updated_at = $3
			WHERE workspace_id = $4 AND user_id = $5
		`, newVersion, string(agentsJSON), now, workspaceID, userID)
	}
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &SyncResult{
		Version:       newVersion,
		UploadedCount: len(filtered),
		Conflict:      false,
	}, nil
}

// Download 拉取云端自定义角色列表。
//
// 返回 server version 供客户端下次 Upload 时携带。
func (s *SyncStore) Download(
	ctx context.Context,
	workspaceID, userID string,
) (*SyncPayload, error) {
	if s.pool == nil {
		return nil, ErrSyncNotConfigured
	}

	var (
		version    int64
		agentsJSON string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT version, agents_json FROM chat_agent_sync
		WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&version, &agentsJSON)

	if err == pgx.ErrNoRows {
		// 服务端无记录：返回空载荷 + version=0
		return &SyncPayload{Version: 0, Agents: []*Agent{}}, nil
	}
	if err != nil {
		return nil, err
	}

	agents := []*Agent{}
	if agentsJSON != "" && agentsJSON != "[]" {
		if err := json.Unmarshal([]byte(agentsJSON), &agents); err != nil {
			return nil, fmt.Errorf("unmarshal agents: %w", err)
		}
	}
	return &SyncPayload{Version: version, Agents: agents}, nil
}

// Status 查询当前同步状态（不含 agents 列表，用于 UI 角标）。
type SyncStatus struct {
	HasRemote       bool  `json:"has_remote"`
	ServerVersion   int64 `json:"server_version"`
	ServerUpdatedAt int64 `json:"server_updated_at"`
	AgentCount      int   `json:"agent_count"`
}

func (s *SyncStore) Status(ctx context.Context, workspaceID, userID string) (*SyncStatus, error) {
	if s.pool == nil {
		return nil, ErrSyncNotConfigured
	}

	var (
		version    int64
		agentsJSON string
		updatedAt  int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT version, agents_json, updated_at FROM chat_agent_sync
		WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&version, &agentsJSON, &updatedAt)

	if err == pgx.ErrNoRows {
		return &SyncStatus{HasRemote: false}, nil
	}
	if err != nil {
		return nil, err
	}

	var agents []*Agent
	_ = json.Unmarshal([]byte(agentsJSON), &agents)

	return &SyncStatus{
		HasRemote:       true,
		ServerVersion:   version,
		ServerUpdatedAt: updatedAt,
		AgentCount:      len(agents),
	}, nil
}
