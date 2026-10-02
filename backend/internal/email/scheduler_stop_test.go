package email

// scheduler_stop_test.go — Stop 的幂等性与「真的停得下来」。
//
// ## 为什么钉这个
//
// `Stop` 原本是裸 `close(s.stop)`。第二次调用会 panic（close of closed
// channel），而 Scheduler 是由 main 的 `defer emailScheduler.Stop()` 持有的
// 生命周期对象——今天生产路径上只有一处 Stop，所以不可达；但「不可达」不是
// 「安全」：任何将来多一次收尾的改动（测试 teardown 与用例体各调一次、
// 优雅退出分两处触发）都会把整个进程带走。
//
// 仓库内已有既定模式，email 这个是漏掉的：
//
//	internal/scheduledtask/scheduler.go:246
//	// Stop requests a graceful stop and waits for in-flight executions. It is
//	// idempotent and safe when Start was never called.
//	func (s *Scheduler) Stop() {
//		if s == nil { return }
//		s.stopOnce.Do(func() { close(s.stop) })
//	}
//
// 另有 redclaw/bridge_test.go 里有显式的「Second stop should not panic」用例。
//
// ## 三条断言分三个层次
//
// 只断言「不 panic」是不够的——那只能证明 close 没被调第二次，
// 证明不了 loop 真的停了。第三条用注入时钟把触发点拉到几百毫秒后、
// 在触发**之前** Stop，断言 runner 一次都没被调用：loop 必须已经退出，
// 否则它会在 200ms 后醒来把这一轮跑掉。

import (
	"context"
	"testing"
	"time"
)

// Stop 重复调用不得 panic。
func TestStop_IsIdempotent(t *testing.T) {
	s := NewScheduler(nil, nil, true)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("第二次 Stop panic 了（Stop 必须幂等）: %v", r)
		}
	}()

	s.Stop()
	s.Stop()
	s.Stop()
}

// Start 从未被调用时 Stop 也必须安全（nil 守卫 + Once 的组合语义）。
func TestStop_SafeWhenNeverStarted(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("未 Start 就 Stop 发生 panic: %v", r)
		}
	}()

	NewScheduler(nil, nil, true).Stop()

	// nil 接收者同样安全：main.go 里 emailScheduler 是条件构造的，
	// 与 scheduledtask.Stop 的 nil 守卫保持一致。
	var nilSched *Scheduler
	nilSched.Stop()
}

// frozenClock 恒定返回同一个时刻。
//
// 不能复用 scheduler_pipeline_test.go 里的 fakeClock：那个第二次起就返回
// 「已过触发点」的时间，是为了让 loop 第二轮把下次触发排到 24h 后。而
// pipelineLoop 一轮里要取两次 now（next := nextTimeAt(s.now(), …) 和
// delay := next.Sub(s.now())），用 fakeClock 会算出负的 delay 被 clamp 成 0，
// loop 在启动瞬间就触发——Stop 根本来不及生效，测的就不是「停不停得下来」。
type frozenClock struct{ at time.Time }

func (c frozenClock) now() time.Time { return c.at }

// Stop 之后 pipelineLoop 必须真的退出，而不只是「close 没炸」。
func TestStop_EndsPipelineLoopBeforeTrigger(t *testing.T) {
	s := NewScheduler(nil, nil, true)

	runner := &stubPipelineRunner{}
	triggerAt := time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local)
	// 恒停在触发点前 400ms：next = triggerAt，delay = 400ms，loop 会老实等。
	s.nowFn = frozenClock{at: triggerAt.Add(-400 * time.Millisecond)}.now
	s.SetPipelineRunner(runner, triggerAt.Hour())
	s.Start(context.Background())

	// loop 已经排期、正在等那 400ms。此时 Stop。
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	// 触发点已过：若 loop 还活着，这一轮会被跑掉。
	time.Sleep(700 * time.Millisecond)
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("Stop 之后 pipelineLoop 仍然触发了 %d 次 —— Stop 只关掉了 channel，"+
			"没有让 loop 退出", got)
	}
}
