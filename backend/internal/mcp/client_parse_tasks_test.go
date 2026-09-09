package mcp

import (
	"strings"
	"testing"
)

// 回归：2026-09-08~09 llm-gateway-pg 日志累计 508 条 duplicate key
// tasks_pkey。根因是 ParseToolTasks 用旧的按行文本解析器去拆 ACC 返回的
// JSON——在 JSON 字符串值内部的 ": "（note 正文 "heartbeat: GP2b 心跳"）
// 处切开，产出 id=JSON 碎片、title=整段 JSON 的垃圾任务；tasksync 把垃圾
// id 写库后每个同步周期重插同一 id。以下用例锁定 JSON 优先解析的行为。

func TestParseToolTasks_JSONArray(t *testing.T) {
	text := `[{"id":"6092ce38-020d-4a94-9c28-b5b80a6b531c","tenant_id":"default","kind":"feature","title":"local cutover delegate probe","phase":"queue","priority":5,"created_at":"2026-09-08T04:52:22.751919Z","updated_at":"2026-09-08T04:52:22.751919Z"},{"id":"a0bdf2bf-7dff-408b-bd24-2723ff550d5c","kind":"swarm_execute","title":"GP2b e2e 蜂群任务","phase":"review","agent_id":"v5-gp-e2e","notes":[{"body":"heartbeat: GP2b 心跳：执行中 50%"}]}]`

	tasks := ParseToolTasks(text)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %+v", len(tasks), tasks)
	}
	first, second := tasks[0], tasks[1]
	if first.ID != "6092ce38-020d-4a94-9c28-b5b80a6b531c" || first.Title != "local cutover delegate probe" {
		t.Errorf("first task mismatch: %+v", first)
	}
	if first.Status != "queue" {
		t.Errorf("status should fall back to phase, got %q", first.Status)
	}
	if second.ID != "a0bdf2bf-7dff-408b-bd24-2723ff550d5c" || second.Title != "GP2b e2e 蜂群任务" {
		t.Errorf("second task mismatch: %+v", second)
	}
	if second.Owner != "v5-gp-e2e" {
		t.Errorf("owner should fall back to agent_id, got %q", second.Owner)
	}
	// 关键回归点：id/title 不得再含 JSON 碎片
	for _, ts := range tasks {
		if strings.ContainsAny(ts.ID, "{},[]") {
			t.Errorf("task id %q must not contain raw JSON characters", ts.ID)
		}
		if strings.HasPrefix(ts.Title, "{\"id\"") {
			t.Errorf("task title %q must not be a raw JSON fragment", ts.Title)
		}
	}
}

func TestParseToolTasks_MultiLineJSONWithColonSpaceInString(t *testing.T) {
	// 事故现场的换行形态：数组元素独占一行，note 正文含 ": "。
	// 旧解析器会在 "heartbeat: " 处 SplitN 出垃圾 id。
	text := `[{"id":"a0bdf2bf-7dff-408b-bd24-2723ff550d5c","title":"GP2b e2e 蜂群任务","phase":"work"},
{"id":"5bcefe85-a3d4-4948-81cd-f9dbd2d6270a","title":"GP2b e2e 蜂群任务 1788727900","notes":[{"body":"heartbeat: GP2b 心跳：执行中 50%"}]}]`

	tasks := ParseToolTasks(text)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %+v", len(tasks), tasks)
	}
	for _, ts := range tasks {
		if strings.Contains(ts.ID, "heartbeat") || strings.ContainsAny(ts.ID, "{},[]\"") {
			t.Errorf("id %q is a JSON fragment, parser regressed", ts.ID)
		}
	}
	if tasks[1].Title != "GP2b e2e 蜂群任务 1788727900" {
		t.Errorf("title should come from JSON field, got %q", tasks[1].Title)
	}
}

func TestParseToolTasks_WrappedObject(t *testing.T) {
	text := `{"tasks":[{"id":"t-1","title":"wrapped","status":"open"}]}`
	tasks := ParseToolTasks(text)
	if len(tasks) != 1 || tasks[0].ID != "t-1" || tasks[0].Status != "open" {
		t.Fatalf("expected wrapped tasks parse, got %+v", tasks)
	}

	text2 := `{"data":[{"id":"t-2","title":"wrapped data"}]}`
	tasks2 := ParseToolTasks(text2)
	if len(tasks2) != 1 || tasks2[0].ID != "t-2" {
		t.Fatalf("expected wrapped data parse, got %+v", tasks2)
	}
}

func TestParseToolTasks_SingleObject(t *testing.T) {
	tasks := ParseToolTasks(`{"id":"solo","title":"one task"}`)
	if len(tasks) != 1 || tasks[0].ID != "solo" || tasks[0].Title != "one task" {
		t.Fatalf("expected single object parse, got %+v", tasks)
	}
}

func TestParseToolTasks_LegacyTextStillSupported(t *testing.T) {
	text := "[open] task-abc: fix the thing (owner: alice)\n[done] task-xyz: ship it (owner: bob)"
	tasks := ParseToolTasks(text)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 legacy tasks, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].ID != "task-abc" || tasks[0].Title != "fix the thing" || tasks[0].Status != "open" || tasks[0].Owner != "alice" {
		t.Errorf("first legacy task mismatch: %+v", tasks[0])
	}
	if tasks[1].ID != "task-xyz" || tasks[1].Owner != "bob" {
		t.Errorf("second legacy task mismatch: %+v", tasks[1])
	}
}

func TestParseToolTasks_EmptyAndNoTasks(t *testing.T) {
	if tasks := ParseToolTasks(""); len(tasks) != 0 {
		t.Errorf("empty text should yield no tasks, got %+v", tasks)
	}
	if tasks := ParseToolTasks("No tasks found."); len(tasks) != 0 {
		t.Errorf("'No tasks found.' should yield no tasks, got %+v", tasks)
	}
}

func TestParseToolTasks_JSONRowsWithoutIDAreSkipped(t *testing.T) {
	tasks := ParseToolTasks(`[{"title":"no id here"},{"id":"ok","title":"fine"}]`)
	if len(tasks) != 1 || tasks[0].ID != "ok" {
		t.Fatalf("expected rows without id to be skipped, got %+v", tasks)
	}
}
