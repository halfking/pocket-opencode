package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// BUG-V12: POST /api/emails/sync 把「已有一轮同步在跑、本轮被单飞锁正常跳过」
// 当成「同步失败」上报。
//
// 实测 2026-10-03（Linux 本机 rig，5 个真实账户）：响应
//   {"mode":"imap_fetch","synced":4,"new":50,"failed":["56551681@qq.com"]}
// 而库里 5 个账户的 last_synced_at 全部非零、QQ 的 uid 推进到 10410 ——
// 那个账户**同步成功了**，只是定时器与手工 POST 同时打到了它。
//
// 危害有两层，第二层更重：
//  1. 响应里给用户报红一个健康账户。
//  2. 旧代码在失败分支里无条件调 RecordSyncFailure，于是往库里写了一条
//     **假的失败记录**，会长期挂在这个账户上，把后续「是不是服务端拒登」
//     的排查引向错误方向（2026-10-02 的 credential health 诊断正是要把
//     (a) 凭据坏 / (b) 服务端拒 / (c) 客户端 三类分开，这条假记录污染 (b)）。
//
// 负控：把 syncSkippedNotFailed 改成恒 false，本文件第一个用例转红。

func TestSyncSkippedNotFailed_RecognisesInFlight(t *testing.T) {
	// fetcher.go:650 是 fmt.Errorf("%w: %s", ErrSyncInFlight, accountID) 包出来的，
	// 所以必须 errors.Is；裸 == 在生产路径上永远不成立。
	wrapped := fmt.Errorf("%w: %s", email.ErrSyncInFlight, "acct-123")
	if !syncSkippedNotFailed(wrapped) {
		t.Errorf("wrapped ErrSyncInFlight must be treated as skipped, not failed; got %v", wrapped)
	}
	if !syncSkippedNotFailed(email.ErrSyncInFlight) {
		t.Error("bare ErrSyncInFlight must be treated as skipped, not failed")
	}
}

func TestSyncSkippedNotFailed_RealFailuresStayFailures(t *testing.T) {
	// 这些必须继续算「真的失败」，否则真正的拒登/超时会被静默吞掉，
	// 那比误报更糟：用户再也看不到红色。
	for name, err := range map[string]error{
		"auth rejected":   errors.New("auth failed: invalid credentials"),
		"imap timeout":    context.DeadlineExceeded,
		"cancelled":       context.Canceled,
		"wrapped timeout": fmt.Errorf("sync account x: %w", context.DeadlineExceeded),
		"163 unsafe":      errors.New("NO SELECT Unsafe Login. Please contact kefu@188.com"),
	} {
		if syncSkippedNotFailed(err) {
			t.Errorf("%s: must stay a real failure, but was classified as skipped", name)
		}
	}
}

// 元护栏：证明上面的用例不是因为「恒 false 也算过」而通过的。
// 若 syncSkippedNotFailed 被改成恒 true，本用例必须转红。
func TestSyncSkippedNotFailed_IsNotConstantlyTrue(t *testing.T) {
	if syncSkippedNotFailed(errors.New("a totally unrelated failure")) {
		t.Error("an unrelated error was classified as skipped: the classifier is too broad")
	}
}
