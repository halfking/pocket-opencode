package server

import "github.com/halfking/pocket-opencode/backend/internal/scheduledtask"

type scheduledTaskAuditWriter struct{ server *Server }

// NewScheduledTaskAuditWriter adapts the background scheduler's small audit
// interface to Server's centralized redaction, truncation, and persistence
// path. The scheduler passes workspace_id as the audit tenant boundary.
func NewScheduledTaskAuditWriter(s *Server) scheduledtask.AuditWriter {
	if s == nil {
		return nil
	}
	return scheduledTaskAuditWriter{server: s}
}

func (w scheduledTaskAuditWriter) Write(userID, tenantID, action, resource string, fields scheduledtask.AuditFields) {
	if w.server == nil {
		return
	}
	w.server.writeAuditEntry(action, resource, AuditFields{
		Success:  fields.Success,
		Detail:   fields.Detail,
		TenantID: tenantID,
	}, &authClaims{UserID: userID, WorkspaceID: tenantID}, "")
}
