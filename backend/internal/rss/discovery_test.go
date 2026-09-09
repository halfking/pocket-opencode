package rss

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDiscoverAlternateAndCommonPaths(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/" {
			body := `<html><head><link rel="alternate" type="application/atom+xml" title="News" href="/atom.xml"></head></html>`
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	got, err := Discover(context.Background(), "https://example.com/", client)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].URL != "https://example.com/atom.xml" {
		t.Fatalf("discovery %#v", got)
	}
}

func TestPublisherBoundaries(t *testing.T) {
	item := Item{Title: "Title", URL: "https://example.com/a"}
	text := ShareText(Draft{Text: "A"}, item)
	if text != "A\nhttps://example.com/a" {
		t.Fatalf("share text %q", text)
	}
	if _, err := (WeiboPublisher{}).Publish(context.Background(), Draft{}, item); err != ErrPublisherUnavailable {
		t.Fatalf("weibo error %v", err)
	}
}
