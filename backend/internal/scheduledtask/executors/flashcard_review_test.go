package executors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// fakeDueStore is a CountDueCardsClient stub that lets each test pin the
// count return + optional error. The store-side interface is intentionally
// narrow (just CountDueCards) so unit tests don't need a PG instance —
// flashcards/store_test.go already skips when POCKET_TEST_PG_DSN is unset.
type fakeDueStore struct {
	count int
	err   error
}

func (f *fakeDueStore) CountDueCards(_ context.Context, _ string, _ int64) (int, error) {
	return f.count, f.err
}

// fakeNotifier captures the dispatch event for assertions. Mirrors the
// executorEmailNotifier pattern in server_email_pipeline.go but stripped
// to just what the executor uses.
type fakeNotifier struct {
	calls   int
	lastEv  notifycenter.Event
	err     error
	enabled bool
}

func (f *fakeNotifier) Dispatch(_ context.Context, ev notifycenter.Event) (*notifycenter.DispatchResult, error) {
	if !f.enabled {
		return nil, errors.New("notifier disabled")
	}
	f.calls++
	f.lastEv = ev
	return &notifycenter.DispatchResult{NotificationID: "ntf-test"}, f.err
}

func flashcardTask(userID string) *scheduledtask.Task {
	return &scheduledtask.Task{
		ID:          "sched-fc-1",
		WorkspaceID: "ws-fc",
		UserID:      "ws-owner", // owner = the user that created the task
		Name:        "daily flashcard review",
		Payload:     json.RawMessage(`{"user_id":"` + userID + `","due_window":"today"}`),
	}
}

// Case A: due count = 0 → executor returns nil + skipped notification.
func TestFlashcardReviewExecutor_ZeroDueNoNotify(t *testing.T) {
	store := &fakeDueStore{count: 0}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)

	res, err := e.Execute(context.Background(), flashcardTask("user-a"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if notif.calls != 0 {
		t.Errorf("expected 0 notifications, got %d", notif.calls)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if due, _ := out["dueCount"].(float64); due != 0 {
		t.Errorf("dueCount = %v, want 0", out["dueCount"])
	}
	if notified, _ := out["notified"].(bool); notified {
		t.Errorf("notified should be false, got %v", out["notified"])
	}
}

// Case B: due count > 0 → executor dispatches exactly one notification
// with stable Kind + a body that includes the count.
func TestFlashcardReviewExecutor_DueNotifies(t *testing.T) {
	store := &fakeDueStore{count: 5}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)

	res, err := e.Execute(context.Background(), flashcardTask("user-b"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if notif.calls != 1 {
		t.Fatalf("expected 1 notification, got %d", notif.calls)
	}
	ev := notif.lastEv
	if ev.UserID != "user-b" {
		t.Errorf("UserID = %q, want user-b", ev.UserID)
	}
	if ev.WorkspaceID != "ws-fc" {
		t.Errorf("WorkspaceID = %q, want ws-fc (inherited from task)", ev.WorkspaceID)
	}
	if ev.Source != "flashcards" {
		t.Errorf("Source = %q, want flashcards", ev.Source)
	}
	if ev.Kind != "flashcards.review.due" {
		t.Errorf("Kind = %q, want flashcards.review.due (frontend i18n key)", ev.Kind)
	}
	if !strings.Contains(ev.Title, "5") {
		t.Errorf("Title should include count 5, got %q", ev.Title)
	}
	if !strings.Contains(ev.Body, "5") {
		t.Errorf("Body should include count 5, got %q", ev.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if due, _ := out["dueCount"].(float64); due != 5 {
		t.Errorf("dueCount = %v, want 5", out["dueCount"])
	}
	if notified, _ := out["notified"].(bool); !notified {
		t.Errorf("notified should be true, got %v", out["notified"])
	}
}

// Case C: store returns an error → executor returns the error and no
// notification is dispatched (so the scheduler sees a failed run, not a
// silent success).
func TestFlashcardReviewExecutor_StoreErrorNoNotify(t *testing.T) {
	dbErr := errors.New("connection refused")
	store := &fakeDueStore{err: dbErr}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)

	_, err := e.Execute(context.Background(), flashcardTask("user-c"))
	if err == nil {
		t.Fatal("expected error from store failure")
	}
	if !errors.Is(err, dbErr) && !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should wrap store error, got %v", err)
	}
	if notif.calls != 0 {
		t.Errorf("expected 0 notifications on store error, got %d", notif.calls)
	}
}

// Bonus — SetNotifier wires a notifier that was nil at construction time
// (mirrors the late-binding seam in cmd/pocketd/main.go).
func TestFlashcardReviewExecutor_SetNotifierLateBinding(t *testing.T) {
	store := &fakeDueStore{count: 2}
	e := NewFlashcardReviewExecutor(store, nil) // mirror main.go's first call
	if e.notif != nil {
		t.Fatal("expected nil notif after construction")
	}

	notif := &fakeNotifier{enabled: true}
	e.SetNotifier(notif)

	if _, err := e.Execute(context.Background(), flashcardTask("user-d")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if notif.calls != 1 {
		t.Fatalf("expected 1 notification after SetNotifier, got %d", notif.calls)
	}
}

// Payload validation: due_window other than "today" must fail in v1 —
// this gates future expansion (next_24h / this_week) so silent fallback
// is impossible.
func TestFlashcardReviewExecutor_RejectsUnknownDueWindow(t *testing.T) {
	store := &fakeDueStore{count: 0}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)
	task := &scheduledtask.Task{
		WorkspaceID: "ws-fc",
		Payload:     json.RawMessage(`{"user_id":"user-x","due_window":"this_week"}`),
	}
	_, err := e.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "due_window") {
		t.Fatalf("expected due_window rejection, got %v", err)
	}
}

// Payload validation: missing user_id (and no task owner fallback) → error.
func TestFlashcardReviewExecutor_RequiresUserID(t *testing.T) {
	store := &fakeDueStore{count: 0}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)
	task := &scheduledtask.Task{Payload: json.RawMessage(`{}`)}
	_, err := e.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("expected user_id rejection, got %v", err)
	}
}

// Payload validation: invalid JSON → error.
func TestFlashcardReviewExecutor_RejectsBadPayload(t *testing.T) {
	store := &fakeDueStore{count: 0}
	notif := &fakeNotifier{enabled: true}
	e := NewFlashcardReviewExecutor(store, notif)
	task := &scheduledtask.Task{Payload: json.RawMessage(`not-json`)}
	_, err := e.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected JSON decode error")
	}
}
