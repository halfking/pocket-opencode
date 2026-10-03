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

// scopeLister 是可选接口：实现了它，调度器就会按库里真实存在的作用域轮询，
// 而不是只认启动时写死的那个。
//
// 为什么要这个：scheduler 以前只有 NewScheduler 传入的单个 Scope
// （cmd/pocketd/main.go 里是 local/default），而订阅源是通过 API 按真实
// 用户+工作区写入的。结果就是"订阅看起来成功了，但后台一条都没拉回来"，
// 因为那些源根本不在扫描范围内。
type scopeLister interface {
	ListActiveScopes(context.Context) ([]Scope, error)
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
	// 每个作用域独立取规则：规则本身也是按 (user, workspace) 存的，
	// 拿别人的规则去过滤别人的源等于没有过滤。
	for _, sc := range s.scopes(ctx) {
		s.scanScope(ctx, sc)
	}
}

// scopes 返回本次要扫描的作用域：库里真实存在的 + 构造时传入的兜底。
func (s *Scheduler) scopes(ctx context.Context) []Scope {
	out := []Scope{}
	if lister, ok := s.store.(scopeLister); ok {
		scopes, err := lister.ListActiveScopes(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("[rss] list active scopes: %v", err)
			}
		} else {
			out = append(out, scopes...)
		}
	}
	// 兜底作用域始终保留：它可能还没有任何源（刚启动时），但下一 tick 可能有。
	if s.scope.valid() && !containsScope(out, s.scope) {
		out = append(out, s.scope)
	}
	return out
}

func containsScope(list []Scope, sc Scope) bool {
	for _, x := range list {
		if x == sc {
			return true
		}
	}
	return false
}

func (s *Scheduler) scanScope(ctx context.Context, sc Scope) {
	sources, err := s.store.ClaimDueSources(ctx, sc, time.Now().UTC(), s.maxParallel)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("[rss] claim due sources (%s/%s): %v", sc.UserID, sc.WorkspaceID, err)
		}
		return
	}
	rules, err := s.store.ListFilterRules(ctx, sc)
	if err != nil {
		log.Printf("[rss] list rules (%s/%s): %v", sc.UserID, sc.WorkspaceID, err)
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
