package server

import (
	"context"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// lookupDiskSessionTask maps a disk/OpenCode session id (shown as a "task" on
// the home list) to a synthetic Task so GET /api/tasks/:id matches LIST.
func (s *Server) lookupDiskSessionTask(ctx context.Context, workspaceID, sessionID string) *task.Task {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.registry == nil || s.opencode == nil {
		return nil
	}
	for _, inst := range s.registry.ListInstancesForWorkspace(workspaceID) {
		if !shouldAggregateSessions(inst) {
			continue
		}
		base := strings.TrimSpace(inst.APIBaseURL)
		if base == "" {
			var err error
			base, err = s.registry.GetInstanceAPIBaseForWorkspace(workspaceID, inst.ID)
			if err != nil {
				continue
			}
		}
		if !strings.HasPrefix(base, "disk://") && inst.Origin != "disk" {
			continue
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		title, err := s.opencode.GetSessionSummary(lookupCtx, base, sessionID)
		cancel()
		if err != nil || strings.TrimSpace(title) == "" {
			continue
		}
		name := inst.DisplayName
		if name == "" {
			name = inst.ID
		}
		now := time.Now().UTC()
		return &task.Task{
			ID:           sessionID,
			Title:        title,
			Status:       "active",
			Priority:     "normal",
			WorkstreamID: inst.ID,
			InstanceName: name,
			Source:       "opencode",
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	}
	return nil
}

func (s *Server) diskSessionBundleRow(ctx context.Context, workspaceID, sessionID string) *sessionBundleRow {
	t := s.lookupDiskSessionTask(ctx, workspaceID, sessionID)
	if t == nil {
		return nil
	}
	return &sessionBundleRow{
		ID:           t.ID,
		Title:        t.Title,
		Lane:         "current",
		AgentKind:    t.WorkstreamID,
		AgentSession: t.ID,
		InstanceID:   t.WorkstreamID,
	}
}
