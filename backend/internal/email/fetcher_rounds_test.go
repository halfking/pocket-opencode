package email

// fetcher_rounds_test.go —— 数一数「同步 N 封邮件」在 IMAP 线上打了几次 FETCH。
//
// ## 为什么数这个
//
// fetcher.go 里紧挨着 `fetchSnippetOnConnected` 的注释自己写着：
//
// > 这里是 Sync 里最可疑的一段：同一连接上**逐封串行**发部分取回，没有并发也没有
// 单独预算。企业微信（imap.exmail.qq.com）实测在这一步会挂到分钟级，而外层只能
// 看到 90s 上界。
//
// 而 §7dr 把「每轮处理 50 封」变成了常态（积压按最老 50 封排空）。所以
// **「50 封 = 多少个串行往返」是一个必须量的数字**，不是直觉。
//
// ## 怎么数
//
// `imapserver.Options.DebugWriter` 会把 server 收发的**明文** IMAP 流写出来
// （本文件只统计命令出现次数，绝不打印内容——明文里含登录凭据）。
// 在本进程里抓流，不需要 MITM、不需要 Docker、不需要真实 server。
//
// ## 一个踩过的坑（第一版数出 0）
//
// 最初在每个 `Write` 回调里切行统计，得到 FETCH=0。两个原因：
//   1. IMAP 命令行带 tag 前缀（`a001 UID FETCH …`），不以 `UID FETCH` 开头；
//   2. **DebugWriter 的 Write 边界与 IMAP 的行边界无关**，一条命令完全可能被
//      切成两次 Write，按 Write 切行必然漏。
// 正确做法是整段缓冲在 stats() 里统一用正则扫。判据要匹配**真实格式**，
// 不是「大概长什么样」。

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// countingWriter 累积 server 的明文 IO。统计在 stats() 里对整段缓冲做。
type countingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// 命令行形如 `a001 UID FETCH 1:10 (ENVELOPE ...)`。
var (
	reUIDFetch = regexp.MustCompile(`(?m)^[A-Za-z0-9]+\s+UID\s+FETCH\b`)
	reFetch    = regexp.MustCompile(`(?m)^[A-Za-z0-9]+\s+FETCH\b`)
	reUIDSrch  = regexp.MustCompile(`(?m)^[A-Za-z0-9]+\s+UID\s+SEARCH\b`)
)

func (c *countingWriter) stats() (fetch, search int) {
	c.mu.Lock()
	s := c.buf.String()
	c.mu.Unlock()
	fetch = len(reUIDFetch.FindAllString(s, -1)) + len(reFetch.FindAllString(s, -1))
	search = len(reUIDSrch.FindAllString(s, -1))
	return fetch, search
}

// startCountingIMAP 与 fetcher_pipeline_test.go 的 startIMAPServer 同构，
// 唯一区别是给 server 挂一个 DebugWriter 以便数命令。
func startCountingIMAP(t *testing.T, username, password string) (*testIMAP, *countingWriter,
	func(string, *imapclient.Options) (*imapclient.Client, error)) {
	t.Helper()
	serverTLS, clientTLS := selfSignedTLS(t)

	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	memServer.AddUser(user)

	cw := &countingWriter{}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		TLSConfig:    serverTLS,
		InsecureAuth: false,
		DebugWriter:  cw,
	})

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})

	addr := ln.Addr().(*net.TCPAddr)
	dial := func(a string, opts *imapclient.Options) (*imapclient.Client, error) {
		if opts == nil {
			opts = &imapclient.Options{}
		}
		opts.TLSConfig = clientTLS
		return imapclient.DialTLS(a, opts)
	}
	return &testIMAP{host: addr.IP.String(), port: addr.Port, user: user}, cw, dial
}

// measureRounds 同步 n 封纯文本邮件，返回 FETCH / SEARCH 次数与落库数。
func measureRounds(t *testing.T, n int) (fetch, search, saved int) {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, cw, dial := startCountingIMAP(t, "recipient@example.com", password)
	base := time.Now().UTC().Truncate(time.Second)
	appendN(t, ti, n, base)

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-rounds-"+strconv.Itoa(n), password, "")
	var err error
	saved, err = fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("sync(%d): %v", n, err)
	}
	fetch, search = cw.stats()
	return fetch, search, saved
}

func TestSyncFetchRoundTripsAreLinearInMessageCount(t *testing.T) {
	// 攒一张表，把「往返次数 vs 邮件数」的形状量出来。数字进 handoff，
	// 断言在下面两条用例里。
	for _, n := range []int{1, 5, 10, 20, 50} {
		f, s, saved := measureRounds(t, n)
		t.Logf("n=%2d saved=%2d FETCH=%3d SEARCH=%d  (每封 %.2f 次 FETCH)",
			n, saved, f, s, float64(f)/float64(n))
	}
}

// TestSyncUsesOneBatchedFetchForEnvelopes 钉住「批量取 envelope 只发一次 FETCH」。
// 修复前它也是 1，所以这条本身不承重，由下面那条承载真正的回归。
func TestSyncUsesOneBatchedFetchForEnvelopes(t *testing.T) {
	fetch, _, saved := measureRounds(t, 10)
	if saved != 10 {
		t.Fatalf("saved = %d, want 10", saved)
	}
	envelopeFetches := fetch - 10 // 减掉逐封 snippet 的那 10 次
	if envelopeFetches != 1 {
		t.Errorf("批量取 envelope 发了 %d 次 FETCH, want 1（应一次取回全部 UID）", envelopeFetches)
	}
}

// TestSyncSnippetFetchesAreStillPerMessage 这条**故意钉住现状**并把数字写死。
//
// 它是后续「把逐封 snippet 改成批量」的基线：那次改动会让这条转红，
// 写死数字是为了让「往返次数从 O(n) 降到 O(1)」有一个可对比的起点，
// 而不是拍脑袋。
func TestSyncSnippetFetchesAreStillPerMessage(t *testing.T) {
	fetch, _, _ := measureRounds(t, 10)
	if fetch != 11 {
		t.Errorf("FETCH = %d, want 11（1 次批量 envelope + 10 次逐封 snippet）—— "+
			"若实际数字变了，说明 snippet 补拉路径改过，请同步更新本用例与 handoff", fetch)
	}
}
