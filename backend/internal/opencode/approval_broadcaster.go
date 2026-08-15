package opencode

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// 审批推送事件类型（经 WS hub 广播，替代前端 10s 轮询）。
// payload 与 GET /api/mobile/approvals（frontend/src/api/approvals.ts
// listPendingApprovals）的数据结构保持一致。
const (
	// ApprovalEventPermissionPending 在新权限请求进入待审批状态时广播。
	ApprovalEventPermissionPending = "approval.permission.pending"
	// ApprovalEventQuestionPending 在新问题请求进入待回答状态时广播。
	ApprovalEventQuestionPending = "approval.question.pending"
	// ApprovalEventResolved 在权限/问题请求被回复、拒绝或过期时广播。
	ApprovalEventResolved = "approval.resolved"
)

// ApprovalBroadcastHub is the minimal WS surface the approval broadcaster
// needs. websocket.Hub satisfies it via BroadcastToWorkspace; tests can
// substitute a recorder. 与 email.OAuthBroadcaster 的注入模式一致。
type ApprovalBroadcastHub interface {
	BroadcastToWorkspace(workspaceID, msgType string, payload interface{})
}

// WsEnvelopeV1 mirrors the frozen envelope contract in
// frontend/src/services/idempotentWsBus.ts (WsEnvelopeV1). cause.approval_id
// lets the frontend idempotent bus dedupe replays per approval request.
type WsEnvelopeV1 struct {
	V       int         `json:"v"`
	ID      string      `json:"id"`
	Ts      int64       `json:"ts"`
	Channel string      `json:"channel"`
	Topic   string      `json:"topic"`
	Type    string      `json:"type"`
	Data    interface{} `json:"data"`
	Cause   *WsCauseV1  `json:"cause,omitempty"`
}

// WsCauseV1 is the envelope cause block used for idempotent dedupe.
type WsCauseV1 struct {
	ActionID      string `json:"action_id,omitempty"`
	ApprovalID    string `json:"approval_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ApprovalPermissionPendingPayload is the data block for
// approval.permission.pending. Request carries the same shape the mobile
// approvals pull API returns per item (adapter.PermissionRequest JSON).
type ApprovalPermissionPendingPayload struct {
	InstanceID string                     `json:"instance_id"`
	SessionID  string                     `json:"session_id"`
	Request    *adapter.PermissionRequest `json:"request"`
}

// ApprovalQuestionPendingPayload is the data block for
// approval.question.pending. Request carries the same shape the mobile
// approvals pull API returns per item (adapter.QuestionRequest JSON).
type ApprovalQuestionPendingPayload struct {
	InstanceID string                    `json:"instance_id"`
	SessionID  string                    `json:"session_id"`
	Request    *adapter.QuestionRequest  `json:"request"`
}

// ApprovalResolvedPayload is the data block for approval.resolved.
type ApprovalResolvedPayload struct {
	InstanceID string `json:"instance_id"`
	SessionID  string `json:"session_id"`
	Kind       string `json:"kind"`      // "permission" | "question"
	RequestID  string `json:"request_id"`
	Resolution string `json:"resolution"` // "approved" | "rejected" | "answered" | "expired" | "resolved"
	Decision   string `json:"decision,omitempty"`
}

// ApprovalBroadcaster bridges PermissionManager / QuestionManager events to
// workspace-targeted WS pushes. It subscribes to both managers' event
// channels, wraps each event in a WsEnvelopeV1, and hands it to the injected
// hub. 广播按实例归属的 workspace 定向；未绑定 workspace 的共享实例不推送
// （前端保留拉取兜底），绝不全站广播。
type ApprovalBroadcaster struct {
	registry *registry.Registry
	perms    *PermissionManager
	ques     *QuestionManager

	hub ApprovalBroadcastHub // optional；nil 跳过 WS 推送（保留 log）

	seq uint64 // event id sequence
}

// NewApprovalBroadcaster creates a broadcaster for the two approval managers.
func NewApprovalBroadcaster(reg *registry.Registry, perms *PermissionManager, ques *QuestionManager) *ApprovalBroadcaster {
	return &ApprovalBroadcaster{
		registry: reg,
		perms:    perms,
		ques:     ques,
	}
}

// SetBroadcaster 注入 WS hub（websocket.Hub 满足 ApprovalBroadcastHub 接口）。
func (b *ApprovalBroadcaster) SetBroadcaster(hub ApprovalBroadcastHub) {
	b.hub = hub
}

// Run subscribes to both managers and forwards events until ctx is cancelled.
// Blocks; run in a goroutine.
func (b *ApprovalBroadcaster) Run(ctx context.Context) {
	if b == nil || b.perms == nil || b.ques == nil {
		return
	}
	permCh, permCleanup := b.perms.Subscribe(128)
	defer permCleanup()
	quesCh, quesCleanup := b.ques.Subscribe(128)
	defer quesCleanup()

	log.Println("[approval-broadcast] started")
	defer log.Println("[approval-broadcast] stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-permCh:
			if !ok {
				return
			}
			b.forwardPermission(evt)
		case evt, ok := <-quesCh:
			if !ok {
				return
			}
			b.forwardQuestion(evt)
		}
	}
}

// forwardPermission maps a PermissionManager event onto a WS push event.
func (b *ApprovalBroadcaster) forwardPermission(evt PermissionEvent) {
	switch evt.Type {
	case "new":
		if evt.Request == nil {
			return
		}
		b.broadcast(evt.InstanceID, ApprovalEventPermissionPending,
			&ApprovalPermissionPendingPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Request:    evt.Request,
			}, evt.RequestID)
	case "resolved":
		payload := &ApprovalResolvedPayload{
			InstanceID: evt.InstanceID,
			SessionID:  evt.SessionID,
			Kind:       "permission",
			RequestID:  evt.RequestID,
			Resolution: "resolved",
		}
		if evt.Reply != nil {
			payload.Decision = string(*evt.Reply)
			payload.Resolution = "approved"
			if *evt.Reply == adapter.PermissionReplyReject {
				payload.Resolution = "rejected"
			}
		}
		b.broadcast(evt.InstanceID, ApprovalEventResolved, payload, evt.RequestID)
	case "expired":
		b.broadcast(evt.InstanceID, ApprovalEventResolved,
			&ApprovalResolvedPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Kind:       "permission",
				RequestID:  evt.RequestID,
				Resolution: "expired",
			}, evt.RequestID)
	}
}

// forwardQuestion maps a QuestionManager event onto a WS push event.
func (b *ApprovalBroadcaster) forwardQuestion(evt QuestionEvent) {
	switch evt.Type {
	case "new":
		if evt.Request == nil {
			return
		}
		b.broadcast(evt.InstanceID, ApprovalEventQuestionPending,
			&ApprovalQuestionPendingPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Request:    evt.Request,
			}, evt.RequestID)
	case "resolved":
		b.broadcast(evt.InstanceID, ApprovalEventResolved,
			&ApprovalResolvedPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Kind:       "question",
				RequestID:  evt.RequestID,
				Resolution: "answered",
			}, evt.RequestID)
	case "rejected":
		b.broadcast(evt.InstanceID, ApprovalEventResolved,
			&ApprovalResolvedPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Kind:       "question",
				RequestID:  evt.RequestID,
				Resolution: "rejected",
			}, evt.RequestID)
	case "expired":
		b.broadcast(evt.InstanceID, ApprovalEventResolved,
			&ApprovalResolvedPayload{
				InstanceID: evt.InstanceID,
				SessionID:  evt.SessionID,
				Kind:       "question",
				RequestID:  evt.RequestID,
				Resolution: "expired",
			}, evt.RequestID)
	}
}

// broadcast resolves the instance's workspace and pushes the envelope there.
// Instances without a bound workspace (shared operator resources) are skipped:
// their approvals stay pull-only rather than being broadcast site-wide.
func (b *ApprovalBroadcaster) broadcast(instanceID, evtType string, data interface{}, approvalID string) {
	if b.hub == nil {
		return
	}
	workspaceID := b.workspaceForInstance(instanceID)
	if workspaceID == "" {
		log.Printf("[approval-broadcast] skip %s for instance=%s: no workspace bound (pull-only)", evtType, instanceID)
		return
	}
	env := WsEnvelopeV1{
		V:       1,
		ID:      b.nextEventID(),
		Ts:      time.Now().UnixMilli(),
		Channel: "approvals",
		Topic:   instanceID,
		Type:    evtType,
		Data:    data,
		Cause:   &WsCauseV1{ApprovalID: approvalID},
	}
	b.hub.BroadcastToWorkspace(workspaceID, evtType, env)
}

func (b *ApprovalBroadcaster) workspaceForInstance(instanceID string) string {
	if b.registry == nil {
		return ""
	}
	inst, err := b.registry.GetInstance(instanceID)
	if err != nil {
		return ""
	}
	return inst.WorkspaceID
}

func (b *ApprovalBroadcaster) nextEventID() string {
	n := atomic.AddUint64(&b.seq, 1)
	return fmt.Sprintf("approval_%d_%d", time.Now().UnixNano(), n)
}
