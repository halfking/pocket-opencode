// Package notifycenter implements S0-E: the Notification Center for the
// Personal Super Terminal.
//
// It owns three concerns (spec §3.2 decision 5):
//
//   1. Persistence — the `notifications` table is the in-app notification
//      inbox (the "Notification Center" tab on mobile). Every dispatched event
//      that survives the rule filter gets a row here, so the user can review
//      what they missed even if the push didn't get tapped.
//   2. Rules — `notification_rules` decide WHICH events become notifications
//      and WHICH channels they use. A rule binds (event_source, event_type)
//      → channels + quiet_hours. Without a matching rule, an event is dropped
//      (or falls through to a default rule).
//   3. Delivery — the Sender interface abstracts the push channel. S0 ships
//      two implementations:
//        - WebsocketSender: pushes to the foreground via the existing ws.Hub
//          (zero-latency, free, works while app is open)
//        - NoopPushSender: placeholder for APNs/FCM. The real APNs/FCM
//          implementation is a deployment-time task (needs certificates +
//          provider SDK); the interface is stable so it drops in later.
//
// Why a new package instead of extending internal/notification:
//   internal/notification only defines Event types (no storage, no rules, no
//   delivery). Reusing it would blur its role. notifycenter owns the full
//   pipeline; internal/notification stays as a pure event-vocabulary package
//   that notifycenter and other modules import.
package notifycenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is a delivery target.
type Channel string

const (
	ChannelWebsocket Channel = "websocket" // foreground real-time
	ChannelAPNs      Channel = "apns"      // iOS background
	ChannelFCM       Channel = "fcm"       // Android background
	ChannelInbox     Channel = "inbox"     // in-app notification list only
)

// Notification is one row in the inbox.
type Notification struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id"`
	UserID       string          `json:"user_id"`
	Source       string          `json:"source"`        // task / email / meeting / ledger / agent / system
	Kind         string          `json:"kind"`          // event_type, e.g. task.completed
	Title        string          `json:"title"`
	Body         string          `json:"body"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Priority     string          `json:"priority"`      // low / normal / high / urgent
	ReadAt       int64           `json:"read_at,omitempty"`
	CreatedAt    int64           `json:"created_at"`
}

// Rule decides what happens to an event.
type Rule struct {
	ID            string   `json:"id"`
	WorkspaceID   string   `json:"workspace_id"`
	Source        string   `json:"source"`         // empty = wildcard
	Kind          string   `json:"kind"`           // empty = wildcard
	Channels      []string `json:"channels"`       // websocket / apns / fcm / inbox
	Priority      string   `json:"priority"`       // default priority for matching events
	QuietStartMin int      `json:"quiet_start_min"` // quiet-hours window (minutes from midnight, local)
	QuietEndMin   int      `json:"quiet_end_min"`
	Enabled       bool     `json:"enabled"`
}

// Event is what callers pass to Service.Dispatch.
type Event struct {
	WorkspaceID string
	UserID      string
	Source      string
	Kind        string
	Title       string
	Body        string
	Payload     json.RawMessage
	Priority    string
}

// ErrNotFound is returned on single-row miss.
var ErrNotFound = errors.New("notifycenter: not found")

// Store manages the notifications + rules tables.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs the Store and runs idempotent migrations.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("notifycenter: pgxpool is nil")
	}
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("notifycenter migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.pool.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS notifications (
	id            TEXT PRIMARY KEY,
	workspace_id  TEXT NOT NULL DEFAULT 'default',
	user_id       TEXT NOT NULL DEFAULT '',
	source        TEXT NOT NULL DEFAULT 'system',
	kind          TEXT NOT NULL DEFAULT '',
	title         TEXT NOT NULL DEFAULT '',
	body          TEXT NOT NULL DEFAULT '',
	payload       JSONB,
	priority      TEXT NOT NULL DEFAULT 'normal',
	read_at       BIGINT NOT NULL DEFAULT 0,
	created_at    BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notif_ws_time ON notifications(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notif_unread ON notifications(workspace_id, read_at) WHERE read_at = 0;

CREATE TABLE IF NOT EXISTS notification_rules (
	id              TEXT PRIMARY KEY,
	workspace_id    TEXT NOT NULL DEFAULT 'default',
	source          TEXT NOT NULL DEFAULT '',
	kind            TEXT NOT NULL DEFAULT '',
	channels        JSONB DEFAULT '["inbox"]',
	priority        TEXT NOT NULL DEFAULT 'normal',
	quiet_start_min INTEGER NOT NULL DEFAULT 0,
	quiet_end_min   INTEGER NOT NULL DEFAULT 0,
	enabled         BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX IF NOT EXISTS idx_notif_rules_ws ON notification_rules(workspace_id, enabled);
`)
	return err
}

// InsertNotification persists one notification row.
func (s *Store) InsertNotification(ctx context.Context, n *Notification) error {
	if n.WorkspaceID == "" {
		n.WorkspaceID = "default"
	}
	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().Unix()
	}
	payload := "null"
	if len(n.Payload) > 0 {
		payload = string(n.Payload)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO notifications (id, workspace_id, user_id, source, kind, title, body, payload, priority, read_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)
`, n.ID, n.WorkspaceID, n.UserID, n.Source, n.Kind, n.Title, n.Body, payload, n.Priority, n.ReadAt, n.CreatedAt)
	return err
}

// ListNotifications returns inbox rows for a workspace, newest first.
func (s *Store) ListNotifications(ctx context.Context, wsID string, limit, unreadOnly int) ([]Notification, error) {
	if wsID == "" {
		wsID = "default"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
SELECT id, workspace_id, user_id, source, kind, title, body, COALESCE(payload::text,'null'),
       priority, read_at, created_at
FROM notifications WHERE workspace_id = $1`
	args := []any{wsID}
	if unreadOnly > 0 {
		q += " AND read_at = 0"
	}
	q += " ORDER BY created_at DESC LIMIT $2"
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var payloadStr string
		if err := rows.Scan(&n.ID, &n.WorkspaceID, &n.UserID, &n.Source, &n.Kind, &n.Title, &n.Body, &payloadStr, &n.Priority, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if payloadStr != "null" && payloadStr != "" {
			n.Payload = json.RawMessage(payloadStr)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead sets read_at on one notification (or all unread in a workspace
// when id is empty).
func (s *Store) MarkRead(ctx context.Context, wsID, id string) error {
	now := time.Now().Unix()
	if id == "" {
		_, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at = $1 WHERE workspace_id = $2 AND read_at = 0`, now, wsID)
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at = $1 WHERE id = $2 AND workspace_id = $3`, now, id, wsID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRules returns all rules for a workspace.
func (s *Store) ListRules(ctx context.Context, wsID string) ([]Rule, error) {
	if wsID == "" {
		wsID = "default"
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, source, kind, channels::text, priority, quiet_start_min, quiet_end_min, enabled
FROM notification_rules WHERE workspace_id = $1 ORDER BY enabled DESC, source, kind
`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var chJSON string
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.Source, &r.Kind, &chJSON, &r.Priority, &r.QuietStartMin, &r.QuietEndMin, &r.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(chJSON), &r.Channels)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertRule creates or updates a rule (id is the key).
func (s *Store) UpsertRule(ctx context.Context, r *Rule) error {
	if r.WorkspaceID == "" {
		r.WorkspaceID = "default"
	}
	if len(r.Channels) == 0 {
		r.Channels = []string{string(ChannelInbox)}
	}
	chJSON, _ := json.Marshal(r.Channels)
	_, err := s.pool.Exec(ctx, `
INSERT INTO notification_rules (id, workspace_id, source, kind, channels, priority, quiet_start_min, quiet_end_min, enabled)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
	source=EXCLUDED.source, kind=EXCLUDED.kind, channels=EXCLUDED.channels,
	priority=EXCLUDED.priority, quiet_start_min=EXCLUDED.quiet_start_min,
	quiet_end_min=EXCLUDED.quiet_end_min, enabled=EXCLUDED.enabled
`, r.ID, r.WorkspaceID, r.Source, r.Kind, string(chJSON), r.Priority, r.QuietStartMin, r.QuietEndMin, r.Enabled)
	return err
}

// matchRule finds the first enabled rule matching (source, kind). Wildcards
// (empty source/kind on the rule) match anything. Returns nil if no match.
func (s *Store) matchRule(ctx context.Context, wsID, source, kind string) (*Rule, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, source, kind, channels::text, priority, quiet_start_min, quiet_end_min, enabled
FROM notification_rules
WHERE workspace_id = $1 AND enabled = TRUE
  AND (source = '' OR source = $2)
  AND (kind = '' OR kind = $3)
ORDER BY (source = $2 AND kind = $3) DESC, (source = $2) DESC, (kind = $3) DESC
LIMIT 1
`, wsID, source, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Rule
		var chJSON string
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.Source, &r.Kind, &chJSON, &r.Priority, &r.QuietStartMin, &r.QuietEndMin, &r.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(chJSON), &r.Channels)
		return &r, nil
	}
	return nil, rows.Err()
}

// ---- Sender interface + implementations ----

// Sender delivers a notification payload to one channel. Implementations:
// WebsocketSender (foreground), NoopPushSender (APNs/FCM placeholder).
type Sender interface {
	Send(ctx context.Context, channel Channel, n *Notification, pushToken string) error
}

// Broadcaster is the subset of ws.Hub notifycenter needs. Defining it here
// avoids a circular import (websocket → server → notifycenter → websocket).
// The server package adapts *ws.Hub to this interface at wiring time.
type Broadcaster interface {
	Broadcast(msgType string, payload any)
}

// WebsocketSender pushes to the foreground via the injected Broadcaster.
// It only fires for ChannelWebsocket; other channels are ignored.
type WebsocketSender struct {
	hub Broadcaster
}

// NewWebsocketSender constructs a sender backed by a Broadcaster (ws.Hub).
func NewWebsocketSender(hub Broadcaster) *WebsocketSender {
	return &WebsocketSender{hub: hub}
}

func (w *WebsocketSender) Send(ctx context.Context, ch Channel, n *Notification, _ string) error {
	if ch != ChannelWebsocket || w.hub == nil {
		return nil
	}
	w.hub.Broadcast("notification", n)
	return nil
}

// NoopSender discards everything. Used when no real push is configured.
type NoopSender struct{}

func (NoopSender) Send(context.Context, Channel, *Notification, string) error { return nil }

// MultiSender fans out to multiple senders (e.g. websocket + apns).
type MultiSender struct {
	senders []Sender
}

func NewMultiSender(senders ...Sender) *MultiSender { return &MultiSender{senders: senders} }
func (m *MultiSender) Send(ctx context.Context, ch Channel, n *Notification, token string) error {
	var firstErr error
	for _, s := range m.senders {
		if err := s.Send(ctx, ch, n, token); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// storeLike is the subset of *Store the Service needs. Making it an interface
// lets unit tests inject a fake without PG. *Store satisfies it.
type storeLike interface {
	matchRule(ctx context.Context, wsID, source, kind string) (*Rule, error)
	InsertNotification(ctx context.Context, n *Notification) error
}

// ---- Service: the orchestration entrypoint ----

// Service is what handlers call. Dispatch decides routing via rules + sends.
type Service struct {
	store  storeLike
	sender Sender
}

// NewService constructs the Service with the real *Store. sender may be nil.
func NewService(store *Store, sender Sender) *Service {
	if sender == nil {
		sender = NoopSender{}
	}
	return &Service{store: store, sender: sender}
}

// newServiceWithStore is the test-friendly constructor accepting any storeLike.
func newServiceWithStore(store storeLike, sender Sender) *Service {
	if sender == nil {
		sender = NoopSender{}
	}
	return &Service{store: store, sender: sender}
}

// DispatchResult records what happened for one event.
type DispatchResult struct {
	NotificationID string
	Rule           *Rule
	Suppressed     bool // true if no rule matched OR quiet hours applied
}

// Dispatch processes one event: match rule → persist notification → fan out
// to channels. Returns DispatchResult even when suppressed (so caller can log).
//
// Notifications ALWAYS get an inbox row when they survive the rule filter —
// the inbox is the source of truth for "what happened". Channels are best-effort.
func (svc *Service) Dispatch(ctx context.Context, ev Event) (*DispatchResult, error) {
	rule, err := svc.store.matchRule(ctx, ev.WorkspaceID, ev.Source, ev.Kind)
	if err != nil {
		return nil, fmt.Errorf("match rule: %w", err)
	}
	res := &DispatchResult{Rule: rule}
	if rule == nil {
		// No rule matched → drop. (Add a default "catch-all" rule to keep.)
		res.Suppressed = true
		return res, nil
	}

	// Build the notification row from event + rule.
	priority := ev.Priority
	if priority == "" {
		priority = rule.Priority
	}
	n := &Notification{
		ID:          genID("ntf"),
		WorkspaceID: orDefault(ev.WorkspaceID, "default"),
		UserID:      ev.UserID,
		Source:      ev.Source,
		Kind:        ev.Kind,
		Title:       ev.Title,
		Body:        ev.Body,
		Payload:     ev.Payload,
		Priority:    priority,
		CreatedAt:   time.Now().Unix(),
	}
	res.NotificationID = n.ID

	// Quiet hours: if the rule defines a window and we're in it, suppress
	// push channels (apns/fcm/websocket) but STILL write the inbox row so
	// the user sees it later. Urgent priority bypasses quiet hours.
	if inQuietWindow(rule.QuietStartMin, rule.QuietEndMin) && priority != "urgent" {
		n.Priority = priority + "_quiet" // tag for UI styling
		if err := svc.store.InsertNotification(ctx, n); err != nil {
			return res, err
		}
		res.Suppressed = true
		return res, nil
	}

	// Persist inbox row first (source of truth).
	if err := svc.store.InsertNotification(ctx, n); err != nil {
		return res, err
	}

	// Fan out to channels. Push token lookup is the sender's job; we pass "".
	for _, ch := range rule.Channels {
		if err := svc.sender.Send(ctx, Channel(ch), n, ""); err != nil {
			// Log + continue; one channel failing must not block others.
			log.Printf("notifycenter: send %s failed: %v", ch, err)
		}
	}
	return res, nil
}

// ---- helpers ----

func genID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// inQuietWindow reports whether "now" (local) falls in [start, end) minute-of-day.
// start==end means no window. Handles overnight wrap (e.g. 22:00→07:00).
func inQuietWindow(startMin, endMin int) bool {
	if startMin == endMin {
		return false
	}
	now := time.Now()
	cur := now.Hour()*60 + now.Minute()
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	// overnight wrap
	return cur >= startMin || cur < endMin
}
