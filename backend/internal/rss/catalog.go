// Package rss — catalog.go
//
// 内置推荐订阅源目录（IT / 财经 / 实事）。这份目录里的每一条 feed 都在
// 2026-10-03 用真实 HTTP 请求验证过：200 + XML content-type + 能解析出
// item/entry 条目。三轮共探测 103 个候选，原始结果（status / content-type /
// 条目数 / 耗时）见 docs/handoff/evidence/rss-feed-probe/round{1,2,3}.json。
//
// 为什么要有 catalog：/sources/seeds 原来只有 5 条、全部是科技/设计，
// 用户看不到财经与实事；而且只能一条条点，没有"加入初始订阅列表"的入口。
// Catalog 把可推荐源集中成可枚举、可按分类批量导入的一等数据。
package rss

import (
	"sort"
	"strings"
	"time"
)

// 内置分类。空字符串的源在日报里归到 CategoryOther。
const (
	CategoryIT      = "it"
	CategoryFinance = "finance"
	CategoryNews    = "news"
	CategoryOther   = "other"
)

// StarterFeed 是一条内置推荐源。
type StarterFeed struct {
	URL           string        `json:"url"`
	Title         string        `json:"title"`
	SiteURL       string        `json:"siteUrl"`
	Language      string        `json:"language"`
	Category      string        `json:"category"`
	FetchInterval time.Duration `json:"-"`
	Note          string        `json:"note,omitempty"`
}

// StarterCategories 是目录的分类顺序（导入默认顺序、前端分组顺序）。
func StarterCategories() []string {
	return []string{CategoryIT, CategoryFinance, CategoryNews}
}

// StarterCategoryLabel 返回分类的中文标签，日报与前端共用。
func StarterCategoryLabel(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case CategoryIT:
		return "IT 科技"
	case CategoryFinance:
		return "财经"
	case CategoryNews:
		return "时事"
	default:
		return "其它"
	}
}

func starterMinutes(n int) time.Duration { return time.Duration(n) * time.Minute }

// StarterFeeds 是内置推荐源目录。顺序即前端默认展示顺序（按分类分组）。
//
// 维护规则：新增条目前必须先用真实 HTTP 请求确认 200 + XML + 有条目；
// 探测不到条目的站点一律不放进来，否则用户会看到一排永远空白的订阅。
var StarterFeeds = []StarterFeed{
	// ===== IT 科技 =====
	{URL: "https://www.ithome.com/rss/", Title: "IT之家", SiteURL: "https://www.ithome.com/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(20), Note: "中文科技资讯更新最快"},
	{URL: "https://www.36kr.com/feed", Title: "36氪", SiteURL: "https://www.36kr.com/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(20), Note: "创投与科技商业"},
	{URL: "https://sspai.com/feed", Title: "少数派", SiteURL: "https://sspai.com/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(60), Note: "数字效率与工具"},
	{URL: "https://www.ruanyifeng.com/blog/atom.xml", Title: "阮一峰的网络日志", SiteURL: "https://www.ruanyifeng.com/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(180), Note: "技术与科技评论长文"},
	{URL: "https://www.infoq.cn/feed", Title: "InfoQ 中文", SiteURL: "https://www.infoq.cn/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(60), Note: "软件架构与 AI 工程实践"},
	{URL: "https://www.infoq.cn/feed/news", Title: "InfoQ 快讯", SiteURL: "https://www.infoq.cn/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(30)},
	{URL: "https://www.solidot.org/index.rss", Title: "Solidot", SiteURL: "https://www.solidot.org/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(60), Note: "奇客的资讯，国内外科技要闻"},
	{URL: "https://www.oschina.net/news/rss", Title: "开源中国资讯", SiteURL: "https://www.oschina.net/", Language: "zh", Category: CategoryIT, FetchInterval: starterMinutes(60)},
	{URL: "https://hnrss.org/frontpage", Title: "Hacker News 首页", SiteURL: "https://news.ycombinator.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(20), Note: "英文技术圈风向标"},
	{URL: "https://hnrss.org/best", Title: "Hacker News 最佳", SiteURL: "https://news.ycombinator.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(30)},
	{URL: "https://github.blog/feed/", Title: "GitHub Blog", SiteURL: "https://github.blog/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(120)},
	{URL: "https://stackoverflow.blog/feed/", Title: "Stack Overflow Blog", SiteURL: "https://stackoverflow.blog/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(120)},
	{URL: "https://www.theverge.com/rss/index.xml", Title: "The Verge", SiteURL: "https://www.theverge.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(30)},
	{URL: "https://feeds.arstechnica.com/arstechnica/index", Title: "Ars Technica", SiteURL: "https://arstechnica.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(30)},
	{URL: "https://techcrunch.com/feed/", Title: "TechCrunch", SiteURL: "https://techcrunch.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(30), Note: "科技创业与融资"},
	{URL: "https://www.wired.com/feed/rss", Title: "WIRED", SiteURL: "https://www.wired.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(60)},
	{URL: "https://www.lwn.net/headlines/rss", Title: "LWN", SiteURL: "https://www.lwn.net/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(60), Note: "Linux 与开源内核"},
	{URL: "https://openai.com/blog/rss.xml", Title: "OpenAI Blog", SiteURL: "https://openai.com/blog/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(180), Note: "大模型进展一手信源"},
	{URL: "https://www.daringfireball.net/feeds/main", Title: "Daring Fireball", SiteURL: "https://www.daringfireball.net/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(120)},
	{URL: "https://www.smashingmagazine.com/feed/", Title: "Smashing Magazine", SiteURL: "https://www.smashingmagazine.com/", Language: "en", Category: CategoryIT, FetchInterval: starterMinutes(180), Note: "前端与设计"},

	// ===== 财经 =====
	{URL: "https://www.chinanews.com.cn/rss/finance.xml", Title: "中新网财经", SiteURL: "https://www.chinanews.com.cn/cj/", Language: "zh", Category: CategoryFinance, FetchInterval: starterMinutes(20), Note: "中文财经要闻，更新稳定"},
	{URL: "http://www.xinhuanet.com/fortune/news_fortune.xml", Title: "新华网财经", SiteURL: "http://www.xinhuanet.com/fortune/", Language: "zh", Category: CategoryFinance, FetchInterval: starterMinutes(30), Note: "国内财经政策与市场"},
	{URL: "https://xueqiu.com/hots/topic/rss", Title: "雪球热帖", SiteURL: "https://xueqiu.com/", Language: "zh", Category: CategoryFinance, FetchInterval: starterMinutes(30), Note: "投资者社区讨论热度"},
	{URL: "https://www.cnbc.com/id/10001147/device/rss/rss.html", Title: "CNBC Markets", SiteURL: "https://www.cnbc.com/markets/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(20), Note: "美股与全球市场"},
	{URL: "https://www.cnbc.com/id/20910258/device/rss/rss.html", Title: "CNBC Economy", SiteURL: "https://www.cnbc.com/economy/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30), Note: "宏观经济数据与解读"},
	{URL: "https://www.cnbc.com/id/100003114/device/rss/rss.html", Title: "CNBC Technology", SiteURL: "https://www.cnbc.com/technology/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30), Note: "科技股与科技公司动态"},
	{URL: "https://feeds.content.dowjones.io/public/rss/mw_topstories", Title: "MarketWatch 头条", SiteURL: "https://www.marketwatch.com/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30)},
	{URL: "https://www.marketwatch.com/rss/topstories", Title: "MarketWatch Top Stories", SiteURL: "https://www.marketwatch.com/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30)},
	{URL: "https://www.nasdaq.com/feed/rssoutbound?category=Markets", Title: "Nasdaq Markets", SiteURL: "https://www.nasdaq.com/market-activity", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30)},
	{URL: "https://fortune.com/feed/fortune-feeds/?id=3230629", Title: "Fortune", SiteURL: "https://fortune.com/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(60)},
	{URL: "https://seekingalpha.com/market_currents.xml", Title: "Seeking Alpha 市场快讯", SiteURL: "https://seekingalpha.com/", Language: "en", Category: CategoryFinance, FetchInterval: starterMinutes(30), Note: "盘面异动解释"},

	// ===== 时事 =====
	{URL: "https://www.chinanews.com.cn/rss/importnews.xml", Title: "中新网要闻", SiteURL: "https://www.chinanews.com.cn/", Language: "zh", Category: CategoryNews, FetchInterval: starterMinutes(20), Note: "国内时事要闻"},
	{URL: "https://www.chinanews.com.cn/rss/scroll-news.xml", Title: "中新网滚动新闻", SiteURL: "https://www.chinanews.com.cn/scroll-news/", Language: "zh", Category: CategoryNews, FetchInterval: starterMinutes(20)},
	{URL: "https://www.chinanews.com.cn/rss/world.xml", Title: "中新网国际", SiteURL: "https://www.chinanews.com.cn/world/", Language: "zh", Category: CategoryNews, FetchInterval: starterMinutes(30), Note: "国际时事"},
	{URL: "http://www.xinhuanet.com/politics/news_politics.xml", Title: "新华网时政", SiteURL: "http://www.xinhuanet.com/politics/", Language: "zh", Category: CategoryNews, FetchInterval: starterMinutes(30), Note: "国内时政要闻"},
	{URL: "https://a.jiemian.com/index.php?m=article&a=rss", Title: "界面新闻", SiteURL: "https://www.jiemian.com/", Language: "zh", Category: CategoryNews, FetchInterval: starterMinutes(30), Note: "商业与时事深度报道"},
	{URL: "https://feeds.a.dj.com/rss/RSSWorldNews.xml", Title: "WSJ World News", SiteURL: "https://www.wsj.com/world", Language: "en", Category: CategoryNews, FetchInterval: starterMinutes(30), Note: "英文国际新闻"},
	{URL: "https://feeds.npr.org/1004/rss.xml", Title: "NPR World", SiteURL: "https://www.npr.org/sections/world/", Language: "en", Category: CategoryNews, FetchInterval: starterMinutes(30), Note: "英文国际新闻"},
}

// StarterFeedsByCategory 返回按分类过滤后的目录。空 categories 等价于全部。
// 结果顺序与 StarterFeeds 一致，且不会返回重复 URL。
func StarterFeedsByCategory(categories ...string) []StarterFeed {
	want := map[string]bool{}
	for _, c := range categories {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			want[c] = true
		}
	}
	out := make([]StarterFeed, 0, len(StarterFeeds))
	seen := map[string]bool{}
	for _, f := range StarterFeeds {
		if len(want) > 0 && !want[strings.ToLower(f.Category)] {
			continue
		}
		key := normalizeFeedURL(f.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// StarterFeedByURL 按 URL 查目录项，用于导入时判定"这条是内置推荐源"。
func StarterFeedByURL(rawURL string) (StarterFeed, bool) {
	key := normalizeFeedURL(rawURL)
	if key == "" {
		return StarterFeed{}, false
	}
	for _, f := range StarterFeeds {
		if normalizeFeedURL(f.URL) == key {
			return f, true
		}
	}
	return StarterFeed{}, false
}

// normalizeFeedURL 只做导入幂等所需的归一化：去空白、去末尾斜杠、小写 host。
// 不做更激进的处理（例如剥 www.），因为那会让两个不同站点的 feed 撞在一起。
func normalizeFeedURL(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		// 查询串是 feed 身份的一部分（Nasdaq 的 category=Markets 等），
		// 但末尾无意义的斜杠要清掉。
		trimmed := strings.TrimRight(s[:i], "/")
		if trimmed == "" {
			return ""
		}
		return trimmed + s[i:]
	}
	return strings.TrimRight(s, "/")
}

// sortedCategories 是目录出现过的分类，按 StarterCategories 的顺序优先，
// 未知分类排在后面且按字母序，保证输出稳定。
func sortedCategories() []string {
	seen := map[string]bool{}
	for _, f := range StarterFeeds {
		if c := strings.ToLower(strings.TrimSpace(f.Category)); c != "" {
			seen[c] = true
		}
	}
	out := []string{}
	for _, c := range StarterCategories() {
		if seen[c] {
			out = append(out, c)
			delete(seen, c)
		}
	}
	rest := make([]string, 0, len(seen))
	for c := range seen {
		rest = append(rest, c)
	}
	sort.Strings(rest)
	return append(out, rest...)
}
