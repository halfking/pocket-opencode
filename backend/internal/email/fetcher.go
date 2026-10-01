package email

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
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
	// BodyCache 为 nil 时不缓存 POP3 原文。见 body_cache.go：POP3 路径的 UID
	// 是位置序号，事后无法用它 IMAP FETCH 回原文，所以必须在同步时存。
	BodyCache BodyCache
	// syncHook 直接顶替整个 Sync。仅用于测试「各账户耗时」这类时序行为
	// （见 pipeline_concurrency_test.go）：真实 Sync 会连外部 IMAP，测不了
	// 「一个慢账户是否拖住其它账户」。生产路径 nil，永远走真实 IMAP 逻辑。
	syncHook func(ctx context.Context, accountID string) (int, error)
	// syncBudget / pop3Reserve 是单账户同步的时间预算，零值时用生产默认
	// （70s / 20s）。做成字段而不是 const，是为了**能测**：
	// 「IMAP 阶段到点是否真的会 Close 连接、POP3 回退是否真拿到正预算」
	// 这条契约只能在 Sync 的真实路径上验，而 50s 的生产值测试等不了 ——
	// 这与 imapDialWithIdle 拆出可注入超时是同一个理由。
	syncBudgetOverride  time.Duration
	pop3ReserveOverride time.Duration
	// inflight 记录正在同步的账户，防止同一账户被并发同步。
	//
	// 2026-10-01 实测：scheduler 的 pollLoop 每 60s 遍历一次，对
	// `now - LastSyncedAt >= interval` 的账户起一个 goroutine 调 Sync，
	// **没有互斥**。而 Sync 一旦卡住（LastSyncedAt 不更新），下一轮 tick
	// 会再起一个 —— 实测进程里同时攒到 7 条到同一台 IMAP 服务器的
	// Established 连接。pipeline 的第 1 步也会对同一账户再起一个。
	//
	// 重复同步不只是浪费连接：QQ 上 POP3 是**主路径**，重复同步等于把同一
	// 批邮件反复拉一遍，正是 §重复副本那 47 组脏数据的来源之一。
	inflight sync.Map // accountID -> struct{}
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

// imapIdleTimeout 单次 IMAP 读/写的**空闲**上限。
//
// 背景（2026-10-01 05:01~05:21 实测）：huangxutao@kxpms.cn 反复挂满 90s，
// 进程里同时攒到 **7 条**到 120.226.165.33:993 的 Established 连接。
// 原因是 `defer client.Close()` 只有在 Sync 返回时才执行，而 go-imap
// 不响应 context 取消，Sync 一旦卡在某次读上就永远不返回——于是：
// 上层 90s 放弃等待 → goroutine 继续挂着 → 连接不释放 → 下一轮又建一条。
// 累积之后新连接越来越慢，形成正反馈。
//
// 为什么之前没被 deadline 兜住：`net.Dialer.Timeout` 只管**建连**，
// 建好之后的读操作没有任何时间上限。而 `imapclient.Options` 根本没有
// ReadTimeout/WriteTimeout 字段（只有 TLSConfig / DebugWriter /
// UnilateralDataHandler / WordDecoder / Dialer）——所以只能自己 dial 拿到
// net.Conn 再挂 deadline。
//
// 60s 的取法：必须大于单次正常 IMAP 操作（大邮件 BODY[] 实测数秒），
// 又要小于单账户 90s 上限，这样「卡死」会先变成一条明确的超时错误，
// Sync 能正常返回、连接被关闭、上层拿到可诊断的 err 而不是干等 90s。
const imapIdleTimeout = 60 * time.Second

// imapHardTimeout 单条 IMAP 连接的**绝对**寿命上限，不看有没有活动。
//
// 它兜的是 imapIdleTimeout 兜不住的那一类：连接活着、期间还有数据往来，
// 但某条命令迟迟不返回。2026-10-01 实测 56551681@qq.com 的 imap login
// 整整挂了 100s，空闲 deadline 一次都没触发，最后仍以 i/o timeout 收场
// （Sync trace total 1m40.128s）。这类「活着但不干活」只有硬截止能治。
//
// 取 45s：实测正常单账户同步 1.0~1.4s，这里有 30 倍余量；又明显小于
// 单账户 90s 上界，给后面的步骤留足余量。
const imapHardTimeout = 45 * time.Second

// deadlineConn 给 IMAP 连接套两道保险：滚动空闲 deadline + 绝对硬截止。
//
// **第一道（滚动空闲）** 只在连接上确实有数据流动时才续期，静默则到期报错。
//
// 这里踩过一个很典型的坑，值得记下来：**用定时器无条件续期是错的**。
// 第一版写成 `ticker 每 idle/3 就 SetDeadline(now+idle)`，看起来正好实现
// 「活跃则续期」，实际效果是**静默连接也被无限续命**——服务端一个字节都不发，
// deadline 被一次次推后，Read 永远不返回。实测：SetDeadline 明确返回
// nil（成功），而 Read 仍挂满 60s 整。所以续期条件必须是「距上次**活动**
// 不到 idle/3」，而不是「定时器响了」。
//
// **第二道（绝对硬截止）** 是第一道兜不住的那一类：连接**活着、有活动，
// 但某条命令迟迟不返回**。
// 2026-10-01 实测 56551681@qq.com：imap login 阶段整整挂了 100s，
// 空闲 deadline 一次都没触发（说明期间连接上有数据往来），最后仍以
// `i/o timeout` 收场。对这类「活着但不干活」的情况，只有硬截止能兜住。
type deadlineConn struct {
	net.Conn
	idle time.Duration
	hard time.Time // 绝对截止；过了就断开，不看有没有活动

	mu   sync.Mutex
	last time.Time // 最近一次成功读/写的时间
}

func (c *deadlineConn) touch() {
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.touch()
	}
	return n, err
}

// start 启动看门狗。连接关闭后 SetDeadline 会持续返回错误，goroutine 随即
// 自行退出，不会因为 Sync 卡死而永久泄漏。
func (c *deadlineConn) start() {
	c.touch()
	_ = c.Conn.SetDeadline(c.nextIdle())
	go func() {
		iv := c.idle / 3
		if iv <= 0 {
			return
		}
		t := time.NewTicker(iv)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			c.mu.Lock()
			since := now.Sub(c.last)
			c.mu.Unlock()
			switch {
			case !c.hard.IsZero() && now.After(c.hard):
				// 过了绝对截止：不管有没有活动，一律断开。
				_ = c.Conn.SetDeadline(now.Add(-time.Second))
			case since >= c.idle:
				// 静默已超过上限：把 deadline 钉到过去，强制下一次读写立刻报错。
				_ = c.Conn.SetDeadline(now.Add(-time.Second))
			default:
				// 最近有活动：续满——但**必须夹在绝对截止之内**。
				_ = c.Conn.SetDeadline(c.nextIdle())
			}
		}
	}()
}

// nextIdle 返回「滚动空闲续期」应设的 socket deadline，且**永远不超过硬截止**。
//
// ## 为什么必须有这个夹取
//
// 原来续期直接写 `SetDeadline(now.Add(c.idle))`。生产值 idle=60s / hard=45s：
// 第一次 tick 在 T+20s（iv = idle/3），此时 now 还**没超过** hard=45s，于是走
// default 分支把 deadline 设成 **T+80s** —— 一个比宣称的绝对上界还晚 35s 的
// 时间点。此后 socket 上的 deadline 已经越界，能不能被拉回来完全取决于
// T+40 / T+60 两次 tick 是否准时跑（GC 停顿、调度饥饿、进程繁忙都会推迟）。
//
// 也就是说：`imapHardTimeout` 注释里写的「绝对寿命上限」在代码上**从来没有
// 成立过**，真正生效的是「hard + idle/3」甚至更久。线上实测（2026-10-01
// 21:38:02，huangxutao@kxpms.cn）login 阶段整整 80.001s 才返回 i/o timeout，
// 正是 idle/3=20s 的两次续期把 deadline 推到 80s 的结果 —— 而这条 80s 已经
// 吃掉了单账户 70s 总预算，POP3 回退只拿到 `budget -10s left` 直接放弃。
//
// 夹取之后：任何一次续期的 deadline 都不超过 hard，socket 上的绝对上界
// 就是 hard 本身，看门狗延迟也不会把它推得更远。
//
// **残余缺口（实测到，未解释）**：合成实验里 hard=3s 时读在 4.001s 才返回
// 而不是 3.0s，说明除 socket deadline 外还有一条「tick 到点后才把 deadline
// 钉到过去」的路径在收尾，即上界是 `hard + 一个 tick 量级`（生产约 45+20=65s），
// 不是精确的 hard。原因没有去查（要进 go-imap 读 goroutine 内部）。
//
// 这条缺口目前**不影响** Sync 的总时长上界：Sync 里另有一个
// `time.AfterFunc(imapStageBudget, client.Close)` 兜底（50s，立即生效），
// 与本机制互补。也就是说不必依赖这里的精确性，但读代码时别把 imapHardTimeout
// 当成精确值。
func (c *deadlineConn) nextIdle() time.Time {
	d := time.Now().Add(c.idle)
	if !c.hard.IsZero() && d.After(c.hard) {
		return c.hard
	}
	return d
}

func imapDialWithTimeout(addr string, secure bool, timeout time.Duration, tlsCfg *tls.Config) (*imapclient.Client, error) {
	return imapDialWithIdle(addr, secure, timeout, imapIdleTimeout, imapHardTimeout, tlsCfg)
}

// imapDialWithIdle 是可注入超时参数的版本，生产走 imapDialWithTimeout。
// 单独拆出来是为了让测试能用秒级值在真实 TCP 上复现「服务端接受连接后
// 不响应」——生产值是几十秒，测试等不了，也没法在测试里证明这两道保险
// 真的会让挂住的读返回。
func imapDialWithIdle(addr string, secure bool, timeout, idle, hard time.Duration, tlsCfg *tls.Config) (*imapclient.Client, error) {
	netDialer := net.Dialer{Timeout: timeout}
	conn, err := netDialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	if secure {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		cfg := &tls.Config{ServerName: host}
		if tlsCfg != nil {
			cfg = tlsCfg.Clone()
			if cfg.ServerName == "" {
				cfg.ServerName = host
			}
		}
		tconn := tls.Client(conn, cfg)
		// 握手也要有上限：TCP 连上了但 TLS 协商卡住同样会泄漏一条连接。
		_ = tconn.SetDeadline(time.Now().Add(timeout))
		if err := tconn.Handshake(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		// 握手成功后交给 deadlineConn 接管滚动 deadline。
		_ = tconn.SetDeadline(time.Time{})
		conn = tconn
	}
	// 必须调 start()：只构造 deadlineConn 不会设任何 deadline（net.Conn 的
	// 零值 deadline = 永不超时），滚动刷新也就不会跑。
	dc := &deadlineConn{Conn: conn, idle: idle}
	if hard > 0 {
		dc.hard = time.Now().Add(hard)
	}
	dc.start()
	return imapclient.New(dc, &imapclient.Options{}), nil
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
	sendClientID(client, acc.EmailAddress)
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

// fetchSnippetOnConnected 在已建立的 IMAP 连接上按 UID 单封拉 BODY[TEXT]
// （Peek，不带 partial），返回截断后的 snippet；任何失败都返回空串——调用方
// （Sync 循环）只是补齐摘要，不应因摘要失败丢邮件。
func (f *Fetcher) fetchSnippetOnConnected(client *imapclient.Client, uid imap.UID) string {
	uidSet := imap.UIDSet{}
	uidSet.AddNum(uid)
	messages, err := client.Fetch(uidSet, &imap.FetchOptions{
		UID: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierText,
			Peek:      true,
		}},
	}).Collect()
	if err != nil {
		log.Printf("[email/fetcher] snippet fetch uid=%d: %v", uid, err)
		return ""
	}
	if len(messages) == 0 {
		return ""
	}
	for _, bs := range messages[0].BodySection {
		if len(bs.Bytes) > 0 {
			// 2026-10-01 真机审计：原来直接取原始字节，用户会看到整段 MIME
			//（--part_xxx / Content-Type: …）或字面 HTML 标签。改走 DeriveSnippet。
			return DeriveSnippet(bs.Bytes, 500)
		}
	}
	return ""
}

// Sync 同步一个账户的新邮件。返回 (新增邮件数, error)。
//
// 协议选择：
//   - IMAP 优先（标准 IMAP4rev1，envelope + UIDSearch + BODY[]）；
//   - IMAP 失败（典型：163 `NO SELECT Unsafe Login`、企业邮 OAuth 不可用）
//     自动降级到 POP3 RETR（仅在 store 探测到 Provider 的 POP3Host 时）。
// sendClientID 发送 RFC 2971 ID 命令声明客户端身份。
//
// 网易 Coremail（163/126）在 SELECT 前要求 ID，否则返回
// `NO SELECT Unsafe Login. Please contact kefu@188.com`——这不是 IP 风控
// 一条原因，缺客户端标识同样触发。ID 是扩展命令，服务器 CAPABILITY 里
// 没有 ID 时会返回 BAD（如 Greenmail），因此失败仅记日志、不阻断同步。
func sendClientID(client *imapclient.Client, emailAddress string) {
	_, err := client.ID(&imap.IDData{
		Name:    "pocketd",
		Version: "1.0.0",
		Vendor:  "openpocket",
		Address: emailAddress,
	}).Wait()
	if err != nil {
		log.Printf("[email/fetcher] imap ID %s not accepted (continuing): %v", emailAddress, err)
	}
}

// ErrSyncInFlight 表示该账户已有一轮同步在跑，本次调用被跳过。
//
// 单独定义而不是复用普通 error，是为了让调用方能区分「真的同步失败」
// （要报警/重试）与「上一轮还没跑完」（正常现象，不该报警）。定时链路
// 每个 tick 都会撞上后者，报成失败只会淹没真正的问题。
var ErrSyncInFlight = errors.New("email: sync already in flight for this account")

// syncStepWarn 单个 Sync 阶段超过这个耗时就告警。
const syncStepWarn = time.Second

// syncTrace 给 Sync 的各阶段打点。
//
// 为什么需要它：2026-10-01 排查「某个账户卡满 90s」时，Sync 里**一个日志
// 都没有**，只能看到 pipeline 外层的 `TIMED OUT after 1m30s`——完全不知道卡在
// dial、login、SELECT、FETCH 还是落库。只能另写诊断程序在包外复刻一遍整条
// 路径来二分定位（见 diag_kxpms_test.go）。有了这个打点，下次直接看日志就知道。
//
// 只打**慢**的阶段：正常同步每步都是几十毫秒，逐条打会淹掉日志。
type syncTrace struct {
	email string
	start time.Time
	last  time.Time
}

func newSyncTrace(email string) *syncTrace {
	now := time.Now()
	return &syncTrace{email: email, start: now, last: now}
}

// step 记录进入某个阶段。若上一个阶段耗时超阈值，说明卡点就在它后面那一步。
func (t *syncTrace) step(name string) {
	now := time.Now()
	if d := now.Sub(t.last); d >= syncStepWarn {
		log.Printf("[email/fetcher] %s SLOW step before %-16s took %s (total %s)",
			t.email, name, d.Round(time.Millisecond), now.Sub(t.start).Round(time.Millisecond))
	}
	t.last = now
}

// done 收尾，把总耗时也记一笔（即使全程不慢，便于和 pipeline 的外层耗时对照）。
func (t *syncTrace) done() {
	log.Printf("[email/fetcher] %s sync trace total %s", t.email, time.Since(t.start).Round(time.Millisecond))
}

func (f *Fetcher) Sync(ctx context.Context, accountID string) (int, error) {
	if f.syncHook != nil {
		return f.syncHook(ctx, accountID)
	}
	if _, loaded := f.inflight.LoadOrStore(accountID, struct{}{}); loaded {
		return 0, fmt.Errorf("%w: %s", ErrSyncInFlight, accountID)
	}
	defer f.inflight.Delete(accountID)
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
	tr := newSyncTrace(acc.EmailAddress)
	defer tr.done()
	// syncBudget 是单个账户的**总**墙钟预算，IMAP 与 POP3 降级共用。
	//
	// 取 70s = IMAP 硬截止 45s + POP3 最多 25s。必须明显小于 pipeline 的
	// 90s 上界（DefaultAccountSyncTimeout），留 20s 给后面的步骤（落库、
	// 发票建档）——之前取 80s 时实测仍然整轮 90619ms 超时，因为 80+ 收尾
	// 已经把 90s 吃满了。
	syncBudget := f.syncBudgetOverride
	if syncBudget <= 0 {
		syncBudget = 70 * time.Second
	}
	pop3Reserve := f.pop3ReserveOverride
	if pop3Reserve <= 0 {
		pop3Reserve = 20 * time.Second
	}
	// pop3Reserve 是**无条件**留给 POP3 降级的时间。
	//
	// 之前这个数字只体现在注释里（「70s = IMAP 硬截止 45s + POP3 最多 25s」），
	// 代码上**没有任何地方保证它**：IMAP 路径从头到尾不检查 syncBudget，
	// `remaining()` 只被传给 POP3 分支。于是 IMAP 一旦跑超，POP3 拿到的就是
	// 负数，syncPOP3Fallback 直接返回
	// `imap failed and no time left for POP3 fallback` —— 降级路径恰好在
	// 最需要它的时候没有预算。线上实测（2026-10-01 21:38:02）：
	// `trying POP3 fallback (budget -10s left)`。
	//
	// 修法不是「给 IMAP 加检查」——go-imap 不响应 ctx，唯一的立即手段是
	// Close。所以用 time.AfterFunc 在 imapStageBudget 到点时直接
	// client.Close()，**不依赖 deadline 机制**（deadline 实测要等看门狗 tick，
	// 见 §7bi；上界是 hard + 一个 tick 量级，不是精确值）。
	imapStageBudget := syncBudget - pop3Reserve
	if imapStageBudget <= 0 {
		// 预算配错（syncBudget <= pop3Reserve）时给 IMAP 留一半，而不是负数。
		// 负的 AfterFunc 周期会让 time.AfterFunc 立刻触发，把 IMAP 阶段秒断，
		// 之后每一轮都直接走降级 —— 那比超预算更难排查。
		imapStageBudget = syncBudget / 2
	}
	deadline := time.Now().Add(syncBudget)
	// 降级时能用的时间 = 总预算减去 IMAP 已经花掉的。
	remaining := func() time.Duration { return time.Until(deadline) }

	tr.step("dial")
	client, err := f.dial(addr)
	if err != nil {
		log.Printf("[email/fetcher] imap dial %s failed: %v — trying POP3 fallback (budget %s left)", addr, err, remaining().Round(time.Second))
		return f.syncPOP3Fallback(ctx, acc, cred, remaining())
	}
	defer client.Close()

	// IMAP 阶段的无条件上界。到点直接 Close：它是立即的，而 deadline 兜底
	// 实测要等看门狗 tick（上界 hard + 一个 tick，见 §7bi），不足以保证
	// remaining() 一定为正。加上这一条之后，POP3 降级**结构上**必然拿得到
	// pop3Reserve，不会再出现「budget -10s left」。
	//
	// 取 imapStageBudget(50s) 而不是 imapHardTimeout(45s)：让 deadline 机制
	// 先按它自己的节奏收尾，Close 只是兜底，正常同步（实测 0.25~1.4s）远够不着。
	stopIMAPStage := time.AfterFunc(imapStageBudget, func() {
		log.Printf("[email/fetcher] imap stage budget %s exhausted for %s — closing connection to leave %s for POP3 fallback",
			imapStageBudget, acc.EmailAddress, pop3Reserve)
		_ = client.Close()
	})
	defer stopIMAPStage.Stop()

	tr.step("login")
	if err := f.login(client, *acc, cred); err != nil {
		log.Printf("[email/fetcher] imap login %s failed: %v — trying POP3 fallback (budget %s left)", acc.EmailAddress, err, remaining().Round(time.Second))
		return f.syncPOP3Fallback(ctx, acc, cred, remaining())
	}
	tr.step("ID")
	sendClientID(client, acc.EmailAddress)
	tr.step("SELECT")

	mbox, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		// 163 等服务在 ID 未发/陌生 IP 时 `NO SELECT Unsafe Login`（ID 已在
		// sendClientID 发过，仍失败多为 IP 风控），降级 POP3 RETR。
		log.Printf("[email/fetcher] imap select %s failed: %v — trying POP3 fallback (budget %s left)", acc.EmailAddress, err, remaining().Round(time.Second))
		return f.syncPOP3Fallback(ctx, acc, cred, remaining())
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
	tr.step("UID SEARCH")
	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		// 无新邮件时不推进 LastSyncedUID：语义是「已拉到的最大 UID」。若
		// 写成 uidNext（下一封的预分配 UID），下轮从 uidNext+1 起搜会永久
		// 跳过恰好分到 uidNext 的那封新邮件（真实踩中：QQ 首轮同步后投递
		// 的发票邮件再没被拉到）。
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
		Envelope:     true,
		UID:          true,
		InternalDate: true,
		// 部分 IMAP server（如 Greenmail）对 BODY[TEXT]<0.1024> 的响应缺
		// SP 分隔符导致 imapwire 解析失败，因此仅 envelope + UID 起步，
		// 完整正文由后续 harvester 通过 FetchMessageRaw 按需单封拉取。
	}
	// uidSet 必须按值传：imapwire.NumSetKind 对 imap.NumSet 做类型 switch，只
	// 认 imap.SeqSet / imap.UIDSet 值类型，*imap.UIDSet 会落到 default 分支直接
	// panic("imap: invalid NumSet type")。之前这里传 &uidSet，任何搜到新邮件的
	// Sync 都会 panic，整条抓取链路从未跑通过。
	tr.step("FETCH envelope")
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
		// IMAP ENVELOPE 的 Subject/个人名是 RFC 2047 编码字，go-imap 不解码。
		// 不解的话列表里所有中文主题都是 `=?GBK?B?...?=`，而且发票关键词匹配
		// 全部落空（主题里明明写着「发票」）。实测企业微信邮箱 5/5 封中招。
		fromName = decodeMIMEWord(fromName)
		subject := decodeMIMEWord(m.Envelope.Subject)
		uid := m.UID
		// 缺 Date 头的邮件（少数自动化系统）envelope Date 是 Go 零值，直接
		// .Unix() 会落成 -62135596800 这类负值，该邮件从此进不了任何 date
		// 时间窗口扫描（发票提取/垃圾清理/提醒），这里按 INTERNALDATE 兜底。
		date := m.Envelope.Date.Unix()
		if m.Envelope.Date.IsZero() {
			if !m.InternalDate.IsZero() {
				date = m.InternalDate.Unix()
			} else {
				date = time.Now().Unix()
			}
		}
		var snippet string
		for _, bs := range m.BodySection {
			// 2026-10-01 真机审计：原来是把 bs.Bytes 直接转字符串再按字节截前 500，
			// 三个问题叠在一起 ——
			//  1. BODY[TEXT]<partial> 时 bs.Bytes 是 MIME 头本身，用户在
			//     /notifications 上直接看到「--part_xxx / Content-Type: …」；
			//  2. 只有 HTML 正文时标签原样透出（字面的 <br/> 与 <a href=…>）；
			//  3. 按**字节**切，中文邮件会在第 500 字节处劈开半个字符产生乱码。
			snippet = DeriveSnippet(bs.Bytes, 500)
			break
		}
		if snippet == "" {
			// 批量 fetch 只取 envelope（Greenmail 对 BODY[TEXT]<partial> 响应
			// 缺 SP 分隔符），snippet 在此复用同一连接按需单封补拉；失败仅
			// 留空，不阻塞落库。
			// 这里是 Sync 里最可疑的一段：同一连接上**逐封串行**发部分取回，
			// 没有并发也没有单独预算。企业微信（imap.exmail.qq.com）实测在这
			// 一步会挂到分钟级，而外层只能看到 90s 上界。单独打点。
			tr.step(fmt.Sprintf("snippet uid=%d", uid))
			snippet = f.fetchSnippetOnConnected(client, uid)
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
		tr.step(fmt.Sprintf("InsertEmail uid=%d", uid))
		if err := f.store.InsertEmail(ctx, em); err != nil {
			log.Printf("[email/fetcher] insert email uid=%d: %v", uid, err)
			continue
		}
		saved++
		if uid > highestUID {
			highestUID = uid
		}
	}
	tr.step("UpdateSyncState")
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
//
// budget 是这次降级**还能花的时间**。它不是可有可无的参数：2026-10-01 实测
// kxpms 的 Sync 总耗时 1m40.137s = IMAP login 挂满 60s（idle deadline 生效）
// + POP3 又拿到了完整的一份 120s。IMAP 慢往往说明同一个服务商整体慢，
// POP3 不会凭空变快，所以降级必须**共用剩余预算**而不是重新拿一份——
// 否则单账户必然撞破 pipeline 的 90s 上界。
func (f *Fetcher) syncPOP3Fallback(ctx context.Context, acc *Account, cred string, budget time.Duration) (int, error) {
	if budget <= 0 {
		return 0, fmt.Errorf("imap failed and no time left for POP3 fallback (%s)", acc.EmailAddress)
	}
	host, port, tls := pop3EndpointFor(acc)
	if host == "" {
		return 0, fmt.Errorf("no POP3 endpoint for %s (imaphost=%s)", acc.EmailAddress, acc.IMAPHost)
	}
	seen, err := f.store.ListPOP3SeenUIDLs(ctx, acc.ID)
	if err != nil {
		return 0, fmt.Errorf("list pop3 seen: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	uidls, payloads, err := FetchPOP3MailboxWithIdle(ctx, addr, tls, acc.EmailAddress, cred, seen, budget)
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
			// 优先用真实 Message-ID 头，而不是合成的 "pop3-<uidl>"。
			//
			// 为什么：同一封邮件走 IMAP 落一条（真实 message_id）、走 POP3 降级
			// 再落一条（合成 message_id），两者不等，UNIQUE(account_id,
			// message_id) 拦不住 → 邮件列表出现重复、发票/垃圾扫描各处理两遍。
			// 实测 QQ 信箱 284/444 封走 POP3 路径，47 组是重复副本。
			// 取不到真实头时才回退到 UIDL（UIDL 本身跨轮稳定，仍能保证幂等）。
			if parsed.MessageID != "" {
				em.MessageID = parsed.MessageID
			}
			if addr := extractFirstEmailAddress(parsed.From); addr != "" {
				em.FromAddress = addr
			}
			em.Subject = parsed.Subject
			em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)
			if em.Snippet == "" {
				// 2026-10-01 真机审计：原来直接塞 HTMLBody，字面的 <br/> 与
				// <a href=…> 会原样透到通知列表。这里走 DeriveSnippet 剥标签。
				em.Snippet = DeriveSnippet([]byte(parsed.HTMLBody), 500)
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
		// POP3 同步是**唯一**能拿到这封邮件完整原文的机会：它的 UID 是位置
		// 序号而不是 IMAP UID，事后再想取只能靠这个缓存（见 body_cache.go）。
		// 缓存失败不阻断同步——同步本身已经成功，只是发票以后采不到。
		if f.BodyCache != nil {
			if rel, cerr := f.BodyCache.Put(em.ID, em.UID, raw); cerr != nil {
				log.Printf("[email/fetcher] pop3 body cache put uidl=%s: %v", uidls[i], cerr)
			} else if merr := f.store.MarkEmailBodyCached(ctx, em.ID, rel, len(raw)); merr != nil {
				log.Printf("[email/fetcher] pop3 mark body cached uidl=%s: %v", uidls[i], merr)
			}
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
// RefetchPOP3RawByIndex 用账户凭据按 POP3 位置序号补取单封邮件原文。
//
// 这是 POP3 来源发票的死结自愈（2026-10-01 实测）：POP3 落库的邮件在 IMAP
// 侧未必存在（实测 QQ 账户 IMAP 侧 50 封里零封发票，两张真实 QQ Wallet 发票
// 只在 POP3 路径的 279 封里），原文缓存又从未落盘。此时既不能 IMAP FETCH、
// 又无缓存可读。位置序号在 POP3 侧是**有效**的（它就是 POP3 自己的编号），
// 所以回到 POP3 RETR 是第三条安全路径——不同于拿它去 IMAP 盲 FETCH。
//
// 安全性依赖调用方：拿到 raw 后应与库记录比对（Message-ID/主题/发件人）确认
// 是同一封，位置序号若因服务器重排漂移就会取到别的邮件。本方法不内置该比对，
// 因为 raw 解析属于 mime 层。
func (f *Fetcher) RefetchPOP3RawByIndex(ctx context.Context, accountID string, index int) ([]byte, error) {
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
	host, port, tlsFlag := pop3EndpointFor(acc)
	if host == "" {
		return nil, fmt.Errorf("no POP3 endpoint for %s (imaphost=%s)", acc.EmailAddress, acc.IMAPHost)
	}
	// 位置序号在 POP3 侧有效，不需要 UIDL 交叉校验（UIDL 被 sanitize 进 ID，
	// 不可逆）；同一封的确认交给调用方比对原文。
	return FetchPOP3MessageByIndex(ctx, fmt.Sprintf("%s:%d", host, port), tlsFlag,
		acc.EmailAddress, cred, index, "", 0)
}

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
