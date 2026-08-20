// Package auth includes the scope + confirmation validation helpers
// for the mobile approval flow (PR6 of optimization v4).
//
// Background:
//
//   - The mobile client posts to ReplyPermission / ReplyQuestion /
//     RejectQuestion with (instanceID, sessionID, requestID, decision).
//   - Per docs/优化v4/02-安全审计与整改清单.md §3 (SEC-03/04) and
//     docs/优化v4/15-PR1-契约冻结与发布前置.md §5, every resource
//     mutation must verify user/workspace/instance/session/request scope
//     before contacting the upstream OpenCode/ACP target.
//   - Approval replies MUST only succeed once the upstream target has
//     confirmed receipt; the response from this package tells the
//     handler whether to write 200/202 or 409/500.
//
// PR6 boundary (per docs/优化v4/14 §2 row 6):
//   - This file introduces the validation layer and audit fields.
//   - Wiring into the live router (server.go handleMobileSessionRouter)
//     happens in PR6 too, but the public surface here is the Validate* /
//     AuditEntry helpers — they do not depend on a running registry and
//     are unit-testable in isolation.
//
// PR6 does NOT:
//   - Touch the dead-code mobile_api.go (file's own comment forbids it).
//   - Modify upstream OpenCode/ACP adapters.
//   - Persist approvals (P1 — see docs/优化v4/04 §3).
package auth

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Decision is the canonical reply enum shared between permission and
// question requests. Strings mirror the PR1 capability matrix.
type Decision string

const (
	DecisionOnce   Decision = "once"
	DecisionAlways Decision = "always"
	DecisionReject Decision = "reject"
	DecisionAnswer Decision = "answer"
)

// Valid returns true when d is one of the canonical decision strings.
func (d Decision) Valid() bool {
	switch d {
	case DecisionOnce, DecisionAlways, DecisionReject, DecisionAnswer:
		return true
	}
	return false
}

// ScopeContext is the minimal actor context extracted from the JWT and
// request, used for scope validation. Wire this from Claims in the
// handler (see server_identity.go).
type ScopeContext struct {
	UserID       string
	Role         string
	WorkspaceID  string
	Capabilities []string
}

// ApprovalTarget describes the upstream target of an approval action.
// It mirrors the (workspace, instance, session, request) tuple from
// PR1 §5 so the validator can check every dimension.
type ApprovalTarget struct {
	WorkspaceID string // may be empty when target is a shared/legacy instance
	InstanceID  string
	SessionID   string
	RequestID   string
	// CapabilityCheck lists the capability strings the caller must hold
	// (e.g. "session.read", "session.cancel"). Empty means "no check".
	CapabilityCheck []string
}

// AuditEntry captures the audit fields every successful approval reply
// must produce. See PR1 §11 for the full schema. We deliberately do not
// include payload, token, or session prompt here.
type AuditEntry struct {
	AuditID        string    `json:"audit_id"`
	ActorID        string    `json:"actor_id"`
	ActorRole      string    `json:"actor_role"`
	WorkspaceID    string    `json:"workspace_id"`
	ActionType     string    `json:"action_type"` // "permission_reply" | "question_reply" | "question_reject"
	ResourceType   string    `json:"resource_type"`
	ResourceID     string    `json:"resource_id"` // request_id
	CorrelationID  string    `json:"correlation_id"`
	PolicyDecision string    `json:"policy_decision"` // "allow" | "deny"
	Result         string    `json:"result"`          // "success" | "failure" | "partial"
	InputDigest    string    `json:"input_digest,omitempty"`
	OutputDigest   string    `json:"output_digest,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ValidationError is returned when scope or shape validation fails. The
// handler should map it to a stable error code per PR1 §10.
//
// Stable codes produced by this package:
//
//   - "workspace_required" — actor has no workspace_id (must pick one).
//   - "capability_denied"  — actor lacks one of CapabilityCheck.
//   - "not_found"          — resource tuple is incomplete or unknown;
//     do NOT leak the difference between "no
//     such request" and "wrong workspace".
//   - "payload_too_large"  — message body exceeds the policy limit.
//   - "unauthenticated"    — actor has no user_id (no JWT / expired).
//   - "invalid_decision"   — decision is not one of the canonical four.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// IsValidationError lets callers type-assert without importing errors.As
// in tight loops.
func IsValidationError(err error) (*ValidationError, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v, true
	}
	return nil, false
}

// Limits enforced by this package. Exposed as vars so tests can override
// them without exporting individual constants.
var (
	MaxMessageBytes = 4096
	MaxAnswersCount = 16
	MaxAnswerBytes  = 1024
)

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9_\-:]{1,128}$`)
	uuidLikeRe = regexp.MustCompile(`^[A-Za-z0-9_\-:.]{1,128}$`)
)

// ValidateScope ensures the actor and target share the same workspace
// (or the target is a shared/legacy instance) and that all four scope
// dimensions are well-formed.
func ValidateScope(actor ScopeContext, target ApprovalTarget) error {
	if strings.TrimSpace(actor.UserID) == "" {
		return &ValidationError{Code: "unauthenticated", Message: "missing user_id in claims"}
	}
	if strings.TrimSpace(target.InstanceID) == "" {
		return &ValidationError{Code: "not_found", Message: "instance_id is required"}
	}
	if !uuidLikeRe.MatchString(target.InstanceID) {
		return &ValidationError{Code: "not_found", Message: "instance_id has invalid shape"}
	}
	if strings.TrimSpace(target.SessionID) == "" {
		return &ValidationError{Code: "not_found", Message: "session_id is required"}
	}
	if !uuidLikeRe.MatchString(target.SessionID) {
		return &ValidationError{Code: "not_found", Message: "session_id has invalid shape"}
	}
	if strings.TrimSpace(target.RequestID) == "" {
		return &ValidationError{Code: "not_found", Message: "request_id is required"}
	}
	if !idPattern.MatchString(target.RequestID) {
		return &ValidationError{Code: "not_found", Message: "request_id has invalid shape"}
	}
	// Workspace binding: actor.workspace_id must equal target.workspace_id
	// unless the target is a shared/legacy instance (empty workspace_id),
	// which must be rejected for write actions to avoid cross-tenant push.
	if target.WorkspaceID == "" {
		return &ValidationError{
			Code:    "not_found",
			Message: "target has no workspace_id; shared instances require explicit admin policy",
		}
	}
	if strings.TrimSpace(actor.WorkspaceID) == "" {
		return &ValidationError{Code: "workspace_required", Message: "missing workspace_id in claims"}
	}
	if actor.WorkspaceID != target.WorkspaceID {
		return &ValidationError{
			Code:    "not_found",
			Message: "target workspace does not match actor",
		}
	}
	return nil
}

// ValidateDecision checks that d is canonical and that any associated
// payload respects the per-resource size limits.
func ValidateDecision(d Decision, message string, answers []string) error {
	if !d.Valid() {
		return &ValidationError{Code: "invalid_decision", Message: fmt.Sprintf("unknown decision %q", string(d))}
	}
	if len(message) > MaxMessageBytes {
		return &ValidationError{
			Code:    "payload_too_large",
			Message: fmt.Sprintf("message exceeds %d bytes", MaxMessageBytes),
		}
	}
	if len(answers) > MaxAnswersCount {
		return &ValidationError{
			Code:    "payload_too_large",
			Message: fmt.Sprintf("more than %d answers", MaxAnswersCount),
		}
	}
	for i, a := range answers {
		if len(a) > MaxAnswerBytes {
			return &ValidationError{
				Code:    "payload_too_large",
				Message: fmt.Sprintf("answer[%d] exceeds %d bytes", i, MaxAnswerBytes),
			}
		}
	}
	return nil
}

// BuildAuditEntry produces the audit fields for a successful reply. The
// handler must persist this via the existing audit store. InputDigest /
// OutputDigest are intentionally not computed here; callers that need
// them can compute hashes around the request body before calling.
func BuildAuditEntry(actor ScopeContext, target ApprovalTarget, decision Decision, correlationID, auditID string) AuditEntry {
	if auditID == "" {
		auditID = newAuditID()
	}
	if correlationID == "" {
		correlationID = "approval:" + target.RequestID
	}
	actionType := actionTypeFor(decision)
	return AuditEntry{
		AuditID:        auditID,
		ActorID:        actor.UserID,
		ActorRole:      actor.Role,
		WorkspaceID:    target.WorkspaceID,
		ActionType:     actionType,
		ResourceType:   "approval_request",
		ResourceID:     target.RequestID,
		CorrelationID:  correlationID,
		PolicyDecision: "allow",
		Result:         "success",
		CreatedAt:      time.Now().UTC(),
	}
}

func actionTypeFor(d Decision) string {
	switch d {
	case DecisionOnce, DecisionAlways, DecisionReject:
		return "permission_reply"
	case DecisionAnswer:
		return "question_reply"
	}
	return "approval_reply"
}

// newAuditID returns a time-prefixed + random-suffix id, mirroring the
// helper in the frontend async state module so ids sort chronologically.
func newAuditID() string {
	const charset = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	now := time.Now().UnixMilli()
	b := make([]byte, 10)
	x := uint32(now)
	for i := 0; i < 6; i++ {
		b[i] = charset[x&0x1f]
		x >>= 5
	}
	for i := 6; i < 10; i++ {
		b[i] = charset[int(uint32(time.Now().UnixNano())>>(i*5))&0x1f]
	}
	return "01" + string(b)
}
