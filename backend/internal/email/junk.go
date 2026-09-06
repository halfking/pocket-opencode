package email

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// junk.go — 真实 IMAP MOVE：把判定为广告/垃圾的邮件移进服务商的垃圾箱。
//
// 之前 route-folder 意图被标 skipped（没有 MOVE 能力），广告只做了本地
// category 标记，收件箱里越积越多。这里补齐服务端语义：
//  1. LIST 找垃圾箱：优先带 \Junk 特殊属性（RFC 6154）的信箱，其次按
//     常见命名匹配（Junk / 垃圾邮件 / Junk E-mail / Spam / 垃圾箱）；
//  2. 找不到时尝试 CREATE "Junk"（exmail/163/qq 都允许），失败则放弃并
//     返回错误，由调用方决定是否只落本地标记；
//  3. go-imap 的 Move(numSet, mailbox) 收到 UIDSet 时自动发 UID MOVE，
//     服务器不支持 MOVE 扩展时自动回退 COPY + \Deleted + EXPUNGE。
//
// 每次调用一条独立 IMAP 连接（与 Sync 同风格）：MOVE 是低频操作，复用
// 连接的复杂度（UIDVALIDITY 漂移、连接池生命周期）不值得。

// junkMailboxNames 常见垃圾箱命名（按优先级）。
var junkMailboxNames = []string{"Junk", "垃圾邮件", "Junk E-mail", "Junk Email", "Spam", "垃圾箱", "广告邮件"}

// findJunkMailbox 在已登录的连接上定位垃圾箱。返回完整信箱名。
func findJunkMailbox(c *imapclient.Client) (string, error) {
	listCmd := c.List("", "*", &imap.ListOptions{})
	var specialUse, byName string
	for {
		item := listCmd.Next()
		if item == nil {
			break
		}
		if hasMailboxAttr(item.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		name := item.Mailbox
		if hasMailboxAttr(item.Attrs, imap.MailboxAttrJunk) {
			specialUse = name
			break
		}
		if byName == "" {
			base := baseMailboxName(name)
			for _, want := range junkMailboxNames {
				if strings.EqualFold(base, want) {
					byName = name
					break
				}
			}
		}
	}
	if err := listCmd.Close(); err != nil {
		return "", fmt.Errorf("list mailboxes: %w", err)
	}
	if specialUse != "" {
		return specialUse, nil
	}
	if byName != "" {
		return byName, nil
	}
	return "", nil
}

// hasMailboxAttr 判断属性列表是否含给定属性（beta.8 的 Attrs 是切片）。
func hasMailboxAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// baseMailboxName 取层级分隔符后的最后一段（QQ 企业邮返回 "其他文件夹/垃圾邮件" 这类带前缀的名字）。
func baseMailboxName(name string) string {
	if i := strings.LastIndexAny(name, "/\\."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// MoveEmailsToJunk 把一批 UID 移入账户的垃圾箱。返回成功移动的 UID 数。
//
// err == ErrNoJunkMailbox 表示服务器没有垃圾箱也建不出来：调用方可以只做
// 本地标记。部分 UID 失败不影响其余（逐条 MOVE，幂等可重放）。
func (f *Fetcher) MoveEmailsToJunk(ctx context.Context, accountID string, uids []int64) (int, error) {
	if len(uids) == 0 {
		return 0, nil
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return 0, fmt.Errorf("account disabled")
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return 0, fmt.Errorf("decrypt credential: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		return 0, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()
	if err := f.login(client, *acc, cred); err != nil {
		return 0, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}

	junkBox, err := findJunkMailbox(client)
	if err != nil {
		return 0, err
	}
	if junkBox == "" {
		if cerr := client.Create("Junk", nil).Wait(); cerr != nil {
			return 0, fmt.Errorf("%w: create failed: %v", ErrNoJunkMailbox, cerr)
		}
		junkBox = "Junk"
		log.Printf("[email/junk] created junk mailbox for %s", acc.EmailAddress)
	}

	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return 0, fmt.Errorf("select INBOX: %w", err)
	}
	moved := 0
	var moveErrs []string
	for _, uid := range uids {
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		if _, merr := client.Move(uidSet, junkBox).Wait(); merr != nil {
			moveErrs = append(moveErrs, fmt.Sprintf("uid=%d: %v", uid, merr))
			continue
		}
		moved++
	}
	if len(moveErrs) > 0 {
		return moved, fmt.Errorf("moved %d/%d (%s)", moved, len(uids), strings.Join(moveErrs, "; "))
	}
	return moved, nil
}

// ErrNoJunkMailbox 保留语义占位：findJunkMailbox + Create 失败时返回的是
// 包装错误，这里显式导出便于上层判断（当前上层按 error 非空统一记日志）。
var ErrNoJunkMailbox = fmt.Errorf("email: junk mailbox unavailable")
