package rss

import (
	"strings"
	"testing"
)

// TestStarterCatalogCoversThreeCategories 锁住"IT / 财经 / 实事"三类都必须有内容。
// 用户要的就是这三类；任何一类被清空都属于需求回退，必须在这里就红。
func TestStarterCatalogCoversThreeCategories(t *testing.T) {
	for _, cat := range []string{CategoryIT, CategoryFinance, CategoryNews} {
		feeds := StarterFeedsByCategory(cat)
		if len(feeds) < 3 {
			t.Errorf("category %q has only %d feeds, want >= 3", cat, len(feeds))
		}
		for _, f := range feeds {
			if f.Category != cat {
				t.Errorf("StarterFeedsByCategory(%q) returned a feed with category %q (%s)", cat, f.Category, f.URL)
			}
		}
	}
}

// TestStarterCatalogEntriesAreWellFormed 挡住"目录里写了死链/空标题"这类问题：
// 这些字段错了用户会看到一个点了没反应的种子项。
func TestStarterCatalogEntriesAreWellFormed(t *testing.T) {
	seen := map[string]string{}
	for _, f := range StarterFeeds {
		if err := ValidateURL(f.URL); err != nil {
			t.Errorf("catalog feed %q has invalid url: %v", f.URL, err)
		}
		if strings.TrimSpace(f.Title) == "" {
			t.Errorf("catalog feed %q has empty title", f.URL)
		}
		if strings.TrimSpace(f.SiteURL) == "" {
			t.Errorf("catalog feed %q has empty site url", f.URL)
		}
		if f.FetchInterval <= 0 {
			t.Errorf("catalog feed %q has no fetch interval", f.URL)
		}
		if f.Language != "zh" && f.Language != "en" {
			t.Errorf("catalog feed %q has unexpected language %q", f.URL, f.Language)
		}
		key := normalizeFeedURL(f.URL)
		if prev, dup := seen[key]; dup {
			t.Errorf("catalog has duplicate url %q (%s and %s)", f.URL, prev, f.Title)
		}
		seen[key] = f.Title
	}
}

func TestStarterFeedsByCategoryUnknownCategoryIsEmpty(t *testing.T) {
	if got := StarterFeedsByCategory("nonexistent-category"); len(got) != 0 {
		t.Errorf("unknown category should yield no feeds, got %d", len(got))
	}
}

func TestStarterCategoryLabel(t *testing.T) {
	cases := map[string]string{
		CategoryIT:      "IT 科技",
		CategoryFinance: "财经",
		CategoryNews:    "时事",
		"":              "其它",
		"whatever":      "其它",
	}
	for in, want := range cases {
		if got := StarterCategoryLabel(in); got != want {
			t.Errorf("StarterCategoryLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeFeedURLKeepsQueryString 说明归一化的边界：查询串是 feed 身份的一部分
// （Nasdaq 的 category=Markets 之类），不能被当成尾随噪声切掉。
func TestNormalizeFeedURLKeepsQueryString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://a.com/feed/", "https://a.com/feed"},
		{"HTTPS://A.com/feed", "https://a.com/feed"},
		{"  https://a.com/feed  ", "https://a.com/feed"},
		{"https://a.com/rss?category=Markets", "https://a.com/rss?category=markets"},
	}
	for _, c := range cases {
		if got := normalizeFeedURL(c.in); got != c.want {
			t.Errorf("normalizeFeedURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := normalizeFeedURL("  "); got != "" {
		t.Errorf("blank url should normalize to empty, got %q", got)
	}
}
