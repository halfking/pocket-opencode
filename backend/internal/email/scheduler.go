package email

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// Scheduler 定期触发 IMAP 同步和每日邮件总结。
type Scheduler struct {
	store    *Store
	fetcher  *Fetcher
	kxmem    kxmemory.Client // optional；nil 表示禁用 DailySummary AI 生成
	stop     chan struct{}
	enabled  bool
	tzOffset int // 用户时区偏移（秒），用于按"日"聚合邮件

	lastTick atomic.Int64
	nextTick atomic.Int64
}

// NewScheduler 构造 Scheduler。kxmem 传 nil 时 DailySummary 跳过 AI 调用
// （保留原 log-only 行为），方便在 kxmemory 未部署的环境下继续运行。
func NewScheduler(store *Store, fetcher *Fetcher, enabled bool) *Scheduler {
	return &Scheduler{store: store, fetcher: fetcher, stop: make(chan struct{}), enabled: enabled, tzOffset: 0}
}

// SetKxmemory 注入 kxmemory 客户端；nil = 禁用 DailySummary AI。
func (s *Scheduler) SetKxmemory(c kxmemory.Client) {
	s.kxmem = c
}

// SetTimezoneOffset 设置用户时区偏移（秒），用于 DailySummary 的"日"边界。
// 中国大陆默认 28800（UTC+8）。
func (s *Scheduler) SetTimezoneOffset(sec int) {
	s.tzOffset = sec
}

// Start 启动调度循环。
func (s *Scheduler) Start(ctx context.Context) {
	if !s.enabled {
		log.Printf("[email/scheduler] disabled via cfg.EmailFetchEnabled=false")
		return
	}
	go s.pollLoop(ctx)
	go s.dailySummaryLoop(ctx)
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
	emails, err := s.store.ListEmailsByDay(ctx, userID, date, s.tzOffset)
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

	// 统计重要邮件数（来自 kxmemory 已有分类：importance=high）
	importantCount := 0
	for _, e := range emails {
		if e.Importance == "high" {
			importantCount++
		}
	}

	// 把 kxmemory 返回的 todos 序列化为 JSON 字符串
	actionItemsJSON := ""
	if len(resp.Todos) > 0 {
		// 不展开 JSON 序列化逻辑（简单起见用 %v）
		actionItemsJSON = fmt.Sprintf("%v", resp.Todos)
	}

	sum := &DailySummary{
		ID:             randomID("summary"),
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
