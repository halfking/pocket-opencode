package task

// Access rules for shared work items (docs/学习muse/03-架构方案.md §4.1).
//
// The three visibility levels are closed, and the difference between them is
// who may read a work item they are not on:
//
//	visibility=private    only the owner and the participants
//	visibility=shared     same read set as private today; reserved for
//	                      "shared with a named set" once that set can differ
//	                      from the participant list
//	visibility=workspace  every member of the work item's workspace
//
// `shared` and `private` currently resolve to the same set. That is deliberate
// and recorded rather than papered over: collapsing them now would make the
// later distinction a schema change instead of a rule change. Writes are
// narrower than reads — being able to see a work item never implies the right
// to reassign it.

import "strings"

// isParticipant reports whether userID appears on the work item in any role.
// Comparison is exact after trimming; ids come from JWT claims, so they are
// already canonical, and a fuzzy match would be a security hole.
func isParticipant(parts []Participant, userID string) bool {
	id := strings.TrimSpace(userID)
	if id == "" {
		return false
	}
	for _, p := range parts {
		if strings.TrimSpace(p.UserID) == id {
			return true
		}
	}
	return false
}

// CanReadWorkItem reports whether userID may read t.
func CanReadWorkItem(t *Task, parts []Participant, userID string) bool {
	if t == nil {
		return false
	}
	id := strings.TrimSpace(userID)
	// An empty actor is never a member. Treating "" as a match would make
	// every anonymous-ish caller the owner of every ownerless task.
	if id == "" {
		return false
	}
	if strings.TrimSpace(t.OwnerID) == id {
		return true
	}
	if isParticipant(parts, id) {
		return true
	}
	// The author of a task has no owner_id set until someone claims it; keep
	// them able to read their own work item.
	return t.Visibility == VisibilityWorkspace
}

// CanWriteWorkItem reports whether userID may change the work item's
// participants, owner or activity stream. The owner and existing participants
// may; a plain workspace member may not reassign someone else's task.
func CanWriteWorkItem(t *Task, parts []Participant, userID string) bool {
	if t == nil {
		return false
	}
	id := strings.TrimSpace(userID)
	if id == "" {
		return false
	}
	if strings.TrimSpace(t.OwnerID) == id {
		return true
	}
	return isParticipant(parts, id)
}
