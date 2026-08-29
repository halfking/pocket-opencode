package scheduledtask

import "errors"

// Sentinel errors. Tests and HTTP handlers match on these; wrap (not replace)
// when adding context.
var (
	// ErrStoreUnavailable is returned by store methods when the underlying
	// pgxpool is nil (remote-only mode) or migrations have not run.
	ErrStoreUnavailable = errors.New("scheduledtask: store unavailable (Postgres not configured)")

	// ErrTaskNotFound is returned by Get/Update/Delete/Trigger when no row
	// matches the (id, workspace_id, user_id) tuple — i.e. cross-tenant
	// lookups always fail closed.
	ErrTaskNotFound = errors.New("scheduledtask: task not found")

	// ErrInvalidSchedule is returned by Schedule.ComputeNext when the
	// expression cannot be parsed (bad cron, bad duration, past at-time).
	ErrInvalidSchedule = errors.New("scheduledtask: invalid schedule expression")

	// ErrUnknownKind is returned when a Task references a Kind that no
	// registered Executor can satisfy.
	ErrUnknownKind = errors.New("scheduledtask: no executor registered for kind")

	// ErrExecutorPanic wraps a recovered panic so callers can still log
	// the run as failed without losing the stack trace.
	ErrExecutorPanic = errors.New("scheduledtask: executor panicked")

	// ErrDisabled is returned by scheduler when the entire subsystem is off
	// via config (POCKET_SCHEDULER_ENABLED=false). The HTTP API still
	// accepts writes so that task definitions can be staged.
	ErrDisabled = errors.New("scheduledtask: scheduler is disabled by configuration")
)
