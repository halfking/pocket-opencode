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
//
// 旧实现只用构造时那个作用域去找源：订阅源是按真实 (user, workspace) 写的，
// 于是手动「立即拉取」对真实用户**永远 404**（本地单用户部署看不出来，因为
// 两边恰好都是 local/default）。现在先按传入作用域找，找不到再在库里的其它
// 活跃作用域里找 —— 拿到的 Source 自带真实的 UserID/WorkspaceID，抓取与落库
// 都跟着它走，不会写到别人的作用域去。
func (s *Scheduler) RunNow(ctx context.Context, sourceID string) (FetchResult, error) {
	return s.runNow(ctx, nil, sourceID)
}

// RunNowInScope 是 RunNow 的作用域显式版本，供 HTTP handler 使用：请求方已经
// 知道自己是谁，直接在自己的作用域里找，既不用扫库，也不会命中别人的源。
func (s *Scheduler) RunNowInScope(ctx context.Context, sc Scope, sourceID string) (FetchResult, error) {
	return s.runNow(ctx, &sc, sourceID)
}

func (s *Scheduler) runNow(ctx context.Context, sc *Scope, sourceID string) (FetchResult, error) {
	var out FetchResult
	if s == nil || s.store == nil || !s.store.Available() {
		return out, ErrStoreUnavailable
	}
	scope, src, err := s.resolveSource(ctx, sc, sourceID)
	if err != nil {
		return out, err
	}
	rules, err := s.store.ListFilterRules(ctx, scope)
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

// resolveSource 定位源所属的作用域。prefer 非空时只用它（找不到就 404，
// 不跨作用域回退——那会把 A 用户点的刷新打到 B 用户的源上）。
func (s *Scheduler) resolveSource(ctx context.Context, prefer *Scope, sourceID string) (Scope, *Source, error) {
	try := func(sc Scope) (*Source, bool) {
		src, err := s.store.GetSource(ctx, sourceID, sc)
		if err != nil {
			return nil, false
		}
		return src, true
	}
	if prefer != nil {
		if !prefer.valid() {
			return Scope{}, nil, ErrInvalidScope
		}
		if src, ok := try(*prefer); ok {
			return *prefer, src, nil
		}
		return Scope{}, nil, ErrNotFound
	}
	if s.scope.valid() {
		if src, ok := try(s.scope); ok {
			return s.scope, src, nil
		}
	}
	for _, sc := range s.scopes(ctx) {
		if sc == s.scope {
			continue
		}
		if src, ok := try(sc); ok {
			return sc, src, nil
		}
	}
	return Scope{}, nil, ErrNotFound
}
