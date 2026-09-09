package rss

import (
	"context"
	"time"
)

// Compatibility aliases keep the domain vocabulary convenient for callers that
// prefer the shorter method names while retaining the explicitly scoped forms.
func (s *Store) ClaimDue(ctx context.Context, sc Scope, now time.Time, limit int) ([]Source, error) {
	return s.ClaimDueSources(ctx, sc, now, limit)
}
func (s *Store) SetSourceStatus(ctx context.Context, id string, status SourceStatus, message string, sc Scope) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE rss_sources SET status=$1,error=$2,updated_at=NOW() WHERE id=$3 AND user_id=$4 AND workspace_id=$5`, status, message, id, sc.UserID, sc.WorkspaceID)
	return err
}
func (s *Store) MarkSourceFetched(ctx context.Context, sc Scope, id string, at time.Time, etag, lastModified string, fetchErr error) error {
	return s.SetSourceFetched(ctx, sc, id, at, etag, lastModified, fetchErr)
}
func (s *Store) GetItemScoped(ctx context.Context, sc Scope, id string) (*Item, error) {
	return s.GetItem(ctx, id, sc)
}
func (s *Store) ListItemsScoped(ctx context.Context, sc Scope, opt ListItemsOptions) ([]Item, error) {
	return s.ListItems(ctx, sc, opt)
}
