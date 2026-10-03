//go:build !greenmail

// sync_budget_test.go — 钉住 Sync 的**真实接线**：IMAP 阶段到点会关连接，
// 于是 POP3 降级结构上必然拿得到正预算。
//
// ## 为什么要有这条
//
// 第一版我写的是「直接测 time.AfterFunc + client.Close 能断开挂住的读」——
// 那条**测的是测试自己**：把 Sync 里那行 AfterFunc 删掉，它照样绿。而真正要防
// 的回归恰恰是「有人把 Sync 里那行删了」。
package email
// 所以这里走完整 Sync：真 store（隔离 schema）+ 真 account + dialTLS 指向
// 一个黑洞 IMAP 服务器。IMAP 登录会挂住，于是只有 Sync 自己装的那个
// AfterFunc 能把它断掉；断掉之后走 POP3 分支，而 POP3 端点解析不出来，所以
// 返回的错误**必然**是 `no POP3 endpoint` —— 而不是
// `imap failed and no time left for POP3 fallback`。
//
// 负控：把 Sync 里的 AfterFunc 删掉 -> login 会一直挂到 deadline 兜底
// （本用例把 idle/hard 也调大，避免它顺手兜住）-> 用例超时或超上界，红。

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// blackholeIMAP 是一个「accept 之后一句话不说、也不关连接」的 IMAP 假服务器。
func blackholeIMAP(t *testing.T) string {
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
			go func(c net.Conn) {
				defer c.Close()
				time.Sleep(60 * time.Second)
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestSyncLeavesBudgetForPOP3Fallback(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	crypto, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	enc, err := crypto.EncryptString("pw")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	addr := blackholeIMAP(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	_ = portStr
	const acctID = "acct-sync-budget"
	acc := &Account{
		ID: acctID, UserID: "u1", WorkspaceID: "ws1",
		DisplayName: "budget", EmailAddress: "budget@example.com",
		IMAPHost: host, IMAPPort: mustAtoi(t, portStr), AuthType: "password",
		Enabled: true, CreatedAt: time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, enc); err != nil {
		t.Fatalf("insert: %v", err)
	}

	f := NewFetcher(store, crypto)
	// dialTLS 是唯一能把 Sync 指向本地黑洞服务器的接缝（生产为 nil）。
	f.dialTLS = func(a string, _ *imapclient.Options) (*imapclient.Client, error) {
		// 60s idle / 无硬截止：保证在这 1.5s 的测试窗口里，**只有** Sync 自己
		// 装的 AfterFunc 能终结这次挂住的登录。IMAPS 也不要（用明文口）。
		return imapDialWithIdle(a, false, 5*time.Second, 60*time.Second, 0, nil)
	}
	// 预算缩到秒级，生产值 70s/20s 测试等不了（与 imapDialWithIdle 同一理由）。
	f.syncBudgetOverride = 4 * time.Second
	f.pop3ReserveOverride = 2 * time.Second // IMAP 阶段 = 2s，POP3 留 2s

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err = f.Sync(ctx, acctID)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("黑洞 IMAP 上 Sync 居然成功了，测试无意义")
	}
	// 核心断言：走到了 POP3 端点解析，而不是「没有预算」。
	if strings.Contains(err.Error(), "no time left for POP3 fallback") {
		t.Fatalf("POP3 回退仍然没拿到预算（elapsed=%s）：%v", elapsed.Round(time.Millisecond), err)
	}
	if !strings.Contains(err.Error(), "no POP3 endpoint") {
		t.Fatalf("期望走到 POP3 端点解析（证明拿到了预算），实际：%v", err)
	}
	// IMAP 阶段应在 2s 左右被关掉，而不是拖到 4s 预算耗尽。
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("Sync 用了 %s 才返回，IMAP 阶段上界是 2s —— AfterFunc 没在 Sync 里生效",
			elapsed.Round(time.Millisecond))
	}
	t.Logf("IMAP stage closed at %s, POP3 fallback entered with a positive budget: %v",
		elapsed.Round(time.Millisecond), err)
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("atoi %q: %v", s, err)
	}
	return n
}
