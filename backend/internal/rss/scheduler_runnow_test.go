package rss

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// ownedStore 是一个「源带归属作用域」的假库：只有用**它真正所属的**作用域去
// GetSource 才命中。用它复现生产上的形态 —— 订阅源按真实用户写入，而 scheduler
// 构造时拿到的是另一个作用域。
type ownedStore struct {
	mu        sync.Mutex
	source    Source
	owner     Scope
	scopes    []Scope
	fetched   []string
	ruleScope []string
}

func (o *ownedStore) Available() bool { return true }

func (o *ownedStore) ListActiveScopes(context.Context) ([]Scope, error) { return o.scopes, nil }

func (o *ownedStore) GetSource(_ context.Context, id string, sc Scope) (*Source, error) {
	if sc == o.owner && id == o.source.ID {
		return &o.source, nil
	}
	return nil, ErrNotFound
}

func (o *ownedStore) ListFilterRules(_ context.Context, sc Scope) ([]FilterRule, error) {
	o.mu.Lock()
	o.ruleScope = append(o.ruleScope, sc.UserID+"/"+sc.WorkspaceID)
	o.mu.Unlock()
	return nil, nil
}

func (o *ownedStore) ClaimDueSources(context.Context, Scope, time.Time, int) ([]Source, error) {
	return nil, nil
}

func (o *ownedStore) UpsertItem(context.Context, Item) error { return nil }

func (o *ownedStore) SetSourceFetched(_ context.Context, sc Scope, id string, _ time.Time, _, _ string, _ error) error {
	o.mu.Lock()
	o.fetched = append(o.fetched, sc.UserID+"/"+sc.WorkspaceID+":"+id)
	o.mu.Unlock()
	return nil
}

// TestRunNowFindsSourceOutsideSchedulerScope 是"立即拉取恒 404"的回归锁。
//
// 生产形态：main.go 用 Scope{local, default} 构造 scheduler，而订阅源写在
// (JWT 用户, 工作区) 下。旧实现只查构造作用域，于是真实用户点「立即拉取」
// 一律 404，而且本地单用户部署看不出来（两边恰好相同）。
func TestRunNowFindsSourceOutsideSchedulerScope(t *testing.T) {
	store := &ownedStore{
		source: Source{ID: "src-1", UserID: "alice", WorkspaceID: "ws-alice", URL: "https://example.com/feed", Status: SourceActive, Enabled: true},
		owner:  Scope{UserID: "alice", WorkspaceID: "ws-alice"},
		scopes: []Scope{{UserID: "alice", WorkspaceID: "ws-alice"}},
	}
	f := NewFetcher(store, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return resp(r, 200, testFeed), nil
	})})
	sched := NewScheduler(store, f, Scope{UserID: "local", WorkspaceID: "default"})

	out, err := sched.RunNow(context.Background(), "src-1")
	if err != nil {
		t.Fatalf("RunNow must find the source across scopes, got %v", err)
	}
	if out.NewItems != 2 {
		t.Errorf("newItems = %d, want 2", out.NewItems)
	}
	if len(store.fetched) != 1 || store.fetched[0] != "alice/ws-alice:src-1" {
		t.Errorf("fetch recorded under wrong scope: %v", store.fetched)
	}
	if len(store.ruleScope) != 1 || store.ruleScope[0] != "alice/ws-alice" {
		t.Errorf("filter rules must be read from the source's own scope, got %v", store.ruleScope)
	}
}

// TestRunNowInScopeDoesNotCrossTenants：请求方自带作用域，找不到就 404，
// 绝不允许回退去扫库（否则 A 用户能刷新 B 用户的源）。
func TestRunNowInScopeDoesNotCrossTenants(t *testing.T) {
	store := &ownedStore{
		source: Source{ID: "src-1", UserID: "alice", WorkspaceID: "ws-alice", URL: "https://example.com/feed", Status: SourceActive, Enabled: true},
		owner:  Scope{UserID: "alice", WorkspaceID: "ws-alice"},
		scopes: []Scope{{UserID: "alice", WorkspaceID: "ws-alice"}},
	}
	sched := NewScheduler(store, NewFetcher(store, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return resp(r, 200, testFeed), nil
	})}), Scope{UserID: "local", WorkspaceID: "default"})

	if _, err := sched.RunNowInScope(context.Background(), Scope{UserID: "alice", WorkspaceID: "ws-alice"}, "src-1"); err != nil {
		t.Fatalf("owner scope should work: %v", err)
	}
	// 对照：同一个 id，另一个用户来点。
	_, err := sched.RunNowInScope(context.Background(), Scope{UserID: "mallory", WorkspaceID: "ws-alice"}, "src-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant refresh must 404, got %v", err)
	}
	if len(store.fetched) != 1 {
		t.Errorf("only the owner's refresh may reach the store, got %v", store.fetched)
	}
	// 作用域不合法要拒，而不是当成"随便找个作用域"。
	if _, err := sched.RunNowInScope(context.Background(), Scope{}, "src-1"); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("invalid scope should be rejected, got %v", err)
	}
}

func TestRunNowUnknownSourceIsNotFound(t *testing.T) {
	store := &ownedStore{scopes: []Scope{{UserID: "alice", WorkspaceID: "ws"}}}
	sched := NewScheduler(store, NewFetcher(store, nil), Scope{UserID: "local", WorkspaceID: "default"})
	if _, err := sched.RunNow(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown source = %v, want ErrNotFound", err)
	}
}
