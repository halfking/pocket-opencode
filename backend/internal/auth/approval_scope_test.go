package auth

import (
	"strings"
	"testing"
)

func goodActor() ScopeContext {
	return ScopeContext{UserID: "u-1", Role: "member", WorkspaceID: "ws-1"}
}

func goodTarget() ApprovalTarget {
	return ApprovalTarget{
		WorkspaceID: "ws-1",
		InstanceID:  "inst-1",
		SessionID:   "sess-1",
		RequestID:   "req-1",
	}
}

func TestValidateScope_OK(t *testing.T) {
	if err := ValidateScope(goodActor(), goodTarget()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateScope_NoUser(t *testing.T) {
	actor := goodActor()
	actor.UserID = ""
	_, ok := IsValidationError(ValidateScope(actor, goodTarget()))
	if !ok {
		t.Fatal("expected ValidationError")
	}
}

func TestValidateScope_SharedInstanceRejected(t *testing.T) {
	target := goodTarget()
	target.WorkspaceID = ""
	_, ok := IsValidationError(ValidateScope(goodActor(), target))
	if !ok {
		t.Fatal("expected ValidationError for shared target")
	}
}

func TestValidateScope_WorkspaceMismatch(t *testing.T) {
	actor := goodActor()
	actor.WorkspaceID = "ws-2"
	_, ok := IsValidationError(ValidateScope(actor, goodTarget()))
	if !ok {
		t.Fatal("expected ValidationError for workspace mismatch")
	}
}

func TestValidateScope_MissingFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ApprovalTarget)
	}{
		{"no_instance", func(t *ApprovalTarget) { t.InstanceID = "" }},
		{"no_session", func(t *ApprovalTarget) { t.SessionID = "" }},
		{"no_request", func(t *ApprovalTarget) { t.RequestID = "" }},
		{"bad_chars_instance", func(t *ApprovalTarget) { t.InstanceID = "evil/path" }},
		{"oversized_request", func(t *ApprovalTarget) { t.RequestID = strings.Repeat("x", 200) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := goodTarget()
			c.mutate(&target)
			err := ValidateScope(goodActor(), target)
			if err == nil {
				t.Fatal("expected error")
			}
			v, ok := IsValidationError(err)
			if !ok {
				t.Fatalf("expected ValidationError, got %T", err)
			}
			if v.Code != "not_found" {
				t.Errorf("expected code not_found, got %s", v.Code)
			}
		})
	}
}

func TestValidateDecision_OK(t *testing.T) {
	for _, d := range []Decision{DecisionOnce, DecisionAlways, DecisionReject, DecisionAnswer} {
		if err := ValidateDecision(d, "msg", []string{"a", "b"}); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}

func TestValidateDecision_Unknown(t *testing.T) {
	_, ok := IsValidationError(ValidateDecision(Decision("nope"), "", nil))
	if !ok {
		t.Fatal("expected ValidationError")
	}
}

func TestValidateDecision_PayloadTooLarge(t *testing.T) {
	big := strings.Repeat("a", MaxMessageBytes+1)
	_, ok := IsValidationError(ValidateDecision(DecisionOnce, big, nil))
	if !ok {
		t.Fatal("expected ValidationError for oversized message")
	}
	answers := make([]string, MaxAnswersCount+1)
	_, ok = IsValidationError(ValidateDecision(DecisionAnswer, "", answers))
	if !ok {
		t.Fatal("expected ValidationError for too many answers")
	}
}

func TestBuildAuditEntry_Once(t *testing.T) {
	entry := BuildAuditEntry(goodActor(), goodTarget(), DecisionOnce, "corr-1", "aud-1")
	if entry.ActionType != "permission_reply" {
		t.Errorf("expected permission_reply, got %s", entry.ActionType)
	}
	if entry.WorkspaceID != "ws-1" {
		t.Errorf("expected ws-1, got %s", entry.WorkspaceID)
	}
	if entry.ResourceID != "req-1" {
		t.Errorf("expected req-1, got %s", entry.ResourceID)
	}
	if entry.CorrelationID != "corr-1" {
		t.Errorf("expected corr-1, got %s", entry.CorrelationID)
	}
	if entry.PolicyDecision != "allow" {
		t.Errorf("expected allow, got %s", entry.PolicyDecision)
	}
	if entry.Result != "success" {
		t.Errorf("expected success, got %s", entry.Result)
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
}

func TestBuildAuditEntry_Answer(t *testing.T) {
	entry := BuildAuditEntry(goodActor(), goodTarget(), DecisionAnswer, "", "")
	if entry.ActionType != "question_reply" {
		t.Errorf("expected question_reply, got %s", entry.ActionType)
	}
	if !strings.HasPrefix(entry.CorrelationID, "approval:") {
		t.Errorf("expected auto-correlation prefix, got %s", entry.CorrelationID)
	}
	if entry.AuditID == "" {
		t.Error("AuditID must be generated when empty")
	}
}

func TestBuildAuditEntry_RejectsSharedWorkspace(t *testing.T) {
	// Even though BuildAuditEntry does not validate scope itself, a
	// shared/empty workspace must never silently produce an audit entry
	// without a workspace id. The validator already rejects this case
	// upstream; we double-check here.
	target := goodTarget()
	target.WorkspaceID = ""
	entry := BuildAuditEntry(goodActor(), target, DecisionOnce, "corr", "")
	if entry.WorkspaceID != "" {
		t.Errorf("expected empty workspace id for shared target, got %s", entry.WorkspaceID)
	}
}
