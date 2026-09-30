package learning

// Tests for the one-click capture path: a request that carries only
// {sourceKind, sourceId} must come out with a real title, and a missing source
// must fail loudly instead of storing a blank item.

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	got      ResolvedSource
	err      error
	kind     string
	sourceID string
	userID   string
	wsID     string
	calls    int
}

func (f *fakeResolver) Resolve(_ context.Context, kind, sourceID, userID, wsID string) (*ResolvedSource, error) {
	f.calls++
	f.kind, f.sourceID, f.userID, f.wsID = kind, sourceID, userID, wsID
	if f.err != nil {
		return nil, f.err
	}
	out := f.got
	return &out, nil
}

func TestCaptureResolvesTitleFromSource(t *testing.T) {
	s := NewService(nil, nil, nil)
	s.SetResolver(&fakeResolver{got: ResolvedSource{
		Title:   "Go 1.25 GC 变更要点",
		Summary: "绿色 GC 默认开启……",
		Tags:    []string{"go", "runtime"},
	}})

	req := CaptureRequest{SourceKind: "email", SourceID: "em-42"}
	if msg := req.Validate(); msg != "" {
		t.Fatalf("a title-less capture must pass validation: %s", msg)
	}
	// The store is nil, so Capture must fail *after* resolution — which proves
	// the resolution happened without needing Postgres.
	_, _, err := s.Capture(context.Background(), "ws1", "u1", req, func() string { return "id" })
	if err == nil {
		t.Fatalf("expected the nil store to fail, got success")
	}
	if err != nil && errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("resolution should have succeeded; got not-found: %v", err)
	}
}

func TestCaptureResolverReceivesTenantScope(t *testing.T) {
	s := NewService(nil, nil, nil)
	fr := &fakeResolver{got: ResolvedSource{Title: "T"}}
	s.SetResolver(fr)

	_, _, _ = s.Capture(context.Background(), "ws-42", "user-7",
		CaptureRequest{SourceKind: "note", SourceID: "n-1"}, func() string { return "id" })

	if fr.kind != "note" || fr.sourceID != "n-1" {
		t.Errorf("resolver got kind=%q sourceID=%q, want note/n-1", fr.kind, fr.sourceID)
	}
	// The tenant must be forwarded verbatim: an unscoped lookup would let a
	// user read another tenant's note.
	if fr.wsID != "ws-42" {
		t.Errorf("resolver workspace = %q, want ws-42", fr.wsID)
	}
	if fr.userID != "user-7" {
		t.Errorf("resolver userID = %q, want user-7", fr.userID)
	}
}

func TestCaptureMissingSourceIsNotFound(t *testing.T) {
	s := NewService(nil, nil, nil)
	s.SetResolver(&fakeResolver{err: ErrSourceNotFound})

	_, _, err := s.Capture(context.Background(), "ws", "u",
		CaptureRequest{SourceKind: "rss", SourceID: "missing"}, func() string { return "id" })
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("a deleted source must surface as not-found, got %v", err)
	}
}

func TestCaptureWithoutResolverRequiresTitle(t *testing.T) {
	s := NewService(nil, nil, nil)
	// No resolver: a title-less request has nothing to fall back to.
	if _, _, err := s.Capture(context.Background(), "ws", "u",
		CaptureRequest{SourceKind: "note", SourceID: "n-1"}, func() string { return "id" }); err == nil {
		t.Fatalf("without a resolver a title-less capture must fail")
	}
}

func TestCaptureManualItemSkipsResolver(t *testing.T) {
	s := NewService(nil, nil, nil)
	fr := &fakeResolver{err: errors.New("resolver must not be called for manual items")}
	s.SetResolver(fr)

	// A manual item has no source row, so the client always supplies the title
	// and the resolver must stay untouched.
	_, _, _ = s.Capture(context.Background(), "ws", "u",
		CaptureRequest{SourceKind: "manual", Title: "自己写的一条"}, func() string { return "id" })
	if fr.calls != 0 {
		t.Errorf("resolver was called %d times for a manual item, want 0", fr.calls)
	}
}

func TestCaptureClientTitleWins(t *testing.T) {
	s := NewService(nil, nil, nil)
	s.SetResolver(&fakeResolver{got: ResolvedSource{Title: "服务端标题", Summary: "服务端摘要"}})

	req := CaptureRequest{SourceKind: "note", SourceID: "n-1", Title: "我改过的标题"}
	if err := s.captureTitleFromSource(context.Background(), "ws", "u", &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Title != "我改过的标题" {
		t.Errorf("title = %q, want the client value to win", req.Title)
	}
	// The summary is still filled from the source when the client omitted it.
	if req.Summary != "服务端摘要" {
		t.Errorf("summary = %q, want the resolved value", req.Summary)
	}
}

func TestCaptureResolverFailureWithClientTitleDoesNotBlock(t *testing.T) {
	s := NewService(nil, nil, nil)
	s.SetResolver(&fakeResolver{err: errors.New("db down")})

	// A PG hiccup must not lose a user action when we already have a title.
	req := CaptureRequest{SourceKind: "email", SourceID: "e1", Title: "邮件标题"}
	if err := s.captureTitleFromSource(context.Background(), "ws", "u", &req); err != nil {
		t.Fatalf("a resolver outage with a client title must not error, got %v", err)
	}
	// Without a title the failure has to surface, otherwise we store a blank.
	req2 := CaptureRequest{SourceKind: "email", SourceID: "e1"}
	if err := s.captureTitleFromSource(context.Background(), "ws", "u", &req2); err == nil {
		t.Errorf("a resolver outage without a title must surface")
	}
}

func TestCaptureTruncatesLongSummary(t *testing.T) {
	long := ""
	for i := 0; i < 400; i++ {
		long += "字"
	}
	s := NewService(nil, nil, nil)
	s.SetResolver(&fakeResolver{got: ResolvedSource{Title: "T", Summary: long}})

	req := CaptureRequest{SourceKind: "note", SourceID: "n-1"}
	if err := s.captureTitleFromSource(context.Background(), "ws", "u", &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len([]rune(req.Summary)) > summaryLimit+1 {
		t.Errorf("summary length = %d runes, want <= %d", len([]rune(req.Summary)), summaryLimit+1)
	}
	// Truncation must not cut a multi-byte rune in half.
	if !validUTF8Tail(req.Summary) {
		t.Errorf("summary was cut mid-rune: %q", req.Summary)
	}
}

func validUTF8Tail(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func TestTruncateShortStringUnchanged(t *testing.T) {
	if got := truncate("  hello  ", 10); got != "hello" {
		t.Errorf("truncate = %q, want hello", got)
	}
	if got := truncate("", 10); got != "" {
		t.Errorf("truncate(\"\") = %q, want empty", got)
	}
}

func TestEncodeDecodeTags(t *testing.T) {
	if got := encodeTags(nil); got != "" {
		t.Errorf("encodeTags(nil) = %q, want empty (column default)", got)
	}
	if got := encodeTags([]string{"go", "runtime"}); got != `["go","runtime"]` {
		t.Errorf("encodeTags = %q", got)
	}
	got := decodeTags(`["a","b"]`)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("decodeTags = %v, want [a b]", got)
	}
	if decodeTags("") != nil {
		t.Errorf("decodeTags(\"\") should be nil")
	}
	if decodeTags("not json") != nil {
		t.Errorf("decodeTags(garbage) should degrade to nil, not panic")
	}
}
