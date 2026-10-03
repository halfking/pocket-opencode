package task

// Parent → child work items (docs/学习muse/03-架构方案.md §4, Muse M1).
//
// A "goal" here is not a new entity. It is an ordinary work item with other
// work items pointing at it through `parent_id`, and its progress is derived
// from those children rather than stored. That keeps the work = task rule
// intact: a goal is just a work item you can nest under.
//
// The two things in this file that can silently corrupt the tree — cycle
// detection and progress arithmetic — are pure functions so they are testable
// without a database. The SQL that feeds them lives in store.go.

import "strings"

// GoalProgress is the rolled-up state of a parent work item.
type GoalProgress struct {
	ParentID string `json:"parentId"`
	// Total is the number of direct children. Zero means "this is not a goal",
	// which is why Percent is 0 rather than 100 — a work item with no children
	// has made no progress, and rendering a full bar for it would be a lie.
	Total   int `json:"total"`
	Done    int `json:"done"`
	Percent int `json:"percent"`
	// Blocked counts children that cannot currently move. Surfaced separately
	// because "50% done but 3 of them stuck" needs different attention than
	// "50% done".
	Blocked int `json:"blocked"`
}

// RollUpProgress derives a parent's progress from its children's statuses.
// A child counts as done on `completed` or `accepted`; `blocked` is reported
// separately rather than treated as progress.
func RollUpProgress(parentID string, statuses []string) GoalProgress {
	p := GoalProgress{ParentID: parentID, Total: len(statuses)}
	if len(statuses) == 0 {
		return p
	}
	for _, s := range statuses {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "completed", "accepted":
			p.Done++
		case "blocked":
			p.Blocked++
		}
	}
	// Truncate rather than round: a goal showing 99% while every child is
	// still open reads as "almost done" and invites premature closing.
	p.Percent = p.Done * 100 / p.Total
	return p
}

// WouldCreateCycle reports whether setting childID's parent to newParentID
// would create a cycle. parentOf maps a task id to its current parent id
// (empty / absent means "no parent").
//
// A cycle is not cosmetic: the roll-up walks children, and a self-referential
// tree makes "done" undecidable and lets a goal count itself as a child.
func WouldCreateCycle(childID, newParentID string, parentOf map[string]string) bool {
	child := strings.TrimSpace(childID)
	parent := strings.TrimSpace(newParentID)
	// A work item with no parent cannot make a cycle.
	if parent == "" {
		return false
	}
	// Parenting something under itself is the degenerate case.
	if child != "" && parent == child {
		return true
	}
	// Walk up from the proposed parent. If we ever reach the child, the
	// proposed parent is already a descendant of the child.
	// The step budget guards against a tree that is *already* corrupt: without
	// it a pre-existing cycle would hang the request instead of being rejected.
	const maxDepth = 64
	seen := make(map[string]bool, maxDepth)
	cur := parent
	for i := 0; i < maxDepth; i++ {
		if child != "" && cur == child {
			return true
		}
		if cur == "" || seen[cur] {
			// Reached the root, or re-entered a node: the walk is bounded and
			// no cycle through the child was found.
			return false
		}
		seen[cur] = true
		next := strings.TrimSpace(parentOf[cur])
		if next == "" {
			return false
		}
		cur = next
	}
	// Ran out of budget on a path that never reached the child. Report a
	// cycle so the caller refuses the write: the walk did not finish, so it
	// cannot claim the tree is safe. The practical effect is a depth limit of
	// maxDepth on a goal tree, which no real one approaches.
	return true
}
