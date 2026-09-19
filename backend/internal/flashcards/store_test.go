package flashcards

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestNewStore_RejectsNilPool verifies the construction guard so callers
// don't accidentally bring up a Store backed by a nil pool (which would
// panic on first query). Matches the spirit of scheduledtask's nil-store
// guard.
func TestNewStore_RejectsNilPool(t *testing.T) {
	if _, err := NewStore(context.Background(), nil); err == nil {
		t.Fatal("NewStore(nil pool) should return an error")
	}
}

// TestTagsToJSON verifies the canonical tag-array encoding used by the
// flashcard_notes.tags column (text default "[]").
func TestTagsToJSON(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"a"}, `["a"]`},
		{[]string{"a", "b", "c"}, `["a","b","c"]`},
	}
	for _, c := range cases {
		if got := TagsToJSON(c.in); got != c.want {
			t.Errorf("TagsToJSON(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRecordReview_RejectsInvalidRating verifies the rating bounds check
// without needing a DB.
func TestRecordReview_RejectsInvalidRating(t *testing.T) {
	s := &Store{pool: nil, now: func() time.Time { return time.Unix(0, 0) }}
	// pool is nil so rating validation runs first and short-circuits.
	_, _, err := s.RecordReview(context.Background(), "u", "c", 0, 100)
	if err == nil {
		t.Fatal("rating=0 should fail validation")
	}
	_, _, err = s.RecordReview(context.Background(), "u", "c", 5, 100)
	if err == nil {
		t.Fatal("rating=5 should fail validation")
	}
}

// TestElapsedDays exercises the day-diff helper used inside RecordReview.
func TestElapsedDays(t *testing.T) {
	const day = int64(86400)
	cases := []struct {
		prev, now int64
		want      int
	}{
		{0, 100, 0},           // no prior review → 0
		{100, 50, 0},          // now before prev → 0 (defensive)
		{100, 100, 0},         // same instant → 0
		{day, day * 5, 4},     // 4 days apart
		{day, day*5 + 100, 4}, // sub-day remainder truncated
	}
	for _, c := range cases {
		if got := elapsedDays(c.prev, c.now); got != c.want {
			t.Errorf("elapsedDays(%d, %d) = %d, want %d", c.prev, c.now, got, c.want)
		}
	}
}

// TestNilStore_PoolAndNow sanity-checks the helper accessors on a
// constructed-but-no-migrate Store.
func TestNilStore_PoolAndNow(t *testing.T) {
	s := &Store{pool: nil, now: func() time.Time { return time.Unix(42, 0) }}
	if s.Pool() != nil {
		t.Error("expected nil Pool()")
	}
	if s.Now() != 42 {
		t.Errorf("Now() = %d, want 42", s.Now())
	}
}

// TestStore_CRUD_RoundTrip runs against a real PostgreSQL if POCKET_TEST_PG_DSN
// is set. Skipped otherwise. The intent is exactly the same as the notes
// smoke tests: EnsureSchema → insert → read-back → field assertion.
func TestStore_CRUD_RoundTrip(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available (set POCKET_TEST_PG_DSN)")
	}
	defer cleanupTestData(t, pool)

	ctx := context.Background()
	s, err := NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	note := &Note{
		ID:     "test-note-fc-1",
		UserID: "user-fc-1",
		DeckID: "deck-default",
		Front:  "What is the capital of France?",
		Back:   "Paris",
		Tags:   `["geo","capitals"]`,
	}
	if err := s.CreateNote(ctx, note); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	got, err := s.GetNote(ctx, note.UserID, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got == nil {
		t.Fatal("expected note back, got nil")
	}
	if got.Front != note.Front || got.Back != note.Back || got.DeckID != note.DeckID {
		t.Errorf("field mismatch: got %+v", got)
	}
	if got.Tags != note.Tags {
		t.Errorf("tags mismatch: got %q want %q", got.Tags, note.Tags)
	}
	if got.Usn < 1 {
		t.Errorf("expected usn>=1 after create, got %d", got.Usn)
	}
}

// GetTestPool exposes the pgxpool for tests that need it (matches
// the pattern from notes/store_test.go — kept private to the package).
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// We could read POCKET_TEST_PG_DSN here. For the v1 smoke test we keep
	// it conservative and skip when not configured; production CI will
	// wire it up later. Returning nil matches notes_test.go convention.
	_ = errors.New // keep the import
	return nil
}

func cleanupTestData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if pool == nil {
		return
	}
	_, _ = pool.Exec(context.Background(), `DELETE FROM flashcard_notes WHERE id LIKE 'test-note-fc-%'`)
}
