package zagclient

import (
	"context"
	"sync"
)

// NoopClient is the safe default Client implementation used until a real
// HTTPClient (or future gRPC client) is wired up.
//
// All methods that would perform I/O return ErrNotConfigured. Read methods
// that have no real implementation also return ErrNotConfigured so the
// caller can fail fast with a clear "ZAG not configured" signal. RiskClass
// still returns the correct value so callers can build audit / outbox
// decisions without needing a live ZAG.
//
// NoopClient is safe for concurrent use. Close is idempotent.
type NoopClient struct {
	closed   bool
	closeMu  sync.Mutex
}

// NewNoopClient returns a NoopClient. There is no configuration because the
// noop implementation does not talk to anything.
func NewNoopClient() *NoopClient {
	return &NoopClient{}
}

// Health returns ErrNotConfigured. Callers should treat this as "ZAG not
// available" and avoid surfacing a 5xx to the user (see docs/新架构v1/04-contracts/
// pocket-zag-incremental.md §6 "Degraded mode").
func (n *NoopClient) Health(_ context.Context) error {
	if n.isClosed() {
		return ErrNotConfigured
	}
	return ErrNotConfigured
}

// ListPods returns ErrNotConfigured.
func (n *NoopClient) ListPods(_ context.Context, _ ListPodsRequest, _ CallOptions) ([]Pod, error) {
	return nil, ErrNotConfigured
}

// GetPod returns ErrNotConfigured.
func (n *NoopClient) GetPod(_ context.Context, _ string, _ CallOptions) (*Pod, error) {
	return nil, ErrNotConfigured
}

// ControlPod returns ErrNotConfigured. Even though this is a write op, we
// intentionally do NOT call ValidateWrite here — the noop implementation
// refuses every call regardless, so precondition enforcement belongs to
// real callers that would otherwise silently no-op.
func (n *NoopClient) ControlPod(_ context.Context, _ string, _ ControlPodRequest, _ CallOptions) error {
	return ErrNotConfigured
}

// ListAgents returns ErrNotConfigured.
func (n *NoopClient) ListAgents(_ context.Context, _ ListAgentsRequest, _ CallOptions) ([]Agent, error) {
	return nil, ErrNotConfigured
}

// GetAgent returns ErrNotConfigured.
func (n *NoopClient) GetAgent(_ context.Context, _ string, _ CallOptions) (*Agent, error) {
	return nil, ErrNotConfigured
}

// InvokeAgent returns ErrNotConfigured.
func (n *NoopClient) InvokeAgent(_ context.Context, _ string, _ InvokeRequest, _ CallOptions) (*InvokeResult, error) {
	return nil, ErrNotConfigured
}

// ListIDEs returns ErrNotConfigured.
func (n *NoopClient) ListIDEs(_ context.Context, _ ListIDEsRequest, _ CallOptions) ([]IDEStatus, error) {
	return nil, ErrNotConfigured
}

// GetIDEStatus returns ErrNotConfigured.
func (n *NoopClient) GetIDEStatus(_ context.Context, _ string, _ CallOptions) (*IDEStatus, error) {
	return nil, ErrNotConfigured
}

// ExecuteIDECommand returns ErrNotConfigured.
func (n *NoopClient) ExecuteIDECommand(_ context.Context, _ string, _ IDECommand, _ CallOptions) (*ExecutionReceipt, error) {
	return nil, ErrNotConfigured
}

// SubmitTask returns ErrNotConfigured.
func (n *NoopClient) SubmitTask(_ context.Context, _ SubmitTaskRequest, _ CallOptions) (*Task, error) {
	return nil, ErrNotConfigured
}

// GetTask returns ErrNotConfigured.
func (n *NoopClient) GetTask(_ context.Context, _ string, _ CallOptions) (*Task, error) {
	return nil, ErrNotConfigured
}

// CancelTask returns ErrNotConfigured.
func (n *NoopClient) CancelTask(_ context.Context, _ string, _ CallOptions) error {
	return ErrNotConfigured
}

// SubscribeTaskEvents returns ErrNotConfigured.
func (n *NoopClient) SubscribeTaskEvents(_ context.Context, _ string, _ CallOptions) (<-chan Event, func(), error) {
	return nil, nil, ErrNotConfigured
}

// SubscribeAgentEvents returns ErrNotConfigured.
func (n *NoopClient) SubscribeAgentEvents(_ context.Context, _ string, _ CallOptions) (<-chan Event, func(), error) {
	return nil, nil, ErrNotConfigured
}

// ReplyPermission returns ErrNotConfigured.
func (n *NoopClient) ReplyPermission(_ context.Context, _ string, _ ReplyPermissionRequest, _ CallOptions) error {
	return ErrNotConfigured
}

// RiskClass returns the canonical risk for an operation even when the
// underlying transport is unavailable. This lets callers pre-classify for
// the audit outbox / approval gate without a live ZAG.
func (n *NoopClient) RiskClass(op Operation) RiskClass {
	switch op {
	case OpHealth, OpListPods, OpGetPod, OpListAgents, OpGetAgent,
		OpListIDEs, OpGetIDEStatus, OpGetTask, OpSubscribeTaskEvents,
		OpSubscribeAgentEvts:
		return RiskRead
	case OpInvokeAgent:
		return RiskMedium
	case OpExecuteIDECommand:
		return RiskMedium
	case OpSubmitTask:
		return RiskLow
	case OpCancelTask:
		return RiskLow
	case OpReplyPermission:
		return RiskHigh
	case OpControlPod:
		// ControlPod can be terminate (critical) or pause (low); without the
		// request body we conservatively report high. The real HTTPClient is
		// expected to inspect ControlPodRequest.Kind and return RiskCritical
		// for "terminate" / "rollback".
		return RiskHigh
	}
	return RiskRead
}

// Close marks the client as closed and is idempotent.
func (n *NoopClient) Close() error {
	n.closeMu.Lock()
	defer n.closeMu.Unlock()
	n.closed = true
	return nil
}

func (n *NoopClient) isClosed() bool {
	n.closeMu.Lock()
	defer n.closeMu.Unlock()
	return n.closed
}

// Compile-time assertion that *NoopClient satisfies Client.
var _ Client = (*NoopClient)(nil)
