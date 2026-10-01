package email

// diag_kxpms_test.go — 定位「某个真实账户同步挂满 90s」的**卡在哪条 IMAP 命令**。
//
// 2026-10-01 05:01 实测：7 个账户里 4 个 256~478ms 完成，huangxutao@kxpms.cn
// （企业微信，imap.exmail.qq.com）单独挂满 90s 被放弃等待。连接层已排除：
// 逐 IP 测得 TCP 41~191ms、TLS 110~187ms、greeting 34~90ms，服务端回的是
// `* OK [CAPABILITY IMAP4 IMAP4rev1 ID AUTH=PLAIN AUTH=LOGIN NAMESPACE] QQMail
// IMAP4Server ready` —— 服务端完全正常。所以卡点在登录之后的某条命令上，
// 而 Sync 里**一条命令一级打点都没有**，只能看到整体 TIMED OUT。
//
// 本测试逐条命令打点，每条套独立超时：诊断本身不会挂在同一个地方，
// 挂住时输出直接告诉你是哪条命令。
//
// 严格只读：只做 CAPABILITY / LOGIN / ID / SELECT / STATUS / UID SEARCH，
// 不 FETCH 正文、不改任何邮箱状态、不写业务库。与真实邮箱验证的既有约定一致。
//
// 默认跳过：需 POCKET_DIAG_ACCOUNT=<邮箱地址> **且** POCKET_DIAG_ALLOW=1
// 二次确认，防止误跑在别的账户上。凭证从生产库读密文、用真实 master key 解，
// 全程不打印明文。

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// diagStepTimeout 单条命令的上限。企业微信这次挂满 90s，20s 足够暴露问题，
	// 又不会让诊断本身跑成一个小时。
	diagStepTimeout = 20 * time.Second
	// diagSlowWarn 超过这个耗时就单独打醒目日志，区分「慢」与「卡死」。
	diagSlowWarn = 2 * time.Second
)

func TestDiagIMAPStepTiming(t *testing.T) {
	target := strings.TrimSpace(os.Getenv("POCKET_DIAG_ACCOUNT"))
	if target == "" {
		t.Skip("POCKET_DIAG_ACCOUNT not set")
	}
	if os.Getenv("POCKET_DIAG_ALLOW") != "1" {
		t.Skip("POCKET_DIAG_ALLOW != 1; set it to confirm a live read-only probe")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}

	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	var host, encrypted, authType string
	var port int
	var lastSyncedUID int64
	err = pool.QueryRow(ctx, `
		SELECT imap_host, imap_port, auth_type, credential_encrypted, last_synced_uid
		FROM email_accounts WHERE email_address = $1`, target).
		Scan(&host, &port, &authType, &encrypted, &lastSyncedUID)
	if err != nil {
		t.Fatalf("load account %s: %v", target, err)
	}
	t.Logf("target=%s host=%s:%d auth=%s lastSyncedUID=%d", target, host, port, authType, lastSyncedUID)

	// 走真实 master key 路径解密；不打印明文。
	dataDir := os.Getenv("POCKET_DIAG_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Skipf("master key unavailable under %s: %v (run from repo root)", dataDir, err)
	}
	crypto, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	cred, err := crypto.DecryptString(encrypted)
	if err != nil {
		t.Fatalf("decrypt credential: %v", err)
	}
	if cred == "" {
		t.Fatalf("empty credential for %s", target)
	}

	// 复用生产 dial/login，保证诊断跑的就是线上那条路径。
	f := NewFetcher(nil, crypto)
	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := f.dial(addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer client.Close()

	// 每条命令独立计时 + 独立超时。
	step := func(name string, wait func() error) {
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- wait() }()
		select {
		case err := <-done:
			el := time.Since(start).Round(time.Millisecond)
			switch {
			case err != nil:
				t.Logf("  %-16s %8s  ERR %v", name, el, err)
			case el > diagSlowWarn:
				t.Logf("  %-16s %8s  <<< SLOW", name, el)
			default:
				t.Logf("  %-16s %8s", name, el)
			}
		case <-time.After(diagStepTimeout):
			t.Logf("  %-16s  >%s  <<<< HUNG —— 服务端对该命令无响应", name, diagStepTimeout)
		}
	}

	acc := Account{EmailAddress: target, IMAPHost: host, IMAPPort: port, AuthType: authType}
	t.Log("逐步时序：")
	step("CAPABILITY", func() error { _, e := client.Capability().Wait(); return e })
	step("LOGIN", func() error { return f.login(client, acc, cred) })
	// ID 是首要嫌疑：sendClientID 里的 Wait() **没有任何超时保护**。
	// 企业微信若对 ID 不返回 tagged response，这里就永久阻塞。
	step("ID", func() error {
		_, e := client.ID(&imap.IDData{
			Name: "pocketd", Version: "1.0.0", Vendor: "openpocket", Address: target,
		}).Wait()
		return e
	})

	var numMessages uint32
	step("SELECT INBOX", func() error {
		mbox, e := client.Select("INBOX", nil).Wait()
		if e == nil {
			numMessages = mbox.NumMessages
		}
		return e
	})
	step("STATUS INBOX", func() error {
		_, e := client.Status("INBOX", &imap.StatusOptions{
			NumMessages: true, UIDNext: true, UIDValidity: true,
		}).Wait()
		return e
	})
	var highest imap.UID
	var numUIDs int
	step("UID SEARCH 1:*", func() error {
		set := imap.UIDSet{}
		set.AddRange(1, imap.UID(math.MaxUint32))
		data, e := client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}},
			&imap.SearchOptions{ReturnAll: true, ReturnMin: true, ReturnMax: true, ReturnCount: true}).Wait()
		if e == nil && data != nil {
			numUIDs = len(data.AllUIDs())
			highest = imap.UID(data.Max)
		}
		return e
	})
	t.Logf("INBOX numMessages=%d  matchedUIDs=%d  maxUID=%d", numMessages, numUIDs, highest)

	// ---- 复刻 Sync 的真实取数路径（fetcher.go:384-472）----
	//
	// 上面的命令全绿不代表 Sync 会跑完：Sync 在 envelope 拿不到 snippet 时，
	// 会在**同一连接上逐封串行**补拉 BODY[TEXT]<0.1024>
	//（fetchSnippetOnConnected），这一步既无超时也无并发。6 封邮件串行
	// 足以把整轮拖到分钟级。这里逐封计时，定位是不是它。
	uidNext := imap.UID(0)
	step("SELECT(again) 记 UIDNext", func() error {
		mbox, e := client.Select("INBOX", nil).Wait()
		if e == nil {
			uidNext = mbox.UIDNext
		}
		return e
	})

	var newUIDs []imap.UID
	step("UID SEARCH (last+1:next)", func() error {
		criteria := &imap.SearchCriteria{}
		if lastSyncedUID > 0 {
			var set imap.UIDSet
			set.AddRange(imap.UID(lastSyncedUID+1), uidNext)
			criteria.UID = []imap.UIDSet{set}
		}
		data, e := client.UIDSearch(criteria, nil).Wait()
		if e == nil && data != nil {
			newUIDs = data.AllUIDs()
		}
		return e
	})
	t.Logf("待处理新邮件 = %d 封 (UIDNext=%d, lastSyncedUID=%d)", len(newUIDs), uidNext, lastSyncedUID)
	if len(newUIDs) == 0 {
		t.Log("无新邮件 —— Sync 会在 search 后直接返回，本账户这次不该卡。")
		return
	}
	if len(newUIDs) > 50 {
		newUIDs = newUIDs[len(newUIDs)-50:]
	}

	var uidSet imap.UIDSet
	for _, u := range newUIDs {
		uidSet.AddNum(u)
	}
	step("FETCH envelope(批量)", func() error {
		_, e := client.Fetch(uidSet, &imap.FetchOptions{
			Envelope: true, UID: true, InternalDate: true,
		}).Collect()
		return e
	})

	// 嫌疑最大的一步：逐封补拉 snippet。只取正文前 1024 字节，仍是只读。
	for i, u := range newUIDs {
		if i >= 8 {
			t.Logf("  ... 另有 %d 封未测（诊断只打前 8 封）", len(newUIDs)-8)
			break
		}
		uid := u
		step(fmt.Sprintf("snippet uid=%d", uid), func() error {
			s := f.fetchSnippetOnConnected(client, uid)
			if s == "" {
				return fmt.Errorf("空 snippet")
			}
			return nil
		})
	}
}
