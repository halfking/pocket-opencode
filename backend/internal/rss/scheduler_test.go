package rss

import (
	"context"
	"net/http"
	"testing"
)

func TestSchedulerRunNowRequiresScopeAndRefreshes(t *testing.T) {
	m := &memStore{items: map[string]bool{}}
	f := NewFetcher(m, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return resp(r, 200, testFeed), nil
	})})
	s := NewScheduler(m, f, Scope{UserID: "u", WorkspaceID: "w"})
	if _, err := s.RunNow(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	if !m.fetched {
		t.Fatal("source was not refreshed")
	}
	bad := NewScheduler(m, f, Scope{UserID: "u"})
	if err := bad.Start(context.Background()); err != ErrInvalidScope {
		t.Fatalf("scope error %v", err)
	}
}
