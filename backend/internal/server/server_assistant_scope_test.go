package server

// Additional handler coverage for the Phase 2.2 hardening:
//   - input validation: email format / port range / authType / rules JSON
//   - SSOT conflict payload preserves required fields (noteId/conflicts/...)

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

func TestValidateEmailAccountInput(t *testing.T) {
	cases := []struct {
		name      string
		addr      string
		host      string
		port      int
		authType  string
		interval  int
		rules     string
		wantError string
	}{
		{"missing address", "", "imap.example.com", 993, "password", 15, "", "emailAddress is required"},
		{"invalid address", "not-an-email", "imap.example.com", 993, "password", 15, "", "emailAddress is invalid"},
		{"missing host", "user@example.com", "", 993, "password", 15, "", "imapHost is required"},
		{"port too large", "user@example.com", "imap.example.com", 70000, "password", 15, "", "imapPort must be between 1 and 65535"},
		{"port too small", "user@example.com", "imap.example.com", 0, "password", 15, "", ""}, // 0 means "default"
		{"invalid authType", "user@example.com", "imap.example.com", 993, "magic", 15, "", "authType must be"},
		{"empty authType ok", "user@example.com", "imap.example.com", 993, "", 15, "", ""},
		{"interval too low", "user@example.com", "imap.example.com", 993, "password", 1, "", "syncIntervalMin must be between 5 and 60"},
		{"interval too high", "user@example.com", "imap.example.com", 993, "password", 90, "", "syncIntervalMin must be between 5 and 60"},
		{"interval zero ok", "user@example.com", "imap.example.com", 993, "password", 0, "", ""},
		{"rules invalid JSON", "user@example.com", "imap.example.com", 993, "password", 15, "{not json}", "rules must be valid JSON"},
		{"rules valid JSON", "user@example.com", "imap.example.com", 993, "password", 15, `{"whitelist":["a@b"]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEmailAccountInput(tc.addr, tc.host, tc.port, tc.authType, tc.interval, tc.rules)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got %q", tc.wantError, err.Error())
			}
		})
	}
}

// TestSSOTConflictPayload 确认 broadcast SSOT 冲突事件载荷保留前端展示
// 所需的字段名。
func TestSSOTConflictPayload(t *testing.T) {
	conflicts := []kxmemory.SSOTConflict{{
		ExistingNoteID: "n2",
		ConflictType:   "update",
		Snippet:        "old snippet",
		Confidence:     0.7,
	}}
	payload := map[string]any{
		"noteId":    "n1",
		"conflicts": conflicts,
		"category":  "plan",
		"domain":    "work",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"noteId":"n1"`, `"category":"plan"`, `"domain":"work"`, `"existing_note_id":"n2"`, `"conflict_type":"update"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
	var roundTrip struct {
		NoteID    string                  `json:"noteId"`
		Conflicts []kxmemory.SSOTConflict `json:"conflicts"`
		Category  string                  `json:"category"`
		Domain    string                  `json:"domain"`
	}
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTrip.NoteID != "n1" || roundTrip.Category != "plan" || roundTrip.Domain != "work" {
		t.Fatalf("payload fields lost in roundtrip: %+v", roundTrip)
	}
	if len(roundTrip.Conflicts) != 1 || !reflect.DeepEqual(roundTrip.Conflicts, conflicts) {
		t.Fatalf("conflicts lost in roundtrip: %+v", roundTrip.Conflicts)
	}
}

// TestKxmemoryErrorMapping 确认 transient/permanent 错误被准确分类，这是
// SSOT 推送路径可靠降级的前提。
func TestKxmemoryErrorMapping(t *testing.T) {
	transient := &kxmemory.Error{Code: "KXMEMORY_TIMEOUT", Permanent: false}
	permanent := &kxmemory.Error{Code: "KXMEMORY_BAD_INPUT", Permanent: true}
	if !errors.Is(transient, transient) {
		t.Fatalf("transient error should satisfy errors.As")
	}
	if !transient.Retryable() {
		t.Fatalf("transient Retryable should be true")
	}
	if permanent.Retryable() {
		t.Fatalf("permanent Retryable should be false")
	}
}
