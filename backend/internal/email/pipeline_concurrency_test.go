package email

// pipeline_concurrency_test.go — 第 1 步收信的并发行为（BUG-AU）。
//
// 背景（2026-10-01 真实邮箱实测，6 个账户）：
//
//	invoice-fixture@example.com  FAILED after 1ms（夹具 stub 未起）
//	audit-poc@pocket-audit.test FAILED after 1ms（同上）
//	huangxutao@kxpms.cn         new=0 in 1.258s
//	feikemanager@163.com        new=1 in 382ms
//	56551681@qq.com             ← TCP 已建立但服务端不回命令，连挂 4m11s+
//
// 串行实现下总耗时 = 各账户之和，于是 4 个健康账户全被一个坏账户拖死：
// 客户端 5 分钟 headers 超时先走，整轮 15 分钟 ctx 也切不断在途的 IMAP 读
// （go-imap 不响应 ctx 取消）。这里钉死「慢账户不拖住其它账户」。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// barrierSync 用「栅栏」而不是 sleep 来制造重叠。
//
// 为什么不用固定 sleep：先测过一版用 peak 统计重叠，10 轮里偶发 peak=1。
// 原因是两个 duration=0 的账户可能在主循环派第三个之前就跑完了——重叠与否
// 取决于调度运气，于是测试本身是 flaky 的（而且 flaky 的红灯比没有测试更糟）。
// 栅栏让重叠成为**构造性保证**：每个账户进来后必须等到「到齐 expect 个」才返回。
//   - 并发实现：三个都到齐，cur 触到 expect，栅栏打开，peak=3，秒过；
//   - 串行实现：第一个永远等不到另外两个，等 barrierTimeout 后超时返回，
//     peak 恒为 1，稳定红。
type barrierSync struct {
	expect         int
	barrierTimeout time.Duration

	mu      sync.Mutex
	cur     int
	peak    int
	started []string
}

func (b *barrierSync) hook() func(context.Context, string) (int, error) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	return func(_ context.Context, id string) (int, error) {
		b.mu.Lock()
		b.started = append(b.started, id)
		b.cur++
		if b.cur > b.peak {
			b.peak = b.cur
		}
		arrived := b.cur >= b.expect
		b.mu.Unlock()

		if arrived {
			releaseOnce.Do(func() { close(release) })
		} else {
			select {
			case <-release:
			case <-time.After(b.barrierTimeout):
				// 串行实现的必然结局：没人能来开栅栏。
			}
		}

		b.mu.Lock()
		b.cur--
		b.mu.Unlock()
		return 1, nil
	}
}

func (b *barrierSync) stats() (peak int, started []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak, append([]string(nil), b.started...)
}

// TestSyncAccounts_SlowAccountDoesNotBlockOthers 是 BUG-AU 的核心回归：
// 一个睡 900ms 的账户不应让整轮收信退化成「900ms + 其它账户的耗时之和」。
func TestSyncAccounts_SlowAccountDoesNotBlockOthers(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ids := []string{"acct-c1", "acct-c2", "acct-c3"}
	accs := make([]Account, 0, len(ids))
	for _, id := range ids {
		seedAccount(t, store, id, "user-1", "ws-1")
		accs = append(accs, Account{ID: id, EmailAddress: id + "@example.com"})
	}

	fs := &barrierSync{expect: len(ids), barrierTimeout: 800 * time.Millisecond}
	p := &Pipeline{Store: store, Fetcher: &Fetcher{syncHook: fs.hook()}}

	rep := &PipelineReport{}
	p.syncAccounts(context.Background(), accs, rep)

	// 结果不能丢：慢账户不能吞掉其它账户的统计。
	if rep.AccountsSynced != 3 {
		t.Fatalf("AccountsSynced = %d, want 3", rep.AccountsSynced)
	}
	if rep.NewEmails != 3 {
		t.Fatalf("NewEmails = %d, want 3（每个账户各 1 封）", rep.NewEmails)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}

	// 判别「修没修」的是 peak：栅栏保证并发实现下 peak==expect，
	// 串行实现下 peak 恒为 1（每个账户各自等满 barrierTimeout）。
	// 刻意不断言总耗时——它在本用例里区分不出修复，只会带来负载相关的抖动。
	peak, _ := fs.stats()
	if peak < 2 {
		t.Fatalf("peak concurrency = %d, want >= 2（收信仍是串行的话 peak 恒为 1）", peak)
	}
}

// TestSyncAccounts_HungAccountDoesNotBlockRound 覆盖 §7e：服务商接受连接后
// 不回命令时，单个账户不能把整轮拖成无上界。
//
// 真实证据：2026-10-01 03:42 起，exmail.qq.com / imap.qq.com 接受 TCP 后
// 7 分 43 秒仍未返回，客户端超时、服务端 15 分钟 ctx 也切不断（go-imap 不
// 响应 ctx 取消，imapclient.Options 没有 ReadTimeout）。
//
// 修法不是打断它（打断不了），而是**到期不再等它**：登记 TIMED OUT 后继续。
// 真正的 Sync 在后台跑完，落库幂等，不污染数据。
func TestSyncAccounts_HungAccountDoesNotBlockRound(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ids := []string{"acct-t1", "acct-t2", "acct-t3"}
	accs := make([]Account, 0, len(ids))
	for _, id := range ids {
		seedAccount(t, store, id, "user-1", "ws-1")
		accs = append(accs, Account{ID: id, EmailAddress: id + "@example.com"})
	}

	// t2 模拟「连上了但永不返回」：睡到测试结束之后。
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	fs := &fakeSyncDurations{
		durations: map[string]time.Duration{"acct-t2": 30 * time.Second},
		blocked:   map[string]chan struct{}{"acct-t2": block},
	}
	p := &Pipeline{
		Store:              store,
		Fetcher:            &Fetcher{syncHook: fs.hook()},
		AccountSyncTimeout: 200 * time.Millisecond,
	}

	rep := &PipelineReport{}
	start := time.Now()
	p.syncAccounts(context.Background(), accs, rep)
	elapsed := time.Since(start)

	// 判别点：整轮必须在超时上限附近返回，而不是等那个 30s 的账户。
	if elapsed > 5*time.Second {
		t.Fatalf("整轮耗时 %s，说明仍在等待挂死的账户（上限是 200ms）", elapsed)
	}
	// 超时账户不能被算成同步成功。
	if rep.AccountsSynced != 2 {
		t.Fatalf("AccountsSynced = %d, want 2（挂死的账户不应计入成功）", rep.AccountsSynced)
	}
	// 但必须**如实记一笔**，不能悄悄当作成功或忽略。
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "exceeded") {
		t.Fatalf("errors = %v, want 恰好一条含 exceeded 的超时记录", rep.Errors)
	}
	// 健康的账户结果不能受影响。
	if rep.NewEmails != 2 {
		t.Fatalf("NewEmails = %d, want 2（健康账户照常计入）", rep.NewEmails)
	}
}

// fakeSyncDurations 按账户 id 指定耗时；指定了 blocked 的账户会一直阻塞到
// 通道关闭，用来模拟「连接建好但服务端永不返回」。
type fakeSyncDurations struct {
	durations map[string]time.Duration
	blocked   map[string]chan struct{}
}

func (f *fakeSyncDurations) hook() func(context.Context, string) (int, error) {
	return func(_ context.Context, id string) (int, error) {
		if ch, ok := f.blocked[id]; ok {
			<-ch
			return 1, nil
		}
		if d := f.durations[id]; d > 0 {
			time.Sleep(d)
		}
		return 1, nil
	}
}

// TestSyncAccounts_CancelledCtxDispatchesNothing 验证取消后不再给排队账户派活。
// 少了这个，一次超时会连带把后面所有账户也去连一遍——本轮正是被限流的 QQ
// 把整轮拖死的，派发闸门能避免「已经超时就别再去打扰服务商」。
func TestSyncAccounts_CancelledCtxDispatchesNothing(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ids := []string{"acct-x1", "acct-x2", "acct-x3", "acct-x4", "acct-x5", "acct-x6"}
	accs := make([]Account, 0, len(ids))
	for _, id := range ids {
		seedAccount(t, store, id, "user-1", "ws-1")
		accs = append(accs, Account{ID: id, EmailAddress: id + "@example.com"})
	}

	// ctx 预先已取消，一个账户都不该派出去。用 expect=1 的栅栏：若实现
	// 错误地派了活，第一个进来的会立刻放行自己并被 stats() 记到。
	fs := &barrierSync{expect: 1, barrierTimeout: 100 * time.Millisecond}
	p := &Pipeline{Store: store, Fetcher: &Fetcher{syncHook: fs.hook()}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消

	rep := &PipelineReport{}
	p.syncAccounts(ctx, accs, rep)

	_, started := fs.stats()
	if len(started) != 0 {
		t.Fatalf("dispatched %v under an already-cancelled ctx, want none", started)
	}
	if rep.AccountsSynced != 0 {
		t.Fatalf("AccountsSynced = %d, want 0", rep.AccountsSynced)
	}
}

// TestSyncAccounts_FailedAccountDoesNotDropOthers 断言一个账户报错时，
// 其余账户的结果照常汇总（真实邮箱里夹具账户必然失败，不能连带影响真账户）。
func TestSyncAccounts_FailedAccountDoesNotDropOthers(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ids := []string{"acct-e1", "acct-e2", "acct-e3"}
	accs := make([]Account, 0, len(ids))
	for _, id := range ids {
		seedAccount(t, store, id, "user-1", "ws-1")
		accs = append(accs, Account{ID: id, EmailAddress: id + "@example.com"})
	}

	fs := &barrierSync{expect: len(ids) - 1, barrierTimeout: 200 * time.Millisecond}
	base := fs.hook() // 必须复用同一个 hook：每次调用会新建一条 release 通道
	failing := "acct-e2"
	p := &Pipeline{Store: store, Fetcher: &Fetcher{syncHook: func(ctx context.Context, id string) (int, error) {
		if id == failing {
			return 0, fmt.Errorf("no POP3 endpoint")
		}
		return base(ctx, id)
	}}}

	rep := &PipelineReport{}
	p.syncAccounts(context.Background(), accs, rep)

	if rep.AccountsSynced != 2 {
		t.Fatalf("AccountsSynced = %d, want 2（失败的账户不计入成功）", rep.AccountsSynced)
	}
	if len(rep.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1", rep.Errors)
	}
}
