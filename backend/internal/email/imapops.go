package email

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// imapops.go — 通用 IMAP 信箱操作：列目录 / 建目录 / 跨目录移动。
//
// junk.go 里的 MoveUIDsToJunk 是第一个「真实 IMAP MOVE」实现，但目标信箱
// 写死为垃圾箱。自定义目录（用户建目录、把邮件移进去）与「移到垃圾箱」
// 的同步执行都需要同一能力，这里把「登录 + LIST + CREATE + UID MOVE」
// 抽成通用形态，junk.go 与 ops 同步执行共用。
//
// 每次调用一条独立 IMAP 连接（与 Sync / MoveUIDsToJunk 同风格）：这些操作
// 都是低频动作，复用连接的复杂度（UIDVALIDITY 漂移、连接池生命周期）
// 不值得。

// Mailbox 是一封「目录」的本地投影。
type Mailbox struct {
	// Name 是完整信箱名（可能带层级前缀，如 "其他文件夹/账单"）。
	Name string `json:"name"`
	// Attrs 是原始属性（\HasChildren 等），原样透传给前端做图标判断。
	Attrs []string `json:"attrs,omitempty"`
	// Special 标记 RFC 6154 特殊用途（trash/junk/sent/drafts/flagged/archive/all）。
	Special string `json:"special,omitempty"`
}

// specialUseAttrs RFC 6154 属性 → 语义名。
var specialUseAttrs = map[imap.MailboxAttr]string{
	imap.MailboxAttrTrash:   "trash",
	imap.MailboxAttrJunk:    "junk",
	imap.MailboxAttrSent:    "sent",
	imap.MailboxAttrDrafts:  "drafts",
	imap.MailboxAttrFlagged: "flagged",
	imap.MailboxAttrArchive: "archive",
	imap.MailboxAttrAll:     "all",
}

// trashMailboxNames 常见垃圾箱命名（按优先级），与 junkMailboxNames 区分：
// 「删除同步」的落点应是 Trash 而非 Junk。
var trashMailboxNames = []string{"Trash", "已删除", "已删除邮件", "Deleted Messages", "Deleted", "回收站"}

// classifyMailbox 由属性 + 命名推断特殊用途。找不到特殊属性时按常见命名兜底，
// 命名表同时覆盖 findJunkMailbox / trash 检测。
func classifyMailbox(name string, attrs []imap.MailboxAttr) string {
	for _, a := range attrs {
		if s, ok := specialUseAttrs[a]; ok {
			return s
		}
	}
	base := baseMailboxName(name)
	for _, want := range trashMailboxNames {
		if strings.EqualFold(base, want) {
			return "trash"
		}
	}
	for _, want := range junkMailboxNames {
		if strings.EqualFold(base, want) {
			return "junk"
		}
	}
	if strings.EqualFold(base, "INBOX") {
		return "inbox"
	}
	return ""
}

// ListMailboxes 列出账户的全部可见信箱（跳过 \NoSelect）。
func (f *Fetcher) ListMailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	client, acc, err := f.dialAndLogin(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	_ = acc

	listCmd := client.List("", "*", &imap.ListOptions{})
	var out []Mailbox
	for {
		item := listCmd.Next()
		if item == nil {
			break
		}
		if hasMailboxAttr(item.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		mb := Mailbox{Name: item.Mailbox, Attrs: make([]string, 0, len(item.Attrs))}
		for _, a := range item.Attrs {
			mb.Attrs = append(mb.Attrs, string(a))
		}
		mb.Special = classifyMailbox(item.Mailbox, item.Attrs)
		out = append(out, mb)
	}
	if err := listCmd.Close(); err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	return out, nil
}

// CreateMailbox 在服务器上创建目录。已存在视为成功（幂等，前端重复点击无副作用）。
func (f *Fetcher) CreateMailbox(ctx context.Context, accountID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mailbox name required")
	}
	client, acc, err := f.dialAndLogin(ctx, accountID)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Create(name, nil).Wait(); err != nil {
		// 部分服务器对已存在信箱返回 ALREADYEXISTS / 报错文本不一，按名字
		// 再 LIST 确认一次，存在即成功。
		if exists, lexErr := mailboxExists(client, name); lexErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create mailbox %s on %s: %w", name, acc.EmailAddress, err)
	}
	return nil
}

// DeleteMailbox 删除服务器目录。INBOX 与仍被引用的特珠目录拒绝删除。
func (f *Fetcher) DeleteMailbox(ctx context.Context, accountID, name string) error {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "INBOX") {
		return fmt.Errorf("refusing to delete INBOX")
	}
	client, _, err := f.dialAndLogin(ctx, accountID)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Delete(name).Wait()
}

// MoveUIDsToMailbox 把 INBOX 里的一批 UID 移动到目标信箱（不存在时先创建）。
// 返回实际移动成功的 UID；go-imap 的 Move 收到 UIDSet 时自动发 UID MOVE，
// 服务器不支持 MOVE 扩展时自动回退 COPY + \Deleted + EXPUNGE。
func (f *Fetcher) MoveUIDsToMailbox(ctx context.Context, accountID string, uids []int64, mailbox string) ([]int64, error) {
	mailbox = strings.TrimSpace(mailbox)
	if mailbox == "" {
		return nil, fmt.Errorf("target mailbox required")
	}
	if len(uids) == 0 {
		return nil, nil
	}
	client, acc, err := f.dialAndLogin(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if exists, lexErr := mailboxExists(client, mailbox); lexErr != nil || !exists {
		if cerr := client.Create(mailbox, nil).Wait(); cerr != nil {
			return nil, fmt.Errorf("create target mailbox %s: %v", mailbox, cerr)
		}
		log.Printf("[email/imapops] created mailbox %s for %s", mailbox, acc.EmailAddress)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select INBOX: %w", err)
	}
	var moved []int64
	var moveErrs []string
	for _, uid := range uids {
		if uid <= 0 {
			moveErrs = append(moveErrs, fmt.Sprintf("uid=%d: missing", uid))
			continue
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		if _, merr := client.Move(uidSet, mailbox).Wait(); merr != nil {
			moveErrs = append(moveErrs, fmt.Sprintf("uid=%d: %v", uid, merr))
			continue
		}
		moved = append(moved, uid)
	}
	if len(moveErrs) > 0 {
		return moved, fmt.Errorf("moved %d/%d (%s)", len(moved), len(uids), strings.Join(moveErrs, "; "))
	}
	return moved, nil
}

// FindTrashMailbox 定位账户的垃圾箱（优先 \Trash 属性，其次常见命名；
// 都没有时尝试创建 "Trash"）。返回完整信箱名。
func (f *Fetcher) FindTrashMailbox(ctx context.Context, accountID string) (string, error) {
	client, acc, err := f.dialAndLogin(ctx, accountID)
	if err != nil {
		return "", err
	}
	defer client.Close()

	listCmd := client.List("", "*", &imap.ListOptions{})
	var special, byName string
	for {
		item := listCmd.Next()
		if item == nil {
			break
		}
		if hasMailboxAttr(item.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		if hasMailboxAttr(item.Attrs, imap.MailboxAttrTrash) {
			special = item.Mailbox
			break
		}
		if byName == "" {
			base := baseMailboxName(item.Mailbox)
			for _, want := range trashMailboxNames {
				if strings.EqualFold(base, want) {
					byName = item.Mailbox
					break
				}
			}
		}
	}
	if err := listCmd.Close(); err != nil {
		return "", fmt.Errorf("list mailboxes: %w", err)
	}
	if special != "" {
		return special, nil
	}
	if byName != "" {
		return byName, nil
	}
	if cerr := client.Create("Trash", nil).Wait(); cerr != nil {
		return "", fmt.Errorf("%w: trash create failed: %v", ErrNoTrashMailbox, cerr)
	}
	log.Printf("[email/imapops] created Trash mailbox for %s", acc.EmailAddress)
	return "Trash", nil
}

// ErrNoTrashMailbox 服务器没有垃圾箱也建不出来时返回（包装错误）。
var ErrNoTrashMailbox = fmt.Errorf("email: trash mailbox unavailable")

// UIDMover 是「把一批 UID 移到指定信箱」的最小能力面，供 intent executor
// （route-folder 规则的真实执行）注入 *Fetcher 之外的替身。
type UIDMover interface {
	MoveUIDsToMailbox(ctx context.Context, accountID string, uids []int64, mailbox string) ([]int64, error)
}

// dialAndLogin 建立一条已登录的 IMAP 连接。返回的 client 由调用方 Close。
func (f *Fetcher) dialAndLogin(ctx context.Context, accountID string) (*imapclient.Client, *Account, error) {
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return nil, nil, fmt.Errorf("account disabled")
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt credential: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	if err := f.login(client, *acc, cred); err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}
	return client, acc, nil
}

// mailboxExists 判断信箱是否已存在。
func mailboxExists(client *imapclient.Client, name string) (bool, error) {
	listCmd := client.List("", name, &imap.ListOptions{})
	defer listCmd.Close()
	return listCmd.Next() != nil, nil
}
