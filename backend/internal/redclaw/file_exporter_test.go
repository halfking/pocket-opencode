package redclaw

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newExporterStore(t *testing.T) *AuditStore {
	t.Helper()
	return NewAuditStore()
}

func TestFileExporterIncrementalExport(t *testing.T) {
	dir := t.TempDir()
	store := newExporterStore(t)
	exp := NewFileExporter(store, FileExporterConfig{Dir: dir, PageSize: 2})

	base := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := store.Record(&AuditEntry{Action: "test.a", TenantID: "ws-a", Success: true, Timestamp: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}

	// 第一轮：全部导出（分页 2 条 → 2 页）。
	n, err := exp.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}
	if n != 3 {
		t.Fatalf("exported %d entries, want 3", n)
	}
	name := "audit-" + base.Format("20060102") + ".jsonl"
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	lines := 0
	for _, line := range splitLines(raw) {
		if line == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid jsonl line: %v", err)
		}
		if e.TenantID != "ws-a" || e.Action != "test.a" {
			t.Fatalf("unexpected entry: %+v", e)
		}
		lines++
	}
	if lines != 3 {
		t.Fatalf("file has %d lines, want 3", lines)
	}

	// 第二轮：无新条目 → 0，且不产生重复行。
	n, err = exp.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce (idle): %v", err)
	}
	if n != 0 {
		t.Fatalf("idle export wrote %d entries, want 0", n)
	}

	// 新增 2 条 → 只导出新增。
	for i := 0; i < 2; i++ {
		if err := store.Record(&AuditEntry{Action: "test.b", TenantID: "ws-a", Success: true, Timestamp: base.Add(time.Duration(10+i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	n, err = exp.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce (new): %v", err)
	}
	if n != 2 {
		t.Fatalf("second export wrote %d entries, want 2", n)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, name))
	if got := countNonEmpty(splitLines(raw)); got != 5 {
		t.Fatalf("file has %d lines, want 5", got)
	}

	// 游标已持久化：新建 exporter（模拟重启）后无重复导出。
	exp2 := NewFileExporter(store, FileExporterConfig{Dir: dir})
	n, err = exp2.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce (restart): %v", err)
	}
	if n != 0 {
		t.Fatalf("after restart exported %d entries, want 0", n)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, name))
	if got := countNonEmpty(splitLines(raw)); got != 5 {
		t.Fatalf("after restart file has %d lines, want 5", got)
	}
}

func TestFileExporterRotatesByDateAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	store := newExporterStore(t)
	exp := NewFileExporter(store, FileExporterConfig{Dir: dir, RetainDays: 7})

	day1 := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	if err := store.Record(&AuditEntry{Action: "a", TenantID: "ws", Success: true, Timestamp: day1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(&AuditEntry{Action: "b", TenantID: "ws", Success: true, Timestamp: day2}); err != nil {
		t.Fatal(err)
	}
	if _, err := exp.ExportOnce(context.Background()); err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}

	// 按日期分成两个文件。
	for _, name := range []string{"audit-20260801.jsonl", "audit-20260802.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}

	// 把 day1 文件的 mtime 改到保留期之外 → cleanup 删除；state.json 不受影响。
	old := time.Now().AddDate(0, 0, -10)
	if err := os.Chtimes(filepath.Join(dir, "audit-20260801.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := exp.cleanupLocked(time.Now()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit-20260801.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("old file should be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit-20260802.jsonl")); err != nil {
		t.Fatalf("recent file should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Fatalf("state.json should remain: %v", err)
	}
}

func TestFileExporterCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "audit")
	store := newExporterStore(t)
	exp := NewFileExporter(store, FileExporterConfig{Dir: dir})
	if err := store.Record(&AuditEntry{Action: "x", TenantID: "ws", Success: true, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	n, err := exp.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce should create dir: %v", err)
	}
	if n != 1 {
		t.Fatalf("exported %d entries, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Fatalf("state.json missing: %v", err)
	}
}

func TestFileExporterResumesV2CursorState(t *testing.T) {
	dir := t.TempDir()
	store := NewAuditStore()
	base := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	first := &AuditEntry{ID: "aud-v2-first", Action: "test.a", TenantID: "ws-a", Timestamp: base}
	second := &AuditEntry{ID: "aud-v2-second", Action: "test.b", TenantID: "ws-a", Timestamp: base.Add(time.Second)}
	if err := store.Record(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(second); err != nil {
		t.Fatal(err)
	}
	state := exporterState{Cursor: "v2:" + strconv.FormatInt(first.Timestamp.UnixNano(), 10) + ":" + first.ID}
	rawState, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), rawState, 0o600); err != nil {
		t.Fatal(err)
	}

	exp := NewFileExporter(store, FileExporterConfig{Dir: dir})
	n, err := exp.ExportOnce(context.Background())
	if err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("exported %d entries, want 1", n)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit-20260815.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(raw)
	if len(lines) != 1 || !strings.Contains(lines[0], second.ID) {
		t.Fatalf("expected only second entry after v2 resume, got %q", string(raw))
	}
}

func splitLines(raw []byte) []string {
	var out []string
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, string(raw[start:i]))
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, string(raw[start:]))
	}
	return out
}

func countNonEmpty(lines []string) int {
	n := 0
	for _, l := range lines {
		if l != "" {
			n++
		}
	}
	return n
}
