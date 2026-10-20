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
	now := time.Now().UnixNano()
	if now <= version {
		return version
	}
	// §947：CAS 的**布尔结果**以前被丢弃 ⇒ 竞争下会读到别人的版本
	// ⇒ 那个版本**可能小于 now** ⇒ 版本号相对时间**回退** ⇒
	// ⇒⇒ 改成真正的 CAS 循环：写不进去就重试，直到自己写进去或发现
	// ⇒⇒ 别人已经写到了不小于 now 的值。
	for {
		cur := p.version.Load()
		if cur >= now {
			return cur
		}
		if p.version.CompareAndSwap(cur, now) {
			return now
		}
	}
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
