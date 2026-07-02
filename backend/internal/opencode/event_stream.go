// Package opencode contains the application-level OpenCode domain managers:
// event stream subscription with fan-out, permission/question handling, and
// session orchestration. These build on top of the lower-level adapters in
// internal/adapter.
package opencode

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// EventSubscriber is the adapter capability required for SSE subscription.
// The HTTP adapter implements this; the DB adapter does not.
type EventSubscriber interface {
	SubscribeEvents(ctx context.Context, baseURL, directory, workspaceID string) (<-chan adapter.OpenCodeEvent, func(), error)
}

// EventStreamManager manages SSE event subscriptions to one or more OpenCode
// instances and fan-outs events to local subscribers. Each instance gets its
// own upstream goroutine that re-connects automatically on disconnect.
//
// Subscribers receive a per-subscriber buffered channel of DomainEvent. The
// manager guarantees that:
//
//   - Slow subscribers do not back-pressure other subscribers
//   - Disconnected upstreams automatically reconnect with exponential backoff
//   - Subscribers are removed cleanly on Unsubscribe
type EventStreamManager struct {
	registry *registry.Registry
	adapter  adapter.OpenCodeAdapter

	mu      sync.RWMutex
	streams map[string]*instanceStream // key: instanceID
	closed  bool
	closeCh chan struct{}

	// Metrics for observability
	muMetrics    sync.RWMutex
	totalEvents  uint64
	totalReconns uint64
	totalErrors  uint64
}

// instanceStream is the per-instance SSE connection state.
type instanceStream struct {
	instanceID  string
	baseURL     string
	directory   string
	workspaceID string

	mu        sync.RWMutex
	subs      map[uint64]*subscription
	nextSubID uint64
	cancelUp  context.CancelFunc
	connected bool
	lastError string
	lastEvent time.Time
}

// subscription is a single subscriber to an instance stream.
type subscription struct {
	id uint64
	ch chan DomainEvent
}

// DomainEvent is the application-facing event envelope passed to subscribers.
// It wraps the raw OpenCode event with routing metadata so subscribers don't
// need to know about individual instance IDs.
type DomainEvent struct {
	InstanceID string                `json:"instanceId"`
	SessionID  string                `json:"sessionId,omitempty"`
	Type       string                `json:"type"`
	Raw        adapter.OpenCodeEvent `json:"raw"`
	ReceivedAt time.Time             `json:"receivedAt"`
}

// NewEventStreamManager creates a new event stream manager.
// It does not connect to any instance until Subscribe is called.
func NewEventStreamManager(reg *registry.Registry, ad adapter.OpenCodeAdapter) *EventStreamManager {
	return &EventStreamManager{
		registry: reg,
		adapter:  ad,
		streams:  make(map[string]*instanceStream),
		closeCh:  make(chan struct{}),
	}
}

// SubscribeOptions configures a single subscription.
type SubscribeOptions struct {
	InstanceID  string // required
	Directory   string // optional: location directory filter
	WorkspaceID string // optional: location workspace filter
	BufferSize  int    // optional: per-subscriber buffer (default 64)
}

// Subscribe registers a subscriber for events from the given instance.
// It returns a channel of DomainEvent. The channel is closed when:
//   - ctx is cancelled
//   - the manager is closed
//   - the upstream stream permanently fails (after exhausting retries)
//
// The returned cleanup function must be called to release resources.
func (m *EventStreamManager) Subscribe(ctx context.Context, opts SubscribeOptions) (<-chan DomainEvent, func(), error) {
	if opts.InstanceID == "" {
		return nil, nil, fmt.Errorf("SubscribeOptions.InstanceID is required")
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 64
	}

	stream, err := m.getOrCreateStream(ctx, opts)
	if err != nil {
		return nil, nil, err
	}

	sub := stream.addSubscriber(opts.BufferSize)
	cleanup := func() {
		stream.removeSubscriber(sub.id)
	}

	return sub.ch, cleanup, nil
}

// PublishEvent injects an event into the manager's fan-out. This is used by
// the permission/question managers (and tests) to deliver synthetic events
// without going through the SSE channel.
func (m *EventStreamManager) PublishEvent(evt DomainEvent) {
	m.fanout(evt)
}

// Close stops all upstream connections and closes all subscriber channels.
func (m *EventStreamManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.closed {
		return
	}
	m.closed = true
	streams := m.streams
	m.streams = make(map[string]*instanceStream)
	close(m.closeCh)

	// Release lock before shutting down streams to avoid holding lock during I/O
	m.mu.Unlock()
	for _, s := range streams {
		s.shutdown()
	}
	m.mu.Lock() // Re-acquire for defer
}

// Stats returns a snapshot of manager-level metrics.
func (m *EventStreamManager) Stats() EventStreamStats {
	m.muMetrics.RLock()
	defer m.muMetrics.RUnlock()
	return EventStreamStats{
		TotalEvents:   m.totalEvents,
		TotalReconns:  m.totalReconns,
		TotalErrors:   m.totalErrors,
		ActiveStreams: m.activeStreamCount(),
	}
}

// EventStreamStats is a snapshot of metrics returned by Stats.
type EventStreamStats struct {
	TotalEvents   uint64 `json:"totalEvents"`
	TotalReconns  uint64 `json:"totalReconnects"`
	TotalErrors   uint64 `json:"totalErrors"`
	ActiveStreams int    `json:"activeStreams"`
}

// =============================================================================
// Internal helpers
// =============================================================================

func (m *EventStreamManager) getOrCreateStream(ctx context.Context, opts SubscribeOptions) (*instanceStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, fmt.Errorf("event stream manager is closed")
	}

	if existing, ok := m.streams[opts.InstanceID]; ok {
		// Update filters (best effort; new filters apply on next reconnect)
		existing.mu.Lock()
		defer existing.mu.Unlock()
		if opts.Directory != "" {
			existing.directory = opts.Directory
		}
		if opts.WorkspaceID != "" {
			existing.workspaceID = opts.WorkspaceID
		}
		return existing, nil
	}

	baseURL, err := m.registry.GetInstanceAPIBase(opts.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("resolve instance base URL: %w", err)
	}

	stream := &instanceStream{
		instanceID:  opts.InstanceID,
		baseURL:     baseURL,
		directory:   opts.Directory,
		workspaceID: opts.WorkspaceID,
		subs:        make(map[uint64]*subscription),
	}

	m.streams[opts.InstanceID] = stream
	go m.runUpstreamLoop(stream)

	return stream, nil
}

func (m *EventStreamManager) runUpstreamLoop(stream *instanceStream) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	const initialBackoff = time.Second

	for {
		select {
		case <-m.closeCh:
			return
		default:
		}

		stream.mu.RLock()
		directory := stream.directory
		workspaceID := stream.workspaceID
		baseURL := stream.baseURL
		stream.mu.RUnlock()

		ctx, cancel := context.WithCancel(context.Background())
		stream.mu.Lock()
		stream.cancelUp = cancel
		stream.mu.Unlock()

		err := m.connectAndPump(ctx, stream, baseURL, directory, workspaceID)
		cancel()

		stream.mu.Lock()
		defer stream.mu.Unlock()
		stream.connected = false
		if err != nil {
			stream.lastError = err.Error()
		}

		m.incError()

		select {
		case <-m.closeCh:
			return
		case <-time.After(backoff):
		}

		// Exponential backoff with cap
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		if backoff == 0 {
			backoff = initialBackoff
		}

		m.incReconnect()
	}
}

func (m *EventStreamManager) connectAndPump(ctx context.Context, stream *instanceStream, baseURL, directory, workspaceID string) error {
	sub, ok := m.adapter.(EventSubscriber)
	if !ok {
		return fmt.Errorf("adapter %T does not support event subscription", m.adapter)
	}

	events, cancelUp, err := sub.SubscribeEvents(ctx, baseURL, directory, workspaceID)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}
	defer cancelUp()

	stream.mu.Lock()
	stream.connected = true
	stream.lastError = ""
	stream.mu.Unlock()

	log.Printf("[event-stream] connected instance=%s directory=%s workspace=%s", stream.instanceID, directory, workspaceID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.closeCh:
			return fmt.Errorf("manager closed")
		case rawEvent, ok := <-events:
			if !ok {
				return fmt.Errorf("upstream channel closed")
			}

			stream.mu.Lock()
			stream.lastEvent = time.Now()
			stream.mu.Unlock()

			domain := DomainEvent{
				InstanceID: stream.instanceID,
				Type:       rawEvent.Type,
				Raw:        rawEvent,
				ReceivedAt: time.Now(),
			}

			// Try to extract sessionID from common event payload shapes
			if sid := extractSessionID(rawEvent); sid != "" {
				domain.SessionID = sid
			}

			m.fanout(domain)
			m.incEvent()
		}
	}
}

// extractSessionID pulls a sessionID out of common event payload shapes:
//   - { sessionID: "ses_xxx" }   (permission/question events)
//   - { info: { sessionID: "ses_xxx" } }
//   - { properties: { sessionID: "ses_xxx" } }
func extractSessionID(evt adapter.OpenCodeEvent) string {
	if data, ok := evt.Data.(map[string]any); ok {
		if v, ok := data["sessionID"].(string); ok && v != "" {
			return v
		}
		if info, ok := data["info"].(map[string]any); ok {
			if v, ok := info["sessionID"].(string); ok && v != "" {
				return v
			}
		}
		if props, ok := data["properties"].(map[string]any); ok {
			if v, ok := props["sessionID"].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

func (m *EventStreamManager) fanout(evt DomainEvent) {
	m.mu.RLock()
	stream, ok := m.streams[evt.InstanceID]
	m.mu.RUnlock()
	if !ok {
		return
	}

	stream.mu.RLock()
	subs := make([]*subscription, 0, len(stream.subs))
	for _, s := range stream.subs {
		subs = append(subs, s)
	}
	stream.mu.RUnlock()

	for _, s := range subs {
		select {
		case s.ch <- evt:
		default:
			// Subscriber buffer full; drop event for that subscriber only
			log.Printf("[event-stream] dropping event for subscriber %d on instance %s (buffer full)", s.id, evt.InstanceID)
		}
	}
}

func (m *EventStreamManager) incEvent() {
	m.muMetrics.Lock()
	defer m.muMetrics.Unlock()
	m.totalEvents++
}

func (m *EventStreamManager) incReconnect() {
	m.muMetrics.Lock()
	defer m.muMetrics.Unlock()
	m.totalReconns++
}

func (m *EventStreamManager) incError() {
	m.muMetrics.Lock()
	defer m.muMetrics.Unlock()
	m.totalErrors++
}

func (m *EventStreamManager) activeStreamCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, s := range m.streams {
		s.mu.RLock()
		if s.connected {
			count++
		}
		s.mu.RUnlock()
	}
	return count
}

// =============================================================================
// instanceStream methods
// =============================================================================

func (s *instanceStream) addSubscriber(bufSize int) *subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSubID++
	id := s.nextSubID
	sub := &subscription{
		id: id,
		ch: make(chan DomainEvent, bufSize),
	}
	s.subs[id] = sub
	return sub
}

func (s *instanceStream) removeSubscriber(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub, ok := s.subs[id]; ok {
		close(sub.ch)
		delete(s.subs, id)
	}
}

func (s *instanceStream) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancel := s.cancelUp
	subs := s.subs
	s.subs = make(map[uint64]*subscription)

	if cancel != nil {
		cancel()
	}
	for _, sub := range subs {
		close(sub.ch)
	}
}

// StreamStatus is a snapshot of an instance stream.
type StreamStatus struct {
	InstanceID  string    `json:"instanceId"`
	Connected   bool      `json:"connected"`
	LastError   string    `json:"lastError,omitempty"`
	LastEvent   time.Time `json:"lastEvent,omitempty"`
	Subscribers int       `json:"subscribers"`
}

// StreamStatus returns the current status of the stream for an instance.
func (m *EventStreamManager) StreamStatus(instanceID string) (StreamStatus, bool) {
	m.mu.RLock()
	s, ok := m.streams[instanceID]
	m.mu.RUnlock()
	if !ok {
		return StreamStatus{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StreamStatus{
		InstanceID:  instanceID,
		Connected:   s.connected,
		LastError:   s.lastError,
		LastEvent:   s.lastEvent,
		Subscribers: len(s.subs),
	}, true
}