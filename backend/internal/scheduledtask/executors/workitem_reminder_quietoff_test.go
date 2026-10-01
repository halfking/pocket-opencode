package executors

// The executor offers two ways to switch do-not-disturb off, and they used to
// disagree. A user who disabled it in settings was left alone, while
// SetQuietWindow(task.QuietWindow{}) — documented on the setter as
// "deferral disabled, a legitimate configuration" — had the zero window
// quietly replaced by the 22:30-07:30 default inside quietResolver.window.
// Every reminder landing in 22:30-07:30 was therefore deferred for a caller
// that had explicitly asked for no quiet hours.
//
// The pair below pins the contrast, and does it without reading the wall
// clock: both scenarios use a reminder at 02:00 UTC, which is inside the
// default window on every day of the year. The only difference between them is
// the window, so a green run means the window is what decided — not the hour
// the suite happened to run at.

import (
	"context"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// reminderAtUTC pins a reminder to hour:min on the current UTC calendar day.
//
// The day is taken from now so the reminder is never stale, which keeps the
// staleness bound from deciding the outcome of a test about quiet hours.
func reminderAtUTC(hour, min int) int64 {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, time.UTC).Unix()
}

func TestSetQuietWindowZeroDisablesDeferral(t *testing.T) {
	pinServerZone(t)

	t.Run("the default window defers a 02:00 reminder", func(t *testing.T) {
		store := newFakeStore()
		store.due = []task.Task{{ID: "t-1", Title: "Night owl", OwnerID: "alice",
			WorkspaceID: "ws-1", RemindAt: reminderAtUTC(2, 0)}}
		ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
		ex.SetStaleAfter(0) // staleness is not what this test is about

		res, err := ex.Execute(context.Background(), schedTask())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		out := decode(t, res)
		if num(t, out, "deferred") != 1 {
			t.Errorf("02:00 is inside the 22:30-07:30 default, so it must defer: %+v", out)
		}
		if num(t, out, "fired") != 0 {
			t.Errorf("a deferred reminder must not also fire: %+v", out)
		}
	})

	t.Run("the zero window fires it at its own time", func(t *testing.T) {
		store := newFakeStore()
		store.due = []task.Task{{ID: "t-1", Title: "Night owl", OwnerID: "alice",
			WorkspaceID: "ws-1", RemindAt: reminderAtUTC(2, 0)}}
		notif := &fakeWorkNotifier{}
		ex := NewWorkItemReminderExecutor(store, notif)
		ex.SetStaleAfter(0)
		ex.SetQuietWindow(task.QuietWindow{}) // "deferral disabled"

		res, err := ex.Execute(context.Background(), schedTask())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		out := decode(t, res)
		if num(t, out, "deferred") != 0 {
			t.Fatalf("quiet hours are off, so nothing may be deferred: %+v", out)
		}
		if num(t, out, "fired") != 1 {
			t.Fatalf("the reminder must fire at its own time: %+v", out)
		}
		if len(store.events) != 1 {
			t.Errorf("expected exactly one reminded event, got %d", len(store.events))
		}
		if len(notif.events) != 1 || notif.events[0].UserID != "alice" {
			t.Errorf("the owner should have been notified, got %+v", notif.events)
		}
		// Nothing was pushed, so remind_at must be retired rather than left to
		// re-fire on the next tick.
		if got := store.cleared["t-1"]; got != 0 {
			t.Errorf("remind_at after firing = %d, want 0", got)
		}
	})
}

// The constructor, not the resolver, is where the 22:30-07:30 default comes
// from. A build that wires the executor the normal way still defers at 02:00;
// this is the half of the contract that the previous test's contrast depends
// on, and it is what keeps production behaviour unchanged by the fix.
func TestDefaultQuietWindowStillAppliesWithoutConfiguration(t *testing.T) {
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Night owl", OwnerID: "alice",
		WorkspaceID: "ws-1", RemindAt: reminderAtUTC(2, 0)}}
	ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
	ex.SetStaleAfter(0)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if num(t, decode(t, res), "deferred") != 1 {
		t.Error("an unconfigured executor must still apply the 22:30-07:30 default")
	}
}
