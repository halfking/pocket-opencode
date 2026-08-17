package email

// scheduler_nil_fetcher_test.go — pins the runtime semantics of a Scheduler
// constructed without a fetcher (NewScheduler(store, nil, true)).
//
// The constructor is already nil-safe (commit 5c59e5b guarded the crypto copy),
// but tick() still spawned a sync goroutine that dereferenced the nil fetcher
// every poll cycle, surfacing as a recovered panic log line per account per
// minute. nil fetcher is a legitimate degraded mode exercised by the intent
// tests and by deployments without a configured IMAP fetcher, so poll/ tick
// must skip fetch sync cleanly rather than noisily recover.

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

// tick() with a nil fetcher must be a no-op: it logs an explicit "fetcher not
// configured" skip and never reaches the s.fetcher.Sync goroutine that would
// otherwise panic and be recovered.
func TestTick_NilFetcherIsNoOp(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()

	// An enabled account gives tick() real work to schedule sync for; the
	// contract is that this work is skipped, not attempted-and-recovered.
	seedAccount(t, store, "nil-fetch-acct", "u", "ws")

	var logBuf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	s.tick(context.Background())
	// tick() schedules sync work in goroutines; give them a beat so an
	// unguarded path surfaces its recovered panic before we assert.
	time.Sleep(300 * time.Millisecond)

	out := logBuf.String()
	if strings.Contains(out, "panicked") {
		t.Fatalf("tick() with nil fetcher recovered a panic instead of skipping: %s", out)
	}
	if !strings.Contains(out, "fetcher not configured") {
		t.Fatalf("expected nil-fetcher skip log, got: %s", out)
	}
}
