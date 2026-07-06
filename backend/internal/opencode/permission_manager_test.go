package opencode

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// fakeInstance creates a PocketInstance stub for testing.
func fakeInstance(id, name string) *model.PocketInstance {
	return &model.PocketInstance{
		ID:           id,
		DisplayName:  name,
		Health:       "healthy",
		Capabilities: []string{"session", "permission", "question"},
	}
}

// fakePermissionAdapter implements PermissionCaller for the test. We embed
// the interface to satisfy the type system.
type fakePermissionAdapter struct {
	adapter.OpenCodeAdapter
	mu sync.Mutex

	pendingRequests map[string][]adapter.PermissionRequest // key: sessionID
	replyCalls       int32
	replied          []repliedPermission
}

type repliedPermission struct {
	instanceID string
	sessionID  string
	requestID  string
	reply      adapter.PermissionReply
	message    string
}

func newFakePermissionAdapter() *fakePermissionAdapter {
	return &fakePermissionAdapter{
		pendingRequests: make(map[string][]adapter.PermissionRequest),
	}
}

func (f *fakePermissionAdapter) setPending(sessionID string, requests []adapter.PermissionRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingRequests[sessionID] = requests
}

func (f *fakePermissionAdapter) GetAllPendingPermissionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.PermissionRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := make([]adapter.PermissionRequest, 0)
	for _, list := range f.pendingRequests {
		all = append(all, list...)
	}
	return all, nil
}

func (f *fakePermissionAdapter) GetPermissionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.PermissionRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pendingRequests[sessionID], nil
}

func (f *fakePermissionAdapter) ReplyPermission(ctx context.Context, baseURL, sessionID, requestID string, reply adapter.PermissionReply, message string) error {
	atomic.AddInt32(&f.replyCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replied = append(f.replied, repliedPermission{
		instanceID: baseURL,
		sessionID:  sessionID,
		requestID:  requestID,
		reply:      reply,
		message:    message,
	})
	return nil
}

func (f *fakePermissionAdapter) replyCount() int32 {
	return atomic.LoadInt32(&f.replyCalls)
}

func newTestPermissionManager() (*PermissionManager, *fakePermissionAdapter) {
	reg := registry.NewRegistry()
	reg.SetInstanceAPIBase("inst-a", "http://fake-a")
	reg.RegisterInstance(fakeInstance("inst-a", "http://fake-a"))

	ad := newFakePermissionAdapter()
	mgr := NewPermissionManager(reg, ad, PermissionManagerOptions{
		PollInterval: 50 * time.Millisecond,
	}, nil)
	return mgr, ad
}

func TestPermissionManager_EmitsNewAndResolved(t *testing.T) {
	mgr, ad := newTestPermissionManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, cleanup := mgr.Subscribe(16)
	defer cleanup()

	go mgr.Start(ctx)

	// Set a pending permission request
	ad.setPending("ses-1", []adapter.PermissionRequest{
		{ID: "per-1", SessionID: "ses-1", Action: "bash", Resources: []string{"ls"}},
	})

	// Wait for "new" event
	select {
	case evt := <-sub:
		if evt.Type != "new" {
			t.Errorf("expected new event, got %q", evt.Type)
		}
		if evt.RequestID != "per-1" {
			t.Errorf("expected requestID per-1, got %q", evt.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive new event")
	}

	// Verify it's listed
	pending := mgr.ListPending("", "")
	if len(pending) != 1 || pending[0].ID != "per-1" {
		t.Errorf("expected 1 pending request per-1, got %d", len(pending))
	}

	// Clear the request from upstream and wait for "expired" event
	ad.setPending("ses-1", nil)

	select {
	case evt := <-sub:
		if evt.Type != "expired" {
			t.Errorf("expected expired event, got %q", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive expired event")
	}

	// Pending set should be empty
	pending = mgr.ListPending("", "")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending requests, got %d", len(pending))
	}
}

func TestPermissionManager_ReplyForwardsToAdapter(t *testing.T) {
	mgr, ad := newTestPermissionManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Start(ctx)

	ad.setPending("ses-1", []adapter.PermissionRequest{
		{ID: "per-1", SessionID: "ses-1", Action: "bash", Resources: []string{"ls"}},
	})

	// Wait for it to be cached
	if !waitFor(2*time.Second, func() bool {
		return len(mgr.ListPending("", "")) == 1
	}) {
		t.Fatalf("permission request was not cached")
	}

	err := mgr.Reply(ctx, "inst-a", "ses-1", "per-1", adapter.PermissionReplyOnce, "ok")
	if err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	if ad.replyCount() != 1 {
		t.Errorf("expected 1 reply call to adapter, got %d", ad.replyCount())
	}

	if len(mgr.ListPending("", "")) != 0 {
		t.Errorf("expected pending set to be empty after reply")
	}
}
