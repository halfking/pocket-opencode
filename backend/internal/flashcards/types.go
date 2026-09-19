// Package flashcards implements the v1 spaced-repetition flashcard module
// for OpenPocket. It mirrors the storage shape from the Anki rslib scheduler
// but runs entirely on PostgreSQL with usn-based last-writer-wins sync
// semantics. Per the frozen contract (docs/flashcards-contract.md §1):
//
//   - v1 is the only source of truth for persistence; FSRS scheduling runs
//     client-side via ts-fsrs.
//   - JWT user_id is authoritative; request-body userId/userID is always
//     stripped (server/flashcards_handler.go).
//   - All four tables use snake_case JSON tags matching §3 of the contract.
package flashcards

import (
	"encoding/json"
)

// Note is the content-level flashcard record. Mirrors flashcard_notes in
// the contract (§1.1). Tags is serialised as a JSON array string because the
// table column is TEXT (default "[]") — clients receive the same shape
// back, so the lossless round-trip happens at the API boundary.
type Note struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	DeckID    string `json:"deckId"`
	Front     string `json:"front"`
	Back      string `json:"back"`
	Tags      string `json:"tags"` // JSON array (string)
	Usn       int64  `json:"usn"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	DeletedAt int64  `json:"deletedAt,omitempty"`
}

// Card is the schedule-level flashcard record. Mirrors flashcard_cards in
// the contract (§1.2). Due is dual-semantic per §1.2 (unix seconds for
// new/learning; epoch-days for graduated review cards). v1 does not
// recompute FSRS here — see cards.go RecordReview.
type Card struct {
	ID           string  `json:"id"`
	NoteID       string  `json:"noteId"`
	UserID       string  `json:"userId"`
	DeckID       string  `json:"deckId"`
	State        int     `json:"state"` // 0=new 1=learning 2=review 3=relearning
	Due          int64   `json:"due"`   // sec | days-from-epoch
	IntervalDays float32 `json:"intervalDays"`
	Stability    float32 `json:"stability"`
	Difficulty   float32 `json:"difficulty"`
	Reps         int     `json:"reps"`
	Lapses       int     `json:"lapses"`
	LastReviewAt int64   `json:"lastReviewAt"`
	Usn          int64   `json:"usn"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
	DeletedAt    int64   `json:"deletedAt,omitempty"`
}

// DeckConfig holds per-deck FSRS tuning + review limits. Mirrors
// flashcard_deck_config (§1.4). FSRS weights default to the 17-d FSRS-5
// vector; v1 does not surface per-user re-tuning, but the column is real
// so v2 can persist it.
type DeckConfig struct {
	DeckID                 string    `json:"deckId"`
	UserID                 string    `json:"userId"`
	Name                   string    `json:"name"`
	NewPerDay              int       `json:"newPerDay"`
	ReviewsPerDay          int       `json:"reviewsPerDay"`
	LearningStepsMin       []int     `json:"learningStepsMin"`
	GraduatingIntervalDays int       `json:"graduatingIntervalDays"`
	EasyIntervalDays       int       `json:"easyIntervalDays"`
	FSRSWeights            []float32 `json:"fsrsWeights"`
	DesiredRetention       float32   `json:"desiredRetention"`
	Usn                    int64     `json:"usn"`
	CreatedAt              int64     `json:"createdAt"`
	UpdatedAt              int64     `json:"updatedAt"`
	DeletedAt              int64     `json:"deletedAt,omitempty"`
}

// RevLog is one review attempt. Mirrors flashcard_revlog (§1.3). The 90-day
// GC job is intentionally out of v1 scope; rows accumulate.
type RevLog struct {
	ID           string  `json:"id"`
	CardID       string  `json:"cardId"`
	UserID       string  `json:"userId"`
	ReviewedAt   int64   `json:"reviewedAt"`
	Rating       int     `json:"rating"` // 1=Again 2=Hard 3=Good 4=Easy
	PrevState    int     `json:"prevState"`
	NextState    int     `json:"nextState"`
	PrevInterval float32 `json:"prevInterval"`
	NextInterval float32 `json:"nextInterval"`
	ElapsedDays  int     `json:"elapsedDays"`
}

// TagsToJSON encodes a string slice into the canonical ["a","b"] form the
// flashcard_notes.tags column expects. Returns "[]" for nil.
func TagsToJSON(tags []string) string {
	if tags == nil {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}
