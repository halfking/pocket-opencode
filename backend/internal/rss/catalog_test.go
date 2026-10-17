package rss

import (
	"strings"
	"testing"
)

// TestStarterCatalogCoversAllCategories 锁住六个内置分类都必须有内容：
// IT / AI / 财经 / 新产品 / 好价·促销 / 时事。任何一类被清空都属于需求
// 回退，必须在这里就红。
func TestStarterCatalogCoversAllCategories(t *testing.T) {
	for _, cat := range []string{CategoryIT, CategoryAI, CategoryFinance, CategoryProduct, CategoryDeals, CategoryNews} {
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
	// StarterCategories 的顺序就是前端分组顺序；六个分类一个不能少。
	if got := StarterCategories(); len(got) != 6 {
		t.Errorf("StarterCategories() = %v, want 6 categories", got)
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
		CategoryAI:      "AI 动态",
		CategoryFinance: "财经",
		CategoryProduct: "新产品",
		CategoryDeals:   "好价·促销",
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

// TestStarterFeedsNoCrossCategoryDuplicates 确认 OpenAI 迁类后没有在两个分类
// 里重复出现（URL 归一化后全局唯一由 WellFormed 测试保证，这里只点明场景）。
func TestStarterFeedsNoCrossCategoryDuplicates(t *testing.T) {
	openaiCount := 0
	for _, f := range StarterFeeds {
		if strings.Contains(f.URL, "openai.com") {
			openaiCount++
			if f.Category != CategoryAI {
				t.Errorf("openai feed %s should be in %q, got %q", f.URL, CategoryAI, f.Category)
			}
		}
	}
	if openaiCount != 1 {
		t.Errorf("expected exactly 1 openai feed, got %d", openaiCount)
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
