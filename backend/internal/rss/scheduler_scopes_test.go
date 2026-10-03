package rss

import (
	"context"
	"sync"
	"testing"
	"time"
)

// scopeStore 是一个"每个作用域各有一条待拉取源"的假库，用来证明调度器
// 会扫到所有作用域，而不只是构造时传入的那一个。
type scopeStore struct {
	mu       sync.Mutex
	scopes   []Scope
	claimed  []string
	fetched  map[string]bool
	ruleCall []string
}

func (s *scopeStore) Available() bool { return true }

func (s *scopeStore) ListActiveScopes(context.Context) ([]Scope, error) {
	return s.scopes, nil
}

func (s *scopeStore) ClaimDueSources(_ context.Context, sc Scope, now time.Time, _ int) ([]Source, error) {
	s.mu.Lock()
	s.claimed = append(s.claimed, sc.UserID+"/"+sc.WorkspaceID)
	s.mu.Unlock()
	return []Source{{ID: "src-" + sc.UserID + "-" + sc.WorkspaceID, UserID: sc.UserID, WorkspaceID: sc.WorkspaceID, URL: "https://example.com/feed", Status: SourceActive, Enabled: true}}, nil
}

func (s *scopeStore) ListFilterRules(_ context.Context, sc Scope) ([]FilterRule, error) {
	s.mu.Lock()
	s.ruleCall = append(s.ruleCall, sc.UserID+"/"+sc.WorkspaceID)
	s.mu.Unlock()
	return nil, nil
}

func (s *scopeStore) UpsertItem(context.Context, Item) error { return nil }

func (s *scopeStore) SetSourceFetched(_ context.Context, sc Scope, id string, _ time.Time, _, _ string, _ error) error {
	s.mu.Lock()
	if s.fetched == nil {
		s.fetched = map[string]bool{}
	}
	s.fetched[id] = true
	s.mu.Unlock()
	return nil
}

func (s *scopeStore) GetSource(_ context.Context, id string, sc Scope) (*Source, error) {
	return &Source{ID: id, UserID: sc.UserID, WorkspaceID: sc.WorkspaceID, URL: "https://example.com/feed"}, nil
}

// TestSchedulerScansEveryActiveScope 是对"订阅了却一条都没拉回来"的回归护栏：
// 真实用户的源写在 (JWT user, workspace) 作用域下，调度器必须扫到它们。
func TestSchedulerScansEveryActiveScope(t *testing.T) {
	store := &scopeStore{scopes: []Scope{
		{UserID: "alice", WorkspaceID: "ws1"},
		{UserID: "bob", WorkspaceID: "ws2"},
	}}
	f := NewFetcher(store, nil)
	sched := NewScheduler(store, f, Scope{UserID: "local", WorkspaceID: "default"})
	sched.SetMaxParallel(1)

	sched.scan(context.Background())
	sched.wg.Wait()

	for _, want := range []string{"alice/ws1", "bob/ws2", "local/default"} {
		if !containsStr(store.claimed, want) {
			t.Errorf("scope %q was never claimed; claimed=%v", want, store.claimed)
		}
	}
	if !containsStr(store.ruleCall, "alice/ws1") {
		t.Errorf("filter rules must be read per scope; got %v", store.ruleCall)
	}
	for id := range store.fetched {
		if id == "src-alice-ws1" || id == "src-bob-ws2" {
			// 拉取失败也会写 fetched（失败信息记在 source.error 上），所以这里
			// 只能断言"确实被尝试过"，不能断言内容。
			continue
		}
		t.Logf("unexpected fetch attempt: %s", id)
	}
	if len(store.fetched) == 0 {
		t.Error("no source was refreshed at all")
	}
}

// TestSchedulerDeduplicatesPrimaryScope 防止同一个作用域被扫两次
// （既在库里、又等于构造作用域时），那会把拉取频率翻倍。
func TestSchedulerDeduplicatesPrimaryScope(t *testing.T) {
	store := &scopeStore{scopes: []Scope{{UserID: "local", WorkspaceID: "default"}}}
	sched := NewScheduler(store, NewFetcher(store, nil), Scope{UserID: "local", WorkspaceID: "default"})
	sched.scan(context.Background())
	n := 0
	for _, c := range store.claimed {
		if c == "local/default" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("primary scope claimed %d times, want 1; claimed=%v", n, store.claimed)
	}
}

// TestSchedulerWithoutScopeListerStillScansPrimary 保护旧实现：
// 不实现 ListActiveScopes 的 store（自定义/测试实现）不能把调度器弄坏。
func TestSchedulerWithoutScopeListerStillScansPrimary(t *testing.T) {
	m := &memStore{items: map[string]bool{}}
	sched := NewScheduler(m, NewFetcher(m, nil), Scope{UserID: "u", WorkspaceID: "w"})
	sched.scan(context.Background())
	// memStore.ClaimDueSources 不返回源，所以这里只能断言"没崩、没卡住"。
	// 真正的护栏在上面的 TestSchedulerScansEveryActiveScope。
	if !m.fetched && len(m.items) != 0 {
		t.Errorf("unexpected state: fetched=%v items=%v", m.fetched, m.items)
	}
}
