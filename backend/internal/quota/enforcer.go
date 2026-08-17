package quota

import (
	"context"
	"time"
)

// Decision is the enforcer's verdict on a single request. EnforceMode=false
// 时 Decision 永远是 Allow；EnforceMode=true 时根据预算余量返回 Allow 或
// Deny（带原因）。本轮 EnforceMode 默认 false。
type Decision struct {
	Allow  bool
	Reason string // 仅 Deny 时非空
	// Remaining 是按 Kind 聚合的余量（USD 或 tokens）。Allow 时也填，便于
	// 调试 / 后续返回给客户端。
	Remaining map[string]float64
}

// AlwaysAllowStrategy 不阻断任何请求；用于本轮默认部署。
type AlwaysAllowStrategy struct{}

// Decide 永远返回 Allow，Remaining 留空。
func (AlwaysAllowStrategy) Decide(_ context.Context, _ []Budget, _ DecisionInput) (Decision, error) {
	return Decision{Allow: true, Remaining: map[string]float64{}}, nil
}

// Strategy 是 Enforcer 的判定逻辑；实现必须线程安全。
type Strategy interface {
	Decide(ctx context.Context, budgets []Budget, in DecisionInput) (Decision, error)
}

// DecisionInput 是单次请求要传递给 Strategy 的信息。
type DecisionInput struct {
	WorkspaceID string
	Kind        string // "tokens" | "cost_usd" | "calls"
	Model       string
	// EstimatedCost / EstimatedTokens 是预先计算的资源估算；
	// 真实值要到调用返回才知道，Enforcer 只看估算。
	EstimatedCostUSD float64
	EstimatedTokens  int
	// Now 是判定时刻；测试可注入。
	Now time.Time
}

// Enforcer 把 Store + Strategy 包装成单一入口。Check 不修改 budgets；返回
// Decision 与一个 error（store 错误 / strategy 错误）。
type Enforcer struct {
	store    Store
	strategy Strategy
	name     string
	enforce  bool
}

// NewEnforcer 构造；strategy == nil 时用 AlwaysAllowStrategy（默认）。
func NewEnforcer(store Store, strategy Strategy) *Enforcer {
	if strategy == nil {
		strategy = AlwaysAllowStrategy{}
	}
	if store == nil {
		store = NewMemoryStore()
	}
	name := "always_allow"
	if _, ok := strategy.(AlwaysAllowStrategy); !ok {
		name = "custom"
	}
	return &Enforcer{store: store, strategy: strategy, name: name}
}

// Store 暴露底层 store，便于 handler /api/llm/quota 直接列出 budgets。
func (e *Enforcer) Store() Store { return e.store }

// StrategyName 是策略可读名字，写入 audit / 响应。
func (e *Enforcer) StrategyName() string { return e.name }

// EnforceMode 返回当前是否处于「硬拒绝」模式；当前默认 false。
func (e *Enforcer) EnforceMode() bool { return e.enforce }

// SetEnforceMode 切换硬拒绝。开启后 Decision.Allow=false 时调用方应阻断。
// 当前轮默认 false，仅用于可观测。
func (e *Enforcer) SetEnforceMode(v bool) { e.enforce = v }

// Check 拉取当前预算并调用 strategy。
func (e *Enforcer) Check(ctx context.Context, in DecisionInput) (Decision, error) {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	budgets, err := e.store.BudgetsFor(ctx, in.WorkspaceID, in.Now)
	if err != nil {
		return Decision{}, err
	}
	return e.strategy.Decide(ctx, budgets, in)
}
