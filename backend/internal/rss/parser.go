package rss

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmcdole/gofeed"
)

const (
	MaxTitleLength  = 512
	MaxTextLength   = 64 * 1024
	MaxURLLength    = 4096
	MaxItemsPerFeed = 1000
)

type Parser struct{ MaxItems int }

func NewParser() *Parser { return &Parser{MaxItems: MaxItemsPerFeed} }

var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)

func normalizeText(s string, max int) string {
	s = html.UnescapeString(tagRE.ReplaceAllString(s, " "))
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}
func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > MaxURLLength {
		s = s[:MaxURLLength]
	}
	return s
}

// Parse parses RSS 2.0, RSS 1.0 and Atom documents using gofeed.
func (p *Parser) Parse(data []byte, source Source) ([]Item, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("rss: empty feed")
	}
	fp := gofeed.NewParser()
	feed, err := fp.ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("rss: parse feed: %w", err)
	}
	limit := p.MaxItems
	if limit <= 0 {
		limit = MaxItemsPerFeed
	}
	if limit > MaxItemsPerFeed {
		limit = MaxItemsPerFeed
	}
	items := make([]Item, 0, min(limit, len(feed.Items)))
	for i, in := range feed.Items {
		if i >= limit {
			break
		}
		guid := strings.TrimSpace(in.GUID)
		link := normalizeURL(in.Link)
		title := normalizeText(in.Title, MaxTitleLength)
		desc := normalizeText(in.Description, MaxTextLength)
		content := normalizeText(in.Content, MaxTextLength)
		if content == "" {
			content = desc
		}
		summary := desc
		if summary == "" {
			summary = normalizeText(content, 1000)
		}
		author := ""
		if in.Author != nil {
			author = normalizeText(in.Author.Name, 512)
			if author == "" {
				author = normalizeText(in.Author.Email, 512)
			}
		}
		published := in.PublishedParsed
		updated := in.UpdatedParsed
		if published == nil {
			published = updated
		}
		cats := append([]string(nil), in.Categories...)
		h := stableHash(guid, link, title, content)
		itemID := stableHash(source.ID, h, "", "")
		items = append(items, Item{ID: itemID, SourceID: source.ID, UserID: source.UserID, WorkspaceID: source.WorkspaceID, GUID: normalizeText(guid, MaxURLLength), Hash: h, URL: link, Title: title, Author: author, Summary: summary, Content: content, Categories: cats, PublishedAt: published, UpdatedAt: updated, FetchedAt: timePtr(time.Now().UTC()), Status: ItemUnread, Language: source.Language})
	}
	return items, nil
}
func timePtr(t time.Time) *time.Time { return &t }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Parse(data []byte, source Source) ([]Item, error) { return NewParser().Parse(data, source) }
