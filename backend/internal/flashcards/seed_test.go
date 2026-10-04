package flashcards

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestShouldSeedUserDefaults covers the bootstrap decision: only a first-time
// user (no marker row, no notes) is seeded; the marker deliberately counts a
// soft-deleted default deck config so users who deleted the starter content
// are never re-seeded.
func TestShouldSeedUserDefaults(t *testing.T) {
	cases := []struct {
		name        string
		hasMarker   bool
		hasNotes    bool
		wantSeedRun bool
	}{
		{"first-time user", false, false, true},
		{"marker row exists (default deck)", true, false, false},
		{"user already has notes", false, true, false},
		{"both", true, true, false},
	}
	for _, c := range cases {
		if got := shouldSeedUserDefaults(c.hasMarker, c.hasNotes); got != c.wantSeedRun {
			t.Errorf("%s: shouldSeedUserDefaults(%v,%v) = %v, want %v",
				c.name, c.hasMarker, c.hasNotes, got, c.wantSeedRun)
		}
	}
}

// TestDefaultDeckConfig verifies the seed deck config carries the Anki-aligned
// §1.4 defaults (same zero-fill values as UpsertDeckConfig).
func TestDefaultDeckConfig(t *testing.T) {
	d := DefaultDeckConfig("u1", SeedDeckDefaultID, SeedDeckDefaultName)
	if d.DeckID != SeedDeckDefaultID || d.UserID != "u1" || d.Name != SeedDeckDefaultName {
		t.Fatalf("identity fields wrong: %+v", d)
	}
	if d.NewPerDay != 20 || d.ReviewsPerDay != 200 {
		t.Errorf("limits: new=%d reviews=%d, want 20/200", d.NewPerDay, d.ReviewsPerDay)
	}
	if len(d.LearningStepsMin) != 2 || d.LearningStepsMin[0] != 1 || d.LearningStepsMin[1] != 10 {
		t.Errorf("learning steps: %v, want [1 10]", d.LearningStepsMin)
	}
	if d.GraduatingIntervalDays != 1 || d.EasyIntervalDays != 4 {
		t.Errorf("intervals: grad=%d easy=%d, want 1/4", d.GraduatingIntervalDays, d.EasyIntervalDays)
	}
	if d.DesiredRetention != 0.9 {
		t.Errorf("retention: %v, want 0.9", d.DesiredRetention)
	}
}

// TestWelcomeNotes verifies the starter content is well-formed: non-empty,
// front/back filled, valid tag JSON, and includes a cloze demo note so the
// Phase 3 masking is discoverable from first run.
func TestWelcomeNotes(t *testing.T) {
	notes := WelcomeNotes()
	if len(notes) == 0 {
		t.Fatal("welcome notes must not be empty")
	}
	hasCloze := false
	for i, n := range notes {
		if strings.TrimSpace(n.Front) == "" || strings.TrimSpace(n.Back) == "" {
			t.Errorf("note %d: front and back must be filled", i)
		}
		var tags []string
		if err := json.Unmarshal([]byte(TagsToJSON(n.Tags)), &tags); err != nil {
			t.Errorf("note %d: tags not valid JSON array: %v", i, err)
		}
		if strings.Contains(n.Front, "{{c1::") {
			hasCloze = true
		}
	}
	if !hasCloze {
		t.Error("welcome notes should include a {{c1::...}} cloze demo")
	}
}

// TestSeedIDsDeterministic verifies the per-user deterministic ids so a
// concurrent double bootstrap collapses onto the same primary keys.
func TestSeedIDsDeterministic(t *testing.T) {
	if seedNoteID("u1", 0) != seedNoteID("u1", 0) {
		t.Error("seed note ids must be deterministic per user+index")
	}
	if seedNoteID("u1", 0) == seedNoteID("u2", 0) {
		t.Error("seed note ids must differ across users")
	}
	if seedCardID("u1", 0) == seedNoteID("u1", 0) {
		t.Error("card and note id spaces must not collide")
	}
}

// TestEnsureUserDefaults_EmptyUserID verifies the guard: no user, no query
// (store pool is nil — touching it would panic), no seed.
func TestEnsureUserDefaults_EmptyUserID(t *testing.T) {
	s := &Store{pool: nil, now: func() time.Time { return time.Unix(0, 0) }}
	seeded, err := s.EnsureUserDefaults(context.Background(), "")
	if err != nil {
		t.Fatalf("empty userID should not error: %v", err)
	}
	if seeded {
		t.Error("empty userID must not seed")
	}
}
