package learning

// Server-side spaced-repetition scheduling (docs/学习muse/03-架构方案.md §3,
// ADR-002).
//
// This is FSRS-5 shaped, written as a pure function so it can be unit tested
// without Postgres, a clock, or a device. The field names mirror
// flashcards.Card (stability / difficulty / interval) and the rating scale
// mirrors flashcard_revlog.rating, so a future switch of the write path to the
// server does not need a data migration.
//
// Deliberately NOT in scope: per-user weight optimisation (the weights column
// already exists on flashcard_deck_config for that) and the client-side
// ts-fsrs preview. Those are Phase 3/4 per ADR-002.

import "math"

// FSRS-5 default weight vector (17 parameters). These are the same values the
// flashcards DDL seeds into flashcard_deck_config.fsrs_weights
// (backend/internal/flashcards/store.go), so a deck created before this
// package existed schedules with the same constants.
var defaultWeights = [17]float64{
	0.4872, 1.4003, 3.7145, 13.8206, 7.2203, 0.5016, 1.0695,
	0.1127, 1.0178, 1.8490, 0.1133, 0.3127, 2.2931, 0.2191,
	3.0004, 0.7536, 0.3332,
}

// Weight indexes inside the FSRS vector, named so the formulas below read like
// the reference implementation instead of a wall of w[i].
const (
	wInitStabGood = 0 // S0 for a first "Good"
	wInitStabEasy = 3 // S0 for a first "Easy"
	wInitDiff     = 4 // base difficulty
	wDiffLinear   = 5 // difficulty slope
	wDiffDamp     = 6 // difficulty damping
	wNextDiffW    = 7 // next-difficulty weight
	wNextStabHard = 8 // stability growth base
	wNextStabPow  = 9
	wForgetCurve  = 10
	wHardPenalty  = 15
	wEasyBonus    = 16
)

// Power-law forgetting curve constants (FSRS-4/5).
const (
	decay       = -0.5
	factorRatio = 19.0 / 81.0
)

// DefaultDesiredRetention is the target probability of recall. It matches the
// flashcard_deck_config default so both schedulers agree.
const DefaultDesiredRetention = 0.9

// ScheduleInput is one review attempt's state.
type ScheduleInput struct {
	// State is the card state before the review: 0=new 1=learning
	// 2=review 3=relearning (same encoding as flashcards.Card).
	State int `json:"state"`
	// Stability / Difficulty are the FSRS S/D of the card before the review.
	Stability  float64 `json:"stability"`
	Difficulty float64 `json:"difficulty"`
	// Rating is 1=Again 2=Hard 3=Good 4=Easy.
	Rating int `json:"rating"`
	// ElapsedDays is the time since the previous review, in days.
	ElapsedDays float64 `json:"elapsedDays"`
	// LearningStepMin is the in-session delay for a failed learning card.
	// 0 falls back to DefaultLearningStepMin.
	LearningStepMin int `json:"learningStepMin,omitempty"`
	// DesiredRetention is the target recall probability; 0 falls back to
	// DefaultDesiredRetention.
	DesiredRetention float64 `json:"desiredRetention,omitempty"`
	// Now is the review timestamp in unix seconds. 0 falls back to "now".
	Now int64 `json:"now,omitempty"`
	// LearningSteps / GraduatingIntervalDays / EasyIntervalDays mirror the
	// per-deck configuration; 0 falls back to the defaults below.
	LearningSteps          []int `json:"learningSteps,omitempty"`
	GraduatingIntervalDays int   `json:"graduatingIntervalDays,omitempty"`
	EasyIntervalDays       int   `json:"easyIntervalDays,omitempty"`
	// Reps / Lapses are the card's counters before the review.
	Reps   int `json:"reps"`
	Lapses int `json:"lapses"`
}

// ScheduleOutput is the card state after the review.
type ScheduleOutput struct {
	State        int     `json:"state"`
	Stability    float64 `json:"stability"`
	Difficulty   float64 `json:"difficulty"`
	IntervalDays float64 `json:"intervalDays"`
	// Due is a unix timestamp. It is deliberately absolute seconds (not
	// epoch-days like graduated flashcard cards) so learning summaries and
	// reminder comparisons never need to know which convention a row uses.
	Due int64 `json:"due"`
	// Relearning is true when the card went (or stayed) in the relearning
	// queue, i.e. the UI should show a short-interval re-study step.
	Relearning bool `json:"relearning"`
	Reps       int  `json:"reps"`
	Lapses     int  `json:"lapses"`
}

// Defaults applied when the caller does not override them.
const (
	DefaultLearningStepMin              = 1
	DefaultGraduatingIntervalDays       = 1
	DefaultEasyIntervalDays             = 4
	minIntervalDays                     = 1.0
	maxIntervalDays                     = 36500.0
	maxStability                        = 36500.0
	minDifficulty                       = 1.0
	maxDifficulty                       = 10.0
	secondsPerDay                 int64 = 86400
)

// Schedule computes the next scheduling state of a card from one review.
// Pure: no clock, no IO, no randomness.
func Schedule(in ScheduleInput) ScheduleOutput {
	now := in.Now
	if now == 0 {
		now = nowUnix()
	}
	retention := in.DesiredRetention
	if retention <= 0 || retention >= 1 {
		retention = DefaultDesiredRetention
	}
	stepMin := in.LearningStepMin
	if stepMin <= 0 {
		stepMin = DefaultLearningStepMin
	}
	graduating := in.GraduatingIntervalDays
	if graduating <= 0 {
		graduating = DefaultGraduatingIntervalDays
	}
	easyDays := in.EasyIntervalDays
	if easyDays <= 0 {
		easyDays = DefaultEasyIntervalDays
	}
	w := defaultWeights

	out := ScheduleOutput{
		State:  in.State,
		Reps:   in.Reps + 1,
		Lapses: in.Lapses,
	}

	// --- Learning / relearning / new -------------------------------------
	// A card that has never graduated (or that failed) stays on short
	// in-session steps; the FSRS stability is only seeded on the first
	// successful graduation so a lapse does not inflate the long-term memory.
	if in.State == StateNew || in.State == StateLearning || in.State == StateRelearning {
		stability := in.Stability
		difficulty := in.Difficulty
		if difficulty <= 0 {
			difficulty = initialDifficulty(w, in.Rating)
		}

		if in.Rating <= RatingHard {
			// Failed: back to a short learning step. Stability collapses to
			// the initial value rather than keeping the old, larger one —
			// this is what makes a lapse actually cost time later.
			stability = math.Min(stability, defaultWeights[wInitStabGood]*0.5)
			if stability <= 0 {
				stability = defaultWeights[wInitStabGood] * 0.5
			}
			out.State = StateRelearning
			out.Relearning = true
			if in.Rating == RatingAgain && in.State != StateNew {
				out.Lapses = in.Lapses + 1
			}
			out.Stability = stability
			out.Difficulty = clamp(difficulty, minDifficulty, maxDifficulty)
			out.IntervalDays = 0
			out.Due = now + int64(stepMin)*60
			return out
		}

		// Graduating: seed the memory model.
		if in.Rating == RatingEasy {
			stability = defaultWeights[wInitStabEasy]
		} else {
			stability = defaultWeights[wInitStabGood]
		}
		if stability <= 0 {
			stability = 1
		}
		out.State = StateReview
		out.Stability = stability
		out.Difficulty = clamp(difficulty, minDifficulty, maxDifficulty)
		if in.Rating == RatingEasy {
			out.IntervalDays = float64(easyDays)
		} else {
			out.IntervalDays = float64(graduating)
		}
		out.Due = now + int64(out.IntervalDays*float64(secondsPerDay))
		return out
	}

	// --- Review ----------------------------------------------------------
	stability := in.Stability
	if stability <= 0 {
		// A review-state card with no stability is corrupt input; treat it as
		// a first study rather than dividing by zero below.
		stability = defaultWeights[wInitStabGood]
	}
	difficulty := in.Difficulty
	if difficulty <= 0 {
		difficulty = initialDifficulty(w, RatingGood)
	}
	elapsed := in.ElapsedDays
	if elapsed < 0 {
		elapsed = 0
	}
	// An interval longer than the requested retention is the caller's bug,
	// not the user's; clamp so R stays in (0,1).
	maxInterval := intervalFor(stability, retention, w)
	if maxInterval > 0 && elapsed > maxInterval {
		elapsed = maxInterval
	}
	retrievability := retrievability(elapsed, stability)

	nextStability := nextStability(stability, difficulty, retrievability, in.Rating, w)
	nextDifficulty := nextDifficulty(difficulty, in.Rating, w)
	interval := intervalFor(nextStability, retention, w)

	out.Stability = clamp(nextStability, 0.1, maxStability)
	out.Difficulty = clamp(nextDifficulty, minDifficulty, maxDifficulty)
	out.IntervalDays = clamp(interval, minIntervalDays, maxIntervalDays)
	out.State = StateReview

	if in.Rating == RatingAgain {
		// A failed review returns to relearning with a short step; the
		// long-term stability keeps the (reduced) value so the next interval
		// is still honest. IntervalDays is reset to 0 because the card is no
		// longer on a day-based schedule — leaving the clamped review interval
		// here would make the UI show a multi-day wait for a card that is
		// actually due in one minute.
		out.Relearning = true
		out.Lapses = in.Lapses + 1
		out.State = StateRelearning
		out.IntervalDays = 0
		out.Due = now + int64(stepMin)*60
		return out
	}
	out.Due = now + int64(out.IntervalDays*float64(secondsPerDay))
	return out
}

// retrievability is the FSRS power forgetting curve R(t,S).
func retrievability(elapsedDays, stability float64) float64 {
	if stability <= 0 {
		return 0
	}
	if elapsedDays <= 0 {
		return 1
	}
	return math.Pow(1+factorRatio*elapsedDays/stability, decay)
}

// nextStability implements the FSRS-5 stability update for a review.
func nextStability(s, d, r float64, rating int, w [17]float64) float64 {
	hardPenalty := 1.0
	if rating == RatingHard {
		hardPenalty = w[wHardPenalty]
	}
	easyBonus := 1.0
	if rating == RatingEasy {
		easyBonus = w[wEasyBonus]
	}
	growth := math.Exp(w[wNextStabHard]) *
		(11 - d) *
		math.Pow(s, -w[wNextStabPow]) *
		(math.Exp(w[wForgetCurve]*(1-r)) - 1) *
		hardPenalty * easyBonus
	return s * (1 + growth)
}

// nextDifficulty implements the FSRS-5 damped difficulty update.
func nextDifficulty(d float64, rating int, w [17]float64) float64 {
	delta := -w[wDiffLinear] * float64(rating-3)
	return w[wNextDiffW]*initialDifficulty(w, RatingEasy) + (1-w[wNextDiffW])*(d+delta)
}

// initialDifficulty is the FSRS D0 curve for a first review.
func initialDifficulty(w [17]float64, rating int) float64 {
	return clamp(w[wInitDiff]-math.Exp(w[wDiffLinear]*float64(rating-3))+1, minDifficulty, maxDifficulty)
}

// intervalFor converts a stability into days at the requested retention.
//
//	I = S / FACTOR * (R^(1/DECAY) - 1)
//
// With DECAY = -0.5 the exponent is -2, so R^(1/DECAY) = 1/R². Writing the
// exponent as -1/DECAY instead would compute R^2 - 1 < 0 and silently produce
// a negative (then clamped-to-1-day) interval for every card — hence this is
// pinned by TestScheduleHigherRetentionShortensInterval and
// TestScheduleSuccessiveGoodsGrowInterval.
func intervalFor(stability, retention float64, w [17]float64) float64 {
	if stability <= 0 {
		return 0
	}
	if retention <= 0 || retention >= 1 {
		retention = DefaultDesiredRetention
	}
	return stability / factorRatio * (math.Pow(retention, 1/decay) - 1)
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// DefaultWeights exposes the FSRS-5 default vector so a caller (e.g. a future
// deck-config loader) does not have to hardcode it a second time.
func DefaultWeights() []float64 {
	out := make([]float64, len(defaultWeights))
	copy(out, defaultWeights[:])
	return out
}
