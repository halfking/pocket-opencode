package executors

// WorkItemReminderExecutor fires one-shot work-item reminders
// (docs/学习muse/03-架构方案.md §3.3 / §4.2, phase P4).
//
// Until this existed, `tasks.remind_at` was stored and range-validated but
// nothing consumed it: the `reminded` event type and its notification mapping
// had no producer. This is that producer.
//
// Three properties matter more than the plumbing:
//
//  1. **Once per reminder point.** The event id is derived from remind_at
//     (task.ReminderEventID), and work_item_events is keyed on
//     (workspace, task, event_id). A scheduler that ticks every few seconds
//     therefore cannot produce a second notification — the database enforces
//     it, not a check in this file.
//  2. **Quiet hours defer, never drop.** A reminder at 23:50 moves to the end
//     of the quiet window instead of being skipped. remind_at is rewritten in
//     the same step so the next tick sees the new time.
//  3. **Silence when there is nothing to say.** A run that finds no due
//     reminders returns successfully with notified=false rather than
//     dispatching an empty ping.
//
//  4. **Stale reminders are retired, not pushed.** A `remind_at` from three
//     days ago is not a reminder any more, it is noise — and a deployment
//     turns every past-due row into a backlog that would otherwise fire as one
//     notification each. Those rows are retired silently and counted in
//     `stale`, so the volume is bounded by the staleness window rather than by
//     how long the service was down. The work item itself still shows up as
//     overdue in the task list, which is where a stale reminder belongs.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// WorkItemClient is the subset of the task Store the reminder needs.
type WorkItemClient interface {
	DueTaskReminders(ctx context.Context, wsID string, now int64, limit int) ([]task.Task, error)
	ClearTaskRemindAt(ctx context.Context, taskID, wsID string, nextRemindAt int64) error
	ListParticipants(ctx context.Context, taskID, wsID string) ([]task.Participant, error)
	AppendEvent(ctx context.Context, ev task.WorkItemEvent) error
}

// QuietPreferencesLookup resolves one user's do-not-disturb configuration.
//
// It exists because a single server-wide window cannot be right for everyone:
// the minute-of-day a reminder lands on is the *owner's* local time, so a
// window applied in UTC pushes a 23:50 Asia/Shanghai reminder straight
// through. A nil implementation is legitimate — it means "no preferences are
// stored", and every user then falls back to the server's zone.
type QuietPreferencesLookup interface {
	QuietPreferences(ctx context.Context, wsID, userID string) (task.QuietPreferences, bool)
}

// WorkItemReminderExecutor implements scheduledtask.KindWorkItemReminder.
type WorkItemReminderExecutor struct {
	tasks WorkItemClient
	notif NotificationClient
	quiet task.QuietWindow
	// prefs resolves each work item owner's own timezone. Optional.
	prefs QuietPreferencesLookup
	// batch caps how many reminders one tick fires, so a backlog created by
	// an outage cannot produce one huge burst the moment the service returns.
	batch int
	// staleAfter retires a reminder older than this without notifying.
	// 0 disables the bound, which is only sensible in tests.
	staleAfter time.Duration
}

func NewWorkItemReminderExecutor(tc WorkItemClient, notif NotificationClient) *WorkItemReminderExecutor {
	return &WorkItemReminderExecutor{
		tasks:      tc,
		notif:      notif,
		quiet:      task.DefaultQuietWindow(),
		batch:      50,
		staleAfter: DefaultStaleAfter,
	}
}

// SetQuietPreferences wires the per-user do-not-disturb lookup. A nil lookup
// leaves every user on the server default.
func (e *WorkItemReminderExecutor) SetQuietPreferences(l QuietPreferencesLookup) {
	if e == nil {
		return
	}
	e.prefs = l
}

// DefaultStaleAfter is how late a reminder may be and still be worth pushing.
const DefaultStaleAfter = 24 * time.Hour

// SetStaleAfter overrides the staleness bound. 0 disables it.
func (e *WorkItemReminderExecutor) SetStaleAfter(d time.Duration) {
	if e == nil {
		return
	}
	e.staleAfter = d
}

func (e *WorkItemReminderExecutor) SetNotifier(notif NotificationClient) {
	if e == nil {
		return
	}
	e.notif = notif
}

// SetQuietWindow overrides the do-not-disturb window. A zero value disables
// deferral, which is a legitimate configuration, not an error.
func (e *WorkItemReminderExecutor) SetQuietWindow(w task.QuietWindow) {
	if e == nil {
		return
	}
	e.quiet = w
}

func (*WorkItemReminderExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindWorkItemReminder
}

// reminderPayload is the scheduled-task payload:
//
//	{"user_id":"...","limit":50}
//
// It carries **no workspace**. The tenant is the scheduled task's own
// workspace_id, and nothing in the payload can change it: a payload-supplied
// workspace used to win over the task's, which let any authenticated user point
// their own reminder job at another tenant — the executor then wrote events and
// sent push notifications inside that workspace. The store query is scoped, but
// it was being handed the *attacker's* scope, so the scoping bought nothing.
type reminderPayload struct {
	UserID string `json:"user_id"`
	Limit  int    `json:"limit"`
}

func (e *WorkItemReminderExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.tasks == nil {
		return nil, fmt.Errorf("work item reminder client is not configured")
	}
	if t == nil {
		return nil, fmt.Errorf("work item reminder task is nil")
	}
	var p reminderPayload
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode work item reminder payload: %w", err)
		}
	}
	// The scheduled task's own tenancy is the only tenancy. An empty workspace
	// means the row is malformed; fail closed rather than fall back to a
	// default tenant and scan somebody's work items.
	wsID := strings.TrimSpace(t.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("work item reminder task %s has no workspace; refusing to scan an unscoped set", t.ID)
	}
	// The payload may lower the batch (a smaller tick) but never raise it: the
	// cap exists so an outage backlog cannot come back as one huge burst, and
	// a client-supplied 200 would defeat exactly that.
	limit := e.batch
	if p.Limit > 0 && p.Limit < limit {
		limit = p.Limit
	}

	now := time.Now().Unix()
	due, err := e.tasks.DueTaskReminders(ctx, wsID, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due work item reminders: %w", err)
	}

	fired, deferred, notified, stale := 0, 0, 0, 0
	quiet := e.newQuietResolver(wsID)
	quiet.fallback = e.quiet
	for _, item := range due {
		// Defence in depth: the scan above is workspace-scoped, so every row
		// belongs to wsID. If one ever does not (a store change, a hand-edited
		// row), writing an event and pushing a notification for it is the
		// cross-tenant write this file used to allow. Skip it instead.
		itemWS := strings.TrimSpace(item.WorkspaceID)
		if itemWS != "" && itemWS != wsID {
			log.Printf("[work_item] reminder for %s claims workspace %q but the job runs in %q; skipped",
				item.ID, itemWS, wsID)
			continue
		}
		if itemWS == "" {
			itemWS = wsID
		}
		// Staleness is checked before quiet hours: a three-day-old reminder
		// must not be "deferred to 07:30 tomorrow" either — it is retired.
		if e.staleAfter > 0 && now-item.RemindAt > int64(e.staleAfter/time.Second) {
			stale++
			if err := e.tasks.ClearTaskRemindAt(ctx, item.ID, itemWS, 0); err != nil {
				// Leave it in place; the next tick re-evaluates it. Retiring a
				// stale reminder is not worth failing the batch over.
				log.Printf("[work_item] retire stale reminder for %s failed: %v", item.ID, err)
			}
			continue
		}

		// Quiet hours first: a deferred reminder is still pending, so it must
		// not be counted as fired and must not be notified yet. The window and
		// the day boundary are the *owner's* — §4.2 sends `reminded` to the
		// owner, so the owner's local midnight is the one that decides.
		window, loc := quiet.resolve(ctx, item)
		next := window.Defer(item.RemindAt, loc)
		if next != item.RemindAt {
			deferred++
			if err := e.tasks.ClearTaskRemindAt(ctx, item.ID, itemWS, next); err != nil {
				// Leave the original time in place; the next tick retries the
				// deferral rather than losing the reminder.
				log.Printf("[work_item] defer reminder for %s failed: %v", item.ID, err)
				continue
			}
			continue
		}

		if err := e.fireOne(ctx, itemWS, item); err != nil {
			// One bad work item must not abort the batch: the rest still get
			// their reminders, and this one is retried on the next tick.
			log.Printf("[work_item] reminder for %s failed: %v", item.ID, err)
			continue
		}
		fired++
		notified += e.notify(ctx, itemWS, item)
	}

	out, _ := json.Marshal(map[string]any{
		"workspaceId": wsID,
		"due":         len(due),
		"fired":       fired,
		"deferred":    deferred,
		"stale":       stale,
		"notified":    notified,
	})
	return &scheduledtask.Result{Output: out}, nil
}

// quietFor returns the per-owner do-not-disturb resolver, created once per
// tick so a batch of fifty reminders does not read the same user's settings
// fifty times. It lives on the call stack rather than on the executor because
// the executor is shared across ticks.
func (e *WorkItemReminderExecutor) newQuietResolver(wsID string) *quietResolver {
	return &quietResolver{
		wsID:     wsID,
		lookup:   e.prefs,
		cache:    map[string]cachedQuiet{},
		fallback: task.QuietWindow{},
	}
}

// quietResolver memoizes one lookup per user for the length of a tick.
type quietResolver struct {
	wsID     string
	lookup   QuietPreferencesLookup
	cache    map[string]cachedQuiet
	fallback task.QuietWindow
}

type cachedQuiet struct {
	window task.QuietWindow
	loc    *time.Location
}

// resolve returns the window and timezone to apply for this work item's owner.
// The lookup is skipped entirely when no preferences are wired, when the work
// item has no owner, or when the same owner was already resolved this tick.
func (q *quietResolver) resolve(ctx context.Context, item task.Task) (task.QuietWindow, *time.Location) {
	if q == nil || q.lookup == nil {
		return q.window(), nil
	}
	owner := strings.TrimSpace(item.OwnerID)
	if owner == "" {
		// §4.2 notifies the owner, so a work item without one is nobody's quiet
		// hours to respect.
		return q.window(), nil
	}
	if hit, ok := q.cache[owner]; ok {
		return hit.window, hit.loc
	}
	out := cachedQuiet{window: q.window()}
	// A lookup failure must not silence a reminder, so an error falls back to
	// the server default rather than aborting the tick.
	if prefs, ok := q.lookup.QuietPreferences(ctx, q.wsID, owner); ok {
		out.window = prefs.Window()
		out.loc = prefs.Location()
	}
	q.cache[owner] = out
	return out.window, out.loc
}

func (q *quietResolver) window() task.QuietWindow {
	if q == nil || q.fallback == (task.QuietWindow{}) {
		return task.DefaultQuietWindow()
	}
	return q.fallback
}

// fireOne writes the `reminded` event and retires the reminder point.
func (e *WorkItemReminderExecutor) fireOne(ctx context.Context, wsID string, item task.Task) error {
	payload, err := json.Marshal(task.EventPayload{
		TaskTitle: item.Title,
		Status:    item.Status,
	})
	if err != nil {
		return fmt.Errorf("encode reminder payload: %w", err)
	}
	ev := task.WorkItemEvent{
		WorkspaceID: wsID,
		TaskID:      item.ID,
		// Derived from remind_at: this is what makes a repeated tick a no-op.
		EventID:   task.ReminderEventID(item.RemindAt),
		EventType: task.EventReminded,
		// The reminder hub is the actor, not a person. Writing "system" keeps
		// the notification rules from excluding a participant who happens to
		// share the id.
		ActorUserID: "system",
		Payload:     payload,
		CreatedAt:   item.RemindAt,
	}
	if err := e.tasks.AppendEvent(ctx, ev); err != nil {
		return err
	}
	// Retire the reminder point only after the event is durably written, so a
	// failure here re-fires rather than silently swallowing the reminder. The
	// event id makes that replay idempotent.
	return e.tasks.ClearTaskRemindAt(ctx, item.ID, wsID, 0)
}

// notify sends the reminder to the recipients §4.2 defines (the owner), and
// returns how many were actually dispatched. A nil notification client, or a
// dispatch error, is logged and swallowed: the reminder already fired, and a
// 500 here would make the scheduler retry a write that succeeded.
func (e *WorkItemReminderExecutor) notify(ctx context.Context, wsID string, item task.Task) int {
	if e.notif == nil {
		return 0
	}
	parts, err := e.tasks.ListParticipants(ctx, item.ID, wsID)
	if err != nil {
		log.Printf("[work_item] list participants for %s failed: %v", item.ID, err)
		return 0
	}
	payload, _ := json.Marshal(task.EventPayload{TaskTitle: item.Title})
	ev := task.WorkItemEvent{
		WorkspaceID: wsID,
		TaskID:      item.ID,
		EventType:   task.EventReminded,
		ActorUserID: "system",
		Payload:     payload,
	}
	sent := 0
	for _, userID := range task.NotifyRecipients(ev, parts, item.OwnerID, task.EventPayload{TaskTitle: item.Title}) {
		if _, err := e.notif.Dispatch(ctx, notifycenter.Event{
			WorkspaceID: wsID,
			UserID:      userID,
			Source:      "work_item",
			Kind:        task.NotifyReminder,
			Title:       task.NotificationTitle(ev, task.EventPayload{TaskTitle: item.Title}),
			Body:        task.NotificationTitle(ev, task.EventPayload{TaskTitle: item.Title}),
			Payload:     payload,
			Priority:    "normal",
		}); err != nil {
			log.Printf("[work_item] notify %s for %s failed: %v", userID, item.ID, err)
			continue
		}
		sent++
	}
	return sent
}
