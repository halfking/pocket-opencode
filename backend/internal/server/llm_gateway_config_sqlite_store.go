// llm_gateway_config_sqlite_store.go — lightweight SQLite persistence for LLM
// gateway default configuration. The PG-backed LLMGatewayStore (POSTGRES) is
// the primary but in SQLite fallback mode we persist a single row per
// workspace so that initial setup defaults (base url / api key /
// preferred models) are stable across restarts.
//
// Meaningful fields are kept alongside. There is no API authentication here —
// encryption uses the email.Crypto cipher (same key + same cipher as
// llm_gateway_store.go) so the API key is safe at rest.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// sqlcipher is a thin interface over email.Crypto match pattern
type llmGatewayCipher interface {
	EncryptString(plaintext string) (string, error)
	DecryptString(encrypted string) (string, error)
}

// LLMGatewaySQLiteConfigStore stores gateway configs in `gateway_config` table
// (key/value pairs to keep schema trivially forward compatible) inside the
// chat_agents.sqlite database used by chatagent.SQLiteStore.
type LLMGatewaySQLiteConfigStore struct {
	db     *sql.DB
	cipher llmGatewayCipher
}

func NewLLMGatewaySQLiteConfigStore(dbPath string, cipher llmGatewayCipher) (*LLMGatewaySQLiteConfigStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("empty db path")
	}
	// 与 PG store（NewLLMGatewayStore）同一道防线、同一份契约：没有可用的
	// cipher 就构造失败，由调用方（cmd/pocketd/main.go）打 WARN 并放弃持久化，
	// 网关回落 env 默认值——绝不能构造"成功"后在第一次加解密时 panic。
	//
	// 实测形态：main.go 传进来的是 NewGatewayCipher(cfg.JWTSecret) 的返回值，
	// secret 为空时它是 nil *gatewayKeyCipher；装进 llmGatewayCipher 接口后
	// `cipher == nil` 判不住（typed-nil），旧代码会构造成功，直到第一次
	// encrypt/decrypt 才在 nil receiver 上解引用 g.key panic。无 PG 且
	// POCKET_JWT_SECRET 漏配的实例，启动自愈（EnsureLLMGatewayDefaults）或
	// 用户在设置页保存配置即触发。这与 3ea18409 在 PG store 修掉的是同一类洞。
	if cipher == nil || typedNilCipher(cipher) {
		return nil, fmt.Errorf("LLM gateway API-key encryption is not configured (cipher=%T)", cipher)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &LLMGatewaySQLiteConfigStore{db: db, cipher: cipher}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *LLMGatewaySQLiteConfigStore) migrate() error {
	_, err := s.db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS gateway_config (
	workspace_id TEXT NOT NULL,
	key          TEXT NOT NULL,
	value        TEXT NOT NULL,
	updated_at   INTEGER DEFAULT (strftime('%s','now')),
	PRIMARY KEY (workspace_id, key)
);`)
	return err
}

func (s *LLMGatewaySQLiteConfigStore) Close() error { return s.db.Close() }

// SaveConfig persists the gateway config gates. API key must be pre-encrypted
// by the caller (or empty when the caller expects 'env' fallback).
func (s *LLMGatewaySQLiteConfigStore) SaveConfig(ctx context.Context, workspaceID string, st llmGatewayState) error {
	if workspaceID == "" {
		workspaceID = "default"
	}
	storedAPIKey, err := encryptString(s.cipher, st.APIKey)
	if err != nil {
		return fmt.Errorf("encrypt api key: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	modelsJSON, _ := json.Marshal(st.Models)
	preferredJSON, _ := json.Marshal(st.PreferredModels)
	write := func(k, v string) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO gateway_config(workspace_id,key,value,updated_at) VALUES(?,?,?,strftime('%s','now'))
			 ON CONFLICT(workspace_id,key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			workspaceID, k, v)
		return err
	}
	if err := write("base_url", st.BaseURL); err != nil {
		return err
	}
	if err := write("api_key_encrypted", storedAPIKey); err != nil {
		return err
	}
	if err := write("format", st.Format); err != nil {
		return err
	}
	if err := write("models", string(modelsJSON)); err != nil {
		return err
	}
	if err := write("preferred_models", string(preferredJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// encryptString encrypts plaintext via cipher; empty input returns empty.
// nil / typed-nil cipher（含单测直构的 nil）都返回显式 error，绝不 panic：
// 调用方（启动自愈、设置页保存）据此降级，见构造器内的注释。
func encryptString(c llmGatewayCipher, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if c == nil || typedNilCipher(c) {
		return "", fmt.Errorf("no cipher configured for gateway config persistence - cannot encrypt sensitive field")
	}
	return c.EncryptString(plain)
}

func decryptString(c llmGatewayCipher, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if c == nil || typedNilCipher(c) {
		return "", fmt.Errorf("no cipher configured for gateway config persistence - cannot decrypt sensitive field")
	}
	return c.DecryptString(enc)
}

// LoadConfig reads most recent config. Returns a zero-valued entry if none found.
func (s *LLMGatewaySQLiteConfigStore) LoadConfig(ctx context.Context, workspaceID string) (*llmGatewayState, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM gateway_config WHERE workspace_id = ?`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kv := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		kv[k] = v
	}
	if len(kv) == 0 {
		return nil, nil
	}

	var st llmGatewayState
	st.BaseURL = kv["base_url"]
	st.Format = kv["format"]
	if kv["models"] != "" {
		_ = json.Unmarshal([]byte(kv["models"]), &st.Models)
	}
	if st.Models == nil {
		st.Models = []string{}
	}
	if kv["preferred_models"] != "" {
		_ = json.Unmarshal([]byte(kv["preferred_models"]), &st.PreferredModels)
	}
	if st.PreferredModels == nil {
		st.PreferredModels = []string{}
	}
	if st.Format == "" {
		st.Format = "openai-chat"
	}
	// Decrypt api key so the rest of server stack sees plaintext-token
	// (gateway forwards it upstream on demand).
	if kv["api_key_encrypted"] != "" {
		dec, err := decryptString(s.cipher, kv["api_key_encrypted"])
		if err != nil {
			return nil, fmt.Errorf("decrypt api key: %w", err)
		}
		st.APIKey = dec
	}
	return &st, nil
}
