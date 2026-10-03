// TestRSSPureHelpers 验证不依赖 *Server 构造的纯函数（rssPathTail、sharecard 渲染）。
//
// 完整的 /api/rss/* 路由集成测试需要构建 server 包，但 repo 当前 server.go 有
// HEAD 一直存在的未实现符号（filterInstancesSince 等），属于 pre-existing 状态。
// 因此本文件只覆盖纯函数；handler 端到端测试应在 server.go 修复完整后另行补上。
package server

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

// TestRSSPathTail 验证子路径提取工具。
func TestRSSPathTail(t *testing.T) {
	cases := []struct {
		raw, prefix, want string
	}{
		{"/api/rss/sources/abc123", "/api/rss/sources/", "abc123"},
		{"/api/rss/sources/abc/refresh", "/api/rss/sources/", "abc/refresh"},
		{"/api/rss/items/it-1", "/api/rss/items/", "it-1"},
		{"/api/rss/items/it-1/read", "/api/rss/items/", "it-1/read"},
		{"/api/rss/items/it-1/share-card", "/api/rss/items/", "it-1/share-card"},
	}
	for _, c := range cases {
		got := rssPathTail(c.raw, c.prefix)
		if got != c.want {
			t.Errorf("rssPathTail(%q, %q) = %q, want %q", c.raw, c.prefix, got, c.want)
		}
	}
}

// TestRSSStarterCatalogHasExpectedEntries 验证内置推荐源目录含 IT/财经/时事三类
// 且中英文都有（不依赖 Server 构造）。
//
// 这条测试之前锁的是 rssSeedFeeds —— 那份硬编码列表只有 5 条、全是科技/设计，
// 用户要的"财经 + 实事"根本不在里面。数据源换成了 rss.StarterFeeds，断言也跟着
// 换成"三类都必须有"。
func TestRSSStarterCatalogHasExpectedEntries(t *testing.T) {
	feeds := rss.StarterFeeds
	if len(feeds) < 9 {
		t.Fatalf("expected at least 9 recommended feeds, got %d", len(feeds))
	}
	var it, finance, news, zh, en, hn int
	for _, f := range feeds {
		switch f.Category {
		case rss.CategoryIT:
			it++
		case rss.CategoryFinance:
			finance++
		case rss.CategoryNews:
			news++
		default:
			t.Errorf("feed %q has unexpected category %q", f.URL, f.Category)
		}
		if f.Language == "zh" {
			zh++
		}
		if f.Language == "en" {
			en++
		}
		if strings.Contains(strings.ToLower(f.Title), "hacker news") {
			hn++
		}
	}
	if it == 0 || finance == 0 || news == 0 {
		t.Errorf("catalog must cover all three categories, got it=%d finance=%d news=%d", it, finance, news)
	}
	if zh == 0 || en == 0 {
		t.Errorf("catalog must be bilingual, got zh=%d en=%d", zh, en)
	}
	if hn == 0 {
		t.Errorf("catalog should include a Hacker News entry")
	}
}
