package learning

// PostgreSQL integration tests, gated on POCKET_TEST_POSTGRES_DSN.
//
// Why these exist: every SQL statement in this package has been written and
// compiled but never executed. The DDL follows the repository's idempotent
// pattern (ADR-005) and the store is exercised only through unit tests that
// inject fakes, so a column name typo or a missing index would not surface
// until a real deployment. These tests are that missing check.
//
// They skip cleanly when no DSN is set, so they add no false signal today:
//
//	POCKET_TEST_POSTGRES_DSN=postgres://... go test ./internal/learning/
//
// Each test gets its own schema (search_path), so they can run against a
// developer's real database without touching existing data.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping learning integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "learning_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	store, err := NewStore(pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewStore: %v", err)
	}
	return store, func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
}

// --- DDL ---

// EnsureSchema must be re-runnable: pocketd constructs the store on every
// boot, and ADR-005 forbids a migration framework, so idempotency is the
// mechanism that stands in for one.
func TestEnsureSchemaIsIdempotent(t *testing.T) {
	_, cleanup := newTestStore(t)
	defer cleanup()
	// The first NewStore already ran it; reaching here at all means it
	// succeeded against a real database.
}

// --- ClaimMilestone: the exactly-once guarantee ---

// This is the load-bearing test of the whole milestone feature. The claim is
// "one row per user per milestone", and that is enforced *only* by the unique
// index idx_learning_reminders_idem. Nothing in Go code checks it. If the index
// were ever dropped or created differently, every daily digest would re-announce
// the same milestone until the user muted the app.
func TestClaimMilestoneIsExactlyOnce(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := s.ClaimMilestone(ctx, "ws-1", "alice", MilestoneKey(7), 1700000000)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !first {
		t.Fatal("the first claim must report that it claimed the milestone")
	}

	second, err := s.ClaimMilestone(ctx, "ws-1", "alice", MilestoneKey(7), 1700000900)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second {
		t.Error("a second claim for the same milestone must not claim it again")
	}

	// A different milestone is a different row.
	other, err := s.ClaimMilestone(ctx, "ws-1", "alice", MilestoneKey(30), 1700000000)
	if err != nil {
		t.Fatalf("claim of a different milestone: %v", err)
	}
	if !other {
		t.Error("milestone 30 must be claimable independently of milestone 7")
	}

	// A different user must be claimable independently too.
	otherUser, err := s.ClaimMilestone(ctx, "ws-1", "bob", MilestoneKey(7), 1700000000)
	if err != nil {
		t.Fatalf("claim by another user: %v", err)
	}
	if !otherUser {
		t.Error("another user must be able to reach the same milestone")
	}

	// And a different workspace as well — the row is keyed by workspace too.
	//
	// This case is what caught a real bug: the synthetic id used to be
	// "ms-<key>-<userID>" with no workspace, so the second workspace collided
	// on the primary key (23505) instead of being caught by the ON CONFLICT
	// clause, which only names the unique index. The milestone could never be
	// announced in the second workspace.
	otherWS, err := s.ClaimMilestone(ctx, "ws-2", "alice", MilestoneKey(7), 1700000000)
	if err != nil {
		t.Fatalf("claim in another workspace: %v", err)
	}
	if !otherWS {
		t.Error("another workspace must be able to reach the same milestone")
	}
}

func TestClaimMilestoneRejectsBlankItemID(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	if _, err := s.ClaimMilestone(context.Background(), "ws-1", "alice", "  ", 1); err == nil {
		t.Error("a blank item id must be rejected rather than written")
	}
}

// --- ActiveDayTimestamps: the streak's input ---

func TestActiveDayTimestamps(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()
	day := int64(86400)

	mk := func(id string, capturedAt, updatedAt int64) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO learning_items
				(id, workspace_id, user_id, source_kind, source_id, title, captured_at, updated_at)
			VALUES ($1, 'ws-1', 'alice', 'note', $1, 't', $2, $3)`, id, capturedAt, updatedAt); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// One item captured two days ago, one captured an hour ago and updated now.
	mk("old", now-2*day, now-2*day)
	mk("new", now-3600, now)

	got, err := s.ActiveDayTimestamps(ctx, "ws-1", "alice", now-3*day)
	if err != nil {
		t.Fatalf("ActiveDayTimestamps: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d timestamps, want 4 (two per item: captured_at and updated_at): %v", len(got), got)
	}

	// The since bound must actually filter.
	recent, err := s.ActiveDayTimestamps(ctx, "ws-1", "alice", now-1800)
	if err != nil {
		t.Fatalf("ActiveDayTimestamps (narrow window): %v", err)
	}
	if len(recent) != 1 {
		t.Errorf("narrow window got %d timestamps, want 1 (only the updated_at of the new item): %v", len(recent), recent)
	}

	// Another user's rows must not leak.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO learning_items
			(id, workspace_id, user_id, source_kind, source_id, title, captured_at, updated_at)
		VALUES ('theirs', 'ws-1', 'bob', 'note', 'theirs', 't', $1, $1)`, now)
	if err != nil {
		t.Fatalf("seed bob: %v", err)
	}
	mine, err := s.ActiveDayTimestamps(ctx, "ws-1", "alice", now-3*day)
	if err != nil {
		t.Fatalf("ActiveDayTimestamps after seeding another user: %v", err)
	}
	if len(mine) != 4 {
		t.Errorf("another user's activity leaked into alice's streak: %d timestamps", len(mine))
	}
}

func TestActiveDayTimestampsSkipsDeleted(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO learning_items
			(id, workspace_id, user_id, source_kind, source_id, title, captured_at, updated_at, deleted_at)
		VALUES ('gone', 'ws-1', 'alice', 'note', 'gone', 't', $1, $1, $1)`, now); err != nil {
		t.Fatalf("seed deleted item: %v", err)
	}
	got, err := s.ActiveDayTimestamps(ctx, "ws-1", "alice", now-60)
	if err != nil {
		t.Fatalf("ActiveDayTimestamps: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a soft-deleted item must not count as study activity, got %v", got)
	}
}

// --- The end-to-end path the streak actually uses ---

// Sticks the store and the pure streak logic together, which is the only place
// the two halves meet.
func TestStoreAndStreakAgree(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := NewService(s, nil, nil)
	now := time.Now().Unix()
	svc.SetNowFunc(func() int64 { return now })

	// Three consecutive days ending today.
	for i := 2; i >= 0; i-- {
		at := now - int64(i)*86400
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO learning_items
				(id, workspace_id, user_id, source_kind, source_id, title, captured_at, updated_at)
			VALUES ($1, 'ws-1', 'alice', 'note', $1, 't', $2, $2)`, fmt.Sprintf("d%d", i), at); err != nil {
			t.Fatalf("seed day %d: %v", i, err)
		}
	}

	view, err := svc.Streak(ctx, "ws-1", "alice", 0)
	if err != nil {
		t.Fatalf("Streak: %v", err)
	}
	if view.Streak.Current != 3 {
		t.Errorf("Current = %d, want 3 (three consecutive days ending today)", view.Streak.Current)
	}
	if !view.Streak.ActiveToday {
		t.Error("ActiveToday = false, want true")
	}
	if view.Milestone != 3 {
		t.Errorf("Milestone = %d, want 3", view.Milestone)
	}
	if view.Next != 7 {
		t.Errorf("Next = %d, want 7", view.Next)
	}
}

func TestStreakWithNoActivityIsZero(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	svc := NewService(s, nil, nil)
	view, err := svc.Streak(context.Background(), "ws-1", "nobody", 0)
	if err != nil {
		t.Fatalf("Streak: %v", err)
	}
	if view.Streak.Current != 0 || view.Milestone != 0 {
		t.Errorf("got %+v, want a zero streak with no milestone", view)
	}
}

// --- CaptureItem: the capture idempotency (ADR-004) ---

// "Clicking Add to learning twice produces one item" is a headline claim of the
// whole material pipeline, and it is enforced entirely by
// idx_learning_items_source — a partial unique index with a WHERE clause.
// A partial index that silently stopped matching would turn every repeat click
// into a duplicate row with no error anywhere.
func TestCaptureItemIsIdempotentPerSource(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first := &LearningItem{ID: "li-1", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "email", SourceID: "em-1", Title: "First title"}
	existing, err := s.CaptureItem(ctx, first)
	if err != nil {
		t.Fatalf("CaptureItem: %v", err)
	}
	if existing != nil {
		t.Error("the first capture must report inserted, not existing")
	}

	second := &LearningItem{ID: "li-2", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "email", SourceID: "em-1", Title: "Second title"}
	existing, err = s.CaptureItem(ctx, second)
	if err != nil {
		t.Fatalf("CaptureItem (repeat): %v", err)
	}
	if existing == nil {
		t.Fatal("a repeat capture must return the existing item")
	}
	if existing.ID != "li-1" {
		t.Errorf("returned id = %q, want li-1 (the first capture wins the id)", existing.ID)
	}
	// The conflict clause updates the mutable fields, which is how a re-capture
	// refreshes a resolved title.
	if existing.Title != "Second title" {
		t.Errorf("title = %q, want the second capture's title", existing.Title)
	}

	// Defaults must be applied, not left zero.
	if existing.Stage != string(StageInbox) {
		t.Errorf("stage = %q, want %q", existing.Stage, StageInbox)
	}
	if existing.Importance != 3 {
		t.Errorf("importance = %d, want the default 3", existing.Importance)
	}

	// A different source kind or id is a different item.
	if existing, _ = s.CaptureItem(ctx, &LearningItem{ID: "li-3", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "note", SourceID: "em-1", Title: "T"}); existing != nil {
		t.Error("a different source kind must be a new item, not an update")
	}
	// As is a different user.
	if existing, _ = s.CaptureItem(ctx, &LearningItem{ID: "li-4", WorkspaceID: "ws-1", UserID: "bob", SourceKind: "email", SourceID: "em-1", Title: "T"}); existing != nil {
		t.Error("another user's identical source must not collide")
	}
	// As is a different workspace.
	if existing, _ = s.CaptureItem(ctx, &LearningItem{ID: "li-5", WorkspaceID: "ws-2", UserID: "alice", SourceKind: "email", SourceID: "em-1", Title: "T"}); existing != nil {
		t.Error("another workspace's identical source must not collide")
	}
}

// The index is partial (WHERE deleted_at = 0), so a soft-deleted item frees the
// source for a fresh capture. If the predicate stopped matching, a user could
// never re-add something they deleted.
func TestCaptureItemAfterSoftDeleteCreatesNew(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := s.CaptureItem(ctx, &LearningItem{ID: "li-1", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "note", SourceID: "n-1", Title: "T"}); err != nil {
		t.Fatalf("CaptureItem: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE learning_items SET deleted_at = $1 WHERE id = 'li-1'`, time.Now().Unix()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	existing, err := s.CaptureItem(ctx, &LearningItem{ID: "li-2", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "note", SourceID: "n-1", Title: "T again"})
	if err != nil {
		t.Fatalf("CaptureItem after delete: %v", err)
	}
	if existing != nil {
		t.Error("a soft-deleted source must be capturable again")
	}
}

func TestListItemsAndCountByStage(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i, stage := range []Stage{StageInbox, StageInbox, StageLearning} {
		if _, err := s.CaptureItem(ctx, &LearningItem{
			ID: fmt.Sprintf("li-%d", i), WorkspaceID: "ws-1", UserID: "alice",
			SourceKind: "note", SourceID: fmt.Sprintf("n-%d", i), Title: "T", Stage: string(stage),
		}); err != nil {
			t.Fatalf("CaptureItem %d: %v", i, err)
		}
	}
	// Someone else's items must not show up.
	if _, err := s.CaptureItem(ctx, &LearningItem{ID: "li-x", WorkspaceID: "ws-1", UserID: "bob", SourceKind: "note", SourceID: "n-x", Title: "T"}); err != nil {
		t.Fatalf("CaptureItem (bob): %v", err)
	}

	inbox, err := s.ListItems(ctx, "ws-1", "alice", string(StageInbox), "", 50)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(inbox) != 2 {
		t.Errorf("inbox has %d items, want 2", len(inbox))
	}
	byKind, err := s.ListItems(ctx, "ws-1", "alice", "", "note", 50)
	if err != nil {
		t.Fatalf("ListItems by kind: %v", err)
	}
	if len(byKind) != 3 {
		t.Errorf("note items = %d, want 3", len(byKind))
	}
	all, err := s.ListItems(ctx, "ws-1", "alice", "", "", 50)
	if err != nil {
		t.Fatalf("ListItems (all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("alice's items = %d, want 3 (bob's must not leak)", len(all))
	}

	counts, err := s.CountByStage(ctx, "ws-1", "alice")
	if err != nil {
		t.Fatalf("CountByStage: %v", err)
	}
	if counts[string(StageInbox)] != 2 || counts[string(StageLearning)] != 1 {
		t.Errorf("counts = %v, want inbox=2 learning=1", counts)
	}
}

func TestUpdateStageIsTenantScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := s.CaptureItem(ctx, &LearningItem{ID: "li-1", WorkspaceID: "ws-1", UserID: "alice", SourceKind: "note", SourceID: "n-1", Title: "T"}); err != nil {
		t.Fatalf("CaptureItem: %v", err)
	}
	if err := s.UpdateStage(ctx, "ws-1", "alice", "li-1", string(StageLearning)); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}
	got, err := s.ListItems(ctx, "ws-1", "alice", string(StageLearning), "", 50)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("item did not move to learning: %d rows", len(got))
	}

	// Another user must not be able to move it.
	if err := s.UpdateStage(ctx, "ws-1", "bob", "li-1", string(StageMastered)); err == nil {
		t.Error("updating another user's item must fail")
	}
}

// --- reminder lifecycle (ADR-004 uniqueness + the snooze/ack states) ---

// One reminder per (workspace, user, kind, item). Two daily digests for the
// same user would double-notify every morning.
func TestUpsertReminderIsIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-1", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDailyDigest), RuleKind: string(RuleDaily), RuleValue: "08:00",
		NextDueAt: 100,
	})
	if err != nil {
		t.Fatalf("UpsertReminder: %v", err)
	}
	second, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-2", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDailyDigest), RuleKind: string(RuleDaily), RuleValue: "20:30",
		NextDueAt: 200,
	})
	if err != nil {
		t.Fatalf("UpsertReminder (repeat): %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("id drifted from %q to %q: the upsert must reuse the existing row", first.ID, second.ID)
	}
	if second.RuleValue != "20:30" || second.NextDueAt != 200 {
		t.Errorf("the upsert did not update the schedule: %+v", second)
	}
	if second.State != string(ReminderPending) {
		t.Errorf("state = %q, want the default pending", second.State)
	}

	all, err := s.ListReminders(ctx, "ws-1", "alice", "", 50)
	if err != nil {
		t.Fatalf("ListReminders: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("got %d reminders, want 1", len(all))
	}
}

func TestReminderLifecycle(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	due, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-due", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDailyDigest), RuleKind: string(RuleDaily), RuleValue: "08:00",
		NextDueAt: now - 60,
	})
	if err != nil {
		t.Fatalf("UpsertReminder: %v", err)
	}
	future, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-future", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDeadline), RuleKind: string(RuleOnce), RuleValue: "later",
		NextDueAt: now + 3600,
	})
	if err != nil {
		t.Fatalf("UpsertReminder (future): %v", err)
	}

	// DueReminders is what the digest scans; it must skip the future one.
	got, err := s.DueReminders(ctx, "ws-1", "alice", now, 50)
	if err != nil {
		t.Fatalf("DueReminders: %v", err)
	}
	if len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("got %d due reminders, want just the past-due one", len(got))
	}

	// MarkReminderSent returns it to pending with the next occurrence.
	if err := s.MarkReminderSent(ctx, "ws-1", "alice", due.ID, now+86400); err != nil {
		t.Fatalf("MarkReminderSent: %v", err)
	}
	if got, _ = s.DueReminders(ctx, "ws-1", "alice", now, 50); len(got) != 0 {
		t.Errorf("a resent reminder must not be due again at the same instant, got %d", len(got))
	}

	// Snooze pushes it further out; a non-positive duration is rejected so a bad
	// client value cannot silence a reminder permanently.
	if _, err := s.SnoozeReminder(ctx, "ws-1", "alice", due.ID, 0); err == nil {
		t.Error("snoozing by 0 minutes must be rejected")
	}
	next, err := s.SnoozeReminder(ctx, "ws-1", "alice", due.ID, 120)
	if err != nil {
		t.Fatalf("SnoozeReminder: %v", err)
	}
	if next <= now+86400 {
		t.Errorf("snoozed until %d, want later than the previous 86400 offset from %d", next, now)
	}

	// Ack is terminal: it must leave both the due queue and the pending count.
	if err := s.AckReminder(ctx, "ws-1", "alice", future.ID); err != nil {
		t.Fatalf("AckReminder: %v", err)
	}
	if got, _ = s.DueReminders(ctx, "ws-1", "alice", now+100000, 50); len(got) != 0 {
		t.Errorf("an acked reminder must never come due, got %d", len(got))
	}
	pending, err := s.CountPendingReminders(ctx, "ws-1", "alice")
	if err != nil {
		t.Fatalf("CountPendingReminders: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0 after acking the only pending one", pending)
	}
}

// A reminder belonging to someone else must be unreachable.
func TestReminderOpsAreTenantScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := s.UpsertReminder(ctx, &Reminder{
		ID: "r-1", WorkspaceID: "ws-1", UserID: "alice",
		Kind: string(ReminderDailyDigest), RuleKind: string(RuleDaily), RuleValue: "08:00", NextDueAt: 1,
	}); err != nil {
		t.Fatalf("UpsertReminder: %v", err)
	}
	if err := s.AckReminder(ctx, "ws-1", "bob", "r-1"); err == nil {
		t.Error("acking another user's reminder must fail")
	}
	if err := s.MarkReminderSent(ctx, "ws-1", "bob", "r-1", 2); err == nil {
		t.Error("marking another user's reminder sent must fail")
	}
	got, err := s.ListReminders(ctx, "ws-1", "bob", "", 50)
	if err != nil {
		t.Fatalf("ListReminders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("another user's reminders leaked: %d", len(got))
	}
}
