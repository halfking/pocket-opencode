package rss

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrQueuedSourceInvalid  = errors.New("rss: invalid queued source")
	ErrQueuedSourceConflict = errors.New("rss: queued operation or source already exists")
)

// CreateQueuedSource records the original intent, native source and portable
// journal insert in one transaction. The receipt outlives source deletion.
func (s *Store) CreateQueuedSource(ctx context.Context, sc Scope, opID string, updatedAt time.Time, req CreateSourceRequest) (string, error) {
	if opID == "" || len(opID) > 200 || strings.TrimSpace(opID) != opID ||
		updatedAt.IsZero() || updatedAt.Nanosecond()%1000 != 0 {
		return "", ErrQueuedSourceInvalid
	}
	req.URL = strings.TrimSpace(req.URL)
	req.PortalSync = true
	updatedAt = updatedAt.UTC()
	digest := sha256.Sum256(j(struct {
		Request   CreateSourceRequest
		UpdatedAt time.Time
	}{req, updatedAt}))
	var sourceID string
	err := s.portableWrite(ctx, sc, func(tx pgx.Tx) error {
		var saved []byte
		readErr := tx.QueryRow(ctx, `SELECT request_hash,source_id FROM rss_source_create_receipts
			WHERE user_id=$1 AND workspace_id=$2 AND op_id=$3`, sc.UserID, sc.WorkspaceID, opID).Scan(&saved, &sourceID)
		if readErr == nil {
			if !bytes.Equal(saved, digest[:]) {
				return ErrQueuedSourceConflict
			}
			return nil
		}
		if !errors.Is(readErr, pgx.ErrNoRows) {
			return readErr
		}
		src, err := prepareCreateSource(req, sc)
		if err != nil {
			return ErrQueuedSourceInvalid
		}
		sourceID = src.ID
		if _, err := tx.Exec(ctx, `INSERT INTO rss_source_create_receipts
			(user_id,workspace_id,op_id,request_hash,source_id,client_updated_at) VALUES($1,$2,$3,$4,$5,$6)`,
			sc.UserID, sc.WorkspaceID, opID, digest[:], sourceID, updatedAt); err != nil {
			return err
		}
		if err := insertSource(ctx, tx, src, req.PortalEpoch); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "rss_sources_user_id_workspace_id_url_key" {
				return ErrQueuedSourceConflict
			}
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return sourceID, nil
}
