package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// OAuthBroadcaster is the minimal WS surface the scheduler needs to push
// revocation events. The websocket.Hub implements it; tests can swap a fake.
type OAuthBroadcaster interface {
	BroadcastToUser(userID, msgType string, payload interface{})
}

// OAuthRevokedEvent 包含前端展示给用户所需的最小信息。
type OAuthRevokedEvent struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	UserID       string `json:"userId,omitempty"`
	Reason       string `json:"reason"`           // provider error code (invalid_grant / ...)
	ProviderID   string `json:"providerId"`        // google / outlook
	At           int64  `json:"at"`               // unix seconds
}

// Scheduler 定期触发 IMAP 同步和每日邮件总结。
type Scheduler struct {
	store    *Store
	fetcher  *Fetcher
	crypto   *Crypto
	kxmem    kxmemory.Client // optional；nil 表示禁用 DailySummary AI 生成
	refresher OAuthRefresher // optional；nil 跳过自动 refresh
	providers map[string]OAuthProviderConfig // providerID -> credentials/tokenURL
	broadcaster OAuthBroadcaster // optional；nil 跳过 WS 推送（保留 log）
	stop     chan struct{}
	enabled  bool
	// tzOffsetSec 用户时区偏移（秒），用于按"日"聚合邮件。
	// 用 atomic.Int64 防止 cmd/pocketd 在 Start 之后修改时与 goroutine 数据竞争。
	tzOffsetSec atomic.Int64

	lastTick atomic.Int64
	nextTick atomic.Int64
}

// OAuthProviderConfig describes how to refresh tokens for a given provider.
// ProviderID matches provider.ID from providers.go (e.g. "gmail", "outlook").
type OAuthProviderConfig struct {
	ProviderID  string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

// 默认时区：UTC+8（中国大陆）。
// 历史注释声称默认 28800 但实际代码默认 0，导致 DailySummary 的"日"
// 与用户预期不一致。修复：默认即 UTC+8，cmd/pocketd 可以通过
// SetTimezoneOffset 覆盖（已通过 POCKET_TIMEZONE_OFFSET_SEC 配置）。
const defaultTimezoneOffsetSec = 28800

// NewScheduler 构造 Scheduler。kxmem 传 nil 时 DailySummary 跳过 AI 调用
// （保留原 log-only 行为），方便在 kxmemory 未部署的环境下继续运行。
//
// 默认时区为 UTC+8（中国大陆）；可通过 SetTimezoneOffset 或
// POCKET_TIMEZONE_OFFSET_SEC 环境变量覆盖。
func NewScheduler(store *Store, fetcher *Fetcher, enabled bool) *Scheduler {
	s := &Scheduler{
		store:     store,
		fetcher:   fetcher,
		crypto:    fetcher.crypto,
		providers: make(map[string]OAuthProviderConfig),
		stop:      make(chan struct{}),
		enabled:   enabled,
	}
	s.tzOffsetSec.Store(int64(defaultTimezoneOffsetSec))
	return s
}

// SetKxmemory 注入 kxmemory 客户端；nil = 禁用 DailySummary AI。
func (s *Scheduler) SetKxmemory(c kxmemory.Client) {
	s.kxmem = c
}

// SetOAuthRefresher 注入 OAuth refresh 客户端及 provider credentials。传
// nil refresher 会禁用自动 refresh（OAuth 用户需要重新走 /start 才能登录）。
func (s *Scheduler) SetOAuthRefresher(r OAuthRefresher, providers []OAuthProviderConfig) {
	s.refresher = r
	for _, p := range providers {
		if p.ProviderID == "" {
			continue
		}
		s.providers[p.ProviderID] = p
	}
}

// SetBroadcaster 注入 WS hub（websocket.Hub 满足 OAuthBroadcaster 接口）。
// nil 时 refresh 失败只写日志，不广播 revocation 事件。
func (s *Scheduler) SetBroadcaster(b OAuthBroadcaster) {
	s.broadcaster = b
}

// SetTimezoneOffset 设置用户时区偏移（秒），用于 DailySummary 的"日"边界。
// 中国大陆默认 28800（UTC+8）。
func (s *Scheduler) SetTimezoneOffset(sec int) {
	s.tzOffsetSec.Store(int64(sec))
}

// timezoneOffset 返回当前时区偏移（秒）。atomic load 安全。
func (s *Scheduler) timezoneOffset() int {
	return int(s.tzOffsetSec.Load())
}

// Start 启动调度循环。
func (s *Scheduler) Start(ctx context.Context) {
	if !s.enabled {
		log.Printf("[email/scheduler] disabled via cfg.EmailFetchEnabled=false")
		return
	}
	go s.pollLoop(ctx)
	go s.dailySummaryLoop(ctx)
	if s.refresher != nil {
		go s.refreshLoop(ctx)
	}
}

// Stop 停止调度器。
func (s *Scheduler) Stop() { close(s.stop) }

// LastTickUnix 返回最后一次 tick 的 Unix 时间戳。
func (s *Scheduler) LastTickUnix() int64 {
	return s.lastTick.Load()
}

// NextTickUnix 返回下一次 tick 的预估时间戳。
func (s *Scheduler) NextTickUnix() int64 {
	if v := s.nextTick.Load(); v > 0 {
		return v
	}
	if l := s.lastTick.Load(); l > 0 {
		return l + 60
	}
	return 0
}

func (s *Scheduler) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			s.lastTick.Store(t.Unix())
			s.nextTick.Store(t.Add(60 * time.Second).Unix())
			s.tick(ctx)
		}
	}
}

// refreshLoop 每 5 分钟遍历即将过期 / 已过期的 OAuth tokens，调用
// provider token endpoint 刷新并落库；30s 单次超时，避免阻塞主调度。
// leewaySec=300（5 min）保证 access token 在 IMAP 登录前总是有效。
func (s *Scheduler) refreshLoop(ctx context.Context) {
	const leewaySec = int64(300)
	const tickInterval = 5 * time.Minute
	const opTimeout = 30 * time.Second
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshOnce(ctx, leewaySec, opTimeout)
		}
	}
}

func (s *Scheduler) refreshOnce(ctx context.Context, leewaySec int64, opTimeout time.Duration) {
	if s.refresher == nil || s.crypto == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	tokens, err := s.store.ListExpiredOAuthTokens(callCtx, leewaySec)
	if err != nil {
		log.Printf("[email/scheduler] list expired oauth tokens: %v", err)
		return
	}
	if len(tokens) == 0 {
		return
	}
	for _, row := range tokens {
		acc, _, err := s.store.GetAccountByID(callCtx, row.AccountID)
		if err != nil || acc == nil {
			continue
		}
		providerID := acc.AuthType
		cfg, ok := s.providers[providerID]
		// authType=="oauth2" 不携带 provider 区分；改用 emailAddress domain
		// 反查 providers via acc.EmailAddress（简单粗暴但够用）。
		if !ok || cfg.ProviderID == "" {
			providerID = guessProviderFromEmail(acc.EmailAddress)
			cfg = s.providers[providerID]
		}
		if cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
			log.Printf("[email/scheduler] skip refresh for %s: provider not configured (set POCKET_EMAIL_{GOOGLE,MICROSOFT}_CLIENT_ID)", acc.EmailAddress)
			continue
		}
		_, err = RefreshAccessToken(callCtx, s.crypto, s.store, s.refresher, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, row.AccountID)
		if err == nil {
			log.Printf("[email/scheduler] refreshed oauth token for %s (account %s)", acc.EmailAddress, acc.ID)
			continue
		}
		if !IsPermanentRefreshError(err) {
			log.Printf("[email/scheduler] transient refresh failure for %s: %v (will retry)", acc.EmailAddress, err)
			continue
		}
		log.Printf("[email/scheduler] permanent refresh failure for %s: %v — revoking", acc.EmailAddress, err)
		if revokeErr := s.store.RevokeOAuthToken(callCtx, row.AccountID); revokeErr != nil {
			log.Printf("[email/scheduler] revoke oauth token for %s: %v", acc.EmailAddress, revokeErr)
			continue
		}
		s.broadcastRevoked(acc, providerID, err)
	}
}

// broadcastRevoked 在 token 永久失效后通过 WS 通知用户重新连接授权。
// broadcaster 为 nil 时仅日志，确保 scheduler 在不同部署形态下都不 panic。
func (s *Scheduler) broadcastRevoked(acc *Account, providerID string, refreshErr error) {
	if s.broadcaster == nil {
		log.Printf("[email/scheduler] oauth revoked for %s (account %s) but no broadcaster configured", acc.EmailAddress, acc.ID)
		return
	}
	reason := ""
	var re *RefreshError
	if errors.As(refreshErr, &re) {
		reason = re.Code
	}
	if reason == "" {
		reason = refreshErr.Error()
	}
	if providerID == "" {
		providerID = acc.AuthType
	}
	event := OAuthRevokedEvent{
		AccountID:    acc.ID,
		EmailAddress: acc.EmailAddress,
		WorkspaceID:  acc.WorkspaceID,
		UserID:       acc.UserID,
		Reason:       reason,
		ProviderID:   providerID,
		At:           time.Now().Unix(),
	}
	s.broadcaster.BroadcastToUser(acc.UserID, "email.oauth.revoked", event)
	log.Printf("[email/scheduler] broadcast email.oauth.revoked user=%s account=%s reason=%s", acc.UserID, acc.ID, reason)
}

// guessProviderFromEmail 根据邮箱域名反推 OAuth provider 名称，用于
// refreshLoop 无法直接拿到 providerID 的情况。简单规则：gmail.com / googlemail.com
// → google；outlook/hotmail/live → outlook；其余返回空字符串由调用方决定。
func guessProviderFromEmail(emailAddr string) string {
	at := strings.LastIndex(emailAddr, "@")
	if at < 0 {
		return ""
	}
	domain := strings.ToLower(emailAddr[at+1:])
	switch domain {
	case "gmail.com", "googlemail.com":
		return "google"
	case "outlook.com", "hotmail.com", "live.com", "msn.com":
		return "outlook"
	}
	return ""
}

func (s *Scheduler) tick(ctx context.Context) {
	accounts, err := s.store.ListEnabledAccounts(ctx)
	if err != nil {
		log.Printf("[email/scheduler] list accounts: %v", err)
		return
	}
	now := time.Now().Unix()
	for _, a := range accounts {
		interval := int64(a.SyncIntervalMin)
		if interval <= 0 {
			interval = 15
		}
		intervalSec := interval * 60
		if a.LastSyncedAt > 0 && now-a.LastSyncedAt < intervalSec {
			continue
		}
		accountID := a.ID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if _, err := s.fetcher.Sync(ctx, accountID); err != nil {
				log.Printf("[email/scheduler] sync %s failed: %v", accountID, err)
			}
		}()
	}
}

// dailySummaryLoop 每日 21:00 触发（本地时区）。
//
// 触发时为每个有启用账户的用户生成当日的 AI 总结。
func (s *Scheduler) dailySummaryLoop(ctx context.Context) {
	for {
		next := nextTime(21, 0, 0)
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.runDailySummary(ctx)
		}
	}
}

// runDailySummary 给当天每个用户生成 AI 邮件总结。
//
// 流程：
//  1. 取所有启用账户（隐含 userID）
//  2. 按用户聚合
//  3. 拉当天邮件 → 调 kxmemory.DailySummary → 写回 daily_summaries
//
// kxmem == nil 时整个步骤降级为 log-only（保留向后兼容）。
func (s *Scheduler) runDailySummary(ctx context.Context) {
	today := time.Now().Format("2006-01-02")
	log.Printf("[email/scheduler] daily summary trigger fired at %s (date=%s)", time.Now().Format(time.RFC3339), today)

	if s.kxmem == nil {
		log.Printf("[email/scheduler] kxmemory not configured; skipping AI summary generation")
		return
	}

	accounts, err := s.store.ListEnabledAccounts(ctx)
	if err != nil {
		log.Printf("[email/scheduler] list accounts for daily summary: %v", err)
		return
	}

	// 按 userID 聚合（同一用户可能有多个邮箱账户）
	userIDs := make(map[string]struct{})
	for _, a := range accounts {
		if a.Enabled && a.UserID != "" {
			userIDs[a.UserID] = struct{}{}
		}
	}

	if len(userIDs) == 0 {
		log.Printf("[email/scheduler] no enabled users; nothing to summarize")
		return
	}

	successes := 0
	failures := 0
	for uid := range userIDs {
		if err := s.summarizeUser(ctx, uid, today); err != nil {
			log.Printf("[email/scheduler] daily summary for user %s failed: %v", uid, err)
			failures++
			continue
		}
		successes++
	}
	log.Printf("[email/scheduler] daily summary done: %d success, %d failed", successes, failures)
}

// summarizeUser 给单个用户生成当天的 AI 邮件总结并持久化。
func (s *Scheduler) summarizeUser(ctx context.Context, userID, date string) error {
	emails, err := s.store.ListEmailsByDay(ctx, userID, date, s.timezoneOffset())
	if err != nil {
		return fmt.Errorf("list emails by day: %w", err)
	}
	if len(emails) == 0 {
		log.Printf("[email/scheduler] user %s: no emails on %s, skipping", userID, date)
		return nil
	}

	// 转换为 kxmemory 输入格式
	items := make([]kxmemory.EmailForClassification, 0, len(emails))
	for _, e := range emails {
		items = append(items, kxmemory.EmailForClassification{
			EmailID:     e.ID,
			Subject:     e.Subject,
			Snippet:     e.Snippet,
			FromAddress: e.FromAddress,
			FromName:    e.FromName,
		})
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := s.kxmem.DailySummary(callCtx, kxmemory.DailySummaryRequest{
		Date:   date,
		Emails: items,
	})
	if err != nil {
		return fmt.Errorf("kxmemory DailySummary: %w", err)
	}

	// 统计重要邮件数（来自本地缓存的 importance 字段）。
	// 注意：fresh-fetch 邮件可能还未异步分类（Importance==""），此时
	// ImportantCount 会偏低 — 这是已知限制，等异步分类链路稳定后修复。
	importantCount := 0
	for _, e := range emails {
		if e.Importance == "high" {
			importantCount++
		}
	}

	// A4: actionItems 用 json.Marshal 序列化（之前 fmt.Sprintf("%v", resp.Todos)
	// 会产生 Go-syntax 输出如 "[kxmemory.ExtractedTodo{...}]"，不可解析）。
	actionItemsJSON := ""
	if len(resp.Todos) > 0 {
		b, _ := json.Marshal(resp.Todos)
		actionItemsJSON = string(b)
	}

	// A6: 复用当天已有 summary 的 ID，避免 ON CONFLICT 触发 update 后旧
	// row 变孤儿（primary key 变更）。
	summaryID := ""
	if existing, _ := s.store.GetSummaryByDate(ctx, userID, date); existing != nil {
		summaryID = existing.ID
	} else {
		summaryID = randomID("summary")
	}

	sum := &DailySummary{
		ID:             summaryID,
		UserID:         userID,
		SummaryDate:    date,
		TotalCount:     len(emails),
		ImportantCount: importantCount,
		Content:        resp.Summary,
		ActionItems:    actionItemsJSON,
		CreatedAt:      time.Now().Unix(),
	}
	if err := s.store.UpsertSummary(ctx, sum); err != nil {
		return fmt.Errorf("upsert summary: %w", err)
	}
	log.Printf("[email/scheduler] user %s: summary for %s written (%d emails, %d important)",
		userID, date, len(emails), importantCount)
	return nil
}

func nextTime(hour, min, sec int) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
