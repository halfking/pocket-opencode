package redclaw

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgAuditIDSeq is only a fallback for the extremely unlikely crypto/rand
// failure. The normal ID path is random so separate pocketd processes cannot
// collide on the same timestamp and counter values.
var pgAuditIDSeq uint64

// PGAuditStore is a PostgreSQL-backed implementation of AuditStore.
type PGAuditStore struct {
	pool    *pgxpool.Pool
	table   string
	maxSize int
}

// NewPGAuditStore creates a new PostgreSQL-backed audit store.
func NewPGAuditStore(pool *pgxpool.Pool) (*PGAuditStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgaudit: pgxpool is nil")
	}
	s := &PGAuditStore{
		pool:    pool,
		table:   "audit_entries",
		maxSize: 10000,
	}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("pgaudit migrate: %w", err)
	}
	return s, nil
}

// migrate creates the audit_entries table if it doesn't exist.
func (s *PGAuditStore) migrate() error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id           TEXT PRIMARY KEY,
	action       TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	tenant_id    TEXT NOT NULL,
	resource     TEXT,
	detail       TEXT,
	duration_ms  BIGINT,
	success      BOOLEAN NOT NULL,
	timestamp    TIMESTAMPTZ NOT NULL,
	ip           TEXT,
	created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_%s_tenant_time ON %s(tenant_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_%s_user_time ON %s(user_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_%s_action_time ON %s(action, timestamp DESC);
`, s.table, s.table, s.table, s.table, s.table, s.table, s.table))
	return err
}

// Record records an audit entry to the store.
func (s *PGAuditStore) Record(entry *AuditEntry) error {
	if entry == nil {
		return fmt.Errorf("audit entry cannot be nil")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.ID == "" {
		var suffix [16]byte
		if _, err := cryptorand.Read(suffix[:]); err == nil {
			entry.ID = "aud_" + hex.EncodeToString(suffix[:])
		} else {
			entry.ID = fmt.Sprintf("aud_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&pgAuditIDSeq, 1))
		}
	}

	ctx := context.Background()
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (id, action, user_id, tenant_id, resource, detail, duration_ms, success, timestamp, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO NOTHING
`, s.table),
		entry.ID, entry.Action, entry.UserID, entry.TenantID,
		entry.Resource, entry.Detail, entry.DurationMs, entry.Success,
		entry.Timestamp, entry.IP)
	return err
}

// Query retrieves audit entries matching the given query filters.
func (s *PGAuditStore) Query(query AuditQuery) ([]*AuditEntry, error) {
	ctx := context.Background()
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	var where []string
	var args []any
	argIdx := 1

	if query.TenantID != "" {
		where = append(where, fmt.Sprintf("tenant_id = $%d", argIdx))
		args = append(args, query.TenantID)
		argIdx++
	}
	if query.UserID != "" {
		where = append(where, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, query.UserID)
		argIdx++
	}
	if query.Action != "" {
		where = append(where, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, query.Action)
		argIdx++
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	querySQL := fmt.Sprintf(`
SELECT id, action, user_id, tenant_id, resource, detail, duration_ms, success, timestamp, ip
FROM %s
%s
ORDER BY timestamp DESC
LIMIT %d
`, s.table, whereSQL, limit)

	rows, err := s.pool.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts time.Time
		if err := rows.Scan(&e.ID, &e.Action, &e.UserID, &e.TenantID, &e.Resource, &e.Detail, &e.DurationMs, &e.Success, &ts, &e.IP); err != nil {
			return nil, err
		}
		e.Timestamp = ts
		result = append(result, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Flush is not supported for PG-backed store (returns empty).
func (s *PGAuditStore) Flush() []*AuditEntry {
	return nil
}

// QueryRange implements incremental paged query for audit export.
func (s *PGAuditStore) QueryRange(query AuditQuery) (*AuditPage, error) {
	ctx := context.Background()
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}

	var where []string
	var args []any
	argIdx := 1

	if query.TenantID != "" {
		where = append(where, fmt.Sprintf("tenant_id = $%d", argIdx))
		args = append(args, query.TenantID)
		argIdx++
	}
	if query.UserID != "" {
		where = append(where, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, query.UserID)
		argIdx++
	}
	if query.Action != "" {
		where = append(where, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, query.Action)
		argIdx++
	}
	if !query.StartTime.IsZero() {
		where = append(where, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, query.StartTime)
		argIdx++
	}
	if !query.EndTime.IsZero() {
		where = append(where, fmt.Sprintf("timestamp < $%d", argIdx))
		args = append(args, query.EndTime)
		argIdx++
	}

	if query.AfterCursor != "" {
		cursorTs, cursorID := decodeAuditCursor(query.AfterCursor)
		if !cursorTs.IsZero() {
			where = append(where, fmt.Sprintf("(timestamp, id) > ($%d, $%d)", argIdx, argIdx+1))
			args = append(args, cursorTs, cursorID)
			argIdx += 2
		}
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	querySQL := fmt.Sprintf(`
SELECT id, action, user_id, tenant_id, resource, detail, duration_ms, success, timestamp, ip
FROM %s
%s
ORDER BY timestamp ASC, id ASC
LIMIT %d
`, s.table, whereSQL, limit)

	rows, err := s.pool.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	page := &AuditPage{Entries: make([]*AuditEntry, 0, limit)}
	for rows.Next() {
		var e AuditEntry
		var ts time.Time
		if err := rows.Scan(&e.ID, &e.Action, &e.UserID, &e.TenantID, &e.Resource, &e.Detail, &e.DurationMs, &e.Success, &ts, &e.IP); err != nil {
			return nil, err
		}
		e.Timestamp = ts
		page.Entries = append(page.Entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(page.Entries) == limit {
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = encodeAuditCursor(last)
	}
	return page, nil
}
