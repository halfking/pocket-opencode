package flashcards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the pgxpool with all four flashcard tables from the v1
// contract (§1.1–§1.4). Schema is created idempotently via EnsureSchema
// (CREATE TABLE IF NOT EXISTS); main.go is expected to call EnsureSchema
// once after construction. All write paths increment usn and refresh
// updated_at so the LWW sync rule (§1.5) holds.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore constructs a Store. It does NOT call EnsureSchema so callers
// that want lazy schema bring-up (tests, one-shot CLIs) can defer it.
// Production paths in cmd/pocketd/main.go call EnsureSchema immediately.
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("flashcards: pool is nil")
	}
	s := &Store{pool: pool, now: time.Now}
	return s, nil
}

// SetClock replaces the time source. Tests can use a fixed clock.
func (s *Store) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Pool exposes the underlying pool for executor side-channels (e.g. the
// daily-review executor might want to count due cards without going
// through the public Store surface).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Now returns the store's clock value as a unix-second int64.
func (s *Store) Now() int64 { return s.now().Unix() }

// EnsureSchema creates the four flashcard tables from the v1 contract.
// All statements are idempotent so this can run on every startup.
func (s *Store) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS flashcard_notes (
			id          TEXT PRIMARY KEY,
			user_id     TEXT NOT NULL,
			deck_id     TEXT NOT NULL,
			front       TEXT NOT NULL,
			back        TEXT NOT NULL,
			tags        TEXT NOT NULL DEFAULT '[]',
			usn         BIGINT NOT NULL DEFAULT 0,
			created_at  BIGINT NOT NULL,
			updated_at  BIGINT NOT NULL,
			deleted_at  BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS flashcard_cards (
			id              TEXT PRIMARY KEY,
			note_id         TEXT NOT NULL REFERENCES flashcard_notes(id) ON DELETE CASCADE,
			user_id         TEXT NOT NULL,
			deck_id         TEXT NOT NULL,
			state           SMALLINT NOT NULL DEFAULT 0,
			due             BIGINT NOT NULL DEFAULT 0,
			interval_days   REAL NOT NULL DEFAULT 0,
			stability       REAL NOT NULL DEFAULT 0,
			difficulty      REAL NOT NULL DEFAULT 0,
			reps            INTEGER NOT NULL DEFAULT 0,
			lapses          INTEGER NOT NULL DEFAULT 0,
			last_review_at  BIGINT,
			usn             BIGINT NOT NULL DEFAULT 0,
			created_at      BIGINT NOT NULL,
			updated_at      BIGINT NOT NULL,
			deleted_at      BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS flashcard_revlog (
			id              TEXT PRIMARY KEY,
			card_id         TEXT NOT NULL,
			user_id         TEXT NOT NULL,
			reviewed_at     BIGINT NOT NULL,
			rating          SMALLINT NOT NULL,
			prev_state      SMALLINT NOT NULL,
			next_state      SMALLINT NOT NULL,
			prev_interval   REAL,
			next_interval   REAL,
			elapsed_days    INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS flashcard_deck_config (
			deck_id                  TEXT NOT NULL,
			user_id                  TEXT NOT NULL,
			name                     TEXT NOT NULL,
			new_per_day              INTEGER NOT NULL DEFAULT 20,
			reviews_per_day          INTEGER NOT NULL DEFAULT 200,
			learning_steps_min       INTEGER[] NOT NULL DEFAULT '{1,10}',
			graduating_interval_days INTEGER NOT NULL DEFAULT 1,
			easy_interval_days       INTEGER NOT NULL DEFAULT 4,
			fsrs_weights             REAL[],
			desired_retention        REAL NOT NULL DEFAULT 0.9,
			usn                      BIGINT NOT NULL DEFAULT 0,
			created_at               BIGINT NOT NULL,
			updated_at               BIGINT NOT NULL,
			deleted_at               BIGINT,
			PRIMARY KEY (deck_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_notes_user_updated ON flashcard_notes(user_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_notes_user_deleted ON flashcard_notes(user_id, deleted_at) WHERE deleted_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_cards_user_updated ON flashcard_cards(user_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_cards_user_deleted ON flashcard_cards(user_id, deleted_at) WHERE deleted_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_cards_user_deck_due ON flashcard_cards(user_id, deck_id, due)`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_revlog_card ON flashcard_revlog(card_id, reviewed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_flashcard_deck_user_updated ON flashcard_deck_config(user_id, updated_at DESC)`,
	}
	for _, q := range stmts {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("flashcards schema: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Notes
// ---------------------------------------------------------------------------

// CreateNote inserts a new note. The caller supplies the id (server-side
// ULID generation lives in the handler). updated_at / created_at / usn are
// stamped from the store clock.
func (s *Store) CreateNote(ctx context.Context, n *Note) error {
	now := s.Now()
	if n.CreatedAt == 0 {
		n.CreatedAt = now
	}
	if n.UpdatedAt == 0 {
		n.UpdatedAt = now
	}
	n.Usn++
	tags := n.Tags
	if tags == "" {
		tags = "[]"
	}
	if !json.Valid([]byte(tags)) {
		tags = "[]"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO flashcard_notes
			(id, user_id, deck_id, front, back, tags, usn, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		n.ID, n.UserID, n.DeckID, n.Front, n.Back, tags, n.Usn, n.CreatedAt, n.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create note %s: %w", n.ID, err)
	}
	return nil
}

// GetNote fetches a single non-deleted note by id+user. Returns (nil, nil)
// on miss so handlers can map to 404 without error-type sniffing.
func (s *Store) GetNote(ctx context.Context, userID, id string) (*Note, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, deck_id, front, back, tags, usn, created_at, updated_at,
		       COALESCE(deleted_at, 0)
		FROM flashcard_notes WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID,
	)
	var n Note
	if err := row.Scan(&n.ID, &n.UserID, &n.DeckID, &n.Front, &n.Back, &n.Tags,
		&n.Usn, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get note %s: %w", id, err)
	}
	return &n, nil
}

// ListNotesSince returns notes with updated_at > sinceSec, capped at limit.
func (s *Store) ListNotesSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]*Note, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, deck_id, front, back, tags, usn, created_at, updated_at,
		       COALESCE(deleted_at, 0)
		FROM flashcard_notes
		WHERE user_id=$1 AND deleted_at IS NULL AND updated_at > $2
		ORDER BY updated_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()
	var out []*Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.DeckID, &n.Front, &n.Back, &n.Tags,
			&n.Usn, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// ListDeletedNotesSince returns ids whose deleted_at > sinceSec, capped.
func (s *Store) ListDeletedNotesSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]string, error) {
	if sinceSec <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM flashcard_notes
		WHERE user_id=$1 AND deleted_at IS NOT NULL AND deleted_at > $2
		ORDER BY deleted_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list deleted notes: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdateNote patches front/back/tags and bumps usn+updated_at.
func (s *Store) UpdateNote(ctx context.Context, userID, id string, front, back *string, tags *string) (*Note, error) {
	now := s.Now()
	cur, err := s.GetNote(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, nil
	}
	if front != nil {
		cur.Front = *front
	}
	if back != nil {
		cur.Back = *back
	}
	if tags != nil {
		if !json.Valid([]byte(*tags)) {
			cur.Tags = "[]"
		} else {
			cur.Tags = *tags
		}
	}
	cur.UpdatedAt = now
	cur.Usn++
	_, err = s.pool.Exec(ctx, `
		UPDATE flashcard_notes SET front=$3, back=$4, tags=$5, usn=$6, updated_at=$7
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, cur.Front, cur.Back, cur.Tags, cur.Usn, cur.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update note %s: %w", id, err)
	}
	return cur, nil
}

// SoftDeleteNote stamps deleted_at on the note and cascades to its cards.
// usn is incremented on both note and cards per §1.5.
func (s *Store) SoftDeleteNote(ctx context.Context, userID, id string) error {
	now := s.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE flashcard_notes SET deleted_at=$3, usn=usn+1, updated_at=$3
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, now)
	if err != nil {
		return fmt.Errorf("soft-delete note %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil // already deleted or not owned
	}
	if _, err := tx.Exec(ctx, `
		UPDATE flashcard_cards SET deleted_at=$3, usn=usn+1, updated_at=$3
		WHERE note_id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, now); err != nil {
		return fmt.Errorf("cascade soft-delete cards for note %s: %w", id, err)
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Cards
// ---------------------------------------------------------------------------

// CreateCard inserts a new card.
func (s *Store) CreateCard(ctx context.Context, c *Card) error {
	now := s.Now()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	if c.UpdatedAt == 0 {
		c.UpdatedAt = now
	}
	c.Usn++
	_, err := s.pool.Exec(ctx, `
		INSERT INTO flashcard_cards
			(id, note_id, user_id, deck_id, state, due, interval_days, stability, difficulty,
			 reps, lapses, last_review_at, usn, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		c.ID, c.NoteID, c.UserID, c.DeckID, c.State, c.Due, c.IntervalDays, c.Stability,
		c.Difficulty, c.Reps, c.Lapses, nullIfZero(c.LastReviewAt), c.Usn, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create card %s: %w", c.ID, err)
	}
	return nil
}

// GetCard returns a single non-deleted card.
func (s *Store) GetCard(ctx context.Context, userID, id string) (*Card, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, note_id, user_id, deck_id, state, due, interval_days, stability, difficulty,
		       reps, lapses, COALESCE(last_review_at, 0), usn, created_at, updated_at,
		       COALESCE(deleted_at, 0)
		FROM flashcard_cards WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID)
	var c Card
	if err := row.Scan(&c.ID, &c.NoteID, &c.UserID, &c.DeckID, &c.State, &c.Due,
		&c.IntervalDays, &c.Stability, &c.Difficulty, &c.Reps, &c.Lapses,
		&c.LastReviewAt, &c.Usn, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get card %s: %w", id, err)
	}
	return &c, nil
}

// ListCardsSince returns non-deleted cards updated after sinceSec.
func (s *Store) ListCardsSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]*Card, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, note_id, user_id, deck_id, state, due, interval_days, stability, difficulty,
		       reps, lapses, COALESCE(last_review_at, 0), usn, created_at, updated_at,
		       COALESCE(deleted_at, 0)
		FROM flashcard_cards
		WHERE user_id=$1 AND deleted_at IS NULL AND updated_at > $2
		ORDER BY updated_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list cards: %w", err)
	}
	defer rows.Close()
	var out []*Card
	for rows.Next() {
		var c Card
		if err := rows.Scan(&c.ID, &c.NoteID, &c.UserID, &c.DeckID, &c.State, &c.Due,
			&c.IntervalDays, &c.Stability, &c.Difficulty, &c.Reps, &c.Lapses,
			&c.LastReviewAt, &c.Usn, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan card: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// ListDeletedCardsSince returns tombstoned card ids.
func (s *Store) ListDeletedCardsSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]string, error) {
	if sinceSec <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM flashcard_cards
		WHERE user_id=$1 AND deleted_at IS NOT NULL AND deleted_at > $2
		ORDER BY deleted_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list deleted cards: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdateCard patches due/state and bumps usn+updated_at.
func (s *Store) UpdateCard(ctx context.Context, userID, id string, due *int64, state *int) (*Card, error) {
	now := s.Now()
	cur, err := s.GetCard(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, nil
	}
	if due != nil {
		cur.Due = *due
	}
	if state != nil {
		cur.State = *state
	}
	cur.UpdatedAt = now
	cur.Usn++
	_, err = s.pool.Exec(ctx, `
		UPDATE flashcard_cards SET due=$3, state=$4, usn=$5, updated_at=$6
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, cur.Due, cur.State, cur.Usn, cur.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update card %s: %w", id, err)
	}
	return cur, nil
}

// SoftDeleteCard stamps deleted_at on a single card.
func (s *Store) SoftDeleteCard(ctx context.Context, userID, id string) error {
	now := s.Now()
	_, err := s.pool.Exec(ctx, `
		UPDATE flashcard_cards SET deleted_at=$3, usn=usn+1, updated_at=$3
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, now)
	if err != nil {
		return fmt.Errorf("soft-delete card %s: %w", id, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DeckConfig
// ---------------------------------------------------------------------------

// GetDeckConfig fetches a single non-deleted deck config row.
func (s *Store) GetDeckConfig(ctx context.Context, userID, deckID string) (*DeckConfig, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT deck_id, user_id, name, new_per_day, reviews_per_day, learning_steps_min,
		       graduating_interval_days, easy_interval_days, fsrs_weights, desired_retention,
		       usn, created_at, updated_at, COALESCE(deleted_at, 0)
		FROM flashcard_deck_config
		WHERE deck_id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		deckID, userID)
	var d DeckConfig
	var steps []int32
	var weights []float32
	if err := row.Scan(&d.DeckID, &d.UserID, &d.Name, &d.NewPerDay, &d.ReviewsPerDay,
		&steps, &d.GraduatingIntervalDays, &d.EasyIntervalDays, &weights,
		&d.DesiredRetention, &d.Usn, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get deck config %s: %w", deckID, err)
	}
	for _, v := range steps {
		d.LearningStepsMin = append(d.LearningStepsMin, int(v))
	}
	d.FSRSWeights = weights
	return &d, nil
}

// UpsertDeckConfig inserts or replaces a deck config row. Bumps usn and
// updated_at. Defaults that arrive empty are filled with the contract's
// §1.4 defaults.
func (s *Store) UpsertDeckConfig(ctx context.Context, d *DeckConfig) error {
	now := s.Now()
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	if d.NewPerDay == 0 {
		d.NewPerDay = 20
	}
	if d.ReviewsPerDay == 0 {
		d.ReviewsPerDay = 200
	}
	if len(d.LearningStepsMin) == 0 {
		d.LearningStepsMin = []int{1, 10}
	}
	if d.GraduatingIntervalDays == 0 {
		d.GraduatingIntervalDays = 1
	}
	if d.EasyIntervalDays == 0 {
		d.EasyIntervalDays = 4
	}
	if d.DesiredRetention == 0 {
		d.DesiredRetention = 0.9
	}
	d.UpdatedAt = now
	d.Usn++

	// pgx wants int32 slice for INTEGER[] column.
	steps := make([]int32, 0, len(d.LearningStepsMin))
	for _, v := range d.LearningStepsMin {
		steps = append(steps, int32(v))
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO flashcard_deck_config
			(deck_id, user_id, name, new_per_day, reviews_per_day, learning_steps_min,
			 graduating_interval_days, easy_interval_days, fsrs_weights, desired_retention,
			 usn, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (deck_id, user_id) DO UPDATE SET
			name                     = EXCLUDED.name,
			new_per_day              = EXCLUDED.new_per_day,
			reviews_per_day          = EXCLUDED.reviews_per_day,
			learning_steps_min       = EXCLUDED.learning_steps_min,
			graduating_interval_days = EXCLUDED.graduating_interval_days,
			easy_interval_days       = EXCLUDED.easy_interval_days,
			fsrs_weights             = EXCLUDED.fsrs_weights,
			desired_retention        = EXCLUDED.desired_retention,
			usn                      = flashcard_deck_config.usn + 1,
			updated_at               = EXCLUDED.updated_at,
			deleted_at               = NULL`,
		d.DeckID, d.UserID, d.Name, d.NewPerDay, d.ReviewsPerDay, steps,
		d.GraduatingIntervalDays, d.EasyIntervalDays, d.FSRSWeights,
		d.DesiredRetention, d.Usn, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert deck config %s: %w", d.DeckID, err)
	}
	return nil
}

// ListDeckConfigsSince returns non-deleted deck configs updated after sinceSec.
func (s *Store) ListDeckConfigsSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]*DeckConfig, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT deck_id, user_id, name, new_per_day, reviews_per_day, learning_steps_min,
		       graduating_interval_days, easy_interval_days, fsrs_weights, desired_retention,
		       usn, created_at, updated_at, COALESCE(deleted_at, 0)
		FROM flashcard_deck_config
		WHERE user_id=$1 AND deleted_at IS NULL AND updated_at > $2
		ORDER BY updated_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list deck configs: %w", err)
	}
	defer rows.Close()
	var out []*DeckConfig
	for rows.Next() {
		var d DeckConfig
		var steps []int32
		var weights []float32
		if err := rows.Scan(&d.DeckID, &d.UserID, &d.Name, &d.NewPerDay, &d.ReviewsPerDay,
			&steps, &d.GraduatingIntervalDays, &d.EasyIntervalDays, &weights,
			&d.DesiredRetention, &d.Usn, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan deck config: %w", err)
		}
		for _, v := range steps {
			d.LearningStepsMin = append(d.LearningStepsMin, int(v))
		}
		d.FSRSWeights = weights
		out = append(out, &d)
	}
	return out, rows.Err()
}

// ListDeletedDeckConfigsSince returns tombstoned deck ids.
func (s *Store) ListDeletedDeckConfigsSince(ctx context.Context, userID string, sinceSec int64, limit int) ([]string, error) {
	if sinceSec <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT deck_id FROM flashcard_deck_config
		WHERE user_id=$1 AND deleted_at IS NOT NULL AND deleted_at > $2
		ORDER BY deleted_at ASC LIMIT $3`,
		userID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("list deleted deck configs: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// RevLog
// ---------------------------------------------------------------------------

// InsertRevLog appends a review log row. Caller supplies id (ULID).
func (s *Store) InsertRevLog(ctx context.Context, r *RevLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO flashcard_revlog
			(id, card_id, user_id, reviewed_at, rating, prev_state, next_state,
			 prev_interval, next_interval, elapsed_days)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.ID, r.CardID, r.UserID, r.ReviewedAt, r.Rating, r.PrevState, r.NextState,
		r.PrevInterval, r.NextInterval, r.ElapsedDays)
	if err != nil {
		return fmt.Errorf("insert revlog %s: %w", r.ID, err)
	}
	return nil
}

// ListRevLogsByCard returns logs for a card, newest first, capped at limit.
func (s *Store) ListRevLogsByCard(ctx context.Context, userID, cardID string, limit int) ([]*RevLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, card_id, user_id, reviewed_at, rating, prev_state, next_state,
		       COALESCE(prev_interval, 0), COALESCE(next_interval, 0),
		       COALESCE(elapsed_days, 0)
		FROM flashcard_revlog
		WHERE card_id=$1 AND user_id=$2
		ORDER BY reviewed_at DESC LIMIT $3`,
		cardID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list revlogs: %w", err)
	}
	defer rows.Close()
	var out []*RevLog
	for rows.Next() {
		var r RevLog
		if err := rows.Scan(&r.ID, &r.CardID, &r.UserID, &r.ReviewedAt, &r.Rating,
			&r.PrevState, &r.NextState, &r.PrevInterval, &r.NextInterval,
			&r.ElapsedDays); err != nil {
			return nil, fmt.Errorf("scan revlog: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// CountDueCards returns cards whose due <= nowSec and state in (new=0,
// learning=1, review=2 due as epoch-days). Used by the daily-review
// executor side-channel.
func (s *Store) CountDueCards(ctx context.Context, userID string, nowSec int64) (int, error) {
	// New + learning cards store due as unix seconds; review cards store due
	// as epoch-days. The reviewer UI flips the comparison per state. For a
	// coarse count we treat both forms together by converting the day form
	// to seconds (multiply by 86400).
	const secsPerDay = int64(86400)
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM flashcard_cards
		WHERE user_id=$1 AND deleted_at IS NULL AND (
			(state IN (0,1) AND due <= $2) OR
			(state = 2 AND due * $3 <= $2)
		)`,
		userID, nowSec, secsPerDay).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count due cards: %w", err)
	}
	return count, nil
}

// ListDueCardsInDeck returns due cards for a specific deck (used by the
// /api/flashcards/decks/:id/due endpoint).
func (s *Store) ListDueCardsInDeck(ctx context.Context, userID, deckID string, nowSec, limit int64) ([]*Card, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	const secsPerDay = int64(86400)
	rows, err := s.pool.Query(ctx, `
		SELECT id, note_id, user_id, deck_id, state, due, interval_days, stability, difficulty,
		       reps, lapses, COALESCE(last_review_at, 0), usn, created_at, updated_at,
		       COALESCE(deleted_at, 0)
		FROM flashcard_cards
		WHERE user_id=$1 AND deck_id=$2 AND deleted_at IS NULL AND (
			(state IN (0,1) AND due <= $3) OR
			(state = 2 AND due * $4 <= $3)
		)
		ORDER BY due ASC LIMIT $5`,
		userID, deckID, nowSec, secsPerDay, limit)
	if err != nil {
		return nil, fmt.Errorf("list due cards in deck: %w", err)
	}
	defer rows.Close()
	var out []*Card
	for rows.Next() {
		var c Card
		if err := rows.Scan(&c.ID, &c.NoteID, &c.UserID, &c.DeckID, &c.State, &c.Due,
			&c.IntervalDays, &c.Stability, &c.Difficulty, &c.Reps, &c.Lapses,
			&c.LastReviewAt, &c.Usn, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan due card: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// nullIfZero is a small helper so we don't store epoch=0 timestamps as a
// real value when the column is nullable (last_review_at).
func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
