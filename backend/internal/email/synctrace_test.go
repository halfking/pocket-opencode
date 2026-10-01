package email

// synctrace_test.go — syncTrace 的分步打点。
//
// 这个打点存在的理由是：2026-10-01 排查「某个账户卡满 90s」时，Sync 里**一个
// 日志都没有**，只能看到 pipeline 外层的 `TIMED OUT after 1m30s`，完全不知道卡在
// dial、login、SELECT、FETCH 还是落库。
//
// 修了打点之后又发现它**仍然漏**：step() 只在「进入下一阶段」时结算上一阶段，
// 而失败路径是直接 return 的 —— 腾讯系账户 login 挂满 80s，日志里只有
// `imap login ... failed` 和 `sync trace total 1m20.143s`，`SLOW step`
// 计数为 0。**最该被记录的那 80s 恰恰是空的。**
//
// 所以下面两条断言盯的就是「最后一个阶段以失败收场」这条路径。

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// captureLogs 把 log 输出重定向到内存缓冲，返回恢复函数与缓冲指针。
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	return &buf, func() {
		log.SetOutput(orig)
		log.SetFlags(origFlags)
	}
}

func TestSyncTrace_ReportsSlowStepBetweenSteps(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	tr := newSyncTrace("x@example.com")
	// 真实调用序列：先进 dial，再进 login。进入 login 时才结算 dial 的耗时。
	tr.step("dial")
	// 模拟 dial 阶段很慢（生产里 dial 一般很快，这里直接改 last 来构造）。
	tr.last = tr.last.Add(-2 * syncStepWarn)
	tr.step("login")

	out := buf.String()
	if !strings.Contains(out, "SLOW step") {
		t.Fatalf("慢阶段没有被记录：%q", out)
	}
	if !strings.Contains(out, "dial") {
		t.Fatalf("应记录卡在 dial 上（进入 login 时结算上一阶段），实际：%q", out)
	}
}

// 第一次 step() 没有「上一阶段」可结算，不该打出 SLOW —— 否则每次同步的
// 第一行日志都是一条无意义的慢步骤告警。
func TestSyncTrace_FirstStepHasNothingToSettle(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	tr := newSyncTrace("x@example.com")
	tr.last = tr.last.Add(-2 * syncStepWarn) // 假装已经过去很久
	tr.step("dial")

	if out := buf.String(); strings.Contains(out, "SLOW step") {
		t.Fatalf("首次 step() 不该报慢阶段：%q", out)
	}
}

func TestSyncTrace_DoneReportsSlowFinalStep(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	tr := newSyncTrace("x@example.com")
	tr.step("login")
	// 关键：进入 login 之后**再也不 step**，直接以失败/提前返回收场。
	// 这正是腾讯系 IMAP 挂 80s 的形状。
	tr.last = tr.last.Add(-2 * syncStepWarn)

	tr.done()

	out := buf.String()
	if !strings.Contains(out, "SLOW step") {
		t.Fatalf("以失败收场的最后一个慢阶段没有被记录：%q", out)
	}
	if !strings.Contains(out, "login") {
		t.Fatalf("应记录卡在 login 上，实际：%q", out)
	}
	// 收场提示必须和 total 一起出现，否则看到 SLOW 不知道发生在什么时候。
	if !strings.Contains(out, "sync trace total") {
		t.Fatalf("total 耗时必须照常记录：%q", out)
	}
}

func TestSyncTrace_DoneStaysQuietWhenFast(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	tr := newSyncTrace("x@example.com")
	tr.step("dial")
	tr.step("login")
	tr.done()

	out := buf.String()
	if strings.Contains(out, "SLOW step") {
		t.Fatalf("正常同步不该打 SLOW step（逐条打会淹掉日志）：%q", out)
	}
	if !strings.Contains(out, "sync trace total") {
		t.Fatalf("total 耗时即使全程不慢也要记一笔：%q", out)
	}
}
