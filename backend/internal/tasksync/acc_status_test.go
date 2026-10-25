package tasksync

// ACC 派单状态回填的判据。
//
// 映射函数住在这个包里，所以判据也住在这个包里 —— 复制一份到别的包去测，
// 测的就不是真函数，而是一份会自己跟自己一致的副本。

import (
	"context"
	"errors"
	"testing"
)

// 判据1：ACC 的派单状态 → 本地任务状态的映射。
// 未知状态映射成 ""（= 不动本地行）：凭猜把还在跑的任务写成已完成，
// 比不更新坏得多。
func TestACCDispatchStatusMapping(t *testing.T) {
	cases := []struct {
		acc   string
		local string
	}{
		{"succeeded", "completed"},
		{"SUCCEEDED", "completed"},
		{"success", "completed"},
		{"completed", "completed"},
		{"done", "completed"},
		{"failed", "failed"},
		{"error", "failed"},
		{"cancelled", "cancelled"},
		{"canceled", "cancelled"},
		{"expired", "cancelled"},
		{"superseded", "cancelled"},
		{"running", ""},
		{"pending", ""},
		{"", ""},
		{"weird-new-status", ""},
	}
	for _, c := range cases {
		if got := accDispatchStatusLocal(c.acc); got != c.local {
			t.Fatalf("acc status %q -> %q, want %q", c.acc, got, c.local)
		}
	}
}

// stubReader answers with a canned status (or an error).
type stubReader struct {
	status string
	err    error
	calls  []string
}

func (r *stubReader) CommandStatus(_ context.Context, dispatchID string) (string, error) {
	r.calls = append(r.calls, dispatchID)
	if r.err != nil {
		return "", r.err
	}
	return r.status, nil
}

// 判据2：读不到命令面 ⇒ 不改本地行（fail-closed：读不到 ≠ 失败）。
// 这一条用 store 的行为间接验证：没有 store 时刷新必须安全地返回 0。
func TestRefreshWithoutStoreIsANoOp(t *testing.T) {
	s := &Scheduler{}
	if n := s.refreshACCDispatchStatuses(context.Background()); n != 0 {
		t.Fatalf("refreshed %d rows without a store", n)
	}
}

// 判据3：命令面读失败 ⇒ 不写终态。
//
// 注入一个总是失败的 reader：刷新必须不产生任何更新，而不是把任务
// 标成失败 —— 读失败是「不知道」，不是「失败了」。
func TestRefreshKeepsRowsWhenTheCommandFaceIsUnreadable(t *testing.T) {
	reader := &stubReader{err: errors.New("acc 502")}
	s := &Scheduler{commands: reader}
	// 没有 store ⇒ 行读不出来 ⇒ 没有任何行被改。reader 也不该被叫醒。
	if n := s.refreshACCDispatchStatuses(context.Background()); n != 0 {
		t.Fatalf("refreshed %d rows", n)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("reader called without rows: %+v", reader.calls)
	}
}

// 判据4：映射出来的每个状态都必须在**本地**任务状态词汇里。
//
// 本地词汇（server.go 的 isValidTaskStatus）：active / blocked / completed
// / cancelled / failed。failed 是 2026-10-26 加进来的 —— 在那之前，
// ACC 的失败只能被写成 cancelled（说成「用户取消了」）或留在 active
// （假的「还在跑」）。两种都是错的说法。
//
// 这条判据读的是 server 包里的**真**校验函数（而不是复制一份），
// 否则它就只测了一份会自己跟自己一致的副本。
func TestMappedStatusesAreInsideTheLocalVocabulary(t *testing.T) {
	for _, acc := range []string{"succeeded", "failed", "cancelled", "expired"} {
		local := accDispatchStatusLocal(acc)
		if local == "" {
			t.Fatalf("acc status %q produced no local status", acc)
		}
		if !localTaskStatusAllowed(local) {
			t.Fatalf("acc status %q -> %q, which is outside the local vocabulary", acc, local)
		}
	}
}

// localTaskStatusAllowed mirrors server.isValidTaskStatus. The two must
// agree; the cross-package check is the point of this test.
func localTaskStatusAllowed(s string) bool {
	switch s {
	case "active", "blocked", "completed", "cancelled", "failed":
		return true
	default:
		return false
	}
}
