package learning

// Streak rules. The "does it survive until tomorrow" case is the one worth
// protecting: it is the difference between a streak that motivates and a streak
// that reads as broken every morning.

import "testing"

// days builds a list of day indices ending at `back` days before today.
func daysEnding(today int64, back int, n int) []int64 {
	out := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, today-int64(back+i))
	}
	return out
}

func TestComputeStreakNoActivity(t *testing.T) {
	s := ComputeStreak(nil, 100)
	if s.Current != 0 || s.Longest != 0 || s.LastActiveDay != 0 || s.ActiveToday {
		t.Errorf("no activity = %+v, want a zero streak", s)
	}
}

func TestComputeStreakActiveToday(t *testing.T) {
	const today = 100
	s := ComputeStreak(daysEnding(today, 0, 4), today) // 97,98,99,100
	if s.Current != 4 {
		t.Errorf("Current = %d, want 4", s.Current)
	}
	if s.Longest != 4 {
		t.Errorf("Longest = %d, want 4", s.Longest)
	}
	if !s.ActiveToday {
		t.Error("ActiveToday = false, want true")
	}
	if s.LastActiveDay != today {
		t.Errorf("LastActiveDay = %d, want %d", s.LastActiveDay, today)
	}
}

// The rule that matters: studying yesterday and not yet today must NOT read as 0.
func TestComputeStreakSurvivesUntilTheDayAfter(t *testing.T) {
	const today = 100
	s := ComputeStreak(daysEnding(today, 1, 3), today) // 97,98,99 — nothing today
	if s.Current != 3 {
		t.Errorf("Current = %d, want 3: a streak must survive until the day after the last active day", s.Current)
	}
	if s.ActiveToday {
		t.Error("ActiveToday = true, want false")
	}
}

func TestComputeStreakBreaksAfterAMissedDay(t *testing.T) {
	const today = 100
	// Last activity was the day before yesterday: today and yesterday both
	// missed, so the run is over.
	s := ComputeStreak(daysEnding(today, 2, 3), today) // 95,96,97
	if s.Current != 0 {
		t.Errorf("Current = %d, want 0 once a full day was missed", s.Current)
	}
	if s.Longest != 3 {
		t.Errorf("Longest = %d, want 3 (the run still happened)", s.Longest)
	}
}

func TestComputeStreakLongestIsIndependentOfCurrent(t *testing.T) {
	const today = 100
	// A long run last month, a one-day run today.
	s := ComputeStreak([]int64{10, 11, 12, 13, 14, 15, 16, today}, today)
	if s.Current != 1 {
		t.Errorf("Current = %d, want 1", s.Current)
	}
	if s.Longest != 7 {
		t.Errorf("Longest = %d, want 7", s.Longest)
	}
}

func TestComputeStreakGapsBreakRuns(t *testing.T) {
	const today = 100
	// Two separate runs of 3, then a single day yesterday. The current run
	// stops at the gap, so it is 1 — not 3, and not 7.
	s := ComputeStreak([]int64{50, 51, 52, 90, 91, 92, 99}, today)
	if s.Current != 1 {
		t.Errorf("Current = %d, want 1: the walk stops at the gap before 98", s.Current)
	}
	if s.Longest != 3 {
		t.Errorf("Longest = %d, want 3", s.Longest)
	}
}

// A gap inside the current window must stop the count there.
func TestComputeStreakStopsAtGap(t *testing.T) {
	const today = 100
	// 99 (yesterday) and 98, but 97 missing.
	s := ComputeStreak([]int64{98, 99}, today)
	if s.Current != 2 {
		t.Errorf("Current = %d, want 2", s.Current)
	}
	if s.Longest != 2 {
		t.Errorf("Longest = %d, want 2", s.Longest)
	}
}

func TestComputeStreakDeduplicates(t *testing.T) {
	const today = 100
	// A duplicate day must not inflate the streak: capture and stage-change on
	// the same day are two events, not two days.
	s := ComputeStreak([]int64{98, 98, 99, 99, 99}, today)
	if s.Current != 2 {
		t.Errorf("Current = %d, want 2 despite duplicates", s.Current)
	}
}

func TestComputeStreakAcceptsUnsortedInput(t *testing.T) {
	const today = 100
	s := ComputeStreak([]int64{99, 96, 98, 97}, today)
	if s.Current != 4 {
		t.Errorf("Current = %d, want 4; the input must be normalised, not assumed sorted", s.Current)
	}
}

// Regression: an older run must not be reported as the current one.
//
// The anchor is yesterday (99), the last active day is 97, and 98/99 were both
// missed. An implementation that counts *up* from the last active day tallies
// the 95-97 run and never reaches the gap, reporting 3 instead of 0. This is
// the case that made the first version of ComputeStreak wrong.
func TestComputeStreakOldRunIsNotTheCurrentStreak(t *testing.T) {
	const today = 100
	s := ComputeStreak([]int64{95, 96, 97}, today)
	if s.Current != 0 {
		t.Errorf("Current = %d, want 0: two whole days were missed", s.Current)
	}
	if s.Longest != 3 {
		t.Errorf("Longest = %d, want 3", s.Longest)
	}
}

// Activity dated after `today` (clock skew, or a stale client) must not produce
// a bogus run.
func TestComputeStreakIgnoresFutureActivity(t *testing.T) {
	s := ComputeStreak([]int64{100, 101, 102}, 100)
	if s.Current != 1 {
		t.Errorf("Current = %d, want 1", s.Current)
	}
	if s.ActiveToday != true {
		t.Error("ActiveToday should still report today as active")
	}
}

func TestDayIndex(t *testing.T) {
	const day = 86400
	cases := []struct {
		name string
		ts   int64
		tz   int64
		want int64
	}{
		{"UTC midnight", day, 0, 1},
		{"just before UTC midnight", day - 1, 0, 0},
		// 23:59 UTC is already 07:59 the *next* day in UTC+8 — the whole point
		// of taking an offset rather than dividing by 86400.
		{"late UTC is the next day in UTC+8", day - 60, 8 * 3600, 1},
		{"UTC midnight in UTC+8", day, 8 * 3600, 1},
		{"a China evening is the same day as its UTC morning", day + 12*3600, 8 * 3600, 1},
	}
	for _, c := range cases {
		if got := DayIndex(c.ts, c.tz); got != c.want {
			t.Errorf("%s: DayIndex(%d, %d) = %d, want %d", c.name, c.ts, c.tz, got, c.want)
		}
	}
}

func TestMilestone(t *testing.T) {
	cases := []struct {
		current int
		want    int
		ok      bool
	}{
		{0, 0, false},
		{-5, 0, false},
		{1, 0, false},
		{2, 0, false},
		{3, 3, true},
		{6, 3, true},
		{7, 7, true},
		{13, 7, true},
		{14, 14, true},
		{365, 365, true},
		{400, 365, true},
	}
	for _, c := range cases {
		got, ok := Milestone(c.current)
		if got != c.want || ok != c.ok {
			t.Errorf("Milestone(%d) = (%d, %v), want (%d, %v)", c.current, got, ok, c.want, c.ok)
		}
	}
}

func TestNextMilestone(t *testing.T) {
	if got, ok := NextMilestone(0); got != 3 || !ok {
		t.Errorf("NextMilestone(0) = (%d, %v), want (3, true)", got, ok)
	}
	if got, ok := NextMilestone(3); got != 7 || !ok {
		t.Errorf("NextMilestone(3) = (%d, %v), want (7, true)", got, ok)
	}
	if got, ok := NextMilestone(365); got != 0 || ok {
		t.Errorf("NextMilestone(365) = (%d, %v), want (0, false): no milestone left", got, ok)
	}
}

func TestMergeActivityDays(t *testing.T) {
	const day = 86400
	const today = 100

	// The regression this exists for: a user who reviews cards but never
	// captures anything. Before reviews were merged, their streak was 0 — which
	// reads as "the feature is broken" rather than "you have not studied".
	t.Run("reviews alone build a streak", func(t *testing.T) {
		reviews := []int64{(today - 2) * day, (today - 1) * day, today * day}
		s := MergeActivityDays(nil, reviews, 0, today)
		if s.Current != 3 {
			t.Errorf("Current = %d, want 3 from reviews alone", s.Current)
		}
		if !s.ActiveToday {
			t.Error("ActiveToday = false, want true")
		}
	})

	t.Run("captures alone build a streak", func(t *testing.T) {
		captures := []int64{(today - 1) * day, today * day}
		if s := MergeActivityDays(captures, nil, 0, today); s.Current != 2 {
			t.Errorf("Current = %d, want 2 from captures alone", s.Current)
		}
	})

	// Captures and reviews interleave into one run, not two separate ones.
	t.Run("captures and reviews interleave into one run", func(t *testing.T) {
		captures := []int64{(today - 3) * day}
		reviews := []int64{(today - 2) * day, (today - 1) * day, today * day}
		s := MergeActivityDays(captures, reviews, 0, today)
		if s.Current != 4 {
			t.Errorf("Current = %d, want 4: a review and a capture are both study", s.Current)
		}
	})

	t.Run("same-day capture and review count once", func(t *testing.T) {
		s := MergeActivityDays([]int64{today * day}, []int64{today*day + 3600}, 0, today)
		if s.Current != 1 {
			t.Errorf("Current = %d, want 1: two events on one day are one day", s.Current)
		}
	})

	t.Run("both empty is a zero streak", func(t *testing.T) {
		s := MergeActivityDays(nil, nil, 0, today)
		if s.Current != 0 || s.ActiveToday {
			t.Errorf("got %+v, want a zero streak", s)
		}
	})

	t.Run("timezone offset applies to both sources", func(t *testing.T) {
		// 23:30 UTC on the previous day is already the next day in UTC+8, so
		// both timestamps must land on the same local day.
		ts := int64((today-1)*day + 23*3600 + 1800)
		s := MergeActivityDays([]int64{ts}, nil, 8*3600, today)
		if s.Current != 1 || !s.ActiveToday {
			t.Errorf("got %+v, want an active today in UTC+8", s)
		}
	})
}

func TestMilestoneKeyIsDistinctPerMilestone(t *testing.T) {
	if MilestoneKey(7) == MilestoneKey(30) {
		t.Error("two milestones produced the same key; announcements would collide")
	}
	if MilestoneKey(7) != "milestone:7" {
		t.Errorf("MilestoneKey(7) = %q, want %q", MilestoneKey(7), "milestone:7")
	}
}
