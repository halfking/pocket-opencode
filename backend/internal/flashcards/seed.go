package flashcards

import (
	"context"
	"fmt"
)

// Bootstrap seed: initialise flashcard data for a first-time user, mirroring
// what the Anki project does when a fresh collection is created — a "Default"
// deck (Anki deck id=1) plus default deck options; AnkiDroid additionally
// ships a small welcome starter deck so the module is not empty on first run.
// openpocket mirrors both: the deck configs land in flashcard_deck_config and
// the welcome notes/cards in flashcard_notes / flashcard_cards.
//
// Seeding is idempotent and never touches a user who already has data:
//   - a soft-deleted seed deck config still counts as the bootstrap marker,
//     so a user who deleted the welcome content is not re-seeded;
//   - note/card ids are deterministic per user + ON CONFLICT DO NOTHING, so
//     two devices racing the initial full sync cannot duplicate rows.

const (
	// SeedDeckDefaultID mirrors Anki's "Default" deck (id=1).
	SeedDeckDefaultID = "default"
	// SeedDeckWelcomeID holds the starter notes; users can delete it whole.
	SeedDeckWelcomeID = "welcome"
)

// SeedDeckDefaultName is the canonical Anki default-deck name.
const SeedDeckDefaultName = "Default"

// SeedDeckWelcomeName is the starter-deck display name (zh-CN first, matching
// the app's default locale).
const SeedDeckWelcomeName = "欢迎使用 OpenPocket 闪卡"

// DefaultDeckConfig returns a DeckConfig with the Anki-aligned §1.4 defaults
// (same zero-fill values as UpsertDeckConfig: 20 new/day, 200 reviews/day,
// learning steps [1,10] min, graduating 1d, easy 4d, retention 0.9).
func DefaultDeckConfig(userID, deckID, name string) *DeckConfig {
	return &DeckConfig{
		DeckID:                 deckID,
		UserID:                 userID,
		Name:                   name,
		NewPerDay:              20,
		ReviewsPerDay:          200,
		LearningStepsMin:       []int{1, 10},
		GraduatingIntervalDays: 1,
		EasyIntervalDays:       4,
		DesiredRetention:       0.9,
	}
}

// seedNoteSpec is one starter note; v1 creates exactly one card per note.
type seedNoteSpec struct {
	Front string
	Back  string
	Tags  []string
}

// WelcomeNotes returns the starter notes. One of them exercises the Phase 3
// cloze syntax ({{c1::...}}) so the review-view masking is discoverable.
func WelcomeNotes() []seedNoteSpec {
	return []seedNoteSpec{
		{
			Front: "OpenPocket 闪卡是什么？",
			Back:  "一个内置的间隔重复（spaced repetition）记忆工具：卡片按遗忘曲线安排复习，到期卡片会出现在牌组的待复习队列里。",
			Tags:  []string{"welcome", "说明"},
		},
		{
			Front: "复习时的四个按钮怎么选？",
			Back:  "忘记 → Again；想起来了但很吃力 → Hard；顺利想起 → Good；轻松秒答 → Easy。评分决定卡片下次到期的时间。",
			Tags:  []string{"welcome", "说明"},
		},
		{
			Front: "{{c1::FSRS}} 算法会根据你的每次评分动态安排复习间隔。",
			Back:  "FSRS（Free Spaced Repetition Scheduler）是目前先进的间隔重复调度算法。这条卡片演示了 Cloze 挖空语法。",
			Tags:  []string{"welcome", "Cloze"},
		},
		{
			Front: "如何添加自己的卡片？",
			Back:  "进入任意牌组点击右上角 +，填写正面和背面即可；也支持在设置里导入 / 导出 JSON。",
			Tags:  []string{"welcome", "操作"},
		},
	}
}

// shouldSeedUserDefaults is the pure bootstrap decision: only a user with no
// notes at all and no bootstrap marker (the default deck-config row, even a
// soft-deleted one) gets seeded.
func shouldSeedUserDefaults(hasBootstrapMarker, hasNotes bool) bool {
	return !hasBootstrapMarker && !hasNotes
}

// seedNoteID / seedCardID are deterministic per user so a concurrent double
// bootstrap collapses onto the same primary keys.
func seedNoteID(userID string, i int) string { return fmt.Sprintf("seednote.%s.%d", userID, i) }
func seedCardID(userID string, i int) string { return fmt.Sprintf("seedcard.%s.%d", userID, i) }

// EnsureUserDefaults seeds the initial flashcard data (default + welcome deck
// configs, welcome notes/cards) for a first-time user. It is a no-op returning
// (false, nil) for users who already have notes or an existing bootstrap
// marker. Returns true when seeding actually ran.
func (s *Store) EnsureUserDefaults(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	var hasMarker, hasNotes bool
	err := s.pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM flashcard_deck_config
				WHERE user_id=$1 AND deck_id=$2),
			EXISTS (SELECT 1 FROM flashcard_notes
				WHERE user_id=$1 AND deleted_at IS NULL)`,
		userID, SeedDeckDefaultID).Scan(&hasMarker, &hasNotes)
	if err != nil {
		return false, fmt.Errorf("flashcards bootstrap check: %w", err)
	}
	if !shouldSeedUserDefaults(hasMarker, hasNotes) {
		return false, nil
	}

	now := s.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("flashcards bootstrap begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, d := range []*DeckConfig{
		DefaultDeckConfig(userID, SeedDeckDefaultID, SeedDeckDefaultName),
		DefaultDeckConfig(userID, SeedDeckWelcomeID, SeedDeckWelcomeName),
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO flashcard_deck_config
				(deck_id, user_id, name, new_per_day, reviews_per_day, learning_steps_min,
				 graduating_interval_days, easy_interval_days, fsrs_weights, desired_retention,
				 usn, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$11)
			ON CONFLICT (deck_id, user_id) DO NOTHING`,
			d.DeckID, d.UserID, d.Name, d.NewPerDay, d.ReviewsPerDay, []int32{1, 10},
			d.GraduatingIntervalDays, d.EasyIntervalDays, d.FSRSWeights,
			d.DesiredRetention, now); err != nil {
			return false, fmt.Errorf("flashcards bootstrap deck config %s: %w", d.DeckID, err)
		}
	}

	for i, spec := range WelcomeNotes() {
		noteID := seedNoteID(userID, i)
		if _, err := tx.Exec(ctx, `
			INSERT INTO flashcard_notes
				(id, user_id, deck_id, front, back, tags, usn, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7)
			ON CONFLICT (id) DO NOTHING`,
			noteID, userID, SeedDeckWelcomeID, spec.Front, spec.Back,
			TagsToJSON(spec.Tags), now); err != nil {
			return false, fmt.Errorf("flashcards bootstrap note %d: %w", i, err)
		}
		// Same initial shape as the POST /api/flashcards create path: one
		// card in state=0 (new) with due = now (unix seconds).
		if _, err := tx.Exec(ctx, `
			INSERT INTO flashcard_cards
				(id, note_id, user_id, deck_id, state, due, interval_days, stability, difficulty,
				 reps, lapses, usn, created_at, updated_at)
			VALUES ($1,$2,$3,$4,0,$5,0,0,0,0,0,1,$5,$5)
			ON CONFLICT (id) DO NOTHING`,
			seedCardID(userID, i), noteID, userID, SeedDeckWelcomeID, now); err != nil {
			return false, fmt.Errorf("flashcards bootstrap card %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("flashcards bootstrap commit: %w", err)
	}
	return true, nil
}
