package agentbridge

// bridge.go — the orchestration layer that turns "dispatch a prompt to an
// agent" into create-session + send-prompt + (the S0 fix) attach-task.
//
// Dependencies are interfaces so the Bridge is unit-testable without a live
// opencode instance or PG. The server package wires the concrete adapter and
// task store in main.go.

import (
	"context"
	"fmt"
	"log"
	"time"
)

// SessionCreator is the subset of the opencode adapter the Bridge needs.
// In production this is *adapter.OpenCodeHTTPAdapter.
type SessionCreator interface {
	// CreateSessionOnInstance creates a session on the instance at apiBaseURL.
	CreateSessionOnInstance(ctx context.Context, apiBaseURL string, req *CreateSessionInput) (*SessionInfo, error)
	// SendPromptToSession sends a prompt to a session.
	SendPromptToSession(ctx context.Context, apiBaseURL, sessionID string, req *SendPromptInput) error
}

// CreateSessionInput mirrors adapter.CreateSessionRequest (subset).
type CreateSessionInput struct {
	Agent      string
	ModelID    string
	ProviderID string
	Directory  string
}

// SessionInfo is the created-session result (subset of adapter.SessionInfo).
type SessionInfo struct {
	ID string
}

// SendPromptInput mirrors adapter.SendPromptRequest (subset).
type SendPromptInput struct {
	Agent string
	Text  string
}

// InstanceResolver resolves an instance_id to its HTTP API base URL within a
// workspace. Request-serving callers must provide the authenticated workspace.
type InstanceResolver interface {
	ResolveAPIBaseForWorkspace(workspaceID, instanceID string) (string, error)
}

// TaskAttacher attaches a session to a task in task_session_links.
// In production this is *task.Store.
type TaskAttacher interface {
	AttachSessionScoped(ctx context.Context, taskID, instanceID, sessionID, role, workspaceID string) error
}

// StoreLike is the subset of *Store the Bridge needs. Defining it as an
// interface lets tests inject a fake without a live PG. *Store satisfies it.
type StoreLike interface {
	Get(ctx context.Context, id string) (*Agent, error)
	GetScoped(ctx context.Context, id, wsID string) (*Agent, error)
	UpdateStatus(ctx context.Context, id string, status Status) error
}

// Bridge orchestrates dispatch through a registered Agent.
type Bridge struct {
	store    StoreLike
	creator  SessionCreator
	resolver InstanceResolver
	attacher TaskAttacher
}

// NewBridge constructs the Bridge. store is required; other deps may be nil
// (Send will return a clear error in that case).
func NewBridge(store StoreLike, creator SessionCreator, resolver InstanceResolver, attacher TaskAttacher) *Bridge {
	return &Bridge{store: store, creator: creator, resolver: resolver, attacher: attacher}
}

// SendOptions controls a dispatch.
type SendOptions struct {
	// WorkspaceID constrains the dispatch to an agent in that workspace.
	// HTTP callers must always set it from the authenticated claims, otherwise
	// any authenticated user could drive another tenant's agent by guessing
	// its ID. Empty means "no tenant check" and is reserved for internal
	// callers that have already established the boundary.
	WorkspaceID string
	TaskID      string // optional; if set, the new session is auto-attached
	Role        string // task_session_links role; default "primary"
	AgentName   string // override the agent's opencode "agent" field (e.g. "build")
	ModelID     string
	ProviderID  string
	Directory   string
}

// SendResult is what Send returns.
type SendResult struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	SessionID  string `json:"session_id"`
	TaskID     string `json:"task_id,omitempty"`
	Attached   bool   `json:"attached"`
}

// updateStatus 写一次实例状态，**写失败必须留痕**（不吞）。
//
// ⚠⚠ §945：这 5 处原来都是 `_ = b.store.UpdateStatus(...)` ⇒ ⇒⇒ **完全无声**。
//
//	为什么这个错误**很重**（比「日志没打」严重）：
//	状态**不是装饰** —— 它是**健康探测的判据**：库里那份状态决定了下一次
//	探测会不会**继续往这个实例上派活**。
//	⇒ ⇒⇒⇒ 写失败时，库里**仍然是旧状态** ⇒ ⇒⇒⇒ 比如「实例明明连不上，
//	⇒ ⇒⇒⇒ 但库里还写着 online」⇒ ⇒⇒⇒ 下一次探测会继续往一个坏实例派活
//	⇒ ⇒⇒⇒⇒ 而**没有人知道**状态没写进去 ⇒ ⇒⇒⇒ 派活失败会一直失败，
//	⇒ ⇒⇒⇒ 且**没有任何一条日志指向真正的原因**。
//
//	⚠ 为什么**不**把它变成致命错误：
//	本函数**每一步的失败都已经通过返回值可见**（下面 4 处都 `return err`）
//	⇒ ⇒⇒⇒ 所以状态写失败**不该**再把主结果否定掉（那会拿一个次要的
//	⇒ ⇒⇒⇒ 副作用去否定**主要结果**）⇒ ⇒⇒⇒ 正确处置是「留痕」。
//
//	⚠ 抽成**一个** helper 而不是抄 5 遍：★ 同一段逻辑抄 N 遍 ⇒
//	  同一个缺陷必然出现在 N 处 ⇒ ⇒⇒⇒ 这里 5 处的处置必须一致。
func (b *Bridge) updateStatus(ctx context.Context, agentID string, st Status) {
	if err := b.store.UpdateStatus(ctx, agentID, st); err != nil {
		log.Printf("ERROR: agentbridge: agent %s 状态写 %q 失败（库里状态未更新，健康探测仍按旧状态判断）: %v",
			agentID, st, err)
	}
}

// Send dispatches a prompt to the agent's underlying instance, creating a new
// session. When opts.TaskID is set, the session is auto-attached to that task
// via task_session_links (the spec §6.2 fix — previously dispatch did NOT
// write the link, leaving tasks unable to find their sessions).
func (b *Bridge) Send(ctx context.Context, agentID, prompt string, opts SendOptions) (*SendResult, error) {
	if b.store == nil {
		return nil, fmt.Errorf("agentbridge: store not configured")
	}
	if b.creator == nil || b.resolver == nil {
		return nil, fmt.Errorf("agentbridge: session creator/resolver not configured")
	}
	if prompt == "" {
		return nil, fmt.Errorf("agentbridge: prompt is required")
	}
	if opts.WorkspaceID == "" {
		return nil, fmt.Errorf("agentbridge: workspace_id is required")
	}

	// 1. Look up the agent scoped to workspace.
	agent, err := b.store.GetScoped(ctx, agentID, opts.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("lookup agent: %w", err)
	}

	// 1b. Fall back to the agent record when the caller didn't override —
	//     lets HTTP callers pass zero values and still get a working dispatch.
	if opts.AgentName == "" {
		opts.AgentName = agent.Name
	}

	// 2. Resolve instance → API base URL for the caller's workspace.
	apiBase, err := b.resolver.ResolveAPIBaseForWorkspace(opts.WorkspaceID, agent.InstanceID)
	if err != nil {
		b.updateStatus(ctx, agentID, StatusOffline)
		return nil, fmt.Errorf("resolve instance %s: %w", agent.InstanceID, err)
	}

	// 3. Create session on the instance.
	createIn := &CreateSessionInput{
		Agent:      opts.AgentName,
		ModelID:    opts.ModelID,
		ProviderID: opts.ProviderID,
		Directory:  opts.Directory,
	}
	info, err := b.creator.CreateSessionOnInstance(ctx, apiBase, createIn)
	if err != nil {
		// 创建会话失败不等同于实例离线——可能只是单次资源竞争。标记
		// unknown 让下一次健康探测重新评估，而不是误判为 offline/busy。
		b.updateStatus(ctx, agentID, StatusUnknown)
		return nil, fmt.Errorf("create session: %w", err)
	}
	if info == nil || info.ID == "" {
		b.updateStatus(ctx, agentID, StatusUnknown)
		return nil, fmt.Errorf("create session returned empty id")
	}

	// 4. Send the prompt.
	sendIn := &SendPromptInput{Agent: opts.AgentName, Text: prompt}
	if err := b.creator.SendPromptToSession(ctx, apiBase, info.ID, sendIn); err != nil {
		// 会话已创建（实例可达）但本轮提示发送失败 → busy 而非 offline。
		b.updateStatus(ctx, agentID, StatusBusy)
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	// 5. Mark agent online — a session was created and prompted successfully,
	//    so the instance is confirmed reachable (previously this incorrectly
	//    left the agent stuck at "busy" after every successful dispatch).
	b.updateStatus(ctx, agentID, StatusOnline)

	// 6. THE S0 FIX: auto-attach session to task if task_id was given.
	res := &SendResult{
		AgentID:    agentID,
		InstanceID: agent.InstanceID,
		SessionID:  info.ID,
		TaskID:     opts.TaskID,
	}
	if opts.TaskID != "" && b.attacher != nil {
		role := opts.Role
		if role == "" {
			role = "primary"
		}
		if err := b.attacher.AttachSessionScoped(ctx, opts.TaskID, agent.InstanceID, info.ID, role, opts.WorkspaceID); err != nil {
			// Attach failure is non-fatal — the session was created and prompt
			// sent. Surface it in the result but don't fail the whole dispatch.
			res.Attached = false
		} else {
			res.Attached = true
		}
	}
	return res, nil
}

// TouchOnline marks an agent online + records last-seen. Called by a future
// health poller or after a successful dispatch round-trip.
func (b *Bridge) TouchOnline(ctx context.Context, agentID string) error {
	if b.store == nil {
		return nil
	}
	return b.store.UpdateStatus(ctx, agentID, StatusOnline)
}

// now helper kept for potential future heartbeat timing.
var _ = time.Now
