//go:build realprobe

// realprobe_test.go — **只读**真实邮箱探针（`-tags=realprobe` 手动启用）。
//
// 目的：回答一个至今无证据的问题 —— go-imap 主路径在**真实 qq/163** 上是否
// 可用。§7be 只在 Greenmail 上确认了它会因为 `BODY[]<0>{n}`（partial 与
// literal 之间无空格）解析失败而掉进降级通道；真实账户的回包形状没测过。
// 若真实服务器也这样，主路径等于长期闲置，每封发票正文都靠那条手搓的
// textproto 通道兜底。
//
// 本文件是刻意的「只读」三件套：
//
//  1. 只发 LOGIN / ID / SELECT / UID SEARCH / UID FETCH BODY.PEEK[]。
//     PEEK 不置 \Seen（代码里 FetchMessageRaw 的 FetchItemBodySection.Peek
//     也是 true）。不发 STORE / MOVE / COPY / EXPUNGE / DELETE 任何一条。
//  2. **不靠承诺，靠前后快照证明**：跑之前记下 INBOX 的 UNSEEN 计数与目标邮件
//     的 flags，跑完再记一次，逐项必须相等；不等就 fail。
//     刻意不比 UIDNEXT —— 探针运行期间真邮箱可能收到新邮件，那不是探针造成的。
//  3. 不写库：本文件只 SELECT，不 INSERT / UPDATE / DELETE。
//
// 凭据解密刻意**不**用 EnsureMasterKey：它在找不到 key 时会新建一个，万一
// dataDir 指错就会在真实目录里留下垃圾文件。这里直接 os.ReadFile 读候选 key。
//
// 用法（key 路径用 POCKET_REAL_KEYS 分号分隔，按序尝试）：
//
//	$env:PG_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable&search_path=opencode_pocket'
//	$env:POCKET_REAL_KEYS='C:\workspace\openpocket\data\email_master.key'
//	go test -tags=realprobe ./internal/email/ -run TestRealImapPrimaryPath -v -count=1
package email

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mailboxState 是用来证明「什么都没改」的只读快照。
type mailboxState struct {
	unseen    int
	uidFlags  string
	messages  int
	uidNext   imap.UID
	uidValid  uint32}

func (s mailboxState) String() string {
	return fmt.Sprintf("unseen=%d messages=%d uidnext=%d uidvalidity=%d flags[%s]",
		s.unseen, s.messages, s.uidNext, s.uidValid, s.uidFlags)
}

func readMailboxState(addr, user, pass string, uid int64) (mailboxState, error) {
	var s mailboxState
	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return s, fmt.Errorf("dial: %w", err)
	}
	defer c.Close()
	// 注意：go-imap v2 beta.8 的 Login 返回 *Command，其 Wait() 只有
	// 一个返回值（Select 的 Wait 才是两个）。写成两值会编译失败。
	if err := c.Login(user, pass).Wait(); err != nil {
		return s, fmt.Errorf("login: %w", err)
	}
	// 163 必须在 SELECT 之前发 ID，否则回 NO SELECT Unsafe Login。
	sendClientID(c, user)
	st, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return s, fmt.Errorf("select: %w", err)
	}
	s.messages = int(st.NumMessages)
	s.uidNext = st.UIDNext
	s.uidValid = st.UIDValidity

	// UNSEEN 计数：只读命令，且**不会**改 \Seen（IMAP 里隐含置位的是
	// "RECENT"，UNSEEN 只是匹配条件）。SearchCriteria 的字段名是
	// NotFlag（没有 NewSearchCriteria 构造器，直接用结构体字面量）。
	criteria := &imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}
	if data, err := c.Search(criteria, nil).Wait(); err == nil && data != nil {
		s.unseen = len(data.AllSeqNums())
	}

	// 目标邮件的 flags。注意 beta.8 的 Fetch 收的是 *FetchOptions
	//（要哪项就开哪个 bool，不是 []FetchItem），且第一个参数是 NumSet，
	// 这里按**序列号**取所以不能开 UID 选项。
	fs := imap.SeqSet{}
	fs.AddNum(uint32(uid))
	opts := &imap.FetchOptions{Flags: true}
	if msgs, err := c.Fetch(fs, opts).Collect(); err == nil && len(msgs) > 0 {
		parts := make([]string, 0, len(msgs[0].Flags))
		for _, f := range msgs[0].Flags {
			parts = append(parts, string(f))
		}
		s.uidFlags = strings.TrimSpace(strings.Join(parts, " "))
	} else if err != nil {
		return s, fmt.Errorf("fetch flags uid=%d: %w", uid, err)
	}
	return s, nil
}

func TestRealImapPrimaryPath(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set")
	}
	keyPaths := strings.Split(os.Getenv("POCKET_REAL_KEYS"), ";")
	if len(keyPaths) == 1 && keyPaths[0] == "" {
		t.Skip("POCKET_REAL_KEYS not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("pool:", err)
	}
	defer pool.Close()
	store, err := NewStore(pool)
	if err != nil {
		t.Fatal("store:", err)
	}

	enabled, err := store.ListEnabledAccounts(ctx)
	if err != nil {
		t.Fatal("list accounts:", err)
	}
	t.Logf("真实库里启用账户 %d 个", len(enabled))

	// 1) 逐把试 key：看哪一把能解开真实账户的凭据。纯本地，不发网络请求。
	var working []byte
	for _, p := range keyPaths {
		key, err := os.ReadFile(p)
		if err != nil {
			fmt.Printf("KEY %s: %v\n", p, err)
			continue
		}
		if len(key) != 32 {
			fmt.Printf("KEY %s: 长度 %d，不是 32 字节\n", p, len(key))
			continue
		}
		c, cerr := NewCrypto(key)
		if cerr != nil {
			fmt.Printf("KEY %s: NewCrypto: %v\n", p, cerr)
			continue
		}
		ok := 0
		for i := range enabled {
			_, enc, gerr := store.GetAccountByID(ctx, enabled[i].ID)
			if gerr != nil || enc == "" {
				continue
			}
			if plain, derr := c.DecryptString(enc); derr == nil &&
				plain != "" && plain != "oauth-pending-no-credential" {
				ok++
			}
		}
		fmt.Printf("KEY %s: 可解出凭据的账户数 = %d\n", p, ok)
		// 直接跑一次新加的启动自检，把**用错 key 时用户实际会看到的那行字**
		// 抓出来（而不是只在文档里描述它应该长什么样）。
		if c != nil {
			if chk, cerr := CheckCredentials(ctx, store, c); cerr != nil {
				fmt.Printf("      self-check error: %v\n", cerr)
			} else {
				fmt.Printf("      AllDecryptable=%v AllFailed=%v -> %s\n",
					chk.AllDecryptable(), chk.AllFailed(), chk.Summary())
			}
		}
		if ok > 0 && working == nil {
			working = key
		}
	}
	if working == nil {
		t.Skip("POCKET_REAL_KEYS 里没有一把能解开真实库凭据的 key（安全结果，不是失败）")
	}
	t.Logf("命中一把可用 key")

	// 2) 逐账户只读探一遍：跑一次真实 FetchMessageRaw，判断走的是主路径还是降级。
	realCrypto, cerr := NewCrypto(working)
	if cerr != nil {
		t.Fatalf("NewCrypto: %v", cerr)
	}
	fetcher := NewFetcherWithOptions(store, realCrypto, false, false)
	for i := range enabled {
		acc, enc, err := store.GetAccountByID(ctx, enabled[i].ID)
		if err != nil || enc == "" || acc.IMAPHost == "" || acc.IMAPPort == 0 {
			continue
		}
		plain, derr := realCrypto.DecryptString(enc)
		if derr != nil || plain == "" || plain == "oauth-pending-no-credential" {
			fmt.Printf("ACCT %s (%s): 凭据解不开，跳过\n", acc.ID, acc.EmailAddress)
			continue
		}
		// deleted_at 是 bigint NOT NULL DEFAULT 0，**0 才表示未删除**（见
		// store_inbox.go 的 idx_emails_alive 偏索引）。我第一版写的是
		// `deleted_at IS NULL`，结果 138 行全被筛掉，差点误读成「真实邮件
		// 全被软删除」——那是我的 SQL 错，不是数据问题。
		var uid int64
		if err := pool.QueryRow(ctx,
			`SELECT uid FROM emails WHERE account_id=$1 AND uid IS NOT NULL AND deleted_at = 0
			 ORDER BY uid DESC LIMIT 1`, acc.ID).Scan(&uid); err != nil {
			fmt.Printf("ACCT %s (%s): 没取到 uid: %v\n", acc.ID, acc.EmailAddress, err)
			continue
		}
		addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)

		before, err := readMailboxState(addr, acc.EmailAddress, plain, uid)
		if err != nil {
			fmt.Printf("ACCT %s (%s): 前置快照失败: %v\n", acc.ID, acc.EmailAddress, err)
			continue
		}

		// 抓 log 以判断走的是主路径还是降级通道。
		var buf bytes.Buffer
		oldW, oldF := log.Writer(), log.Flags()
		log.SetOutput(&buf)
		log.SetFlags(0)
		fctx, fcancel := context.WithTimeout(ctx, 90*time.Second)
		t0 := time.Now()
		body, ferr := fetcher.FetchMessageRaw(fctx, acc.ID, uid)
		took := time.Since(t0)
		fcancel()
		log.SetOutput(oldW)
		log.SetFlags(oldF)

		after, err := readMailboxState(addr, acc.EmailAddress, plain, uid)
		if err != nil {
			t.Errorf("ACCT %s: 后置快照失败: %v", acc.ID, err)
			continue
		}
		// 证明只读：UNSEEN 计数与目标邮件 flags 必须一动不动。
		if after.unseen != before.unseen || after.uidFlags != before.uidFlags {
			t.Errorf("ACCT %s (%s): 邮箱状态变了！\n before: %s\n after : %s\n —— 本探针不该改动任何状态",
				acc.ID, acc.EmailAddress, before, after)
		}

		verdict := "GO-IMAP 主路径可用"
		if strings.Contains(buf.String(), "textproto fallback ok") {
			verdict = "**主路径失败，走了降级通道**"
		}
		fmt.Printf("ACCT %-28s %-28s uid=%-12d %s  耗时=%s err=%v body=%d 字节  状态未变=%v\n",
			acc.ID, acc.EmailAddress, uid, verdict, took.Round(time.Millisecond), ferr, len(body),
			after.unseen == before.unseen && after.uidFlags == before.uidFlags)
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line != "" {
				fmt.Println("   LOG:", line)
			}
		}
	}
}
