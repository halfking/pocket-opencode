package task

// Progress roll-up and cycle detection for parent → child work items.

import (
	"strconv"
	"testing"
)

func TestRollUpProgress(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     GoalProgress
	}{
		{
			// A work item with no children has made no progress. Reporting 100
			// would render a full bar for "not a goal".
			name:     "no children is zero, not complete",
			statuses: nil,
			want:     GoalProgress{Percent: 0},
		},
		{
			name:     "all done",
			statuses: []string{"completed", "completed", "accepted"},
			want:     GoalProgress{Total: 3, Done: 3, Percent: 100},
		},
		{
			// Truncation: 2/3 must read 66, not 67 — a goal that rounds up
			// looks finished when it is not.
			name:     "partial truncates",
			statuses: []string{"completed", "completed", "active"},
			want:     GoalProgress{Total: 3, Done: 2, Percent: 66},
		},
		{
			name:     "blocked is not progress",
			statuses: []string{"completed", "blocked", "blocked"},
			want:     GoalProgress{Total: 3, Done: 1, Blocked: 2, Percent: 33},
		},
		{
			name:     "status matching is tolerant",
			statuses: []string{" COMPLETED ", "Blocked", "active"},
			want:     GoalProgress{Total: 3, Done: 1, Blocked: 1, Percent: 33},
		},
		{
			name:     "everything blocked",
			statuses: []string{"blocked", "blocked"},
			want:     GoalProgress{Total: 2, Done: 0, Blocked: 2, Percent: 0},
		},
	}
	for _, c := range cases {
		got := RollUpProgress("parent-1", c.statuses)
		want := c.want
		want.ParentID = "parent-1"
		if got != want {
			t.Errorf("%s: RollUpProgress = %+v, want %+v", c.name, got, want)
		}
	}
}

func TestWouldCreateCycle(t *testing.T) {
	// a → b → c, with c as the root. Plus a loose d with no parent.
	parentOf := map[string]string{
		"a": "b",
		"b": "c",
		"d": "",
	}
	cases := []struct {
		name   string
		child  string
		parent string
		want   bool
		why    string
	}{
		{"no parent is always fine", "a", "", false, "clearing a parent cannot loop"},
		{"unrelated parent is fine", "d", "c", false, "d is a leaf, c is a root"},
		{"re-asserting the current parent is fine", "a", "b", false, "a→b→c is already a valid chain"},
		// The interesting case: c is the root, so making b the parent of c
		// closes the loop b→c→b. A validator that only asked "is b below c?"
		// would miss it entirely.
		{"root under its own child is a cycle", "c", "b", true, "closes b→c→b"},
		{"self parent is a cycle", "a", "a", true, "degenerate self-loop"},
		{"making a child the parent of its own parent", "b", "a", true, "b→a→b"},
		{"unknown parent id is not judged here", "a", "ghost", false, "existence is the store's check"},
	}
	for _, c := range cases {
		if got := WouldCreateCycle(c.child, c.parent, parentOf); got != c.want {
			t.Errorf("%s: WouldCreateCycle(%q,%q) = %v, want %v (%s)", c.name, c.child, c.parent, got, c.want, c.why)
		}
	}
}

// A tree that is already corrupt must not hang the request. The walk is
// budgeted and remembers visited nodes, so both a loop and a very deep chain
// terminate instead of spinning inside a request handler.
func TestWouldCreateCycleTerminatesOnCorruptTree(t *testing.T) {
	corrupt := map[string]string{"x": "y", "y": "x"}
	// z is genuinely not inside the x↔y loop, so attaching it creates no new
	// cycle. What matters is that the call returns at all.
	if WouldCreateCycle("z", "x", corrupt) {
		t.Error("z is not inside the x↔y loop, so no new cycle is created")
	}
	deep := map[string]string{}
	const depth = 500
	for i := 0; i < depth; i++ {
		deep[strconv.Itoa(i)] = strconv.Itoa(i + 1)
	}
	// Exceeding the walk budget is reported as a cycle on purpose: the walk
	// did not finish, so it cannot claim the write is safe. A 500-deep goal
	// tree is not a shape worth supporting, and refusing it is cheaper than
	// computing a verdict the function never reached.
	if !WouldCreateCycle("root", "0", deep) {
		t.Error("a chain longer than the walk budget must fail closed, not pass")
	}
	// A chain within the budget resolves normally.
	shallow := map[string]string{"0": "1", "1": "2", "2": ""}
	if WouldCreateCycle("root", "0", shallow) {
		t.Error("a 3-deep chain that never reaches the child is not a cycle")
	}
}
