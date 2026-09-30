package email

// scheduler_pipeline_test.go — 每日定时流水线的触发语义。
//
// 这里钉的是 BUG-AM：SetPipelineRunner 在全仓没有任何调用点，
// POCKET_EMAIL_PIPELINE_HOUR 读进配置后无人消费，pipelineLoop 永不启动，
// 于是「每天定时处理邮件」实际不存在——清垃圾 / 重要提醒 / 发票采集 /
// 飞书推送全都只能靠手动 POST /api/email/pipeline/run。
//
// 修复后 cmd/pocketd 在 Start() 之后才注入 runner（server 实例晚于 scheduler
// 构造），所以要同时钉住两条：
//  1. Start() 之前注入 → loop 起，且到点真的跑；
//  2. Start() 之后注入（cmd/pocketd 的真实顺序）→ 补起 loop，同样到点就跑。

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// stubPipelineRunner 记录被调用的次数。
type stubPipelineRunner struct {
	calls atomic.Int64
}

func (s *stubPipelineRunner) RunEmailPipeline(ctx context.Context) *PipelineReport {
	s.calls.Add(1)
	return &PipelineReport{}
}

// fakeClock：第一次返回 fakeNow（距触发点 200ms），之后返回已过触发点的时间，
// 使 loop 第二次迭代把下一次触发排到 24 小时后，测试不会陷入高速空转。
type fakeClock struct {
	n        atomic.Int64
	fakeNow  time.Time
	afterNow time.Time
}

func (c *fakeClock) now() time.Time {
	if c.n.Add(1) == 1 {
		return c.fakeNow
	}
	return c.afterNow
}

func waitForCalls(t *testing.T, r *stubPipelineRunner, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if r.calls.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pipeline runner was not invoked within %s", within)
}

// Start() 之前注入 runner：到点必须真的跑一轮。
func TestPipelineLoop_FiresWhenRunnerInjectedBeforeStart(t *testing.T) {
	s := NewScheduler(nil, nil, true) // 每日流水线只依赖注入的 runner，不需要 store/fetcher

	runner := &stubPipelineRunner{}
	triggerAt := time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local)
	clock := &fakeClock{
		fakeNow:  triggerAt.Add(-200 * time.Millisecond),
		afterNow: triggerAt.Add(200 * time.Millisecond),
	}
	s.nowFn = clock.now
	s.SetPipelineRunner(runner, triggerAt.Hour())

	s.Start(context.Background())
	defer s.Stop()

	waitForCalls(t, runner, 3*time.Second)
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 pipeline run, got %d", got)
	}
}

// cmd/pocketd 的真实注入顺序：Start() 先跑，SetPipelineRunner 后到。
// 这是「定时流水线根本没跑」的那个洞，必须由这个用例守住。
func TestPipelineLoop_FiresWhenRunnerInjectedAfterStart(t *testing.T) {
	s := NewScheduler(nil, nil, true) // 每日流水线只依赖注入的 runner，不需要 store/fetcher

	triggerAt := time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local)
	clock := &fakeClock{
		fakeNow:  triggerAt.Add(-200 * time.Millisecond),
		afterNow: triggerAt.Add(200 * time.Millisecond),
	}
	s.nowFn = clock.now

	s.Start(context.Background())
	defer s.Stop()

	// Start 时没有 runner，loop 不应存在。
	if got := s.NextPipelineUnix(); got != 0 {
		t.Fatalf("pipeline scheduled before runner injected: %d", got)
	}

	runner := &stubPipelineRunner{}
	s.SetPipelineRunner(runner, triggerAt.Hour())

	// 契约就是「到点真的会跑」，不是「某个瞬时刻能被观测到排期」
	// （loop 是异步 goroutine，触发后 nextPipeline 会被清 0）。
	waitForCalls(t, runner, 3*time.Second)
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 pipeline run, got %d", got)
	}
}

// hour<0 = 关闭定时：注入后不得排期、不得触发（手动 API 不受影响）。
func TestPipelineLoop_NegativeHourDisablesSchedule(t *testing.T) {
	s := NewScheduler(nil, nil, true) // 每日流水线只依赖注入的 runner，不需要 store/fetcher

	triggerAt := time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local)
	clock := &fakeClock{fakeNow: triggerAt.Add(-200 * time.Millisecond), afterNow: triggerAt.Add(200 * time.Millisecond)}
	s.nowFn = clock.now

	s.Start(context.Background())
	defer s.Stop()

	runner := &stubPipelineRunner{}
	s.SetPipelineRunner(runner, -1)

	if got := s.NextPipelineUnix(); got != 0 {
		t.Fatalf("hour<0 must disable scheduling, got next=%d", got)
	}
	time.Sleep(400 * time.Millisecond)
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("hour<0 must not trigger pipeline, got %d calls", got)
	}
}

// 重复注入同一个 scheduler 不得起两个 loop（否则一天会跑两轮、飞书重复推送）。
func TestPipelineLoop_RepeatedInjectionStartsSingleLoop(t *testing.T) {
	s := NewScheduler(nil, nil, true) // 每日流水线只依赖注入的 runner，不需要 store/fetcher

	triggerAt := time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local)
	clock := &fakeClock{
		fakeNow:  triggerAt.Add(-200 * time.Millisecond),
		afterNow: triggerAt.Add(200 * time.Millisecond),
	}
	s.nowFn = clock.now

	s.Start(context.Background())
	defer s.Stop()

	runner := &stubPipelineRunner{}
	s.SetPipelineRunner(runner, triggerAt.Hour())
	s.SetPipelineRunner(runner, triggerAt.Hour())
	s.SetPipelineRunner(runner, triggerAt.Hour())

	waitForCalls(t, runner, 3*time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("expected 1 pipeline run after 3 injections, got %d", got)
	}
}
