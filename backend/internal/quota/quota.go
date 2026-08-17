// Package quota provides per-workspace cost / token budgets and an enforcement
// hook for the LLM BFF.
//
// 本轮交付的是「可观测、可审计、可扩展」骨架：
//
//   - Budget 描述一个 workspace 在某时间段内的硬上限；
//   - Store 读取预算（PG/内存两套实现，PG 实现本期不引入 schema 改动）；
//   - Enforcer 在调用 llmbff.Service 之前 Allow 检查，写 audit；
//   - 当前默认策略 AlwaysAllow：永远通过，但**所有 audit / 路径齐全**，未来
//     把策略换成真实预算（按 token / cost / time window）只需替换策略实现。
//
// 不在本轮引入「硬拒绝」（即不让 Allow 返回 false 时阻断调用）—— 这是有意
// 之举，避免误伤现有用户；§2 P3 验收「成本/配额」的下一步可以是引入
// Enforce 模式（Allow 返回 false → 429 + 结构化错误），但需要先在 UI 上
// 暴露给用户。当前阶段把骨架 + 定价表 + 可观测铺好。
package quota

import (
	"context"
	"time"
)

// Budget is the per-workspace resource cap. Zero values mean "unlimited".
//
// Kind 字段（"tokens"、"cost_usd"、"calls"）是预留多维度预算；当前 Enforcer
// 只看 CostUSD 一项，但 Store 与 Enforcer 接口设计为可扩展。
type Budget struct {
	WorkspaceID string    `json:"workspace_id"`
	Kind        string    `json:"kind"`           // tokens | cost_usd | calls
	Limit       float64   `json:"limit"`          // 数值上限；kind=cost_usd 时单位 USD
	PeriodStart time.Time `json:"period_start"`   // 闭区间
	PeriodEnd   time.Time `json:"period_end"`     // 开区间
}

// Store 读取当前预算。本期提供 MemoryStore（测试 / 无 DB 部署），未来
// 接入 PG 时新增 PGBudgetStore。
type Store interface {
	// BudgetsFor 返回 workspace 在与 t 相交期间的所有预算；t 通常是「现在」。
	BudgetsFor(ctx context.Context, wsID string, t time.Time) ([]Budget, error)
	// Set 在测试 / 内部 API 中写入预算；生产路径不暴露给前端。
	Set(ctx context.Context, b Budget) error
}

// MemoryStore 进程内预算；非持久化，仅用于无 DB 部署和测试。
type MemoryStore struct {
	rows map[string][]Budget // workspace_id -> 预算列表
}

// NewMemoryStore 构造一个空内存 store。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string][]Budget{}}
}

// BudgetsFor 返回相交预算（PeriodStart <= t < PeriodEnd）。无匹配返 nil。
func (m *MemoryStore) BudgetsFor(_ context.Context, wsID string, t time.Time) ([]Budget, error) {
	var out []Budget
	for _, b := range m.rows[wsID] {
		if b.PeriodStart.IsZero() && b.PeriodEnd.IsZero() {
			out = append(out, b)
			continue
		}
		if !b.PeriodStart.IsZero() && t.Before(b.PeriodStart) {
			continue
		}
		if !b.PeriodEnd.IsZero() && !t.Before(b.PeriodEnd) {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// Set 写入预算；period 为零值表示「无时段限制」。
func (m *MemoryStore) Set(_ context.Context, b Budget) error {
	if b.WorkspaceID == "" {
		return errEmptyWorkspace
	}
	m.rows[b.WorkspaceID] = append(m.rows[b.WorkspaceID], b)
	return nil
}

type quotaError string

func (e quotaError) Error() string { return string(e) }

const errEmptyWorkspace = quotaError("quota: workspace_id is required")
