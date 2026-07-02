package email

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// Scheduler 定期触发 IMAP 同步。
type Scheduler struct {
	store   *Store
	fetcher *Fetcher
	stop    chan struct{}
	enabled bool

	lastTick atomic.Int64
	nextTick atomic.Int64
}

// NewScheduler 构造 Scheduler。
func NewScheduler(store *Store, fetcher *Fetcher, enabled bool) *Scheduler {
	return &Scheduler{store: store, fetcher: fetcher, stop: make(chan struct{}), enabled: enabled}
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

func (s *Scheduler) runDailySummary(ctx context.Context) {
	log.Printf("[email/scheduler] daily summary trigger fired at %s", time.Now().Format(time.RFC3339))
}

func nextTime(hour, min, sec int) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
