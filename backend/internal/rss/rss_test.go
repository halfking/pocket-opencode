package rss

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>Example</title><item><guid>a</guid><title> Go News </title><link>https://example.com/a</link><description><![CDATA[<b>Go</b> release]]></description><pubDate>Wed, 01 Jan 2025 00:00:00 GMT</pubDate></item><item><guid>a</guid><title>Duplicate</title><link>https://example.com/a</link><description>same</description></item><item><guid>b</guid><title>Other</title><link>https://example.com/b</link><description>Other text</description></item></channel></rss>`

func TestParserNormalizesAndDeduplicatesStableHash(t *testing.T) {
	s := Source{ID: "s", UserID: "u", WorkspaceID: "w", Language: "en"}
	a, e := Parse([]byte(testFeed), s)
	if e != nil {
		t.Fatal(e)
	}
	if len(a) != 3 {
		t.Fatalf("got %d items", len(a))
	}
	if a[0].Title != "Go News" || a[0].Summary != "Go release" {
		t.Fatalf("normalization failed: %#v", a[0])
	}
	b, e := Parse([]byte(testFeed), s)
	if e != nil || a[0].Hash != b[0].Hash || a[0].ID != b[0].ID {
		t.Fatalf("unstable IDs")
	}
}
func TestFilterKeywordsLanguageAndWindow(t *testing.T) {
	at := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	item := Item{Title: "Go Security", Summary: "release", Language: "en-US", PublishedAt: &at}
	rSince := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	r := FilterRule{Enabled: true, IncludeKeywords: []string{"security"}, Languages: []string{"en"}, Since: &rSince, MinRelevance: .1}
	got, ok := ApplyFilter(item, []FilterRule{r})
	if !ok || got.Relevance <= 0 || len(got.MatchReasons) < 2 {
		t.Fatalf("expected match: %#v %v", got, ok)
	}
	r.ExcludeKeywords = []string{"security"}
	if _, ok = ApplyFilter(item, []FilterRule{r}); ok {
		t.Fatal("exclude did not win")
	}
	r.ExcludeKeywords = nil
	r.Since = &time.Time{}
	*r.Since = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, ok = ApplyFilter(item, []FilterRule{r}); ok {
		t.Fatal("time window did not reject")
	}
}
func TestValidateURLSSRFAndLimits(t *testing.T) {
	for _, raw := range []string{"ftp://example.com", "http://127.0.0.1/x", "http://[::1]/", "http://localhost", "http://169.254.169.254", "http://10.0.0.1", "http://example.com:99999"} {
		if ValidateURL(raw) == nil {
			t.Errorf("accepted unsafe %s", raw)
		}
	}
	if ValidateURL("https://example.com/feed?x=1") != nil {
		t.Fatal("rejected valid URL")
	}
	if _, e := readLimited(strings.NewReader("1234"), 3); e == nil {
		t.Fatal("limit not enforced")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type memStore struct {
	items   map[string]bool
	fetched bool
	err     error
}

func (m *memStore) Available() bool { return true }
func (m *memStore) ClaimDueSources(context.Context, Scope, time.Time, int) ([]Source, error) {
	return nil, nil
}
func (m *memStore) UpsertItem(_ context.Context, x Item) error { m.items[x.Hash] = true; return m.err }
func (m *memStore) SetSourceFetched(context.Context, Scope, string, time.Time, string, string, error) error {
	m.fetched = true
	return nil
}
func (m *memStore) UpsertItemScoped(_ context.Context, x Item, _ Scope) (bool, error) {
	if m.items[x.Hash] {
		return false, nil
	}
	m.items[x.Hash] = true
	return true, m.err
}
func (m *memStore) GetSource(_ context.Context, id string, sc Scope) (*Source, error) {
	return &Source{ID: id, UserID: sc.UserID, WorkspaceID: sc.WorkspaceID, URL: "https://example.com/feed"}, nil
}
func (m *memStore) ListFilterRules(context.Context, Scope) ([]FilterRule, error) { return nil, nil }
func resp(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestFetcherSuccessDuplicateAndConditional304(t *testing.T) {
	m := &memStore{items: map[string]bool{}}
	var seenReq *http.Request
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { seenReq = r; return resp(r, 200, testFeed), nil })}
	f := NewFetcher(m, c)
	src := Source{ID: "s", UserID: "u", WorkspaceID: "w", URL: "https://example.com/feed"}
	out, e := f.FetchOne(context.Background(), src, nil)
	if e != nil {
		t.Fatal(e)
	}
	if out.NewItems != 2 || out.DuplicateItems != 1 {
		t.Fatalf("dedup result %#v", out)
	}
	if seenReq.Header.Get("User-Agent") == "" {
		t.Fatal("missing user agent")
	}
	c.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-None-Match") != "tag" || r.Header.Get("If-Modified-Since") == "" {
			t.Fatal("conditional headers missing")
		}
		return resp(r, 304, ""), nil
	})
	src.ETag = "tag"
	src.LastModified = "y"
	out, e = f.FetchOne(context.Background(), src, nil)
	if e != nil || !out.NotModified {
		t.Fatalf("304 %#v %v", out, e)
	}
}
func TestFetcherBodyLimitAndRefreshStatus(t *testing.T) {
	m := &memStore{items: map[string]bool{}}
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return resp(r, 200, strings.Repeat("x", 100)), nil })}
	f := NewFetcher(m, c)
	f.SetMaxBodyBytes(10)
	src := Source{ID: "s", UserID: "u", WorkspaceID: "w", URL: "https://example.com/feed"}
	if _, e := f.RefreshSource(context.Background(), src, nil); e == nil || !m.fetched {
		t.Fatal("oversized response/status update missing")
	}
}
