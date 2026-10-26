package mcp

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

// 这组判据守的是「写请求失败后，认领『服务器其实建出来了』的那条任务」。
//
// ★ 缺陷原型（2026-10-27 01:32 真机夹具实测）：
//   委托返回 502，但 ACC 侧**其实已经建出任务**了
//   （ACC 的 MCP 面会回 EOF/超时/400 —— 实测四种都出现过）。
//   ⇒ acc_create_task 没有幂等键 ⇒ 盲重试会建出重复任务；
//     直接报 502 也不对 —— 任务确实建出来了，只是响应没回来。
//
// ★ 认领判据必须是**两个条件同时成立**：
//   ① 标题完全相同；
//   ② 创建时间不早于本次请求发出的时刻。
//   只按标题找 ⇒ 会认领**上一轮的同名残留**（ACC 里同名能堆 27 条）
//   ⇒ 前提是假的，流会拿着一个不存在的任务去跑。
//
// 判据清单：
//  1. 标题不同 ⇒ 不认领；
//  2. 标题相同但创建时间更早 ⇒ 不认领（那是上一轮的）；
//  3. 没有 created_at ⇒ 不认领（没有时间戳就不敢认领，保守方向）；
//  4. 多个候选 ⇒ 取**最新**的那条（本次请求是最后发出的）；
//  5. 时钟偏差容忍：创建时间比请求时刻早几秒 ⇒ 仍认领；
//  6. 空标题 ⇒ 不认领；
//  7. acc_get_tasks 的返回要真的带上 created_at（解析判据）。

func mkTask(id, title string, created time.Time) ParsedTask {
	return ParsedTask{ID: id, Title: title, Status: "queue", CreatedAt: created}
}

func TestMatchTaskCreatedSince(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 32, 0, 0, time.UTC)
	older := now.Add(-10 * time.Minute)
	newer := now.Add(-2 * time.Second)

	t.Run("标题不同不认领", func(t *testing.T) {
		tasks := []ParsedTask{mkTask("a", "别人的任务", newer)}
		if _, ok := MatchTaskCreatedSince(tasks, "maestro-acc-approve", now); ok {
			t.Fatalf("标题不同却认领了")
		}
	})

	t.Run("标题相同但更早 ⇒ 不认领", func(t *testing.T) {
		tasks := []ParsedTask{mkTask("old", "maestro-acc-approve", older)}
		if _, ok := MatchTaskCreatedSince(tasks, "maestro-acc-approve", now); ok {
			t.Fatalf("把上一轮的旧任务认领了（同名残留能堆几十条）")
		}
	})

	t.Run("没有 created_at ⇒ 不认领", func(t *testing.T) {
		tasks := []ParsedTask{{ID: "x", Title: "maestro-acc-approve", Status: "queue"}}
		if _, ok := MatchTaskCreatedSince(tasks, "maestro-acc-approve", now); ok {
			t.Fatalf("没有时间戳却认领了")
		}
	})

	t.Run("多个候选取最新", func(t *testing.T) {
		tasks := []ParsedTask{
			mkTask("older", "maestro-acc-approve", now.Add(-5*time.Second)),
			mkTask("newest", "maestro-acc-approve", newer),
			mkTask("mid", "maestro-acc-approve", now.Add(-30*time.Second)),
		}
		got, ok := MatchTaskCreatedSince(tasks, "maestro-acc-approve", now)
		if !ok {
			t.Fatalf("应当认领到一条")
		}
		if got.ID != "newest" {
			t.Fatalf("认领了 %s，期望 newest", got.ID)
		}
	})

	t.Run("时钟偏差容忍", func(t *testing.T) {
		// ACC 与 pocketd 的时钟可能差几秒 ⇒ 创建时间比请求时刻早 3 秒仍应认领。
		tasks := []ParsedTask{mkTask("skew", "maestro-acc-approve", now.Add(-3*time.Second))}
		if _, ok := MatchTaskCreatedSince(tasks, "maestro-acc-approve", now); !ok {
			t.Fatalf("3 秒的时钟偏差不该导致不认领")
		}
	})

	t.Run("空标题不认领", func(t *testing.T) {
		tasks := []ParsedTask{mkTask("a", "", newer)}
		if _, ok := MatchTaskCreatedSince(tasks, "", now); ok {
			t.Fatalf("空标题却认领了")
		}
	})
}

// acc_get_tasks 的返回**必须**真的带 created_at ——
// 不带的话「按时间认领」永远认领不到（判据自身要被测）。
func TestParseToolTasksKeepsCreatedAt(t *testing.T) {
	raw := `[{"id":"t-1","title":"跑一件很慢的事","phase":"queue","created_at":"2026-10-27T01:12:02.450198Z"}]`
	tasks := ParseToolTasks(raw)
	if len(tasks) != 1 {
		t.Fatalf("解析出 %d 条，期望 1 条", len(tasks))
	}
	if tasks[0].CreatedAt.IsZero() {
		t.Fatalf("created_at 没被解析出来：%+v", tasks[0])
	}
	want := time.Date(2026, 10, 27, 1, 12, 2, 450198000, time.UTC)
	if !tasks[0].CreatedAt.Equal(want) {
		t.Fatalf("created_at 解析成 %s，期望 %s", tasks[0].CreatedAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// created_at 缺失/畸形 ⇒ 任务仍在列表里（不丢），但没有时间戳。
func TestParseToolTasksToleratesMissingCreatedAt(t *testing.T) {
	raw := `[{"id":"t-1","title":"跑一件很慢的事","phase":"queue"}]`
	tasks := ParseToolTasks(raw)
	if len(tasks) != 1 {
		t.Fatalf("解析出 %d 条，期望 1 条（created_at 缺失不该让任务消失）", len(tasks))
	}
	if !tasks[0].CreatedAt.IsZero() {
		t.Fatalf("created_at 应当是零值，实际 %v", tasks[0].CreatedAt)
	}
}

// IsAmbiguousWriteFailure：EOF / 连接重置 / 超时 ⇒ 结果未知；
// 明确的业务错误（403 之类）⇒ 不是「结果未知」，不该触发「问服务器」。
func TestIsAmbiguousWriteFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"EOF", io.EOF, true},
		{"连接被重置", fmt.Errorf("x: %w", syscall.ECONNRESET), true},
		{"超时", os.ErrDeadlineExceeded, true},
		{"业务错误(403)", &httpStatusError{Status: http.StatusForbidden}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAmbiguousWriteFailure(tc.err); got != tc.want {
				t.Fatalf("IsAmbiguousWriteFailure(%v) = %v，期望 %v", tc.err, got, tc.want)
			}
		})
	}
}
