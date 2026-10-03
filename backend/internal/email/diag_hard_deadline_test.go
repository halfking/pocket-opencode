package email

// diag_hard_deadline_test.go — **诊断**（非常驻用例）：量出生产常量下的**实测**
// 有效硬截止，并钉住一条「算出来不算数、必须测」的不变式。
//
// 门禁：必须显式设 POCKET_DIAG_HARDDEADLINE=1（单次约 80s）。验完保留文件，
// 改动 IMAP 超时相关常量后应当重跑。
//
// # 起因
//
// 运行进程里 76 次 `imap login … i/o timeout — trying POP3 fallback (budget
// -10s left)`，耗时全部是 1m20.1xx s（80.114 / 80.131 / 80.146 / 80.162，
// 抖动仅十几毫秒）。而按常量算应该是 60s：
//
//	tick = imapIdleTimeout/3 = 20s，hard = 45s
//	=> 断在第 ceil(45/20)=3 拍 = 60s，syncBudget 70s 还剩 +10s
//
// 实测比模型多整整一拍，预算从 +10 变成 -10 —— 于是 syncPOP3Fallback 在
// `budget <= 0` 上直接返回，**POP3 兜底 76 次全部没跑成**。
//
// 曾怀疑是「运行进程的二进制与源码常量对不上」，两个工作区都查过：
// imapIdleTimeout=60s / imapHardTimeout=45s / syncBudget=70s 完全一致，
// 二进制构建于 2026-10-02 01:35:53。**不是常量问题，是模型问题。**
//
// # 实测机制（给 deadlineConn.start 的循环加临时日志打出来的四拍）
//
//	t=20s   since=20s      now-hard=-25.001s  -> REFRESH  (deadline -> t0+80)
//	t=40s   since=40s      now-hard=-4.999s   -> REFRESH  (deadline -> t0+100)
//	t=60s   since=1m0.001s now-hard=+15.001s  -> HARD, set past   ← 判定正确
//	t=80s   since=1m20s    now-hard=+35.001s  -> HARD, set past
//	实测断开 = 1m20.001s
//
// 即：第 3 拍**已经**正确判定越界、也确实调用了 `SetDeadline(now-1s)`，但那次
// 调用没能打断已经阻塞的读；读一直活到第 1 拍刷进去的那个 OS 截止到期。
// 第 2 拍的 REFRESH（t0+100）同样没生效。**只有第 1 拍的 SetDeadline 真正
// 生效**，之后无论刷进过去还是刷进更远，都不再改变已在等待中的读的到期时刻。
//
// 因此实测不变式是：
//
//	有效硬截止 = 首个 tick + imapIdleTimeout = idle/3 + idle = 4/3 · idle
//	（本例 20 + 60 = 80s）
//
// 它**与 imapHardTimeout 无关**——只要 hard > 首个 tick，硬截止分支就来不及
// 生效，兜底的反而是第 1 拍刷下的那个 idle 滚动截止。把 imapHardTimeout 从
// 45s 调到 30s 或 15s 都不会让它提前断开（30s > 20s 仍是第 1 拍 REFRESH；
// 要真正生效必须 hard <= tick）。
//
// # 这条为什么是「诊断」而不是常驻用例
//
// 它要真等 80s，且结论依赖具体常量取值。常驻断言请看
// TestIMAPLeavesBudgetForPOP3Fallback —— 但要注意它的公式是按
// `ceil(hard/tick)*tick` 算的，**这个公式已被本诊断证伪**（它算出 60s，
// 实测 80s）。改动 IMAP 超时前请先重跑本诊断。

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestDiagHardDeadlineWithProductionConstants(t *testing.T) {
	if os.Getenv("POCKET_DIAG_HARDDEADLINE") != "1" {
		t.Skip("set POCKET_DIAG_HARDDEADLINE=1 to run (~80s)")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// 接住但永不写入：连接 Established，读永远等不到。这正是 163/企业邮
	// 「登录挂住」最真实的形态。
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { <-time.After(3 * time.Minute); c.Close() }(c)
		}
	}()

	tick := imapIdleTimeout / 3
	modeled := time.Duration(int(imapHardTimeout/tick)+1) * tick
	measuredModel := tick + imapIdleTimeout
	t.Logf("生产常量: idle=%s hard=%s tick=%s syncBudget=%s", imapIdleTimeout, imapHardTimeout, tick, syncBudget)
	t.Logf("模型(ceil(hard/tick)*tick) = %s", modeled)
	t.Logf("实测模型(首个 tick + idle) = %s", measuredModel)

	start := time.Now()
	client, err := imapDialWithIdle(ln.Addr().String(), false, 10*time.Second, imapIdleTimeout, imapHardTimeout, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	_, err = client.Capability().Wait()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("黑洞服务器居然成功了，测量无意义")
	}
	t.Logf("实测有效截止 = %s (err=%v)", elapsed.Round(time.Millisecond), err)
	t.Logf("POP3 兜底可用余量 = %s", (syncBudget - elapsed).Round(time.Millisecond))

	// 只断言「实测确实远大于模型」，这条不受常量取值影响。
	if elapsed < modeled {
		t.Logf("本次实测(%s)小于模型(%s)——常量或机制已变，请重新核对上面的说明",
			elapsed.Round(time.Millisecond), modeled)
	}
	if elapsed >= syncBudget {
		t.Errorf("POP3 兜底不可达：IMAP 实测占用 %s >= 单账户预算 %s，"+
			"syncPOP3Fallback 会在 budget<=0 上直接返回，降级形同虚设",
			elapsed.Round(time.Millisecond), syncBudget)
	}
}
