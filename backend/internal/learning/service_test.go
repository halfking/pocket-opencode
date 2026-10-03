package learning

import (
	"context"
	"testing"
	"time"
)

// FakeStore is not used: the store needs Postgres. These tests target the pure
// parts of the service (validation, recurrence maths) that decide *when* a
// reminder fires 鈥?the part that silently breaks reminders when it is wrong.

func TestCaptureRequestValidation(t *testing.T) {
	cases := []struct {
		name string
		req  CaptureRequest
		ok   bool
	}{
		{"note ok", CaptureRequest{SourceKind: "note", SourceID: "n1", Title: "T"}, true},
		{"email ok", CaptureRequest{SourceKind: "email", SourceID: "e1", Title: "T"}, true},
		// Title is optional on the wire (resolved from the source row by
		// captureTitleFromSource), so validation must not reject it. The
		// "no title and no resolver" case is covered in resolver_test.go.
		{"title optional", CaptureRequest{SourceKind: "note", SourceID: "n1"}, true},
		{"manual without source id", CaptureRequest{SourceKind: "manual", Title: "T"}, true},
		{"unknown source", CaptureRequest{SourceKind: "sms", SourceID: "x", Title: "T"}, false},
		{"empty source", CaptureRequest{Title: "T"}, false},
		{"note without id", CaptureRequest{SourceKind: "note", Title: "T"}, false},
		{"bad stage", CaptureRequest{SourceKind: "note", SourceID: "n1", Title: "T", Stage: "done"}, false},
		{"importance too high", CaptureRequest{SourceKind: "note", SourceID: "n1", Title: "T", Importance: 9}, false},
		{"importance negative", CaptureRequest{SourceKind: "note", SourceID: "n1", Title: "T", Importance: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.req.Validate()
			if tc.ok && msg != "" {
				t.Fatalf("Validate() = %q, want accepted", msg)
			}
			if !tc.ok && msg == "" {
				t.Fatalf("Validate() = \"\", want a rejection message")
			}
		})
	}
}

func TestUpsertReminderRequestValidation(t *testing.T) {
	ok := UpsertReminderRequest{Kind: "daily_digest", RuleKind: "daily", RuleValue: "20:30", NextDueAt: 100}
	if msg := ok.Validate(); msg != "" {
		t.Fatalf("valid request rejected: %s", msg)
	}
	for name, req := range map[string]UpsertReminderRequest{
		"bad kind":         {Kind: "nag", RuleKind: "daily", RuleValue: "20:30", NextDueAt: 100},
		"bad rule kind":    {Kind: "daily_digest", RuleKind: "weekly", RuleValue: "20:30", NextDueAt: 100},
		"no due":           {Kind: "daily_digest", RuleKind: "daily", RuleValue: "20:30"},
		"bad daily value":  {Kind: "daily_digest", RuleKind: "daily", RuleValue: "8pm", NextDueAt: 100},
		"bad hour value":   {Kind: "daily_digest", RuleKind: "daily", RuleValue: "29:30", NextDueAt: 100},
		"bad minute value": {Kind: "daily_digest", RuleKind: "daily", RuleValue: "20:75", NextDueAt: 100},
	} {
		if msg := req.Validate(); msg == "" {
			t.Errorf("%s: Validate() = \"\", want a rejection", name)
		}
	}
}

func TestParseHHMM(t *testing.T) {
	if m, ok := parseHHMM("20:30"); !ok || m != 20*60+30 {
		t.Errorf("parseHHMM(20:30) = %d,%v want 1230,true", m, ok)
	}
	if m, ok := parseHHMM("07:05"); !ok || m != 7*60+5 {
		t.Errorf("parseHHMM(07:05) = %d,%v want 425,true", m, ok)
	}
	for _, bad := range []string{"", "7:05", "0730", "24:00", "20:60", "ab:cd", "20-30"} {
		if _, ok := parseHHMM(bad); ok {
			t.Errorf("parseHHMM(%q) accepted, want rejected", bad)
		}
	}
}

func TestNextDailyOccurrencePicksTodayThenTomorrow(t *testing.T) {
	// 2026-01-01 00:00:00 UTC
	dayStart := int64(1767225600)
	at := 20*60 + 30
	atSec := int64(at) * 60

	// 10:00 -> today's 20:30
	now := dayStart + 10*3600
	if got := nextDailyOccurrence(now, at, 0); got != dayStart+atSec {
		t.Errorf("before the slot: got %d, want %d", got, dayStart+atSec)
	}
	// 21:00 -> tomorrow's 20:30
	now = dayStart + 21*3600
	if got := nextDailyOccurrence(now, at, 0); got != dayStart+86400+atSec {
		t.Errorf("after the slot: got %d, want %d", got, dayStart+86400+atSec)
	}
	// Exactly at the slot -> tomorrow (the slot was just handled, do not double fire).
	now = dayStart + atSec
	if got := nextDailyOccurrence(now, at, 0); got != dayStart+86400+atSec {
		t.Errorf("at the slot: got %d, want %d", got, dayStart+86400+atSec)
	}
}

// A reminder scheduled for 03:00 while the quiet window ends at 07:30 must be
// pushed to 07:30, otherwise the user is woken up by a learning nag.
func TestNextAfterQuietHoursDefersToWindowEnd(t *testing.T) {
	dayStart := int64(1767225600)
	now := dayStart + 2*3600       // 02:00
	candidate := dayStart + 3*3600 // 03:00 鈥?inside the quiet window
	got := nextAfterQuietHours(candidate, now, 7*60+30)
	want := dayStart + int64(7*60+30)*60
	if got != want {
		t.Errorf("deferred to %d, want %d (07:30)", got, want)
	}
	// Outside the quiet window the time is untouched.
	candidate2 := dayStart + 12*3600
	now2 := dayStart + 10*3600
	if got := nextAfterQuietHours(candidate2, now2, 7*60+30); got != candidate2 {
		t.Errorf("outside quiet hours: got %d, want %d", got, candidate2)
	}
	// No quiet hours configured -> no deferral at all.
	if got := nextAfterQuietHours(candidate, now, 0); got != candidate {
		t.Errorf("quiet hours disabled: got %d, want %d", got, candidate)
	}
}

func TestNextOccurrenceRuleHandling(t *testing.T) {
	now := int64(1767225600 + 10*3600) // 10:00

	if _, repeat := nextOccurrence(Reminder{RuleKind: "once", NextDueAt: 5}, now, 0); repeat {
		t.Errorf("a once rule must not repeat")
	}
	if next, _ := nextOccurrence(Reminder{RuleKind: "interval", RuleValue: "30"}, now, 0); next != now+1800 {
		t.Errorf("interval rule: got %d, want %d", next, now+1800)
	}
	if _, repeat := nextOccurrence(Reminder{RuleKind: "interval", RuleValue: "abc"}, now, 0); !repeat {
		t.Errorf("a malformed interval must fall back to repeating, not spin")
	}
	if next, _ := nextOccurrence(Reminder{RuleKind: "interval", RuleValue: "abc"}, now, 0); next != now+86400 {
		t.Errorf("malformed interval fallback: got %d, want %d", next, now+86400)
	}
	if _, repeat := nextOccurrence(Reminder{RuleKind: "daily", RuleValue: "nope"}, now, 0); !repeat {
		t.Errorf("a malformed daily rule must fall back to repeating, not spin")
	}
}

func TestDueSummaryEmptyPolicy(t *testing.T) {
	if !(DueSummary{}).Empty() {
		t.Errorf("a zero summary must be Empty so the digest stays silent")
	}
	if (DueSummary{DueCards: 1}).Empty() {
		t.Errorf("a summary with a due card must not be Empty")
	}
	if (DueSummary{DueTasks: 1}).Empty() {
		t.Errorf("a summary with a due task must not be Empty")
	}
}

// The service must fail closed when it has no store, instead of panicking or
// silently reporting "nothing due" (which would mean never reminding anyone).
func TestServiceWithoutStoreFailsClosed(t *testing.T) {
	s := NewService(nil, nil, nil)
	ctx := context.Background()
	if _, _, err := s.Capture(ctx, "w", "u", CaptureRequest{SourceKind: "note", SourceID: "n", Title: "t"}, func() string { return "id" }); err == nil {
		t.Errorf("Capture without a store must error")
	}
	if _, err := s.DueSummary(ctx, "w", "u"); err == nil {
		t.Errorf("DueSummary without a store must error")
	}
}

func TestServiceCaptureRejectsInvalidRequestBeforeHittingTheStore(t *testing.T) {
	s := NewService(nil, nil, nil)
	ctx := context.Background()
	// The store is nil, so any error here must come from validation, not from
	// a nil-pointer dereference.
	if _, _, err := s.Capture(ctx, "w", "u", CaptureRequest{SourceKind: "sms", Title: "t"}, func() string { return "id" }); err == nil {
		t.Fatalf("invalid source kind must be rejected")
	}
}

func TestServiceNowFuncIsUsedForSummary(t *testing.T) {
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).Unix()
	s := NewService(nil, nil, nil)
	s.SetNowFunc(func() int64 { return fixed })
	if got := s.now(); got != fixed {
		t.Errorf("now() = %d, want %d", got, fixed)
	}
	// A nil override must not break the clock.
	s.SetNowFunc(nil)
	if s.now() <= 0 {
		t.Errorf("now() = %d after clearing the override, want a real clock value", s.now())
	}
}

func TestNormalizeWorkspaceDefaults(t *testing.T) {
	if got := normalizeWorkspace(""); got != DefaultWorkspaceID {
		t.Errorf("normalizeWorkspace(\"\") = %q, want %q", got, DefaultWorkspaceID)
	}
	if got := normalizeWorkspace("ws1"); got != "ws1" {
		t.Errorf("normalizeWorkspace(ws1) = %q, want ws1", got)
	}
}
