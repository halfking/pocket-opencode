package learning

// PostgreSQL persistence for the learning domain. Follows the same conventions
// as the rest of the repository (see ADR-005): the schema is created
// idempotently at construction time, there is no migration framework, and
// every statement is scoped by workspace_id + user_id.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a learning row does not exist for the caller.
var ErrNotFound = errors.New("learning: not found")

// Store is the PostgreSQL-backed learning store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore accepts the shared pocketd pool and ensures the schema.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.EnsureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("learning schema: %w", err)
	}
	return s, nil
}

// EnsureSchema creates the learning tables. Both idempotency-critical indexes
// are part of the contract, not an optimisation:
//
//   - idx_learning_items_source makes "capture the same email twice" a no-op
//     (ADR-004) — it is a partial unique index over the source reference.
//   - idx_learning_reminders_idem keeps one reminder per (user, kind, item)
//     even when a scheduler tick and a user tap race each other.
func (s *Store) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS learning_items (
			id           TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			user_id      TEXT NOT NULL,
			source_kind  TEXT NOT NULL,
			source_id    TEXT NOT NULL DEFAULT '',
			title        TEXT NOT NULL,
			summary      TEXT NOT NULL DEFAULT '',
			deck_id      TEXT NOT NULL DEFAULT '',
			stage        TEXT NOT NULL DEFAULT 'inbox',
			importance   INT NOT NULL DEFAULT 3,
			captured_at  BIGINT NOT NULL,
			updated_at   BIGINT NOT NULL,
			deleted_at   BIGINT NOT NULL DEFAULT 0
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_items_source
			ON learning_items(workspace_id, user_id, source_kind, source_id)
			WHERE deleted_at = 0;
		CREATE INDEX IF NOT EXISTS idx_learning_items_stage
			ON learning_items(workspace_id, user_id, stage, captured_at DESC);
		-- tags carries the source vocabulary across (notes/rss categories), so a
		-- learning item is findable with the same words as its origin.
		ALTER TABLE learning_items ADD COLUMN IF NOT EXISTS tags TEXT NOT NULL DEFAULT '';

		CREATE TABLE IF NOT EXISTS learning_reminders (
			id            TEXT PRIMARY KEY,
			workspace_id  TEXT NOT NULL,
			user_id       TEXT NOT NULL,
			kind          TEXT NOT NULL,
			item_id       TEXT NOT NULL DEFAULT '',
			card_id       TEXT NOT NULL DEFAULT '',
			rule_kind     TEXT NOT NULL,
			rule_value    TEXT NOT NULL DEFAULT '',
			next_due_at   BIGINT NOT NULL,
			state         TEXT NOT NULL DEFAULT 'pending',
			last_sent_at  BIGINT NOT NULL DEFAULT 0,
			snoozed_until BIGINT NOT NULL DEFAULT 0,
			created_at    BIGINT NOT NULL,
			updated_at    BIGINT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_reminders_idem
			ON learning_reminders(workspace_id, user_id, kind, item_id);
		CREATE INDEX IF NOT EXISTS idx_learning_reminders_due
			ON learning_reminders(workspace_id, user_id, state, next_due_at);
	`)
	return err
}

// Close releases the pool reference. The pool itself is owned by pocketd.
func (s *Store) Close() error { return nil }

const learningItemColumns = `id, workspace_id, user_id, source_kind, source_id, title, summary, deck_id, stage, importance, tags, captured_at, updated_at, deleted_at`

func scanLearningItem(row interface{ Scan(dest ...any) error }) (*LearningItem, error) {
	var it LearningItem
	var tags string
	if err := row.Scan(&it.ID, &it.WorkspaceID, &it.UserID, &it.SourceKind, &it.SourceID,
		&it.Title, &it.Summary, &it.DeckID, &it.Stage, &it.Importance, &tags,
		&it.CapturedAt, &it.UpdatedAt, &it.DeletedAt); err != nil {
		return nil, err
	}
	it.Tags = decodeTags(tags)
	return &it, nil
}

// encodeTags serialises tags as a JSON array string, matching the convention the
// notes domain already uses for its tags column.
func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeTags is the read counterpart; a malformed column degrades to no tags
// rather than failing the list.
func decodeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// CaptureItem inserts a learning item, or returns the existing one when the
// same source was already captured. created=true distinguishes the two.
//
// The upsert only refreshes the fields the caller is allowed to change
// (title/summary/deck/stage/importance); captured_at and id stay with the
// first capture so "when did I start learning this" is stable.
func (s *Store) CaptureItem(ctx context.Context, item *LearningItem) (existing *LearningItem, err error) {
	ws := normalizeWorkspace(item.WorkspaceID)
	now := time.Now().Unix()
	item.WorkspaceID = ws
	if item.CapturedAt == 0 {
		item.CapturedAt = now
	}
	item.UpdatedAt = now
	if item.Stage == "" {
		item.Stage = string(StageInbox)
	}
	if item.Importance == 0 {
		item.Importance = 3
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO learning_items (id, workspace_id, user_id, source_kind, source_id, title, summary, deck_id, stage, importance, tags, captured_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 0)
		ON CONFLICT (workspace_id, user_id, source_kind, source_id) WHERE deleted_at = 0
		DO UPDATE SET
			title      = EXCLUDED.title,
			summary    = EXCLUDED.summary,
			deck_id    = EXCLUDED.deck_id,
			stage      = EXCLUDED.stage,
			importance = EXCLUDED.importance,
			tags       = EXCLUDED.tags,
			updated_at = EXCLUDED.updated_at
		RETURNING `+learningItemColumns+`, (xmax = 0) AS inserted
	`, item.ID, ws, item.UserID, item.SourceKind, item.SourceID, item.Title, item.Summary,
		item.DeckID, item.Stage, item.Importance, encodeTags(item.Tags), item.CapturedAt, now)

	// pgx's QueryRow.Scan may only be called once, so build the full
	// destination list (row columns + the inserted flag) up front.
	got := &LearningItem{}
	var inserted bool
	if err := row.Scan(append(destLearningItem(got), &inserted)...); err != nil {
		return nil, fmt.Errorf("capture learning item: %w", err)
	}
	*item = *got
	if !inserted {
		return got, nil
	}
	return nil, nil
}

// destLearningItem builds the scan destinations in learningItemColumns order.
func destLearningItem(it *LearningItem) []any {
	return []any{&it.ID, &it.WorkspaceID, &it.UserID, &it.SourceKind, &it.SourceID,
		&it.Title, &it.Summary, &it.DeckID, &it.Stage, &it.Importance, new(string),
		&it.CapturedAt, &it.UpdatedAt, &it.DeletedAt}
}

// ListItems returns the caller's learning items, newest first. Empty filters
// mean "no filter"; limit is capped so a client cannot ask for the whole table.
func (s *Store) ListItems(ctx context.Context, wsID, userID, stage, sourceKind string, limit int) ([]LearningItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + learningItemColumns + ` FROM learning_items
	      WHERE workspace_id = $1 AND user_id = $2 AND deleted_at = 0`
	args := []any{normalizeWorkspace(wsID), userID}
	if stage != "" {
		args = append(args, stage)
		q += fmt.Sprintf(" AND stage = $%d", len(args))
	}
	if sourceKind != "" {
		args = append(args, sourceKind)
		q += fmt.Sprintf(" AND source_kind = $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY captured_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list learning items: %w", err)
	}
	defer rows.Close()
	out := []LearningItem{}
	for rows.Next() {
		it, err := scanLearningItem(rows)
		if err != nil {
			return nil, fmt.Errorf("list learning items: %w", err)
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// CountByStage returns how many of the caller's items sit in each stage. It is
// the inbox counter behind the learning hub and the daily digest.
func (s *Store) CountByStage(ctx context.Context, wsID, userID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT stage, count(*)
		FROM learning_items
		WHERE workspace_id = $1 AND user_id = $2 AND deleted_at = 0
		GROUP BY stage
	`, normalizeWorkspace(wsID), userID)
	if err != nil {
		return nil, fmt.Errorf("count learning items by stage: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var stage string
		var n int
		if err := rows.Scan(&stage, &n); err != nil {
			return nil, fmt.Errorf("count learning items by stage: %w", err)
		}
		out[stage] = n
	}
	return out, rows.Err()
}

// UpdateStage moves an item through the learning funnel.
func (s *Store) UpdateStage(ctx context.Context, wsID, userID, id, stage string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE learning_items SET stage = $1, updated_at = $2
		WHERE id = $3 AND workspace_id = $4 AND user_id = $5 AND deleted_at = 0
	`, stage, time.Now().Unix(), id, normalizeWorkspace(wsID), userID)
	if err != nil {
		return fmt.Errorf("update learning stage: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const learningReminderColumns = `id, workspace_id, user_id, kind, item_id, card_id, rule_kind, rule_value, next_due_at, state, last_sent_at, snoozed_until, created_at, updated_at`

func scanReminder(row interface{ Scan(dest ...any) error }) (*Reminder, error) {
	var r Reminder
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.UserID, &r.Kind, &r.ItemID, &r.CardID,
		&r.RuleKind, &r.RuleValue, &r.NextDueAt, &r.State, &r.LastSentAt,
		&r.SnoozedUntil, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpsertReminder writes a reminder idempotently on
// (workspace_id, user_id, kind, item_id). A repeated POST updates the schedule
// instead of inserting a second row, so the user can re-save their settings
// without creating duplicate nudges (ADR-004).
func (s *Store) UpsertReminder(ctx context.Context, r *Reminder) (*Reminder, error) {
	ws := normalizeWorkspace(r.WorkspaceID)
	now := time.Now().Unix()
	if r.State == "" {
		r.State = string(ReminderPending)
	}
	created := now

	row := s.pool.QueryRow(ctx, `
		INSERT INTO learning_reminders (id, workspace_id, user_id, kind, item_id, card_id, rule_kind, rule_value, next_due_at, state, last_sent_at, snoozed_until, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)
		ON CONFLICT (workspace_id, user_id, kind, item_id) DO UPDATE SET
			rule_kind   = EXCLUDED.rule_kind,
			rule_value  = EXCLUDED.rule_value,
			next_due_at = EXCLUDED.next_due_at,
			card_id     = EXCLUDED.card_id,
			updated_at  = EXCLUDED.updated_at
		RETURNING `+learningReminderColumns+`
	`, r.ID, ws, r.UserID, r.Kind, r.ItemID, r.CardID, r.RuleKind, r.RuleValue,
		r.NextDueAt, r.State, r.LastSentAt, r.SnoozedUntil, created)

	out, err := scanReminder(row)
	if err != nil {
		return nil, fmt.Errorf("upsert learning reminder: %w", err)
	}
	return out, nil
}

// ListReminders returns the caller's reminders, optionally filtered by state.
func (s *Store) ListReminders(ctx context.Context, wsID, userID, state string, limit int) ([]Reminder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + learningReminderColumns + ` FROM learning_reminders
	      WHERE workspace_id = $1 AND user_id = $2`
	args := []any{normalizeWorkspace(wsID), userID}
	if state != "" {
		args = append(args, state)
		q += fmt.Sprintf(" AND state = $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY next_due_at ASC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list learning reminders: %w", err)
	}
	defer rows.Close()
	out := []Reminder{}
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, fmt.Errorf("list learning reminders: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DueReminders returns reminders whose next_due_at has passed and that are not
// in a terminal or snoozed-until-later state. A snooze is respected by moving
// next_due_at forward, so there is no second "snoozed queue" to reconcile.
func (s *Store) DueReminders(ctx context.Context, wsID, userID string, now int64, limit int) ([]Reminder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+learningReminderColumns+` FROM learning_reminders
		WHERE workspace_id = $1 AND user_id = $2
		  AND state IN ('pending', 'snoozed', 'sent')
		  AND next_due_at <= $3
		ORDER BY next_due_at ASC
		LIMIT $4
	`, normalizeWorkspace(wsID), userID, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due learning reminders: %w", err)
	}
	defer rows.Close()
	out := []Reminder{}
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, fmt.Errorf("list due learning reminders: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// MarkReminderSent records a delivery attempt and returns the reminder to
// pending with the next occurrence already computed by the service.
func (s *Store) MarkReminderSent(ctx context.Context, wsID, userID, id string, nextDueAt int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE learning_reminders
		SET state = 'pending', last_sent_at = $1, next_due_at = $2, updated_at = $1
		WHERE id = $3 AND workspace_id = $4 AND user_id = $5
	`, time.Now().Unix(), nextDueAt, id, normalizeWorkspace(wsID), userID)
	if err != nil {
		return fmt.Errorf("mark learning reminder sent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SnoozeReminder pushes a reminder into the future. A non-positive duration is
// rejected so a bad client value cannot make a reminder permanently silent.
//
// The new time is measured from the reminder's own next_due_at (floored at
// now), not from now. Measuring from now meant a snooze could pull a reminder
// *forward*: snoozing a daily digest that was due in 24 hours by two hours
// rescheduled it to two hours from now, so the "later" the user asked for
// arrived twenty-two hours earlier than the rule intended — and the daily
// schedule stopped matching RuleValue. The GREATEST floor also keeps an
// already-overdue reminder from staying overdue when snoozed by a short
// duration.
func (s *Store) SnoozeReminder(ctx context.Context, wsID, userID, id string, minutes int64) (int64, error) {
	if minutes <= 0 {
		return 0, fmt.Errorf("snooze minutes must be positive")
	}
	now := time.Now().Unix()
	var until int64
	err := s.pool.QueryRow(ctx, `
		UPDATE learning_reminders
		SET state = 'snoozed',
		    snoozed_until = GREATEST(next_due_at, $1) + $2 * 60,
		    next_due_at = GREATEST(next_due_at, $1) + $2 * 60,
		    updated_at = $1
		WHERE id = $3 AND workspace_id = $4 AND user_id = $5
		RETURNING next_due_at
	`, now, minutes, id, normalizeWorkspace(wsID), userID).Scan(&until)
	if err == pgx.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("snooze learning reminder: %w", err)
	}
	return until, nil
}

// AckReminder stops a reminder from firing again. Unlike a snooze this is
// terminal for the reminder row: the user said "I have seen this", not "later".
func (s *Store) AckReminder(ctx context.Context, wsID, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE learning_reminders SET state = 'acked', updated_at = $1
		WHERE id = $2 AND workspace_id = $3 AND user_id = $4
	`, time.Now().Unix(), id, normalizeWorkspace(wsID), userID)
	if err != nil {
		return fmt.Errorf("ack learning reminder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveDayTimestamps returns every timestamp at which the user did something
// in the learning domain since sinceUnix: capturing an item, or moving one
// through the funnel (updated_at).
//
// Raw timestamps are returned rather than day indices on purpose. Grouping by
// day needs the caller's timezone offset, and doing the arithmetic in SQL with
// an offset spliced in is where off-by-one-day bugs live. The Go side owns
// DayIndex instead, and it is unit-tested.
//
// sinceUnix is applied **per timestamp**, not per row. The WHERE clause keeps
// any row that has at least one timestamp inside the window, but a row that
// was captured long ago and updated just now carries both, and emitting the
// old captured_at would credit activity before the caller's window — which is
// the streak's input, so it inflates the streak rather than merely returning
// an extra number.
func (s *Store) ActiveDayTimestamps(ctx context.Context, wsID, userID string, sinceUnix int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT captured_at, updated_at
		FROM learning_items
		WHERE workspace_id = $1 AND user_id = $2 AND deleted_at = 0
		  AND (captured_at >= $3 OR updated_at >= $3)`,
		normalizeWorkspace(wsID), userID, sinceUnix)
	if err != nil {
		return nil, fmt.Errorf("active day timestamps: %w", err)
	}
	defer rows.Close()

	out := []int64{}
	for rows.Next() {
		var capturedAt, updatedAt int64
		if err := rows.Scan(&capturedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("active day timestamps: scan: %w", err)
		}
		if capturedAt >= sinceUnix && capturedAt > 0 {
			out = append(out, capturedAt)
		}
		if updatedAt >= sinceUnix && updatedAt > 0 {
			out = append(out, updatedAt)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active day timestamps: %w", err)
	}
	return out, nil
}

// ClaimMilestone records that a streak milestone has been announced, and
// reports whether this call was the one that claimed it.
//
// The claim is a row in learning_reminders with kind=streak and
// item_id="milestone:N". The existing unique index
// (workspace_id, user_id, kind, item_id) then makes it exactly-once per user
// per milestone, enforced by PostgreSQL rather than by a check-then-insert race
// in the executor. Reusing the reminders table avoids adding another table
// whose DDL would go unverified alongside everything else.
//
// The synthetic id MUST include the workspace. It used to be
// "ms-<key>-<userID>", and that is a live bug: ON CONFLICT names the unique
// *index*, not the primary key, so a second workspace claiming the same
// milestone for the same user collided on the primary key instead — 23505,
// unhandled by that ON CONFLICT clause, and the milestone was never announced
// in that workspace. Existing rows keep working: within one workspace the
// index still suppresses the re-claim regardless of the id it carries.
func (s *Store) ClaimMilestone(ctx context.Context, wsID, userID, itemID string, now int64) (bool, error) {
	key := strings.TrimSpace(itemID)
	if key == "" {
		return false, fmt.Errorf("claim milestone: item id is required")
	}
	workspace := normalizeWorkspace(wsID)
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO learning_reminders
			(id, workspace_id, user_id, kind, item_id, rule_kind, rule_value, next_due_at, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (workspace_id, user_id, kind, item_id) DO NOTHING`,
		"ms-"+workspace+"-"+key+"-"+userID, workspace, userID, string(ReminderStreak), key,
		string(RuleOnce), "", now, string(ReminderAcked), now)
	if err != nil {
		return false, fmt.Errorf("claim milestone: %w", err)
	}
	// RowsAffected is 1 only when the insert was not suppressed by the conflict
	// clause, which is exactly "this call claimed it".
	return tag.RowsAffected() == 1, nil
}

// CountPendingReminders counts the reminders still armed for a user. Used by
// tests and diagnostics; the digest itself uses DueReminders.
func (s *Store) CountPendingReminders(ctx context.Context, wsID, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM learning_reminders
		WHERE workspace_id = $1 AND user_id = $2 AND state IN ('pending', 'snoozed')
	`, normalizeWorkspace(wsID), userID).Scan(&n)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("count pending learning reminders: %w", err)
	}
	return n, nil
}
