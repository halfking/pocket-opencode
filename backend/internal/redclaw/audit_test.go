package redclaw

import (
	"testing"
	"time"
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

	entries, err := store.Query(AuditQuery{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for t1, got %d", len(entries))
	}

	entries, _ = store.Query(AuditQuery{UserID: "user-1"})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for user-1, got %d", len(entries))
	}
}

func TestAuditLog_QueryHonorsExclusionAndOrder(t *testing.T) {
	store := NewAuditStore()
	ts := time.UnixMilli(1_000_000)
	for _, entry := range []*AuditEntry{
		{ID: "aud_a", Action: "chat.send", UserID: "u", TenantID: "t", Timestamp: ts},
		{ID: "aud_c", Action: "chat.send", UserID: "u", TenantID: "t", Timestamp: ts},
		{ID: "aud_b", Action: "chat.send", UserID: "u", TenantID: "t", Timestamp: ts},
		{ID: "aud_d", Action: "audit.export", UserID: "u", TenantID: "t", Timestamp: ts.Add(time.Second)},
	} {
		if err := store.Record(entry); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := store.Query(AuditQuery{TenantID: "t", ExcludeAction: "audit.export"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%d, want 3", len(entries))
	}
	for i, id := range []string{"aud_c", "aud_b", "aud_a"} {
		if entries[i].ID != id {
			t.Fatalf("entry[%d]=%q, want %q", i, entries[i].ID, id)
		}
	}
}

func TestAuditLog_QueryReturnsCopy(t *testing.T) {
	store := NewAuditStore()
	entry := &AuditEntry{Action: "chat.send", UserID: "user-1", TenantID: "t1", Detail: "original"}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	entry.Detail = "mutated input"

	entries, err := store.Query(AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Detail != "original" {
		t.Fatalf("stored entry was aliased: %+v", entries)
	}
	entries[0].Detail = "mutated result"

	again, err := store.Query(AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Detail != "original" {
		t.Fatalf("query result was aliased: %+v", again[0])
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

func TestAuditLog_NilEntry(t *testing.T) {
	store := NewAuditStore()

	err := store.Record(nil)
	if err == nil {
		t.Error("expected error for nil entry")
	}
}

func TestAuditLog_ConcurrentAccess(t *testing.T) {
	store := NewAuditStore()

	const goroutines = 10
	const entriesPerGoroutine = 100

	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < entriesPerGoroutine; j++ {
				store.Record(&AuditEntry{
					Action:   "test",
					UserID:   "user-" + string(rune(id)),
					TenantID: "tenant-1",
				})
			}
			done <- true
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	entries, _ := store.Query(AuditQuery{Limit: 10000})
	if len(entries) != goroutines*entriesPerGoroutine {
		t.Errorf("expected %d entries, got %d", goroutines*entriesPerGoroutine, len(entries))
	}
}
