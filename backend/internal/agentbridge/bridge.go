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

// InstanceResolver resolves an instance_id to its HTTP API base URL.
// In production this is *registry.Registry.
type InstanceResolver interface {
	ResolveAPIBase(instanceID string) (string, error)
}

// TaskAttacher attaches a session to a task in task_session_links.
// In production this is *task.Store.
type TaskAttacher interface {
	AttachSession(ctx context.Context, taskID, instanceID, sessionID, role string) error
}

// StoreLike is the subset of *Store the Bridge needs. Defining it as an
// interface lets tests inject a fake without a live PG. *Store satisfies it.
type StoreLike interface {
	Get(ctx context.Context, id string) (*Agent, error)
	UpdateStatus(ctx context.Context, id string, status Status) error
}

// Bridge orchestrates dispatch through a registered Agent.
type Bridge struct {
	store     StoreLike
	creator   SessionCreator
	resolver  InstanceResolver
	attacher  TaskAttacher
}

// NewBridge constructs the Bridge. store is required; other deps may be nil
// (Send will return a clear error in that case).
func NewBridge(store StoreLike, creator SessionCreator, resolver InstanceResolver, attacher TaskAttacher) *Bridge {
	return &Bridge{store: store, creator: creator, resolver: resolver, attacher: attacher}
}

// SendOptions controls a dispatch.
type SendOptions struct {
	TaskID    string // optional; if set, the new session is auto-attached
	Role      string // task_session_links role; default "primary"
	AgentName string // override the agent's opencode "agent" field (e.g. "build")
	ModelID   string
	ProviderID string
	Directory  string
}

// SendResult is what Send returns.
type SendResult struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	SessionID  string `json:"session_id"`
	TaskID     string `json:"task_id,omitempty"`
	Attached   bool   `json:"attached"`
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

	// 1. Look up the agent → instance.
	agent, err := b.store.Get(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("lookup agent: %w", err)
	}

	// 2. Resolve instance → API base URL.
	apiBase, err := b.resolver.ResolveAPIBase(agent.InstanceID)
	if err != nil {
		_ = b.store.UpdateStatus(ctx, agentID, StatusOffline)
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
		_ = b.store.UpdateStatus(ctx, agentID, StatusBusy)
		return nil, fmt.Errorf("create session: %w", err)
	}
	if info == nil || info.ID == "" {
		return nil, fmt.Errorf("create session returned empty id")
	}

	// 4. Send the prompt.
	sendIn := &SendPromptInput{Agent: opts.AgentName, Text: prompt}
	if err := b.creator.SendPromptToSession(ctx, apiBase, info.ID, sendIn); err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	// 5. Mark agent busy (a session is now active).
	_ = b.store.UpdateStatus(ctx, agentID, StatusBusy)

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
		if err := b.attacher.AttachSession(ctx, opts.TaskID, agent.InstanceID, info.ID, role); err != nil {
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
