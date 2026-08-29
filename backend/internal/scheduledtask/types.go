// Package scheduledtask implements the user-facing "automation / scheduled
// tasks" subsystem. A Task describes a recurring or one-shot trigger plus a
// typed payload; an Executor carries out the payload against a downstream
// service (RedClaw, OpenCode Agent Bridge, llmbff, kxmemory, ACC MCP, generic
// HTTP webhook, or the email intent executor).
//
// Persistence lives in PostgreSQL (scheduled_tasks + scheduled_task_runs).
// The scheduler ticks every few seconds, claims due rows via a single
// UPDATE ... RETURNING statement guarded by a pg advisory lock to keep two
// pocketd instances from firing the same task at the same time, and dispatches
// each claim to a goroutine that records a Run row plus an audit entry.
//
// All public types are safe to use from multiple goroutines. nil-safe
// everywhere: when Postgres is not configured (remote-only mode) the store
// short-circuits and the scheduler becomes a no-op.
package scheduledtask

import (
	"context"
	"encoding/json"
	"time"
)

// ScheduleKind is how the schedule expression is interpreted.
type ScheduleKind string

const (
	ScheduleCron     ScheduleKind = "cron"     // 5- or 6-field cron expression
	ScheduleInterval ScheduleKind = "interval" // Go duration string e.g. "30m"
	ScheduleAt       ScheduleKind = "at"       // RFC3339 timestamp, one-shot
)

// Kind identifies which Executor should run a Task. Adding a new executor
// requires registering a new Kind constant here.
type Kind string

const (
	KindRedClawChat      Kind = "redclaw_chat"
	KindRedClawKnowledge Kind = "redclaw_knowledge"
	KindAgentBridge      Kind = "agent_bridge"
	KindLLMBFFSummary    Kind = "llmbff_summary"
	KindKxmemorySummary  Kind = "kxmemory_summary"
	KindACCMCP           Kind = "acc_mcp"
	KindWebhook          Kind = "webhook"
)

// AllKinds lists every built-in kind. Used by the API layer to validate
// client payloads and to drive the UI's kind picker.
func AllKinds() []Kind {
	return []Kind{
		KindRedClawChat,
		KindRedClawKnowledge,
		KindAgentBridge,
		KindLLMBFFSummary,
		KindKxmemorySummary,
		KindACCMCP,
		KindWebhook,
	}
}

// RunStatus is the terminal state of a Run.
type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
	RunStatusSkipped RunStatus = "skipped"
)

// Task is one scheduled automation. Created/updated via the HTTP API; the
// scheduler claims due rows, dispatches them to the registered Executor for
// the Kind, and records the outcome in Run.
type Task struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspaceId"`
	UserID       string          `json:"userId"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Kind         Kind            `json:"kind"`
	ScheduleKind ScheduleKind    `json:"scheduleKind"`
	ScheduleExpr string          `json:"scheduleExpr"`
	Timezone     string          `json:"timezone"`
	Payload      json.RawMessage `json:"payload"`
	Enabled      bool            `json:"enabled"`

	// Scheduler-managed state.
	NextRunAt  int64     `json:"nextRunAt"`  // unix seconds
	LeaseUntil int64     `json:"-"`          // internal claim lease; never exposed to clients
	LastRunAt  int64     `json:"lastRunAt"`  // unix seconds; 0 = never
	LastStatus RunStatus `json:"lastStatus"` // "" if never run
	LastError  string    `json:"lastError"`
	RunCount   int       `json:"runCount"`

	// Per-task limits. 0 means "not set / unlimited".
	MaxRuns     int `json:"maxRuns"`     // hard cap; 0 = unlimited
	CooldownSec int `json:"cooldownSec"` // post-run grace before next due time
	TimeoutSec  int `json:"timeoutSec"`  // per-run context timeout (default 120)

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// Run records one execution attempt of a Task. Stored in scheduled_task_runs.
type Run struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"taskId"`
	WorkspaceID      string          `json:"workspaceId"`
	UserID           string          `json:"userId"`
	Status           RunStatus       `json:"status"`
	StartedAt        int64           `json:"startedAt"`
	FinishedAt       int64           `json:"finishedAt"`
	DurationMs       int             `json:"durationMs"`
	Output           json.RawMessage `json:"output"`
	Error            string          `json:"error"`
	ReferencedTaskID string          `json:"referencedTaskId,omitempty"` // e.g. AgentBridge.created task
}

// Result is what an Executor returns. The scheduler is responsible for
// persisting the Run row, computing next_run_at, broadcasting the WS event,
// and recording the audit entry — the executor should not touch storage.
type Result struct {
	Output           json.RawMessage `json:"output,omitempty"`
	ReferencedTaskID string          `json:"referencedTaskId,omitempty"`
	// Skip marks the run as a terminal skip (no retry). Used when an executor
	// decides the payload is no longer applicable (e.g. webhook target 410'd).
	Skip bool `json:"skip,omitempty"`
}

// Executor runs a Task. The scheduler calls Execute with a per-run context
// that already carries the timeout (task.TimeoutSec) and the tenant headers
// (workspace_id + user_id injected via WithTenant). Implementations must
// return promptly on context cancellation and must not retain t.Payload
// past the call.
type Executor interface {
	Kind() Kind
	Execute(ctx context.Context, t *Task) (*Result, error)
}

// TaskInput is the writable subset used by the API create/update paths.
type TaskInput struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Kind         Kind            `json:"kind"`
	ScheduleKind ScheduleKind    `json:"scheduleKind"`
	ScheduleExpr string          `json:"scheduleExpr"`
	Timezone     string          `json:"timezone"`
	Payload      json.RawMessage `json:"payload"`
	Enabled      *bool           `json:"enabled,omitempty"`
	MaxRuns      int             `json:"maxRuns"`
	CooldownSec  int             `json:"cooldownSec"`
	TimeoutSec   int             `json:"timeoutSec"`
}

// Defaults fills in zero-value fields with sensible v1 defaults.
func (in *TaskInput) Defaults() {
	if in.Timezone == "" {
		in.Timezone = "Asia/Shanghai"
	}
	if in.TimeoutSec <= 0 {
		in.TimeoutSec = 120
	}
	if in.Enabled == nil {
		e := true
		in.Enabled = &e
	}
}

// nextRunPreview is the JSON shape returned by the schedule preview endpoint.
type NextRunPreview struct {
	Next []int64 `json:"next"` // unix seconds, up to 5 entries
}

// helper for tests and store layer.
func unixNow() int64 { return time.Now().Unix() }
