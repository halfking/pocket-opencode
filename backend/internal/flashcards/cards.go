package flashcards

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrCardNotFound is returned when a review attempt targets a card that is
// missing or soft-deleted. Handlers translate this to 404.
var ErrCardNotFound = errors.New("flashcards: card not found")

// RecordReview writes a RevLog row and bumps the card's reps/lapses/
// last_review_at/usn. It does NOT recompute FSRS — per the v1 contract §4
// the only scheduling source of truth is ts-fsrs on the client. The caller
// is expected to follow up with a PATCH /api/flashcards/cards/:id carrying
// the freshly-computed due/state/stability/difficulty/interval_days.
//
// We still increment reps and (on Again) lapses server-side because those
// are bookkeeping counters, not schedule math, and the client's count must
// agree with the DB to keep usn monotonic.
func (s *Store) RecordReview(ctx context.Context, userID, cardID string, rating int, reviewedAtSec int64) (*Card, *RevLog, error) {
	if rating < 1 || rating > 4 {
		return nil, nil, fmt.Errorf("invalid rating %d (must be 1..4)", rating)
	}
	if reviewedAtSec <= 0 {
		reviewedAtSec = s.Now()
	}

	card, err := s.GetCard(ctx, userID, cardID)
	if err != nil {
		return nil, nil, err
	}
	if card == nil {
		return nil, nil, ErrCardNotFound
	}

	prevState := card.State
	prevInterval := card.IntervalDays
	now := s.Now()

	// Bump counters.
	card.Reps++
	if rating == 1 {
		card.Lapses++
	}
	card.LastReviewAt = reviewedAtSec
	card.UpdatedAt = now
	card.Usn++

	// Persist the bookkeeping update. We deliberately leave Due/State/
	// Stability/Difficulty/IntervalDays untouched here — those are the
	// scheduling fields ts-fsrs will overwrite on the next PATCH.
	if _, err := s.pool.Exec(ctx, `
		UPDATE flashcard_cards SET reps=$3, lapses=$4, last_review_at=$5,
		                           usn=$6, updated_at=$7
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		card.ID, userID, card.Reps, card.Lapses, card.LastReviewAt, card.Usn, card.UpdatedAt,
	); err != nil {
		return nil, nil, fmt.Errorf("record review update: %w", err)
	}

	log := &RevLog{
		ID:           newRevLogID(),
		CardID:       card.ID,
		UserID:       userID,
		ReviewedAt:   reviewedAtSec,
		Rating:       rating,
		PrevState:    prevState,
		NextState:    card.State, // unchanged on this call; PATCH will adjust
		PrevInterval: prevInterval,
		NextInterval: card.IntervalDays,
		ElapsedDays:  elapsedDays(card.LastReviewAt, reviewedAtSec),
	}
	if err := s.InsertRevLog(ctx, log); err != nil {
		return nil, nil, err
	}
	return card, log, nil
}

// newRevLogID returns a 24-char hex id. We don't need strict ULID ordering
// for review logs (they sort by reviewed_at anyway), so a fast random id
// is enough.
func newRevLogID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("rev-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func elapsedDays(prevReviewAt, nowSec int64) int {
	if prevReviewAt <= 0 || nowSec <= prevReviewAt {
		return 0
	}
	diff := nowSec - prevReviewAt
	const secsPerDay = int64(86400)
	return int(diff / secsPerDay)
}
