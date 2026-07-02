package email

import (
	"context"
	"time"
)

// Scheduler periodically triggers IMAP syncs for all enabled accounts and
// fires the daily-summary job at a fixed hour. It is a simple in-process
// ticker loop — no external cron dependency. Each account's SyncIntervalMin
// is honored independently.
//
// Skeleton: Start launches background goroutines; Stop cancels them.
type Scheduler struct {
	store   *Store
	fetcher *Fetcher
	stop    chan struct{}
}

func NewScheduler(store *Store, fetcher *Fetcher) *Scheduler {
	return &Scheduler{store: store, fetcher: fetcher, stop: make(chan struct{})}
}

// Start launches the polling loop and the daily 21:00 summary trigger.
// Call once from main.go after constructing the scheduler.
func (s *Scheduler) Start(ctx context.Context) {
	go s.pollLoop(ctx)
	go s.dailySummaryLoop(ctx)
}

func (s *Scheduler) Stop() { close(s.stop) }

func (s *Scheduler) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// TODO Phase 3: load all enabled accounts, for each whose
			// (now - last_synced_at) >= sync_interval_min, call fetcher.Sync.
		}
	}
}

func (s *Scheduler) dailySummaryLoop(ctx context.Context) {
	for {
		// Sleep until next 21:00 local time.
		next := nextTime(21, 0, 0)
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			// TODO Phase 4: gather today's emails per user, call kxmemory
			// /api/email/daily-summary, store result, broadcast ws event.
		}
	}
}

func nextTime(hour, min, sec int) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
