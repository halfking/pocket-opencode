package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is the calendar facade the HTTP layer talks to. It owns the event
// store and the (optional) bridges to the other time-bearing domains.
type Service struct {
	store   *Store
	sources dueSources
}

// NewService builds a service over the shared Postgres pool and runs the
// idempotent migration. sources may be nil, in which case the feed contains
// user events only — that is a valid, working configuration.
func NewService(pool *pgxpool.Pool, sources dueSources) (*Service, error) {
	store, err := NewStore(pool)
	if err != nil {
		return nil, err
	}
	return &Service{store: store, sources: sources}, nil
}

// Available reports whether the service can serve requests.
func (s *Service) Available() bool {
	return s != nil && s.store.Available()
}

// DefaultTimezone matches the repo's convention for user-facing schedules.
const DefaultTimezone = "Asia/Shanghai"

// EventInput is the create/update payload. It deliberately does not carry
// OwnerUserID or WorkspaceID: ownership comes from the authenticated claims,
// never from the request body.
type EventInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartAt     int64  `json:"startAt"`
	EndAt       int64  `json:"endAt"`
	AllDay      bool   `json:"allDay"`
	Timezone    string `json:"timezone"`
	RemindAt    int64  `json:"remindAt"`
	Visibility  string `json:"visibility"`
}

// normalize applies defaults and rejects inputs the store must not accept.
//
// Validation lives here rather than in the handler so both the HTTP path and
// any future non-HTTP caller get the same guarantees — an event with an empty
// title or an end before its start is nonsense in every context.
func (in *EventInput) normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return errors.New("title is required")
	}
	if in.StartAt <= 0 {
		return errors.New("startAt is required")
	}
	if in.EndAt > 0 && in.EndAt < in.StartAt {
		return errors.New("endAt must not be before startAt")
	}
	if in.Timezone == "" {
		in.Timezone = DefaultTimezone
	}
	if in.Visibility == "" {
		in.Visibility = VisibilityPrivate
	}
	if !ValidVisibility(in.Visibility) {
		return fmt.Errorf("invalid visibility %q", in.Visibility)
	}
	return nil
}

// CreateEvent validates and stores a new event.
func (s *Service) CreateEvent(ctx context.Context, wsID, userID string, in EventInput) (*Event, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	e := &Event{
		ID:          NewID(),
		WorkspaceID: normalizeWorkspace(wsID),
		OwnerUserID: userID,
		Title:       in.Title,
		Description: in.Description,
		Location:    in.Location,
		StartAt:     in.StartAt,
		EndAt:       in.EndAt,
		AllDay:      in.AllDay,
		Timezone:    in.Timezone,
		RemindAt:    in.RemindAt,
		Visibility:  in.Visibility,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.Create(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

// GetEvent reads one event.
func (s *Service) GetEvent(ctx context.Context, id, wsID string) (*Event, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	return s.store.Get(ctx, id, wsID)
}

// UpdateEvent applies a partial-style payload: fields left at their zero value
// keep their stored value, because "clear the description" and "leave the
// description alone" must not be the same request.
func (s *Service) UpdateEvent(ctx context.Context, id, wsID string, in EventInput) (*Event, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id, wsID)
	if err != nil {
		return nil, err
	}
	if in.Description == "" {
		in.Description = current.Description
	}
	if in.Location == "" {
		in.Location = current.Location
	}
	if in.RemindAt == 0 {
		in.RemindAt = current.RemindAt
	}
	updated := *current
	updated.Title = in.Title
	updated.Description = in.Description
	updated.Location = in.Location
	updated.StartAt = in.StartAt
	updated.EndAt = in.EndAt
	updated.AllDay = in.AllDay
	updated.Timezone = in.Timezone
	updated.RemindAt = in.RemindAt
	updated.Visibility = in.Visibility
	updated.UpdatedAt = time.Now().Unix()
	if err := s.store.Update(ctx, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteEvent removes an event.
func (s *Service) DeleteEvent(ctx context.Context, id, wsID string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	return s.store.Delete(ctx, id, wsID)
}

// maxFeedRangeSeconds caps a single feed request at 62 days. A calendar
// month grid needs 42 days, so this leaves headroom for the adjacent-month
// padding while stopping a client from asking for a decade of events and
// turning one request into a table scan of the whole workspace.
const maxFeedRangeSeconds int64 = 62 * 86400

// Feed returns the merged calendar view for [from, to).
func (s *Service) Feed(ctx context.Context, wsID, userID string, from, to int64, includeShared bool) ([]FeedEntry, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if to <= from {
		return nil, errors.New("to must be greater than from")
	}
	if to-from > maxFeedRangeSeconds {
		return nil, fmt.Errorf("range too large: max %d seconds", maxFeedRangeSeconds)
	}
	return s.BuildFeed(ctx, wsID, userID, from, to, includeShared)
}
