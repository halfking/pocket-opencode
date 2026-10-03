package task

// Event → notification mapping (docs/学习muse/03-架构方案.md §4.2).
//
// This file is deliberately pure: it takes an event plus the work item's
// participants and returns who should be told. Keeping it free of I/O means
// the recipient rules — the part that actually decides whether a user gets
// spammed or left out — are unit-testable without a database.
//
// Two rules that are easy to get wrong and are therefore encoded here rather
// than in the handler:
//
//   - The actor never notifies themselves. Someone who changes a status or
//     posts a comment already knows; pinging them is the single fastest way
//     to train a user to mute a source.
//   - An event with no mapping produces no notification at all. Silence is
//     the correct answer for `created` and `due_changed`.
//
// §4.2 also listed a `due_soon` row, but the reminder hub writes the `reminded`
// event when a reminder fires, and `reminded` already has its own row below.
// Having both would double-notify the owner on the same fire, so `due_soon`
// is folded into `reminded`; the architecture doc was corrected to match.

import "strings"

// Notification kinds for the work_item source.
const (
	NotifyAssigned      = "work_item.assigned"
	NotifyStatusChanged = "work_item.status_changed"
	NotifyComment       = "work_item.comment"
	NotifyCompleted     = "work_item.completed"
	NotifyReminder      = "work_item.reminder"
)

// EventPayload is the subset of the event payload the mapping reads. Callers
// decode it from WorkItemEvent.Payload; a decode failure yields zero values,
// which maps to "no specific recipient" rather than an error — a malformed
// payload must not turn a successful write into a failed request.
type EventPayload struct {
	// UserID is the newly assigned user for an `assigned` event.
	UserID string `json:"userId"`
	// TaskTitle is carried so the dispatcher can render a headline without
	// re-reading the task row.
	TaskTitle string `json:"taskTitle"`
	// Status is the resulting status for a `status_changed` event.
	Status string `json:"status"`
	// Comment is the comment body.
	Comment string `json:"comment"`
	// ChildID / ChildTitle identify a sub-task added under this work item.
	ChildID    string `json:"childId,omitempty"`
	ChildTitle string `json:"childTitle,omitempty"`
}

// NotificationKind returns the notifycenter kind an event maps to, or "" when
// the event should not generate a notification.
func NotificationKind(eventType string) string {
	switch eventType {
	case EventAssigned:
		return NotifyAssigned
	case EventStatusChanged:
		return NotifyStatusChanged
	case EventComment:
		return NotifyComment
	case EventCompleted:
		return NotifyCompleted
	case EventReminded:
		return NotifyReminder
	default:
		// EventCreated, EventDueChanged and EventChildAdded stay silent on
		// purpose. A sub-task appearing is already visible in the parent's
		// progress bar; telling every participant about it is exactly the
		// noise that gets a whole notification source muted.
		return ""
	}
}

// NotifyRecipients returns the deduplicated, order-stable list of users who
// should receive a notification for ev. ownerID is the work item's owner; for
// `reminded` the owner is the only recipient.
//
// The returned slice never contains the actor, and never contains an empty
// user id.
func NotifyRecipients(ev WorkItemEvent, parts []Participant, ownerID string, payload EventPayload) []string {
	if NotificationKind(ev.EventType) == "" {
		return nil
	}
	actor := strings.TrimSpace(ev.ActorUserID)

	// exceptActor drops the actor; keepSelf keeps them (used by `completed`,
	// where the owner closing the task does want the receipt).
	collect := func(keepSelf bool) []string {
		seen := make(map[string]bool, len(parts)+1)
		out := make([]string, 0, len(parts)+1)
		add := func(id string) {
			id = strings.TrimSpace(id)
			// An assignee is a legitimate recipient even before they accept,
			// so `assigned` is handled by its own branch below.
			if id == "" || seen[id] {
				return
			}
			if !keepSelf && id == actor {
				return
			}
			seen[id] = true
			out = append(out, id)
		}
		if o := strings.TrimSpace(ownerID); o != "" {
			add(o)
		}
		for _, p := range parts {
			add(p.UserID)
		}
		return out
	}

	switch ev.EventType {
	case EventAssigned:
		// Only the person who was just assigned. Notifying every participant
		// would tell the whole team about something only one person can act on.
		target := strings.TrimSpace(payload.UserID)
		if target == "" || target == actor {
			return nil
		}
		return []string{target}
	case EventStatusChanged, EventComment:
		// Everyone involved except whoever caused it. `comment` keeps the
		// actor out so a thread does not notify its own author on every post.
		return collect(false)
	case EventCompleted:
		return collect(true)
	case EventReminded:
		// The owner is the one responsible for the due date; assignees get
		// their own reminder through their own rule, not this event.
		owner := strings.TrimSpace(ownerID)
		if owner == "" || owner == actor {
			return nil
		}
		return []string{owner}
	default:
		return nil
	}
}

// StatusChangeEventType picks the activity-stream event for a status change.
// It returns "" when nothing actually changed — that is the caller's signal to
// stay silent rather than to write a no-op event and notify everyone that
// nothing happened.
//
// Completion is its own event type because it reaches a different set of
// people with a different tone: everyone involved hears about a status change,
// while `completed` also tells the person who did it (see NotifyRecipients).
func StatusChangeEventType(from, to string) string {
	before := strings.ToLower(strings.TrimSpace(from))
	after := strings.ToLower(strings.TrimSpace(to))
	if before == after {
		return ""
	}
	switch after {
	case "completed", "accepted":
		return EventCompleted
	default:
		return EventStatusChanged
	}
}

// NotificationTitle renders the English fallback headline. Clients with a
// translation entry localise it themselves; this string exists so a client
// without one still shows something meaningful, matching the flashcard and
// learning-digest executors' contract.
func NotificationTitle(ev WorkItemEvent, p EventPayload) string {
	title := strings.TrimSpace(p.TaskTitle)
	if title == "" {
		title = "Work item"
	}
	switch ev.EventType {
	case EventAssigned:
		return "Assigned to you: " + title
	case EventStatusChanged:
		if s := strings.TrimSpace(p.Status); s != "" {
			return title + " → " + s
		}
		return "Status changed: " + title
	case EventComment:
		return "New comment: " + title
	case EventCompleted:
		return "Completed: " + title
	case EventReminded:
		return "Reminder: " + title
	default:
		return title
	}
}
