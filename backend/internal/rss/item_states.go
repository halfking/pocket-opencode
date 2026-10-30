package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var ErrPortableStateConflict = errors.New("rss: portable status operation conflict")
var ErrPortableStateRequired = errors.New("rss: portable status version required")
var portableStateTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$`)

// A status version belongs to the user action, independently of feed metadata.
type PortableItemState struct {
	ExtID           string     `json:"ext_id"`
	Title           string     `json:"title"`
	Status          ItemStatus `json:"status"`
	ClientUpdatedAt string     `json:"client_updated_at"`
	OpID            string     `json:"op_id"`
	Epoch           int64      `json:"epoch"`
	Deleted         bool       `json:"deleted"`
}
type PortableStateAck struct {
	ExtID   string `json:"ext_id"`
	OpID    string `json:"op_id"`
	Epoch   int64  `json:"epoch"`
	Outcome string `json:"outcome"`
}

func samePortableStateOperation(a, b PortableItemState) bool {
	left, leftErr := parsePortableStateTime(a.ClientUpdatedAt)
	right, rightErr := parsePortableStateTime(b.ClientUpdatedAt)
	if leftErr != nil || rightErr != nil || !left.Equal(right) {
		return false
	}
	// Services may serialize one instant using different zones or trailing
	// zeros. Compare every other field exactly, without rewriting the first
	// receipt or reducing nanoseconds to PostgreSQL's microsecond precision.
	a.ClientUpdatedAt = ""
	b.ClientUpdatedAt = ""
	return a == b
}

func parsePortableStateTime(value string) (time.Time, error) {
	// time.Parse silently truncates fractions longer than nine digits. Reject
	// unsupported precision instead of treating two distinct instants as equal.
	if !portableStateTimePattern.MatchString(value) {
		return time.Time{}, fmt.Errorf("rss: invalid portable status time")
	}
	return time.Parse(time.RFC3339Nano, value)
}

func (x PortableItemState) validate() error {
	at, err := parsePortableStateTime(x.ClientUpdatedAt)
	if err != nil || at.After(time.Now().Add(5*time.Minute)) || !strings.HasPrefix(x.ExtID, "pocketd-item:") || len(x.ExtID) <= len("pocketd-item:") || len(x.ExtID) > 512 || !utf8.ValidString(x.ExtID) || len(x.Title) > 4096 || !utf8.ValidString(x.Title) || strings.TrimSpace(x.OpID) != x.OpID || x.OpID == "" || len(x.OpID) > 200 || !utf8.ValidString(x.OpID) || x.Epoch < 0 || x.Epoch > 9007199254740991 {
		return fmt.Errorf("rss: invalid portable status version")
	}
	if x.Deleted {
		if x.Status != "" {
			return fmt.Errorf("rss: status tombstone must be empty")
		}
		return nil
	}
	switch x.Status {
	case ItemUnread, ItemRead, ItemStarred, ItemArchived:
		return nil
	}
	return fmt.Errorf("rss: invalid item status")
}

// The scope lock serializes status/content/source deletion and journal sequence
// allocation. An unavailable target never creates a source or claims old data.
func (s *Store) ApplyPortableItemStates(ctx context.Context, sc Scope, items []PortableItemState) ([]PortableStateAck, error) {
	if len(items) < 1 || len(items) > 100 {
		return nil, fmt.Errorf("rss: 1 to 100 status operations required")
	}
	for _, item := range items {
		if err := item.validate(); err != nil {
			return nil, err
		}
	}
	acks := make([]PortableStateAck, 0, len(items))
	err := s.portableWrite(ctx, sc, func(tx pgx.Tx) error {
		for _, item := range items {
			ack, err := applyPortableItemState(ctx, tx, sc, item)
			if err != nil {
				return err
			}
			acks = append(acks, ack)
		}
		return nil
	})
	return acks, err
}

func applyPortableItemState(ctx context.Context, tx pgx.Tx, sc Scope, x PortableItemState) (PortableStateAck, error) {
	ack := PortableStateAck{ExtID: x.ExtID, OpID: x.OpID, Epoch: x.Epoch, Outcome: "unavailable"}
	encoded, _ := json.Marshal(x)
	var original []byte
	err := tx.QueryRow(ctx, `SELECT operation FROM rss_portable_state_ops WHERE user_id=$1 AND workspace_id=$2 AND op_id=$3`, sc.UserID, sc.WorkspaceID, x.OpID).Scan(&original)
	if err == nil {
		var prior PortableItemState
		if json.Unmarshal(original, &prior) != nil || !samePortableStateOperation(prior, x) {
			return ack, ErrPortableStateConflict
		}
		ack.Outcome = "duplicate"
		return ack, nil
	}
	if err != pgx.ErrNoRows {
		return ack, err
	}
	id := strings.TrimPrefix(x.ExtID, "pocketd-item:")
	var managed bool
	err = tx.QueryRow(ctx, `SELECT s.portal_managed FROM rss_items i JOIN rss_sources s ON s.id=i.source_id AND s.user_id=i.user_id AND s.workspace_id=i.workspace_id WHERE i.id=$1 AND i.user_id=$2 AND i.workspace_id=$3 FOR UPDATE OF i,s`, id, sc.UserID, sc.WorkspaceID).Scan(&managed)
	if err == pgx.ErrNoRows || (err == nil && !managed) {
		return ack, nil
	}
	if err != nil {
		return ack, err
	}
	var previousJSON []byte
	err = tx.QueryRow(ctx, `SELECT operation FROM rss_portable_item_states WHERE user_id=$1 AND workspace_id=$2 AND ext_id=$3`, sc.UserID, sc.WorkspaceID, x.ExtID).Scan(&previousJSON)
	if err != nil && err != pgx.ErrNoRows {
		return ack, err
	}
	ack.Outcome = "applied"
	if err == nil {
		var previous PortableItemState
		if err = json.Unmarshal(previousJSON, &previous); err != nil {
			return ack, err
		}
		at, _ := time.Parse(time.RFC3339Nano, x.ClientUpdatedAt)
		priorAt, _ := time.Parse(time.RFC3339Nano, previous.ClientUpdatedAt)
		if x.Epoch < previous.Epoch || (x.Epoch == previous.Epoch && (at.Before(priorAt) || (at.Equal(priorAt) && x.OpID <= previous.OpID))) {
			ack.Outcome = "kept_existing"
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rss_portable_state_ops(user_id,workspace_id,op_id,operation) VALUES($1,$2,$3,$4)`, sc.UserID, sc.WorkspaceID, x.OpID, encoded); err != nil {
		return ack, err
	}
	if ack.Outcome != "applied" {
		return ack, nil
	}
	if !x.Deleted {
		if _, err = tx.Exec(ctx, `UPDATE rss_items SET status=$1 WHERE id=$2 AND user_id=$3 AND workspace_id=$4`, x.Status, id, sc.UserID, sc.WorkspaceID); err != nil {
			return ack, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rss_portable_item_states(user_id,workspace_id,ext_id,operation) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,workspace_id,ext_id) DO UPDATE SET operation=excluded.operation`, sc.UserID, sc.WorkspaceID, x.ExtID, encoded); err != nil {
		return ack, err
	}
	payload := map[string]any{}
	if !x.Deleted {
		payload = map[string]any{"item_ext_id": x.ExtID, "status": x.Status}
	}
	at, _ := time.Parse(time.RFC3339Nano, x.ClientUpdatedAt)
	_, err = tx.Exec(ctx, `INSERT INTO rss_portable_changes(user_id,workspace_id,kind,ext_id,title,payload,client_updated_at,client_updated_at_exact,op_id,epoch,deleted,intent,changed_fields) VALUES($1,$2,'item-state',$3,$10,$4,$5,$6,$7,$8,$9,'status','["status"]')`, sc.UserID, sc.WorkspaceID, x.ExtID, j(payload), at, x.ClientUpdatedAt, x.OpID, x.Epoch, x.Deleted, x.Title)
	return ack, err
}
