// Package zagclient defines the interface contract for a future ZAgentGateway
// (ZAG) client that OpenPocket will use to talk to the ZAG control-plane adapter.
//
// IMPORTANT (子任务 D):
//   - This file only declares the interface and shared request/response types.
//   - No HTTP transport, no auth middleware, no goroutine, no real I/O lives here.
//   - The default implementation (NoopClient) lives in noop.go and returns
//     ErrNotConfigured for every method; it is the safe default until ZAG
//     contracts are frozen and a real implementation is added.
//
// All boundary rules from docs/新架构v1/04-contracts/pocket-zag-incremental.md
// apply: no query-string JWT, no bare X-Tenant-ID header as the only auth
// source; write operations MUST carry an Idempotency-Key; the client must
// surface enough error context for the caller to decide retry vs reconcile.
package zagclient

import (
	"context"
	"errors"
	"time"
)

// ErrNotConfigured is returned by NoopClient (and any implementation that has
// not been wired up). It is a sentinel so callers can detect "ZAG not yet
// available" without string-matching.
var ErrNotConfigured = errors.New("zagclient: client not configured")

// RiskClass classifies how dangerous a ZAG-side operation is. The caller must
// consult this before performing outbox-then-write; critical operations must
// fail-closed if the durable outbox is unavailable.
type RiskClass string

const (
	RiskRead     RiskClass = "read"     // safe to retry freely
	RiskLow      RiskClass = "low"      // reversible, e.g. tag / annotation
	RiskMedium   RiskClass = "medium"   // workspace file edits, IDE write
	RiskHigh     RiskClass = "high"     // shell.run, git.push
	RiskCritical RiskClass = "critical" // pod.terminate, key rotation
)

// Scope names match the canonical scope strings from docs/新架构v1/01-architecture/安全模型.md §3.2.
const (
	ScopeAgentRead     = "agent:read"
	ScopeTaskCreate    = "task:create"
	ScopePermission    = "permission:approve"
	ScopeIDEWrite      = "ide:write"
	ScopePodControl    = "pod:control"
	ScopeSecretAdmin   = "secret:admin"
)

// Event is the normalized SSE/WebSocket event payload. The producer fills in
// schema_version so older consumers can reject events they do not understand.
type Event struct {
	EventID       string         `json:"event_id"`
	Sequence      int64          `json:"sequence"`
	SchemaVersion string         `json:"schema_version"`
	TenantID      string         `json:"tenant_id"`
	AggregateID   string         `json:"aggregate_id"`
	AggregateType string         `json:"aggregate_type"` // task|pod|agent|ide|session|permission
	Type          string         `json:"type"`           // task.update, agent.message, ...
	OccurredAt    time.Time      `json:"occurred_at"`
	Data          map[string]any `json:"data,omitempty"`
}

// Pod mirrors the ZAG /api/v1/pods payload (see pocketd-fleet-bridge.md §2.2).
type Pod struct {
	ID         string    `json:"id"`
	FleetID    string    `json:"fleetId"`
	Name       string    `json:"name"`
	Hostname   string    `json:"hostname"`
	OS         string    `json:"os"`
	Status     string    `json:"status"`
	CPUs       int       `json:"cpus"`
	MemoryGB   int       `json:"memoryGB"`
	GPU        string    `json:"gpu,omitempty"`
	Agents     []string  `json:"agents"`
	IDEs       []string  `json:"ides"`
	Region     string    `json:"region"`
	LastSeen   time.Time `json:"lastSeen"`
}

// ControlPodRequest models POST /api/v1/pods/:id/control body.
type ControlPodRequest struct {
	Kind   string `json:"kind"`   // pause|resume|restart|upgrade|rollback|terminate
	Reason string `json:"reason"`
}

// Agent mirrors ZAG /api/v1/agents payload (see pocketd-fleet-bridge.md §2.3).
type Agent struct {
	ID           string    `json:"id"`
	FleetID      string    `json:"fleetId"`
	PodID        string    `json:"podId"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Runtime      string    `json:"runtime"`
	Status       string    `json:"status"`
	Capabilities []string  `json:"capabilities"`
	Harness      string    `json:"harness"`
	Model        string    `json:"model"`
	LastSeen     time.Time `json:"lastSeen"`
}

// InvokeRequest is the body for POST /api/v1/agents/:id/invoke.
type InvokeRequest struct {
	Goal    string         `json:"goal"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	Session string         `json:"session,omitempty"`
}

// InvokeResult is the synchronous portion of an agent invocation. Long-running
// work is delivered via SubscribeTaskEvents / SubscribeAgentEvents.
type InvokeResult struct {
	OperationID    string `json:"operation_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Accepted       bool   `json:"accepted"`
	Status         string `json:"status"`
}

// IDEStatus mirrors ZAG /api/v1/ide payload.
type IDEStatus struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Running      bool      `json:"running"`
	Workspace    string    `json:"workspace,omitempty"`
	Extensions   []string  `json:"extensions,omitempty"`
	LastCommand  string    `json:"lastCommand,omitempty"`
	LastActivity time.Time `json:"lastActivity"`
}

// IDECommand models POST /api/v1/ide/:name/command body. Command MUST be a
// schema-registered name (see 安全模型 §4.1); raw command strings are forbidden.
type IDECommand struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// ExecutionReceipt is returned by synchronous IDE command execution.
type ExecutionReceipt struct {
	OperationID    string `json:"operation_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	OutputDigest   string `json:"output_digest,omitempty"`
}

// Task mirrors ZAG /api/v1/tasks payload.
type Task struct {
	ID         string     `json:"id"`
	FleetID    string     `json:"fleetId"`
	SessionID  string     `json:"sessionId"`
	PodID      string     `json:"podId"`
	AgentID    string     `json:"agentId"`
	Goal       string     `json:"goal"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// SubmitTaskRequest is the body for POST /api/v1/tasks.
type SubmitTaskRequest struct {
	FleetID         string         `json:"fleetId"`
	Goal            string         `json:"goal"`
	AgentID         string         `json:"agentId"`
	Inputs          map[string]any `json:"inputs,omitempty"`
	IdempotencyKey  string         `json:"idempotency_key"` // required
	ApprovalReceipt string         `json:"approval_receipt,omitempty"`
}

// ReplyPermissionRequest models POST /api/v1/permissions/:id/reply.
type ReplyPermissionRequest struct {
	Decision        string `json:"decision"` // allow|deny|always
	Reason          string `json:"reason,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"` // required
	ApprovalReceipt string `json:"approval_receipt,omitempty"`
}

// ListPodsRequest is the query for GET /api/v1/pods.
type ListPodsRequest struct {
	FleetID string
	Status  string
}

// ListAgentsRequest is the query for GET /api/v1/agents.
type ListAgentsRequest struct {
	FleetID string
	Status  string
	PodID   string
	Kind    string
}

// ListIDEsRequest is the query for GET /api/v1/ide.
type ListIDEsRequest struct {
	FleetID string
}

// CallOptions are passed per-call. IdempotencyKey is mandatory for write
// methods (callers should set it; the interface documents this as a precondition).
type CallOptions struct {
	// IdempotencyKey MUST be set for any write method. A non-empty value here
	// overrides any auto-generation strategy a future implementation might use.
	IdempotencyKey string

	// CorrelationID is propagated as X-Correlation-ID for log tracing.
	CorrelationID string

	// TraceID is propagated as X-Trace-ID and bound into the audit outbox row.
	TraceID string

	// Scopes narrows the delegated token's scope set for this single call.
	// Implementations MUST intersect with the client's granted scope and
	// reject (with a sentinel error) if the requested scope is not granted.
	Scopes []string
}

// Client is the interface contract for any ZAG client implementation.
//
// All methods take a context.Context and CallOptions. The returned error must
// be (or wrap) one of the sentinel errors declared in this package so callers
// can distinguish retryable failures from forbidden/auth errors.
//
// No method here performs I/O directly: that is the responsibility of concrete
// implementations (HTTPClient, NoopClient, future mocks).
type Client interface {
	// Health checks that the ZAG instance is reachable and the client's auth
	// material is still valid. It MUST NOT touch durable outbox state.
	Health(ctx context.Context) error

	// ---- Pods ----
	ListPods(ctx context.Context, req ListPodsRequest, opts CallOptions) ([]Pod, error)
	GetPod(ctx context.Context, podID string, opts CallOptions) (*Pod, error)
	ControlPod(ctx context.Context, podID string, req ControlPodRequest, opts CallOptions) error

	// ---- Agents ----
	ListAgents(ctx context.Context, req ListAgentsRequest, opts CallOptions) ([]Agent, error)
	GetAgent(ctx context.Context, agentID string, opts CallOptions) (*Agent, error)
	InvokeAgent(ctx context.Context, agentID string, req InvokeRequest, opts CallOptions) (*InvokeResult, error)

	// ---- IDE ----
	ListIDEs(ctx context.Context, req ListIDEsRequest, opts CallOptions) ([]IDEStatus, error)
	GetIDEStatus(ctx context.Context, name string, opts CallOptions) (*IDEStatus, error)
	ExecuteIDECommand(ctx context.Context, name string, req IDECommand, opts CallOptions) (*ExecutionReceipt, error)

	// ---- Tasks / Permissions ----
	SubmitTask(ctx context.Context, req SubmitTaskRequest, opts CallOptions) (*Task, error)
	GetTask(ctx context.Context, taskID string, opts CallOptions) (*Task, error)
	CancelTask(ctx context.Context, taskID string, opts CallOptions) error

	// SubscribeTaskEvents opens an SSE channel for /api/v1/tasks/:id/events.
	// The returned cancel function MUST be idempotent.
	SubscribeTaskEvents(ctx context.Context, taskID string, opts CallOptions) (<-chan Event, func(), error)

	// SubscribeAgentEvents opens an SSE channel for /api/v1/agents/:id/events.
	SubscribeAgentEvents(ctx context.Context, agentID string, opts CallOptions) (<-chan Event, func(), error)

	// ReplyPermission posts a decision for /api/v1/permissions/:id/reply.
	ReplyPermission(ctx context.Context, permID string, req ReplyPermissionRequest, opts CallOptions) error

	// RiskClass reports the operation's risk so callers can decide whether to
	// route through the durable outbox + approval gate before invoking the
	// matching method. Implementations may return RiskRead for read methods
	// and RiskCritical for things like pod.terminate.
	RiskClass(op Operation) RiskClass

	// Close releases any underlying resources (HTTP connections, file handles).
	// Calling Close on an already-closed client MUST NOT panic.
	Close() error
}

// Operation is an enum-style tag so RiskClass can answer without taking the
// full request payload. It also gives us a single switch point for future
// instrumentation.
type Operation string

const (
	OpHealth              Operation = "health"
	OpListPods            Operation = "list_pods"
	OpGetPod              Operation = "get_pod"
	OpControlPod          Operation = "control_pod"
	OpListAgents          Operation = "list_agents"
	OpGetAgent            Operation = "get_agent"
	OpInvokeAgent         Operation = "invoke_agent"
	OpListIDEs            Operation = "list_ides"
	OpGetIDEStatus        Operation = "get_ide_status"
	OpExecuteIDECommand   Operation = "execute_ide_command"
	OpSubmitTask          Operation = "submit_task"
	OpGetTask             Operation = "get_task"
	OpCancelTask          Operation = "cancel_task"
	OpReplyPermission     Operation = "reply_permission"
	OpSubscribeTaskEvents Operation = "subscribe_task_events"
	OpSubscribeAgentEvts  Operation = "subscribe_agent_events"
)

// IsWrite returns true if the operation is a write (idempotency required).
// Read operations do not require Idempotency-Key per the contract.
func (o Operation) IsWrite() bool {
	switch o {
	case OpControlPod, OpInvokeAgent, OpExecuteIDECommand,
		OpSubmitTask, OpCancelTask, OpReplyPermission:
		return true
	}
	return false
}

// ValidateWrite ensures the CallOptions carry an Idempotency-Key for write
// operations. It returns ErrMissingIdempotencyKey when the precondition is
// violated; this is the single enforcement point that all future
// implementations should defer to.
func ValidateWrite(op Operation, opts CallOptions) error {
	if !op.IsWrite() {
		return nil
	}
	if opts.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	return nil
}

// ErrMissingIdempotencyKey is returned when a write operation is invoked
// without an Idempotency-Key in CallOptions.
var ErrMissingIdempotencyKey = errors.New("zagclient: Idempotency-Key is required for write operations")

// ErrScopeInsufficient is returned when a caller asks for a scope that the
// client's delegated token does not grant. It is intentionally distinct from
// ErrUnauthorized so retries with the same scope don't loop forever.
var ErrScopeInsufficient = errors.New("zagclient: requested scope is not granted by the delegated token")

// ErrUnauthorized is returned for token/signature failures (mTLS handshake,
// expired JWT, signature mismatch). Callers should NOT retry; they should
// re-auth and reconcile.
var ErrUnauthorized = errors.New("zagclient: unauthorized")

// ErrTenantMismatch is returned when the delegated JWT's tenant_id claim does
// not match the mTLS SAN/CN or the upstream body's tenant_id. Per 安全模型 §3.1
// this is treated as 403 and must be logged.
var ErrTenantMismatch = errors.New("zagclient: tenant mismatch between delegated token and mTLS SAN")

// ErrUpstreamUnavailable is returned for retryable upstream failures (5xx,
// network errors). Callers may retry with backoff and/or query/reconcile.
var ErrUpstreamUnavailable = errors.New("zagclient: upstream unavailable")

// ErrIndeterminate signals that the call's outcome is unknown (timeout after
// request was sent, connection drop mid-response). Per 安全模型 §7.2 the caller
// MUST NOT auto-retry the write; it must query/reconcile first.
var ErrIndeterminate = errors.New("zagclient: outcome indeterminate; do not retry without reconcile")
