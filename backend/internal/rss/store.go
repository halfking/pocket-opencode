package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrStoreUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("rss migration: %w", err)
	}
	return s, nil
}
func (s *Store) Available() bool { return s != nil && s.pool != nil }
func requireScope(sc Scope) error {
	if !sc.valid() {
		return ErrInvalidScope
	}
	return nil
}
func id(prefix string) string { return prefix + "_" + uuid.NewString() }
func j(v any) []byte          { b, _ := json.Marshal(v); return b }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS rss_sources (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
 url TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', site_url TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '',
 etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', enabled BOOLEAN NOT NULL DEFAULT TRUE, error TEXT NOT NULL DEFAULT '',
 fetch_interval BIGINT NOT NULL DEFAULT 3600, next_fetch_at TIMESTAMPTZ, last_fetched_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(user_id,workspace_id,url));
CREATE INDEX IF NOT EXISTS idx_rss_sources_due ON rss_sources(user_id,workspace_id,enabled,next_fetch_at);
CREATE TABLE IF NOT EXISTS rss_items (
 id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES rss_sources(id) ON DELETE CASCADE, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
 guid TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', categories JSONB NOT NULL DEFAULT '[]'::jsonb,
 published_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), relevance DOUBLE PRECISION NOT NULL DEFAULT 0, match_reasons JSONB NOT NULL DEFAULT '[]'::jsonb, status TEXT NOT NULL DEFAULT 'unread',
 UNIQUE(source_id,hash));
CREATE INDEX IF NOT EXISTS idx_rss_items_scope_date ON rss_items(user_id,workspace_id,published_at DESC);
CREATE INDEX IF NOT EXISTS idx_rss_items_source ON rss_items(source_id,published_at DESC);
CREATE TABLE IF NOT EXISTS rss_filter_rules (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,name TEXT NOT NULL DEFAULT '',enabled BOOLEAN NOT NULL DEFAULT TRUE,
 include_keywords JSONB NOT NULL DEFAULT '[]'::jsonb,exclude_keywords JSONB NOT NULL DEFAULT '[]'::jsonb,languages JSONB NOT NULL DEFAULT '[]'::jsonb,since_at TIMESTAMPTZ,until_at TIMESTAMPTZ,min_relevance DOUBLE PRECISION NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE INDEX IF NOT EXISTS idx_rss_rules_scope ON rss_filter_rules(user_id,workspace_id,enabled);
CREATE TABLE IF NOT EXISTS rss_drafts (
 id TEXT PRIMARY KEY,item_id TEXT NOT NULL REFERENCES rss_items(id) ON DELETE CASCADE,user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,text TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),UNIQUE(user_id,workspace_id,item_id));
CREATE INDEX IF NOT EXISTS idx_rss_drafts_scope ON rss_drafts(user_id,workspace_id,updated_at DESC);
CREATE TABLE IF NOT EXISTS rss_publish_attempts (
 id TEXT PRIMARY KEY,draft_id TEXT NOT NULL REFERENCES rss_drafts(id) ON DELETE CASCADE,item_id TEXT NOT NULL DEFAULT '',user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,platform TEXT NOT NULL,remote_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',error TEXT NOT NULL DEFAULT '',attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE INDEX IF NOT EXISTS idx_rss_attempts_scope ON rss_publish_attempts(user_id,workspace_id,attempted_at DESC);
`)
	return err
}

func (s *Store) CreateSource(ctx context.Context, req CreateSourceRequest, sc Scope) (*Source, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if err := ValidateURL(req.URL); err != nil {
		return nil, err
	}
	if req.FetchInterval <= 0 {
		req.FetchInterval = time.Hour
	}
	now := time.Now().UTC()
	x := &Source{ID: id("src"), UserID: sc.UserID, WorkspaceID: sc.WorkspaceID, URL: strings.TrimSpace(req.URL), Title: req.Title, Description: req.Description, SiteURL: req.SiteURL, Language: req.Language, Enabled: req.Enabled, Status: SourceActive, FetchInterval: req.FetchInterval, CreatedAt: now, UpdatedAt: now}
	if !req.Enabled {
		x.Status = SourceDisabled
	}
	x.NextFetchAt = &now
	_, err := s.pool.Exec(ctx, `INSERT INTO rss_sources(id,user_id,workspace_id,url,title,description,site_url,language,status,enabled,fetch_interval,next_fetch_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`, x.ID, x.UserID, x.WorkspaceID, x.URL, x.Title, x.Description, x.SiteURL, x.Language, x.Status, x.Enabled, int64(x.FetchInterval/time.Second), x.NextFetchAt, x.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create source: %w", err)
	}
	return x, nil
}

const sourceCols = `id,user_id,workspace_id,url,title,description,site_url,language,etag,last_modified,status,enabled,error,fetch_interval,next_fetch_at,last_fetched_at,created_at,updated_at`

func scanSource(r pgx.Row) (*Source, error) {
	var x Source
	var sec int64
	err := r.Scan(&x.ID, &x.UserID, &x.WorkspaceID, &x.URL, &x.Title, &x.Description, &x.SiteURL, &x.Language, &x.ETag, &x.LastModified, &x.Status, &x.Enabled, &x.Error, &sec, &x.NextFetchAt, &x.LastFetchedAt, &x.CreatedAt, &x.UpdatedAt)
	x.FetchInterval = time.Duration(sec) * time.Second
	return &x, err
}
func (s *Store) GetSource(ctx context.Context, id string, sc Scope) (*Source, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x, e := scanSource(s.pool.QueryRow(ctx, `SELECT `+sourceCols+` FROM rss_sources WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) ListSources(ctx context.Context, sc Scope) ([]Source, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	rows, e := s.pool.Query(ctx, `SELECT `+sourceCols+` FROM rss_sources WHERE user_id=$1 AND workspace_id=$2 ORDER BY created_at DESC`, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		x, e := scanSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSource(ctx context.Context, id string, req UpdateSourceRequest, sc Scope) (*Source, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if req.URL != nil {
		if err := ValidateURL(*req.URL); err != nil {
			return nil, err
		}
	}
	_, e := s.pool.Exec(ctx, `UPDATE rss_sources SET url=COALESCE($1,url),title=COALESCE($2,title),description=COALESCE($3,description),site_url=COALESCE($4,site_url),language=COALESCE($5,language),enabled=COALESCE($6,enabled),fetch_interval=COALESCE($7,fetch_interval),status=COALESCE($8,status),updated_at=NOW() WHERE id=$9 AND user_id=$10 AND workspace_id=$11`, req.URL, req.Title, req.Description, req.SiteURL, req.Language, req.Enabled, durationSec(req.FetchInterval), req.Status, id, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	return s.GetSource(ctx, id, sc)
}
func durationSec(v *time.Duration) any {
	if v == nil {
		return nil
	}
	if *v <= 0 {
		return int64(3600)
	}
	return int64(v.Seconds())
}
func (s *Store) DeleteSource(ctx context.Context, id string, sc Scope) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	tag, e := s.pool.Exec(ctx, `DELETE FROM rss_sources WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}

func (s *Store) ClaimDueSources(ctx context.Context, sc Scope, now time.Time, limit int) ([]Source, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	rows, e := s.pool.Query(ctx, `WITH claimed AS (SELECT id FROM rss_sources WHERE user_id=$1 AND workspace_id=$2 AND enabled AND (next_fetch_at IS NULL OR next_fetch_at <= $3) ORDER BY next_fetch_at NULLS FIRST FOR UPDATE SKIP LOCKED LIMIT $4) UPDATE rss_sources s SET next_fetch_at=$3 + make_interval(secs => s.fetch_interval),updated_at=NOW() FROM claimed c WHERE s.id=c.id RETURNING `+sourceCols, sc.UserID, sc.WorkspaceID, now, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		x, e := scanSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func (s *Store) SetSourceFetched(ctx context.Context, sc Scope, id string, at time.Time, etag, lastModified string, fetchErr error) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	status := SourceActive
	msg := ""
	if fetchErr != nil {
		status = SourceError
		msg = fetchErr.Error()
	}
	_, e := s.pool.Exec(ctx, `UPDATE rss_sources SET last_fetched_at=$1,etag=CASE WHEN $2='' THEN etag ELSE $2 END,last_modified=CASE WHEN $3='' THEN last_modified ELSE $3 END,status=$4,error=$5,updated_at=NOW() WHERE id=$6 AND user_id=$7 AND workspace_id=$8`, at, etag, lastModified, status, msg, id, sc.UserID, sc.WorkspaceID)
	return e
}
