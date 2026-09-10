package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type RunProjectionEvent struct {
	EventID   string
	EventType string
	TenantID  string
	RunID     string
	TaskID    string
	Sequence  uint64
	Raw       json.RawMessage
}

func checkEvents(b TaskRunBinding, events []RunProjectionEvent) error {
	if err := validateBinding(b); err != nil {
		return err
	}
	last := uint64(0)
	for i, e := range events {
		if e.TenantID != b.TenantID || e.RunID != b.RunID || e.Sequence == 0 || len(e.Raw) == 0 || !json.Valid(e.Raw) {
			return fmt.Errorf("invalid event at %d", i)
		}
		if e.TaskID != "" && e.TaskID != b.TaskID && strings.HasPrefix(e.EventType, "task.") {
			return fmt.Errorf("event task mismatch at %d", i)
		}
		if i > 0 && e.Sequence <= last {
			return errors.New("events must be strictly ordered")
		}
		last = e.Sequence
	}
	return nil
}
func (s *Store) BindVerifiedRun(ctx context.Context, b TaskRunBinding, title string, events []RunProjectionEvent) error {
	if err := checkEvents(b, events); err != nil {
		return err
	}
	found := false
	for _, e := range events {
		if e.TaskID == b.TaskID && (e.EventType == "task.created" || strings.HasPrefix(e.EventType, "task.created.")) {
			found = true
		}
	}
	if !found {
		return errors.New("matching task.created event required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldRun, oldTenant string
	err = tx.QueryRow(ctx, `SELECT run_id,tenant_id FROM task_run_bindings WHERE workspace_id=$1 AND task_id=$2 FOR UPDATE`, b.WorkspaceID, b.TaskID).Scan(&oldRun, &oldTenant)
	if err == nil && (oldRun != b.RunID || oldTenant != b.TenantID) {
		return errors.New("immutable binding conflict")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND workspace_id=$2)`, b.TaskID, b.WorkspaceID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("local task already exists")
	}
	now := int64(0)
	_, err = tx.Exec(ctx, `INSERT INTO tasks(id,workspace_id,title,description,status,priority,source,created_at,updated_at) VALUES($1,$2,$3,'','queued','normal','acc',extract(epoch from now()),extract(epoch from now()))`, b.TaskID, b.WorkspaceID, title)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err = insertEvent(ctx, tx, b, e); err != nil {
			return err
		}
		if e.Sequence > uint64(now) {
			now = int64(e.Sequence)
		}
	}
	if len(events) > 0 {
		b.Watermark = events[len(events)-1].Sequence
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = nowTime()
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_run_bindings(workspace_id,task_id,run_id,operation_id,tenant_id,watermark,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, b.WorkspaceID, b.TaskID, b.RunID, b.OperationID, b.TenantID, b.Watermark, b.CreatedAt.Unix())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func nowTime() (t time.Time) { return time.Now().UTC() }
func insertEvent(ctx context.Context, tx pgx.Tx, b TaskRunBinding, e RunProjectionEvent) error {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT raw FROM pocket_run_events WHERE workspace_id=$1 AND run_id=$2 AND sequence=$3`, b.WorkspaceID, b.RunID, e.Sequence).Scan(&raw)
	if err == nil {
		if !bytes.Equal(raw, e.Raw) {
			return errors.New("sequence event conflict")
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO pocket_run_events(tenant_id,workspace_id,run_id,event_id,event_type,task_id,sequence,raw) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, e.TenantID, b.WorkspaceID, b.RunID, e.EventID, e.EventType, e.TaskID, e.Sequence, e.Raw)
	return err
}
func (s *Store) SyncRunEvents(ctx context.Context, b TaskRunBinding, events []RunProjectionEvent) error {
	if err := checkEvents(b, events); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var ok bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_run_bindings WHERE workspace_id=$1 AND task_id=$2 AND run_id=$3 AND tenant_id=$4)`, b.WorkspaceID, b.TaskID, b.RunID, b.TenantID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("binding not found")
	}
	for _, e := range events {
		if err = insertEvent(ctx, tx, b, e); err != nil {
			return err
		}
	}
	if len(events) > 0 {
		_, err = tx.Exec(ctx, `UPDATE task_run_bindings SET watermark=GREATEST(watermark,$3) WHERE workspace_id=$1 AND task_id=$2 AND run_id=$4 AND tenant_id=$5`, b.WorkspaceID, b.TaskID, events[len(events)-1].Sequence, b.RunID, b.TenantID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AckRunCursor(ctx context.Context, workspaceID, taskID, userID, consumerID string, seq uint64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var wm uint64
	if err = tx.QueryRow(ctx, `SELECT watermark FROM task_run_bindings WHERE workspace_id=$1 AND task_id=$2 FOR UPDATE`, normalizeWorkspace(workspaceID), taskID).Scan(&wm); err != nil {
		return err
	}
	if seq > wm {
		return errors.New("cursor exceeds watermark")
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pocket_run_events e JOIN task_run_bindings b ON b.workspace_id=e.workspace_id AND b.run_id=e.run_id WHERE b.workspace_id=$1 AND b.task_id=$2 AND e.sequence=$3)`, normalizeWorkspace(workspaceID), taskID, seq).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("cursor event not found")
	}
	_, err = tx.Exec(ctx, `INSERT INTO pocket_run_cursors(workspace_id,task_id,user_id,consumer_id,sequence) VALUES($1,$2,$3,$4,$5) ON CONFLICT (workspace_id,task_id,user_id,consumer_id) DO UPDATE SET sequence=GREATEST(pocket_run_cursors.sequence,EXCLUDED.sequence)`, normalizeWorkspace(workspaceID), taskID, userID, consumerID, seq)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) GetRunCursor(ctx context.Context, workspaceID, taskID, userID, consumerID string) (uint64, error) {
	var n uint64
	err := s.pool.QueryRow(ctx, `SELECT sequence FROM pocket_run_cursors WHERE workspace_id=$1 AND task_id=$2 AND user_id=$3 AND consumer_id=$4`, normalizeWorkspace(workspaceID), taskID, userID, consumerID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
func (s *Store) ReplayRunEvents(ctx context.Context, workspaceID, taskID string, after uint64) ([]RunProjectionEvent, error) {
	b, err := s.GetTaskRunBinding(ctx, workspaceID, taskID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT event_id,event_type,tenant_id,run_id,task_id,sequence,raw FROM pocket_run_events WHERE workspace_id=$1 AND run_id=$2 AND tenant_id=$3 AND sequence>$4 ORDER BY sequence`, b.WorkspaceID, b.RunID, b.TenantID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunProjectionEvent
	for rows.Next() {
		var e RunProjectionEvent
		if err = rows.Scan(&e.EventID, &e.EventType, &e.TenantID, &e.RunID, &e.TaskID, &e.Sequence, &e.Raw); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
