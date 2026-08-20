package redclaw

import (
	"log"
	"sync"
	"time"
)

// BridgeEventCallback WebSocket 事件推送回调
type BridgeEventCallback func(event BridgeEvent)

// Bridge RedClaw 桥接服务
type Bridge struct {
	client    *Client
	onEvent   BridgeEventCallback
	mu        sync.RWMutex
	connected bool
	stopCh    chan struct{}
	stopped   bool
}

// NewBridge creates a new RedClaw bridge service.
func NewBridge(client *Client, onEvent BridgeEventCallback) *Bridge {
	if client == nil {
		panic("redclaw: NewBridge called with nil client")
	}
	return &Bridge{
		client:  client,
		onEvent: onEvent,
		stopCh:  make(chan struct{}),
	}
}

// Start starts the bridge service and background health monitoring.
func (b *Bridge) Start() {
	b.mu.Lock()
	b.connected = true
	b.mu.Unlock()

	log.Println("[RedClaw Bridge] started")

	// 启动后台健康检查
	go b.healthLoop()
}

// Stop stops the bridge service and background health monitoring.
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		return
	}

	b.connected = false
	b.stopped = true
	close(b.stopCh)
	log.Println("[RedClaw Bridge] stopped")
}

// Chat sends a chat request to RedClaw with tenant isolation.
func (b *Bridge) Chat(req ChatRequest) (*ChatResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()

	if !connected {
		return nil, ErrBridgeNotConnected
	}

	return b.client.Chat(req)
}

// KnowledgeSearch searches the knowledge base with tenant isolation.
func (b *Bridge) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()

	if !connected {
		return nil, ErrBridgeNotConnected
	}

	return b.client.KnowledgeSearch(req)
}

// HealthCheck performs a health check against the RedClaw service.
func (b *Bridge) HealthCheck() bool {
	_, err := b.client.Health()
	return err == nil
}

// IsConnected returns whether the bridge is currently connected.
func (b *Bridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// healthLoop 定期健康检查
func (b *Bridge) healthLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			healthy := b.HealthCheck()
			b.mu.Lock()
			prevConnected := b.connected
			b.connected = healthy
			b.mu.Unlock()

			if prevConnected != healthy && b.onEvent != nil {
				eventType := "redclaw.connected"
				if !healthy {
					eventType = "redclaw.disconnected"
				}
				b.onEvent(BridgeEvent{
					Type:      eventType,
					Payload:   map[string]bool{"connected": healthy},
					Timestamp: time.Now(),
				})
			}
		case <-b.stopCh:
			return
		}
	}
}

// ErrBridgeNotConnected 桥接未连接错误
var ErrBridgeNotConnected = &BridgeError{"RedClaw bridge not connected"}

// BridgeError 桥接错误
type BridgeError struct {
	Message string
}

func (e *BridgeError) Error() string {
	return e.Message
}