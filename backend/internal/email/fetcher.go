package email

import (
	"context"
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
		// 评估账户规则。规则输出会写入 Importance / Category / ActionReason；
		// archive / route-folder / trigger-autoreply 暂不支持，引擎已统一返回
		// ActionUnsupported，调用方安全跳过。
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
