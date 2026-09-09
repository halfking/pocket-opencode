package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const itemCols = `id,source_id,user_id,workspace_id,guid,hash,url,title,author,summary,content,language,categories,published_at,updated_at,fetched_at,relevance,match_reasons,status`

func scanItem(r pgx.Row) (*Item, error) {
	var x Item
	var cats, reasons []byte
	err := r.Scan(&x.ID, &x.SourceID, &x.UserID, &x.WorkspaceID, &x.GUID, &x.Hash, &x.URL, &x.Title, &x.Author, &x.Summary, &x.Content, &x.Language, &cats, &x.PublishedAt, &x.UpdatedAt, &x.FetchedAt, &x.Relevance, &reasons, &x.Status)
	if err == nil {
		_ = json.Unmarshal(cats, &x.Categories)
		_ = json.Unmarshal(reasons, &x.MatchReasons)
	}
	return &x, err
}
func (s *Store) UpsertItem(ctx context.Context, x Item) error {
	_, e := s.UpsertItemScoped(ctx, x, Scope{x.UserID, x.WorkspaceID})
	return e
}
func (s *Store) UpsertItemScoped(ctx context.Context, x Item, sc Scope) (bool, error) {
	if err := requireScope(sc); err != nil {
		return false, err
	}
	if x.SourceID == "" {
		return false, fmt.Errorf("rss: source_id is required")
	}
	x.UserID = sc.UserID
	x.WorkspaceID = sc.WorkspaceID
	if x.Hash == "" {
		x.Hash = stableHash(x.GUID, x.URL, x.Title, x.Content)
	}
	if x.ID == "" {
		x.ID = x.Hash
	}
	if x.Status == "" {
		x.Status = ItemUnread
	}
	if x.FetchedAt == nil {
		x.FetchedAt = timePtr(time.Now().UTC())
	}
	var inserted bool
	e := s.pool.QueryRow(ctx, `INSERT INTO rss_items(id,source_id,user_id,workspace_id,guid,hash,url,title,author,summary,content,language,categories,published_at,updated_at,fetched_at,relevance,match_reasons,status) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19 WHERE EXISTS(SELECT 1 FROM rss_sources WHERE id=$2 AND user_id=$3 AND workspace_id=$4) ON CONFLICT(source_id,hash) DO UPDATE SET guid=EXCLUDED.guid,url=EXCLUDED.url,title=EXCLUDED.title,author=EXCLUDED.author,summary=EXCLUDED.summary,content=EXCLUDED.content,language=EXCLUDED.language,categories=EXCLUDED.categories,published_at=EXCLUDED.published_at,updated_at=EXCLUDED.updated_at,fetched_at=EXCLUDED.fetched_at,relevance=EXCLUDED.relevance,match_reasons=EXCLUDED.match_reasons RETURNING (xmax=0)`, x.ID, x.SourceID, x.UserID, x.WorkspaceID, x.GUID, x.Hash, x.URL, x.Title, x.Author, x.Summary, x.Content, x.Language, j(x.Categories), x.PublishedAt, x.UpdatedAt, x.FetchedAt, x.Relevance, j(x.MatchReasons), x.Status).Scan(&inserted)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return inserted, e
}
func (s *Store) GetItem(ctx context.Context, id string, sc Scope) (*Item, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x, e := scanItem(s.pool.QueryRow(ctx, `SELECT `+itemCols+` FROM rss_items WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) ListItems(ctx context.Context, sc Scope, opt ListItemsOptions) ([]Item, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if opt.Limit < 1 {
		opt.Limit = 50
	}
	if opt.Limit > 200 {
		opt.Limit = 200
	}
	rows, e := s.pool.Query(ctx, `SELECT `+itemCols+` FROM rss_items WHERE user_id=$1 AND workspace_id=$2 AND ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR published_at >= $4) AND ($5::timestamptz IS NULL OR published_at <= $5) AND ($6='' OR title ILIKE '%'||$6||'%' OR summary ILIKE '%'||$6||'%') ORDER BY published_at DESC NULLS LAST,fetched_at DESC LIMIT $7 OFFSET $8`, sc.UserID, sc.WorkspaceID, opt.Status, opt.Since, opt.Until, opt.Query, opt.Limit, opt.Offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		x, e := scanItem(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func (s *Store) MarkItem(ctx context.Context, id string, status ItemStatus, sc Scope) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	switch status {
	case ItemUnread, ItemRead, ItemStarred, ItemArchived:
	default:
		return fmt.Errorf("rss: invalid item status %q", status)
	}
	tag, e := s.pool.Exec(ctx, `UPDATE rss_items SET status=$1 WHERE id=$2 AND user_id=$3 AND workspace_id=$4`, status, id, sc.UserID, sc.WorkspaceID)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}

const ruleCols = `id,user_id,workspace_id,name,enabled,include_keywords,exclude_keywords,languages,since_at,until_at,min_relevance,created_at,updated_at`

func scanRule(r pgx.Row) (*FilterRule, error) {
	var x FilterRule
	var a, b, c []byte
	e := r.Scan(&x.ID, &x.UserID, &x.WorkspaceID, &x.Name, &x.Enabled, &a, &b, &c, &x.Since, &x.Until, &x.MinRelevance, &x.CreatedAt, &x.UpdatedAt)
	if e == nil {
		_ = json.Unmarshal(a, &x.IncludeKeywords)
		_ = json.Unmarshal(b, &x.ExcludeKeywords)
		_ = json.Unmarshal(c, &x.Languages)
	}
	return &x, e
}
func (s *Store) CreateFilterRule(ctx context.Context, x FilterRule, sc Scope) (*FilterRule, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x.ID = id("rule")
	x.UserID = sc.UserID
	x.WorkspaceID = sc.WorkspaceID
	if _, e := s.pool.Exec(ctx, `INSERT INTO rss_filter_rules(id,user_id,workspace_id,name,enabled,include_keywords,exclude_keywords,languages,since_at,until_at,min_relevance) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, x.ID, x.UserID, x.WorkspaceID, x.Name, x.Enabled, j(x.IncludeKeywords), j(x.ExcludeKeywords), j(x.Languages), x.Since, x.Until, x.MinRelevance); e != nil {
		return nil, e
	}
	return s.GetFilterRule(ctx, x.ID, sc)
}
func (s *Store) GetFilterRule(ctx context.Context, id string, sc Scope) (*FilterRule, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x, e := scanRule(s.pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM rss_filter_rules WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) ListFilterRules(ctx context.Context, sc Scope) ([]FilterRule, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	rows, e := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM rss_filter_rules WHERE user_id=$1 AND workspace_id=$2 ORDER BY created_at`, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FilterRule{}
	for rows.Next() {
		x, e := scanRule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func (s *Store) UpdateFilterRule(ctx context.Context, x FilterRule, sc Scope) (*FilterRule, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	tag, e := s.pool.Exec(ctx, `UPDATE rss_filter_rules SET name=$1,enabled=$2,include_keywords=$3,exclude_keywords=$4,languages=$5,since_at=$6,until_at=$7,min_relevance=$8,updated_at=NOW() WHERE id=$9 AND user_id=$10 AND workspace_id=$11`, x.Name, x.Enabled, j(x.IncludeKeywords), j(x.ExcludeKeywords), j(x.Languages), x.Since, x.Until, x.MinRelevance, x.ID, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetFilterRule(ctx, x.ID, sc)
}
func (s *Store) DeleteFilterRule(ctx context.Context, id string, sc Scope) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	tag, e := s.pool.Exec(ctx, `DELETE FROM rss_filter_rules WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}

const draftCols = `id,item_id,user_id,workspace_id,text,status,created_at,updated_at`

func scanDraft(r pgx.Row) (*Draft, error) {
	var x Draft
	e := r.Scan(&x.ID, &x.ItemID, &x.UserID, &x.WorkspaceID, &x.Text, &x.Status, &x.CreatedAt, &x.UpdatedAt)
	return &x, e
}
func (s *Store) UpsertDraft(ctx context.Context, req UpsertDraftRequest, sc Scope) (*Draft, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if req.ID == "" {
		req.ID = id("draft")
	}
	if req.Status == "" {
		req.Status = DraftPending
	}
	x, e := scanDraft(s.pool.QueryRow(ctx, `INSERT INTO rss_drafts(id,item_id,user_id,workspace_id,text,status) SELECT $1,$2,$3,$4,$5,$6 WHERE EXISTS(SELECT 1 FROM rss_items WHERE id=$2 AND user_id=$3 AND workspace_id=$4) ON CONFLICT(user_id,workspace_id,item_id) DO UPDATE SET text=EXCLUDED.text,status=EXCLUDED.status,updated_at=NOW() RETURNING `+draftCols, req.ID, req.ItemID, sc.UserID, sc.WorkspaceID, req.Text, req.Status))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) GetDraft(ctx context.Context, id string, sc Scope) (*Draft, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x, e := scanDraft(s.pool.QueryRow(ctx, `SELECT `+draftCols+` FROM rss_drafts WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) GetDraftByItem(ctx context.Context, itemID string, sc Scope) (*Draft, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	x, e := scanDraft(s.pool.QueryRow(ctx, `SELECT `+draftCols+` FROM rss_drafts WHERE item_id=$1 AND user_id=$2 AND workspace_id=$3`, itemID, sc.UserID, sc.WorkspaceID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) UpdateDraft(ctx context.Context, id string, req UpdateDraftRequest, sc Scope) (*Draft, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	tag, e := s.pool.Exec(ctx, `UPDATE rss_drafts SET text=COALESCE($1,text),status=COALESCE($2,status),updated_at=NOW() WHERE id=$3 AND user_id=$4 AND workspace_id=$5`, req.Text, req.Status, id, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetDraft(ctx, id, sc)
}

const attemptCols = `id,draft_id,item_id,user_id,workspace_id,platform,remote_id,status,error,attempted_at,created_at`

func scanAttempt(r pgx.Row) (*PublishAttempt, error) {
	var x PublishAttempt
	e := r.Scan(&x.ID, &x.DraftID, &x.ItemID, &x.UserID, &x.WorkspaceID, &x.Platform, &x.RemoteID, &x.Status, &x.Error, &x.AttemptedAt, &x.CreatedAt)
	return &x, e
}
func (s *Store) RecordPublishAttempt(ctx context.Context, req PublishAttemptRequest, sc Scope) (*PublishAttempt, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	if req.Status == "" {
		req.Status = PublishPending
	}
	x, e := scanAttempt(s.pool.QueryRow(ctx, `INSERT INTO rss_publish_attempts(id,draft_id,item_id,user_id,workspace_id,platform,remote_id,status,error) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 WHERE EXISTS(SELECT 1 FROM rss_drafts WHERE id=$2 AND user_id=$4 AND workspace_id=$5) RETURNING `+attemptCols, id("attempt"), req.DraftID, req.ItemID, sc.UserID, sc.WorkspaceID, req.Platform, req.RemoteID, req.Status, req.Error))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, e
}
func (s *Store) ListPublishAttempts(ctx context.Context, draftID string, sc Scope) ([]PublishAttempt, error) {
	if err := requireScope(sc); err != nil {
		return nil, err
	}
	rows, e := s.pool.Query(ctx, `SELECT `+attemptCols+` FROM rss_publish_attempts WHERE draft_id=$1 AND user_id=$2 AND workspace_id=$3 ORDER BY attempted_at DESC`, draftID, sc.UserID, sc.WorkspaceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PublishAttempt{}
	for rows.Next() {
		x, e := scanAttempt(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

type RetentionResult struct{ Items, Attempts, Drafts int64 }

func (s *Store) Retain(ctx context.Context, sc Scope, opt RetentionOptions) (RetentionResult, error) {
	if err := requireScope(sc); err != nil {
		return RetentionResult{}, err
	}
	var out RetentionResult
	if opt.AttemptsBefore != nil {
		t, e := s.pool.Exec(ctx, `DELETE FROM rss_publish_attempts WHERE user_id=$1 AND workspace_id=$2 AND attempted_at<$3`, sc.UserID, sc.WorkspaceID, opt.AttemptsBefore)
		if e != nil {
			return out, e
		}
		out.Attempts = t.RowsAffected()
	}
	if opt.DraftsBefore != nil {
		t, e := s.pool.Exec(ctx, `DELETE FROM rss_drafts WHERE user_id=$1 AND workspace_id=$2 AND updated_at<$3`, sc.UserID, sc.WorkspaceID, opt.DraftsBefore)
		if e != nil {
			return out, e
		}
		out.Drafts = t.RowsAffected()
	}
	if opt.ItemsBefore != nil {
		t, e := s.pool.Exec(ctx, `DELETE FROM rss_items WHERE user_id=$1 AND workspace_id=$2 AND fetched_at<$3`, sc.UserID, sc.WorkspaceID, opt.ItemsBefore)
		if e != nil {
			return out, e
		}
		out.Items = t.RowsAffected()
	}
	return out, nil
}
func (s *Store) Retention(ctx context.Context, sc Scope, before time.Time) (RetentionResult, error) {
	return s.Retain(ctx, sc, RetentionOptions{ItemsBefore: &before, AttemptsBefore: &before, DraftsBefore: &before})
}
