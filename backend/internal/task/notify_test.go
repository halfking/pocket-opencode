package task

// Recipient rules for the event → notification mapping. These assertions are
// the contract from docs/学习muse/03-架构方案.md §4.2: the cases that decide
// whether a person is told about their own work.

import (
	"encoding/json"
	"testing"
)

func parts(users ...string) []Participant {
	out := make([]Participant, 0, len(users))
	for _, u := range users {
		out = append(out, Participant{UserID: u, Role: RoleAssignee})
	}
	return out
}

func payloadJSON(t *testing.T, p EventPayload) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("recipients = %v, want %v", got, want)
		}
	}
}

func TestNotificationKind(t *testing.T) {
	cases := map[string]string{
		EventAssigned:      NotifyAssigned,
		EventStatusChanged: NotifyStatusChanged,
		EventComment:       NotifyComment,
		EventCompleted:     NotifyCompleted,
		EventReminded:      NotifyReminder,
		// No notification: the author knows they created it, a due-date edit is
		// only interesting to the reminder hub, and a sub-task appearing is
		// already visible in the parent's progress bar.
		EventCreated:    "",
		EventDueChanged: "",
		EventChildAdded: "",
		"unknown_type":  "",
	}
	for in, want := range cases {
		if got := NotificationKind(in); got != want {
			t.Errorf("NotificationKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotifyRecipientsAssigned(t *testing.T) {
	ev := WorkItemEvent{EventType: EventAssigned, ActorUserID: "alice"}
	p := EventPayload{UserID: "bob"}

	// Only the assignee — not every participant on the work item.
	got := NotifyRecipients(ev, parts("carol", "dave"), "alice", p)
	eq(t, got, []string{"bob"})

	// Assigning to yourself notifies nobody.
	ev.ActorUserID = "bob"
	eq(t, NotifyRecipients(ev, parts("carol"), "bob", p), nil)

	// A payload without a target is malformed; fail silent rather than
	// broadcasting to the participant list.
	eq(t, NotifyRecipients(ev, parts("carol"), "alice", EventPayload{}), nil)
}

func TestNotifyRecipientsStatusChangedExcludesActor(t *testing.T) {
	ev := WorkItemEvent{EventType: EventStatusChanged, ActorUserID: "bob"}
	got := NotifyRecipients(ev, parts("bob", "carol"), "bob", EventPayload{Status: "in_progress"})
	// bob is both actor and owner → excluded; carol is told.
	eq(t, got, []string{"carol"})
}

func TestNotifyRecipientsCommentExcludesActor(t *testing.T) {
	ev := WorkItemEvent{EventType: EventComment, ActorUserID: "carol"}
	got := NotifyRecipients(ev, parts("alice", "carol", "dave"), "alice", EventPayload{Comment: "ping"})
	eq(t, got, []string{"alice", "dave"})
}

func TestNotifyRecipientsCompletedKeepsActor(t *testing.T) {
	// The owner closing the task still wants the receipt, unlike the other
	// broadcast events.
	ev := WorkItemEvent{EventType: EventCompleted, ActorUserID: "alice"}
	got := NotifyRecipients(ev, parts("alice", "bob"), "alice", EventPayload{})
	eq(t, got, []string{"alice", "bob"})
}

func TestNotifyRecipientsRemindedGoesToOwnerOnly(t *testing.T) {
	ev := WorkItemEvent{EventType: EventReminded, ActorUserID: "system"}
	got := NotifyRecipients(ev, parts("bob", "carol"), "alice", EventPayload{})
	eq(t, got, []string{"alice"})

	// The owner triggering their own reminder needs no notification.
	ev.ActorUserID = "alice"
	eq(t, NotifyRecipients(ev, parts("bob"), "alice", EventPayload{}), nil)
}

func TestNotifyRecipientsSilentEvents(t *testing.T) {
	for _, typ := range []string{EventCreated, EventDueChanged, EventChildAdded, "bogus"} {
		ev := WorkItemEvent{EventType: typ, ActorUserID: "alice"}
		if got := NotifyRecipients(ev, parts("bob", "carol"), "alice", EventPayload{}); got != nil {
			t.Errorf("event %q notified %v, want nobody", typ, got)
		}
	}
}

func TestNotifyRecipientsDedupesAndDropsBlanks(t *testing.T) {
	ev := WorkItemEvent{EventType: EventComment, ActorUserID: "dave"}
	// "alice" arrives twice (owner + participant); "  " is whitespace noise.
	got := NotifyRecipients(ev, parts("alice", "  ", "alice", "bob"), "alice", EventPayload{})
	eq(t, got, []string{"alice", "bob"})
}

func TestNotifyRecipientsNoOwnerStillUsesParticipants(t *testing.T) {
	ev := WorkItemEvent{EventType: EventComment, ActorUserID: "zoe"}
	got := NotifyRecipients(ev, parts("alice", "bob"), "", EventPayload{})
	eq(t, got, []string{"alice", "bob"})
}

func TestNotificationTitle(t *testing.T) {
	cases := []struct {
		eventType string
		payload   EventPayload
		want      string
	}{
		{EventAssigned, EventPayload{TaskTitle: "Ship P3"}, "Assigned to you: Ship P3"},
		{EventStatusChanged, EventPayload{TaskTitle: "Ship P3", Status: "in_progress"}, "Ship P3 → in_progress"},
		{EventComment, EventPayload{TaskTitle: "Ship P3"}, "New comment: Ship P3"},
		{EventCompleted, EventPayload{TaskTitle: "Ship P3"}, "Completed: Ship P3"},
		{EventReminded, EventPayload{TaskTitle: "Ship P3"}, "Reminder: Ship P3"},
		// No title anywhere → still renders something meaningful.
		{EventComment, EventPayload{}, "New comment: Work item"},
	}
	for _, c := range cases {
		got := NotificationTitle(WorkItemEvent{EventType: c.eventType}, c.payload)
		if got != c.want {
			t.Errorf("NotificationTitle(%q) = %q, want %q", c.eventType, got, c.want)
		}
	}
}
