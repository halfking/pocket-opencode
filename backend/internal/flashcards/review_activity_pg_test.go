package flashcards

// PostgreSQL integration test for the review-activity read used by the study
// streak (docs/学习muse phase P4c).
//
// The package's existing getTestPool() in store_test.go always returns nil, so
// there was no way to run anything against a real database here at all. This
// file adds a working gate for the one statement that needed it.
//
//	POCKET_TEST_POSTGRES_DSN=postgres://... go test ./internal/flashcards/
//
// It also asserts the index added for the streak exists, because a missing
// index is a silent performance regression: the query still returns the right
// rows, it just scans the user's entire review history every time the study
// hub opens.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newPgStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping flashcards integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "flashcards_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	s, err := NewStore(ctx, pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		pool.Close()
		rootPool.Close()
		t.Fatalf("EnsureSchema: %v", err)
	}
	return s, func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
}

func TestReviewTimestampsSince(t *testing.T) {
	s, cleanup := newPgStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()
	day := int64(86400)

	// The revlog row is written directly: the streak cares about review times,
	// and going through the card/rating path would test a different feature.
	seed := func(id, user string, reviewedAt int64) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO flashcard_revlog
				(id, card_id, user_id, reviewed_at, rating, prev_state, next_state)
			VALUES ($1, 'card-1', $2, $3, 3, 2, 2)`, id, user, reviewedAt); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("r-old", "alice", now-3*day)
	seed("r-mid", "alice", now-day)
	seed("r-new", "alice", now-60)
	seed("r-theirs", "bob", now-60)

	got, err := s.ReviewTimestampsSince(ctx, "alice", now-2*day)
	if err != nil {
		t.Fatalf("ReviewTimestampsSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d timestamps, want 2 (the two at/after the bound): %v", len(got), got)
	}
	// Oldest first: the streak's day-index mapping does not care about order,
	// but a caller that does would silently get a reversed history.
	if got[0] > got[1] {
		t.Errorf("timestamps are not oldest-first: %v", got)
	}

	// Another user's reviews must not leak into this user's streak.
	for _, ts := range got {
		if ts != now-day && ts != now-60 {
			t.Errorf("unexpected timestamp %d", ts)
		}
	}

	// A window in the past returns an empty list, not an error.
	none, err := s.ReviewTimestampsSince(ctx, "alice", now+10*day)
	if err != nil {
		t.Fatalf("ReviewTimestampsSince (future window): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %v, want an empty list", none)
	}
}

// The index was added specifically so the streak query is not a sequential
// scan. Its absence is invisible in the result set, which is exactly why it
// needs an assertion.
func TestReviewIndexExists(t *testing.T) {
	s, cleanup := newPgStore(t)
	defer cleanup()
	ctx := context.Background()

	var name string
	err := s.pool.QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'flashcard_revlog'
		  AND indexname = 'idx_flashcard_revlog_user_reviewed'`).Scan(&name)
	if err != nil {
		t.Fatalf("look up idx_flashcard_revlog_user_reviewed: %v", err)
	}
	if name != "idx_flashcard_revlog_user_reviewed" {
		t.Errorf("index name = %q", name)
	}
}
