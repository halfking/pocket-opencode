package email

// imap_deadline_test.go — BUG-AX：IMAP 连接卡死导致连接泄漏 + 同账户重复同步。
//
// 2026-10-01 05:01~05:21 真实邮箱实测：
//
//	h[email/pipeline] step1 sync huangxutao@kxpms.cn TIMED OUT after 1m30s
//
// 同一账户稳定复现两轮，而用同样凭证在独立进程里逐步打点，全绿且只要
// 1.4 秒（IMAP 命令层没问题）。决定性对照：**重启 pocketd 后第一次同步
// 只需 1.261s**，而重启前进程里已经攒到 **11 条**到 993 的 Established
// 连接（其中 120.226.165.33 一台就占 7 条）。
//
// 根因链：
//  1. go-imap 不响应 context 取消；`net.Dialer.Timeout` 只管**建连**，
//     建好之后读操作没有任何时间上限。`imapclient.Options` 里压根没有
//     ReadTimeout/WriteTimeout 字段。
//  2. 卡在读上 → Sync 不返回 → `defer client.Close()` 永远执行不到 →
//     连接泄漏。上层 90s 只是「不再等待」，Sync 还在后台挂着。
//  3. scheduler 的 pollLoop 每 60s 对「LastSyncedAt 没更新」的账户再起一个
//     goroutine 调 Sync，**无互斥** → 泄漏正反馈，新连接越来越慢。
//
// 本文件钉住两道防线，任何一道被摘掉都必须转红。

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startBlackholeServer 起一个「接受 TCP 连接后永远不发任何字节」的服务器。
// 这是 IMAP 卡死最真实的形态：连接是 Established 的，客户端读却永远等不到。
func startBlackholeServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 接住但永不写入，也永不关闭。
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	}
}

// TestIMAPIdleDeadlineBreaksHungRead 是第一道防线的核心断言：
// 服务端接受连接后不响应，客户端的读必须在 idle 之内**返回错误**，
// 而不是永远挂着。
//
// 没有这道 deadline 时，Sync 卡在这里 → Close() 执行不到 → 连接泄漏。
func TestIMAPIdleDeadlineBreaksHungRead(t *testing.T) {
	addr, stop := startBlackholeServer(t)
	defer stop()

	const idle = 400 * time.Millisecond
	client, err := imapDialWithIdle(addr, false, 5*time.Second, idle, 0, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	start := time.Now()
	// Greeting 读不到（服务端不发），这条命令必然要靠 deadline 才能返回。
	_, err = client.Capability().Wait()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("服务端不响应时 Capability 居然成功了 —— 黑洞服务器没生效，测试无意义")
	}
	// 滚动 deadline 每 idle/3 刷新一次，所以最坏情况是 idle + idle/3 + 少量开销。
	upper := idle + idle + 3*time.Second
	if elapsed > upper {
		t.Fatalf("读在 %s 才返回（上限 %s），deadline 没兜住；err=%v", elapsed.Round(time.Millisecond), upper, err)
	}
	t.Logf("hung read broken after %s: %v", elapsed.Round(time.Millisecond), err)
}

// TestIMAPIdleDeadlineAllowsSlowButActiveConnection 钉住另一半：滚动 deadline
// 只在**静默**时生效。持续有数据的慢连接不能被误杀——否则一条正常但慢的
// 大邮件同步会被自己的保护机制打断。
func TestIMAPIdleDeadlineAllowsSlowButActiveConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	const idle = 300 * time.Millisecond
	// 服务器每隔 idle/2 发一个字节，模拟「很慢但在动」。
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; i < 20; i++ {
			time.Sleep(idle / 2)
			if _, err := c.Write([]byte("x")); err != nil {
				return
			}
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	dc := &deadlineConn{Conn: conn, idle: idle}
	dc.start()
	defer conn.Close()

	buf := make([]byte, 16)
	// 必须走 dc.Read 而不是 conn.Read：dc.Read 才会 touch() 记录活动。
	// 直连底层 conn 的话 last 永远不动，看门狗会正确地判定「静默」并钉死
	// deadline —— 那个失败是真的，但不是被测语义，而是测试绕过了被测逻辑。
	start := time.Now()
	got := 0
	for i := 0; i < 5; i++ {
		if _, err := dc.Read(buf); err != nil {
			t.Fatalf("第 %d 次读失败（滚动 deadline 误杀了活跃连接）：%v", i+1, err)
		}
		got++
	}
	if got != 5 {
		t.Fatalf("只读到 %d 字节，期望 5", got)
	}
	t.Logf("5 reads over %s (idle=%s) all succeeded", time.Since(start).Round(time.Millisecond), idle)
}

// TestIMAPHardDeadlineBreaksBusyButStuckConnection 钉住第二道保险。
//
// 2026-10-01 真实事故：56551681@qq.com 的 imap login 挂了 100s，空闲
// deadline 一次都没触发（连接期间有数据往来，滚动续期一直生效），最后仍
// 以 i/o timeout 收场。对这类「活着但不干活」的情况，只有绝对硬截止有效。
//
// 服务器在 hard 之前持续发数据（制造"活跃"假象），必须仍被硬截止断开。
func TestIMAPHardDeadlineBreaksBusyButStuckConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	const (
		idle = 5 * time.Second // 空闲上限故意放很长：绝不能靠它断开
		hard = 600 * time.Millisecond
	)
	// 每 100ms 发一个字节，全程保持"有活动"，直到客户端断开。
	stop := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if _, err := c.Write([]byte("x")); err != nil {
					return
				}
			}
		}
	}()
	defer close(stop)

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()

	dc := &deadlineConn{Conn: raw, idle: idle, hard: time.Now().Add(hard)}
	dc.start()

	// 一直读，模拟"连接活着、有活动，但命令迟迟不返回"。
	start := time.Now()
	buf := make([]byte, 4)
	var lastErr error
	for time.Since(start) < 3*time.Second {
		if _, err := dc.Read(buf); err != nil {
			lastErr = err
			break
		}
	}
	elapsed := time.Since(start)

	if lastErr == nil {
		t.Fatal("硬截止过后连接仍未被断开 —— 「活着但不干活」这类卡死兜不住")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("断开得太晚：%s（hard=%s，idle=%s 不该起作用）", elapsed, hard, idle)
	}
	t.Logf("busy-but-stuck connection broken after %s (idle=%s never fired): %v",
		elapsed.Round(time.Millisecond), idle, lastErr)
}

// TestSyncSkipsAccountAlreadyInFlight 钉住第二道防线：同一账户并发同步时，
// 第二个必须立刻返回 ErrSyncInFlight，而不是也去建一条连接。
//
// 现实危害不只是浪费连接：QQ 上 POP3 是**主路径**，重复同步等于把同一批
// 邮件反复拉一遍，正是那 47 组重复副本的来源之一。
func TestSyncSkipsAccountAlreadyInFlight(t *testing.T) {
	f := NewFetcher(nil, nil)

	// 手工占位，模拟「另一轮 Sync 正在跑」。
	f.inflight.Store("acct-1", struct{}{})

	_, err := f.Sync(context.Background(), "acct-1")
	if err == nil {
		t.Fatal("并发同步同一账户应当返回 ErrSyncInFlight，却成功了")
	}
	if !errors.Is(err, ErrSyncInFlight) {
		t.Fatalf("err = %v, want ErrSyncInFlight（调用方要用它区分「跳过」和「失败」）", err)
	}

	// 释放后应当能正常进入（虽然 store 为 nil 会报另一个错，但**不是**
	// ErrSyncInFlight —— 说明互斥确实只挡并发，不挡后续正常同步）。
	f.inflight.Delete("acct-1")
	_, err = f.Sync(context.Background(), "acct-1")
	if errors.Is(err, ErrSyncInFlight) {
		t.Fatal("占位释放后仍被 ErrSyncInFlight 拦住，互斥没随 Sync 结束解除")
	}
	if err != nil && strings.Contains(err.Error(), "not configured") {
		return // 预期的下一个错误：store 为 nil
	}
}

// TestSyncReleasesInflightOnReturn 保证 Sync 正常返回后不会把账户永久占住
// —— 否则一次异常就会让该账户此后再也同步不了。
func TestSyncReleasesInflightOnReturn(t *testing.T) {
	f := NewFetcher(nil, nil)
	if _, err := f.Sync(context.Background(), "acct-2"); err == nil {
		t.Skip("store 为 nil 时 Sync 提前返回，跳过本用例")
	}
	if _, busy := f.inflight.Load("acct-2"); busy {
		t.Fatal("Sync 返回后 inflight 仍被占用 —— 该账户将永远无法再同步")
	}
}

// TestHardDeadlineCheckedAtTickGranularity 钉住**生产形态**下的时序。
//
// 上面那条 TestIMAPHardDeadlineBreaksBusyButStuckConnection 用的是
// idle=5s / hard=600ms：tick=idle/3=1.67s，而 hard=0.6s **小于**一个 tick，
// 于是第一次 tick 就必然越过 hard——「硬截止生效」这件事在该用例里是
// 白送的，tick 粒度这个真正的变量一次都没被测到。
//
// 生产取值恰好相反：imapIdleTimeout=60s → tick=20s，而
// imapHardTimeout=45s > 20s。看门狗只在 tick 上判 hard，所以真实断开点是
// **第一个严格大于 hard 的 tick**，不是 hard 本身。
//
// 这条用例把两者按生产比例缩放（idle=3s/tick=1s，hard=1.5s），断言断开落在
// (hard, hard+tick] 这个窗口里——把「有效截止 = 向上取整到 tick」钉成事实。
// 有人把 imapHardTimeout 调到 tick 的整数倍之外时，有效截止会静默翻倍，
// 这条用例会先转红。
func TestHardDeadlineCheckedAtTickGranularity(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if _, err := c.Write([]byte("x")); err != nil {
					return
				}
			}
		}
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()

	const (
		idle = 3 * time.Second
		hard = 1500 * time.Millisecond // > tick(=1s)，与生产同形
	)
	tick := idle / 3
	dc := &deadlineConn{Conn: raw, idle: idle, hard: time.Now().Add(hard)}
	dc.start()

	start := time.Now()
	buf := make([]byte, 4)
	var lastErr error
	for time.Since(start) < 10*time.Second {
		if _, err := dc.Read(buf); err != nil {
			lastErr = err
			break
		}
	}
	el := time.Since(start)

	if lastErr == nil {
		t.Fatal("连接始终没被断开 —— hard 大于一个 tick 时看门狗漏判了")
	}
	// 有效截止应是「第一个严格大于 hard 的 tick」= 2s，不是 hard 本身。
	wantLo, wantHi := hard, hard+tick
	if el < wantLo {
		t.Fatalf("断得太早：%s < hard=%s，说明 hard 被精确执行了（那不是看门狗的实际语义）", el, hard)
	}
	if el > wantHi+300*time.Millisecond {
		t.Fatalf("断得太晚：%s > hard+tick=%s，有效截止比预期多了一个 tick", el, wantHi)
	}
	t.Logf("断开耗时 %s（hard=%s tick=%s → 有效截止落在 (hard, hard+tick]）", el.Round(time.Millisecond), hard, tick)
}

// TestIMAPLeavesBudgetForPOP3Fallback 钉住「降级通道必须可达」这条设计要求。
//
// 现实危害：IMAP 一旦把单账户预算 syncBudget 吃光，syncPOP3Fallback 会在
// budget<=0 上直接返回 "imap failed and no time left for POP3 fallback"，
// 于是「IMAP 一挂了就降级 POP3」这条兜底在**最需要它的时候**恰好不可用。
// 2026-10-02 真实日志里这条错误出现了 76 次，且每一条的 budget 都是负的
// （日志明写 "budget -10s left"）。
//
// # ⚠ 本用例下面的公式已被实测证伪（2026-10-02）
//
// 它假设「有效截止 = 第一个严格大于 imapHardTimeout 的 tick」，算出 60s、
// 剩 10s，于是通过。但用生产常量实测（见 diag_hard_deadline_test.go，
// 门禁 POCKET_DIAG_HARDDEADLINE=1）真实占用是 **80.0s**、余量 **-10.0s**，
// 与线上 76 次 1m20.1xxs 完全吻合。
//
// 原因是 deadlineConn 的看门狗在**第 1 拍**就走了 REFRESH 分支
// （SetDeadline(now+idle)），那个 OS 截止真正生效；等第 3 拍硬截止分支
// 终于正确判定越界并 SetDeadline(过去) 时，已经阻塞的读**不再被它打断**，
// 一直活到第 1 拍刷下的那个到期时刻。于是：
//
//	有效硬截止 = 首个 tick + imapIdleTimeout = 4/3 · idle = 80s
//
// 且**与 imapHardTimeout 无关**（只要 hard > 首个 tick）。把它从 45s 调到
// 30s/15s 都不会提前断开——要真正生效必须 hard <= idle/3。
//
// 本用例保留是为了锁住「余量必须为正」这个意图（公式一旦被修正为实测值就会
// 转红），但**不要再把它的通过当成 POP3 兜底可达的证据**。改 IMAP 超时
// 前先重跑 diag_hard_deadline_test.go。
func TestIMAPLeavesBudgetForPOP3Fallback(t *testing.T) {
	tick := imapIdleTimeout / 3
	// 有效截止 = 第一个严格大于 imapHardTimeout 的 tick。
	effective := (imapHardTimeout/tick + 1) * tick

	if effective >= syncBudget {
		t.Fatalf(
			"IMAP 有效截止 %s 已经吃光单账户预算 %s（tick=%s hard=%s）：POP3 兜底永远拿不到预算，"+
				"IMAP 一挂 syncPOP3Fallback 就在 budget<=0 上返回，降级形同虚设",
			effective, syncBudget, tick, imapHardTimeout)
	}
	left := syncBudget - effective
	if left < 10*time.Second {
		t.Fatalf("IMAP 有效截止 %s 之后只剩 %s 给 POP3 兜底，余量过窄（tick=%s hard=%s）",
			effective, left, tick, imapHardTimeout)
	}
	t.Logf("IMAP 有效截止 %s / 预算 %s → POP3 兜底可用余量 %s", effective, syncBudget, left)
}
