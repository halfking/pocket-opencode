package llmbff

// store.go — PG-backed UsageStore implementing both Recorder and Summarizer.
//
// One row per LLM call. The table is append-only (no UPDATEs) so it doubles as
// a per-call audit log. Aggregates for S3 dashboards are computed at read time
// with SUM/COUNT; the expected volume (a few thousand calls/day for a single
// "one-person company") doesn't warrant a pre-aggregated rollup table yet.
//
// The model_usage table follows the pocketd convention: workspace_id column
// for S0 isolation (spec §3.2), CREATE TABLE IF NOT EXISTS in the constructor.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UsageStore persists per-call LLM usage to PostgreSQL. It satisfies both the
// Recorder (write) and Summarizer (read) interfaces.
type UsageStore struct {
	pool *pgxpool.Pool
}

// NewUsageStore constructs the store and runs idempotent migrations.
func NewUsageStore(pool *pgxpool.Pool) (*UsageStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("llmbff: pgxpool is nil")
	}
	s := &UsageStore{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("llmbff migrate: %w", err)
	}
	return s, nil
}

func (s *UsageStore) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS model_usage (
	id              BIGSERIAL PRIMARY KEY,
	workspace_id    TEXT NOT NULL DEFAULT 'default',
	user_id         TEXT NOT NULL DEFAULT '',
	model           TEXT NOT NULL,
	kind            TEXT NOT NULL DEFAULT 'chat',
	prompt_tokens   INTEGER NOT NULL DEFAULT 0,
	completion_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens    INTEGER NOT NULL DEFAULT 0,
	cost_usd        DOUBLE PRECISION NOT NULL DEFAULT 0,
	created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_model_usage_ws_time ON model_usage(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usage(model);
CREATE INDEX IF NOT EXISTS idx_model_usage_kind ON model_usage(kind);
`)
	return err
}

// RecordUsage inserts one usage row. Implements Recorder.
func (s *UsageStore) RecordUsage(ctx context.Context, wsID, model, userID string, u Usage, kind string) error {
	if kind == "" {
		kind = "chat"
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO model_usage (workspace_id, user_id, model, kind, prompt_tokens, completion_tokens, total_tokens, cost_usd)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`, wsID, userID, model, kind, u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CostUSD)
	return err
}

// Summarize aggregates usage for a workspace over a [from, to) window.
// Implements Summarizer.
func (s *UsageStore) Summarize(ctx context.Context, wsID string, from, to time.Time) (UsageSummary, error) {
	row := s.pool.QueryRow(ctx, `
SELECT
	COALESCE(SUM(total_tokens), 0),
	COALESCE(SUM(prompt_tokens), 0),
	COALESCE(SUM(completion_tokens), 0),
	COALESCE(SUM(cost_usd), 0),
	COUNT(*)
FROM model_usage
WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3
`, wsID, from, to)
	var sum UsageSummary
	sum.WorkspaceID = wsID
	sum.PeriodStart = from
	sum.PeriodEnd = to
	err := row.Scan(&sum.TotalTokens, &sum.PromptTokens, &sum.CompletionTokens, &sum.TotalCostUSD, &sum.CallCount)
	return sum, err
}

// noopSummarizer always returns zeros. Used when PG is absent.
type noopSummarizer struct{}

func (noopSummarizer) Summarize(_ context.Context, wsID string, from, to time.Time) (UsageSummary, error) {
	return UsageSummary{WorkspaceID: wsID, PeriodStart: from, PeriodEnd: to}, nil
}
