package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// apiKeyCipher 提供 API key 的对称加密/解密，与 email.Crypto 复用同一组
// AES-256-GCM 密钥（POCKET_EMAIL_MASTER_KEY）。这样 LLM gateway 凭证的
// at-rest 保护与 IMAP 凭证保持一致；改用独立密钥仅需在 NewLLMGatewayStore
// 注入不同实现即可。
type apiKeyCipher interface {
	EncryptString(plaintext string) (string, error)
	DecryptString(encrypted string) (string, error)
}

// LLMGatewayStore persists LLM gateway configurations to PostgreSQL.
// Table: llm_gateway_configs
type LLMGatewayStore struct {
	pool   *pgxpool.Pool
	cipher apiKeyCipher
}

// NewLLMGatewayStore creates the store and runs idempotent migrations. A
// non-nil cipher is required because api_key_encrypted must never silently
// degrade to plaintext storage when the email master key is unavailable.
func NewLLMGatewayStore(pool *pgxpool.Pool, cipher apiKeyCipher) (*LLMGatewayStore, error) {
	if cipher == nil {
		return nil, fmt.Errorf("LLM gateway API-key encryption is not configured")
	}
	s := &LLMGatewayStore{pool: pool, cipher: cipher}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("llm_gateway_configs migrate: %w", err)
	}
	return s, nil
}

func (s *LLMGatewayStore) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS llm_gateway_configs (
		id SERIAL PRIMARY KEY,
		base_url TEXT NOT NULL,
		api_key_encrypted TEXT NOT NULL DEFAULT '',
		models JSONB DEFAULT '[]',
		is_active BOOLEAN DEFAULT true,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	-- S0-A: workspace_id isolation (idempotent). The active config is scoped
	-- per workspace so collaborators can carry their own gateway settings.
	ALTER TABLE llm_gateway_configs ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
	CREATE INDEX IF NOT EXISTS idx_llm_gw_ws ON llm_gateway_configs(workspace_id);
	`)
	return err
}

// SaveConfig inserts a new config row and marks it as the active one.
// 当 cipher 非 nil 时 apiKey 会被加密后入库；cipher 为 nil 时按明文存储。
func (s *LLMGatewayStore) SaveConfig(ctx context.Context, workspaceID string, st llmGatewayState) error {
	if workspaceID == "" {
		workspaceID = "default"
	}
	storedAPIKey, err := s.encryptAPIKey(st.APIKey)
	if err != nil {
		return fmt.Errorf("encrypt api key: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Deactivate all previous configs
	if _, err := tx.Exec(ctx, `UPDATE llm_gateway_configs SET is_active = false WHERE workspace_id = $1`, workspaceID); err != nil {

		return err
	}

	modelsJSON, _ := json.Marshal(st.Models)

	_, err = tx.Exec(ctx, `
			INSERT INTO llm_gateway_configs (workspace_id, base_url, api_key_encrypted, models, is_active, created_at)
			VALUES ($1, $2, $3, $4, true, NOW())
		`, workspaceID, st.BaseURL, storedAPIKey, string(modelsJSON))

	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// LoadConfig loads the most recent active config from the database.
//   - pgx.ErrNoRows：调用方应走 env 默认值；
//   - 其它错误：视为存储层异常，调用方应记录并保留 env 默认值（不假装"无配置"）。
func (s *LLMGatewayStore) LoadConfig(ctx context.Context, workspaceID string) (*llmGatewayState, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	row := s.pool.QueryRow(ctx, `
		SELECT base_url, api_key_encrypted, models
		FROM llm_gateway_configs
			WHERE is_active = true AND workspace_id = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, workspaceID)

	var st llmGatewayState
	var modelsJSON string
	var apiKeyEnc string

	err := row.Scan(&st.BaseURL, &apiKeyEnc, &modelsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("llm_gateway_configs load: %w", err)
	}
	decoded, err := s.decryptAPIKey(apiKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt api key: %w", err)
	}
	st.APIKey = decoded
	if modelsJSON != "" {
		_ = json.Unmarshal([]byte(modelsJSON), &st.Models)
	}
	if st.Models == nil {
		st.Models = []string{}
	}
	return &st, nil
}

// encryptAPIKey always encrypts non-empty key material. The constructor rejects
// a nil cipher, so no production path can write plaintext into the encrypted
// column.
func (s *LLMGatewayStore) encryptAPIKey(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("api key encryption is not configured")
	}
	return s.cipher.EncryptString(plain)
}

// decryptAPIKey：与 encrypt 对称。空字符串按未配置处理直接返回。历史明文行
// 不会被静默接受，避免把未保护的秘密继续传播到内存或 OpenCode。
func (s *LLMGatewayStore) decryptAPIKey(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("api key decryption is not configured")
	}
	return s.cipher.DecryptString(enc)
}
