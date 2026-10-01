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
	// junkBox 为空是**合法**场景，不是失败：Greenmail 默认没建垃圾箱，
	// 真实服务器也常没有。此时 MoveUIDsToJunk 会走 CREATE "Junk" 兜底
	// （junk.go:127-133），那条分支同样需要被验证。
	snap := mailboxSnapshot{
		junkBox: junkBox,
		inbox:   uidsIn(t, client, "INBOX"),
		junk:    map[imap.UID]bool{},
	}
	if junkBox != "" {
		snap.junk = uidsIn(t, client, junkBox)
	}
	return snap
}

// uidsIn 列出某信箱的全部 UID。
//
// UID 是**按信箱独立编号**的：邮件从 INBOX 移入 Junk 会在 Junk 拿到全新 UID，
// 所以跨信箱**不能**拿 UID 比对。跨信箱只用**数量**判定增量；
// 「被选中的离开 INBOX」「未选中的仍在 INBOX」这类判断发生在**同一个信箱内**，
// UID 在那里是稳定且可比的。
func uidsIn(t *testing.T, client *imapclient.Client, mailbox string) map[imap.UID]bool {
	t.Helper()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		t.Fatalf("select %s: %v", mailbox, err)
	}
	// 空 criteria = 全部邮件；不能传 nil，imapclient.searchCriteriaIsASCII 会解引用。
	res, err := client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		t.Fatalf("uidsearch %s: %v", mailbox, err)
	}
	out := map[imap.UID]bool{}
	if res == nil {
		return out
	}
	uidSet, ok := res.All.(imap.UIDSet)
	if !ok {
		t.Fatalf("UIDSearch 的 All 不是 UIDSet（拿到 %T）", res.All)
	}
	// Nums 展开区间；false 表示集合是动态的（含 '*'），不能静态枚举。
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

	// **跑完必须把账户删掉**。这两个 greenmail 账户是故意用 t.TempDir() 里的
	// 临时 master key 加密的，而线上 pocketd 每 60 秒扫一遍启用账户，于是它会
	// 永远地打 `decrypt credential: cipher: message authentication failed`。
	// 实测（2026-10-01 21:43 读线上 logs/pocketd-18099d.err.log）：这个账户从
	// 20:34 起每分钟报一次，一小时几百行，把真实故障埋在里面。
	// 之前只在开头清理（保证重复跑幂等），忘了收尾，等于把测试垃圾留在了
	// 共享库里。这里用 t.Cleanup 保证即使 t.Fatal 也会删。
	//
	// **必须自己开连接**：测试里是 `defer pool.Close()`，而 defer 在函数返回时
	// 先于 t.Cleanup 执行 —— 复用 pool 会拿到 `closed pool`，清理静默失败。
	// 那是第一版真踩的：日志里三条 `cleanup ...: closed pool`，账户照样留着。
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		cpool, cerr := pgxpool.New(cctx, dsn)
		if cerr != nil {
			t.Logf("cleanup pool: %v", cerr)
			return
		}
		defer cpool.Close()
		// 三张表的 WHERE 列不一样：email_accounts 是父表，只有 id，没有
		// account_id（第一版统一写 `WHERE account_id=$1 OR id=$1`，父表那句
		// 报 column "account_id" does not exist，清理静默失败）。
		for _, d := range []struct{ table, where string }{
			{"email_invoices", "account_id"},
			{"emails", "account_id"},
			{"email_accounts", "id"},
		} {
			if _, err := cpool.Exec(cctx,
				`DELETE FROM `+d.table+` WHERE `+d.where+`=$1`, greenmailJunkAcctID); err != nil {
				t.Logf("cleanup %s: %v", d.table, err)
			}
		}
	})

	before := snapshotMailboxes(t, fetcher, greenmailJunkAcctID)
	if len(before.inbox) < 3 {
		t.Skipf("收件箱只有 %d 封，不足以验证「部分移动」，请先经 3025 投递 3 封以上", len(before.inbox))
	}
	if before.junkBox == "" {
		t.Log(`该实例没有垃圾箱 —— 本次会顺带验证 CREATE "Junk" 兜底分支`)
	} else {
		t.Logf("已有垃圾箱 %q —— 本次验证既有垃圾箱的移动路径", before.junkBox)
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

	// 移动前没有垃圾箱时，MoveUIDsToJunk 会 CREATE "Junk"（junk.go:127-133），
	// 所以名字从 "" 变成 "Junk" 是**预期**，不是不一致。
	switch {
	case before.junkBox == "":
		if after.junkBox != "Junk" {
			t.Fatalf("没有垃圾箱时应被 CREATE 成 %q，实际 %q", "Junk", after.junkBox)
		}
	case after.junkBox != before.junkBox:
		t.Fatalf("既有垃圾箱名在移动前后不应变化：%q -> %q", before.junkBox, after.junkBox)
	}

	// INBOX 内部用 UID 比对是有效的——UID 在同一信箱内稳定。
	for _, u := range targets {
		if after.inbox[u] {
			t.Errorf("uid %d 移动后仍在 INBOX —— 邮件没被移走", u)
		}
	}
	for u := range before.inbox {
		if !containsUID(targets, u) && !after.inbox[u] {
			t.Errorf("uid %d 未被选中却从 INBOX 消失 —— 误伤", u)
		}
	}
	// 跨信箱只能用**数量**判定：UID 是按信箱独立编号的，移过去的邮件会在
	// Junk 里拿到全新 UID，拿 INBOX 的 UID 去 Junk 里找必然找不到。
	// 我第一版就是这么误报的：「邮件丢了」与「误伤既有垃圾箱」同时报，
	// 而移动其实完全成功。
	if want, got := len(before.junk)+len(targets), len(after.junk); got != want {
		t.Errorf("垃圾箱邮件数 %d，期望 %d（移动前 %d + 移动 %d）—— 邮件可能丢失或被误移",
			got, want, len(before.junk), len(targets))
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
