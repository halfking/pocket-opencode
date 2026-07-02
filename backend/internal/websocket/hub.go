package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message WebSocket 消息
type Message struct {
	Type    string      `json:"type"`    // task_created, task_updated, session_attached, etc.
	Payload interface{} `json:"payload"` // 消息内容
}

// Client WebSocket 客户端
type Client struct {
	ID     string
	conn   *websocket.Conn
	send   chan Message
	hub    *Hub
	ctx    context.Context
	cancel context.CancelFunc
}

// Hub WebSocket 连接管理中心
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan Message
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
}

// NewHub 创建新的 Hub
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run 启动 Hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("WebSocket client connected: %s (total: %d)", client.ID, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("WebSocket client disconnected: %s (total: %d)", client.ID, len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			// 收集需要移除的客户端（发送缓冲区满）
			var toRemove []*Client
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// 客户端发送缓冲区满，标记为待移除
					toRemove = append(toRemove, client)
				}
			}
			h.mu.RUnlock()

			// 在读锁外统一移除（避免在 RLock 下写 map）
			if len(toRemove) > 0 {
				h.mu.Lock()
				for _, client := range toRemove {
					if _, ok := h.clients[client]; ok {
						close(client.send)
						delete(h.clients, client)
						log.Printf("WebSocket client removed (send buffer full): %s", client.ID)
					}
				}
				h.mu.Unlock()
			}
		}
	}
}

// Register 注册新客户端
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister 注销客户端
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// Broadcast 广播消息给所有客户端
func (h *Hub) Broadcast(msgType string, payload interface{}) {
	message := Message{
		Type:    msgType,
		Payload: payload,
	}
	select {
	case h.broadcast <- message:
	default:
		log.Printf("Warning: broadcast channel full, message dropped")
	}
}

// GetClientCount 获取当前连接的客户端数量
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// NewClient 创建新的客户端
func NewClient(hub *Hub, conn *websocket.Conn, clientID string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		ID:     clientID,
		conn:   conn,
		send:   make(chan Message, 256),
		hub:    hub,
		ctx:    ctx,
		cancel: cancel,
	}
}

// ReadPump 读取客户端消息
func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
		c.cancel()
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
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// 处理客户端发来的消息（心跳等）
		var msg Message
		if err := json.Unmarshal(message, &msg); err == nil {
			if msg.Type == "ping" {
				c.send <- Message{Type: "pong", Payload: time.Now()}
			}
		}
	}
}

// WritePump 向客户端发送消息
func (c *Client) WritePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Hub 关闭了 send channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// 发送 JSON 消息
			if err := c.conn.WriteJSON(message); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}

		case <-ticker.C:
			// 发送心跳
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}
