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

import "strings"

// Default quiet hours: 22:30 → 07:30 local.
const (
	DefaultQuietStartMinute = 22*60 + 30
	DefaultQuietEndMinute   = 7*60 + 30
)

// QuietWindow is a do-not-disturb window in minutes from local midnight.
type QuietWindow struct {
	StartMin int `json:"startMin"`
	EndMin   int `json:"endMin"`
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
// The window's end is always the *next* occurrence after fireAt, so a reminder
// set for 23:50 lands on 07:30 the following morning rather than 07:30 the same
// (already-past) one.
func (w QuietWindow) Defer(fireAt int64) int64 {
	if !w.Active() {
		return fireAt
	}
	minute := int((fireAt / 60) % 1440)
	if !w.InWindow(minute) {
		return fireAt
	}
	dayStart := fireAt - (fireAt % 86400)
	endAt := dayStart + int64(w.EndMin)*60
	if endAt <= fireAt {
		// The window's end already passed today; it belongs to the next day.
		endAt += 86400
	}
	return endAt
}

// DeferralMinutes reports how far a fire time was pushed, so a caller can log
// or surface the deferral instead of silently changing the time.
func (w QuietWindow) DeferralMinutes(fireAt int64) int {
	moved := w.Defer(fireAt)
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
