package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/halfking/pocket-opencode/backend/internal/email/rules"
)

// Fetcher 通过 IMAP 拉取邮件。
type Fetcher struct {
	store  *Store
	crypto *Crypto
	// dialTLS 是可替换的连接入口，nil 时用 imapclient.DialTLS。
	// 仅为测试留缝：Sync 原先直接写死 imapclient.DialTLS(addr, nil)，
	// 没法指向本地 IMAP server，整条抓取链路因此无法自动化验证。
	// 生产路径行为不变。
	dialTLS func(addr string, opts *imapclient.Options) (*imapclient.Client, error)
}

// NewFetcher 构造 Fetcher。
func NewFetcher(store *Store, crypto *Crypto) *Fetcher {
	return &Fetcher{store: store, crypto: crypto}
}

func (f *Fetcher) dial(addr string) (*imapclient.Client, error) {
	if f.dialTLS != nil {
		return f.dialTLS(addr, nil)
	}
	return imapclient.DialTLS(addr, nil)
}

// login 根据账户 authType 选择合适的 IMAP 鉴权机制。
//
//   - authType == "oauth2"：先尝试 OAUTHBEARER（RFC 7628），若 server 不
//     公告该 capability，回退 XOAUTH2（RFC 4959）。OAuth token 由主链路
//     OAuthCallback 持久化在 email_oauth_tokens 表；如果该账户没有 OAuth
//     token 而 credential_encrypted 是 IMAP password，则允许兼容旧实现。
//   - authType == "password"（默认）：使用 go-imap 内置 Login（PLAIN SASL）。
func (f *Fetcher) login(client *imapclient.Client, acc Account, cred string) error {
	switch acc.AuthType {
	case "oauth2":
		if client.Caps().Has(imap.AuthCap(sasl.OAuthBearer)) {
			saslClient := NewOAuthBearerClient(acc.EmailAddress, cred, acc.IMAPHost, acc.IMAPPort)
			if err := client.Authenticate(saslClient); err == nil {
				return nil
			} else {
				log.Printf("[email/fetcher] OAUTHBEARER for %s failed: %v; falling back to XOAUTH2", acc.EmailAddress, err)
			}
		}
		saslClient := NewXOAuth2Client(acc.EmailAddress, cred)
		return client.Authenticate(saslClient)
	default:
		return client.Login(acc.EmailAddress, cred).Wait()
	}
}

// RefreshTokenForAccount is a thin wrapper around RefreshAccessToken so that
// the Fetcher (and tests) can run an on-demand refresh outside of the
// scheduler loop. Returns the plaintext new access token.
func (f *Fetcher) RefreshTokenForAccount(
	ctx context.Context,
	refresher OAuthRefresher,
	tokenURL, clientID, clientSecret string,
	accountID string,
) (string, error) {
	if f == nil || f.store == nil || f.crypto == nil {
		return "", fmt.Errorf("email: fetcher not configured")
	}
	return RefreshAccessToken(ctx, f.crypto, f.store, refresher, tokenURL, clientID, clientSecret, accountID)
}

// FetchBody 按 UID 单封拉取完整正文（TEXT part, RFC 3501 §6.4.5）。
//
// 用于 GET /api/emails/{id}/body：上层先按 user/workspace 取出 email，
// 拿到 accountID + UID 后调用本方法，不在请求路径上接受 client 提供的
// account/workspace。返回的字节是 IMAP server 解码后的 UTF-8 正文；multipart
// 文本取第一个非空 text/* part，HTML 不会被剥离，前端可按需另走 mime 解析。
//
// maxBytes<=0 时不做客户端截断，调用方负责收尾限速；这里优先正确性。
func (f *Fetcher) FetchBody(ctx context.Context, accountID string, uid int64, maxBytes int) ([]byte, error) {
	if f == nil || f.store == nil || f.crypto == nil {
		return nil, fmt.Errorf("email: fetcher not configured")
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return nil, fmt.Errorf("account disabled")
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		return nil, fmt.Errorf("account has no usable credential")
	}
	if uid <= 0 {
		return nil, fmt.Errorf("invalid uid")
	}

	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	if err := f.login(client, *acc, cred); err != nil {
		return nil, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select INBOX: %w", err)
	}

	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(uid))
	fetchOpts := &imap.FetchOptions{
		UID: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierText,
			Peek:      true,
		}},
	}
	if maxBytes > 0 {
		fetchOpts.BodySection[0].Partial = &imap.SectionPartial{Offset: 0, Size: int64(maxBytes)}
	}
	// uidSet 必须按值传：imapwire.NumSetKind 对 imap.NumSet 做类型 switch，只
	// 认 imap.SeqSet / imap.UIDSet 值类型，*imap.UIDSet 会落到 default 分支直接
	// panic("imap: invalid NumSet type")。
	messages, err := client.Fetch(uidSet, fetchOpts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid=%d: %w", uid, err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("uid %d not found", uid)
	}
	body, err := findBodySection(messages[0].BodySection)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(body) > maxBytes {
		body = body[:maxBytes]
	}
	return body, nil
}

// recordActionIntent 把副作用型规则建议写入 email_action_intents。
//
// idempotency_key = sha256(email_id || action || folder) 的 hex 前 32 字节：
// 同一封邮件的同一动作只产生一行；folder 留空时（如 trigger-autoreply）
// 仍能与 route-folder with folder=xxx 区分，不会合并。
//
// userID 取自所属账户行（account 在创建时绑定 user/workspace）。调度器按
// (userID, workspaceID) 领取意图，因此这里必须写账户的真正 owner，而不是
// accountID —— 否则 ClaimActionIntents 永远按 accountID 过滤、消费不到。
func (f *Fetcher) recordActionIntent(ctx context.Context, em Email, acc Account, act rules.ActionResult) error {
	if f.store == nil {
		return fmt.Errorf("email: store not configured")
	}
	if em.WorkspaceID == "" || em.AccountID == "" {
		return fmt.Errorf("action intent: missing workspace/account on email %s", em.ID)
	}
	h := sha256.Sum256([]byte(em.ID + "|" + string(act.Action) + "|" + act.Folder))
	intent := &ActionIntent{
		EmailID:        em.ID,
		AccountID:      em.AccountID,
		WorkspaceID:    em.WorkspaceID,
		UserID:         acc.UserID,
		Action:         string(act.Action),
		Folder:         act.Folder,
		Reason:         act.Reason,
		IdempotencyKey: hex.EncodeToString(h[:])[:32],
		Status:         "pending",
	}
	if err := f.store.InsertActionIntent(ctx, intent); err != nil {
		return err
	}
	log.Printf("[email/fetcher] action intent queued action=%s email=%s folder=%q", act.Action, em.ID, act.Folder)
	return nil
}

// findBodySection 选取 UID Fetch 返回的第一个非空 BODY[TEXT] 片段。
func findBodySection(sections []imapclient.FetchBodySectionBuffer) ([]byte, error) {
	for _, bs := range sections {
		if len(bs.Bytes) > 0 {
			return bs.Bytes, nil
		}
	}
	return nil, fmt.Errorf("empty body section")
}

// Sync 同步一个账户的新邮件。返回 (新增邮件数, error)。
func (f *Fetcher) Sync(ctx context.Context, accountID string) (int, error) {
	if f.store == nil {
		return 0, fmt.Errorf("email: store not configured")
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return 0, nil
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return 0, fmt.Errorf("decrypt credential: %w", err)
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		return 0, fmt.Errorf("account has no usable credential")
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

	mbox, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("select INBOX: %w", err)
	}
	uidNext := mbox.UIDNext
	highestUID := imap.UID(acc.LastSyncedUID)

	criteria := &imap.SearchCriteria{}
	if acc.LastSyncedUID > 0 {
		var uidSet imap.UIDSet
		uidSet.AddRange(imap.UID(acc.LastSyncedUID+1), uidNext)
		criteria.UID = []imap.UIDSet{uidSet}
	}
	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		_ = f.store.UpdateSyncState(ctx, accountID, int64(uidNext), time.Now().Unix())
		return 0, nil
	}
	if len(uids) > 50 {
		uids = uids[len(uids)-50:]
	}

	var uidSet imap.UIDSet
	for _, u := range uids {
		uidSet.AddNum(u)
	}
	fetchOpts := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierText,
			Peek:      true,
			Partial:   &imap.SectionPartial{Offset: 0, Size: 5 * 1024},
		}},
	}
	// uidSet 必须按值传：imapwire.NumSetKind 对 imap.NumSet 做类型 switch，只
	// 认 imap.SeqSet / imap.UIDSet 值类型，*imap.UIDSet 会落到 default 分支直接
	// panic("imap: invalid NumSet type")。之前这里传 &uidSet，任何搜到新邮件的
	// Sync 都会 panic，整条抓取链路从未跑通过。
	messages, err := client.Fetch(uidSet, fetchOpts).Collect()
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}

	saved := 0
	rulesParsed, ruleErr := rules.ParseRules(acc.Rules)
	if ruleErr != nil {
		log.Printf("[email/fetcher] parse rules for %s failed: %v (skipping rules)", acc.EmailAddress, ruleErr)
	}
	for _, m := range messages {
		if m.Envelope == nil {
			continue
		}
		fromAddr, fromName := "", ""
		if len(m.Envelope.From) > 0 {
			fromAddr = m.Envelope.From[0].Addr()
			fromName = m.Envelope.From[0].Name
		}
		subject := m.Envelope.Subject
		uid := m.UID
		date := m.Envelope.Date.Unix()
		var snippet string
		for _, bs := range m.BodySection {
			snippet = strings.TrimSpace(string(bs.Bytes))
			break
		}
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		messageID := ""
		if m.Envelope.MessageID != "" {
			// go-imap returns the message ID already wrapped in < >. Strip them
			// so the UNIQUE(account_id, message_id) index is consistent.
			messageID = strings.TrimPrefix(strings.TrimSuffix(m.Envelope.MessageID, ">"), "<")
		}
		em := Email{
			ID:        fmt.Sprintf("em-%d-%s", uid, accountID),
			AccountID: accountID,
			// 抓取任务的作用域来自账户行自带的 workspace，不来自任何请求上下文。
			// 之前没带这个字段，InsertEmail 的 defaultWorkspace 兜底把所有邮件都
			// 写成 'default'。
			WorkspaceID: acc.WorkspaceID,
			MessageID:   messageID,
			UID:         int64(uid),
			FromAddress: fromAddr,
			FromName:    fromName,
			Subject:     subject,
			Snippet:     snippet,
			Date:        date,
		}
		// 评估账户规则。规则输出分两类落地：
		//   - 内联型（mark-important / label-category / archive）：直接写邮件字段，
		//     archive 置 category=archived + 已读，入库即生效，不需要后续消费。
		//   - 延迟型（route-folder / trigger-autoreply）：写入 email_action_intents，
		//     由 scheduler.intentLoop 消费（IMAP MOVE / SMTP 自动回复）。
			if ruleErr == nil && len(rulesParsed) > 0 {
				apply := rules.Evaluate(rulesParsed, rules.EmailInput{
					From:       fromAddr,
					Subject:    subject,
					Body:       snippet,
					Importance: em.Importance,
					Category:   em.Category,
					ReceivedAt: m.Envelope.Date,
				})
				if len(apply) > 0 {
					reasons := make([]string, 0, len(apply))
					imSet := false
					for _, act := range apply {
						switch act.Action {
						case rules.ActionMarkImportant:
							em.Importance = "high"
							imSet = true
						case rules.ActionLabelCategory:
							// 规则可在 action 里携带 category（如
							// {"name":"label-category","category":"work"}）。
							// 持久化到 emails.category，让前端可以立即
							// 在列表里看到分类结果，不必等待 kxmemory。
							if cat := strings.TrimSpace(act.Category); cat != "" {
								em.Category = cat
							}
						case rules.ActionArchive:
							// 归档直接在入库时落地：分类标 archived + 标已读，
							// 避免引入 IMAP MOVE 副作用与 intent 队列复杂度。
							// 行为可预测、可重放（重跑 sync 不会重复 MOVE）。
							em.Category = "archived"
							em.IsRead = true
						case rules.ActionRouteFolder, rules.ActionTriggerAutoReply:
							// 副作用型动作落 intent 表，由 scheduler 消费：
							//   route-folder → 标记 applied（真实 IMAP MOVE 延后）
							//   trigger-autoreply → SMTP 自动回复
							if err := f.recordActionIntent(ctx, em, *acc, act); err != nil {
								log.Printf("[email/fetcher] record action intent %s email=%s: %v", act.Action, em.ID, err)
							}
						}
						if act.Action != rules.ActionUnsupported {
							reasons = append(reasons, string(act.Action)+": "+act.Reason)
						}
					}
					if len(reasons) > 0 {
						em.ActionReason = strings.Join(reasons, "; ")
					}
					if imSet {
						log.Printf("[email/fetcher] uid=%d mark-important applied (account=%s)", uid, acc.ID)
					}
				}
			}
		if err := f.store.InsertEmail(ctx, em); err != nil {
			log.Printf("[email/fetcher] insert email uid=%d: %v", uid, err)
			continue
		}
		saved++
		if uid > highestUID {
			highestUID = uid
		}
	}
	if err := f.store.UpdateSyncState(ctx, accountID, int64(highestUID), time.Now().Unix()); err != nil {
		log.Printf("[email/fetcher] update sync state %s: %v", accountID, err)
	}
	return saved, nil
}
