//go:build greenmail

// junk_greenmail_test.go — 需求 2「移到垃圾邮件箱」的真实 IMAP 链路验证。
//
// 为什么需要它：junk.go 的定位与移动逻辑此前只有纯函数层面的覆盖
//（junk_mailbox_test.go），**没有任何测试真正驱动过 UID MOVE**。
// 而 fetcher_greenmail_test.go 挂在同一个 build tag 下却只覆盖同步，不覆盖 MOVE。
//
// 这一层要证明的是纯函数证明不了的三件事：
//  1. findJunkMailbox 在真实 LIST 响应里能定位到垃圾箱（Greenmail 用
//     -Dgreenmail.setup.test.all 建了全套标准信箱）；
//  2. MoveUIDsToJunk 的逐条 MOVE 在真实连接上全部成功；
//  3. 移动后 **INBOX 里确实没有那几封了**，且垃圾箱里**确实多出了它们**——
//     只断言返回值不够，那只能证明没报错。
//
// 前置：
//   docker run -d --rm --name greenmail-test -p 3025:3025 -p 3993:3993 \
//     greenmail/standalone:latest -Dgreenmail.setup.test.all \
//     -Dgreenmail.users=huangxutao@kxmail.local:h8pass
//   通过 3025 SMTP 投递若干封邮件到 huangxutao@kxmail.local
//   env PG_DSN=postgresql://...:.../pocket?sslmode=disable go test -tags=greenmail \
//     ./internal/email/ -run TestMoveToJunkGreenmail -v
package email

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	imapclient "github.com/emersion/go-imap/v2/imapclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

const greenmailJunkAcctID = "acct-greenmail-junk"

// mailboxSnapshot 是一次观测：垃圾箱名 + 两个信箱各自的 UID 集合。
type mailboxSnapshot struct {
	junkBox string
	inbox   map[imap.UID]bool
	junk    map[imap.UID]bool
}

// snapshotMailboxes 独立开一条 IMAP 连接观测真实信箱状态。
//
// 刻意不复用 MoveUIDsToJunk 内部的连接：验证必须在**新连接**上做，
// 否则只能证明同一会话里的内存状态。
func snapshotMailboxes(t *testing.T, f *Fetcher, accountID string) mailboxSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	acc, encrypted, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	cred, err := f.crypto.DecryptString(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	addr := addrOf(acc)
	client, err := f.dial(addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer client.Close()
	if err := f.login(client, *acc, cred); err != nil {
		t.Fatalf("login: %v", err)
	}

	junkBox, err := findJunkMailbox(client)
	if err != nil {
		t.Fatalf("findJunkMailbox: %v", err)
	}
	if junkBox == "" {
		t.Skipf("该 Greenmail 实例没有垃圾箱，无法验证 MOVE（junkBox 为空）")
	}
	return mailboxSnapshot{
		junkBox: junkBox,
		inbox:   uidsIn(t, client, "INBOX"),
		junk:    uidsIn(t, client, junkBox),
	}
}

// uidsIn 列出某信箱的全部 UID。移动后 UID 保持不变（IMAP UID 是单调的），
// 所以可以直接用移动前的 UID 集合去移动后的信箱里比对。
func uidsIn(t *testing.T, client *imapclient.Client, mailbox string) map[imap.UID]bool {
	t.Helper()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		t.Fatalf("select %s: %v", mailbox, err)
	}
	// nil criteria = 全部邮件；搜索范围是上面 Select 选中的信箱。
	// 必须用 UIDSearch：普通 Search 返回的 SearchData.All 是序号集（NumSet），
	// 只有 UIDSearch 才保证 All 是 imap.UIDSet。
	res, err := client.UIDSearch(nil, nil).Wait()
	if err != nil {
		t.Fatalf("uidsearch %s: %v", mailbox, err)
	}
	out := map[imap.UID]bool{}
	if res == nil {
		return out
	}
	uidSet, ok := res.All.(imap.UIDSet)
	if !ok {
		t.Fatalf("UIDSearch 的 All 不是 UIDSet（拿到 %T），无法按 UID 比对", res.All)
	}
	// Nums 展开区间；第二个返回值为 false 表示集合是动态的（含 '*'），
	// 那种情况不能静态枚举，直接失败比静默漏邮件好。
	nums, static := uidSet.Nums()
	if !static {
		t.Fatalf("UIDSearch 返回动态 UID 集合（可能含 '*'），无法静态枚举")
	}
	for _, u := range nums {
		out[u] = true
	}
	return out
}

func addrOf(a *Account) string {
	return a.IMAPHost + ":" + strconv.Itoa(a.IMAPPort)
}

func TestMoveToJunkGreenmail(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set; skipping greenmail integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	masterKey, err := EnsureMasterKey("", t.TempDir())
	if err != nil {
		t.Fatal("master:", err)
	}
	c, err := NewCrypto(masterKey)
	if err != nil {
		t.Fatal("crypto:", err)
	}
	fetcher := NewFetcherWithOptions(store, c, true, false)

	now := time.Now().Unix()
	acc := &Account{
		ID:              greenmailJunkAcctID,
		UserID:          "user-admin",
		WorkspaceID:     "ws_user-admin",
		DisplayName:     "Greenmail 垃圾箱测试",
		EmailAddress:    "huangxutao@kxmail.local",
		IMAPHost:        "127.0.0.1",
		IMAPPort:        3993,
		AuthType:        "password",
		SyncIntervalMin: 5,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	encrypted, err := c.EncryptString("h8pass")
	if err != nil {
		t.Fatal("encrypt:", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM email_accounts WHERE id=$1`, greenmailJunkAcctID); err != nil {
		t.Fatal("cleanup acct:", err)
	}
	if err := store.InsertAccount(ctx, acc, encrypted); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	before := snapshotMailboxes(t, fetcher, greenmailJunkAcctID)
	if len(before.inbox) < 3 {
		t.Skipf("收件箱只有 %d 封，不足以验证「部分移动」，请先经 3025 投递 3 封以上", len(before.inbox))
	}
	t.Logf("移动前: junkBox=%q inbox=%d 封 junk=%d 封",
		before.junkBox, len(before.inbox), len(before.junk))

	// 只移动 2 封，保留其余 —— 验证「部分移动」不会误伤同批其它邮件。
	targets := firstNUIDs(before.inbox, 2)
	var uids []int64
	for _, u := range targets {
		uids = append(uids, int64(u))
	}

	moved, err := fetcher.MoveUIDsToJunk(ctx, greenmailJunkAcctID, uids)
	if err != nil {
		t.Fatalf("MoveUIDsToJunk: %v", err)
	}
	if len(moved) != len(uids) {
		t.Fatalf("moved=%d 期望 %d —— 只返回成功集合，不能静默少移", len(moved), len(uids))
	}

	// 关键：新连接上复核，邮件真的换地方了。
	after := snapshotMailboxes(t, fetcher, greenmailJunkAcctID)
	if after.junkBox != before.junkBox {
		t.Fatalf("垃圾箱名在移动前后不一致：%q -> %q", before.junkBox, after.junkBox)
	}
	for _, u := range targets {
		if after.inbox[u] {
			t.Errorf("uid %d 移动后仍在 INBOX —— 邮件没被移走", u)
		}
		if !after.junk[u] {
			t.Errorf("uid %d 移动后不在垃圾箱 %q —— 邮件丢了", u, after.junkBox)
		}
	}
	for u := range before.inbox {
		if !containsUID(targets, u) && !after.inbox[u] {
			t.Errorf("uid %d 未被选中却从 INBOX 消失 —— 误伤", u)
		}
	}
	for u := range before.junk {
		if !after.junk[u] {
			t.Errorf("uid %d 移动前就在垃圾箱，移动后却消失 —— 误伤既有垃圾箱", u)
		}
	}
}

func containsUID(list []imap.UID, u imap.UID) bool {
	for _, x := range list {
		if x == u {
			return true
		}
	}
	return false
}

func firstNUIDs(set map[imap.UID]bool, n int) []imap.UID {
	var out []imap.UID
	for u := range set {
		out = append(out, u)
		if len(out) >= n {
			break
		}
	}
	// 排序保证可重复运行得到同一批目标。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
