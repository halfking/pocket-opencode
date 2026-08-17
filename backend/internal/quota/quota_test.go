package quota

import (
	"context"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

func TestMemoryStore_BudgetsForFiltersByPeriod(t *testing.T) {
	s := NewMemoryStore()
	mustSet(t, s, Budget{WorkspaceID: "ws-a", Kind: "cost_usd", Limit: 100, PeriodStart: time.Now().Add(-time.Hour), PeriodEnd: time.Now().Add(time.Hour)})
	mustSet(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000, PeriodStart: time.Now().Add(time.Hour), PeriodEnd: time.Now().Add(2 * time.Hour)})

	got, err := s.BudgetsFor(context.Background(), "ws-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 budget in window, got %d", len(got))
	}
	if got[0].Kind != "cost_usd" {
		t.Fatalf("expected cost_usd budget, got %q", got[0].Kind)
	}
}

func TestMemoryStore_BudgetsForAcceptsZeroPeriod(t *testing.T) {
	s := NewMemoryStore()
	mustSet(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000})

	got, _ := s.BudgetsFor(context.Background(), "ws-a", time.Now())
	if len(got) != 1 {
		t.Fatalf("zero-period budget must always apply, got %d", len(got))
	}
}

func TestMemoryStore_RejectsEmptyWorkspace(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Set(context.Background(), Budget{Kind: "tokens"}); err == nil {
		t.Fatal("expected error for empty workspace_id")
	}
}

func TestEnforcer_AlwaysAllowStrategy(t *testing.T) {
	e := NewEnforcer(NewMemoryStore(), nil) // 默认 AlwaysAllowStrategy
	d, err := e.Check(context.Background(), DecisionInput{WorkspaceID: "ws-a", Kind: "cost_usd", EstimatedCostUSD: 99})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow {
		t.Fatalf("AlwaysAllowStrategy must allow, got Deny: %s", d.Reason)
	}
}

func TestEnforcer_PassesBudgetsToStrategy(t *testing.T) {
	s := NewMemoryStore()
	mustSet(t, s, Budget{WorkspaceID: "ws-a", Kind: "cost_usd", Limit: 50})
	var captured []Budget
	strat := captureStrategy(func(ctx context.Context, bs []Budget, _ DecisionInput) (Decision, error) {
		captured = bs
		return Decision{Allow: true, Remaining: map[string]float64{}}, nil
	})
	e := NewEnforcer(s, strat)
	if _, err := e.Check(context.Background(), DecisionInput{WorkspaceID: "ws-a", Kind: "cost_usd"}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].Limit != 50 {
		t.Fatalf("strategy did not receive budget: %+v", captured)
	}
}

func TestEnforcer_NilStoreFallsBackToMemory(t *testing.T) {
	e := NewEnforcer(nil, nil)
	if _, err := e.Check(context.Background(), DecisionInput{WorkspaceID: "ws-a"}); err != nil {
		t.Fatalf("nil store must not error: %v", err)
	}
}

func TestCostFromUsage_KnownModel(t *testing.T) {
	// gpt-4o: $5/1M input, $15/1M output. 1M input + 1M output = $20.
	got := CostFromUsage("gpt-4o", llmbff.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	want := 20.0
	if !floatEq(got, want) {
		t.Fatalf("gpt-4o 1M+1M tokens: got %v, want %v", got, want)
	}
}

func TestCostFromUsage_UnknownModelUsesFallback(t *testing.T) {
	got := CostFromUsage("totally-unknown-model-x", llmbff.Usage{PromptTokens: 1000, CompletionTokens: 1000})
	if got <= 0 {
		t.Fatalf("unknown model must use fallback price, got %v", got)
	}
	// 兜底价格 0.001/1K input + 0.003/1K output * 1K = 0.001 + 0.003 = 0.004
	if !floatEq(got, 0.004) {
		t.Fatalf("unknown model fallback price: got %v, want 0.004", got)
	}
}

func TestCostFromUsage_ZeroTokensReturnsZero(t *testing.T) {
	if got := CostFromUsage("gpt-4o", llmbff.Usage{}); got != 0 {
		t.Fatalf("zero tokens must yield zero cost, got %v", got)
	}
}

func TestApplyCost_WritesBackUsage(t *testing.T) {
	in := llmbff.Usage{PromptTokens: 1000, CompletionTokens: 500}
	out := ApplyCost("gpt-4o-mini", in)
	if out.CostUSD == 0 {
		t.Fatal("ApplyCost must populate CostUSD")
	}
	if in.CostUSD != 0 {
		t.Fatal("ApplyCost must not mutate input")
	}
}

// 真实场景：adapter 把 token 计数转成 cost；多模型回归。
func TestApplyCost_ModelScenarios(t *testing.T) {
	cases := []struct {
		name        string
		model       string
		input       int
		output      int
		wantAtLeast  float64
		wantAtMost   float64
	}{
		{"gpt-4o", "gpt-4o", 1_000_000, 1_000_000, 19.9, 20.1},
		{"claude-sonnet", "claude-3-5-sonnet", 1_000_000, 1_000_000, 17.9, 18.1},
		{"embedding", "text-embedding-3-small", 10_000, 0, 0.0001, 0.0003},
		{"unknown", "completely-unknown-model", 1000, 1000, 0.003, 0.005},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CostFromUsage(c.model, llmbff.Usage{PromptTokens: c.input, CompletionTokens: c.output})
			if got < c.wantAtLeast || got > c.wantAtMost {
				t.Fatalf("%s: got %v, want in [%v, %v]", c.name, got, c.wantAtLeast, c.wantAtMost)
			}
		})
	}
}

// helpers

func mustSet(t *testing.T, s *MemoryStore, b Budget) {
	t.Helper()
	if err := s.Set(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func floatEq(a, b float64) bool {
	const eps = 1e-6
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}

// captureStrategy 把 strategy 调用捕获下来便于断言。
type captureStrategy func(ctx context.Context, bs []Budget, in DecisionInput) (Decision, error)

func (c captureStrategy) Decide(ctx context.Context, bs []Budget, in DecisionInput) (Decision, error) {
	return c(ctx, bs, in)
}
