package background

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncBuffer 是**带锁**的日志缓冲。
//
// ⚠ 为什么必须带锁：`go test -race`（CI 就是 `go test -race ./...`）会抓
//
//	「goroutine 往 buffer 写」与「测试线程读 buffer」之间的竞争
//	⇒ bytes.Buffer 不是并发安全的 ⇒ ⇒⇒ 裸用它会让**这道测试自己**变成红的。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureLog 把 log 的输出临时接到 buf 上，返回恢复函数。
//
// ⚠ log.SetOutput 是**全局**的 ⇒ 本包的测试不能并行跑（默认就不并行）。
func captureLog(t *testing.T) (*syncBuffer, func()) {
	t.Helper()
	buf := &syncBuffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	return buf, func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	}
}

// TestGoRecoversPanic 证明 **panic 不会打死进程**，而且**留了痕**。
//
// ⚠ 这一条是本包存在的**全部理由** ⇒ 必须同时验两件事：
// ① 函数确实跑完了（没有 panic 逃出去把测试进程带走）；
// ② 日志里**有**那条 [PANIC] 记录（否则 recover 就成了「静默吞掉 panic」）。
//
// ⚠ 竞态：goroutine 写日志、测试线程读日志 ⇒ 读之前必须**等到那条日志出现**
//
//	（用带锁的 buffer 轮询）⇒ 否则 `-race` 会报竞争，测试自己先红。
func TestGoRecoversPanic(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	var ran atomic.Bool
	Go("test/panic", func() {
		ran.Store(true)
		panic("boom")
	})

	// 等那条 [PANIC] 日志真正落进 buffer（不是只等 goroutine 结束 ——
	// recover 之后还要写日志 ⇒ 两者不是同一时刻）。
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "[PANIC]")
	}, "panic 日志没有出现")

	if !ran.Load() {
		t.Errorf("goroutine 里的函数没有跑到（ran=false）⇒ background.Go 根本没执行它")
	}
	out := buf.String()
	if !strings.Contains(out, "test/panic") {
		t.Errorf("panic 日志里没有任务名 %q（实际输出：%q）⇒ 事后定位不到是哪个任务炸的", "test/panic", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("panic 日志里没有 panic 值 %q（实际输出：%q）", "boom", out)
	}
	// ⚠ 栈必须一起打出来：没有栈的 panic 日志等于把崩溃换成了「一句没头没尾的话」。
	if !strings.Contains(out, "goroutine ") && !strings.Contains(out, "background.") {
		t.Errorf("panic 日志里没有栈信息（实际输出：%q）⇒ 事后无法定位", out)
	}
}

// TestGoRunsNormally 证明正常路径**照常执行**（recover 没有把正常逻辑吃掉）。
func TestGoRunsNormally(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	var wg sync.WaitGroup
	wg.Add(1)
	Go("test/ok", func() {
		defer wg.Done()
	})
	wg.Wait()

	if s := buf.String(); s != "" {
		t.Errorf("正常路径不该有日志（实际：%q）", s)
	}
}

// TestGo1AndGo2PassArgs 证明参数是**原样**传过去的（不是靠闭包捕获）。
func TestGo1AndGo2PassArgs(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	var wg sync.WaitGroup
	wg.Add(2)
	var got1, got2 string
	Go1("test/go1", func(s string) {
		defer wg.Done()
		got1 = s
	}, "one")
	Go2("test/go2", func(s string, n int) {
		defer wg.Done()
		got2 = s + string(rune('0'+n))
	}, "two", 2)
	wg.Wait()

	if got1 != "one" {
		t.Errorf("Go1 的参数没原样传过去（got %q，want %q）", got1, "one")
	}
	if got2 != "two2" {
		t.Errorf("Go2 的参数没原样传过去（got %q，want %q）", got2, "two2")
	}
	if s := buf.String(); s != "" {
		t.Errorf("参数传递路径不该有日志（实际：%q）", s)
	}
}

// waitFor 轮询到 cond 为真（最多 2 秒）。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待超时：%s", msg)
}
