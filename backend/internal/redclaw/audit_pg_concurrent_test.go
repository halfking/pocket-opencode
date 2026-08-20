package redclaw

// audit_pg_concurrent_test.go — concurrency safety for PG-backed store.
//
// Spawns N goroutines that interleave Record and QueryRange against the same
// pool, then asserts the final durable count is exactly what was attempted
// (no lost writes, no double-count, no panic / data race at the Go level).
// PG handles row-level concurrency; this exercises the client paths under
// contention. Skips when POCKET_TEST_POSTGRES_DSN is unset.

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPGAuditStore_ConcurrentRecordQueryRange(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()

	const goroutines = 8
	const perGoroutine = 50
	total := goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				entry := &AuditEntry{
					Action:    "chat.send",
					UserID:    fmt.Sprintf("u-%d", g),
					TenantID:  "ws-conc",
					Timestamp: time.Now(),
				}
				if err := s.Record(entry); err != nil {
					t.Errorf("Record (g=%d i=%d): %v", g, i, err)
					return
				}
				// Concurrently page through the same tenant.
				if _, err := s.QueryRange(AuditQuery{TenantID: "ws-conc", Limit: 100}); err != nil {
					t.Errorf("QueryRange (g=%d i=%d): %v", g, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	entries, err := s.Query(AuditQuery{TenantID: "ws-conc", Limit: total})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != total {
		t.Fatalf("expected %d durable entries after concurrent writes, got %d", total, len(entries))
	}
}
