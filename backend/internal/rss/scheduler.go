package rss

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

type SchedulerStore interface {
	StoreAPI
	GetSource(context.Context, string, Scope) (*Source, error)
	ListFilterRules(context.Context, Scope) ([]FilterRule, error)
}
type Scheduler struct {
	store               SchedulerStore
	fetcher             *Fetcher
	scope               Scope
	interval            time.Duration
	maxParallel         int
	startOnce, stopOnce sync.Once
	stop                chan struct{}
	wg                  sync.WaitGroup
	sem                 chan struct{}
}

func NewScheduler(store SchedulerStore, fetcher *Fetcher, scope Scope) *Scheduler {
	if fetcher == nil {
		fetcher = NewFetcher(store, nil)
	}
	return &Scheduler{store: store, fetcher: fetcher, scope: scope, interval: time.Minute, maxParallel: 4, stop: make(chan struct{}), sem: make(chan struct{}, 4)}
}
func (s *Scheduler) SetInterval(v time.Duration) {
	if v > 0 {
		s.interval = v
	}
}
func (s *Scheduler) SetMaxParallel(v int) {
	if v < 1 {
		v = 1
	}
	s.maxParallel = v
	s.sem = make(chan struct{}, v)
}
func (s *Scheduler) Start(ctx context.Context) error {
	if s == nil || s.store == nil || !s.store.Available() {
		return ErrStoreUnavailable
	}
	if err := requireScope(s.scope); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.startOnce.Do(func() { s.wg.Add(1); go s.loop(ctx) })
	return nil
}
func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	s.scan(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.scan(ctx)
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		}
	}
}
func (s *Scheduler) scan(ctx context.Context) {
	sources, err := s.store.ClaimDueSources(ctx, s.scope, time.Now().UTC(), s.maxParallel)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("[rss] claim due sources: %v", err)
		}
		return
	}
	rules, err := s.store.ListFilterRules(ctx, s.scope)
	if err != nil {
		log.Printf("[rss] list rules: %v", err)
		return
	}
	for i := range sources {
		src := sources[i]
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.sem <- struct{}{}
			defer func() { <-s.sem }()
			if _, e := s.fetcher.RefreshSource(ctx, src, rules); e != nil && !errors.Is(e, context.Canceled) {
				log.Printf("[rss] refresh %s: %v", src.ID, e)
			}
		}()
	}
}
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// RunNow synchronously refreshes one source while respecting the same worker bound as scheduled runs.
func (s *Scheduler) RunNow(ctx context.Context, sourceID string) (FetchResult, error) {
	var out FetchResult
	if s == nil || s.store == nil || !s.store.Available() {
		return out, ErrStoreUnavailable
	}
	src, err := s.store.GetSource(ctx, sourceID, s.scope)
	if err != nil {
		return out, err
	}
	rules, err := s.store.ListFilterRules(ctx, s.scope)
	if err != nil {
		return out, err
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	return s.fetcher.RefreshSource(ctx, *src, rules)
}
