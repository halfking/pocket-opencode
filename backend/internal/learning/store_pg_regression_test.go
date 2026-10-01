package learning

// Two invariants that the broad lifecycle tests cover only incidentally, each
// of which was broken at the time this file was written.
//
// Both need a real PostgreSQL, so they live beside store_pg_test.go and share
// its newTestStore.

import (
	"context"
	"testing"
	"time"
)

// Snoozing means "later". It must never mean "sooner than it already was".
//
// SnoozeReminder measured the new time from `now`, so snoozing a reminder that
// was already scheduled for tomorrow by two hours rescheduled it to two hours
// from now — the user asked to be reminded later and got a notification
// twenty-two hours earlier, and a daily rule stopped matching its own
// RuleValue. The bug is invisible for an overdue reminder (there `now` is the
// right base) and only bites once next_due_at is in the future, which is why it
// survived: the common case looks correct.
func TestSnoozeNeverPullsAReminderForward(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	r, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-tomorrow", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDailyDigest), RuleKind: string(RuleDaily), RuleValue: "08:00",
		NextDueAt: now + 86400,
	})
	if err != nil {
		t.Fatalf("UpsertReminder: %v", err)
	}

	next, err := s.SnoozeReminder(ctx, "ws-1", "alice", r.ID, 120)
	if err != nil {
		t.Fatalf("SnoozeReminder: %v", err)
	}
	if next <= now+86400 {
		t.Errorf("snoozed to %d, want later than the scheduled %d — the reminder was pulled forward",
			next, now+86400)
	}
	// And the stored value has to agree with the returned one, or the caller
	// and the next scheduler tick disagree about when this fires.
	var stored int64
	if err := s.pool.QueryRow(ctx,
		`SELECT next_due_at FROM learning_reminders WHERE id = $1`, r.ID).Scan(&stored); err != nil {
		t.Fatalf("read back next_due_at: %v", err)
	}
	if stored != next {
		t.Errorf("stored next_due_at = %d but SnoozeReminder returned %d", stored, next)
	}
}

// The other end of the same floor: a reminder that is already overdue must
// still end up in the future. Without the GREATEST(next_due_at, now) term,
// snoozing an overdue reminder by less than its lateness leaves it overdue and
// it fires again on the very next tick — the snooze silently does nothing.
func TestSnoozeOfAnOverdueReminderStillLandsInTheFuture(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	r, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-overdue", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDeadline), RuleKind: string(RuleOnce), RuleValue: "x",
		NextDueAt: now - 7200, // two hours late
	})
	if err != nil {
		t.Fatalf("UpsertReminder: %v", err)
	}

	next, err := s.SnoozeReminder(ctx, "ws-1", "alice", r.ID, 5) // snooze by five minutes
	if err != nil {
		t.Fatalf("SnoozeReminder: %v", err)
	}
	if next <= now {
		t.Errorf("snoozed to %d, want later than now (%d) — it is still overdue and will fire again immediately",
			next, now)
	}
	if got, _ := s.DueReminders(ctx, "ws-1", "alice", now, 50); len(got) != 0 {
		t.Errorf("a snoozed reminder must not be due, got %+v", got)
	}
}

// sinceUnix is a bound on the timestamps returned, not only on the rows
// scanned. ActiveDayTimestamps is the streak's input, so a captured_at from
// before the window leaking through does not merely return an extra number —
// it credits the user with a day they were not active.
func TestActiveDayTimestampsNeverReturnsBeforeSince(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	// An item captured long before the window but updated inside it: exactly the
	// shape that leaked, because the row qualifies on updated_at.
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO learning_items
			(id, workspace_id, user_id, source_kind, source_id, title, captured_at, updated_at)
		VALUES ('mixed', 'ws-1', 'alice', 'note', 'mixed', 't', $1, $2)`, now-86400, now-60); err != nil {
		t.Fatalf("seed: %v", err)
	}

	since := now - 3600
	got, err := s.ActiveDayTimestamps(ctx, "ws-1", "alice", since)
	if err != nil {
		t.Fatalf("ActiveDayTimestamps: %v", err)
	}
	for _, ts := range got {
		if ts < since {
			t.Errorf("returned %d, which is %d seconds before the since bound %d",
				ts, since-ts, since)
		}
	}
	if len(got) != 1 {
		t.Errorf("got %v, want just the updated_at %d", got, now-60)
	}
}
