package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"
)

// PostgresHistoryStore PostgreSQL 历史存储实现
type PostgresHistoryStore struct {
	db *sql.DB
}

// NewPostgresHistoryStore 创建 PostgreSQL 历史存储
func NewPostgresHistoryStore(db *sql.DB) *PostgresHistoryStore {
	return &PostgresHistoryStore{db: db}
}

// SaveEvent 保存历史事件
func (s *PostgresHistoryStore) SaveEvent(ctx context.Context, sessionID string, event *HistoryEvent) error {
	query := `
		INSERT INTO opencode_session_history 
		(session_id, timestamp, event_type, actor, content, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	
	metadataJSON, err := jsonMarshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata failed: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query,
		sessionID,
		event.Timestamp,
		event.Type,
		event.Actor,
		event.Content,
		metadataJSON,
	)
	
	if err != nil {
		return fmt.Errorf("insert history event failed: %w", err)
	}

	return nil
}

// GetHistory 获取历史记录
func (s *PostgresHistoryStore) GetHistory(ctx context.Context, sessionID string, limit int) ([]*HistoryEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT timestamp, event_type, actor, content, metadata
		FROM opencode_session_history
		WHERE session_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`

	rows, err := s.db.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query history failed: %w", err)
	}
	defer rows.Close()

	events := make([]*HistoryEvent, 0)
	for rows.Next() {
		event := &HistoryEvent{}
		var metadataJSON []byte
		
		err := rows.Scan(
			&event.Timestamp,
			&event.Type,
			&event.Actor,
			&event.Content,
			&metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan history event failed: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := jsonUnmarshal(metadataJSON, &event.Metadata); err != nil {
				// 忽略元数据解析错误
				event.Metadata = nil
			}
		}

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration failed: %w", err)
	}

	return events, nil
}

// SaveSession 保存或更新会话信息
func (s *PostgresHistoryStore) SaveSession(ctx context.Context, session *CachedSession) error {
	query := `
		INSERT INTO opencode_sessions 
		(id, instance_id, title, status, created_at, updated_at, message_count, additions, deletions, files_changed, duration_secs, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at,
			message_count = EXCLUDED.message_count,
			additions = EXCLUDED.additions,
			deletions = EXCLUDED.deletions,
			files_changed = EXCLUDED.files_changed,
			duration_secs = EXCLUDED.duration_secs,
			metadata = EXCLUDED.metadata
	`

	var additions, deletions, filesChanged int
	if session.FileChanges != nil {
		additions = session.FileChanges.Additions
		deletions = session.FileChanges.Deletions
		filesChanged = session.FileChanges.Files
	}

	metadataJSON, err := jsonMarshal(session.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata failed: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query,
		session.ID,
		session.InstanceID,
		session.Title,
		session.Status,
		session.CreatedAt,
		session.UpdatedAt,
		session.MessageCount,
		additions,
		deletions,
		filesChanged,
		session.Duration,
		metadataJSON,
	)

	if err != nil {
		return fmt.Errorf("insert/update session failed: %w", err)
	}

	return nil
}

// GetSession 获取会话信息
func (s *PostgresHistoryStore) GetSession(ctx context.Context, sessionID string) (*CachedSession, error) {
	query := `
		SELECT id, instance_id, title, status, created_at, updated_at, 
		       message_count, additions, deletions, files_changed, duration_secs, metadata
		FROM opencode_sessions
		WHERE id = $1
	`

	session := &CachedSession{
		FileChanges: &FileChangeStats{},
	}
	var metadataJSON []byte

	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(
		&session.ID,
		&session.InstanceID,
		&session.Title,
		&session.Status,
		&session.CreatedAt,
		&session.UpdatedAt,
		&session.MessageCount,
		&session.FileChanges.Additions,
		&session.FileChanges.Deletions,
		&session.FileChanges.Files,
		&session.Duration,
		&metadataJSON,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query session failed: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := jsonUnmarshal(metadataJSON, &session.Metadata); err != nil {
			session.Metadata = nil
		}
	}

	return session, nil
}

// ListSessionsByInstance 列出实例的所有会话
func (s *PostgresHistoryStore) ListSessionsByInstance(ctx context.Context, instanceID string, limit int) ([]*CachedSession, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, instance_id, title, status, created_at, updated_at, 
		       message_count, additions, deletions, files_changed, duration_secs, metadata
		FROM opencode_sessions
		WHERE instance_id = $1
		ORDER BY updated_at DESC
		LIMIT $2
	`

	rows, err := s.db.QueryContext(ctx, query, instanceID, limit)
	if err != nil {
		return nil, fmt.Errorf("query sessions failed: %w", err)
	}
	defer rows.Close()

	sessions := make([]*CachedSession, 0)
	for rows.Next() {
		session := &CachedSession{
			FileChanges: &FileChangeStats{},
		}
		var metadataJSON []byte

		err := rows.Scan(
			&session.ID,
			&session.InstanceID,
			&session.Title,
			&session.Status,
			&session.CreatedAt,
			&session.UpdatedAt,
			&session.MessageCount,
			&session.FileChanges.Additions,
			&session.FileChanges.Deletions,
			&session.FileChanges.Files,
			&session.Duration,
			&metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan session failed: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := jsonUnmarshal(metadataJSON, &session.Metadata); err != nil {
				session.Metadata = nil
			}
		}

		sessions = append(sessions, session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration failed: %w", err)
	}

	return sessions, nil
}

// Helper functions

func jsonMarshal(v interface{}) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// InitSchema 初始化数据库表结构
func InitSchema(db *sql.DB) error {
	schema := `
	-- OpenCode 会话记录表
	CREATE TABLE IF NOT EXISTS opencode_sessions (
		id              VARCHAR(64) PRIMARY KEY,
		instance_id     VARCHAR(64) NOT NULL,
		title           TEXT NOT NULL,
		status          VARCHAR(20) NOT NULL,
		created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
		updated_at      TIMESTAMP NOT NULL DEFAULT NOW(),
		completed_at    TIMESTAMP,
		message_count   INTEGER DEFAULT 0,
		additions       INTEGER DEFAULT 0,
		deletions       INTEGER DEFAULT 0,
		files_changed   INTEGER DEFAULT 0,
		duration_secs   INTEGER DEFAULT 0,
		summary         TEXT,
		metadata        JSONB
	);

	CREATE INDEX IF NOT EXISTS idx_opencode_sessions_instance_id ON opencode_sessions(instance_id);
	CREATE INDEX IF NOT EXISTS idx_opencode_sessions_status ON opencode_sessions(status);
	CREATE INDEX IF NOT EXISTS idx_opencode_sessions_created_at ON opencode_sessions(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_opencode_sessions_updated_at ON opencode_sessions(updated_at DESC);

	-- OpenCode 会话历史事件表
	CREATE TABLE IF NOT EXISTS opencode_session_history (
		id              SERIAL PRIMARY KEY,
		session_id      VARCHAR(64) NOT NULL,
		timestamp       TIMESTAMP NOT NULL DEFAULT NOW(),
		event_type      VARCHAR(32) NOT NULL,
		actor           VARCHAR(32) NOT NULL,
		content         TEXT,
		metadata        JSONB
	);

	CREATE INDEX IF NOT EXISTS idx_opencode_history_session_id ON opencode_session_history(session_id);
	CREATE INDEX IF NOT EXISTS idx_opencode_history_timestamp ON opencode_session_history(timestamp);
	`

	_, err := db.Exec(schema)
	return err
}
