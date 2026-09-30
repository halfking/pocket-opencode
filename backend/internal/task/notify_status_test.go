package task

// B-10: `status_changed` and `completed` had notification kinds and recipient
// rules but no producer — nothing in the server ever wrote either event, so
// completing a work item told nobody. StatusChangeEventType is the decision the
// PATCH path makes, and it is pure, so the mapping is pinned here.
//
// The wiring (that PATCH actually calls the producer) is guarded in the server
// package by TestTaskStatusChangeEmitsEvent, which reads the source; the event
// write itself needs a database.

import "testing"

func TestStatusChangeEventType(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
		want string
		why  string
	}{
		{"active to blocked", "active", "blocked", EventStatusChanged, ""},
		{"blocked back to active", "blocked", "active", EventStatusChanged, ""},
		{"to completed", "active", "completed", EventCompleted,
			"completion is its own event: it also notifies the person who did it"},
		{"to accepted", "active", "accepted", EventCompleted,
			"accepting closes the loop the same way completing does"},
		{"same status", "active", "active", "", "no change means no event and no notification"},
		{"same status with different case", "Active", " active ", "",
			"the comparison must not fire on whitespace or casing"},
		{"empty to active", "", "active", EventStatusChanged,
			"a claim that sets the first status is still a transition"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StatusChangeEventType(c.from, c.to); got != c.want {
				t.Errorf("StatusChangeEventType(%q, %q) = %q, want %q %s", c.from, c.to, got, c.want, c.why)
			}
		})
	}
}

// Every event this function can return must map to a real notification kind —
// otherwise a status change would be recorded in the activity feed and dropped
// on the way to the user's device, which is the bug this whole path exists to
// close.
func TestStatusChangeEventTypeAlwaysNotifies(t *testing.T) {
	for _, from := range []string{"", "active", "blocked", "completed"} {
		for _, to := range []string{"active", "blocked", "completed", "accepted"} {
			ev := StatusChangeEventType(from, to)
			if ev == "" {
				continue
			}
			if kind := NotificationKind(ev); kind == "" {
				t.Errorf("%q -> %q produces event %q, which maps to no notification kind", from, to, ev)
			}
		}
	}
}
