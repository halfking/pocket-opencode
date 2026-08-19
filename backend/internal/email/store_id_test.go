package email

import (
	"sync"
	"testing"
)

func TestRandomIDConcurrentCallsAreUnique(t *testing.T) {
	const calls = 20000
	ids := make(chan string, calls)
	var wg sync.WaitGroup
	wg.Add(calls)
	for i := 0; i < calls; i++ {
		go func() {
			defer wg.Done()
			ids <- randomID("test")
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, calls)
	for id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate generated id %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != calls {
		t.Fatalf("generated %d unique IDs, want %d", len(seen), calls)
	}
}
