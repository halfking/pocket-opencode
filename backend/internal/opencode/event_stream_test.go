package opencode

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// fakeEventAdapter implements the bits of the adapter that the
// EventStreamManager uses. We embed the interface to satisfy the type check
// at construction; calls to the unused methods would panic but the tests
// below never trigger them.
type fakeEventAdapter struct {
	adapter.OpenCodeAdapter
	mu             sync.Mutex
	connectCount   int32
	instanceEvents map[string]chan adapter.OpenCodeEvent // baseURL -> eventCh
}

func newFakeEventAdapter() *fakeEventAdapter {
	return &fakeEventAdapter{
		instanceEvents: make(map[string]chan adapter.OpenCodeEvent),
	}
}

// SubscribeEvents satisfies EventSubscriber.
func (f *fakeEventAdapter) SubscribeEvents(ctx context.Context, baseURL, directory, workspaceID string) (<-chan adapter.OpenCodeEvent, func(), error) {
	atomic.AddInt32(&f.connectCount, 1)

	f.mu.Lock()
	ch := make(chan adapter.OpenCodeEvent, 64)
	f.instanceEvents[baseURL] = ch
	f.mu.Unlock()

	cancel := func() {}
	return ch, cancel, nil
}

func (f *fakeEventAdapter) emit(baseURL string, evt adapter.OpenCodeEvent) {
	f.mu.Lock()
	ch := f.instanceEvents[baseURL]
	f.mu.Unlock()
	if ch == nil {
		return
	}
	ch <- evt
}

func newTestManager() (*EventStreamManager, *fakeEventAdapter) {
	reg := registry.NewRegistry()
	reg.SetInstanceAPIBase("inst-a", "http://fake-a")
	ad := newFakeEventAdapter()
	mgr := NewEventStreamManager(reg, ad)
	return mgr, ad
}

func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return condition()
}

func TestEventStreamManager_FanoutToMultipleSubscribers(t *testing.T) {
	mgr, fake := newTestManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, cleanup1, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 16})
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	defer cleanup1()

	ch2, cleanup2, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 16})
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	defer cleanup2()

	if !waitFor(500*time.Millisecond, func() bool {
		return atomic.LoadInt32(&fake.connectCount) > 0
	}) {
		t.Fatalf("upstream never connected")
	}

	fake.emit("http://fake-a", adapter.OpenCodeEvent{
		ID:   "evt-1",
		Type: "test.event",
		Data: map[string]any{"sessionID": "ses-abc"},
	})

	for i, ch := range []<-chan DomainEvent{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.SessionID != "ses-abc" {
				t.Errorf("subscriber %d: got sessionID %q, want %q", i, evt.SessionID, "ses-abc")
			}
			if evt.Type != "test.event" {
				t.Errorf("subscriber %d: got type %q, want %q", i, evt.Type, "test.event")
			}
		case <-time.After(2 * time.Second):
			t.Errorf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestEventStreamManager_SlowSubscriberDoesNotBlock(t *testing.T) {
	mgr, fake := newTestManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fastCh, fastCleanup, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 16})
	if err != nil {
		t.Fatalf("subscribe fast: %v", err)
	}
	defer fastCleanup()

	// Slow subscriber: small buffer, never reads
	_, slowCleanup, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 1})
	if err != nil {
		t.Fatalf("subscribe slow: %v", err)
	}
	defer slowCleanup()

	if !waitFor(500*time.Millisecond, func() bool {
		return atomic.LoadInt32(&fake.connectCount) > 0
	}) {
		t.Fatalf("upstream never connected")
	}

	for i := 0; i < 5; i++ {
		fake.emit("http://fake-a", adapter.OpenCodeEvent{
			ID:   "evt",
			Type: "test.event",
		})
	}

	// Fast subscriber should receive all 5
	received := 0
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case _, ok := <-fastCh:
			if !ok {
				break loop
			}
			received++
			if received == 5 {
				break loop
			}
		case <-timeout:
			break loop
		}
	}

	if received != 5 {
		t.Errorf("fast subscriber received %d/5 events", received)
	}
}

func TestEventStreamManager_UnsubscribeStopsDelivery(t *testing.T) {
	mgr, fake := newTestManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, cleanup, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 16})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if !waitFor(500*time.Millisecond, func() bool {
		return atomic.LoadInt32(&fake.connectCount) > 0
	}) {
		t.Fatalf("upstream never connected")
	}

	cleanup()

	// Channel should be closed after cleanup
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("expected channel to be closed, got a value")
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("expected channel to be closed within 500ms after cleanup")
	}
}

func TestEventStreamManager_Stats(t *testing.T) {
	mgr, fake := newTestManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, cleanup, err := mgr.Subscribe(ctx, SubscribeOptions{InstanceID: "inst-a", BufferSize: 16})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()

	if !waitFor(500*time.Millisecond, func() bool {
		return atomic.LoadInt32(&fake.connectCount) > 0
	}) {
		t.Fatalf("upstream never connected")
	}

	for i := 0; i < 3; i++ {
		fake.emit("http://fake-a", adapter.OpenCodeEvent{ID: "evt", Type: "test.event"})
	}

	// Drain at least one event
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive event")
	}

	stats := mgr.Stats()
	if stats.TotalEvents == 0 {
		t.Errorf("expected TotalEvents > 0, got %d", stats.TotalEvents)
	}
	if stats.ActiveStreams != 1 {
		t.Errorf("expected ActiveStreams=1, got %d", stats.ActiveStreams)
	}
}

func TestExtractSessionID(t *testing.T) {
	cases := []struct {
		name string
		evt  adapter.OpenCodeEvent
		want string
	}{
		{
			name: "top-level sessionID",
			evt:  adapter.OpenCodeEvent{Data: map[string]any{"sessionID": "ses-1"}},
			want: "ses-1",
		},
		{
			name: "info.sessionID",
			evt:  adapter.OpenCodeEvent{Data: map[string]any{"info": map[string]any{"sessionID": "ses-2"}}},
			want: "ses-2",
		},
		{
			name: "properties.sessionID",
			evt:  adapter.OpenCodeEvent{Data: map[string]any{"properties": map[string]any{"sessionID": "ses-3"}}},
			want: "ses-3",
		},
		{
			name: "no sessionID",
			evt:  adapter.OpenCodeEvent{Data: map[string]any{"foo": "bar"}},
			want: "",
		},
		{
			name: "nil data",
			evt:  adapter.OpenCodeEvent{},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSessionID(tc.evt)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}