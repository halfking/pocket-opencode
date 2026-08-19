package quota

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreConcurrentSetAndBudgetsFor(t *testing.T) {
	store := NewMemoryStore()
	const writers = 8
	const perWriter = 250
	var wg sync.WaitGroup
	wg.Add(writers)
	for writer := 0; writer < writers; writer++ {
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_ = store.Set(context.Background(), Budget{
					WorkspaceID: "ws-concurrent",
					Kind:        "tokens",
					Limit:       float64(writer*perWriter + i),
				})
				_, _ = store.BudgetsFor(context.Background(), "ws-concurrent", time.Now())
			}
		}(writer)
	}
	wg.Wait()

	got, err := store.BudgetsFor(context.Background(), "ws-concurrent", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != writers*perWriter {
		t.Fatalf("stored %d budgets, want %d", len(got), writers*perWriter)
	}
}
