package email

// sync_state_advance_test.go — 没有新邮件的同步也必须推进 last_synced_at。
//
// 2026-10-02 从运行 7h41m 的真实日志里量出来的（[email/fetcher] … sync trace
// 逐行统计，1430 条）：
//
//	feikemanager@163.com   40 次  →  ~12.3 分钟   ✅ 符合 syncIntervalMin=15
//	kimmy.huang@163.com    39 次  →  ~12.4 分钟   ✅
//	56551681@qq.com       435 次  →  ~64 秒      ❌
//	huangxutao@kxpms.cn   427 次  →  ~65 秒      ❌
//	feikemanager1@163.com 489 次  →  ~57 秒      ❌
//
// 对着三个真实信箱持续高频轮询。成因两条，同一个病根：
//
//  1. IMAP：UID SEARCH 为空时 `return 0, nil`，跳过了末尾的 UpdateSyncState。
//     last_synced_at 于是永远停在「最后一次收到邮件」的时刻。
//  2. POP3：整条兜底路径**从不**调用 UpdateSyncState（只有一个调用点，且在
//     IMAP 分支里），走兜底的账户 last_synced_at 恒为 0 或旧值。
//
// 两者都让 scheduler.go 的到期判据 `now - a.LastSyncedAt < intervalSec` 恒为
// false，于是每 60s 的 pollLoop 每一轮都判它到期。修法是「UID 原样回填、
// 只刷新时间戳」——last_synced_uid 的语义（已拉到的最大 UID）绝不能动。

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// readSyncState 读回账户当前的同步进度。用 ListAccounts 而不是直接查 SQL，
// 是为了让断言走生产同款的 NULL 处理路径。
func readSyncState(t *testing.T, store *Store, userID, wantEmail string) (lastUID, lastAt int64) {
	t.Helper()
	accts, err := store.ListAccounts(context.Background(), userID)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	for _, a := range accts {
		if a.EmailAddress == wantEmail {
			return a.LastSyncedUID, a.LastSyncedAt
		}
	}
	t.Fatalf("account %s not found", wantEmail)
	return 0, 0
}

// TestSyncWithNoNewMailAdvancesLastSyncedAt 钉住 IMAP 空结果路径。
//
// 判据不是「有没有调用 UpdateSyncState」，而是**后果**：同步完之后，这个账户
// 在 15 分钟内不该再被判为到期。写成后果，pollLoop 的判据被改坏时也会红。
func TestSyncWithNoNewMailAdvancesLastSyncedAt(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	// 服务器上一个消息都没有 —— 生产里绝大多数轮次都是这个形态。
	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")

	// 先把 last_synced_at 伪造成「7 小时前收到过邮件」，复刻线上那三个账户的状态。
	const sevenHoursAgo = 7 * 3600
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET last_synced_at = $2 WHERE id = $1`,
		acctID, time.Now().Unix()-sevenHoursAgo); err != nil {
		t.Fatalf("seed stale last_synced_at: %v", err)
	}
	beforeUID, beforeAt := readSyncState(t, store, "user-1", "recipient@example.com")
	if !accountDueForSync(time.Now().Unix(), beforeAt, 15) {
		t.Fatal("前置条件不成立：7 小时前的 last_synced_at 本应判为到期")
	}

	saved, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if saved != 0 {
		t.Fatalf("空邮箱应报 0 封新邮件，got %d", saved)
	}

	afterUID, afterAt := readSyncState(t, store, "user-1", "recipient@example.com")
	if afterAt <= beforeAt {
		t.Fatalf("last_synced_at 没有推进：%d → %d —— 这个账户此后每一轮 pollLoop 都会判它到期，"+
			"于是对着真实信箱按 60 秒而不是 15 分钟轮询", beforeAt, afterAt)
	}
	// UID 语义不许动：空结果时既不能回退，也不能写成 uidNext。
	if afterUID != beforeUID {
		t.Fatalf("last_synced_uid 被改动：%d → %d。空结果时必须原样回填，"+
			"写成 uidNext 会让下一轮从 uidNext+1 起搜，永久跳过恰好分到该 UID 的新邮件",
			beforeUID, afterUID)
	}
	if accountDueForSync(time.Now().Unix(), afterAt, 15) {
		t.Fatalf("空同步后仍被判为到期（last_synced_at=%d）—— 15 分钟内的轮询没有被挡住", afterAt)
	}
	t.Logf("last_synced_at %d → %d（推进 %ds），uid 保持 %d，15 分钟内不再到期",
		beforeAt, afterAt, afterAt-beforeAt, afterUID)
}

// TestEmptySyncKeepsUIDForNextIncomingMail 钉住「推进时间戳」与「推进 UID」
// 这两件事必须分开。上面的用例从 0 起步看不出差别：0 既是「没同步过」也是
// 「刚好拉到 0」。这里先真拉一封，再让它空一次，UID 必须停在真值上。
func TestEmptySyncKeepsUIDForNextIncomingMail(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	now := time.Now().UTC().Truncate(time.Second)
	ti.appendMessage(t, "boss@corp.example", "Invoice 2026-09", "See attached.", now.Add(-time.Hour))

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")

	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	uidAfterFirst, atAfterFirst := readSyncState(t, store, "user-1", "recipient@example.com")
	if uidAfterFirst == 0 {
		t.Fatal("首轮同步没有记录 last_synced_uid —— 测试没走到被测路径")
	}

	// 第二轮：服务器没有新邮件。
	//
	// 必须等过秒边界：last_synced_at 是 Unix **秒**，而两次 Sync 在本用例里
	// 相隔不到 0.4s，同一秒内写入的时间戳相同，断言会假红。生产里两次同步
	// 隔 60s，不存在这个问题。
	waitForNextSecond()
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	uidAfterEmpty, atAfterEmpty := readSyncState(t, store, "user-1", "recipient@example.com")

	if uidAfterEmpty != uidAfterFirst {
		t.Fatalf("空同步把 last_synced_uid 从 %d 改成了 %d —— 下一轮会从 %d 起搜，"+
			"恰好分到该 UID 的新邮件会被永久跳过", uidAfterFirst, uidAfterEmpty, uidAfterEmpty+1)
	}
	if atAfterEmpty <= atAfterFirst {
		t.Fatalf("空同步没有推进 last_synced_at：%d → %d", atAfterFirst, atAfterEmpty)
	}
	t.Logf("uid 稳定在 %d，last_synced_at %d → %d", uidAfterFirst, atAfterFirst, atAfterEmpty)
}

// TestPOP3FallbackWithEmptyMailboxAdvancesLastSyncedAt 覆盖第二条路径。
//
// POP3 兜底此前从不写同步进度，所以走兜底的账户（163 的
// `NO SELECT Unsafe Login`、企业邮 OAuth 不可用）永远被判为到期。仓库里
// 原本没有任何 POP3 进程内测试服务器，这条用例顺带把它补上：只实现
// greeting / USER / PASS / STAT / UIDL / QUIT，且**邮箱为空**——生产常态。
func TestPOP3FallbackWithEmptyMailboxAdvancesLastSyncedAt(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	port, stop := startEmptyPOP3Server(t)
	defer stop()

	// pop3EndpointFor 按域名匹配包级 providers，测试期临时插一个指向本地
	// mock 的条目（明文、无 TLS）。用 defer 还原，不影响其它用例。
	const mockProviderID = "pop3mock"
	providers = append(providers, Provider{
		ID:       mockProviderID,
		POP3Host: "127.0.0.1",
		POP3Port: port,
		POP3TLS:  false,
	})
	defer func() {
		for i, p := range providers {
			if p.ID == mockProviderID {
				providers = append(providers[:i], providers[i+1:]...)
				return
			}
		}
	}()

	const password = "app-specific-pw"
	crypto, err := NewCrypto([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	enc, err := crypto.EncryptString(password)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	const addr = "someone@" + mockProviderID + ".test"
	acc := &Account{
		ID: "acct-pop3-ws-a", UserID: "user-1", WorkspaceID: "ws-a",
		DisplayName: "pop3", EmailAddress: addr,
		IMAPHost: "127.0.0.1", IMAPPort: 1, // 故意指向死端口：本用例只调 POP3
		AuthType: "password", SyncIntervalMin: 15, Enabled: true,
		CreatedAt: time.Now().Unix(),
	}
	if err := store.InsertAccount(ctx, acc, enc); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET last_synced_at = $2 WHERE id = $1`,
		acc.ID, time.Now().Unix()-7*3600); err != nil {
		t.Fatalf("seed stale last_synced_at: %v", err)
	}
	beforeUID, beforeAt := readSyncState(t, store, "user-1", addr)

	f := &Fetcher{store: store, crypto: crypto}
	saved, err := f.syncPOP3Fallback(ctx, acc, password, 30*time.Second)
	if err != nil {
		t.Fatalf("pop3 fallback: %v", err)
	}
	if saved != 0 {
		t.Fatalf("空邮箱应报 0 封新邮件，got %d", saved)
	}

	afterUID, afterAt := readSyncState(t, store, "user-1", addr)
	if afterAt <= beforeAt {
		t.Fatalf("POP3 兜底空邮箱没有推进 last_synced_at：%d → %d —— 走兜底的账户会一直被 60s 轮询", beforeAt, afterAt)
	}
	if afterUID != beforeUID {
		t.Fatalf("POP3 路径不得改动 last_synced_uid：%d → %d（POP3 用位置序号，没有 IMAP UID 语义）", beforeUID, afterUID)
	}
	if accountDueForSync(time.Now().Unix(), afterAt, 15) {
		t.Fatalf("POP3 空同步后仍被判为到期（last_synced_at=%d）", afterAt)
	}
	t.Logf("POP3 兜底：last_synced_at %d → %d，uid 保持 %d", beforeAt, afterAt, afterUID)
}

// TestAccountDueForSync 钉住被抽出来的到期判据本身。
//
// 阈值型判据的边界值必须单独覆盖：从「多条」这种最丰满的取值起步会漏掉
// 恰好卡在阈值上的那些真实取值。
func TestAccountDueForSync(t *testing.T) {
	const now = 1_700_000_000
	cases := []struct {
		name       string
		lastSynced int64
		interval   int64
		want       bool
	}{
		// 从没同步过（0）：永远到期。
		{"never synced", 0, 15, true},
		// 15 分钟区间：刚同步过 / 恰好差 1 秒 / 恰好等于 15 分钟。
		{"just synced", now, 15, false},
		{"one second short", now - 15*60 + 1, 15, false},
		{"exactly at the interval", now - 15*60, 15, true},
		// interval<=0 兜底成 15 分钟，不能变成「永远到期」。
		{"zero interval falls back to 15m", now, 0, false},
		{"negative interval falls back to 15m", now, -5, false},
		// 不同区间。
		{"5m interval not yet due", now - 4*60, 5, false},
		{"5m interval due", now - 5*60, 5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := accountDueForSync(now, c.lastSynced, c.interval); got != c.want {
				t.Fatalf("accountDueForSync(now, %d, %d) = %v, want %v", c.lastSynced, c.interval, got, c.want)
			}
		})
	}
}

// waitForNextSecond 等到下一个 Unix 秒边界。
//
// last_synced_at / last_synced_uid 的时间分量是 Unix 秒。断言「时间戳被推进
// 了」时，两次写入必须落在不同的秒里，否则同秒写入会被误判成没推进。
func waitForNextSecond() {
	now := time.Now()
	time.Sleep(now.Truncate(time.Second).Add(time.Second).Sub(now) + 20*time.Millisecond)
}

// startEmptyPOP3Server 起一个「邮箱为空」的最小 POP3 服务器（明文）。
//
// 只实现 pop3_fetcher.go 实际发出的那条命令序列，不多不少：greeting →
// USER → PASS → STAT → UIDL（空列表）→ QUIT。UIDL 返回空正是被测分支
// （len(uidls)==0 那次早退），所以这里绝不能塞消息进去。
func startEmptyPOP3Server(t *testing.T) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveEmptyPOP3Conn(c)
		}
	}()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		t.Fatal("listener is not TCP")
	}
	return tcpAddr.Port, func() { _ = ln.Close() }
}

func serveEmptyPOP3Conn(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	send := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format+"\r\n", args...)
		_ = w.Flush()
	}
	send("+OK pop3 mock ready")

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		verb := cmd
		if i := strings.IndexByte(cmd, ' '); i >= 0 {
			verb = cmd[:i]
		}
		switch strings.ToUpper(verb) {
		case "USER", "PASS":
			send("+OK")
		case "STAT":
			// 空邮箱：0 封 0 字节。
			send("+OK 0 0")
		case "UIDL":
			// RFC 1939 §4.1.5：无参数时列出全量，多行以单独一行 "." 结束。
			send("+OK")
			send(".")
		case "RETR":
			// 不该被调到（UIDL 为空就没有可 RETR 的），明说而不是回静默。
			send("-ERR no messages")
		case "QUIT":
			send("+OK bye")
			return
		default:
			send("-ERR unsupported: %s", verb)
		}
	}
}
