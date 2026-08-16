package opencode

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// approvalProjector converts manager lifecycle changes into durable task-domain
// projection events. It only trusts workspace ownership from the registry.
// approvalProjectionSink is the synchronous boundary managers need to persist a
// lifecycle state before changing their in-memory cache.
type approvalProjectionSink interface {
	apply(context.Context, task.ApprovalKind, task.ApprovalState, string, string, string, string) error
}

type approvalProjector struct {
	registry *registry.Registry
	writer   task.ApprovalProjectionWriter
	version  atomic.Int64
}

func newApprovalProjector(reg *registry.Registry, writer task.ApprovalProjectionWriter) *approvalProjector {
	if reg == nil || writer == nil {
		return nil
	}
	return &approvalProjector{registry: reg, writer: writer}
}

func (p *approvalProjector) nextVersion() int64 {
	version := p.version.Add(1)
	if now := time.Now().UnixNano(); now > version {
		p.version.CompareAndSwap(version, now)
		version = p.version.Load()
	}
	return version
}

func (p *approvalProjector) apply(ctx context.Context, kind task.ApprovalKind, state task.ApprovalState, instanceID, sessionID, requestID, decision string) error {
	if p == nil || requestID == "" {
		return nil
	}
	instance, err := p.registry.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("resolve approval projection instance: %w", err)
	}
	if instance.WorkspaceID == "" {
		return nil
	}
	return p.writer.ApplyApprovalProjection(ctx, task.ApprovalProjectionEvent{
		WorkspaceID: instance.WorkspaceID,
		InstanceID:  instanceID,
		SessionID:   sessionID,
		RequestID:   requestID,
		Kind:        kind,
		State:       state,
		Version:     p.nextVersion(),
		Decision:    decision,
	})
}
