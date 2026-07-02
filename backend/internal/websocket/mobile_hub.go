package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// MobileWSHub manages WebSocket connections for mobile clients and
// broadcasts real-time events from OpenCode instances.
type MobileWSHub struct {
	// Registered clients
	clients map[*MobileClient]bool

	// Session subscriptions: sessionID -> set of clients
	sessionSubs map[string]map[*MobileClient]bool

	// Broadcast channel for all clients
	broadcast chan MobileEvent

	// Register/unregister requests
	register   chan *MobileClient
	unregister chan *MobileClient

	// Session subscription requests
	subscribe   chan *subscriptionRequest
	unsubscribe chan *subscriptionRequest

	// Event stream manager
	eventMgr *opencode.EventStreamManager

	// Permission manager
	permMgr *opencode.PermissionManager

	// Question manager
	questionMgr *opencode.QuestionManager

	mu     sync.RWMutex
	closed bool
}

// MobileClient represents a connected mobile WebSocket client.
type MobileClient struct {
	hub  *MobileWSHub
	conn *websocket.Conn
	send chan []byte

	// Client metadata
	userID    string
	deviceID  string
	connected time.Time

	// Subscribed sessions
	sessions map[string]bool
	mu       sync.RWMutex
}

// MobileEvent is the envelope for all WebSocket messages.
type MobileEvent struct {
	Type      string      `json:"type"`
	SessionID string      `json:"sessionId,omitempty"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

// subscriptionRequest represents a client's session subscription request.
type subscriptionRequest struct {
	client    *MobileClient
	sessionID string
}

// Event type constants
const (
	EventSessionUpdated      = "session.updated"
	EventMessageAdded        = "message.added"
	EventPermissionAsked     = "permission.asked"
	EventPermissionReplied   = "permission.replied"
	EventQuestionAsked       = "question.asked"
	EventQuestionReplied     = "question.replied"
	EventToolProgress        = "tool.progress"
	EventSessionStatusChange = "session.status.changed"
	EventPing                = "ping"
	EventPong                = "pong"
)

// NewMobileWSHub creates a new WebSocket hub for mobile clients.
func NewMobileWSHub(
	eventMgr *opencode.EventStreamManager,
	permMgr *opencode.PermissionManager,
	questionMgr *opencode.QuestionManager,
) *MobileWSHub {
	return &MobileWSHub{
		clients:     make(map[*MobileClient]bool),
		sessionSubs: make(map[string]map[*MobileClient]bool),
		broadcast:   make(chan MobileEvent, 256),
		register:    make(chan *MobileClient),
		unregister:  make(chan *MobileClient),
		subscribe:   make(chan *subscriptionRequest),
		unsubscribe: make(chan *subscriptionRequest),
		eventMgr:    eventMgr,
		permMgr:     permMgr,
		questionMgr: questionMgr,
	}
}

// Run starts the hub's main loop.
func (h *MobileWSHub) Run(ctx context.Context) {
	// Subscribe to permission events
	permEvents, permCleanup := h.permMgr.Subscribe(64)
	defer permCleanup()

	// Subscribe to question events
	questionEvents, questionCleanup := h.questionMgr.Subscribe(64)
	defer questionCleanup()

	// Heartbeat ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return

		case client := <-h.register:
			h.clients[client] = true
			log.Printf("[ws-hub] client connected: %s (total: %d)", client.deviceID, len(h.clients))

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.removeClientFromAllSessions(client)
				log.Printf("[ws-hub] client disconnected: %s (total: %d)", client.deviceID, len(h.clients))
			}

		case req := <-h.subscribe:
			h.addSessionSubscription(req.sessionID, req.client)
			log.Printf("[ws-hub] client %s subscribed to session %s", req.client.deviceID, req.sessionID)

		case req := <-h.unsubscribe:
			h.removeSessionSubscription(req.sessionID, req.client)
			log.Printf("[ws-hub] client %s unsubscribed from session %s", req.client.deviceID, req.sessionID)

		case event := <-h.broadcast:
			h.broadcastEvent(event)

		case permEvent := <-permEvents:
			// Convert permission event to mobile event
			mobileEvent := MobileEvent{
				Type:      mapPermissionEventType(permEvent.Type),
				SessionID: permEvent.SessionID,
				Data:      permEvent,
				Timestamp: permEvent.Timestamp,
			}
			h.broadcastToSession(permEvent.SessionID, mobileEvent)

		case questionEvent := <-questionEvents:
			// Convert question event to mobile event
			mobileEvent := MobileEvent{
				Type:      mapQuestionEventType(questionEvent.Type),
				SessionID: questionEvent.SessionID,
				Data:      questionEvent,
				Timestamp: questionEvent.Timestamp,
			}
			h.broadcastToSession(questionEvent.SessionID, mobileEvent)

		case <-ticker.C:
			// Send heartbeat to all clients
			h.sendHeartbeat()
		}
	}
}

// ServeClient handles a new WebSocket connection.
func (h *MobileWSHub) ServeClient(conn *websocket.Conn, userID, deviceID string) {
	client := &MobileClient{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, 256),
		userID:    userID,
		deviceID:  deviceID,
		connected: time.Now(),
		sessions:  make(map[string]bool),
	}

	h.register <- client

	// Start read and write pumps
	go client.writePump()
	go client.readPump()
}

// BroadcastEvent sends an event to all connected clients or to a specific session.
func (h *MobileWSHub) BroadcastEvent(event MobileEvent) {
	event.Timestamp = time.Now()
	h.broadcast <- event
}

// =============================================================================
// Internal methods
// =============================================================================

func (h *MobileWSHub) addSessionSubscription(sessionID string, client *MobileClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.sessionSubs[sessionID] == nil {
		h.sessionSubs[sessionID] = make(map[*MobileClient]bool)
	}
	h.sessionSubs[sessionID][client] = true

	client.mu.Lock()
	client.sessions[sessionID] = true
	client.mu.Unlock()
}

func (h *MobileWSHub) removeSessionSubscription(sessionID string, client *MobileClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if subs, ok := h.sessionSubs[sessionID]; ok {
		delete(subs, client)
		if len(subs) == 0 {
			delete(h.sessionSubs, sessionID)
		}
	}

	client.mu.Lock()
	delete(client.sessions, sessionID)
	client.mu.Unlock()
}

func (h *MobileWSHub) removeClientFromAllSessions(client *MobileClient) {
	client.mu.RLock()
	sessions := make([]string, 0, len(client.sessions))
	for sessionID := range client.sessions {
		sessions = append(sessions, sessionID)
	}
	client.mu.RUnlock()

	for _, sessionID := range sessions {
		h.removeSessionSubscription(sessionID, client)
	}
}

func (h *MobileWSHub) broadcastEvent(event MobileEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[ws-hub] marshal event failed: %v", err)
		return
	}

	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			// Client buffer full, close connection
			close(client.send)
			delete(h.clients, client)
			h.removeClientFromAllSessions(client)
		}
	}
}

func (h *MobileWSHub) broadcastToSession(sessionID string, event MobileEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[ws-hub] marshal event failed: %v", err)
		return
	}

	h.mu.RLock()
	subscribers, ok := h.sessionSubs[sessionID]
	h.mu.RUnlock()

	if !ok || len(subscribers) == 0 {
		return
	}

	for client := range subscribers {
		select {
		case client.send <- data:
		default:
			// Client buffer full, skip
			log.Printf("[ws-hub] dropping event for client %s (buffer full)", client.deviceID)
		}
	}
}

func (h *MobileWSHub) sendHeartbeat() {
	ping := MobileEvent{
		Type:      EventPing,
		Timestamp: time.Now(),
	}
	data, _ := json.Marshal(ping)

	for client := range h.clients {
		select {
		case client.send <- data:
		default:
		}
	}
}

func (h *MobileWSHub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}
	h.closed = true

	for client := range h.clients {
		close(client.send)
	}
}

// =============================================================================
// MobileClient methods
// =============================================================================

// readPump reads messages from the WebSocket connection.
func (c *MobileClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[ws-client] read error: %v", err)
			}
			break
		}

		// Handle client messages (subscribe/unsubscribe)
		var msg struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId,omitempty"`
		}
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[ws-client] unmarshal message failed: %v", err)
			continue
		}

		switch msg.Type {
		case "subscribe":
			if msg.SessionID != "" {
				c.hub.subscribe <- &subscriptionRequest{
					client:    c,
					sessionID: msg.SessionID,
				}
			}
		case "unsubscribe":
			if msg.SessionID != "" {
				c.hub.unsubscribe <- &subscriptionRequest{
					client:    c,
					sessionID: msg.SessionID,
				}
			}
		case "pong":
			// Client responded to ping
		}
	}
}

// writePump sends messages to the WebSocket connection.
func (c *MobileClient) writePump() {
	ticker := time.NewTicker(50 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Batch writes if available
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// =============================================================================
// Helper functions
// =============================================================================

func mapPermissionEventType(permEventType string) string {
	switch permEventType {
	case "new":
		return EventPermissionAsked
	case "resolved":
		return EventPermissionReplied
	default:
		return "permission." + permEventType
	}
}

func mapQuestionEventType(questionEventType string) string {
	switch questionEventType {
	case "new":
		return EventQuestionAsked
	case "resolved":
		return EventQuestionReplied
	default:
		return "question." + questionEventType
	}
}
