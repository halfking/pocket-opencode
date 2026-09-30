package executors

// Tests for WorkItemReminderExecutor (P4).
//
// The executor's three promises are each pinned here with a fake client, so
// none of them needs a database: once-per-reminder-point, quiet-hours deferral
// instead of dropping, and one bad work item not aborting the batch.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// fakeWorkItemStore records what the executor asked for.
type fakeWorkItemStore struct {
	due       []task.Task
	dueErr    error
	events    []task.WorkItemEvent
	appendErr error
	cleared   map[string]int64
	parts     map[string][]task.Participant
	listErr   error
}

func newFakeStore() *fakeWorkItemStore {
	return &fakeWorkItemStore{cleared: map[string]int64{}, parts: map[string][]task.Participant{}}
}

func (f *fakeWorkItemStore) DueTaskReminders(_ context.Context, _ string, _ int64, _ int) ([]task.Task, error) {
	return f.due, f.dueErr
}

func (f *fakeWorkItemStore) ClearTaskRemindAt(_ context.Context, id, _ string, next int64) error {
	f.cleared[id] = next
	return nil
}

func (f *fakeWorkItemStore) ListParticipants(_ context.Context, id, _ string) ([]task.Participant, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if p, ok := f.parts[id]; ok {
		return p, nil
	}
	return nil, nil
}

func (f *fakeWorkItemStore) AppendEvent(_ context.Context, ev task.WorkItemEvent) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.events = append(f.events, ev)
	return nil
}

// fakeWorkNotifier records dispatches. Named distinctly from the flashcard
// test's fakeNotifier — same package, so the names must not collide.
type fakeWorkNotifier struct {
	events []notifycenter.Event
	err    error
}

func (f *fakeWorkNotifier) Dispatch(_ context.Context, ev notifycenter.Event) (*notifycenter.DispatchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.events = append(f.events, ev)
	return &notifycenter.DispatchResult{}, nil
}

func schedTask() *scheduledtask.Task {
	return &scheduledtask.Task{ID: "sched-1", WorkspaceID: "ws-1", Kind: scheduledtask.KindWorkItemReminder}
}

// recent returns a remind_at `ago` seconds before now, so tests do not have to
// be rewritten every time the staleness bound changes.
func recent(ago int64) int64 { return time.Now().Unix() - ago }

func decode(t *testing.T, res *scheduledtask.Result) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}

// num reads an integer out of the decoded result. JSON numbers come back as
// float64, so comparing them to an int literal with == is always false — a
// comparison bug that silently reads as "the executor produced the wrong
// count" and sends you hunting in the wrong file.
func num(t *testing.T, out map[string]any, key string) int {
	t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		t.Fatalf("result[%q] = %v (%T), want a number", key, out[key], out[key])
	}
	return int(v)
}

func TestWorkItemReminderFiresDueReminder(t *testing.T) {
	store := newFakeStore()
	fireAt := recent(60)
	store.due = []task.Task{{ID: "t-1", Title: "Ship P3", OwnerID: "alice", RemindAt: fireAt}}
	store.parts["t-1"] = []task.Participant{{UserID: "alice", Role: task.RoleOwner}, {UserID: "bob", Role: task.RoleAssignee}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(store.events) != 1 {
		t.Fatalf("expected 1 reminded event, got %d", len(store.events))
	}
	ev := store.events[0]
	if ev.EventType != task.EventReminded {
		t.Errorf("event type = %q, want %q", ev.EventType, task.EventReminded)
	}
	// The idempotency key must be derived from remind_at: this is what makes a
	// repeated tick a no-op via the events primary key.
	if want := task.ReminderEventID(fireAt); ev.EventID != want {
		t.Errorf("event id = %q, want %q", ev.EventID, want)
	}
	if ev.ActorUserID != "system" {
		t.Errorf("actor = %q, want \"system\" (the hub, not a person)", ev.ActorUserID)
	}
	// §4.2: reminded notifies the owner only.
	if len(notif.events) != 1 || notif.events[0].UserID != "alice" {
		t.Fatalf("expected exactly one notification to the owner alice, got %+v", notif.events)
	}
	if notif.events[0].Kind != task.NotifyReminder {
		t.Errorf("notification kind = %q, want %q", notif.events[0].Kind, task.NotifyReminder)
	}
	// The reminder point is retired, not left to re-fire.
	if got := store.cleared["t-1"]; got != 0 {
		t.Errorf("remind_at after firing = %d, want 0", got)
	}
	out := decode(t, res)
	if num(t, out, "fired") != 1 || num(t, out, "notified") != 1 {
		t.Errorf("result = %+v, want fired=1 notified=1", out)
	}
}

// windowAroundNow builds a quiet window that definitely contains the current
// minute-of-day. Deriving it from `now` keeps the deferral path deterministic
// no matter what hour the suite runs at — hard-coding 23:50 would only be
// inside the window half the day, and a test that passes only at night is
// worse than no test.
func windowAroundNow() (task.QuietWindow, int) {
	nowMin := int((time.Now().Unix() / 60) % 1440)
	return task.QuietWindow{
		StartMin: (nowMin - 30 + 1440) % 1440,
		EndMin:   (nowMin + 30) % 1440,
	}, nowMin
}

// A reminder inside quiet hours is moved, not fired and not dropped.
func TestWorkItemReminderDefersInsideQuietHours(t *testing.T) {
	store := newFakeStore()
	window, _ := windowAroundNow()
	fireAt := recent(60) // one minute ago → inside the ±30m window
	store.due = []task.Task{{ID: "t-1", Title: "Late", OwnerID: "alice", RemindAt: fireAt}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	ex.SetQuietWindow(window)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(store.events) != 0 {
		t.Errorf("a deferred reminder must not fire, got %d events", len(store.events))
	}
	if len(notif.events) != 0 {
		t.Errorf("a deferred reminder must not notify, got %d", len(notif.events))
	}
	next, ok := store.cleared["t-1"]
	if !ok {
		t.Fatal("the deferral did not rewrite remind_at; the reminder would fire again unchanged")
	}
	if next <= fireAt {
		t.Errorf("deferred time %d is not after the original %d", next, fireAt)
	}
	// The deferral must land on the window's end. Combined with
	// next > fireAt, the minute-of-day check is what proves it is the *next*
	// occurrence rather than one that already passed.
	if got := int((next / 60) % 1440); got != window.EndMin {
		t.Errorf("deferred to minute %d, want the window end %d", got, window.EndMin)
	}
	out := decode(t, res)
	if num(t, out, "deferred") != 1 || num(t, out, "fired") != 0 {
		t.Errorf("result = %+v, want deferred=1 fired=0", out)
	}
}

// Quiet hours disabled means the reminder fires at its own time.
func TestWorkItemReminderFiresWhenQuietHoursDisabled(t *testing.T) {
	store := newFakeStore()
	window, _ := windowAroundNow()
	store.due = []task.Task{{ID: "t-1", Title: "Late", OwnerID: "alice", RemindAt: recent(60)}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	ex.SetQuietWindow(task.QuietWindow{}) // not configured
	_ = window

	if _, err := ex.Execute(context.Background(), schedTask()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(store.events) != 1 {
		t.Errorf("with quiet hours off the reminder must fire, got %d events", len(store.events))
	}
}

// An empty tick must be silent: no dispatch, no error.
func TestWorkItemReminderSilentWhenNothingIsDue(t *testing.T) {
	store := newFakeStore()
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(notif.events) != 0 {
		t.Errorf("an empty tick must not notify, got %d", len(notif.events))
	}
	out := decode(t, res)
	if num(t, out, "due") != 0 || num(t, out, "fired") != 0 {
		t.Errorf("result = %+v, want all zeros", out)
	}
}

// One failing work item must not take the rest of the batch down.
func TestWorkItemReminderBatchSurvivesOneFailure(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{
		{ID: "bad", Title: "Bad", OwnerID: "alice", RemindAt: recent(60)},
		{ID: "good", Title: "Good", OwnerID: "alice", RemindAt: recent(30)},
	}
	store.appendErr = errors.New("db down")
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute returned an error for one bad row: %v", err)
	}
	out := decode(t, res)
	if num(t, out, "due") != 2 || num(t, out, "fired") != 0 {
		t.Errorf("result = %+v, want due=2 fired=0", out)
	}
	// Nothing was retried-cleared, so the next tick picks both up again.
	if len(store.cleared) != 0 {
		t.Errorf("a failed reminder must keep its remind_at, but %v was cleared", store.cleared)
	}
}

// A dispatch failure is logged, not propagated: the reminder already fired, so
// failing the run would make the scheduler retry a write that succeeded.
func TestWorkItemReminderNotificationFailureIsSwallowed(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Ship", OwnerID: "alice", RemindAt: recent(60)}}
	notif := &fakeWorkNotifier{err: errors.New("channel down")}
	ex := NewWorkItemReminderExecutor(store, notif)

	if _, err := ex.Execute(context.Background(), schedTask()); err != nil {
		t.Fatalf("Execute propagated a notification failure: %v", err)
	}
	if len(store.events) != 1 {
		t.Errorf("the event must still be written when the push fails, got %d", len(store.events))
	}
}

// A nil notification client (remote-only / notifycenter not ready) must not
// panic and must still fire the event.
func TestWorkItemReminderToleratesNilNotifier(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Ship", OwnerID: "alice", RemindAt: recent(60)}}
	ex := NewWorkItemReminderExecutor(store, nil)

	if _, err := ex.Execute(context.Background(), schedTask()); err != nil {
		t.Fatalf("Execute with a nil notifier: %v", err)
	}
	if len(store.events) != 1 {
		t.Errorf("event not written with a nil notifier")
	}
}

func TestWorkItemReminderRejectsUnconfiguredClient(t *testing.T) {
	ex := NewWorkItemReminderExecutor(nil, nil)
	if _, err := ex.Execute(context.Background(), schedTask()); err == nil {
		t.Error("a nil client must fail loudly rather than silently doing nothing")
	}
}

// The kind must be registered, or POST /api/scheduled-tasks rejects it and the
// reminder never runs — the same class of bug that hid flashcard_review and
// learning_digest from AllKinds().
func TestWorkItemReminderKindIsRegistered(t *testing.T) {
	found := false
	for _, k := range scheduledtask.AllKinds() {
		if k == scheduledtask.KindWorkItemReminder {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%q is missing from AllKinds(); the API would reject this kind", scheduledtask.KindWorkItemReminder)
	}
	if got := (*WorkItemReminderExecutor)(nil).Kind(); got != scheduledtask.KindWorkItemReminder {
		t.Errorf("Kind() = %q, want %q", got, scheduledtask.KindWorkItemReminder)
	}
}

// A failed scan is a real error: the caller must not see a successful run.
func TestWorkItemReminderPropagatesScanFailure(t *testing.T) {
	store := newFakeStore()
	store.dueErr = errors.New("connection refused")
	ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
	if _, err := ex.Execute(context.Background(), schedTask()); err == nil {
		t.Error("a failed DueTaskReminders scan must fail the run")
	}
}

// A reminder from days ago is retired without a push. This is what bounds the
// notification volume when a deployment turns every past-due row into a
// backlog: without it, ten thousand stale rows become ten thousand pushes.
func TestWorkItemReminderRetiresStaleReminders(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{
		{ID: "ancient", Title: "Old", OwnerID: "alice", RemindAt: recent(72 * 3600)},
		{ID: "fresh", Title: "New", OwnerID: "alice", RemindAt: recent(60)},
	}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The stale one produces no event and no notification…
	if len(store.events) != 1 || store.events[0].TaskID != "fresh" {
		t.Fatalf("only the fresh reminder should fire, got %+v", store.events)
	}
	if len(notif.events) != 1 {
		t.Errorf("only the fresh reminder should notify, got %d", len(notif.events))
	}
	// …but it is still retired, so it does not resurface on every tick.
	if got := store.cleared["ancient"]; got != 0 {
		t.Errorf("stale remind_at = %d, want 0 (retired)", got)
	}
	out := decode(t, res)
	if num(t, out, "stale") != 1 || num(t, out, "fired") != 1 {
		t.Errorf("result = %+v, want stale=1 fired=1", out)
	}
}

// The staleness bound is checked before quiet hours: an ancient reminder must
// be retired, not deferred to tomorrow morning.
func TestWorkItemReminderStaleBeatsQuietHours(t *testing.T) {
	store := newFakeStore()
	window, _ := windowAroundNow()
	store.due = []task.Task{{ID: "ancient", Title: "Old", OwnerID: "alice", RemindAt: recent(72 * 3600)}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	ex.SetQuietWindow(window)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if num(t, decode(t, res), "stale") != 1 {
		t.Errorf("a stale reminder inside quiet hours must be retired, not deferred")
	}
	if num(t, decode(t, res), "deferred") != 0 {
		t.Errorf("a stale reminder must not be deferred to the next quiet window")
	}
	if len(notif.events) != 0 {
		t.Errorf("a stale reminder must not notify")
	}
}

// Setting the bound to 0 disables it, which is how a deployment that genuinely
// wants every past-due row to fire can opt in.
func TestWorkItemReminderStaleBoundIsConfigurable(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{{ID: "ancient", Title: "Old", OwnerID: "alice", RemindAt: recent(72 * 3600)}}
	ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
	ex.SetStaleAfter(0)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if num(t, decode(t, res), "stale") != 0 {
		t.Error("with the bound disabled a stale reminder must still fire")
	}
	if len(store.events) != 1 {
		t.Errorf("with the bound disabled the reminder should fire, got %d events", len(store.events))
	}
}
