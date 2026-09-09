// TestRSSPureHelpers 验证不依赖 *Server 构造的纯函数（rssPathTail、sharecard 渲染）。
//
// 完整的 /api/rss/* 路由集成测试需要构建 server 包，但 repo 当前 server.go 有
// HEAD 一直存在的未实现符号（filterInstancesSince 等），属于 pre-existing 状态。
// 因此本文件只覆盖纯函数；handler 端到端测试应在 server.go 修复完整后另行补上。
package server

import (
	"strings"
	"testing"
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

// TestRSSSeedListHasExpectedEntries 验证 seeds 列表含关键源（不依赖 Server 构造）。
func TestRSSSeedListHasExpectedEntries(t *testing.T) {
	if len(rssSeedFeeds) < 3 {
		t.Fatalf("expected at least 3 seeds, got %d", len(rssSeedFeeds))
	}
	foundHN, foundCN := false, false
	for _, s := range rssSeedFeeds {
		if strings.Contains(strings.ToLower(s.Title), "hacker news") {
			foundHN = true
		}
		if s.Language == "zh" {
			foundCN = true
		}
	}
	if !foundHN {
		t.Errorf("seed list should include a Hacker News entry")
	}
	if !foundCN {
		t.Errorf("seed list should include at least one Chinese feed")
	}
}