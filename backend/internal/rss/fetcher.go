package rss

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type FetchResult struct {
	Items              []Item
	NewItems           int
	DuplicateItems     int
	NotModified        bool
	ETag, LastModified string
}

type Fetcher struct {
	store        StoreAPI
	client       *http.Client
	parser       *Parser
	maxBodyBytes int64
	now          func() time.Time
}

func NewFetcher(store StoreAPI, client *http.Client) *Fetcher {
	if client == nil {
		client = NewSafeHTTPClient(HTTPOptions{})
	} else if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			if err := ValidateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		}
	}
	return &Fetcher{store: store, client: client, parser: NewParser(), maxBodyBytes: DefaultMaxBodyBytes, now: time.Now}
}
func NewStandaloneFetcher(client *http.Client) *Fetcher { return NewFetcher(nil, client) }
func (f *Fetcher) SetMaxBodyBytes(n int64) {
	if n > 0 {
		f.maxBodyBytes = n
	}
}
func (f *Fetcher) SetParser(p *Parser) {
	if p != nil {
		f.parser = p
	}
}

// FetchOne performs one conditional request, parses and filters the response,
// deduplicates both within the document and at the Store boundary.
func (f *Fetcher) FetchOne(ctx context.Context, source Source, rules []FilterRule) (FetchResult, error) {
	var out FetchResult
	if err := ValidateURL(source.URL); err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "OpenPocket-RSS/1.0")
	if source.ETag != "" {
		req.Header.Set("If-None-Match", source.ETag)
	}
	if source.LastModified != "" {
		req.Header.Set("If-Modified-Since", source.LastModified)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return out, fmt.Errorf("rss: fetch: %w", err)
	}
	if resp.Request != nil {
		if err := ValidateURL(resp.Request.URL.String()); err != nil {
			resp.Body.Close()
			return out, fmt.Errorf("rss: unsafe redirect target: %w", err)
		}
	}
	defer resp.Body.Close()
	out.ETag = resp.Header.Get("ETag")
	out.LastModified = resp.Header.Get("Last-Modified")
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("rss: fetch status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, f.maxBodyBytes)
	if err != nil {
		return out, err
	}
	items, err := f.parser.Parse(body, source)
	if err != nil {
		return out, err
	}
	seen := map[string]struct{}{}
	for _, item := range items {
		if _, ok := seen[item.Hash]; ok {
			out.DuplicateItems++
			continue
		}
		seen[item.Hash] = struct{}{}
		item, ok := ApplyFilter(item, rules)
		if !ok {
			continue
		}
		out.Items = append(out.Items, item)
		if f.store == nil {
			out.NewItems++
			continue
		}
		if scoped, ok := f.store.(interface {
			UpsertItemScoped(context.Context, Item, Scope) (bool, error)
		}); ok {
			inserted, e := scoped.UpsertItemScoped(ctx, item, Scope{source.UserID, source.WorkspaceID})
			if e != nil {
				return out, e
			}
			if inserted {
				out.NewItems++
			} else {
				out.DuplicateItems++
			}
		} else if e := f.store.UpsertItem(ctx, item); e != nil {
			return out, e
		} else {
			out.NewItems++
		}
	}
	return out, nil
}
func (f *Fetcher) RefreshSource(ctx context.Context, source Source, rules []FilterRule) (out FetchResult, err error) {
	at := f.now().UTC()
	out, err = f.FetchOne(ctx, source, rules)
	if f.store != nil {
		updateErr := f.store.SetSourceFetched(ctx, Scope{source.UserID, source.WorkspaceID}, source.ID, at, out.ETag, out.LastModified, err)
		if err == nil && updateErr != nil {
			err = updateErr
		}
	}
	return out, err
}

func contentTypeIsFeed(v string) bool {
	v = strings.ToLower(v)
	return strings.Contains(v, "rss") || strings.Contains(v, "atom") || strings.Contains(v, "xml")
}
