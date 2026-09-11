package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/task"
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

// mobilePermissionReplyBody is the decoded POST body of a permission reply.
// The acc_* fields are an optional client echo of the task binding for
// audit reconciliation only — the authoritative binding always comes from
// the task store (SetACCBinding is the sole writer).
type mobilePermissionReplyBody struct {
	InstanceID    string `json:"instance_id"`
	SessionID     string `json:"session_id"`
	Decision      string `json:"decision"`
	Message       string `json:"message"`
	ACCTaskID     string `json:"acc_task_id,omitempty"`
	ACCDispatchID string `json:"acc_dispatch_id,omitempty"`
}

func (s *Server) replyMobilePermission(w http.ResponseWriter, r *http.Request, requestID string) {
	var body mobilePermissionReplyBody
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

	// Pocket↔ACC 审批互锁（fail-closed）。accRuntime 未配置 → gate 不生效，
	// 行为与历史版本完全一致。gate 生效时：
	//   - 绑定查询失败（含 taskStore 缺失）→ fail-closed；
	//   - task_session_links → task 命中 ACC dispatch 绑定（acc_dispatch_id
	//     非空）→ 先 AnswerPermission；ACC 失败（含 ErrGateUnavailable）→
	//     fail-closed；
	//   - 未绑定 → 原行为不变。
	if s.accRuntime != nil {
		bound, gateErr := s.enforceACCPermissionGate(r, workspaceID, requestID, decision, body)
		if gateErr != nil {
			s.failClosedPermissionReply(w, r, workspaceID, body, requestID, bound, gateErr)
			return
		}
		if bound != nil {
			s.Write(r, "mobile.approval.acc_gate_answered",
				"instance:"+body.InstanceID+"/session:"+body.SessionID+"/request:"+requestID,
				AuditFields{Detail: "dispatch:" + bound.ACCDispatchID + "/decision:" + body.Decision, Success: true})
		}
	}

	reply := adapter.PermissionReply(decision)
	if err := s.permMgr.ReplyForWorkspace(r.Context(), workspaceID, body.InstanceID, body.SessionID, requestID, reply, body.Message); err != nil {
		s.writeApprovalManagerError(w, r, err)
		return
	}
	s.recordMobileApprovalAudit(r, "permission_"+body.Decision, body.InstanceID, body.SessionID, requestID)
	s.writeApprovalConfirmed(w, r, requestID, body.Decision)
}

// accGateFailure classifies why the ACC approval interlock forced a deny.
// The class (never the raw upstream body) is what lands in the audit trail
// and the 502 envelope.
type accGateFailure struct {
	cause error
	class string // binding_lookup_failed | gate_unavailable | acc_error
}

func (e *accGateFailure) Error() string {
	return "acc gate failure (" + e.class + "): " + e.cause.Error()
}
func (e *accGateFailure) Unwrap() error { return e.cause }

// enforceACCPermissionGate applies the Pocket↔ACC interlock for one
// permission reply. Returns:
//
//	(bound=nil, err=nil)  → gate not applicable (unbound); original behavior
//	(bound,     err=nil)  → ACC answered; continue the local forward
//	(bound-or-nil, err)   → fail closed: deny locally + 502 acc_gate_unavailable
//
// bound is non-nil on gate failures caused by the ACC call so the response
// can carry the binding audit fields; lookup failures leave it nil because
// the binding could not be established.
func (s *Server) enforceACCPermissionGate(r *http.Request, workspaceID, requestID string, decision auth.Decision, body mobilePermissionReplyBody) (*task.Task, error) {
	// accRuntime 非 nil 而 taskStore 为 nil 是装配错误：无法判定绑定状态
	// 就无法判定 gate 是否适用，必须 fail-closed。
	if s.taskStore == nil {
		return nil, &accGateFailure{cause: errors.New("task store unavailable"), class: "binding_lookup_failed"}
	}
	bound, err := s.taskStore.FindTaskBySessionScoped(r.Context(), workspaceID, body.InstanceID, body.SessionID)
	if err != nil {
		return nil, &accGateFailure{cause: err, class: "binding_lookup_failed"}
	}
	// 无链接或未绑定 ACC dispatch → gate 不适用，保持原行为。
	if bound == nil || bound.ACCDispatchID == "" {
		return nil, nil
	}
	// OptionID 透传原始 decision 字符串（once/always/reject）；reject 映射
	// 为 allow=false。缺 source_ref/correlation_id 时 AnswerPermission 在
	// 客户端侧校验失败 → 同样落入 fail-closed 分支。
	if err := s.accRuntime.AnswerPermission(r.Context(), accruntime.PermissionDecision{
		DispatchID:    bound.ACCDispatchID,
		ToolCallID:    requestID,
		SourceRef:     bound.ACCSourceRef,
		CorrelationID: bound.ACCCorrelationID,
		OptionID:      string(decision),
		Allow:         decision != auth.DecisionReject,
		Reason:        body.Message,
	}); err != nil {
		class := "acc_error"
		if errors.Is(err, accruntime.ErrGateUnavailable) {
			class = "gate_unavailable"
		}
		return bound, &accGateFailure{cause: err, class: class}
	}
	return bound, nil
}

// failClosedPermissionReply is the deny side of the interlock: best-effort
// reject on the local OpenCode so the tool call cannot hang, an audit
// entry, and a 502 acc_gate_unavailable envelope with binding audit fields.
func (s *Server) failClosedPermissionReply(w http.ResponseWriter, r *http.Request, workspaceID string, body mobilePermissionReplyBody, requestID string, bound *task.Task, gateErr error) {
	if s.permMgr != nil {
		if err := s.permMgr.ReplyForWorkspace(r.Context(), workspaceID, body.InstanceID, body.SessionID, requestID,
			adapter.PermissionReply(auth.DecisionReject), "acc gate unavailable: denied fail-closed"); err != nil {
			log.Printf("[mobile-approval] fail-closed local reject failed: instance=%s session=%s request=%s err=%v",
				body.InstanceID, body.SessionID, requestID, err)
		}
	}

	class := "acc_error"
	var gateFailure *accGateFailure
	if errors.As(gateErr, &gateFailure) {
		class = gateFailure.class
	}
	s.Write(r, "mobile.approval.acc_gate_unavailable",
		"instance:"+body.InstanceID+"/session:"+body.SessionID+"/request:"+requestID,
		AuditFields{Detail: fmt.Sprintf("class=%s acc_echo_task_id=%s acc_echo_dispatch_id=%s",
			class, body.ACCTaskID, body.ACCDispatchID), Success: false})

	// 结构化错误信封：与 writeStructuredError 同形，附带绑定审计字段。
	// 出于脱敏考虑只带错误类别，不透传 ACC 响应体原文。
	envelope := map[string]any{
		"error":      "acc permission gate unavailable; permission denied fail-closed",
		"code":       "acc_gate_unavailable",
		"retryable":  true,
		"gate_class": class,
	}
	if r != nil {
		envelope["request_id"] = s.requestIDFromContext(r)
	}
	if bound != nil {
		envelope["task_id"] = bound.ID
		envelope["acc_task_id"] = bound.ACCTaskID
		envelope["acc_dispatch_id"] = bound.ACCDispatchID
	}
	writeJSON(w, http.StatusBadGateway, envelope)
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
	if s.claimsFromContext(r) == nil {
		return
	}
	s.Write(r, "mobile.approval."+action,
		"instance:"+instanceID+"/session:"+sessionID+"/request:"+requestID,
		AuditFields{Detail: "upstream_confirmed", Success: true})
}
