package server

import (
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// handleMobileApprovalRouter is the production HTTP approval surface. It is
// intentionally separate from the legacy Echo MobileAPI, whose isolation test
// prevents it from being re-wired into the net/http server.
//
// Routes:
//
//	GET  /api/mobile/approvals?instance_id=&session_id=
//	POST /api/mobile/approvals/permission/{request_id}/reply
//	POST /api/mobile/approvals/question/{request_id}/reply
//	POST /api/mobile/approvals/question/{request_id}/reject
func (s *Server) handleMobileApprovalRouter(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireMobileWorkspace(w, r); !ok {
		return
	}
	if s.registry == nil || s.permMgr == nil || s.quesMgr == nil {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable,
			"mobile approval managers are not configured")
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/mobile/approvals"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		s.listMobileApprovals(w, r)
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) != 3 || (parts[2] != "reply" && parts[2] != "reject") {
		s.writeResourceNotFound(w, r)
		return
	}
	kind, requestID, operation := parts[0], parts[1], parts[2]
	switch {
	case kind == "permission" && operation == "reply" && r.Method == http.MethodPost:
		s.replyMobilePermission(w, r, requestID)
	case kind == "question" && operation == "reply" && r.Method == http.MethodPost:
		s.replyMobileQuestion(w, r, requestID)
	case kind == "question" && operation == "reject" && r.Method == http.MethodPost:
		s.rejectMobileQuestion(w, r, requestID)
	default:
		s.writeResourceNotFound(w, r)
	}
}

func (s *Server) listMobileApprovals(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if _, _, ok := s.resolveMobileInstance(w, r, instanceID, true); !ok {
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	writeJSON(w, http.StatusOK, map[string]any{
		"permissions": s.permMgr.ListPending(instanceID, sessionID),
		"questions":   s.quesMgr.ListPending(instanceID, sessionID),
	})
}

func (s *Server) replyMobilePermission(w http.ResponseWriter, r *http.Request, requestID string) {
	var body struct {
		InstanceID string `json:"instance_id"`
		SessionID  string `json:"session_id"`
		Decision   string `json:"decision"`
		Message    string `json:"message"`
	}
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	decision := auth.Decision(body.Decision)
	if decision != auth.DecisionOnce && decision != auth.DecisionAlways && decision != auth.DecisionReject {
		s.writeStructuredError(w, r, http.StatusBadRequest, "invalid_decision", "permission decision must be once, always, or reject")
		return
	}
	if err := s.validateMobileApproval(r, body.InstanceID, body.SessionID, requestID, decision, body.Message, nil); err != nil {
		s.writeApprovalValidationError(w, r, err)
		return
	}

	workspaceID, _ := s.requireMobileWorkspace(w, r)
	reply := adapter.PermissionReply(decision)
	if err := s.permMgr.ReplyForWorkspace(r.Context(), workspaceID, body.InstanceID, body.SessionID, requestID, reply, body.Message); err != nil {
		s.writeApprovalManagerError(w, r, err)
		return
	}
	s.recordMobileApprovalAudit(r, "permission_"+body.Decision, body.InstanceID, body.SessionID, requestID)
	s.writeApprovalConfirmed(w, r, requestID, body.Decision)
}

func (s *Server) replyMobileQuestion(w http.ResponseWriter, r *http.Request, requestID string) {
	var body struct {
		InstanceID string                   `json:"instance_id"`
		SessionID  string                   `json:"session_id"`
		Answers    []adapter.QuestionAnswer `json:"answers"`
	}
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	answers := flattenQuestionAnswers(body.Answers)
	if err := s.validateMobileApproval(r, body.InstanceID, body.SessionID, requestID, auth.DecisionAnswer, "", answers); err != nil {
		s.writeApprovalValidationError(w, r, err)
		return
	}

	workspaceID, _ := s.requireMobileWorkspace(w, r)
	if err := s.quesMgr.ReplyForWorkspace(r.Context(), workspaceID, body.InstanceID, body.SessionID, requestID, body.Answers); err != nil {
		s.writeApprovalManagerError(w, r, err)
		return
	}
	s.recordMobileApprovalAudit(r, "question_answer", body.InstanceID, body.SessionID, requestID)
	s.writeApprovalConfirmed(w, r, requestID, string(auth.DecisionAnswer))
}

func (s *Server) rejectMobileQuestion(w http.ResponseWriter, r *http.Request, requestID string) {
	var body struct {
		InstanceID string `json:"instance_id"`
		SessionID  string `json:"session_id"`
	}
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	if err := s.validateMobileApproval(r, body.InstanceID, body.SessionID, requestID, auth.DecisionReject, "", nil); err != nil {
		s.writeApprovalValidationError(w, r, err)
		return
	}

	workspaceID, _ := s.requireMobileWorkspace(w, r)
	if err := s.quesMgr.RejectForWorkspace(r.Context(), workspaceID, body.InstanceID, body.SessionID, requestID); err != nil {
		s.writeApprovalManagerError(w, r, err)
		return
	}
	s.recordMobileApprovalAudit(r, "question_reject", body.InstanceID, body.SessionID, requestID)
	s.writeApprovalConfirmed(w, r, requestID, string(auth.DecisionReject))
}

func (s *Server) validateMobileApproval(r *http.Request, instanceID, sessionID, requestID string, decision auth.Decision, message string, answers []string) error {
	claims := s.claimsFromContext(r)
	if claims == nil {
		return &auth.ValidationError{Code: CodeUnauthenticated, Message: "authentication required"}
	}
	if claims.WorkspaceID == "" {
		return &auth.ValidationError{Code: CodeWorkspaceRequired, Message: "workspace_id is required in claims"}
	}
	target := auth.ApprovalTarget{
		WorkspaceID: claims.WorkspaceID,
		InstanceID:  instanceID,
		SessionID:   sessionID,
		RequestID:   requestID,
	}
	actor := auth.ScopeContext{UserID: claims.UserID, Role: claims.Role, WorkspaceID: claims.WorkspaceID}
	if err := auth.ValidateScope(actor, target); err != nil {
		return err
	}
	return auth.ValidateDecision(decision, message, answers)
}

func flattenQuestionAnswers(answers []adapter.QuestionAnswer) []string {
	out := make([]string, 0)
	for _, answer := range answers {
		out = append(out, answer...)
	}
	return out
}

func (s *Server) writeApprovalValidationError(w http.ResponseWriter, r *http.Request, err error) {
	validationErr, ok := auth.IsValidationError(err)
	if !ok {
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid approval request")
		return
	}
	status := http.StatusBadRequest
	switch validationErr.Code {
	case CodeUnauthenticated:
		status = http.StatusUnauthorized
	case CodeWorkspaceRequired:
		status = http.StatusBadRequest
	case CodeNotFound:
		status = http.StatusNotFound
	case CodePayloadTooLarge:
		status = http.StatusRequestEntityTooLarge
	}
	s.writeStructuredError(w, r, status, validationErr.Code, validationErr.Message)
}

func (s *Server) writeApprovalManagerError(w http.ResponseWriter, r *http.Request, err error) {
	if strings.Contains(err.Error(), "not pending") {
		s.writeStructuredError(w, r, http.StatusConflict, CodeApprovalExpired, "approval request is no longer pending")
		return
	}
	if strings.Contains(err.Error(), "resolve writable instance") {
		s.writeResourceNotFound(w, r)
		return
	}
	s.writeStructuredError(w, r, http.StatusBadGateway, CodeUpstreamUnavailable, "approval reply failed")
}

func (s *Server) writeApprovalConfirmed(w http.ResponseWriter, r *http.Request, requestID, decision string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id":     requestID,
		"decision":       decision,
		"confirmed":      true,
		"correlation_id": s.requestIDFromContext(r),
	})
}

func (s *Server) recordMobileApprovalAudit(r *http.Request, action, instanceID, sessionID, requestID string) {
	if s.auditStore == nil {
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil {
		return
	}
	_ = s.auditStore.Record(&redclaw.AuditEntry{
		Action:   "mobile.approval." + action,
		UserID:   claims.UserID,
		TenantID: claims.WorkspaceID,
		Resource: "instance:" + instanceID + "/session:" + sessionID + "/request:" + requestID,
		Detail:   "upstream_confirmed",
		Success:  true,
	})
}
