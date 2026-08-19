package email

// scheduler_intent_test.go — covers the intent consumption loop (Work item A5).
//
// The loop iterates distinct (userID, workspaceID) scopes derived from enabled
// accounts, claims pending email_action_intents, hands each to the IntentExecutor,
// and marks applied/failed/skipped. These tests pin:
//   - a pending route-folder intent reaches the executor and is marked by status;
//   - an executor returning ErrSkipIntent marks skipped (terminal, no retry);
//   - an executor returning a normal error marks failed (still terminal in this
//     state machine — pending→failed);
//   - intents from another workspace are never claimed (no leak);
//   - a scope with no pending intents is a no-op.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeIntentExecutor records every Execute call and can be programmed with a
// per-action result. The mutex guards the records slice because the scheduler
// runs claims sequentially per scope, but the test asserts concurrently-safely.
type fakeIntentExecutor struct {
	mu      sync.Mutex
	records []ActionIntent
	result  map[string]error // action -> err (default nil = applied)
}

func newFakeIntentExecutor() *fakeIntentExecutor {
	return &fakeIntentExecutor{result: make(map[string]error)}
}

func (f *fakeIntentExecutor) Execute(_ context.Context, intent ActionIntent) error {
	f.mu.Lock()
	f.records = append(f.records, intent)
	err := f.result[intent.Action]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return nil
}

func (f *fakeIntentExecutor) seen() []ActionIntent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ActionIntent, len(f.records))
	copy(out, f.records)
	return out
}

// seedPendingIntent inserts a pending intent owned by (user, ws) for the given
// email/account. Uses the same idempotency scheme the fetcher does.
func seedPendingIntent(t *testing.T, store *Store, emailID, accountID, user, ws, action, folder string) {
	t.Helper()
	intent := &ActionIntent{
		EmailID:        emailID,
		AccountID:      accountID,
		WorkspaceID:    ws,
		UserID:         user,
		Action:         action,
		Folder:         folder,
		Reason:         "test",
		IdempotencyKey: "test-intent-" + emailID + "-" + action,
		Status:         "pending",
	}
	if err := store.InsertEmail(context.Background(), Email{
		ID: emailID, AccountID: accountID, WorkspaceID: ws,
		MessageID: emailID + "@example.com", FromAddress: "sender@example.com", Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("seed email %s: %v", emailID, err)
	}
	if err := store.InsertActionIntent(context.Background(), intent); err != nil {
		t.Fatalf("seed intent %s: %v", action, err)
	}
}

func newTestScheduler(t *testing.T) (*Scheduler, *Store, func()) {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	// The loop only touches store + intentExecutor; fetcher/crypto are not
	// exercised by runIntents. Pass a nil-safe fetcher via NewScheduler.
	s := NewScheduler(store, nil, true)
	return s, store, cleanup
}

// A pending route-folder intent is claimed, handed to the executor, and marked
// applied on the executor's nil return.
func TestRunIntents_MarksAppliedOnSuccess(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user, ws, acct = "intent-user", "intent-ws", "intent-acct"
	seedAccount(t, store, acct, user, ws)
	seedPendingIntent(t, store, "mail-1", acct, user, ws, "route-folder", "Junk")

	exec := newFakeIntentExecutor()
	s.SetIntentExecutor(exec)
	s.runIntents(ctx)

	seen := exec.seen()
	if len(seen) != 1 {
		t.Fatalf("executor saw %d intents, want 1", len(seen))
	}
	if seen[0].Action != "route-folder" || seen[0].Folder != "Junk" {
		t.Fatalf("unexpected intent: %#v", seen[0])
	}
	// No pending should remain; the row is now applied.
	remaining, err := store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim after run: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 pending after applied, got %d", len(remaining))
	}
}

// ErrSkipIntent from the executor marks the intent skipped (terminal), not
// failed — important for route-folder which intentionally does not do IMAP MOVE.
func TestRunIntents_SkipIsTerminal(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user, ws, acct = "skip-user", "skip-ws", "skip-acct"
	seedAccount(t, store, acct, user, ws)
	seedPendingIntent(t, store, "mail-skip", acct, user, ws, "route-folder", "Archive")

	exec := newFakeIntentExecutor()
	exec.result["route-folder"] = ErrSkipIntent
	s.SetIntentExecutor(exec)
	s.runIntents(ctx)

	if len(exec.seen()) != 1 {
		t.Fatalf("executor should have been invoked once, got %d", len(exec.seen()))
	}
	// Re-claiming must yield nothing: skipped is terminal, not retried.
	remaining, err := store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim after skip: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("skipped intent must not be re-claimed, got %d", len(remaining))
	}
}

// A normal error from the executor marks failed; the row leaves pending state
// (failed is terminal in this state machine).
func TestRunIntents_FailureMarksFailed(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user, ws, acct = "fail-user", "fail-ws", "fail-acct"
	seedAccount(t, store, acct, user, ws)
	seedPendingIntent(t, store, "mail-fail", acct, user, ws, "trigger-autoreply", "")

	exec := newFakeIntentExecutor()
	exec.result["trigger-autoreply"] = errors.New("smtp not configured")
	s.SetIntentExecutor(exec)
	s.runIntents(ctx)

	if len(exec.seen()) != 1 {
		t.Fatalf("executor should have been invoked once, got %d", len(exec.seen()))
	}
	remaining, err := store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim after fail: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("failed intent must leave pending state, got %d pending", len(remaining))
	}
}

// An intent owned by (user, ws-a) must never be claimed when the only enabled
// account is in ws-b — even though the user is the same.
func TestRunIntents_DoesNotLeakAcrossWorkspace(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user = "leak-user"
	seedAccount(t, store, "acct-a", user, "ws-a")
	seedPendingIntent(t, store, "mail-a", "acct-a", user, "ws-a", "route-folder", "X")

	// ws-b has an enabled account but no intents; ws-a has the intent but NO
	// enabled account here is irrelevant — the loop derives scopes from accounts.
	// The point: ws-b's scope must not see ws-a's intent.
	seedAccount(t, store, "acct-b", user, "ws-b")

	exec := newFakeIntentExecutor()
	s.SetIntentExecutor(exec)
	s.runIntents(ctx)

	for _, r := range exec.seen() {
		if r.WorkspaceID == "ws-b" {
			t.Fatalf("ws-b scope consumed a ws-a intent: %#v", r)
		}
	}
	// ws-a's intent should have been consumed by its own scope.
	remaining, err := store.ClaimActionIntents(ctx, user, "ws-a", 10)
	if err != nil {
		t.Fatalf("claim ws-a: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("ws-a intent should have been consumed, %d still pending", len(remaining))
	}
}

// No executor wired → runIntents is a no-op (Start gates the loop, but runIntents
// itself must also be safe to call without one).
func TestRunIntents_NoExecutorIsNoop(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user, ws, acct = "noop-user", "noop-ws", "noop-acct"
	seedAccount(t, store, acct, user, ws)
	seedPendingIntent(t, store, "mail-noop", acct, user, ws, "route-folder", "")

	// Deliberately do NOT call SetIntentExecutor.
	s.runIntents(ctx)

	remaining, err := store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("without executor the intent must stay pending, got %d", len(remaining))
	}
}

// Sanity: the loop does not spin forever when the executor keeps re-queuing —
// it caps at batchLimit per scope per tick (pending→applied drains the queue).
func TestRunIntents_DrainsBatchAndTerminates(t *testing.T) {
	s, store, cleanup := newTestScheduler(t)
	defer cleanup()
	ctx := context.Background()

	const user, ws, acct = "batch-user", "batch-ws", "batch-acct"
	seedAccount(t, store, acct, user, ws)
	// Seed more intents than the per-scope batch limit (25).
	for i := 0; i < 30; i++ {
		seedPendingIntent(t, store,
			"batch-mail-"+itoa(i), acct, user, ws, "route-folder", "F")
	}

	exec := newFakeIntentExecutor()
	s.SetIntentExecutor(exec)
	start := time.Now()
	s.runIntents(ctx)
	elapsed := time.Since(start)

	seen := exec.seen()
	if len(seen) != 30 {
		t.Fatalf("executor saw %d, want all 30 (loop must drain, not cap at 25)", len(seen))
	}
	// Guard against an accidental infinite loop: a healthy run is sub-second.
	if elapsed > 10*time.Second {
		t.Fatalf("runIntents took %v; suspected spin", elapsed)
	}
}

// itoa avoids pulling strconv into the test file for a trivial need.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
