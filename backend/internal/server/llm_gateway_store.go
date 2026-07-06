package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LLMGatewayStore persists LLM gateway configurations to PostgreSQL.
// Table: llm_gateway_configs
type LLMGatewayStore struct {
	pool *pgxpool.Pool
}

// NewLLMGatewayStore creates the store and runs idempotent migrations.
func NewLLMGatewayStore(pool *pgxpool.Pool) (*LLMGatewayStore, error) {
	s := &LLMGatewayStore{pool: pool}
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
	`)
	return err
}

// SaveConfig inserts a new config row and marks it as the active one.
func (s *LLMGatewayStore) SaveConfig(ctx context.Context, st llmGatewayState) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Deactivate all previous configs
	if _, err := tx.Exec(ctx, `UPDATE llm_gateway_configs SET is_active = false`); err != nil {
		return err
	}

	modelsJSON, _ := json.Marshal(st.Models)

	_, err = tx.Exec(ctx, `
		INSERT INTO llm_gateway_configs (base_url, api_key_encrypted, models, is_active, created_at)
		VALUES ($1, $2, $3, true, NOW())
	`, st.BaseURL, st.APIKey, string(modelsJSON))
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// LoadConfig loads the most recent active config from the database.
func (s *LLMGatewayStore) LoadConfig(ctx context.Context) (*llmGatewayState, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT base_url, api_key_encrypted, models
		FROM llm_gateway_configs
		WHERE is_active = true
		ORDER BY created_at DESC
		LIMIT 1
	`)

	var st llmGatewayState
	var modelsJSON string
	var apiKeyEnc string

	err := row.Scan(&st.BaseURL, &apiKeyEnc, &modelsJSON)
	if err != nil {
		// No config saved yet — not an error
		return nil, nil
	}
	st.APIKey = apiKeyEnc
	if modelsJSON != "" {
		_ = json.Unmarshal([]byte(modelsJSON), &st.Models)
	}
	if st.Models == nil {
		st.Models = []string{}
	}
	return &st, nil
}

