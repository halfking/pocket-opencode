package rss

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

type DiscoveryResult struct{ URL, Title, ContentType string }
type SearchProvider interface {
	Search(context.Context, string) ([]DiscoveryResult, error)
}

// Discover finds feed links advertised by a site's HTML and probes common feed
// paths. It returns safe, absolute URLs in stable order without duplicates.
func Discover(ctx context.Context, rawURL string, client *http.Client) ([]DiscoveryResult, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}
	base, _ := url.Parse(rawURL)
	if client == nil {
		client = NewSafeHTTPClient(HTTPOptions{})
	} else if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			return ValidateURL(req.URL.String())
		}
	}
	results := []DiscoveryResult{}
	seen := map[string]bool{}
	add := func(raw, title, ct string) {
		u, e := base.Parse(strings.TrimSpace(raw))
		if e != nil || u.Host == "" || ValidateURL(u.String()) != nil {
			return
		}
		key := u.String()
		if !seen[key] {
			seen[key] = true
			results = append(results, DiscoveryResult{URL: key, Title: strings.TrimSpace(title), ContentType: ct})
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/rss+xml,application/atom+xml;q=0.9")
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			body, e := readLimited(resp.Body, DefaultMaxBodyBytes)
			if e != nil {
				return nil, e
			}
			ct := strings.ToLower(resp.Header.Get("Content-Type"))
			if strings.Contains(ct, "xml") || strings.Contains(ct, "rss") || strings.Contains(ct, "atom") {
				add(rawURL, "", ct)
			} else if doc, e := html.Parse(strings.NewReader(string(body))); e == nil {
				walkLinks(doc, add)
			}
		}
	}
	// Discovery remains useful when a homepage is unavailable: paths are only
	// candidates; fetcher will verify their content before persisting items.
	for _, path := range []string{"/feed", "/feed/", "/feed.xml", "/rss", "/rss.xml", "/atom.xml", "/index.xml"} {
		add(path, "", "")
	}
	if len(results) == 0 && err != nil {
		return nil, fmt.Errorf("rss: discovery: %w", err)
	}
	return results, nil
}
func walkLinks(n *html.Node, add func(string, string, string)) {
	if n.Type == html.ElementNode && n.Data == "link" {
		var rel, href, title, ct string
		for _, a := range n.Attr {
			switch strings.ToLower(a.Key) {
			case "rel":
				rel = a.Val
			case "href":
				href = a.Val
			case "title":
				title = a.Val
			case "type":
				ct = a.Val
			}
		}
		for _, r := range strings.Fields(strings.ToLower(rel)) {
			if r == "alternate" && (strings.Contains(strings.ToLower(ct), "rss") || strings.Contains(strings.ToLower(ct), "atom") || strings.Contains(strings.ToLower(ct), "xml")) {
				add(href, title, ct)
				break
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkLinks(c, add)
	}
}
