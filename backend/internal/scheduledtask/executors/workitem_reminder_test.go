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
	"strconv"
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
	// askedWS is the workspace every scan was issued against, and clearedWS /
	// listedWS record the workspace each per-item write used. They are the only
	// way to see which tenant the executor actually touched.
	askedWS    string
	scanCall   int
	askedLimit int
	clearedWS  map[string]string
	listedWS   map[string]string
}

func newFakeStore() *fakeWorkItemStore {
	return &fakeWorkItemStore{
		cleared:   map[string]int64{},
		parts:     map[string][]task.Participant{},
		clearedWS: map[string]string{},
		listedWS:  map[string]string{},
	}
}

func (f *fakeWorkItemStore) DueTaskReminders(_ context.Context, wsID string, _ int64, limit int) ([]task.Task, error) {
	f.askedWS = wsID
	f.askedLimit = limit
	f.scanCall++
	return f.due, f.dueErr
}

func (f *fakeWorkItemStore) ClearTaskRemindAt(_ context.Context, id, wsID string, next int64) error {
	f.cleared[id] = next
	f.clearedWS[id] = wsID
	return nil
}

func (f *fakeWorkItemStore) ListParticipants(_ context.Context, id, wsID string) ([]task.Participant, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.listedWS[id] = wsID
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
	pinServerZone(t)
	store := newFakeStore()
	fireAt := recent(60)
	store.due = []task.Task{{ID: "t-1", Title: "Ship P3", OwnerID: "alice", RemindAt: fireAt}}
	store.parts["t-1"] = []task.Participant{{UserID: "alice", Role: task.RoleOwner}, {UserID: "bob", Role: task.RoleAssignee}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	quietHoursOff(ex)

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
//
// The minute must be read in **time.Local**, not from the raw epoch. That was
// the second half of the suite's time-of-day coupling: the window was built
// from `time.Now().Unix()/60 % 1440` (a UTC minute-of-day) while
// QuietWindow.Defer resolves the owner's zone via time.Local. The two only
// agree when the server runs on UTC — so any pinned non-UTC zone shifted the
// window by the zone offset and the deferral fixtures missed it.
func windowAroundNow() (task.QuietWindow, int) {
	now := time.Now().In(time.Local)
	nowMin := now.Hour()*60 + now.Minute()
	return task.QuietWindow{
		StartMin: (nowMin - 30 + 1440) % 1440,
		EndMin:   (nowMin + 30) % 1440,
	}, nowMin
}

// pinServerZone fixes the server's local zone for the duration of a test, at a
// point in the day that is guaranteed to be **outside** the default
// do-not-disturb window (22:30→07:30).
//
// Why "a point in the day" and not plain UTC — this helper used to pin
// `time.Local = time.UTC`, and the fixtures below assert that a due reminder
// *fires*. That assertion is only true when the local minute-of-day is outside
// 22:30–07:30, so pinning to UTC made the suite time-of-day dependent: on a
// UTC+8 machine running at 11:59 local, UTC is 03:59 — inside the window —
// and 7 cases failed with `remind_at was cleared` / `event not written`.
//
// The bug this file's comment originally described (minute-of-day computed
// against the UTC day boundary) is real, but pinning to UTC only trades it for
// a different clock assumption. What the fixtures actually need is a
// deterministic *and* representative local time, so we offset the zone from
// the current instant to land near local noon. The non-UTC behaviour itself is
// covered in workitem_reminder_quiet_test.go, which drives the zone through
// user settings rather than through the machine.
func pinServerZone(t *testing.T) {
	t.Helper()
	orig := time.Local
	// FixedZone's offset is relative to **UTC**, so it has to be derived from the
	// UTC reading of now — not from now.Hour(), which is in the *original* zone.
	// Mixing the two silently lands on the wrong wall clock (measured: local
	// 15:56 / UTC 07:56 with a now.Hour()-derived offset pinned it to 04:00).
	utc := time.Now().UTC()
	offset := 12*3600 - (utc.Hour()*3600 + utc.Minute()*60 + utc.Second())
	time.Local = time.FixedZone("pinned-noon", offset)
	t.Cleanup(func() { time.Local = orig })
}

// quietHoursOff makes a test that is about *firing* independent of the hour it
// runs at.
//
// The default window is 22:30-07:30 and pinServerZone pins time.Local to UTC, so
// a suite that runs between those minutes defers every reminder and fails for
// reasons unrelated to what it asserts — batch survival, a nil notifier, the
// staleness bound. Those cases are not about quiet hours, so they say so
// explicitly instead of inheriting a window from the wall clock. The deferral
// path keeps its own coverage, with a window built by windowAroundNow.
func quietHoursOff(ex *WorkItemReminderExecutor) {
	ex.SetQuietWindow(task.QuietWindow{})
}

// A reminder inside quiet hours is moved, not fired and not dropped.
func TestWorkItemReminderDefersInsideQuietHours(t *testing.T) {
	pinServerZone(t)
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
	//
	// Read the minute in time.Local for the same reason windowAroundNow does:
	// `(next/60)%1440` is a **UTC** minute-of-day, so under any pinned non-UTC
	// zone it disagrees with the window the executor resolved.
	got := time.Unix(next, 0).In(time.Local).Hour()*60 + time.Unix(next, 0).In(time.Local).Minute()
	if got != window.EndMin {
		t.Errorf("deferred to minute %d, want the window end %d", got, window.EndMin)
	}
	out := decode(t, res)
	if num(t, out, "deferred") != 1 || num(t, out, "fired") != 0 {
		t.Errorf("result = %+v, want deferred=1 fired=0", out)
	}
}

// Quiet hours disabled means the reminder fires at its own time.
func TestWorkItemReminderFiresWhenQuietHoursDisabled(t *testing.T) {
	pinServerZone(t)
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
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{
		{ID: "bad", Title: "Bad", OwnerID: "alice", RemindAt: recent(60)},
		{ID: "good", Title: "Good", OwnerID: "alice", RemindAt: recent(30)},
	}
	store.appendErr = errors.New("db down")
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	quietHoursOff(ex)

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
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Ship", OwnerID: "alice", RemindAt: recent(60)}}
	notif := &fakeWorkNotifier{err: errors.New("channel down")}
	ex := NewWorkItemReminderExecutor(store, notif)
	quietHoursOff(ex)

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
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Ship", OwnerID: "alice", RemindAt: recent(60)}}
	ex := NewWorkItemReminderExecutor(store, nil)
	quietHoursOff(ex)

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
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{
		{ID: "ancient", Title: "Old", OwnerID: "alice", RemindAt: recent(72 * 3600)},
		{ID: "fresh", Title: "New", OwnerID: "alice", RemindAt: recent(60)},
	}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)
	quietHoursOff(ex)

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
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "ancient", Title: "Old", OwnerID: "alice", RemindAt: recent(72 * 3600)}}
	ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
	ex.SetStaleAfter(0)
	quietHoursOff(ex)

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

// --- tenancy (B-4) ---

// The payload must not be able to point the executor at another tenant. It
// used to: `wsID := p.WorkspaceID` won over the scheduled task's own workspace,
// so any authenticated user could aim their reminder job at somebody else's
// workspace and have it write events and push notifications over there.
func TestWorkItemReminderIgnoresPayloadWorkspace(t *testing.T) {
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Ship", OwnerID: "alice", WorkspaceID: "ws-1", RemindAt: recent(60)}}
	store.parts["t-1"] = []task.Participant{{UserID: "alice", Role: task.RoleOwner}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	st := schedTask()
	st.Payload = json.RawMessage(`{"workspace_id":"ws-victim","user_id":"mallory","limit":50}`)

	if _, err := ex.Execute(context.Background(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if store.askedWS != "ws-1" {
		t.Errorf("scanned workspace %q, want ws-1 (the scheduled task's own tenant)", store.askedWS)
	}
	for id, ws := range store.clearedWS {
		if ws != "ws-1" {
			t.Errorf("remind_at of %s was cleared in workspace %q, want ws-1", id, ws)
		}
	}
	for id, ws := range store.listedWS {
		if ws != "ws-1" {
			t.Errorf("participants of %s were read in workspace %q, want ws-1", id, ws)
		}
	}
	for _, ev := range store.events {
		if ev.WorkspaceID != "ws-1" {
			t.Errorf("event written in workspace %q, want ws-1", ev.WorkspaceID)
		}
	}
	for _, ev := range notif.events {
		if ev.WorkspaceID != "ws-1" {
			t.Errorf("notification dispatched in workspace %q, want ws-1", ev.WorkspaceID)
		}
	}
	// The payload is still allowed to lower the batch, which is a scheduling
	// knob rather than a permission.
	if store.askedLimit != ex.batch {
		t.Errorf("limit = %d, want the batch cap %d", store.askedLimit, ex.batch)
	}
}

// A payload limit may shrink the tick but never grow it past the batch cap:
// the cap is what stops an outage backlog returning as one huge burst.
func TestWorkItemReminderPayloadLimitCannotExceedBatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int
		wantCap bool
	}{
		{"a client asking for 200", 200, true},
		{"a client asking for the exact batch size", 50, true},
		{"a client asking for less", 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})
			st := schedTask()
			st.Payload = json.RawMessage(`{"limit":` + strconv.Itoa(tc.limit) + `}`)

			if _, err := ex.Execute(context.Background(), st); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if tc.wantCap && store.askedLimit > ex.batch {
				t.Errorf("payload raised the batch to %d, cap is %d", store.askedLimit, ex.batch)
			}
			if !tc.wantCap && store.askedLimit != tc.limit {
				t.Errorf("limit = %d, want the requested %d", store.askedLimit, tc.limit)
			}
		})
	}
}

// A row that claims a different workspace than the job is never written,
// retired or notified — the scan is already scoped, so this can only happen if
// that scoping is ever broken, and then it must fail closed.
func TestWorkItemReminderSkipsRowFromAnotherWorkspace(t *testing.T) {
	pinServerZone(t)
	store := newFakeStore()
	store.due = []task.Task{{ID: "foreign", Title: "Not mine", OwnerID: "victim", WorkspaceID: "ws-victim", RemindAt: recent(60)}}
	notif := &fakeWorkNotifier{}
	ex := NewWorkItemReminderExecutor(store, notif)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(store.events) != 0 {
		t.Errorf("a row outside the job's workspace must not be written, got %+v", store.events)
	}
	if len(store.cleared) != 0 {
		t.Errorf("a row outside the job's workspace must not be retired, got %v", store.cleared)
	}
	if len(notif.events) != 0 {
		t.Errorf("a row outside the job's workspace must not notify")
	}
	out := decode(t, res)
	if num(t, out, "fired") != 0 || num(t, out, "notified") != 0 {
		t.Errorf("result = %+v, want nothing fired", out)
	}
}

// No workspace on the job means no scan: guessing a default tenant would read
// — and write — a workspace the job was never granted.
func TestWorkItemReminderFailsClosedWithoutWorkspace(t *testing.T) {
	store := newFakeStore()
	ex := NewWorkItemReminderExecutor(store, &fakeWorkNotifier{})

	st := schedTask()
	st.WorkspaceID = ""

	if _, err := ex.Execute(context.Background(), st); err == nil {
		t.Fatal("a scheduled task with no workspace must fail, not default to somebody's tenant")
	}
	if store.scanCall != 0 {
		t.Errorf("the store was queried %d times despite the missing workspace", store.scanCall)
	}
}
