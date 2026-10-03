package task

// Access rules for shared work items (docs/学习muse/03-架构方案.md §4.1).
//
// The security-relevant cases get their own names; the rest are coverage so a
// change to the participant lookup cannot quietly widen access.

import "testing"

func mkTask(owner, visibility string) *Task {
	return &Task{ID: "t-1", OwnerID: owner, Visibility: visibility}
}

func TestCanReadWorkItem(t *testing.T) {
	cases := []struct {
		name         string
		task         *Task
		parts        []Participant
		user         string
		want         bool
		whyItMatters string
	}{
		{
			name: "owner always reads",
			task: mkTask("alice", VisibilityPrivate), user: "alice", want: true,
		},
		{
			name:  "participant reads a private item",
			task:  mkTask("alice", VisibilityPrivate),
			parts: []Participant{{UserID: "bob", Role: RoleWatcher}}, user: "bob", want: true,
		},
		{
			name:  "outsider is denied a private item",
			task:  mkTask("alice", VisibilityPrivate),
			parts: []Participant{{UserID: "bob", Role: RoleAssignee}}, user: "carol", want: false,
			whyItMatters: "this is the 403 the phase acceptance calls for",
		},
		{
			name: "outsider reads a workspace item",
			task: mkTask("alice", VisibilityWorkspace), user: "carol", want: true,
		},
		{
			name:  "shared behaves like private today",
			task:  mkTask("alice", VisibilityShared),
			parts: []Participant{{UserID: "bob", Role: RoleAssignee}}, user: "carol", want: false,
			whyItMatters: "the doc calls this level out; the code must not quietly widen it",
		},
		{
			name: "empty actor matches nothing",
			task: mkTask("", VisibilityPrivate), user: "", want: false,
			whyItMatters: "treating \"\" as a match would make every ownerless task readable by anyone",
		},
		{
			name:  "ownerless private item is readable by nobody but its participants",
			task:  mkTask("", VisibilityPrivate),
			parts: []Participant{{UserID: "bob", Role: RoleAssignee}}, user: "carol", want: false,
		},
		{
			name:  "ids are matched exactly, not by prefix",
			task:  mkTask("alice", VisibilityPrivate),
			parts: []Participant{{UserID: "bobby", Role: RoleAssignee}}, user: "bob", want: false,
			whyItMatters: "a prefix match would be a real identity leak",
		},
		{
			name: "nil task denies",
			task: nil, user: "alice", want: false,
		},
	}
	for _, c := range cases {
		if got := CanReadWorkItem(c.task, c.parts, c.user); got != c.want {
			t.Errorf("%s: CanReadWorkItem = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCanWriteWorkItem(t *testing.T) {
	cases := []struct {
		name  string
		task  *Task
		parts []Participant
		user  string
		want  bool
	}{
		{"owner writes", mkTask("alice", VisibilityPrivate), nil, "alice", true},
		{"participant writes", mkTask("alice", VisibilityPrivate),
			[]Participant{{UserID: "bob", Role: RoleAssignee}}, "bob", true},
		// Reading a workspace-wide item must never imply the right to
		// reassign it.
		{"workspace viewer does not write", mkTask("alice", VisibilityWorkspace), nil, "carol", false},
		{"outsider does not write", mkTask("alice", VisibilityPrivate),
			[]Participant{{UserID: "bob", Role: RoleAssignee}}, "carol", false},
		{"empty actor denied", mkTask("", VisibilityWorkspace), nil, "", false},
		{"nil task denies", nil, nil, "alice", false},
	}
	for _, c := range cases {
		if got := CanWriteWorkItem(c.task, c.parts, c.user); got != c.want {
			t.Errorf("%s: CanWriteWorkItem = %v, want %v", c.name, got, c.want)
		}
	}
}
