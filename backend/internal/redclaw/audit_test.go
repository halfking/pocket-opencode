package redclaw

import (
	"testing"
)

func TestAuditLog_Create(t *testing.T) {
	store := NewAuditStore()

	entry := &AuditEntry{
		Action:     "chat.send",
		UserID:     "user-123",
		TenantID:   "enterprise-a",
		Resource:   "session/sess_abc",
		Detail:     "Sent message to AI",
		DurationMs: 1500,
	}

	err := store.Record(entry)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestAuditLog_Query(t *testing.T) {
	store := NewAuditStore()

	store.Record(&AuditEntry{Action: "chat.send", UserID: "user-1", TenantID: "t1"})
	store.Record(&AuditEntry{Action: "chat.send", UserID: "user-2", TenantID: "t1"})
	store.Record(&AuditEntry{Action: "file.read", UserID: "user-1", TenantID: "t2"})

	// 按租户查询
	entries, err := store.Query(AuditQuery{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for t1, got %d", len(entries))
	}

	// 按用户查询
	entries, _ = store.Query(AuditQuery{UserID: "user-1"})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for user-1, got %d", len(entries))
	}
}

func TestAuditLog_Flush(t *testing.T) {
	store := NewAuditStore()
	store.Record(&AuditEntry{Action: "test", UserID: "u1", TenantID: "t1"})

	entries := store.Flush()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	remaining, _ := store.Query(AuditQuery{})
	if len(remaining) != 0 {
		t.Errorf("expected 0 after flush, got %d", len(remaining))
	}
}