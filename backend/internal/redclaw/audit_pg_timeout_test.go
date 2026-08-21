package redclaw

import (
	"context"
	"testing"
	"time"
)

func TestWithAuditQueryTimeoutAddsDefaultDeadline(t *testing.T) {
	ctx, cancel := withAuditQueryTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected default deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > auditQueryTimeout {
		t.Fatalf("deadline remaining=%v, want within %v", remaining, auditQueryTimeout)
	}
}

func TestWithAuditQueryTimeoutPreservesShorterDeadline(t *testing.T) {
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, release := withAuditQueryTimeout(short)
	defer release()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected caller deadline")
	}
	shortDeadline, _ := short.Deadline()
	if !deadline.Equal(shortDeadline) {
		t.Fatalf("deadline=%v, want caller deadline=%v", deadline, shortDeadline)
	}
}
