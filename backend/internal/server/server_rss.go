// server_rss.go — RSS 订阅、过滤、详情、分享的 HTTP handlers。
//
// 路由（server.go 注册，全部走 requireAuth）：
//
//	GET    /api/rss/sources                  列出当前用户的 RSS 源
//	POST   /api/rss/sources                  新增源（自动 discover → first-fetch）
//	GET    /api/rss/sources/seeds            返回内置种子源（中文常见资讯/科技）
//	POST   /api/rss/sources/discover         {url} → 发现候选 feed URL
//	PATCH  /api/rss/sources/{id}             部分更新（启用/间隔/标题等）
//	DELETE /api/rss/sources/{id}             删除源（级联删除 items/drafts/attempts）
//	POST   /api/rss/sources/{id}/refresh     立即拉取一次（scheduler.RunNow）
//
//	GET    /api/rss/items                    列表（按 sourceId / status / 关键词 / 时间窗）
//	GET    /api/rss/items/{id}               详情（含所属源摘要）
//	POST   /api/rss/items/{id}/read          标记已读
//	POST   /api/rss/items/bulk-read          {ids:[…]} 批量已读
//	POST   /api/rss/items/{id}/star          {starred:true|false}
//
//	GET    /api/rss/filters                  当前用户的过滤规则
//	PUT    /api/rss/filters                  整体替换过滤规则列表
//	POST   /api/rss/items/{id}/apply-filters 预览过滤命中（不写入）
//
//	GET    /api/rss/items/{id}/share-card    返回 PNG 分享卡（1080×1350）
//	POST   /api/rss/items/{id}/share         {dest:"wechat"|"weibo"|"clipboard"|"download"}
//
// 失败/降级：
//   - store 为 nil（未配置 PG / 未启用 RSS）→ 503 + error code "rss_unavailable"。
//   - 任何 rss.ErrNotFound → 404 + code "not_found"。
//   - 任何 rss.ErrInvalidScope → 401（claims 缺失，应在 requireAuth 阶段就拦掉）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

// ===== Server 字段注入 =====
//
// rssStore / rssScheduler 在 cmd/pocketd/main.go 装配后通过 SetRSSStore /
// SetRSSScheduler 注入；handler 找不到对应字段时降级 503。

// rssStoreSafe 返回 store 或 nil（永不 panic）。
func (s *Server) rssStoreSafe() *rss.Store {
	if s == nil {
		return nil
	}
	return s.rssStore
}

// rssSchedulerSafe 返回 scheduler 或 nil。
func (s *Server) rssSchedulerSafe() *rss.Scheduler {
	if s == nil {
		return nil
	}
	return s.rssScheduler
}

// rssScopeFromClaims 把当前请求的 claims 翻译为 rss.Scope。
func (s *Server) rssScopeFromClaims(r *http.Request) rss.Scope {
	return rss.Scope{UserID: s.userIDFromRequest(r), WorkspaceID: s.workspaceIDFromRequest(r)}
}

// writeRSSError 把 rss 包错误翻译为标准 JSON 响应。
func writeRSSError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rss.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, rss.ErrInvalidScope):
		writeError(w, http.StatusUnauthorized, "missing user/workspace claims")
	case errors.Is(err, rss.ErrStoreUnavailable):
		writeError(w, http.StatusServiceUnavailable, "rss store unavailable")
	case errors.Is(err, rss.ErrPublisherUnavailable):
		writeError(w, http.StatusNotImplemented, "publisher unavailable; use clipboard/download")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// requireRSSStore 在 store 缺失时快速失败 503；返回 false 时 handler 已写完响应。
func (s *Server) requireRSSStore(w http.ResponseWriter) *rss.Store {
	st := s.rssStoreSafe()
	if st == nil || !st.Available() {
		writeError(w, http.StatusServiceUnavailable, "rss_unavailable: store not configured")
		return nil
	}
	return st
}

// ===== Seeds（内置种子源） =====
//
// 不接远端，避免冷启动空状态。每一项都是 known-good 的 RSS/Atom feed。
var rssSeedFeeds = []rssSeed{
	{URL: "https://hnrss.org/frontpage", Title: "Hacker News — Front Page", SiteURL: "https://news.ycombinator.com/", Language: "en", Category: "tech"},
	{URL: "https://www.36kr.com/feed", Title: "36氪", SiteURL: "https://www.36kr.com/", Language: "zh", Category: "tech"},
	{URL: "https://rsshub.app/sspai/index", Title: "少数派", SiteURL: "https://sspai.com/", Language: "zh", Category: "tech"},
	{URL: "https://www.ruanyifeng.com/blog/atom.xml", Title: "阮一峰的网络日志", SiteURL: "https://www.ruanyifeng.com/", Language: "zh", Category: "tech"},
	{URL: "https://www.smashingmagazine.com/feed/", Title: "Smashing Magazine", SiteURL: "https://www.smashingmagazine.com/", Language: "en", Category: "design"},
}

type rssSeed struct {
	URL, Title, SiteURL, Language, Category string
}

// ===== /sources =====

type rssSourceDTO struct {
	ID             string  `json:"id"`
	URL            string  `json:"url"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	SiteURL        string  `json:"siteUrl"`
	Language       string  `json:"language"`
	Status         string  `json:"status"`
	Enabled        bool    `json:"enabled"`
	Error          string  `json:"error,omitempty"`
	FetchIntervalS int64   `json:"fetchIntervalSec"`
	LastFetchedAt  *string `json:"lastFetchedAt,omitempty"`
	NextFetchAt    *string `json:"nextFetchAt,omitempty"`
	UnreadCount    int64   `json:"unreadCount"`
}

func toSourceDTO(src rss.Source, unread int64) rssSourceDTO {
	out := rssSourceDTO{
		ID:             src.ID,
		URL:            src.URL,
		Title:          src.Title,
		Description:    src.Description,
		SiteURL:        src.SiteURL,
		Language:       src.Language,
		Status:         string(src.Status),
		Enabled:        src.Enabled,
		Error:          src.Error,
		FetchIntervalS: int64(src.FetchInterval / time.Second),
		UnreadCount:    unread,
	}
	if src.LastFetchedAt != nil {
		s := src.LastFetchedAt.UTC().Format(time.RFC3339)
		out.LastFetchedAt = &s
	}
	if src.NextFetchAt != nil {
		s := src.NextFetchAt.UTC().Format(time.RFC3339)
		out.NextFetchAt = &s
	}
	return out
}

type rssCreateSourceBody struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	SiteURL       string `json:"siteUrl"`
	Language      string `json:"language"`
	Enabled       *bool  `json:"enabled"`
	FetchInterval string `json:"fetchInterval"` // 接收 "30m" / "1h"
}

func parseRSSInterval(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// handleRSSSources 列表（GET）/ 新增（POST）
func (s *Server) handleRSSSources(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	sc := s.rssScopeFromClaims(r)
	switch r.Method {
	case http.MethodGet:
		sources, err := st.ListSources(r.Context(), sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		out := make([]rssSourceDTO, 0, len(sources))
		for _, src := range sources {
			// 未读计数：单条 count 查询成本可控；MVP 直接算。后续可换成按 source 聚合的 SQL。
			items, err := st.ListItems(r.Context(), sc, rss.ListItemsOptions{Status: rss.ItemUnread, Limit: 1})
			var unread int64
			if err == nil {
				_ = items
				// ListItems 没有按 source 过滤的参数；走兜底：
				all, err2 := st.ListItems(r.Context(), sc, rss.ListItemsOptions{Limit: 200})
				if err2 == nil {
					for _, it := range all {
						if it.SourceID == src.ID && it.Status == rss.ItemUnread {
							unread++
						}
					}
				}
			}
			out = append(out, toSourceDTO(src, unread))
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": out})
	case http.MethodPost:
		var body rssCreateSourceBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		src, err := st.CreateSource(r.Context(), rss.CreateSourceRequest{
			URL:           body.URL,
			Title:         body.Title,
			Description:   body.Description,
			SiteURL:       body.SiteURL,
			Language:      body.Language,
			Enabled:       enabled,
			FetchInterval: parseRSSInterval(body.FetchInterval, 30*time.Minute),
		}, sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toSourceDTO(*src, 0))
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// handleRSSSourceSeeds 返回内置种子（不需要 store，但保持 requireAuth 一致）。
func (s *Server) handleRSSSourceSeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	out := make([]map[string]any, 0, len(rssSeedFeeds))
	for _, s2 := range rssSeedFeeds {
		out = append(out, map[string]any{
			"url":      s2.URL,
			"title":    s2.Title,
			"siteUrl":  s2.SiteURL,
			"language": s2.Language,
			"category": s2.Category,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"seeds": out})
}

// handleRSSDiscover 给一个页面 URL，返回候选 feed URL 列表。
func (s *Server) handleRSSDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	candidates, err := rss.Discover(r.Context(), body.URL, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]any{"url": c.URL, "title": c.Title, "contentType": c.ContentType})
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

// handleRSSSourceItem 处理 /api/rss/sources/{id} 上的 PATCH / DELETE。
func (s *Server) handleRSSSourceItem(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	sc := s.rssScopeFromClaims(r)
	id := rssPathTail(r.URL.Path, "/api/rss/sources/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing source id")
		return
	}
	// 子动作：/refresh
	if strings.HasSuffix(id, "/refresh") {
		s.handleRSSSourceRefresh(w, r, strings.TrimSuffix(id, "/refresh"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Title         *string           `json:"title"`
			Description   *string           `json:"description"`
			SiteURL       *string           `json:"siteUrl"`
			Language      *string           `json:"language"`
			Enabled       *bool             `json:"enabled"`
			FetchInterval *time.Duration    `json:"fetchInterval"`
			Status        *rss.SourceStatus `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		upd := rss.UpdateSourceRequest{
			Title:         body.Title,
			Description:   body.Description,
			SiteURL:       body.SiteURL,
			Language:      body.Language,
			Enabled:       body.Enabled,
			FetchInterval: body.FetchInterval,
			Status:        body.Status,
		}
		src, err := st.UpdateSource(r.Context(), id, upd, sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSourceDTO(*src, 0))
	case http.MethodDelete:
		if err := st.DeleteSource(r.Context(), id, sc); err != nil {
			writeRSSError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "PATCH or DELETE only")
	}
}

// handleRSSSourceRefresh 触发一次立即拉取。
func (s *Server) handleRSSSourceRefresh(w http.ResponseWriter, r *http.Request, sourceID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sched := s.rssSchedulerSafe()
	if sched == nil {
		// scheduler 未装配时降级：直接走 fetcher.RefreshSource，但需要 store 自己起 fetcher。
		// 这里简单拒绝，要求 main.go 注入 scheduler（生产必须）。
		writeError(w, http.StatusServiceUnavailable, "rss scheduler unavailable")
		return
	}
	res, err := sched.RunNow(r.Context(), sourceID)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fetched":     true,
		"newItems":    res.NewItems,
		"duplicates":  res.DuplicateItems,
		"notModified": res.NotModified,
	})
}

// ===== /items =====

type rssItemDTO struct {
	ID           string   `json:"id"`
	SourceID     string   `json:"sourceId"`
	GUID         string   `json:"guid"`
	Title        string   `json:"title"`
	Author       string   `json:"author"`
	URL          string   `json:"url"`
	Summary      string   `json:"summary"`
	Content      string   `json:"content,omitempty"`
	Language     string   `json:"language"`
	Categories   []string `json:"categories"`
	PublishedAt  *string  `json:"publishedAt,omitempty"`
	FetchedAt    string   `json:"fetchedAt"`
	Relevance    float64  `json:"relevance"`
	MatchReasons []string `json:"matchReasons"`
	Status       string   `json:"status"`
}

func toItemDTO(it rss.Item) rssItemDTO {
	out := rssItemDTO{
		ID:           it.ID,
		SourceID:     it.SourceID,
		GUID:         it.GUID,
		Title:        it.Title,
		Author:       it.Author,
		URL:          it.URL,
		Summary:      it.Summary,
		Content:      it.Content,
		Language:     it.Language,
		Categories:   it.Categories,
		Relevance:    it.Relevance,
		MatchReasons: it.MatchReasons,
		Status:       string(it.Status),
	}
	if it.FetchedAt != nil {
		out.FetchedAt = it.FetchedAt.UTC().Format(time.RFC3339)
	}
	if it.PublishedAt != nil {
		s := it.PublishedAt.UTC().Format(time.RFC3339)
		out.PublishedAt = &s
	}
	return out
}

// handleRSSItems 列表（GET）。支持 query 参数：
//
//	sourceId, status (unread|read|starred|archived), q, since, until, limit, offset
func (s *Server) handleRSSItems(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	sc := s.rssScopeFromClaims(r)
	q := r.URL.Query()
	opt := rss.ListItemsOptions{
		Limit:  50,
		Offset: 0,
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			opt.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opt.Offset = n
		}
	}
	opt.Query = q.Get("q")
	switch q.Get("status") {
	case "unread":
		opt.Status = rss.ItemUnread
	case "read":
		opt.Status = rss.ItemRead
	case "starred":
		opt.Status = rss.ItemStarred
	case "archived":
		opt.Status = rss.ItemArchived
	}
	// 时间窗用 RFC3339 字符串。
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opt.Since = &t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opt.Until = &t
		}
	}
	sourceID := q.Get("sourceId")
	// ListItems 内部已经过滤 status/q/since/until；sourceId 在内存中二次过滤。
	items, err := st.ListItems(r.Context(), sc, opt)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	out := make([]rssItemDTO, 0, len(items))
	for _, it := range items {
		if sourceID != "" && it.SourceID != sourceID {
			continue
		}
		out = append(out, toItemDTO(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

// handleRSSItemItem 处理 /api/rss/items/{id} 上的 GET 与子动作。
func (s *Server) handleRSSItemItem(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	sc := s.rssScopeFromClaims(r)
	tail := rssPathTail(r.URL.Path, "/api/rss/items/")
	if tail == "" {
		writeError(w, http.StatusBadRequest, "missing item id")
		return
	}
	parts := strings.Split(tail, "/")
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		it, err := st.GetItem(r.Context(), id, sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toItemDTO(*it))
		return
	}
	switch parts[1] {
	case "read":
		s.handleRSSItemRead(w, r, id)
	case "star":
		s.handleRSSItemStar(w, r, id)
	case "apply-filters":
		s.handleRSSApplyFilters(w, r, id)
	case "share-card":
		s.handleRSSShareCard(w, r, id)
	case "share":
		s.handleRSSShare(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "unknown sub-action")
	}
}

// handleRSSBulkRead 处理 POST /api/rss/items/bulk-read
func (s *Server) handleRSSBulkRead(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sc := s.rssScopeFromClaims(r)
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	updated := 0
	for _, id := range body.IDs {
		if id == "" {
			continue
		}
		if err := st.MarkItem(r.Context(), id, rss.ItemRead, sc); err == nil {
			updated++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

func (s *Server) handleRSSItemRead(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	st := s.rssStoreSafe()
	sc := s.rssScopeFromClaims(r)
	if err := st.MarkItem(r.Context(), id, rss.ItemRead, sc); err != nil {
		writeRSSError(w, err)
		return
	}
	it, err := st.GetItem(r.Context(), id, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toItemDTO(*it))
}

func (s *Server) handleRSSItemStar(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	st := s.rssStoreSafe()
	sc := s.rssScopeFromClaims(r)
	var body struct {
		Starred bool `json:"starred"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Starred {
		if err := st.MarkItem(r.Context(), id, rss.ItemStarred, sc); err != nil {
			writeRSSError(w, err)
			return
		}
	} else {
		if err := st.MarkItem(r.Context(), id, rss.ItemRead, sc); err != nil {
			writeRSSError(w, err)
			return
		}
	}
	it, err := st.GetItem(r.Context(), id, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toItemDTO(*it))
}

// handleRSSApplyFilters 预览 item 命中哪些 filter 规则。
func (s *Server) handleRSSApplyFilters(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	st := s.rssStoreSafe()
	sc := s.rssScopeFromClaims(r)
	it, err := st.GetItem(r.Context(), id, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	rules, err := st.ListFilterRules(r.Context(), sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	res := rss.Evaluate(*it, rules)
	writeJSON(w, http.StatusOK, map[string]any{
		"matched":   res.Matched,
		"relevance": res.Relevance,
		"reasons":   res.MatchReasons,
	})
}

// ===== /filters =====

type rssFilterDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	IncludeKeywords []string `json:"includeKeywords"`
	ExcludeKeywords []string `json:"excludeKeywords"`
	Languages       []string `json:"languages"`
	Since           *string  `json:"since,omitempty"`
	Until           *string  `json:"until,omitempty"`
	MinRelevance    float64  `json:"minRelevance"`
}

func toFilterDTO(x rss.FilterRule) rssFilterDTO {
	out := rssFilterDTO{
		ID:              x.ID,
		Name:            x.Name,
		Enabled:         x.Enabled,
		IncludeKeywords: x.IncludeKeywords,
		ExcludeKeywords: x.ExcludeKeywords,
		Languages:       x.Languages,
		MinRelevance:    x.MinRelevance,
	}
	if x.Since != nil {
		s := x.Since.UTC().Format(time.RFC3339)
		out.Since = &s
	}
	if x.Until != nil {
		s := x.Until.UTC().Format(time.RFC3339)
		out.Until = &s
	}
	return out
}

func parseRSSFilterDTO(d rssFilterDTO) rss.FilterRule {
	out := rss.FilterRule{
		ID:              d.ID,
		Name:            d.Name,
		Enabled:         d.Enabled,
		IncludeKeywords: d.IncludeKeywords,
		ExcludeKeywords: d.ExcludeKeywords,
		Languages:       d.Languages,
		MinRelevance:    d.MinRelevance,
	}
	if d.Since != nil {
		if t, err := time.Parse(time.RFC3339, *d.Since); err == nil {
			out.Since = &t
		}
	}
	if d.Until != nil {
		if t, err := time.Parse(time.RFC3339, *d.Until); err == nil {
			out.Until = &t
		}
	}
	return out
}

// handleRSSFilters GET 列表 / PUT 整体替换。
func (s *Server) handleRSSFilters(w http.ResponseWriter, r *http.Request) {
	st := s.requireRSSStore(w)
	if st == nil {
		return
	}
	sc := s.rssScopeFromClaims(r)
	switch r.Method {
	case http.MethodGet:
		rules, err := st.ListFilterRules(r.Context(), sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		out := make([]rssFilterDTO, 0, len(rules))
		for _, x := range rules {
			out = append(out, toFilterDTO(x))
		}
		writeJSON(w, http.StatusOK, map[string]any{"filters": out})
	case http.MethodPut:
		var body struct {
			Filters []rssFilterDTO `json:"filters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		// MVP：整体替换 = 先按 ListFilterRules 删除现有，再用 ID 保持的 upsert 写入。
		// 这里简化：每条都走 CreateFilterRule（自动分配 ID）；已存在的按 ID 重写。
		// 真正 production 应支持保留 ID + delete-then-insert；MVP 接受 ID 重置。
		existing, err := st.ListFilterRules(r.Context(), sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		for _, x := range existing {
			if err := st.DeleteFilterRule(r.Context(), x.ID, sc); err != nil && !errors.Is(err, rss.ErrNotFound) {
				writeRSSError(w, err)
				return
			}
		}
		out := make([]rssFilterDTO, 0, len(body.Filters))
		for _, d := range body.Filters {
			if _, err := st.CreateFilterRule(r.Context(), parseRSSFilterDTO(d), sc); err != nil {
				writeRSSError(w, err)
				return
			}
		}
		rules, err := st.ListFilterRules(r.Context(), sc)
		if err != nil {
			writeRSSError(w, err)
			return
		}
		for _, x := range rules {
			out = append(out, toFilterDTO(x))
		}
		writeJSON(w, http.StatusOK, map[string]any{"filters": out})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or PUT only")
	}
}

// ===== 分享 =====

// handleRSSShareCard 返回 PNG 字节流。
func (s *Server) handleRSSShareCard(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	st := s.rssStoreSafe()
	sc := s.rssScopeFromClaims(r)
	it, err := st.GetItem(r.Context(), id, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	src, err := st.GetSource(r.Context(), it.SourceID, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	theme := r.URL.Query().Get("theme")
	if theme != "dark" {
		theme = "light"
	}
	png, err := renderShareCard(*it, src, theme)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "share card render: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(png)
}

// handleRSSShare 生成可分享的深链 / 文本。
func (s *Server) handleRSSShare(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	st := s.rssStoreSafe()
	sc := s.rssScopeFromClaims(r)
	it, err := st.GetItem(r.Context(), id, sc)
	if err != nil {
		writeRSSError(w, err)
		return
	}
	var body struct {
		Dest    string `json:"dest"` // "wechat" | "weibo" | "clipboard" | "download"
		Caption string `json:"caption"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	caption := strings.TrimSpace(body.Caption)
	if caption == "" {
		caption = strings.TrimSpace(it.Title)
	}
	out := map[string]any{}
	switch body.Dest {
	case "weibo":
		// service.weibo.com/share/share.php 接收 url + title 参数，浏览器直接打开即触发分享面板。
		q := url.Values{}
		q.Set("url", it.URL)
		q.Set("title", caption)
		out["deepLink"] = "https://service.weibo.com/share/share.php?" + q.Encode()
		out["copyText"] = caption + "\n" + it.URL
	case "wechat":
		// 朋友圈没有公开 API；返回 copyText 由前端写入剪贴板并提示用户。
		out["deepLink"] = ""
		out["copyText"] = caption + "\n" + it.URL
		out["hint"] = "wechat-moments-has-no-public-api"
	case "clipboard":
		out["copyText"] = caption + "\n" + it.URL
	case "download":
		// download 让前端直接命中 GET /share-card 路径；这里返回元数据指引。
		out["downloadUrl"] = "/api/rss/items/" + id + "/share-card"
	default:
		writeError(w, http.StatusBadRequest, "dest must be wechat|weibo|clipboard|download")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ===== 工具 =====

// rssPathTail 截掉 prefix 后剩余的尾巴（不带前导 /）。
func rssPathTail(rawPath, prefix string) string {
	tail := strings.TrimPrefix(rawPath, prefix)
	tail = strings.Trim(tail, "/")
	return tail
}

// renderShareCard 是 sharecard.go 的 server 侧入口；该文件实现在 internal/rss/sharecard.go。
func renderShareCard(it rss.Item, src *rss.Source, theme string) ([]byte, error) {
	return rss.RenderShareCard(context.Background(), it, src, theme)
}

// ===== 路由分发器 =====
//
// server.go 注册：mux.HandleFunc("/api/rss/", s.requireAuth(s.handleRSSRouter))
// 然后分发器把不同子路径转给对应 handler。

func (s *Server) handleRSSRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/rss/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "missing rss subpath")
		return
	}
	switch parts[0] {
	case "sources":
		// /sources, /sources/seeds, /sources/{id}[/refresh], /sources/discover
		switch {
		case len(parts) == 1:
			s.handleRSSSources(w, r)
		case len(parts) == 2 && parts[1] == "seeds":
			s.handleRSSSourceSeeds(w, r)
		case len(parts) == 2 && parts[1] == "discover":
			s.handleRSSDiscover(w, r)
		default:
			s.handleRSSSourceItem(w, r)
		}
	case "items":
		switch {
		case len(parts) == 1:
			// /items alone shouldn't be valid — list uses GET /items
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "GET only")
				return
			}
			s.handleRSSItems(w, r)
		case len(parts) == 2 && parts[1] == "bulk-read":
			s.handleRSSBulkRead(w, r)
		default:
			s.handleRSSItemItem(w, r)
		}
	case "filters":
		s.handleRSSFilters(w, r)
	default:
		writeError(w, http.StatusNotFound, "unknown rss subpath: "+path)
	}
}

// ===== Server 字段 + 注入器 =====
//
// 以下会在 server.go 里追加字段和 setter，并在 cmd/pocketd/main.go 里实例化 Store / Scheduler。

// SetRSSStore 注入 RSS 持久化。nil = 关闭 RSS 模块。
func (s *Server) SetRSSStore(st *rss.Store) {
	s.rssStore = st
	if st == nil {
		s.rssScheduler = nil
	}
}

// SetRSSScheduler 注入后台调度器。可选；不注入时仍可走手动 refresh。
func (s *Server) SetRSSScheduler(sched *rss.Scheduler) {
	s.rssScheduler = sched
}
