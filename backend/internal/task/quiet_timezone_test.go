package task

// B-11: quiet hours were evaluated against the **UTC** day boundary.
//
// `minute := int((fireAt / 60) % 1440)` asks "what time is it in UTC", and
// `dayStart := fireAt - (fireAt % 86400)` assumes every day is 86400 seconds.
// For a user in UTC+8 that is eight hours out: a reminder set for 23:50 local
// reads as 15:50 UTC, looks like the middle of the afternoon, and is pushed out
// at 23:50 local while the user is asleep. The whole chain was only correct on
// a deployment whose server clock happened to sit in the user's own zone —
// which is why the earlier "deferral verified" note was true only under UTC.
//
// The decision here is per-user storage: the window and the day boundary are
// resolved in the owner's timezone, read from their settings.

import (
	"testing"
	"time"
)

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

// localAt builds the unix timestamp of a wall-clock time in loc, so the test
// states its intent in the same terms the user thinks in.
func localAt(t *testing.T, loc *time.Location, y int, month time.Month, d, hour, min int) int64 {
	t.Helper()
	return time.Date(y, month, d, hour, min, 0, 0, loc).Unix()
}

// The bug, stated as a test: 23:50 in Shanghai must be pushed to 07:30 the
// next morning, Shanghai time.
func TestQuietWindowDeferUsesTheUsersTimezone(t *testing.T) {
	loc := shanghai(t)
	w := DefaultQuietWindow() // 22:30 → 07:30

	late := localAt(t, loc, 2026, time.September, 30, 23, 50)
	local := time.Unix(late, 0).In(loc)
	if !w.InWindow(local.Hour()*60 + local.Minute()) {
		t.Fatalf("the fixture is at %s in %s, which is outside the quiet window; the test would prove nothing",
			local.Format(time.RFC3339), loc)
	}
	moved := w.Defer(late, loc)
	if moved <= late {
		t.Fatalf("23:50 %s was not deferred at all", loc)
	}
	got := time.Unix(moved, 0).In(loc)
	if got.Hour() != 7 || got.Minute() != 30 {
		t.Errorf("deferred to %s, want 07:30 %s", got.Format(time.RFC3339), loc)
	}
	if got.Day() != 1 || got.Month() != time.October {
		t.Errorf("deferred to %s, want 2026-10-01 07:30 %s", got.Format(time.RFC3339), loc)
	}
}

// The same instant, evaluated in UTC, must NOT be deferred — that is precisely
// the old behaviour, and the difference between the two is the bug.
func TestQuietWindowUTCIsNotTheUsersTimezone(t *testing.T) {
	loc := shanghai(t)
	late := localAt(t, loc, 2026, time.September, 30, 23, 50)

	inUTC := time.Unix(late, 0).UTC()
	if inUTC.Hour() >= 22 {
		t.Skipf("fixture landed at %s UTC, which is inside the window; the contrast would not show", inUTC)
	}
	if got := DefaultQuietWindow().Defer(late, time.UTC); got != late {
		t.Errorf("in UTC the reminder moved to %d; this test documents the old, wrong answer (%s UTC)",
			got, inUTC.Format(time.RFC3339))
	}
}

// A user in a western zone must not inherit the server's or another user's
// window: the same wall-clock intent in New York defers differently.
func TestQuietWindowIsPerUserNotPerServer(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	sh := shanghai(t)
	fire := localAt(t, sh, 2026, time.September, 30, 23, 50)

	if got := DefaultQuietWindow().Defer(fire, sh); got == fire {
		t.Fatal("the Shanghai owner should have been deferred")
	}
	if got := DefaultQuietWindow().Defer(fire, ny); got != fire {
		t.Errorf("the same instant is %s in New York, which is outside the window; it must not be deferred, got %d",
			time.Unix(fire, 0).In(ny).Format(time.RFC3339), got)
	}
}

// No stored timezone must fall back to the server's zone, never to UTC by
// accident: with time.Local pinned to UTC+8 the answer has to be the +8 one.
func TestQuietWindowNilLocationUsesServerZoneNotUTC(t *testing.T) {
	orig := time.Local
	time.Local = shanghai(t)
	t.Cleanup(func() { time.Local = orig })

	fire := localAt(t, time.Local, 2026, time.September, 30, 23, 50)
	if got := DefaultQuietWindow().Defer(fire, nil); got <= fire {
		t.Errorf("a nil location must use the server zone (UTC+8 here) and defer, got %d", got)
	}
}

// A DST transition day is 23 or 25 hours long. Fixed 86400 arithmetic lands an
// hour off for half the year; the calendar-based end does not.
func TestQuietWindowDeferSurvivesDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// US DST ends 2026-11-01: that local day is 25 hours long.
	fire := localAt(t, ny, 2026, time.November, 1, 23, 50)
	moved := DefaultQuietWindow().Defer(fire, ny)
	got := time.Unix(moved, 0).In(ny)
	if got.Hour() != 7 || got.Minute() != 30 {
		t.Errorf("after a 25-hour day the reminder landed at %s, want 07:30 local", got.Format(time.RFC3339))
	}
	if got.Day() != 2 {
		t.Errorf("deferred to %s, want 2026-11-02", got.Format(time.RFC3339))
	}
}

// --- preferences: decoding and validation ---

func TestQuietPreferencesFromPayload(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantOK  bool
		tz      string
		start   int
		end     int
		whyFail string
	}{
		{
			name: "a complete document", raw: `{"timezone":"Asia/Shanghai","startMin":1350,"endMin":450}`,
			wantOK: true, tz: "Asia/Shanghai", start: 1350, end: 450,
		},
		{
			name: "timezone only, window left to the default", raw: `{"timezone":"Europe/Berlin"}`,
			wantOK: true, tz: "Europe/Berlin",
		},
		{
			name: "an explicit window", raw: `{"timezone":"UTC","startMin":540,"endMin":1080}`,
			wantOK: true, tz: "UTC", start: 540, end: 1080,
		},
		{
			name: "explicitly disabled", raw: `{"disabled":true,"startMin":0,"endMin":0}`,
			wantOK: true,
		},
		{name: "empty", raw: "", whyFail: "a missing document is the normal case"},
		{name: "blank", raw: "   ", whyFail: "whitespace is not a preference"},
		{name: "malformed json", raw: `{"timezone":`, whyFail: "a corrupt document must fall back, not error"},
		{name: "wrong type", raw: `{"startMin":"nine"}`, whyFail: "a string where a number belongs is corruption"},
		{name: "minute out of range", raw: `{"startMin":1500,"endMin":450}`, whyFail: "1500 is not a minute of any day"},
		{name: "negative minute", raw: `{"startMin":-30,"endMin":450}`, whyFail: "a negative minute is not a preference"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := QuietPreferencesFromPayload([]byte(c.raw))
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (%s)", ok, c.wantOK, c.whyFail)
			}
			if !ok {
				return
			}
			if got.Timezone != c.tz {
				t.Errorf("timezone = %q, want %q", got.Timezone, c.tz)
			}
			if got.StartMin != c.start || got.EndMin != c.end {
				t.Errorf("window = %d→%d, want %d→%d", got.StartMin, got.EndMin, c.start, c.end)
			}
		})
	}
}

// An unknown IANA name must resolve to "no location" rather than to UTC: the
// caller falls back to the server zone, which is a documented default, instead
// of silently picking a zone the user never asked for.
func TestQuietPreferencesUnknownTimezoneIsNotUTC(t *testing.T) {
	p := QuietPreferences{Timezone: "Mars/Olympus_Mons"}
	if loc := p.Location(); loc != nil {
		t.Errorf("unknown timezone resolved to %v, want nil (caller falls back)", loc)
	}
	if loc := (QuietPreferences{}).Location(); loc != nil {
		t.Errorf("empty timezone resolved to %v, want nil", loc)
	}
	if loc := (QuietPreferences{Timezone: "  Asia/Shanghai  "}).Location(); loc == nil || loc.String() != "Asia/Shanghai" {
		t.Errorf("a padded but valid name resolved to %v", loc)
	}
}

// A document with only a timezone still gets the default window; a document
// with only a window still gets a resolvable (nil) location.
func TestQuietPreferencesWindowFallback(t *testing.T) {
	if w := (QuietPreferences{}).Window(); w != DefaultQuietWindow() {
		t.Errorf("empty preferences window = %+v, want the 22:30→07:30 default", w)
	}
	if w := (QuietPreferences{Timezone: "UTC"}).Window(); w != DefaultQuietWindow() {
		t.Errorf("timezone-only window = %+v, want the default", w)
	}
	want := QuietWindow{StartMin: 540, EndMin: 1080}
	if w := (QuietPreferences{StartMin: 540, EndMin: 1080}).Window(); w != want {
		t.Errorf("explicit window = %+v, want %+v", w, want)
	}
	// Turning do-not-disturb off must stay off: a disabled document resolves
	// to the inactive zero window, not back to the 22:30 default.
	off := (QuietPreferences{Disabled: true}).Window()
	if off.Active() {
		t.Errorf("a disabled preference produced an active window %+v", off)
	}
	// …and it really is a no-op at the reminder layer.
	fire := localAt(t, shanghai(t), 2026, time.September, 30, 23, 50)
	if got := off.Defer(fire, shanghai(t)); got != fire {
		t.Errorf("a disabled window moved the reminder to %d", got)
	}
}
