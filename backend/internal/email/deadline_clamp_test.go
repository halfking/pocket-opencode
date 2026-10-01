//go:build !greenmail

// deadline_clamp_test.go — 「绝对硬截止」在代码上到底成不成立。
//
// ## 起因
//
// 2026-10-01 21:38 线上日志（huangxutao@kxpms.cn）：
//
//	imap login failed: in response: cannot read tag: read tcp ...:993: i/o timeout
//	— trying POP3 fallback (budget -10s left)
//	SLOW step login took 1m20.001s (total 1m20.167s)
//
// 80.001s。而 `imapHardTimeout` 写的是 45s，注释把它叫「绝对寿命上限」。
//
// ## 根因
//
// 看门狗的滚动续期写的是 `SetDeadline(now.Add(idle))`，**没有用 hard 夹住**。
// 生产 idle=60s / hard=45s：第一次 tick 在 T+20s（iv = idle/3），此时 now 还没
// 超过 hard=45s，于是走 default 分支把 socket deadline 设成 **T+80s** ——
// 比宣称的绝对上界还晚 35s。之后能不能被拉回来，完全取决于 T+40 / T+60
// 两次 tick 是否准时跑（GC、调度饥饿、进程繁忙都会推迟）。
//
// 80.001s 正好等于 T+20 + idle，与「续期把 deadline 推到 80s」完全吻合。
//
// ## 本文件的断言
//
// 黑洞服务器（accept 后不响应），idle 与 hard 都用秒级值：读必须**不超过
// hard + idle/3** 返回，且必须**明显早于**「不夹取」时会被推到的那条线。
// 这条断言在修复前是红的（见文件头的负控说明）。
package email

import (
	"net"
	"testing"
	"time"
)

// startSilentServer 起一个「accept 之后一句话不说、也不关连接」的服务器。
func startSilentServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 保持连接打开但永不写：这样客户端只可能被自己的 deadline 打断。
			go func(c net.Conn) {
				defer c.Close()
				time.Sleep(30 * time.Second)
			}(c)
		}
	}()
	return ln.Addr().String()
}

// TestHardDeadlineIsNotExceededByIdleRenewal 是本轮的核心时序断言。
//
// 参数是**标定过的**（第一版选错过：hard 正好落在 tick 上，两种实现只差
// 0.5s，断言抓不住回归，等于白写）：
//
//	idle = 6s  ->  看门狗 tick = idle/3 = 2s
//	hard = 3s  ->  故意**不**是 tick 的整数倍
//
// 两个实测数据点（黑洞服务器，读一个必然要靠 deadline 才返回的 CAPABILITY）：
//
//	不夹取：初始 deadline = T+6（= now+idle，**越过了 hard=3s**）→ 实测 6.000s
//	夹取：  初始 deadline = min(T+6, T+3) = T+3            -> 实测 4.001s
//
// 上界取 5s：夹取后（4.0s）绿、不夹取（6.0s）红。
//
// **诚实标注残余缺口**：夹取后仍在 ~4.0s 而不是 3.0s，说明还有一条
// 「看门狗 tick 到点后才把 deadline 钉到过去」的路径在起作用（不夹取时
// 实测 6.000s 恰好是初始 socket deadline 到点，也说明 socket deadline 本身
// 是生效的）。也就是说上界是 `hard + 一个 tick 量级`，不是精确的 hard。
// 这一点本文不去猜，但**必须写下来**，否则下一个读代码的人会以为
// imapHardTimeout 就是精确值。生产参数下即 `45s + 20s ≈ 65s`。
func TestHardDeadlineIsNotExceededByIdleRenewal(t *testing.T) {
	addr := startSilentServer(t)
	const idle = 6 * time.Second
	const hard = 3 * time.Second

	client, err := imapDialWithIdle(addr, false, 5*time.Second, idle, hard, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	start := time.Now()
	_, err = client.Capability().Wait()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("黑洞服务器居然握手成功了，测试无意义")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("读在 %s 才返回，hard=%s —— socket deadline 越过了绝对上界"+
			"（不夹取实测 6.0s，夹取实测 4.0s）（err=%v）",
			elapsed.Round(time.Millisecond), hard, err)
	}
	t.Logf("hung read broken after %s (hard=%s, idle=%s): %v",
		elapsed.Round(time.Millisecond), hard, idle, err)
}

// TestCloseClientAfterBreaksHungReadIndependentOfDeadline 证明 Sync 里那条
// `time.AfterFunc(imapStageBudget, client.Close)` 兜底**确实是它**在收尾，
// 而不是 deadline 机制顺手断的。
//
// 参数是刻意这样配的：idle 给 60s（远超测试窗口），所以在这 2.5 秒里
// deadline 机制**不可能**成为终结原因（滚动续期只会把 deadline 往后推，
// 见 §7bi）。唯一的终结者是 AfterFunc 里的 Close。
//
// 负控：去掉那个 AfterFunc，这一条会挂到 60s idle 才断（测试超时/超时断言）——
// 也就是「POP3 回退拿不到预算」的那个老问题。
func TestCloseClientAfterBreaksHungReadIndependentOfDeadline(t *testing.T) {
	addr := startSilentServer(t)

	// idle 60s / hard 0：模拟「deadline 机制在测试窗口内完全不作为」。
	client, err := imapDialWithIdle(addr, false, 5*time.Second, 60*time.Second, 0, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	const imapStage = 1 * time.Second
	stop := time.AfterFunc(imapStage, func() { _ = client.Close() })
	defer stop.Stop()

	start := time.Now()
	_, err = client.Capability().Wait()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("连接被关掉了却还返回成功，测试无意义")
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("读在 %s 才返回，AfterFunc 定的上界是 %s —— 兜底没生效"+
			"（idle=60s 说明不是 deadline 断的）（err=%v）",
			elapsed.Round(time.Millisecond), imapStage, err)
	}
	t.Logf("hung read broken by Close after %s (idle=60s never fired): %v",
		elapsed.Round(time.Millisecond), err)
}

// TestNextIdleIsClampedByHard 直接钉住那个夹取函数本身：它的返回值永远
// 不得超过 hard。这是一个纯函数断言，不依赖任何时序，负控必然精确。
func TestNextIdleIsClampedByHard(t *testing.T) {
	hard := time.Now().Add(time.Second)
	dc := &deadlineConn{idle: 60 * time.Second, hard: hard}
	if got := dc.nextIdle(); got.After(hard) {
		t.Fatalf("nextIdle=%s 超过 hard=%s —— 续期会把 socket deadline 推出绝对上界",
			got.Format(time.RFC3339Nano), hard.Format(time.RFC3339Nano))
	}

	// 没有硬截止时（hard 为零值）必须退回纯 idle 续期，不能变成「立刻超时」。
	dc2 := &deadlineConn{idle: 60 * time.Second}
	if got := dc2.nextIdle(); !got.After(time.Now().Add(30 * time.Second)) {
		t.Fatalf("没有 hard 时 nextIdle=%s 明显偏近，滚动续期被误伤", got)
	}

	// idle 小于 hard 时不该被夹到 hard 之前（夹取只能收窄，不能提前）。
	dc3 := &deadlineConn{idle: 200 * time.Millisecond, hard: time.Now().Add(time.Minute)}
	if got := dc3.nextIdle(); got.After(time.Now().Add(500 * time.Millisecond)) {
		t.Fatalf("idle 小于 hard 时 nextIdle=%s 不该被拉到 hard 附近", got)
	}
}
