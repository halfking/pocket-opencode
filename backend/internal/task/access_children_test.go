package task

// B-3: GET /api/tasks/{id}/children checked the *parent's* visibility and then
// returned every child verbatim, so one read of a shared goal handed over each
// private child's full record (title, description, assignees, due date).
//
// The rule is the same one that guards a single work item — CanReadWorkItem —
// so these are unit tests: a second SQL predicate would be a second copy of the
// access rules, and the copy that drifts is the one that leaks.

import "testing"

func mkChild(id, owner, visibility string) Task {
	return Task{ID: id, OwnerID: owner, Visibility: visibility, Title: "child " + id}
}

func TestFilterReadableChildren(t *testing.T) {
	children := []Task{
		mkChild("c-pub", "alice", VisibilityWorkspace),
		mkChild("c-priv-mine", "carol", VisibilityPrivate),
		mkChild("c-priv-alice", "alice", VisibilityPrivate),
		mkChild("c-part", "dave", VisibilityPrivate),
	}
	parts := map[string][]Participant{
		"c-part":      {{UserID: "carol", Role: RoleWatcher}},
		"c-priv-mine": nil,
	}
	lookup := func(t Task) []Participant { return parts[t.ID] }

	cases := []struct {
		name  string
		user  string
		want  []string
		whyIt string
	}{
		{
			name:  "outsider keeps only the workspace-visible child",
			user:  "carol",
			want:  []string{"c-pub", "c-priv-mine", "c-part"},
			whyIt: "the private children belong to other people; being able to read the parent is not consent to read them",
		},
		{
			name:  "the owner of the parent keeps her own private children",
			user:  "alice",
			want:  []string{"c-pub", "c-priv-alice"},
			whyIt: "filtering must not hide work items from their own owner",
		},
		{
			name:  "a plain member sees nothing private",
			user:  "erin",
			want:  []string{"c-pub"},
			whyIt: "this is the case the endpoint used to get wrong",
		},
		{
			name:  "an empty actor sees nothing",
			user:  "",
			want:  nil,
			whyIt: "an unresolved identity must not be treated as a match",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FilterReadableChildren(children, lookup, c.user)
			ids := make([]string, 0, len(got))
			for _, g := range got {
				ids = append(ids, g.ID)
			}
			if len(ids) != len(c.want) {
				t.Fatalf("visible = %v, want %v (%s)", ids, c.want, c.whyIt)
			}
			for i := range ids {
				if ids[i] != c.want[i] {
					t.Fatalf("visible = %v, want %v (order must be preserved)", ids, c.want)
				}
			}
		})
	}
}

// Order is the store's ordering (oldest first); the filter must not reshuffle.
func TestFilterReadableChildrenPreservesOrder(t *testing.T) {
	children := []Task{
		mkChild("c1", "alice", VisibilityWorkspace),
		mkChild("c2", "bob", VisibilityPrivate),
		mkChild("c3", "alice", VisibilityWorkspace),
	}
	got := FilterReadableChildren(children, func(Task) []Participant { return nil }, "alice")
	if len(got) != 2 || got[0].ID != "c1" || got[1].ID != "c3" {
		t.Fatalf("filter reordered or dropped rows: %+v", got)
	}
}

// A nil lookup (a caller that has no participant data) must not panic, and must
// fall back to the owner/visibility rule alone.
func TestFilterReadableChildrenToleratesMissingParticipantData(t *testing.T) {
	children := []Task{
		mkChild("c-priv", "alice", VisibilityPrivate),
		mkChild("c-pub", "alice", VisibilityWorkspace),
	}
	got := FilterReadableChildren(children, nil, "alice")
	if len(got) != 2 {
		t.Fatalf("owner must still see both of her children, got %+v", got)
	}
	got = FilterReadableChildren(children, nil, "bob")
	if len(got) != 1 || got[0].ID != "c-pub" {
		t.Fatalf("a non-owner must only see the workspace child, got %+v", got)
	}
}

func TestFilterReadableChildrenEmptyInput(t *testing.T) {
	if got := FilterReadableChildren(nil, nil, "alice"); len(got) != 0 {
		t.Fatalf("empty input must stay empty, got %+v", got)
	}
}
