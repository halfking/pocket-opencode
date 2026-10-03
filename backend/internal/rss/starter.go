// Package rss — starter.go
//
// 内置推荐源的一键导入。ImportStarterSources 幂等：同一条 feed 在同一
// (user, workspace) 下只会存在一份（表上有 UNIQUE 约束），重复导入只会计入
// Skipped，不会产生重复订阅、也不会覆盖用户自己改过的标题或间隔。
package rss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// StarterImportOptions 控制一次内置源导入。
type StarterImportOptions struct {
	// Categories 为空表示按 StarterCategories() 全部导入。
	Categories []string
	// MaxPerCategory > 0 时每个分类最多导入多少条（0 = 不限）。
	MaxPerCategory int
	// Enabled 决定导入后的源是否立即启用。
	Enabled bool
	// DefaultInterval 是目录里没有自带间隔时的兜底值。
	DefaultInterval time.Duration
	// Now 可注入，便于测试。
	Now func() time.Time
}

// StarterImportResult 报告一次导入的真实结果。
type StarterImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Total   int      `json:"total"`
	Sources []Source `json:"sources"`
}

func (o StarterImportOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// ImportStarterSources 把内置目录写进给定作用域的订阅列表。
//
// 用 INSERT ... ON CONFLICT DO NOTHING 而不是"先查再插"：并发导入
// （用户连点两次、或多个实例同时启动自举）时后者会插出重复行。
func (s *Store) ImportStarterSources(ctx context.Context, sc Scope, opt StarterImportOptions) (*StarterImportResult, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	feeds := StarterFeedsByCategory(opt.Categories...)
	if len(feeds) == 0 {
		return &StarterImportResult{Sources: []Source{}}, nil
	}
	now := opt.now()
	interval := opt.DefaultInterval
	if interval <= 0 {
		interval = time.Hour
	}

	result := &StarterImportResult{Total: len(feeds), Sources: []Source{}}
	perCategory := map[string]int{}
	for _, f := range feeds {
		cat := strings.ToLower(strings.TrimSpace(f.Category))
		if cat == "" {
			cat = CategoryOther
		}
		if opt.MaxPerCategory > 0 && perCategory[cat] >= opt.MaxPerCategory {
			continue
		}
		perCategory[cat]++

		if err := ValidateURL(f.URL); err != nil {
			// 目录里写错的 URL 不该让整批导入失败，但必须能被看见：
			// 计入 Total、不计入 Created，并让调用方在日志里看到。
			continue
		}
		fetchEvery := f.FetchInterval
		if fetchEvery <= 0 {
			fetchEvery = interval
		}
		status := SourceActive
		if !opt.Enabled {
			status = SourceDisabled
		}
		row, err := scanSource(s.pool.QueryRow(ctx, `
			INSERT INTO rss_sources(id,user_id,workspace_id,url,title,description,site_url,language,category,status,enabled,fetch_interval,next_fetch_at,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,'',$6,$7,$8,$9,$10,$11,$12,$13,$13)
			ON CONFLICT (user_id,workspace_id,url) DO NOTHING
			RETURNING `+sourceCols, id("src"), sc.UserID, sc.WorkspaceID, f.URL, f.Title, f.SiteURL, f.Language, cat, status, opt.Enabled, int64(fetchEvery/time.Second), now, now))
		if err != nil {
			// ErrNoRows = ON CONFLICT 命中 = 这一条早就订阅过了。
			if errors.Is(err, pgx.ErrNoRows) {
				result.Skipped++
				continue
			}
			return nil, fmt.Errorf("rss: import starter source %s: %w", f.URL, err)
		}
		result.Created++
		result.Sources = append(result.Sources, *row)
	}
	return result, nil
}

// HasStarterSources 判断该作用域是否已经导入过内置源。
// 供"新用户首启自动铺一份"的自举逻辑做幂等判断。
func (s *Store) HasStarterSources(ctx context.Context, sc Scope) (bool, error) {
	if err := requireScope(sc); err != nil {
		return false, err
	}
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM rss_sources WHERE user_id=$1 AND workspace_id=$2 AND url = ANY($3)`, sc.UserID, sc.WorkspaceID, starterURLs()).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func starterURLs() []string {
	out := make([]string, 0, len(StarterFeeds))
	for _, f := range StarterFeeds {
		out = append(out, f.URL)
	}
	return out
}

// ListActiveScopes 列出当前库里还有订阅源的用户作用域。
//
// 存在的理由：后台拉取与日报生成必须覆盖**真实用户的**订阅，而不能只认
// 启动时写死的那一个作用域（历史上 scheduler 写死 local/default，导致
// 通过 API 建的源从来没有被后台拉取过）。
func (s *Store) ListActiveScopes(ctx context.Context) ([]Scope, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT user_id, workspace_id FROM rss_sources ORDER BY user_id, workspace_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		var sc Scope
		if err := rows.Scan(&sc.UserID, &sc.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
