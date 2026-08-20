package kxmemory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPathsNormalization 验证当用户传入空路径时回退到默认值，并保留自定义值。
func TestPathsNormalization(t *testing.T) {
	p := NormalizePaths(Paths{})
	if p.NoteClassify != DefaultPaths.NoteClassify {
		t.Fatalf("note classify fallback wrong: %q", p.NoteClassify)
	}
	if p.EmailClassify != DefaultPaths.EmailClassify {
		t.Fatalf("email classify fallback wrong: %q", p.EmailClassify)
	}
	if p.DailySummary != DefaultPaths.DailySummary {
		t.Fatalf("daily summary fallback wrong: %q", p.DailySummary)
	}
	custom := NormalizePaths(Paths{
		NoteClassify:  "/custom/note",
		EmailClassify: "", // empty should fall back
		DailySummary:  "/custom/summary",
	})
	if custom.NoteClassify != "/custom/note" || custom.DailySummary != "/custom/summary" {
		t.Fatalf("custom paths lost: %+v", custom)
	}
	if custom.EmailClassify != DefaultPaths.EmailClassify {
		t.Fatalf("partial empty should fall back to default, got %q", custom.EmailClassify)
	}
}

// TestClassifyNoteUsesConfiguredPath 验证 ClassifyNote 走用户配置的路径而非硬编码默认。
func TestClassifyNoteUsesConfiguredPath(t *testing.T) {
	const wantPath = "/api/kx/v2/notes/classify"
	var observed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","classification":{"domain":"work","category":"meeting","tags":[],"confidence":0.5}}`))
	}))
	defer srv.Close()

	c := NewClientWithPaths(srv.URL, "", NoRetry, Paths{NoteClassify: wantPath})
	if _, err := c.ClassifyNote(context.Background(), ClassifyNoteRequest{Content: "x"}); err != nil {
		t.Fatalf("ClassifyNote: %v", err)
	}
	if observed != wantPath {
		t.Fatalf("expected %s, observed %s", wantPath, observed)
	}
}

// TestDailySummaryUsesConfiguredPath 同上但覆盖 DailySummary。
func TestDailySummaryUsesConfiguredPath(t *testing.T) {
	const wantPath = "/kx/v3/daily-summary"
	var observed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"date":"2026-07-16","summary":"hi","breakdown":[],"todos":[]}`))
	}))
	defer srv.Close()

	c := NewClientWithPaths(srv.URL, "", NoRetry, Paths{DailySummary: wantPath})
	if _, err := c.DailySummary(context.Background(), DailySummaryRequest{Date: "2026-07-16"}); err != nil {
		t.Fatalf("DailySummary: %v", err)
	}
	if observed != wantPath {
		t.Fatalf("expected %s, observed %s", wantPath, observed)
	}
}