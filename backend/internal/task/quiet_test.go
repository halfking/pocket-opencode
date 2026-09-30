package task

// Quiet-hours deferral for work-item reminders, and the reminder idempotency
// key. The cases here are the ones a naive `start <= x && x < end` gets wrong.

import (
	"testing"
	"time"
)

// at builds a unix timestamp for a given minute-of-day on day 0, so the
// arithmetic is readable.
func at(minute int) int64 { return int64(minute) * 60 }

func TestQuietWindowActive(t *testing.T) {
	if !DefaultQuietWindow().Active() {
		t.Error("the 22:30 to 07:30 default must be active")
	}
	// A zero end means "not configured". If this were active, every reminder
	// would be deferred to midnight.
	if (QuietWindow{}).Active() {
		t.Error("an empty window must not be active")
	}
	if (QuietWindow{StartMin: 600, EndMin: 600}).Active() {
		t.Error("a degenerate start == end window must not be active")
	}
}

func TestQuietWindowInWindowWrapsMidnight(t *testing.T) {
	w := DefaultQuietWindow() // 22:30 (1350) to 07:30 (450)
	cases := []struct {
		minute int
		want   bool
		why    string
	}{
		{0, true, "just after midnight is inside the window"},
		{449, true, "one minute before the end"},
		{450, false, "the end minute itself is outside"},
		{720, false, "midday is outside"},
		{1349, false, "one minute before the start"},
		{1350, true, "the start minute itself is inside"},
		{1439, true, "just before midnight is inside"},
	}
	for _, c := range cases {
		if got := w.InWindow(c.minute); got != c.want {
			t.Errorf("InWindow(%d) = %v, want %v (%s)", c.minute, got, c.want, c.why)
		}
	}
}

func TestQuietWindowInWindowSameDay(t *testing.T) {
	// A 09:00 to 18:00 window does not wrap and must not use the wrap rule.
	w := QuietWindow{StartMin: 9 * 60, EndMin: 18 * 60}
	cases := map[int]bool{
		8 * 60:     false,
		9 * 60:     true,
		17*60 + 59: true,
		18 * 60:    false,
		23 * 60:    false,
		2 * 60:     false, // a wrapping window would say true here
	}
	for minute, want := range cases {
		if got := w.InWindow(minute); got != want {
			t.Errorf("InWindow(%d) = %v, want %v", minute, got, want)
		}
	}
}

// at builds a unix timestamp for a given minute-of-day on day 0, so the
// arithmetic is readable.
//
// The cases below state their zone explicitly at the call site (time.UTC),
// because that is the whole point: they used to run against an implicit UTC
// day boundary, which made them pass for any user who is not eight hours off
// it. quiet_timezone_test.go covers the non-UTC cases that used to be wrong.
func TestQuietWindowDefer(t *testing.T) {
	utc := time.UTC
	w := DefaultQuietWindow()
	cases := []struct {
		name   string
		fireAt int64
		want   int64
		why    string
	}{
		{
			name:   "late evening rolls to the next morning",
			fireAt: at(23*60 + 50),
			// 23:50 is inside the window. 07:30 *today* already passed, so the
			// deferral must land on tomorrow's 07:30 (day 1 = minute 1890),
			// not on this morning's — which would be a reminder in the past.
			want: at(24*60 + 7*60 + 30),
			why:  "the window's end is the next occurrence, never a past one",
		},
		{
			name:   "early morning defers to the same day's end",
			fireAt: at(3 * 60),
			want:   at(7*60 + 30),
			why:    "07:30 today is still ahead",
		},
		{
			name:   "daytime is untouched",
			fireAt: at(14 * 60),
			want:   at(14 * 60),
			why:    "outside the window",
		},
		{
			name:   "the end minute itself is untouched",
			fireAt: at(7*60 + 30),
			want:   at(7*60 + 30),
			why:    "the window is half-open",
		},
	}
	for _, c := range cases {
		if got := w.Defer(c.fireAt, utc); got != c.want {
			t.Errorf("%s: Defer(%d) = %d, want %d (%s)", c.name, c.fireAt, got, c.want, c.why)
		}
	}
}

func TestQuietWindowDeferDisabledIsIdentity(t *testing.T) {
	var w QuietWindow // not configured
	fire := at(23 * 60)
	if got := w.Defer(fire, time.UTC); got != fire {
		t.Errorf("an unconfigured window changed the fire time: %d to %d", fire, got)
	}
	if got := w.DeferralMinutes(fire, time.UTC); got != 0 {
		t.Errorf("an unconfigured window reported a %d minute deferral", got)
	}
}

func TestQuietWindowDeferralMinutes(t *testing.T) {
	w := DefaultQuietWindow()
	// 03:00 to 07:30 is 4h30m.
	if got := w.DeferralMinutes(at(3*60), time.UTC); got != 4*60+30 {
		t.Errorf("DeferralMinutes(03:00) = %d, want %d", got, 4*60+30)
	}
	if got := w.DeferralMinutes(at(14*60), time.UTC); got != 0 {
		t.Errorf("DeferralMinutes(14:00) = %d, want 0", got)
	}
}

func TestReminderEventIDIsStableAndDistinct(t *testing.T) {
	// The same reminder point must always produce the same id, or the
	// (workspace, task, event_id) primary key stops deduplicating replays.
	if ReminderEventID(1700000000) != ReminderEventID(1700000000) {
		t.Error("ReminderEventID is not stable for the same input")
	}
	if ReminderEventID(1700000000) == ReminderEventID(1700000060) {
		t.Error("two different reminder points produced the same id")
	}
	if got := ReminderEventID(0); got != "reminder:0" {
		t.Errorf("ReminderEventID(0) = %q, want %q", got, "reminder:0")
	}
	if got := ReminderEventID(-1); got != "reminder:-1" {
		t.Errorf("ReminderEventID(-1) = %q, want %q", got, "reminder:-1")
	}
}
