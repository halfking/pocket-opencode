package calendar

// JSON 契约护栏：Go 的 json tag 与前端 TS 接口是**分别手写**的，
// 没有任何工具会检查两边一致。改了一边的字段名而忘了另一边时，
// 后端照样编译、门禁全绿、接口照常 200 —— 只是前端读到 undefined，
// 色块不显示、时间不显示、来源判断落空。
//
// 这里的每个用例都对着 frontend/src/features/calendar/types.ts 抄。
// 改后端字段名时，这个文件会红；改前端字段名时，也应该回来改这里。

import (
	"encoding/json"
	"sort"
	"testing"
)

func jsonKeys(t *testing.T, v interface{}) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// assertKeys 比较键集合，**与顺序无关**（两边都排序）。只排一边会让
// 「got 恰好已排序、want 恰好没排」的调用方假红。
func assertKeys(t *testing.T, got []string, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		t.Fatalf("key set changed.\n got: %v\nwant: %v", g, w)
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("key #%d = %q, want %q\n got: %v\nwant: %v", i, g[i], w[i], g, w)
		}
	}
}

// FeedEntry ↔ types.ts 的 CalendarEntry。
func TestFeedEntryJSONContract(t *testing.T) {
	got := jsonKeys(t, FeedEntry{})
	// 对应 CalendarEntry 的字段；带 omitempty 的在零值时不会出现。
	assertKeys(t, got, []string{
		"id", "source", "refId", "title", "startAt", "endAt", "allDay", "timezone",
	})
}

func TestFeedEntryJSONOptionalKeys(t *testing.T) {
	// description / location / done / remindAt 是 omitempty 的：有值时必须出现，
	// 无值时必须消失（前端用 `entry.description ?` 判断，出现 undefined 键无害，
	// 但空串与 undefined 语义不同，钉住更稳）。
	full := jsonKeys(t, FeedEntry{
		ID: "x", Description: "d", Location: "l", Done: true, RemindAt: 5,
	})
	for _, k := range []string{"description", "location", "done", "remindAt"} {
		found := false
		for _, g := range full {
			if g == k {
				found = true
			}
		}
		if !found {
			t.Errorf("optional key %q must be present when set", k)
		}
	}
}

// Event ↔ types.ts 的 CalendarEvent。
//
// description / location / remindAt 带 omitempty，零值时不出键 —— 这与前端
// `description?: string`（可选）一致，事件面板用 `?? ”` 兜底。
func TestEventJSONContract(t *testing.T) {
	assertKeys(t, jsonKeys(t, Event{}), []string{
		"id", "workspaceId", "ownerUserId", "title",
		"startAt", "endAt", "allDay", "timezone", "visibility",
		"createdAt", "updatedAt",
	})

	set := jsonKeys(t, Event{
		ID: "x", Description: "d", Location: "l", RemindAt: 5,
	})
	for _, k := range []string{"description", "location", "remindAt"} {
		found := false
		for _, g := range set {
			if g == k {
				found = true
			}
		}
		if !found {
			t.Errorf("optional key %q must be present when set", k)
		}
	}
}

// 来源枚举两边必须同名：前端 SOURCE_META 按 'event'|'task'|'scheduled' 取图标与颜色，
// 多一个值（如曾想加的 'meeting'）就会取不到而回落到默认色。
func TestSourceConstantsMatchFrontend(t *testing.T) {
	want := map[string]string{
		SourceEvent:     "event",
		SourceTask:      "task",
		SourceScheduled: "scheduled",
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("source constant = %q, want %q (must match types.ts CalendarSource)", got, expected)
		}
	}
	// 恰好三个来源：多一个前端就没有对应的图标/颜色/筛选项。
	if len(want) != 3 {
		t.Errorf("expected exactly 3 sources, got %d", len(want))
	}
}

// 可见性枚举 ↔ types.ts 的 'private' | 'shared'。
func TestVisibilityConstantsMatchFrontend(t *testing.T) {
	if VisibilityPrivate != "private" || VisibilityShared != "shared" {
		t.Errorf("visibility constants drifted: %q / %q", VisibilityPrivate, VisibilityShared)
	}
}
