package quota

// pg_store.go — PG-backed Store for per-workspace budget caps.
//
// 一个 budget 代表一个 workspace 在某时间段内的硬上限。表结构沿用
// pocketd 约定：workspace_id 列做租户隔离（spec §3.2），CREATE TABLE IF
// NOT EXISTS 在构造时幂等执行，调用方无需独立 migrate 步骤。
//
// 表名 quota_budgets 与 model_usage (llmbff) 同构（按 workspace_id 分区
// 检索），未来若做预算"按调用量自减"，可在该表加 current_spent 列即可，
// 不破坏现有读路径。

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore persists budgets to PostgreSQL. It satisfies the quota.Store
// interface defined in quota.go.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs the store and runs idempotent migrations. Mirrors the
// llmbff.UsageStore pattern: nil pool → error; success path returns a usable
// store whose BudgetsFor/Set are safe under concurrent callers.
func NewPGStore(pool *pgxpool.Pool) (*PGStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("quota: pgxpool is nil")
	}
	s := &PGStore{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("quota migrate: %w", err)
	}
	return s, nil
}

func (s *PGStore) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS quota_budgets (
	id            BIGSERIAL PRIMARY KEY,
	workspace_id  TEXT NOT NULL,
	kind          TEXT NOT NULL,
	limit_value   DOUBLE PRECISION NOT NULL,
	period_start  TIMESTAMPTZ,
	period_end    TIMESTAMPTZ,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_quota_budgets_ws_kind ON quota_budgets(workspace_id, kind);
CREATE INDEX IF NOT EXISTS idx_quota_budgets_window ON quota_budgets(workspace_id, period_start, period_end);
`)
	return err
}

// BudgetsFor 返回 workspace 在与 t 相交期间的所有预算；t 通常是「现在」。
//
// 与 MemoryStore 的语义一致：PeriodStart/PeriodEnd 均为零值时永远命中，
// 否则 PeriodStart <= t < PeriodEnd。PeriodStart/PeriodEnd 的 NULL/零值
// 在 SQL 层用 IS NULL 表达（GO 零值 time.Time 经过 pgx 编码为零时间戳，
// 这里用 OR period_start IS NULL 兼容两种写法）。
func (s *PGStore) BudgetsFor(ctx context.Context, wsID string, t time.Time) ([]Budget, error) {
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, kind, limit_value, period_start, period_end
FROM quota_budgets
WHERE workspace_id = $1
  AND (period_start IS NULL OR period_start <= $2)
  AND (period_end   IS NULL OR period_end   >  $2)
`, wsID, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Budget
	for rows.Next() {
		var b Budget
		var start, end *time.Time
		if err := rows.Scan(&b.WorkspaceID, &b.Kind, &b.Limit, &start, &end); err != nil {
			return nil, err
		}
		if start != nil {
			b.PeriodStart = *start
		}
		if end != nil {
			b.PeriodEnd = *end
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Set 写入预算；period 为零值表示「无时段限制」。生产路径不暴露给前端，
// 仅供测试 / 内部 API 使用。
func (s *PGStore) Set(ctx context.Context, b Budget) error {
	if b.WorkspaceID == "" {
		return errEmptyWorkspace
	}
	var start, end interface{}
	if !b.PeriodStart.IsZero() {
		start = b.PeriodStart
	}
	if !b.PeriodEnd.IsZero() {
		end = b.PeriodEnd
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO quota_budgets (workspace_id, kind, limit_value, period_start, period_end)
VALUES ($1, $2, $3, $4, $5)
`, b.WorkspaceID, b.Kind, b.Limit, start, end)
	return err
}