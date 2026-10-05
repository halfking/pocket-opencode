// Package calendar stores user-created calendar events and serves the unified
// feed that the mobile calendar renders.
//
// # Why a separate table instead of reading the other domains directly
//
// The goal is "important information should show up in the calendar", and the
// app already has several time-bearing domains (work items carry due_at,
// scheduled tasks carry next_run_at, meetings carry created_at). The tempting
// shortcut is to fan out at query time and union those tables. That produces a
// calendar that is *read-only* — you can see your deadlines but you cannot
// schedule anything, which is the other half of the request. So there is a
// real events table for user-scheduled things, and the feed merges both.
//
// # Tenancy
//
// Every key and every statement carries workspace_id, matching the rest of the
// repo (see task/access.go): a cross-tenant id reads as "not found" rather than
// leaking another tenant's row. OwnerUserID is carried alongside it because two
// people in one workspace must not see each other's private events by default.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultWorkspaceID mirrors task.DefaultWorkspaceID for single-tenant setups.
const DefaultWorkspaceID = "default"

// ErrNotFound is returned for an id that does not exist inside the caller's
// workspace. It is deliberately the same error for "missing" and "other
// tenant's", so a caller cannot probe for the existence of foreign ids.
var ErrNotFound = errors.New("calendar event not found")

// ErrUnavailable is returned when the store has no database, so a misconfigured
// deployment fails loudly at the call site instead of silently returning "no
// events" — an empty calendar looks exactly like a working one.
var ErrUnavailable = errors.New("calendar store unavailable")

// Visibility values.
const (
	VisibilityPrivate = "private"
	VisibilityShared  = "shared"
)

// ValidVisibility reports whether v is an accepted visibility.
func ValidVisibility(v string) bool {
	return v == VisibilityPrivate || v == VisibilityShared
}

// Event is one user-scheduled item on the calendar.
//
// StartAt/EndAt are unix **seconds**. A zero-length event (EndAt <= StartAt)
// is legal and means "a moment in time" — that is how a deadline is
// represented, and the feed relies on it (see the frontend calendar-feed).
type Event struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	OwnerUserID string `json:"ownerUserId"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	StartAt     int64  `json:"startAt"`
	EndAt       int64  `json:"endAt"`
	AllDay      bool   `json:"allDay"`
	// Timezone is an IANA name ("Asia/Shanghai"), not a UTC offset: an offset
	// cannot express DST, and an event that drifts by an hour every March is
	// exactly the defect this field exists to prevent.
	Timezone   string `json:"timezone"`
	RemindAt   int64  `json:"remindAt,omitempty"`
	Visibility string `json:"visibility"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// DurationSeconds returns the event length, treating a zero-length event as
// zero rather than negative.
func (e *Event) DurationSeconds() int64 {
	if e.EndAt <= e.StartAt {
		return 0
	}
	return e.EndAt - e.StartAt
}

// Store is the PostgreSQL-backed calendar store. It shares the pocketd pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore runs the idempotent migration and returns the store.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("calendar migrate: %w", err)
	}
	return s, nil
}

// Available reports whether the store can serve queries.
func (s *Store) Available() bool {
	return s != nil && s.pool != nil
}

// eventIDSeq guarantees process-local uniqueness of minted ids.
//
// This is not theoretical: the meeting package hit this exact defect — on some
// platforms (notably the repo's Windows machines) `time.Now()` has no
// nanosecond precision, so two events created inside one clock tick got the
// same id and the second silently overwrote the first: create returned 201 and
// the row was never readable. See internal/meeting/store.go meetingIDSeq.
//
// A monotonic counter appended to the timestamp makes the pair unique within a
// process; across processes the nanosecond part differs. Format matches
// meeting / finance / chat_summary.
var eventIDSeq atomic.Uint64

// NewID mints an event id. The prefix makes ids greppable in logs.
func NewID() string {
	return fmt.Sprintf("cal_%d_%d", time.Now().UnixNano(), eventIDSeq.Add(1))
}

const schema = `
CREATE TABLE IF NOT EXISTS calendar_events (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT 'default',
    owner_user_id TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    start_at     BIGINT NOT NULL,
    end_at       BIGINT NOT NULL DEFAULT 0,
    all_day      BOOLEAN NOT NULL DEFAULT FALSE,
    timezone     TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    remind_at    BIGINT NOT NULL DEFAULT 0,
    visibility   TEXT NOT NULL DEFAULT 'private',
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

-- The hot query is always "this workspace, this range". The index is on
-- (workspace_id, start_at) for exactly that; owner_user_id comes second so the
-- private-only variant stays index-only.
CREATE INDEX IF NOT EXISTS idx_calendar_events_ws_start
    ON calendar_events(workspace_id, start_at);
CREATE INDEX IF NOT EXISTS idx_calendar_events_ws_owner_start
    ON calendar_events(workspace_id, owner_user_id, start_at);
`

func (s *Store) migrate() error {
	if s == nil || s.pool == nil {
		return ErrUnavailable
	}
	_, err := s.pool.Exec(context.Background(), schema)
	return err
}

func normalizeWorkspace(wsID string) string {
	if wsID == "" {
		return DefaultWorkspaceID
	}
	return wsID
}

const eventColumns = `id, workspace_id, owner_user_id, title, description, location,
	start_at, end_at, all_day, timezone, remind_at, visibility, created_at, updated_at`

func scanEvent(row pgx.Row) (*Event, error) {
	var e Event
	err := row.Scan(
		&e.ID, &e.WorkspaceID, &e.OwnerUserID, &e.Title, &e.Description, &e.Location,
		&e.StartAt, &e.EndAt, &e.AllDay, &e.Timezone, &e.RemindAt, &e.Visibility,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Create inserts an event. The caller supplies OwnerUserID from JWT claims;
// it is never taken from the request body, matching the rest of the server.
func (s *Store) Create(ctx context.Context, e *Event) error {
	if !s.Available() {
		return ErrUnavailable
	}
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("calendar create: id is required")
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("calendar create: title is required")
	}
	if e.StartAt <= 0 {
		return errors.New("calendar create: startAt is required")
	}
	e.WorkspaceID = normalizeWorkspace(e.WorkspaceID)
	if e.Visibility == "" {
		e.Visibility = VisibilityPrivate
	}
	if !ValidVisibility(e.Visibility) {
		return fmt.Errorf("calendar create: invalid visibility %q", e.Visibility)
	}
	if e.Timezone == "" {
		e.Timezone = "Asia/Shanghai"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO calendar_events (`+eventColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		e.ID, e.WorkspaceID, e.OwnerUserID, e.Title, e.Description, e.Location,
		e.StartAt, e.EndAt, e.AllDay, e.Timezone, e.RemindAt, e.Visibility,
		e.CreatedAt, e.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("calendar create: %w", err)
	}
	return nil
}

// Get reads one event scoped to a workspace.
func (s *Store) Get(ctx context.Context, id, wsID string) (*Event, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	e, err := scanEvent(s.pool.QueryRow(ctx,
		`SELECT `+eventColumns+` FROM calendar_events WHERE id = $1 AND workspace_id = $2`,
		id, normalizeWorkspace(wsID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("calendar get: %w", err)
	}
	return e, nil
}

// Update replaces the mutable fields of an event inside the caller's workspace.
func (s *Store) Update(ctx context.Context, e *Event) error {
	if !s.Available() {
		return ErrUnavailable
	}
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("calendar update: id is required")
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("calendar update: title is required")
	}
	if !ValidVisibility(e.Visibility) {
		return fmt.Errorf("calendar update: invalid visibility %q", e.Visibility)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE calendar_events
		    SET title = $3, description = $4, location = $5, start_at = $6, end_at = $7,
		        all_day = $8, timezone = $9, remind_at = $10, visibility = $11, updated_at = $12
		  WHERE id = $1 AND workspace_id = $2`,
		e.ID, normalizeWorkspace(e.WorkspaceID), e.Title, e.Description, e.Location,
		e.StartAt, e.EndAt, e.AllDay, e.Timezone, e.RemindAt, e.Visibility, e.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("calendar update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an event inside the caller's workspace.
func (s *Store) Delete(ctx context.Context, id, wsID string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM calendar_events WHERE id = $1 AND workspace_id = $2`,
		id, normalizeWorkspace(wsID))
	if err != nil {
		return fmt.Errorf("calendar delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRange returns events that **intersect** [from, to).
//
// The predicate is `start_at < to AND end_at > from`, with zero-length events
// (end_at <= start_at) folded to a single instant so a deadline on the first
// instant of the range is still returned. Using `start_at BETWEEN from AND to`
// instead would drop every multi-day event that started before the window —
// which is the common case when paging backwards through history.
//
// includeShared controls whether other users' `shared` events join the result.
func (s *Store) ListRange(ctx context.Context, wsID, userID string, from, to int64, includeShared bool) ([]Event, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if to <= from {
		return []Event{}, nil
	}
	query := `SELECT ` + eventColumns + ` FROM calendar_events
	           WHERE workspace_id = $1
	             AND start_at < $3
	             AND (CASE WHEN end_at > start_at THEN end_at ELSE start_at + 1 END) > $2`
	args := []interface{}{normalizeWorkspace(wsID), from, to}
	if !includeShared {
		query += ` AND (owner_user_id = $4 OR visibility = 'shared')`
		args = append(args, userID)
	}
	query += ` ORDER BY start_at ASC, id ASC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("calendar list: %w", err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("calendar scan: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("calendar list: %w", err)
	}
	return out, nil
}
