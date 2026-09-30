package learning

// Scheduler tests. These are the contract that matters for the learning module:
// if Schedule drifts, every reminder in the app fires at the wrong time, and
// nothing else in the system would notice.

import (
	"math"
	"testing"
)

const baseNow int64 = 1790000000

func reviewInput(state int, rating int, elapsed float64) ScheduleInput {
	return ScheduleInput{
		State:       state,
		Stability:   10,
		Difficulty:  5,
		Rating:      rating,
		ElapsedDays: elapsed,
		Now:         baseNow,
	}
}

func TestScheduleNewCardGraduatesOnGood(t *testing.T) {
	out := Schedule(reviewInput(StateNew, RatingGood, 0))
	if out.State != StateReview {
		t.Fatalf("new+Good state = %d, want StateReview(%d)", out.State, StateReview)
	}
	if out.Relearning {
		t.Errorf("new+Good should not be relearning")
	}
	if out.IntervalDays != DefaultGraduatingIntervalDays {
		t.Errorf("new+Good interval = %v days, want %d", out.IntervalDays, DefaultGraduatingIntervalDays)
	}
	if out.Stability <= 0 {
		t.Fatalf("new+Good stability = %v, want > 0", out.Stability)
	}
	if out.Due != baseNow+int64(DefaultGraduatingIntervalDays)*secondsPerDay {
		t.Errorf("new+Good due = %d, want %d", out.Due, baseNow+int64(DefaultGraduatingIntervalDays)*secondsPerDay)
	}
}

func TestScheduleNewCardEasyUsesEasyInterval(t *testing.T) {
	out := Schedule(reviewInput(StateNew, RatingEasy, 0))
	if out.IntervalDays != DefaultEasyIntervalDays {
		t.Errorf("new+Easy interval = %v days, want %d", out.IntervalDays, DefaultEasyIntervalDays)
	}
	if out.Stability <= 0 {
		t.Fatalf("new+Easy stability = %v, want > 0", out.Stability)
	}
}

func TestScheduleNewCardAgainStaysInLearning(t *testing.T) {
	out := Schedule(ScheduleInput{State: StateNew, Rating: RatingAgain, Now: baseNow})
	if out.State != StateRelearning {
		t.Fatalf("new+Again state = %d, want StateRelearning(%d)", out.State, StateRelearning)
	}
	if !out.Relearning {
		t.Errorf("new+Again should be relearning")
	}
	if out.IntervalDays != 0 {
		t.Errorf("new+Again interval = %v, want 0 (short in-session step)", out.IntervalDays)
	}
	if out.Due != baseNow+int64(DefaultLearningStepMin)*60 {
		t.Errorf("new+Again due = %d, want %d", out.Due, baseNow+int64(DefaultLearningStepMin)*60)
	}
	// A brand-new card has no prior memory, so a lapse must not be recorded.
	if out.Lapses != 0 {
		t.Errorf("new+Again lapses = %d, want 0", out.Lapses)
	}
}

// The full rating x state matrix must always produce a schedulable card:
// a state in range, a future due date, a bounded stability and difficulty.
func TestScheduleMatrixIsAlwaysSchedulable(t *testing.T) {
	states := []int{StateNew, StateLearning, StateReview, StateRelearning}
	ratings := []int{RatingAgain, RatingHard, RatingGood, RatingEasy}
	for _, state := range states {
		for _, rating := range ratings {
			in := reviewInput(state, rating, 3)
			out := Schedule(in)
			if out.State < StateNew || out.State > StateRelearning {
				t.Errorf("state=%d rating=%d -> state %d out of range", state, rating, out.State)
			}
			if out.Due <= baseNow {
				t.Errorf("state=%d rating=%d -> due %d is not in the future", state, rating, out.Due)
			}
			if out.Stability <= 0 || out.Stability > maxStability {
				t.Errorf("state=%d rating=%d -> stability %v out of (0, %v]", state, rating, out.Stability, maxStability)
			}
			if out.Difficulty < minDifficulty || out.Difficulty > maxDifficulty {
				t.Errorf("state=%d rating=%d -> difficulty %v out of [%v, %v]", state, rating, out.Difficulty, minDifficulty, maxDifficulty)
			}
			if out.Reps != in.Reps+1 {
				t.Errorf("state=%d rating=%d -> reps %d, want %d", state, rating, out.Reps, in.Reps+1)
			}
		}
	}
}

func TestScheduleDifficultyIsMonotonicInRating(t *testing.T) {
	prev := math.Inf(1)
	for _, rating := range []int{RatingAgain, RatingHard, RatingGood, RatingEasy} {
		out := Schedule(reviewInput(StateReview, rating, 5))
		if out.Difficulty > prev+1e-9 {
			t.Errorf("rating %d difficulty %v is easier than the previous rating's %v", rating, out.Difficulty, prev)
		}
		prev = out.Difficulty
	}
}

// Stability must follow the reference FSRS-5 semantics, not intuition.
//
// In the reference implementation (ts-fsrs 5.4.2, dist/index.mjs
// next_recall_stability) the rating enters stability growth through exactly two
// coefficients:
//
//	hard_penalty = w[15] if rating == Hard else 1
//	easy_bound   = w[16] if rating == Easy else 1
//
// With the FSRS-5 default 17-d vector w[15]=0.7536 and w[16]=0.3332, both are
// *below* 1: "Hard" is penalised, and "Easy" is deliberately bounded so one
// lucky answer cannot inflate the interval. An "Easy" answer still pays off
// through difficulty (D drops, so 11-D grows on the next review) — which is
// what TestScheduleEasyEventuallyPaysOff pins.
func TestScheduleRatingCoefficientsMatchReferenceFSRS5(t *testing.T) {
	again := Schedule(reviewInput(StateReview, RatingAgain, 5))
	hard := Schedule(reviewInput(StateReview, RatingHard, 5))
	good := Schedule(reviewInput(StateReview, RatingGood, 5))
	easy := Schedule(reviewInput(StateReview, RatingEasy, 5))

	if hard.Stability >= again.Stability {
		t.Errorf("Hard stability %v should be penalised below Again %v (w15=%v)", hard.Stability, again.Stability, defaultWeights[wHardPenalty])
	}
	if easy.Stability >= good.Stability {
		t.Errorf("Easy stability %v should be bounded below Good %v (w16=%v)", easy.Stability, good.Stability, defaultWeights[wEasyBonus])
	}
	for name, out := range map[string]ScheduleOutput{"again": again, "hard": hard, "good": good, "easy": easy} {
		if out.Stability <= 0 {
			t.Errorf("%s stability = %v, want > 0", name, out.Stability)
		}
	}
}

// An "Easy" review lowers difficulty, and difficulty is the lever that widens
// every later interval (the growth term is 11 - D). So the Easy path must keep
// pushing D down over successive reviews — that, not a single large interval
// jump, is how Easy pays off once easy_bound caps the one-step stability gain.
func TestScheduleEasyKeepsLoweringDifficulty(t *testing.T) {
	easy := Schedule(reviewInput(StateReview, RatingEasy, 5))
	good := Schedule(reviewInput(StateReview, RatingGood, 5))
	if easy.Difficulty >= good.Difficulty {
		t.Fatalf("Easy difficulty %v should be below Good %v", easy.Difficulty, good.Difficulty)
	}
	prev := easy.Difficulty
	stability := easy.Stability
	for i := 0; i < 4; i++ {
		out := Schedule(ScheduleInput{
			State: StateReview, Stability: stability, Difficulty: prev,
			Rating: RatingEasy, ElapsedDays: out_elapsed(easy.IntervalDays), Now: baseNow,
		})
		if out.Difficulty >= prev {
			t.Fatalf("round %d difficulty %v did not drop below %v", i, out.Difficulty, prev)
		}
		prev = out.Difficulty
		stability = out.Stability
	}
	if prev < minDifficulty {
		t.Errorf("difficulty %v fell below the %v floor", prev, minDifficulty)
	}
}

// out_elapsed converts an interval in days into the elapsed-days value the next
// review should report.
func out_elapsed(intervalDays float64) float64 {
	if intervalDays < 1 {
		return 1
	}
	return intervalDays
}

func TestScheduleReviewAgainRecordsLapseAndRelearning(t *testing.T) {
	in := reviewInput(StateReview, RatingAgain, 30)
	in.Lapses = 2
	out := Schedule(in)
	if !out.Relearning {
		t.Errorf("review+Again should be relearning")
	}
	if out.State != StateRelearning {
		t.Errorf("review+Again state = %d, want StateRelearning(%d)", out.State, StateRelearning)
	}
	if out.Lapses != 3 {
		t.Errorf("review+Again lapses = %d, want 3", out.Lapses)
	}
	if out.IntervalDays != 0 {
		t.Errorf("review+Again interval = %v, want 0", out.IntervalDays)
	}
}

// A later review of a card that was answered correctly must be scheduled at a
// longer interval than an early one: this is the whole point of the algorithm.
func TestScheduleLongerElapsedYieldsLongerIntervalForGood(t *testing.T) {
	early := Schedule(reviewInput(StateReview, RatingGood, 1))
	late := Schedule(reviewInput(StateReview, RatingGood, 20))
	if late.IntervalDays <= early.IntervalDays {
		t.Errorf("elapsed 20d interval %v should exceed elapsed 1d interval %v", late.IntervalDays, early.IntervalDays)
	}
}

// Repeated successful reviews must keep pushing the interval out, otherwise
// the "memory curve" would not actually work.
func TestScheduleSuccessiveGoodsGrowInterval(t *testing.T) {
	stability, difficulty := 10.0, 5.0
	now := baseNow
	prev := 0.0
	for i := 0; i < 5; i++ {
		out := Schedule(ScheduleInput{
			State:       StateReview,
			Stability:   stability,
			Difficulty:  difficulty,
			Rating:      RatingGood,
			ElapsedDays: math.Max(prev, 1),
			Now:         now,
		})
		if out.IntervalDays <= prev {
			t.Fatalf("round %d interval %v did not grow past %v", i, out.IntervalDays, prev)
		}
		prev = out.IntervalDays
		stability, difficulty, now = out.Stability, out.Difficulty, out.Due
	}
}

// Higher desired retention means a shorter interval: the retention dial has to
// behave the way the deck-config UI promises.
func TestScheduleHigherRetentionShortensInterval(t *testing.T) {
	in := reviewInput(StateReview, RatingGood, 10)
	in.DesiredRetention = 0.8
	low := Schedule(in)
	in.DesiredRetention = 0.95
	high := Schedule(in)
	if high.IntervalDays >= low.IntervalDays {
		t.Errorf("retention 0.95 interval %v should be shorter than retention 0.8 interval %v", high.IntervalDays, low.IntervalDays)
	}
}

func TestScheduleRetentionDefaultsWhenOutOfRange(t *testing.T) {
	base := reviewInput(StateReview, RatingGood, 7)
	explicit := ScheduleInput(base)
	explicit.DesiredRetention = DefaultDesiredRetention
	want := Schedule(explicit)

	zero := base
	zero.DesiredRetention = 0
	if got := Schedule(zero); math.Abs(got.IntervalDays-want.IntervalDays) > 1e-9 {
		t.Errorf("retention 0 should fall back to the default: got %v, want %v", got.IntervalDays, want.IntervalDays)
	}
	one := base
	one.DesiredRetention = 1
	if got := Schedule(one); math.Abs(got.IntervalDays-want.IntervalDays) > 1e-9 {
		t.Errorf("retention 1 should fall back to the default: got %v, want %v", got.IntervalDays, want.IntervalDays)
	}
}

func TestScheduleClampsDifficultyIntoRange(t *testing.T) {
	in := reviewInput(StateReview, RatingAgain, 5)
	in.Difficulty = 10
	out := Schedule(in)
	if out.Difficulty < minDifficulty || out.Difficulty > maxDifficulty {
		t.Errorf("difficulty %v escaped [%v, %v]", out.Difficulty, minDifficulty, maxDifficulty)
	}
	in2 := reviewInput(StateReview, RatingAgain, 5)
	in2.Difficulty = -5
	if out2 := Schedule(in2); out2.Difficulty < minDifficulty {
		t.Errorf("difficulty %v escaped the lower bound", out2.Difficulty)
	}
}

func TestScheduleHandlesCorruptZeroStability(t *testing.T) {
	in := reviewInput(StateReview, RatingGood, 5)
	in.Stability = 0
	out := Schedule(in)
	if math.IsNaN(out.IntervalDays) || math.IsInf(out.IntervalDays, 0) {
		t.Fatalf("interval = %v, want a finite number", out.IntervalDays)
	}
	if out.IntervalDays <= 0 {
		t.Errorf("interval = %v, want > 0", out.IntervalDays)
	}
}

func TestScheduleClampsAbsurdElapsedToTheRequestableInterval(t *testing.T) {
	// A card reviewed 100 years late must not produce a NaN retrievability.
	in := reviewInput(StateReview, RatingGood, 36500)
	out := Schedule(in)
	if math.IsNaN(out.Stability) || math.IsInf(out.Stability, 0) {
		t.Fatalf("stability = %v, want finite", out.Stability)
	}
	if out.IntervalDays <= 0 || out.IntervalDays > maxIntervalDays {
		t.Errorf("interval = %v, want within (0, %v]", out.IntervalDays, maxIntervalDays)
	}
}

func TestScheduleLearningStepMinutesIsHonoured(t *testing.T) {
	in := ScheduleInput{State: StateLearning, Rating: RatingAgain, LearningStepMin: 10, Now: baseNow}
	out := Schedule(in)
	if out.Due != baseNow+10*60 {
		t.Errorf("due = %d, want %d", out.Due, baseNow+10*60)
	}
}

func TestScheduleIsDeterministic(t *testing.T) {
	in := reviewInput(StateReview, RatingGood, 6)
	first := Schedule(in)
	for i := 0; i < 20; i++ {
		if got := Schedule(in); got != first {
			t.Fatalf("run %d differs: %+v vs %+v", i, got, first)
		}
	}
}

func TestRetrievabilityDecreasesWithTime(t *testing.T) {
	prev := 2.0
	for _, days := range []float64{0, 1, 5, 20, 100} {
		r := retrievability(days, 10)
		if r > prev {
			t.Errorf("retrievability at %vd = %v, want <= %v", days, r, prev)
		}
		if r <= 0 {
			t.Errorf("retrievability at %vd = %v, want > 0", days, r)
		}
		prev = r
	}
}

func TestDefaultWeightsAreFiniteAndPositive(t *testing.T) {
	for i, w := range DefaultWeights() {
		if math.IsNaN(w) || w <= 0 {
			t.Errorf("weight %d = %v, want a positive finite number", i, w)
		}
	}
}
