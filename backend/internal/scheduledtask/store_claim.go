package scheduledtask

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ClaimTaskNow atomically reserves one owned task for a manual run. It shares
// the same lease column as ClaimDue, enforces max_runs, and therefore cannot
// overlap a tick that already claimed the task.
func (s *Store) ClaimTaskNow(ctx context.Context, id, userID, workspaceID string, nowSec int64) (*Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
UPDATE scheduled_tasks
   SET next_run_at = $4 + GREATEST(300, timeout_sec + 60),
       lease_until = $4 + GREATEST(300, timeout_sec + 60)
 WHERE id = $1 AND user_id = $2 AND workspace_id = $3
	   AND (max_runs = 0 OR run_count < max_runs)
   AND (lease_until = 0 OR lease_until <= $4)
RETURNING id, workspace_id, user_id, name, description, kind,
          schedule_kind, schedule_expr, timezone, payload, enabled,
          next_run_at, lease_until, last_run_at, last_status, last_error, run_count,
          max_runs, cooldown_sec, timeout_sec, created_at, updated_at
`, id, userID, workspaceID, nowSec)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	return t, err
}
