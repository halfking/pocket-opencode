// Package rss — digest.go
//
// 每日摘要：把一天内抓到的全部条目按分类（IT / 财经 / 时事）聚合成一份可读、
// 可分享的日报，并按天持久化，保证同一天重复打开看到的是同一份、且可以被
// 分享出去（用户要的是"每天收到一份全部信息的摘要"）。
package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DigestItem 是日报里的一条。
type DigestItem struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	SourceID    string     `json:"sourceId"`
	SourceTitle string     `json:"sourceTitle"`
	Category    string     `json:"category"`
	Language    string     `json:"language"`
	Summary     string     `json:"summary,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

// DigestSection 是按分类聚合的一节。
type DigestSection struct {
	Category string       `json:"category"`
	Label    string       `json:"label"`
	Items    []DigestItem `json:"items"`
}

// Digest 是一天的全部信息摘要。
type Digest struct {
	ID          string          `json:"id"`
	Date        string          `json:"date"`
	Headline    string          `json:"headline"`
	Body        string          `json:"body"`
	Sections    []DigestSection `json:"sections"`
	ItemCount   int             `json:"itemCount"`
	SourceCount int             `json:"sourceCount"`
	GeneratedAt time.Time       `json:"generatedAt"`
}

// DigestOptions 控制日报的取舍。
type DigestOptions struct {
	// MaxPerSection 每节最多放几条（<=0 用 DefaultDigestMaxPerSection）。
	MaxPerSection int
	// IncludeSummary 是否带上条目摘要（分享到微博/朋友圈时更需要）。
	IncludeSummary bool
	// MaxSummaryRunes 单条摘要的字符上限。
	MaxSummaryRunes int
	// Now 可注入，便于测试。
	Now func() time.Time
}

// DefaultDigestMaxPerSection 是一节默认的条数：一条推送里 4 节 × 8 条 = 32 条，
// 再多就不适合在手机上快速扫读。
const DefaultDigestMaxPerSection = 8

const (
	defaultDigestMaxSummaryRunes = 90
	digestDateLayout             = "2006-01-02"
)

func (o DigestOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o DigestOptions) maxPerSection() int {
	if o.MaxPerSection <= 0 {
		return DefaultDigestMaxPerSection
	}
	return o.MaxPerSection
}

func (o DigestOptions) maxSummaryRunes() int {
	if o.MaxSummaryRunes <= 0 {
		return defaultDigestMaxSummaryRunes
	}
	return o.MaxSummaryRunes
}

// DigestDayRange 返回 day 当天的 [start, end) 半开区间（UTC）。
// 半开区间是必须的：用 BETWEEN 会把次日 00:00:00 那一条算进前一天。
func DigestDayRange(day time.Time) (time.Time, time.Time) {
	d := time.Date(day.UTC().Year(), day.UTC().Month(), day.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return d, d.Add(24 * time.Hour)
}

// DigestDateOf 返回一个时间点所属的日报日期字符串。
func DigestDateOf(t time.Time) string {
	return t.UTC().Format(digestDateLayout)
}

// BuildDigest 汇总某一天的条目，生成日报（不落库）。
func (s *Store) BuildDigest(ctx context.Context, sc Scope, day time.Time, opt DigestOptions) (*Digest, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	start, end := DigestDayRange(day)
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.title, i.url, i.summary, i.published_at, i.fetched_at,
		       s.id, s.title, s.category, s.language
		FROM rss_items i
		JOIN rss_sources s ON s.id = i.source_id
		WHERE i.user_id = $1 AND i.workspace_id = $2
		  AND COALESCE(i.published_at, i.fetched_at) >= $3
		  AND COALESCE(i.published_at, i.fetched_at) <  $4
		ORDER BY COALESCE(i.published_at, i.fetched_at) DESC`, sc.UserID, sc.WorkspaceID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCategory := map[string][]DigestItem{}
	sources := map[string]bool{}
	total := 0
	for rows.Next() {
		var it DigestItem
		var publishedAt, fetchedAt *time.Time
		if err := rows.Scan(&it.ID, &it.Title, &it.URL, &it.Summary, &publishedAt, &fetchedAt, &it.SourceID, &it.SourceTitle, &it.Category, &it.Language); err != nil {
			return nil, err
		}
		it.PublishedAt = publishedAt
		if it.PublishedAt == nil {
			it.PublishedAt = fetchedAt
		}
		it.Category = normalizeCategory(it.Category)
		it.Summary = summarize(it.Summary, opt.maxSummaryRunes())
		if !opt.IncludeSummary {
			it.Summary = ""
		}
		byCategory[it.Category] = append(byCategory[it.Category], it)
		sources[it.SourceID] = true
		total++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sections := buildSections(byCategory, opt.maxPerSection())
	d := &Digest{
		Date:        DigestDateOf(start),
		Sections:    sections,
		ItemCount:   total,
		SourceCount: len(sources),
		GeneratedAt: opt.now(),
	}
	d.Headline = buildHeadline(d, start)
	d.Body = buildBody(d)
	return d, nil
}

// normalizeCategory 把历史/未知分类收敛到四个已知桶，避免日报里出现
// 一堆各写各的分类名。
func normalizeCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CategoryIT:
		return CategoryIT
	case CategoryFinance:
		return CategoryFinance
	case CategoryNews:
		return CategoryNews
	default:
		return CategoryOther
	}
}

func buildSections(byCategory map[string][]DigestItem, maxPer int) []DigestSection {
	order := []string{CategoryIT, CategoryFinance, CategoryNews, CategoryOther}
	// 未在 order 里的分类（理论上不会有，normalizeCategory 已收敛）排后面。
	rest := []string{}
	for c := range byCategory {
		if !containsStr(order, c) {
			rest = append(rest, c)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	out := make([]DigestSection, 0, len(order))
	for _, c := range order {
		items := byCategory[c]
		if len(items) == 0 {
			continue
		}
		if len(items) > maxPer {
			items = items[:maxPer]
		}
		out = append(out, DigestSection{Category: c, Label: StarterCategoryLabel(c), Items: items})
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func buildHeadline(d *Digest, day time.Time) string {
	if d.ItemCount == 0 {
		return fmt.Sprintf("%s 暂无新内容", day.Format("1月2日"))
	}
	parts := make([]string, 0, len(d.Sections))
	for _, sec := range d.Sections {
		parts = append(parts, fmt.Sprintf("%s %d", sec.Label, len(sec.Items)))
	}
	return fmt.Sprintf("%s 全部信息摘要：%d 条 · %s", day.Format("1月2日"), d.ItemCount, strings.Join(parts, " · "))
}

// buildBody 生成可直接分享出去的纯文本。
func buildBody(d *Digest) string {
	var b strings.Builder
	b.WriteString(d.Headline)
	b.WriteString("\n")
	for _, sec := range d.Sections {
		b.WriteString("\n【")
		b.WriteString(sec.Label)
		b.WriteString("】\n")
		for i, it := range sec.Items {
			b.WriteString(fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(it.Title)))
			if src := strings.TrimSpace(it.SourceTitle); src != "" {
				b.WriteString(" — ")
				b.WriteString(src)
			}
			b.WriteString("\n")
			if it.URL != "" {
				b.WriteString("   ")
				b.WriteString(it.URL)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func summarize(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// ===== 持久化 =====

// SaveDigest 按 (user, workspace, date) 幂等落库：同一天重复生成只会覆盖。
func (s *Store) SaveDigest(ctx context.Context, sc Scope, d *Digest) (*Digest, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	sections, err := json.Marshal(d.Sections)
	if err != nil {
		return nil, err
	}
	// 主键是 id，而 (user, workspace, date) 才唯一：id 必须每次生成新的，
	// 否则两个用户同一天的日报会在主键上撞车。
	d.ID = id("digest")
	row, err := scanDigest(s.pool.QueryRow(ctx, `
		INSERT INTO rss_digests(id,user_id,workspace_id,digest_date,headline,body,sections,item_count,generated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id,workspace_id,digest_date)
		DO UPDATE SET headline=EXCLUDED.headline, body=EXCLUDED.body, sections=EXCLUDED.sections,
		              item_count=EXCLUDED.item_count, generated_at=EXCLUDED.generated_at
		RETURNING id,user_id,workspace_id,digest_date,headline,body,sections,item_count,generated_at`,
		d.ID, sc.UserID, sc.WorkspaceID, d.Date, d.Headline, d.Body, sections, d.ItemCount, d.GeneratedAt))
	if err != nil {
		return nil, fmt.Errorf("rss: save digest: %w", err)
	}
	return row, nil
}

func scanDigest(r pgx.Row) (*Digest, error) {
	var d Digest
	var date time.Time
	var sections []byte
	if err := r.Scan(&d.ID, new(string), new(string), &date, &d.Headline, &d.Body, &sections, &d.ItemCount, &d.GeneratedAt); err != nil {
		return nil, err
	}
	d.Date = date.UTC().Format(digestDateLayout)
	if len(sections) > 0 {
		_ = json.Unmarshal(sections, &d.Sections)
	}
	d.SourceCount = countSources(d.Sections)
	return &d, nil
}

func countSources(sections []DigestSection) int {
	seen := map[string]bool{}
	for _, s := range sections {
		for _, it := range s.Items {
			if it.SourceID != "" {
				seen[it.SourceID] = true
			}
		}
	}
	return len(seen)
}

// GetDigest 取某天的已存日报；不存在返回 ErrNotFound。
func (s *Store) GetDigest(ctx context.Context, sc Scope, date string) (*Digest, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	d, err := scanDigest(s.pool.QueryRow(ctx, `
		SELECT id,user_id,workspace_id,digest_date,headline,body,sections,item_count,generated_at
		FROM rss_digests WHERE user_id=$1 AND workspace_id=$2 AND digest_date=$3`, sc.UserID, sc.WorkspaceID, date))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// DigestListItem 是历史日报列表的一项。
type DigestListItem struct {
	Date      string    `json:"date"`
	Headline  string    `json:"headline"`
	ItemCount int       `json:"itemCount"`
	Generated time.Time `json:"generatedAt"`
}

// ListDigests 返回最近的日报列表（新的一天在前）。
func (s *Store) ListDigests(ctx context.Context, sc Scope, limit int) ([]DigestListItem, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT digest_date, headline, item_count, generated_at
		FROM rss_digests WHERE user_id=$1 AND workspace_id=$2
		ORDER BY digest_date DESC LIMIT $3`, sc.UserID, sc.WorkspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DigestListItem{}
	for rows.Next() {
		var it DigestListItem
		var date time.Time
		if err := rows.Scan(&date, &it.Headline, &it.ItemCount, &it.Generated); err != nil {
			return nil, err
		}
		it.Date = date.UTC().Format(digestDateLayout)
		out = append(out, it)
	}
	return out, rows.Err()
}
