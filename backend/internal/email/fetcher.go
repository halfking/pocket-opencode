package email

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log"
	"net"
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
	// insecureSkipVerify 允许跳过 IMAPS 证书校验。仅用于自签测试服务器
	// （如 Greenmail）；生产应保持 false 让 Go 默认 CA 池验证，避免
	// 中间人攻击。设置入口在 NewFetcherWithOptions。
	insecureSkipVerify bool
	// useStartTLS 走 STARTTLS 升级路径（明文 IMAP 端口 143，加密协商）；
	// 一些自签测试 IMAP server 不支持 IMAPS（端口 993）但支持 143+STARTTLS，
	// 此选项打开后用 imapclient.DialStartTLS + InsecureSkipVerify。
	useStartTLS bool
}

// NewFetcherWithOptions 同 NewFetcher，但允许开启证书跳过（自签 IMAPS 用）。
func NewFetcherWithOptions(store *Store, crypto *Crypto, insecureSkipVerify, useStartTLS bool) *Fetcher {
	return &Fetcher{
		store:               store,
		crypto:              crypto,
		insecureSkipVerify:  insecureSkipVerify,
		useStartTLS:         useStartTLS,
	}
}

// NewFetcher 构造 Fetcher。
func NewFetcher(store *Store, crypto *Crypto) *Fetcher {
	return &Fetcher{store: store, crypto: crypto}
}

// isPlainIMAPPort 判定 "host:port" 形式是否使用明文 IMAP。
// 启发式：端口==143（标准）或者端口==1143（Greenmail 容器内部重映射到主机）。
// 其它端口默认走隐式 TLS。
func isPlainIMAPPort(addr string) bool {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	switch portStr {
	case "143", "1143":
		return true
	}
	return false
}

func (f *Fetcher) dial(addr string) (*imapclient.Client, error) {
	if f.dialTLS != nil {
		return f.dialTLS(addr, nil)
	}
	// 10 秒硬上限：Greenmail / 自建 quirk server 可能不发 CAPABILITY 而挂死，
	// 超时后我们走 POP3 fallback（Sync 在 login/select 阶段失败时已调）。
	const dialTimeout = 10 * time.Second
	if f.insecureSkipVerify {
		return imapDialWithTimeout(addr, true, dialTimeout, &tls.Config{InsecureSkipVerify: true})
	}
	if f.useStartTLS {
		return imapDialWithTimeout(addr, true, dialTimeout, &tls.Config{InsecureSkipVerify: true})
	}
	// 默认按端口自动选择协议：143 明文，993+ 隐式 TLS。
	if isPlainIMAPPort(addr) {
		return imapDialWithTimeout(addr, false, dialTimeout, nil)
	}
	return imapDialWithTimeout(addr, true, dialTimeout, nil)
}

func imapDialWithTimeout(addr string, secure bool, timeout time.Duration, tlsCfg *tls.Config) (*imapclient.Client, error) {
	netDialer := net.Dialer{Timeout: timeout}
	opts := &imapclient.Options{
		Dialer: &netDialer,
		// 给 read deadline 一个上限，防止 server 不规范致连接挂死。
	}
	if tlsCfg != nil {
		opts.TLSConfig = tlsCfg
	}
	if secure {
		return imapclient.DialTLS(addr, opts)
	}
	return imapclient.DialInsecure(addr, opts)
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
		// OAuth 失败时（如 token 失效/未授权）自动降级到 password auth，
		// 前提是 store 里同时存了 smtp_credential_encrypted（service 层做
		// password + oauth 双写时一起写入；老账户可能只写 oauth）。
		if client.Caps().Has(imap.AuthCap(sasl.OAuthBearer)) {
			saslClient := NewOAuthBearerClient(acc.EmailAddress, cred, acc.IMAPHost, acc.IMAPPort)
			if err := client.Authenticate(saslClient); err == nil {
				return nil
			} else {
				log.Printf("[email/fetcher] OAUTHBEARER for %s failed: %v; falling back to XOAUTH2", acc.EmailAddress, err)
			}
		}
		if err := client.Authenticate(NewXOAuth2Client(acc.EmailAddress, cred)); err == nil {
			return nil
		}
		// OAuth 都失败：尝试 password 回退（exmail 企业邮等支持 LOGIN）。
		// 我们用 SMTP 凭证列里存的值作为 password，因为 service 层
		// 账户创建时把 password 与 oauth_token 都加密进了同一表；
		// 找不到就显式报错。
		if fallback, ferr := f.loadAccountPasswordFallback(&acc); ferr == nil && fallback != "" {
			log.Printf("[email/fetcher] oauth failed for %s, using password fallback", acc.EmailAddress)
			return client.Login(acc.EmailAddress, fallback).Wait()
		}
		return fmt.Errorf("oauth failed and no password fallback for %s", acc.EmailAddress)
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
//
// 协议选择：
//   - IMAP 优先（标准 IMAP4rev1，envelope + UIDSearch + BODY[]）；
//   - IMAP 失败（典型：163 `NO SELECT Unsafe Login`、企业邮 OAuth 不可用）
//     自动降级到 POP3 RETR（仅在 store 探测到 Provider 的 POP3Host 时）。
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
		log.Printf("[email/fetcher] imap dial %s failed: %v — trying POP3 fallback", addr, err)
		return f.syncPOP3Fallback(ctx, acc, cred)
	}
	defer client.Close()

	if err := f.login(client, *acc, cred); err != nil {
		log.Printf("[email/fetcher] imap login %s failed: %v — trying POP3 fallback", acc.EmailAddress, err)
		return f.syncPOP3Fallback(ctx, acc, cred)
	}

	mbox, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		// 163 等服务对陌生 IP 经常 `NO SELECT Unsafe Login`，IMAP 链路
		// 在 SELECT 处死，降级 POP3 RETR（同样是明文/SSL，单一 RETR 抓原文）。
		log.Printf("[email/fetcher] imap select %s failed: %v — trying POP3 fallback", acc.EmailAddress, err)
		return f.syncPOP3Fallback(ctx, acc, cred)
	}
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
		// 部分 IMAP server（如 Greenmail）对 BODY[TEXT]<0.1024> 的响应缺
		// SP 分隔符导致 imapwire 解析失败，因此仅 envelope + UID 起步，
		// 完整正文由后续 harvester 通过 FetchMessageRaw 按需单封拉取。
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
		// 部分 IMAP server（Greenmail、自建测试）不返回 Message-ID，导致同
		// 账户多封邮件 messageID 都为空字符串，触发 UNIQUE(account_id,
		// message_id) 冲突被 ON CONFLICT DO NOTHING 静默跳过。补一个
		// uid 维度的合成键，确保每封邮件都能落库。
		if messageID == "" {
			messageID = fmt.Sprintf("uid-%d", uid)
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

// syncPOP3Fallback 是 IMAP 链路被服务端拒绝时的备用同步通道。
// 返回 (新邮件数, error)。本函数：
//   1. 解析账户的 email 域名（如 163 → 走 Provider.POP3Host）；
//   2. POP3 RETR 每封新邮件，转成 email.Email 入库；
//   3. 持久化 UIDL 已读集合（按 email_pop3_seen）保证幂等。
func (f *Fetcher) syncPOP3Fallback(ctx context.Context, acc *Account, cred string) (int, error) {
	host, port, tls := pop3EndpointFor(acc)
	if host == "" {
		return 0, fmt.Errorf("no POP3 endpoint for %s (imaphost=%s)", acc.EmailAddress, acc.IMAPHost)
	}
	seen, err := f.store.ListPOP3SeenUIDLs(ctx, acc.ID)
	if err != nil {
		return 0, fmt.Errorf("list pop3 seen: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	uidls, payloads, err := FetchPOP3Mailbox(ctx, addr, tls, acc.EmailAddress, cred, seen)
	if err != nil {
		return 0, fmt.Errorf("pop3 fetch: %w", err)
	}
	if len(uidls) == 0 {
		return 0, nil
	}
	saved := 0
	var nowUIDLSeen []string
	now := time.Now().Unix()
	for i, raw := range payloads {
		// POP3 RETR 已拿到完整 RFC 5322 原文，直接解析出真实发件人/主题/
		// 摘要，与 IMAP 路径落库字段保持一致。harvester 需要 PDF 原文时走
		// storePipeline 的 body 缓存（此处只落 envelope + 摘要）。
		em := Email{
			ID:          fmt.Sprintf("em-pop3-%s-%s", acc.ID, sanitizeUIDLForID(uidls[i])),
			AccountID:   acc.ID,
			WorkspaceID: acc.WorkspaceID,
			MessageID:   "pop3-" + sanitizeUIDLForID(uidls[i]),
			UID:         int64(i + 1),
			Date:        now,
		}
		if parsed, perr := ParseMIMEMessage(raw); perr == nil {
			em.FromAddress = parsed.From
			em.FromName = parsed.Subject // 无独立 FromName；主题优先展示
			if addr := extractFirstEmailAddress(parsed.From); addr != "" {
				em.FromAddress = addr
			}
			em.Subject = parsed.Subject
			em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)
			if em.Snippet == "" {
				em.Snippet = truncateStr(strings.TrimSpace(parsed.HTMLBody), 500)
			}
			if !parsed.Date.IsZero() {
				em.Date = parsed.Date.Unix()
			}
			em.HasAttachments = len(parsed.Attachments) > 0
		} else {
			// 解析失败仍落一条占位（保留 UIDL 幂等，避免每轮重拉同一封），
			// 但标记来源便于排查。
			em.FromAddress = acc.EmailAddress
			em.Subject = "[POP3 解析失败] uidl=" + uidls[i]
			em.Snippet = fmt.Sprintf("parse error: %v", perr)
			log.Printf("[email/fetcher] pop3 parse uidl=%s failed: %v", uidls[i], perr)
		}
		if err := f.store.InsertEmail(ctx, em); err != nil {
			log.Printf("[email/fetcher] pop3 insert email uidl=%s: %v", uidls[i], err)
			continue
		}
		nowUIDLSeen = append(nowUIDLSeen, uidls[i])
		saved++
	}
	if err := f.store.MarkPOP3UIDLSeen(ctx, acc.ID, nowUIDLSeen, now); err != nil {
		log.Printf("[email/fetcher] mark pop3 seen: %v", err)
	}
	if saved > 0 {
		log.Printf("[email/fetcher] pop3 fallback %s: %d new (uidls=%v)", acc.EmailAddress, saved, nowUIDLSeen)
	}
	return saved, nil
}

// pop3EndpointFor 从账户的 email 域名匹配已知 provider 的 POP3 配置。
// 未知域名返回空（不启用降级）。
func pop3EndpointFor(acc *Account) (host string, port int, tlsFlag bool) {	addr := strings.ToLower(acc.EmailAddress)
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return "", 0, false
	}
	domain := addr[at+1:]
	for _, p := range providers {
		if strings.Contains(domain, strings.SplitN(p.ID, ".", 2)[0]) && p.POP3Host != "" {
			return p.POP3Host, p.POP3Port, p.POP3TLS
		}
		// 163/qq 这种短名匹配
		if strings.HasSuffix(p.ID+".com", domain) || strings.HasSuffix(p.ID+".cn", domain) || p.ID == domain {
			if p.POP3Host != "" {
				return p.POP3Host, p.POP3Port, p.POP3TLS
			}
		}
	}
	// exmail 兜底（QQ 企业邮）
	if strings.Contains(domain, "exmail") || strings.HasSuffix(domain, "kxpms.cn") {
		return "pop.exmail.qq.com", 995, true
	}
	return "", 0, false
}

// loadAccountPasswordFallback 在 OAuth 失败时尝试用 IMAP/SMTP 凭证列里
// 存的明文密码（service 层账户创建时如果 password 与 oauth token 都提供，
// 会把 password 加密到 smtp_credential_encrypted 复用的字段）。
// 找不到返回空字符串（caller 据此放弃）。
func (f *Fetcher) loadAccountPasswordFallback(acc *Account) (string, error) {
	if f == nil || f.store == nil || f.crypto == nil {
		return "", fmt.Errorf("fetcher not configured")
	}
	enc, err := f.store.GetAccountPasswordFallback(context.Background(), acc.ID)
	if err != nil || enc == "" {
		return "", err
	}
	return f.crypto.DecryptString(enc)
}

// sanitizeUIDLForID 把 POP3 UIDL 清洗成可安全嵌入主键/Message-ID 的字符串
//（只保留字母数字与连字符，其余替换为连字符；超长截断）。
func sanitizeUIDLForID(uidl string) string {
	var b strings.Builder
	for _, r := range uidl {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "empty"
	}
	return s
}

// truncateStr 按字节截断（最长 max 字节）。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// extractFirstEmailAddress 从 "Name <a@b.c>" / "a@b.c" 形态中提取纯地址。
func extractFirstEmailAddress(s string) string {
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	if strings.Contains(s, "@") {
		return strings.TrimSpace(s)
	}
	return ""
}
