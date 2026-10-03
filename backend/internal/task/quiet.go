package task

// Quiet hours for work-item reminders (docs/学习muse/03-架构方案.md §3.3, P4).
//
// The learning domain already defers its own reminders inside
// learning.nextAfterQuietHours, but that helper answers "when should this
// *recurring* rule next fire". A work item carries a one-shot `remind_at`
// timestamp, which is a different question: "this moment is inside the quiet
// window — push it to the end".
//
// Both helpers are kept because the answers differ: a daily rule that keeps
// missing the window needs re-anchoring, while a one-shot reminder must not be
// silently moved to tomorrow and must not be dropped either.
//
// The window is expressed in minutes from midnight and **wraps midnight**: the
// default 22:30→07:30 has StartMin > EndMin, which is the case that naive
// `start <= x && x < end` implementations get wrong.

import (
	"encoding/json"
	"strings"
	"time"
)

// Default quiet hours: 22:30 → 07:30 local.
const (
	DefaultQuietStartMinute = 22*60 + 30
	DefaultQuietEndMinute   = 7*60 + 30
)

// QuietWindow is a do-not-disturb window in minutes from **the user's local
// midnight**. The location is not part of the window: it is supplied per call,
// because two participants in the same workspace are not in the same timezone
// and a server-wide window silently picks one of them.
type QuietWindow struct {
	StartMin int `json:"startMin"`
	EndMin   int `json:"endMin"`
}

// QuietPreferences is one user's do-not-disturb configuration, as stored under
// the `notifications` / `quiet-hours` user-setting document.
//
// Timezone is an IANA name ("Asia/Shanghai"), not a fixed offset: an offset
// cannot express DST, and a reminder that fires an hour early every March is
// exactly the class of bug this type exists to remove.
type QuietPreferences struct {
	Timezone string `json:"timezone"`
	// Disabled turns do-not-disturb off for this user. It is explicit rather
	// than inferred from a zero window, because 00:00–00:00 is not the way a
	// user says "no quiet hours" and guessing here would silently re-enable
	// notifications someone had deliberately turned off.
	Disabled bool `json:"disabled,omitempty"`
	StartMin int  `json:"startMin"`
	EndMin   int  `json:"endMin"`
}

// Window returns the do-not-disturb window, falling back to the 22:30→07:30
// default when the user has not chosen one. A disabled preference yields the
// zero window, which QuietWindow treats as "not configured" — the reminder
// then fires at its own time.
func (p QuietPreferences) Window() QuietWindow {
	if p.Disabled {
		return QuietWindow{}
	}
	if p.StartMin == 0 && p.EndMin == 0 {
		return DefaultQuietWindow()
	}
	return QuietWindow{StartMin: p.StartMin, EndMin: p.EndMin}
}

// Location resolves the IANA name. An empty or unknown name yields nil, which
// every caller must treat as "use the server's zone" rather than silently
// substituting UTC — guessing UTC is what made the old code wrong on a
// UTC+8 deployment.
func (p QuietPreferences) Location() *time.Location {
	name := strings.TrimSpace(p.Timezone)
	if name == "" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

// QuietPreferencesFromPayload decodes a stored user-setting document. A
// malformed or empty payload yields ok=false, which means "fall back to the
// server defaults" — a bad preference must not stop reminders from firing.
func QuietPreferencesFromPayload(raw []byte) (QuietPreferences, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return QuietPreferences{}, false
	}
	var p QuietPreferences
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return QuietPreferences{}, false
	}
	// Minutes outside a day are not a preference, they are corruption.
	if p.StartMin < 0 || p.StartMin >= 1440 || p.EndMin < 0 || p.EndMin >= 1440 {
		return QuietPreferences{}, false
	}
	return p, true
}

// DefaultQuietWindow is 22:30 → 07:30.
func DefaultQuietWindow() QuietWindow {
	return QuietWindow{StartMin: DefaultQuietStartMinute, EndMin: DefaultQuietEndMinute}
}

// Active reports whether the window does anything. A zero end (or a degenerate
// start == end) means quiet hours are not configured, and must not silently
// defer every reminder to midnight.
func (w QuietWindow) Active() bool {
	return w.EndMin > 0 && w.StartMin != w.EndMin
}

// InWindow reports whether a minute-of-day falls inside the window.
func (w QuietWindow) InWindow(minute int) bool {
	if !w.Active() {
		return false
	}
	if w.StartMin <= w.EndMin {
		// Same-day window, e.g. 09:00–18:00.
		return minute >= w.StartMin && minute < w.EndMin
	}
	// Wrapping window, e.g. 22:30–07:30: either side of midnight.
	return minute >= w.StartMin || minute < w.EndMin
}

// Defer pushes a fire time out to the end of the quiet window. A time outside
// the window is returned unchanged; the caller cannot tell the difference
// except by comparing, which is what the tests do.
//
// loc is the **owner's** timezone. A nil loc means "no preference recorded",
// and falls back to the server's local zone — never to UTC. Getting this wrong
// is what made a 23:50 reminder push on a UTC+8 deployment: the minute-of-day
// was computed against the UTC day boundary, so 23:50 local read as 15:50 and
// looked like midday.
//
// The window's end is always the *next* occurrence after fireAt, so a reminder
// set for 23:50 lands on 07:30 the following morning rather than 07:30 the same
// (already-past) one.
func (w QuietWindow) Defer(fireAt int64, loc *time.Location) int64 {
	if !w.Active() {
		return fireAt
	}
	if loc == nil {
		loc = time.Local
	}
	local := time.Unix(fireAt, 0).In(loc)
	minute := local.Hour()*60 + local.Minute()
	if !w.InWindow(minute) {
		return fireAt
	}
	// The window's end is a **wall-clock** time in the user's zone, built from
	// calendar fields. Two things depend on that: on a DST transition day a
	// local day is 23 or 25 hours long, so adding 7h30m as a duration lands an
	// hour off; and midnight is not 86400 seconds after the previous midnight,
	// so truncating the day and adding a constant is wrong for the same reason.
	endOfDay := func(dayOffset int) time.Time {
		return time.Date(local.Year(), local.Month(), local.Day()+dayOffset,
			w.EndMin/60, w.EndMin%60, 0, 0, loc)
	}
	end := endOfDay(0)
	if !end.After(local) {
		// Today's end already passed: the next occurrence is tomorrow's.
		end = endOfDay(1)
	}
	return end.Unix()
}

// DeferralMinutes reports how far a fire time was pushed, so a caller can log
// or surface the deferral instead of silently changing the time.
func (w QuietWindow) DeferralMinutes(fireAt int64, loc *time.Location) int {
	moved := w.Defer(fireAt, loc)
	if moved <= fireAt {
		return 0
	}
	return int((moved - fireAt) / 60)
}

// ReminderEventID is the idempotency key for a work-item reminder
// (docs/学习muse/03-架构方案.md §4.2: "每任务每提醒点只发一次"). Because
// work_item_events is keyed on (workspace, task, event_id), deriving the id
// from remind_at is what makes a repeated scheduler tick a no-op instead of a
// second notification — the uniqueness is enforced by the table, not by a
// best-effort check in the executor.
func ReminderEventID(remindAt int64) string {
	return "reminder:" + strings.TrimSpace(formatInt(remindAt))
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
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
