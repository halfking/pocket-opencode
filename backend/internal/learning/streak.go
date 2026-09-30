package learning

// Study streaks (docs/学习muse/05-实施路线图.md §5, P4).
//
// A streak is a derived read model: it is computed from the days on which the
// user did something, never stored as a counter. A stored counter has to be
// repaired after a crash, a timezone change, or a missed tick; recomputing from
// activity dates is self-healing by construction.
//
// Everything here is pure. The one genuinely tricky decision — what counts as
// "today" for the purpose of not breaking a streak — is exactly the kind of
// rule that must be unit-tested rather than discovered in production.

import "sort"

// SecondsPerDay is the day length used to turn a timestamp into a day index.
const SecondsPerDay = 86400

// DayIndex converts a unix timestamp to a day number in the given timezone.
// tzOffsetSec is seconds east of UTC (e.g. +8h = 28800) and is supplied by the
// caller rather than read from a global, so a test and the server agree by
// construction.
//
// The learning tables store unix seconds and no timezone column, so the offset
// has to come from outside. This is the same approximation the quiet-hours
// helper makes, and it carries the same known limitation: a user who travels
// across a date line sees their day boundaries shift.
func DayIndex(ts, tzOffsetSec int64) int64 {
	return (ts + tzOffsetSec) / SecondsPerDay
}

// Streak is the derived study-streak state.
type Streak struct {
	// Current is the run of consecutive active days ending today or yesterday.
	Current int `json:"current"`
	// Longest is the longest run ever seen in the loaded window.
	Longest int `json:"longest"`
	// LastActiveDay is the most recent active day, 0 when there is none.
	LastActiveDay int64 `json:"lastActiveDay"`
	// ActiveToday tells the UI whether to say "keep it going" or "start one".
	ActiveToday bool `json:"activeToday"`
}

// ComputeStreak derives the streak from the days the user was active.
//
// activeDays need not be sorted or deduplicated; the function normalises both,
// because a caller feeding it raw SQL output should not have to pre-sort and a
// duplicate day must not inflate a streak.
//
// The rule that matters: **a streak survives until the day after the last active
// day.** If the user studied yesterday and has not opened the app yet today, the
// streak is still alive. Breaking it at midnight — the obvious implementation —
// means every morning the app shows 0 to someone who studied last night, which
// is precisely when a streak is most motivating.
func ComputeStreak(activeDays []int64, today int64) Streak {
	days := normalizeDays(activeDays)
	// Timestamps after `today` are clock skew (a client with a wrong clock, a
	// row written by a node running ahead). Counting them would let a future
	// day satisfy "active today" and inflate the run, so they are dropped.
	days = dropAfter(days, today)
	var s Streak
	if len(days) == 0 {
		return s
	}
	s.LastActiveDay = days[len(days)-1]
	s.ActiveToday = s.LastActiveDay == today

	// Longest: one pass over the ascending, deduplicated list.
	run := 1
	for i := 1; i < len(days); i++ {
		if days[i] == days[i-1]+1 {
			run++
			continue
		}
		if run > s.Longest {
			s.Longest = run
		}
		run = 1
	}
	if run > s.Longest {
		s.Longest = run
	}

	// Anchor: today when the user was active today, otherwise yesterday. The
	// streak survives until the day after the last active day, so someone who
	// studied last night and has not opened the app yet this morning still sees
	// it intact — which is the moment a streak is most worth protecting.
	anchor := today
	if !s.ActiveToday {
		anchor = today - 1
	}
	if s.LastActiveDay > anchor {
		// The only activity is dated after the anchor — clock skew, or a caller
		// passing a `today` that predates its own data. Report 0 rather than a
		// run the input does not support.
		return s
	}

	// Current: walk **down from the anchor**, not up from the last active day.
	// Walking up is subtly wrong: with active days {95,96,97} and today=100 the
	// anchor is 99, and counting up from 97 would tally the old run of 3 and
	// never reach the two missed days. Starting at the anchor means the first
	// missing day ends the count, which is the whole definition.
	//
	// The lower bound is the earliest active day, not the last one: the walk
	// has to descend *below* the last active day to count a multi-day run.
	earliest := days[0]
	current := 0
	for d := anchor; d >= earliest; d-- {
		if !contains(days, d) {
			break
		}
		current++
	}
	s.Current = current
	return s
}

// dropAfter returns the prefix of the ascending, deduplicated slice that is
// <= today.
func dropAfter(days []int64, today int64) []int64 {
	i := sort.Search(len(days), func(i int) bool { return days[i] > today })
	return days[:i]
}

// normalizeDays returns the input as a sorted, deduplicated slice.
func normalizeDays(in []int64) []int64 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int64, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	// In-place dedupe keeps the result stable after the sort.
	j := 0
	for i := 1; i < len(out); i++ {
		if out[i] != out[j] {
			j++
			out[j] = out[i]
		}
	}
	return out[:j+1]
}

func contains(days []int64, d int64) bool {
	i := sort.Search(len(days), func(i int) bool { return days[i] >= d })
	return i < len(days) && days[i] == d
}

// MergeActivityDays turns raw activity timestamps into a streak: it maps each
// timestamp to a day in the caller's timezone, then runs ComputeStreak.
//
// Captures and reviews are merged rather than tracked separately on purpose.
// A user who reviews a card on Monday and captures an article on Tuesday is on
// a two-day streak, and reporting "1 day of reviews, 1 day of captures" would
// be both harder to read and wrong.
func MergeActivityDays(captureStamps, reviewStamps []int64, tzOffsetSec, today int64) Streak {
	days := make([]int64, 0, len(captureStamps)+len(reviewStamps))
	for _, ts := range captureStamps {
		days = append(days, DayIndex(ts, tzOffsetSec))
	}
	for _, ts := range reviewStamps {
		days = append(days, DayIndex(ts, tzOffsetSec))
	}
	return ComputeStreak(days, today)
}

// MilestoneDays are the streak lengths worth celebrating. They are spaced the
// way habit formation actually works: dense early (the first week is where a
// habit forms or dies), sparse later.
var MilestoneDays = []int{3, 7, 14, 30, 60, 100, 180, 365}

// Milestone returns the highest milestone a streak of length current has
// reached, and whether there is one. current <= 0 yields none: congratulating
// someone on a streak they do not have is worse than saying nothing.
func Milestone(current int) (int, bool) {
	if current <= 0 {
		return 0, false
	}
	best := 0
	for _, d := range MilestoneDays {
		if current >= d {
			best = d
		}
	}
	return best, best > 0
}

// NextMilestone returns the next milestone to aim for, so the UI can say
// "2 more days to 7" instead of only congratulating.
func NextMilestone(current int) (int, bool) {
	for _, d := range MilestoneDays {
		if current < d {
			return d, true
		}
	}
	return 0, false
}

// MilestoneKey is the id used to make milestone announcements idempotent. It
// lands in learning_reminders.item_id, whose unique index is
// (workspace_id, user_id, kind, item_id) — so one row per user per milestone,
// enforced by the database rather than by a check in the executor.
func MilestoneKey(days int) string {
	return "milestone:" + itoa(days)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
