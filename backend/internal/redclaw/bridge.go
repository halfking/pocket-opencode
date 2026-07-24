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
}

// NewBridge 创建桥接服务
func NewBridge(client *Client, onEvent BridgeEventCallback) *Bridge {
	return &Bridge{
		client:  client,
		onEvent: onEvent,
		stopCh:  make(chan struct{}),
	}
}

// Start 启动桥接服务
func (b *Bridge) Start() {
	b.mu.Lock()
	b.connected = true
	b.mu.Unlock()

	log.Println("[RedClaw Bridge] started")

	// 启动后台健康检查
	go b.healthLoop()
}

// Stop 停止桥接服务
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.connected {
		return
	}

	b.connected = false
	close(b.stopCh)
	log.Println("[RedClaw Bridge] stopped")
}

// Chat LLM 对话
func (b *Bridge) Chat(req ChatRequest) (*ChatResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()

	if !connected {
		return nil, ErrBridgeNotConnected
	}

	return b.client.Chat(req)
}

// KnowledgeSearch 知识库检索
func (b *Bridge) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()

	if !connected {
		return nil, ErrBridgeNotConnected
	}

	return b.client.KnowledgeSearch(req)
}

// HealthCheck 健康检查
func (b *Bridge) HealthCheck() bool {
	_, err := b.client.Health()
	return err == nil
}

// IsConnected 返回连接状态
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