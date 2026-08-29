package scheduledtask

import (
	"context"
	"errors"
	"testing"
)

func TestNilStoreIsUnavailable(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if s.Available() {
		t.Fatal("nil store should not be available")
	}
	if _, err := NewStore(ctx, nil); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("NewStore(nil) = %v, want ErrStoreUnavailable", err)
	}
	if err := s.CreateTask(ctx, &Task{ID: "x"}); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("CreateTask = %v", err)
	}
	if _, err := s.GetTaskScoped(ctx, "x", "u", "w"); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("GetTaskScoped = %v", err)
	}
	if _, err := s.ListTasksScoped(ctx, "u", "w", false, 0); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("ListTasksScoped = %v", err)
	}
	if err := s.DeleteTaskScoped(ctx, "x", "u", "w"); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("DeleteTaskScoped = %v", err)
	}
	if _, err := s.ClaimDue(ctx, 0, 0); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("ClaimDue = %v", err)
	}
	if err := s.UpdateTaskAfterRun(ctx, "x", RunStatusSuccess, "", 0); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("UpdateTaskAfterRun = %v", err)
	}
	if err := s.InsertRun(ctx, &Run{ID: "r"}); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("InsertRun = %v", err)
	}
	if err := s.FinishRun(ctx, "r", RunStatusSuccess, nil, "", ""); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("FinishRun = %v", err)
	}
	if _, err := s.ListRuns(ctx, "x", "u", "w", 0); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("ListRuns = %v", err)
	}
}

func TestNewIDAndAdvisoryKey(t *testing.T) {
	first := NewID()
	second := NewID()
	if len(first) != 32 || len(second) != 32 || first == second {
		t.Fatalf("unexpected IDs: %q %q", first, second)
	}
	if AdvisoryKey("task-a") != AdvisoryKey("task-a") {
		t.Fatal("advisory key must be stable")
	}
	if AdvisoryKey("task-a") == AdvisoryKey("task-b") {
		t.Fatal("different IDs unexpectedly share advisory key")
	}
}
