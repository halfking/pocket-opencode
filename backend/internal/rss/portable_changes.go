package rss

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"time"
)

//go:embed portable_changes.sql
var portableSchema string

func (s *Store) migratePortable(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, portableSchema)
	return err
}

func (s *Store) IsPortableSource(ctx context.Context, id string, sc Scope) (bool, error) {
	if err := requireScope(sc); err != nil {
		return false, err
	}
	var managed bool
	err := s.pool.QueryRow(ctx, `SELECT portal_managed FROM rss_sources WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, id, sc.UserID, sc.WorkspaceID).Scan(&managed)
	if err == pgx.ErrNoRows {
		return false, ErrNotFound
	}
	return managed, err
}
func validPortableEpoch(epoch *int64) error {
	if epoch != nil && (*epoch < 0 || *epoch > 9007199254740991) {
		return fmt.Errorf("rss: invalid portable epoch")
	}
	return nil
}

// The scope lock precedes source/item row locks and sequence allocation, so
// a later visible cursor cannot skip an earlier uncommitted mutation.
func (s *Store) portableWrite(ctx context.Context, sc Scope, write func(pgx.Tx) error) error {
	if err := requireScope(sc); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(json_build_array('rss-portable',$1::text,$2::text)::text,0))`, sc.UserID, sc.WorkspaceID); err != nil {
		return err
	}
	if err = write(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type PortableChange struct {
	Sequence        int64          `json:"sequence"`
	Kind            string         `json:"kind"`
	ExtID           string         `json:"ext_id"`
	Title           string         `json:"title"`
	Payload         map[string]any `json:"payload"`
	ClientUpdatedAt *time.Time     `json:"client_updated_at"`
	OpID            string         `json:"op_id"`
	Epoch           *int64         `json:"epoch"`
	Deleted         bool           `json:"deleted"`
	Intent          string         `json:"intent"`
	Fields          []string       `json:"fields"`
	SyncError       string         `json:"sync_error"`
	OriginalTime    *string        `json:"-"`
}

// Preserve the original wire timestamp of a state operation, including its
// offset and trailing zeros, when another device imports the native journal.
func (c PortableChange) MarshalJSON() ([]byte, error) {
	type plain PortableChange
	var at any = c.ClientUpdatedAt
	if c.OriginalTime != nil {
		at = *c.OriginalTime
	}
	return json.Marshal(struct {
		plain
		Time any `json:"client_updated_at"`
	}{plain: plain(c), Time: at})
}

type PortablePage struct {
	Changes    []PortableChange `json:"changes"`
	NextCursor int64            `json:"next_cursor"`
}

// Only newly and explicitly enrolled sources emit records; existing rows are
// never scanned or assigned a portable owner by migration or list requests.
func (s *Store) ListPortableChanges(ctx context.Context, sc Scope, cursor int64, limit int) (PortablePage, error) {
	out := PortablePage{Changes: []PortableChange{}, NextCursor: cursor}
	if cursor < 0 || cursor > 9007199254740991 {
		return out, fmt.Errorf("rss: invalid portable cursor")
	}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("rss: invalid portable limit")
	}
	err := s.portableWrite(ctx, sc, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sequence,kind,ext_id,title,payload,client_updated_at,op_id,epoch,deleted,intent,changed_fields,sync_error,client_updated_at_exact FROM rss_portable_changes WHERE user_id=$1 AND workspace_id=$2 AND sequence>$3 ORDER BY sequence ASC LIMIT $4`, sc.UserID, sc.WorkspaceID, cursor, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var change PortableChange
			var payload, fields []byte
			var exact *string
			if err := rows.Scan(&change.Sequence, &change.Kind, &change.ExtID, &change.Title, &payload, &change.ClientUpdatedAt, &change.OpID, &change.Epoch, &change.Deleted, &change.Intent, &fields, &change.SyncError, &exact); err != nil {
				return err
			}
			if exact != nil {
				at, err := time.Parse(time.RFC3339Nano, *exact)
				if err != nil {
					return err
				}
				change.ClientUpdatedAt = &at
				change.OriginalTime = exact
			}
			if err := json.Unmarshal(payload, &change.Payload); err != nil {
				return err
			}
			if err := json.Unmarshal(fields, &change.Fields); err != nil {
				return err
			}
			out.Changes = append(out.Changes, change)
			out.NextCursor = change.Sequence
		}
		return rows.Err()
	})
	return out, err
}
