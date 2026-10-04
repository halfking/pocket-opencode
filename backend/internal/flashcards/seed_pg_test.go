package flashcards

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPGFlashcardStore brings up a Store against an isolated PG schema
// (random suffix, dropped on cleanup) so the bootstrap seed can be verified
// against real PostgreSQL without touching the dev schema. Same DSN
// convention as the other PG integration tests: POCKET_TEST_POSTGRES_DSN
// takes precedence, POCKET_POSTGRES_DSN is the fallback; no DSN → skip.
func newTestPGFlashcardStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := ""
	for _, key := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if value := os.Getenv(key); value != "" {
			dsn = value
			break
		}
	}
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN or POCKET_POSTGRES_DSN not set; skipping flashcards seed PG integration test")
	}

	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "flashcards_seed_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	scopedCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	scopedCfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	scopedPool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}

	cleanup := func() {
		scopedPool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}

	store, err := NewStore(ctx, scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		cleanup()
		t.Fatalf("EnsureSchema: %v", err)
	}
	return store, cleanup
}

// TestEnsureUserDefaults_PGIntegration verifies the first-run seed end to end
// against real PostgreSQL: a first-time user gets the default + welcome deck
// configs and the welcome notes/cards; the second call is a no-op; and a user
// who already has notes is never seeded.
func TestEnsureUserDefaults_PGIntegration(t *testing.T) {
	store, cleanup := newTestPGFlashcardStore(t)
	defer cleanup()

	ctx := context.Background()
	const user = "user-seedtest"

	seeded, err := store.EnsureUserDefaults(ctx, user)
	if err != nil {
		t.Fatalf("first EnsureUserDefaults: %v", err)
	}
	if !seeded {
		t.Fatal("first EnsureUserDefaults should seed a first-time user")
	}

	defaultCfg, err := store.GetDeckConfig(ctx, user, SeedDeckDefaultID)
	if err != nil || defaultCfg == nil {
		t.Fatalf("default deck config missing after seed: %v", err)
	}
	if defaultCfg.Name != SeedDeckDefaultName {
		t.Errorf("default deck name = %q, want %q", defaultCfg.Name, SeedDeckDefaultName)
	}
	welcomeCfg, err := store.GetDeckConfig(ctx, user, SeedDeckWelcomeID)
	if err != nil || welcomeCfg == nil {
		t.Fatalf("welcome deck config missing after seed: %v", err)
	}

	notes, err := store.ListNotesSince(ctx, user, 0, 100)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != len(WelcomeNotes()) {
		t.Fatalf("seeded %d notes, want %d", len(notes), len(WelcomeNotes()))
	}
	for _, n := range notes {
		if n.DeckID != SeedDeckWelcomeID {
			t.Errorf("note %s deck = %q, want %q", n.ID, n.DeckID, SeedDeckWelcomeID)
		}
	}

	cards, err := store.ListCardsSince(ctx, user, 0, 100)
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	if len(cards) != len(WelcomeNotes()) {
		t.Fatalf("seeded %d cards, want %d", len(cards), len(WelcomeNotes()))
	}
	for _, c := range cards {
		if c.State != 0 {
			t.Errorf("seeded card %s state = %d, want 0 (new)", c.ID, c.State)
		}
		if c.Due == 0 {
			t.Errorf("seeded card %s due = 0, want now (unix sec)", c.ID)
		}
	}

	// Second call: bootstrap marker (the default deck-config row) exists now,
	// so this must be a no-op even though the data is present.
	seededAgain, err := store.EnsureUserDefaults(ctx, user)
	if err != nil {
		t.Fatalf("second EnsureUserDefaults: %v", err)
	}
	if seededAgain {
		t.Error("second EnsureUserDefaults must not re-seed")
	}

	// Simulate the user wiping all flashcard content (soft-delete everything).
	for _, n := range notes {
		if err := store.SoftDeleteNote(ctx, user, n.ID); err != nil {
			t.Fatalf("soft delete note: %v", err)
		}
	}
	for _, deckID := range []string{SeedDeckDefaultID, SeedDeckWelcomeID} {
		if _, err := store.pool.Exec(ctx, `
			UPDATE flashcard_deck_config SET deleted_at=EXTRACT(EPOCH FROM now())::bigint
			WHERE user_id=$1 AND deck_id=$2`, user, deckID); err != nil {
			t.Fatalf("soft delete deck config: %v", err)
		}
	}
	seededAfterWipe, err := store.EnsureUserDefaults(ctx, user)
	if err != nil {
		t.Fatalf("EnsureUserDefaults after wipe: %v", err)
	}
	if seededAfterWipe {
		t.Error("user who deleted the starter content must not be re-seeded")
	}

	// A user who already has real notes is never seeded.
	const user2 = "user-existing"
	if err := store.CreateNote(ctx, &Note{ID: "note-existing-1", UserID: user2, DeckID: "mydeck", Front: "f", Back: "b"}); err != nil {
		t.Fatalf("create existing-user note: %v", err)
	}
	seededExisting, err := store.EnsureUserDefaults(ctx, user2)
	if err != nil {
		t.Fatalf("EnsureUserDefaults for existing user: %v", err)
	}
	if seededExisting {
		t.Error("user with existing notes must not be seeded")
	}
	user2Notes, err := store.ListNotesSince(ctx, user2, 0, 100)
	if err != nil {
		t.Fatalf("list user2 notes: %v", err)
	}
	if len(user2Notes) != 1 {
		t.Errorf("existing user notes = %d, want 1 (their own)", len(user2Notes))
	}
}
