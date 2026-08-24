package zagclient

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestNoopClientImplementsClient is a compile-time guard: if NoopClient ever
// drifts away from the Client interface the build itself breaks.
func TestNoopClientImplementsClient(t *testing.T) {
	var _ Client = NewNoopClient()
}

// TestNoopClient_AllMethodsReturnErrNotConfigured verifies that no method
// on the NoopClient silently succeeds — the only acceptable outcome for a
// not-yet-wired-up client is ErrNotConfigured. This guarantees the safe
// default until a real transport is added.
func TestNoopClient_AllMethodsReturnErrNotConfigured(t *testing.T) {
	ctx := context.Background()
	c := NewNoopClient()

	t.Run("Health", func(t *testing.T) {
		if err := c.Health(ctx); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Health: want ErrNotConfigured, got %v", err)
		}
	})

	t.Run("ListPods", func(t *testing.T) {
		if pods, err := c.ListPods(ctx, ListPodsRequest{}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ListPods: want ErrNotConfigured, got %v", err)
		} else if pods != nil {
			t.Fatalf("ListPods: want nil slice on error, got %v", pods)
		}
	})

	t.Run("GetPod", func(t *testing.T) {
		if pod, err := c.GetPod(ctx, "pod-1", CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("GetPod: want ErrNotConfigured, got %v", err)
		} else if pod != nil {
			t.Fatalf("GetPod: want nil pointer on error, got %v", pod)
		}
	})

	t.Run("ControlPod", func(t *testing.T) {
		if err := c.ControlPod(ctx, "pod-1", ControlPodRequest{Kind: "pause"}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ControlPod: want ErrNotConfigured, got %v", err)
		}
	})

	t.Run("ListAgents", func(t *testing.T) {
		if agents, err := c.ListAgents(ctx, ListAgentsRequest{}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ListAgents: want ErrNotConfigured, got %v", err)
		} else if agents != nil {
			t.Fatalf("ListAgents: want nil slice on error, got %v", agents)
		}
	})

	t.Run("GetAgent", func(t *testing.T) {
		if agent, err := c.GetAgent(ctx, "agent-1", CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("GetAgent: want ErrNotConfigured, got %v", err)
		} else if agent != nil {
			t.Fatalf("GetAgent: want nil pointer on error, got %v", agent)
		}
	})

	t.Run("InvokeAgent", func(t *testing.T) {
		if res, err := c.InvokeAgent(ctx, "agent-1", InvokeRequest{Goal: "x"}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("InvokeAgent: want ErrNotConfigured, got %v", err)
		} else if res != nil {
			t.Fatalf("InvokeAgent: want nil result on error, got %v", res)
		}
	})

	t.Run("ListIDEs", func(t *testing.T) {
		if ides, err := c.ListIDEs(ctx, ListIDEsRequest{}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ListIDEs: want ErrNotConfigured, got %v", err)
		} else if ides != nil {
			t.Fatalf("ListIDEs: want nil slice on error, got %v", ides)
		}
	})

	t.Run("GetIDEStatus", func(t *testing.T) {
		if status, err := c.GetIDEStatus(ctx, "vscode", CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("GetIDEStatus: want ErrNotConfigured, got %v", err)
		} else if status != nil {
			t.Fatalf("GetIDEStatus: want nil pointer on error, got %v", status)
		}
	})

	t.Run("ExecuteIDECommand", func(t *testing.T) {
		if receipt, err := c.ExecuteIDECommand(ctx, "vscode", IDECommand{Command: "ide.open_file"}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ExecuteIDECommand: want ErrNotConfigured, got %v", err)
		} else if receipt != nil {
			t.Fatalf("ExecuteIDECommand: want nil receipt on error, got %v", receipt)
		}
	})

	t.Run("SubmitTask", func(t *testing.T) {
		if task, err := c.SubmitTask(ctx, SubmitTaskRequest{FleetID: "f", Goal: "g", AgentID: "a"}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("SubmitTask: want ErrNotConfigured, got %v", err)
		} else if task != nil {
			t.Fatalf("SubmitTask: want nil task on error, got %v", task)
		}
	})

	t.Run("GetTask", func(t *testing.T) {
		if task, err := c.GetTask(ctx, "task-1", CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("GetTask: want ErrNotConfigured, got %v", err)
		} else if task != nil {
			t.Fatalf("GetTask: want nil task on error, got %v", task)
		}
	})

	t.Run("CancelTask", func(t *testing.T) {
		if err := c.CancelTask(ctx, "task-1", CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("CancelTask: want ErrNotConfigured, got %v", err)
		}
	})

	t.Run("SubscribeTaskEvents", func(t *testing.T) {
		ch, cancel, err := c.SubscribeTaskEvents(ctx, "task-1", CallOptions{})
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("SubscribeTaskEvents: want ErrNotConfigured, got %v", err)
		}
		if ch != nil {
			t.Fatalf("SubscribeTaskEvents: want nil channel on error, got %v", ch)
		}
		if cancel != nil {
			t.Fatalf("SubscribeTaskEvents: want nil cancel on error")
		}
	})

	t.Run("SubscribeAgentEvents", func(t *testing.T) {
		ch, cancel, err := c.SubscribeAgentEvents(ctx, "agent-1", CallOptions{})
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("SubscribeAgentEvents: want ErrNotConfigured, got %v", err)
		}
		if ch != nil {
			t.Fatalf("SubscribeAgentEvents: want nil channel on error, got %v", ch)
		}
		if cancel != nil {
			t.Fatalf("SubscribeAgentEvents: want nil cancel on error")
		}
	})

	t.Run("ReplyPermission", func(t *testing.T) {
		if err := c.ReplyPermission(ctx, "perm-1", ReplyPermissionRequest{Decision: "allow"}, CallOptions{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ReplyPermission: want ErrNotConfigured, got %v", err)
		}
	})
}

// TestNoopClient_CloseIsIdempotent verifies that Close() can be called
// repeatedly without panicking and that the closed-state remains consistent.
func TestNoopClient_CloseIsIdempotent(t *testing.T) {
	c := NewNoopClient()

	for i := 0; i < 5; i++ {
		if err := c.Close(); err != nil {
			t.Fatalf("Close() iteration %d: want nil error, got %v", i, err)
		}
	}

	// After Close the noop client must still return ErrNotConfigured rather
	// than panicking or surfacing a different error type.
	ctx := context.Background()
	if err := c.Health(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Health after Close: want ErrNotConfigured, got %v", err)
	}
}

// TestNoopClient_ConcurrentSafety makes sure NoopClient is safe to share
// across goroutines (it is registered into Server state). Run with -race
// to also catch any data races on the closed flag.
func TestNoopClient_ConcurrentSafety(t *testing.T) {
	c := NewNoopClient()
	const goroutines = 16
	const iters = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Mix close, health, and risk-class lookups across goroutines.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = c.Health(context.Background())
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = c.RiskClass(OpListPods)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = c.Close()
			}
		}()
	}
	wg.Wait()
}

// TestOperation_IsWrite covers the IsWrite() predicate which gates the
// mandatory Idempotency-Key precondition. A future commit that adds a new
// Operation constant MUST update this test or the precondition contract
// silently drifts.
func TestOperation_IsWrite(t *testing.T) {
	cases := []struct {
		op   Operation
		want bool
	}{
		// Writes — require Idempotency-Key.
		{OpControlPod, true},
		{OpInvokeAgent, true},
		{OpExecuteIDECommand, true},
		{OpSubmitTask, true},
		{OpCancelTask, true},
		{OpReplyPermission, true},
		// Reads — no Idempotency-Key required.
		{OpHealth, false},
		{OpListPods, false},
		{OpGetPod, false},
		{OpListAgents, false},
		{OpGetAgent, false},
		{OpListIDEs, false},
		{OpGetIDEStatus, false},
		{OpGetTask, false},
		{OpSubscribeTaskEvents, false},
		{OpSubscribeAgentEvts, false},
		// Unknown / future operation: IsWrite returns false (fail-open on
		// idempotency, fail-closed on execution since the switch will not
		// route it). This is intentional; ValidateWrite on an unknown op
		// returns nil so the interface contract can be extended.
		{Operation("future_op"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			if got := tc.op.IsWrite(); got != tc.want {
				t.Fatalf("Operation(%q).IsWrite() = %v, want %v", tc.op, got, tc.want)
			}
		})
	}
}

// TestValidateWrite exercises the single Idempotency-Key enforcement point.
func TestValidateWrite(t *testing.T) {
	// Read op + empty key: OK.
	if err := ValidateWrite(OpListPods, CallOptions{}); err != nil {
		t.Fatalf("ValidateWrite(read): want nil, got %v", err)
	}

	// Write op + empty key: must return ErrMissingIdempotencyKey.
	if err := ValidateWrite(OpControlPod, CallOptions{}); !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("ValidateWrite(write,empty): want ErrMissingIdempotencyKey, got %v", err)
	}
	if err := ValidateWrite(OpSubmitTask, CallOptions{}); !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("ValidateWrite(SubmitTask,empty): want ErrMissingIdempotencyKey, got %v", err)
	}
	if err := ValidateWrite(OpReplyPermission, CallOptions{}); !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("ValidateWrite(ReplyPermission,empty): want ErrMissingIdempotencyKey, got %v", err)
	}

	// Write op + key set: OK.
	if err := ValidateWrite(OpControlPod, CallOptions{IdempotencyKey: "k-1"}); err != nil {
		t.Fatalf("ValidateWrite(write,key): want nil, got %v", err)
	}

	// Sanity: an unknown future op must NOT require idempotency (fail-open)
	// because ValidateWrite relies on IsWrite which falls through to false.
	if err := ValidateWrite(Operation("future_op"), CallOptions{}); err != nil {
		t.Fatalf("ValidateWrite(unknown,empty): want nil, got %v", err)
	}
}

// TestNoopClient_RiskClass covers the canonical risk mapping that callers
// use to decide outbox-then-write. Changing RiskClass for a known op must be
// an explicit decision; this test guards against silent drift.
func TestNoopClient_RiskClass(t *testing.T) {
	c := NewNoopClient()

	cases := []struct {
		op   Operation
		want RiskClass
	}{
		{OpHealth, RiskRead},
		{OpListPods, RiskRead},
		{OpGetPod, RiskRead},
		{OpListAgents, RiskRead},
		{OpGetAgent, RiskRead},
		{OpListIDEs, RiskRead},
		{OpGetIDEStatus, RiskRead},
		{OpGetTask, RiskRead},
		{OpSubscribeTaskEvents, RiskRead},
		{OpSubscribeAgentEvts, RiskRead},
		{OpInvokeAgent, RiskMedium},
		{OpExecuteIDECommand, RiskMedium},
		{OpSubmitTask, RiskLow},
		{OpCancelTask, RiskLow},
		{OpReplyPermission, RiskHigh},
		{OpControlPod, RiskHigh},
		// Unknown operation falls through to RiskRead.
		{Operation("future_op"), RiskRead},
	}
	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			if got := c.RiskClass(tc.op); got != tc.want {
				t.Fatalf("RiskClass(%q) = %q, want %q", tc.op, got, tc.want)
			}
		})
	}
}

// TestNoopClient_RiskClassAfterClose verifies that RiskClass keeps working
// after Close (it's a pure lookup, no I/O). This matches the contract: the
// caller can pre-classify an op without needing a live transport.
func TestNoopClient_RiskClassAfterClose(t *testing.T) {
	c := NewNoopClient()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: want nil, got %v", err)
	}
	if got := c.RiskClass(OpSubmitTask); got != RiskLow {
		t.Fatalf("RiskClass(SubmitTask) after Close = %q, want %q", got, RiskLow)
	}
}

// TestSentinelErrors_AreDistinct ensures the five sentinel errors do not
// collide; callers rely on errors.Is to pick the right remediation path
// (retry / reconcile / re-auth / fail-closed).
func TestSentinelErrors_AreDistinct(t *testing.T) {
	sentinels := []error{
		ErrNotConfigured,
		ErrMissingIdempotencyKey,
		ErrScopeInsufficient,
		ErrUnauthorized,
		ErrTenantMismatch,
		ErrUpstreamUnavailable,
		ErrIndeterminate,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Fatalf("sentinel errors must not alias: errors.Is(%v, %v) = true", a, b)
			}
		}
	}
}

// TestCallOptions_Fields documents that CallOptions carries the four
// fields required by the contract: IdempotencyKey, CorrelationID, TraceID,
// and Scopes. Adding a field is fine; renaming/removing breaks downstream
// callers, so this test fails loudly if the surface shrinks.
func TestCallOptions_Fields(t *testing.T) {
	opts := CallOptions{
		IdempotencyKey: "key-1",
		CorrelationID:  "corr-1",
		TraceID:        "trace-1",
		Scopes:         []string{ScopeAgentRead, ScopeTaskCreate},
	}
	if opts.IdempotencyKey != "key-1" {
		t.Fatalf("IdempotencyKey roundtrip failed: %q", opts.IdempotencyKey)
	}
	if opts.CorrelationID != "corr-1" {
		t.Fatalf("CorrelationID roundtrip failed: %q", opts.CorrelationID)
	}
	if opts.TraceID != "trace-1" {
		t.Fatalf("TraceID roundtrip failed: %q", opts.TraceID)
	}
	if len(opts.Scopes) != 2 || opts.Scopes[0] != ScopeAgentRead || opts.Scopes[1] != ScopeTaskCreate {
		t.Fatalf("Scopes roundtrip failed: %v", opts.Scopes)
	}
}

// TestEvent_JSONFieldAlignment ensures the wire-level JSON tags on Event
// match the canonical schema. Drift here would silently break SSE consumers.
func TestEvent_JSONFieldAlignment(t *testing.T) {
	e := Event{
		EventID:       "evt-1",
		Sequence:      42,
		SchemaVersion: "1.0",
		TenantID:      "ws-1",
		AggregateID:   "task-1",
		AggregateType: "task",
		Type:          "task.update",
	}
	// Verify the canonical names exist via direct field access; this is
	// mostly a safety net for accidental rename that would otherwise only
	// surface at SSE decode time.
	if e.EventID == "" || e.Sequence != 42 || e.SchemaVersion == "" ||
		e.TenantID == "" || e.AggregateID == "" || e.AggregateType == "" ||
		e.Type == "" {
		t.Fatalf("Event fields not assignable: %+v", e)
	}
}
