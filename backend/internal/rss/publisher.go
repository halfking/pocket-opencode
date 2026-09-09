package rss

import (
	"context"
	"errors"
	"strings"
)

var ErrPublisherUnavailable = errors.New("rss: publisher unavailable; use share-only output")

type PublishResult struct{ Platform, RemoteID, ShareText string }
type Publisher interface {
	Publish(context.Context, Draft, Item) (PublishResult, error)
}

// ShareText creates copyable text only. It performs no external request and is
// therefore the safe default until the user explicitly configures an approved
// publishing integration.
func ShareText(d Draft, item Item) string {
	text := strings.TrimSpace(d.Text)
	if text == "" {
		text = strings.TrimSpace(item.Title)
	}
	link := strings.TrimSpace(item.URL)
	if link != "" && !strings.Contains(text, link) {
		if text != "" {
			text += "\n"
		}
		text += link
	}
	return text
}

type ShareOnlyPublisher struct{}

func (ShareOnlyPublisher) Publish(_ context.Context, d Draft, item Item) (PublishResult, error) {
	return PublishResult{Platform: "share-only", ShareText: ShareText(d, item)}, nil
}

// WeiboPublisher is intentionally unavailable: this package must never mimic
// an unofficial API or publish without an explicitly authorized integration.
type WeiboPublisher struct{}

func (WeiboPublisher) Publish(context.Context, Draft, Item) (PublishResult, error) {
	return PublishResult{Platform: "weibo"}, ErrPublisherUnavailable
}
func NewWeiboPublisher() Publisher { return WeiboPublisher{} }
