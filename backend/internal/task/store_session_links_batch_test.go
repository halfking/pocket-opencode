package task

import (
	"context"
	"testing"
)

// TestListAllSessionLinksMatchesPerTaskSemantics 钉住「批量版」与「逐任务版」
// 的语义**逐条等价**（2026-10-28 §976）。
//
// ⚠ 为什么必须钉等价而不是只钉「更快」：
//
//	批量版换掉了「ListTasks + 每个任务查一次」的 N+1 ⇒ 一条 LEFT JOIN。
//	若两条路径的**输出集合**不同（比如没有链接的任务被漏掉、或多了一行），
//	那广播器就会漏发 idle 或多发 ⇒ 这是**行为回归**，比慢严重得多。
//	⇒ 所以判据是：**同一批数据上，两条路径产出的行集合完全相同**。
//
// ⚠ 负控：把批量版退化成「只返回有链接的行」⇒ 必然红（没有链接的任务会消失）。
func TestListAllSessionLinksMatchesPerTaskSemantics(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 三个任务：t1 有两条链接、t2 有一条、t3 **没有**链接。
	mustCreate := func(id, ws string) {
		t.Helper()
		tk := &Task{ID: id, Title: id, WorkspaceID: ws, Status: "open", Priority: "normal"}
		if err := store.CreateTask(context.Background(), tk); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mustCreate("t-one", "ws-a")
	mustCreate("t-two", "ws-b")
	mustCreate("t-three", "ws-c")

	attach := func(taskID, ws, instance, session, role string) {
		t.Helper()
		link := SessionLink{TaskID: taskID, InstanceID: instance, SessionID: session, Role: role}
		if err := store.AttachSessionScoped(ctx, link, ws); err != nil {
			t.Fatalf("attach %s: %v", taskID, err)
		}
	}
	attach("t-one", "ws-a", "inst-1", "sess-1", "primary")
	attach("t-one", "ws-a", "inst-2", "sess-2", "supporting")
	attach("t-two", "ws-b", "inst-3", "sess-3", "primary")

	// 参照实现：旧的 N+1 路径。
	type ref struct {
		taskID, ws, instance, session string
	}
	var want []ref
	tasks, err := store.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, tk := range tasks {
		links, err := store.ListSessionsForTask(ctx, tk.ID)
		if err != nil {
			t.Fatalf("ListSessionsForTask(%s): %v", tk.ID, err)
		}
		if len(links) == 0 {
			want = append(want, ref{tk.ID, tk.WorkspaceID, "", ""})
			continue
		}
		for _, l := range links {
			want = append(want, ref{tk.ID, tk.WorkspaceID, l.InstanceID, l.SessionID})
		}
	}

	// 被测：批量路径。
	got, err := store.ListAllSessionLinks(ctx)
	if err != nil {
		t.Fatalf("ListAllSessionLinks: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("批量版行数 = %d，逐任务版行数 = %d ⇒ 语义不等价\ngot  = %+v\nwant = %+v",
			len(got), len(want), got, want)
	}
	// 逐条比对（顺序不敏感：两条路径的行序都没有契约）。
	key := func(taskID, ws, inst, sess string) string {
		return taskID + "|" + ws + "|" + inst + "|" + sess
	}
	gotKeys := map[string]bool{}
	for _, r := range got {
		gotKeys[key(r.TaskID, r.WorkspaceID, r.InstanceID, r.SessionID)] = true
	}
	for _, w := range want {
		if !gotKeys[key(w.taskID, w.ws, w.instance, w.session)] {
			t.Fatalf("批量版缺这一行：task=%s ws=%s inst=%s sess=%s\ngot = %+v",
				w.taskID, w.ws, w.instance, w.session, got)
		}
	}
	// 没有链接的任务必须在场（带空 instance/session）⇒ 负控的着力点。
	sawEmpty := false
	for _, r := range got {
		if r.TaskID == "t-three" && r.InstanceID == "" && r.SessionID == "" {
			sawEmpty = true
		}
	}
	if !sawEmpty {
		t.Fatalf("没有会话链接的任务（t-three）不在批量结果里 ⇒ 广播器会对它漏发 idle：%+v", got)
	}
}
