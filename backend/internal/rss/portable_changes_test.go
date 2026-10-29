package rss

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type portableGate struct {
	claimed          atomic.Bool
	written, release chan struct{}
}
type portableQueryKey struct{}

func (g *portableGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, portableQueryKey{}, strings.HasPrefix(strings.TrimSpace(data.SQL), "INSERT INTO rss_sources"))
}
func (g *portableGate) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if writing, _ := ctx.Value(portableQueryKey{}).(bool); writing && g.claimed.CompareAndSwap(false, true) {
		close(g.written)
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
}

func TestPortableJournalSnapshotWaitsForEarlierScopeCommit(t *testing.T) {
	original, cleanup := newPGStore(t)
	defer cleanup()
	gate := &portableGate{written: make(chan struct{}), release: make(chan struct{})}
	pool := cloneScopedRSSPool(t, original, gate)
	defer pool.Close()
	s := &Store{pool: pool}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sc := testScope()
	epoch := int64(0)
	writer := make(chan error, 1)
	go func() {
		_, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/concurrent", PortalSync: true, PortalEpoch: &epoch}, sc)
		writer <- err
	}()
	select {
	case <-gate.written:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reader := make(chan error, 1)
	go func() {
		page, err := s.ListPortableChanges(ctx, sc, 0, 50)
		if err == nil && len(page.Changes) != 1 {
			err = fmt.Errorf("missing committed event: %+v", page)
		}
		reader <- err
	}()
	select {
	case err := <-reader:
		close(gate.release)
		<-writer
		t.Fatalf("snapshot escaped uncommitted writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate.release)
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if err := <-reader; err != nil {
		t.Fatal(err)
	}
}

func TestPortableJournalFailureRollsBackSourceAndCascadeKeepsOriginalEpoch(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(0)
	if _, err := s.pool.Exec(ctx, `ALTER TABLE rss_portable_changes ADD CONSTRAINT test_journal_failure CHECK(title<>'reject')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/rejected", Title: "reject", PortalSync: true, PortalEpoch: &epoch}, sc); err == nil {
		t.Fatal("source committed without journal")
	}
	sources, err := s.ListSources(ctx, sc)
	if err != nil || len(sources) != 0 {
		t.Fatalf("source rollback: %+v %v", sources, err)
	}
	source, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/source", Title: "allowed", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	fresh := int64(2)
	if err = s.DeleteSourcePortable(ctx, source.ID, sc, &fresh); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, 0, 50)
	if err != nil || len(page.Changes) != 2 {
		t.Fatalf("events: %+v %v", page, err)
	}
	if *page.Changes[0].Epoch != 0 || *page.Changes[1].Epoch != 2 {
		t.Fatalf("epoch was rekeyed: %+v", page)
	}
}

func TestPortableJournalOnlyEnrollsNewExplicitSources(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	old, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/old", Enabled: true}, sc)
	if err != nil {
		t.Fatal(err)
	}
	epoch := int64(0)
	source, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/new", Title: "new", Enabled: true, PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, 0, 50)
	if err != nil || len(page.Changes) != 1 || page.Changes[0].ExtID != "pocketd-source:"+source.ID {
		t.Fatalf("new source journal: %+v %v", page, err)
	}
	if _, err := s.UpdateSource(ctx, old.ID, UpdateSourceRequest{Title: strPtr("old edited")}, sc); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceFetched(ctx, sc, source.ID, time.Now(), "etag", "", nil); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListPortableChanges(ctx, sc, page.NextCursor, 50)
	if err != nil || len(page.Changes) != 0 {
		t.Fatalf("history or scheduler enrolled: %+v %v", page, err)
	}
}

func TestPortableItemsKeepOriginalTimeAndDeleteTombstones(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(4)
	source, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/feed", Enabled: true, PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	item := Item{ID: "known", SourceID: source.ID, URL: "https://example.com/item", Title: "title", Summary: "summary", Content: "PRIVATE FULL BODY", UpdatedAt: &at, Hash: "known"}
	if _, err = s.UpsertItemScoped(ctx, item, sc); err != nil {
		t.Fatal(err)
	}
	unknown := item
	unknown.ID = "unknown"
	unknown.Hash = "unknown"
	unknown.UpdatedAt = nil
	if _, err = s.UpsertItemScoped(ctx, unknown, sc); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, 0, 50)
	if err != nil || len(page.Changes) != 3 {
		t.Fatalf("journal: %+v %v", page, err)
	}
	if page.Changes[1].ClientUpdatedAt == nil || !page.Changes[1].ClientUpdatedAt.Equal(at) || page.Changes[1].Epoch == nil || *page.Changes[1].Epoch != 4 {
		t.Fatalf("original version lost: %+v", page.Changes[1])
	}
	if page.Changes[2].ClientUpdatedAt != nil || page.Changes[2].SyncError != "source_time_unknown" {
		t.Fatalf("download time substituted: %+v", page.Changes[2])
	}
	if _, ok := page.Changes[1].Payload["content"]; ok {
		t.Fatal("private body in portable journal")
	}
	if err = s.DeleteSource(ctx, source.ID, sc); err != nil {
		t.Fatal(err)
	}
	tombstones, err := s.ListPortableChanges(ctx, sc, page.NextCursor, 50)
	if err != nil || len(tombstones.Changes) != 3 {
		t.Fatalf("cascade tombstones: %+v %v", tombstones, err)
	}
	for _, change := range tombstones.Changes {
		if !change.Deleted || change.ClientUpdatedAt == nil || change.Epoch == nil || *change.Epoch != 4 {
			t.Fatalf("invalid tombstone: %+v", change)
		}
	}
	other, err := s.ListPortableChanges(ctx, Scope{UserID: "other", WorkspaceID: sc.WorkspaceID}, 0, 50)
	if err != nil || len(other.Changes) != 0 {
		t.Fatal("journal owner leak")
	}
}

func TestPortableSchedulerBookkeepingDoesNotInvertScopeLocks(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sc, epoch := testScope(), int64(0)
	source, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.com/deadlock", Title: "old", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Rollback(context.Background())
	portable, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer portable.Rollback(context.Background())
	if _, err = scheduler.Exec(ctx, `SELECT id FROM rss_sources WHERE id=$1 FOR UPDATE`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = portable.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(json_build_array('rss-portable',$1::text,$2::text)::text,0))`, sc.UserID, sc.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := portable.Exec(ctx, `UPDATE rss_sources SET title='new' WHERE id=$1`, source.ID)
		result <- err
	}()
	// The portable writer waits for the source row; bookkeeping must not ask
	// for the scope lock while it still owns that row.
	select {
	case err := <-result:
		t.Fatalf("portable writer escaped source row: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_, schedulerErr := scheduler.Exec(ctx, `UPDATE rss_sources SET next_fetch_at=NOW(),last_fetched_at=NOW(),updated_at=NOW(),status='active' WHERE id=$1`, source.ID)
	_ = scheduler.Rollback(context.Background())
	if portableErr := <-result; schedulerErr != nil || portableErr != nil {
		t.Fatalf("lock order: scheduler=%v portable=%v", schedulerErr, portableErr)
	}
}
