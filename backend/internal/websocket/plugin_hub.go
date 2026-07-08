package websocket

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// PluginHub manages WebSocket connections for OpenCode plugins and managers
type PluginHub struct {
	// Connected OpenCode plugin instances
	plugins map[string]*PluginConnection

	// Connected instance managers
	managers map[string]*ManagerConnection

	// Connected mobile clients
	clients map[string]*ClientConnection

	// Broadcast channel
	broadcast chan Message

	// Register/unregister channels
	registerPlugin   chan *PluginConnection
	unregisterPlugin chan *PluginConnection
	registerManager  chan *ManagerConnection
	unregisterManager chan *ManagerConnection
	registerClient   chan *ClientConnection
	unregisterClient chan *ClientConnection

	// Mutex for thread-safe operations
	mu sync.RWMutex

	// —— 会话迁移方案：边端注册回调 ——
	// InstanceRegistrar 由 server 层注入（实现是 *registry.Registry），
	// 当插件/manager 发来 instance.register / heartbeat 时，把实例写入 Registry，
	// 让 /api/instances 能展示边端注册的真实实例（origin=registered）。
	// nil 时退化为仅打日志（向后兼容）。
	instanceRegistrar model.InstanceRegistrar
}

// SetInstanceRegistrar 注入实例注册器（server 装配时调用）。
func (h *PluginHub) SetInstanceRegistrar(r model.InstanceRegistrar) {
	h.mu.Lock()
	h.instanceRegistrar = r
	h.mu.Unlock()
}

type PluginConnection struct {
	ID       string
	Conn     *websocket.Conn
	Send     chan []byte
	Hub      *PluginHub
	Metadata PluginMetadata
}

type ManagerConnection struct {
	ID       string
	Conn     *websocket.Conn
	Send     chan []byte
	Hub      *PluginHub
	Metadata ManagerMetadata
}

type ClientConnection struct {
	ID       string
	Conn     *websocket.Conn
	Send     chan []byte
	Hub      *PluginHub
	Metadata ClientMetadata
}

type PluginMetadata struct {
	InstanceID  string    `json:"instanceID"`
	DisplayName string    `json:"displayName"`
	Version     string    `json:"version"`
	Environment string    `json:"environment"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ManagerMetadata struct {
	InstanceID  string    `json:"instanceID"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ClientMetadata struct {
	UserID      string    `json:"userID"`
	DeviceID    string    `json:"deviceID"`
	Platform    string    `json:"platform"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type PluginMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// NewPluginHub creates a new PluginHub
func NewPluginHub() *PluginHub {
	return &PluginHub{
		plugins:           make(map[string]*PluginConnection),
		managers:          make(map[string]*ManagerConnection),
		clients:           make(map[string]*ClientConnection),
		broadcast:         make(chan Message, 256),
		registerPlugin:    make(chan *PluginConnection),
		unregisterPlugin:  make(chan *PluginConnection),
		registerManager:   make(chan *ManagerConnection),
		unregisterManager: make(chan *ManagerConnection),
		registerClient:    make(chan *ClientConnection),
		unregisterClient:  make(chan *ClientConnection),
	}
}

// Run starts the hub's main loop
func (h *PluginHub) Run() {
	log.Println("[PluginHub] Starting...")

	for {
		select {
		// Plugin registration
		case conn := <-h.registerPlugin:
			h.mu.Lock()
			h.plugins[conn.ID] = conn
			h.mu.Unlock()
			log.Printf("[PluginHub] Plugin registered: %s (%s)", conn.ID, conn.Metadata.DisplayName)

			// Notify all clients
			h.broadcastToClients(Message{
				Type: "instance.online",
				Payload: map[string]interface{}{
					"instanceID":  conn.ID,
					"displayName": conn.Metadata.DisplayName,
					"timestamp":   time.Now(),
				},
			})

		case conn := <-h.unregisterPlugin:
			h.mu.Lock()
			if _, ok := h.plugins[conn.ID]; ok {
				delete(h.plugins, conn.ID)
				close(conn.Send)
			}
			h.mu.Unlock()
			log.Printf("[PluginHub] Plugin unregistered: %s", conn.ID)

			// Notify all clients
			h.broadcastToClients(Message{
				Type: "instance.offline",
				Payload: map[string]interface{}{
					"instanceID": conn.ID,
					"timestamp":  time.Now(),
				},
			})

		// Manager registration
		case conn := <-h.registerManager:
			h.mu.Lock()
			h.managers[conn.ID] = conn
			h.mu.Unlock()
			log.Printf("[PluginHub] Manager registered: %s", conn.ID)

		case conn := <-h.unregisterManager:
			h.mu.Lock()
			if _, ok := h.managers[conn.ID]; ok {
				delete(h.managers, conn.ID)
				close(conn.Send)
			}
			h.mu.Unlock()
			log.Printf("[PluginHub] Manager unregistered: %s", conn.ID)

		// Client registration
		case conn := <-h.registerClient:
			h.mu.Lock()
			h.clients[conn.ID] = conn
			h.mu.Unlock()
			log.Printf("[PluginHub] Client connected: %s", conn.ID)

			// Send current instance list
			h.sendInstanceListToClient(conn)

		case conn := <-h.unregisterClient:
			h.mu.Lock()
			if _, ok := h.clients[conn.ID]; ok {
				delete(h.clients, conn.ID)
				close(conn.Send)
			}
			h.mu.Unlock()
			log.Printf("[PluginHub] Client disconnected: %s", conn.ID)

		// Broadcast message
		case message := <-h.broadcast:
			h.handleBroadcast(message)
		}
	}
}

// RegisterPlugin registers a new plugin connection
func (h *PluginHub) RegisterPlugin(conn *PluginConnection) {
	h.registerPlugin <- conn
}

// UnregisterPlugin unregisters a plugin connection
func (h *PluginHub) UnregisterPlugin(conn *PluginConnection) {
	h.unregisterPlugin <- conn
}

// RegisterManager registers a new manager connection
func (h *PluginHub) RegisterManager(conn *ManagerConnection) {
	h.registerManager <- conn
}

// UnregisterManager unregisters a manager connection
func (h *PluginHub) UnregisterManager(conn *ManagerConnection) {
	h.unregisterManager <- conn
}

// RegisterClient registers a new client connection
func (h *PluginHub) RegisterClient(conn *ClientConnection) {
	h.registerClient <- conn
}

// UnregisterClient unregisters a client connection
func (h *PluginHub) UnregisterClient(conn *ClientConnection) {
	h.unregisterClient <- conn
}

// Broadcast sends a message to all appropriate connections
func (h *PluginHub) Broadcast(message Message) {
	h.broadcast <- message
}

// handleBroadcast handles broadcasting logic
func (h *PluginHub) handleBroadcast(message Message) {
	switch message.Type {
	case "session.created", "session.updated", "session.completed":
		// Broadcast to all clients
		h.broadcastToClients(message)

	case "instance.status":
		// Broadcast to all clients
		h.broadcastToClients(message)

	default:
		log.Printf("[PluginHub] Unknown broadcast type: %s", message.Type)
	}
}

// broadcastToClients sends a message to all connected clients
func (h *PluginHub) broadcastToClients(message Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("[PluginHub] Failed to marshal message: %v", err)
		return
	}

	for _, client := range h.clients {
		select {
		case client.Send <- data:
		default:
			// Client buffer full, skip
			log.Printf("[PluginHub] Client %s buffer full, skipping message", client.ID)
		}
	}
}

// SendCommandToInstance sends a command to a specific instance
func (h *PluginHub) SendCommandToInstance(instanceID string, command Message) error {
	h.mu.RLock()
	plugin, ok := h.plugins[instanceID]
	h.mu.RUnlock()

	if !ok {
		// Instance not connected, try manager
		return h.SendCommandToManager(instanceID, command)
	}

	data, err := json.Marshal(command)
	if err != nil {
		return err
	}

	select {
	case plugin.Send <- data:
		return nil
	default:
		return nil // Buffer full, command dropped
	}
}

// SendCommandToManager sends a command to an instance manager
func (h *PluginHub) SendCommandToManager(instanceID string, command Message) error {
	h.mu.RLock()
	manager, ok := h.managers[instanceID]
	h.mu.RUnlock()

	if !ok {
		log.Printf("[PluginHub] Manager not found: %s", instanceID)
		return nil
	}

	data, err := json.Marshal(command)
	if err != nil {
		return err
	}

	select {
	case manager.Send <- data:
		return nil
	default:
		return nil
	}
}

// sendInstanceListToClient sends current instance list to a newly connected client
func (h *PluginHub) sendInstanceListToClient(client *ClientConnection) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	instances := make([]map[string]interface{}, 0, len(h.plugins))
	for _, plugin := range h.plugins {
		instances = append(instances, map[string]interface{}{
			"instanceID":  plugin.ID,
			"displayName": plugin.Metadata.DisplayName,
			"version":     plugin.Metadata.Version,
			"environment": plugin.Metadata.Environment,
			"status":      "online",
		})
	}

	message := PluginMessage{
		Type: "instance.list",
		Data: mustMarshal(map[string]interface{}{
			"instances": instances,
		}),
	}

	data, _ := json.Marshal(message)
	select {
	case client.Send <- data:
	default:
	}
}

// GetConnectedInstances returns list of connected instances
func (h *PluginHub) GetConnectedInstances() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	instances := make([]string, 0, len(h.plugins))
	for id := range h.plugins {
		instances = append(instances, id)
	}
	return instances
}

// GetConnectedManagers returns list of connected managers
func (h *PluginHub) GetConnectedManagers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	managers := make([]string, 0, len(h.managers))
	for id := range h.managers {
		managers = append(managers, id)
	}
	return managers
}

// GetConnectedClients returns count of connected clients
func (h *PluginHub) GetConnectedClients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.clients)
}

// Helper function to marshal data
func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[PluginHub] Marshal error: %v", err)
		return json.RawMessage("{}")
	}
	return data
}

// ReadPump pumps messages from the websocket connection to the hub
func (c *PluginConnection) ReadPump() {
	defer func() {
		c.Hub.UnregisterPlugin(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[PluginConnection] Invalid message: %v", err)
			continue
		}

		// Handle plugin messages
		c.handleMessage(msg)
	}
}

// WritePump pumps messages from the hub to the websocket connection
func (c *PluginConnection) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles messages from plugin
func (c *PluginConnection) handleMessage(msg Message) {
	switch msg.Type {
	case "instance.register":
		// 解析注册消息中的完整 InstanceInfo（含 machine/capabilities/version），
		// 写入 Registry 使其出现在 /api/instances（origin=registered）。
		log.Printf("[PluginConnection] Instance %s sent register", c.ID)
		c.Hub.applyRegisteredInstance(msg, c)

	case "session.created", "session.updated", "session.completed":
		// Broadcast to all clients
		c.Hub.Broadcast(msg)

	case "heartbeat":
		// Update last heartbeat time + 触发 Registry 心跳
		log.Printf("[PluginConnection] Heartbeat from %s", c.ID)
		c.Hub.touchInstance(c.ID)

	case "pong":
		// Pong response
		break

	default:
		log.Printf("[PluginConnection] Unknown message type: %s", msg.Type)
	}
}

// applyRegisteredInstance 把 instance.register 消息映射成 RegisteredInstanceInfo 并写入 Registry。
func (h *PluginHub) applyRegisteredInstance(msg Message, c *PluginConnection) {
	h.mu.RLock()
	reg := h.instanceRegistrar
	h.mu.RUnlock()
	if reg == nil {
		return // 未注入 Registry，仅日志（向后兼容）
	}

	// 注册消息 data 结构对齐 opencode-plugin InstanceInfo
	var payload struct {
		ID           string `json:"id"`
		DisplayName  string `json:"displayName"`
		Version      string `json:"version"`
		Environment  string `json:"environment"`
		Capabilities []string `json:"capabilities"`
		APIBaseURL   string `json:"apiBaseURL"`
		Machine      struct {
			Hostname string `json:"hostname"`
			Platform string `json:"platform"`
			Arch     string `json:"arch"`
			CPUs     int    `json:"cpus"`
			Memory   int64  `json:"memory"` // 字节
		} `json:"machine"`
	}
	raw, _ := json.Marshal(msg.Payload)
	if err := json.Unmarshal(raw, &payload); err != nil {
		log.Printf("[PluginHub] parse instance.register: %v", err)
		return
	}
	if payload.ID == "" {
		payload.ID = c.ID
	}
	if payload.DisplayName == "" {
		payload.DisplayName = c.Metadata.DisplayName
	}

	info := model.RegisteredInstanceInfo{
		ID:           payload.ID,
		DisplayName:  payload.DisplayName,
		APIBaseURL:   payload.APIBaseURL,
		Environment:  payload.Environment,
		Version:      payload.Version,
		Capabilities: payload.Capabilities,
		Hostname:     payload.Machine.Hostname,
		Platform:     payload.Machine.Platform,
		Arch:         payload.Machine.Arch,
		CPUs:         payload.Machine.CPUs,
		MemoryMB:     payload.Machine.Memory / 1024 / 1024,
	}
	if err := reg.RegisterRegisteredInstance(info); err != nil {
		log.Printf("[PluginHub] register instance %s: %v", info.ID, err)
	}
}

// touchInstance 触发 Registry 心跳更新（实例仍在线）。
func (h *PluginHub) touchInstance(instanceID string) {
	h.mu.RLock()
	reg := h.instanceRegistrar
	h.mu.RUnlock()
	if reg == nil {
		return
	}
	reg.TouchInstance(instanceID)
}

// ReadPump pumps messages from the websocket connection to the hub
func (c *ManagerConnection) ReadPump() {
	defer func() {
		c.Hub.UnregisterManager(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[ManagerConnection] Invalid message: %v", err)
			continue
		}

		// Handle manager messages
		c.handleMessage(msg)
	}
}

// WritePump pumps messages from the hub to the websocket connection
func (c *ManagerConnection) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles messages from manager
func (c *ManagerConnection) handleMessage(msg Message) {
	switch msg.Type {
	case "manager.register":
		// Already registered in connection setup
		log.Printf("[ManagerConnection] Manager %s sent register", c.ID)

	case "instance.status":
		// Broadcast to all clients
		c.Hub.Broadcast(msg)

	case "heartbeat":
		// Update last heartbeat time
		log.Printf("[ManagerConnection] Heartbeat from %s", c.ID)

	case "pong":
		// Pong response
		break

	default:
		log.Printf("[ManagerConnection] Unknown message type: %s", msg.Type)
	}
}

// ReadPump pumps messages from the websocket connection to the hub
func (c *ClientConnection) ReadPump() {
	defer func() {
		c.Hub.UnregisterClient(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[ClientConnection] Invalid message: %v", err)
			continue
		}

		// Handle client messages
		c.handleMessage(msg)
	}
}

// WritePump pumps messages from the hub to the websocket connection
func (c *ClientConnection) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles messages from client
func (c *ClientConnection) handleMessage(msg Message) {
	switch msg.Type {
	case "client.register":
		// Already registered in connection setup
		log.Printf("[ClientConnection] Client %s sent register", c.ID)

	case "command":
		// Forward command to appropriate instance
		log.Printf("[ClientConnection] Command from %s: %s", c.ID, msg.Type)

	case "heartbeat":
		// Update last heartbeat time
		log.Printf("[ClientConnection] Heartbeat from %s", c.ID)

	case "pong":
		// Pong response
		break

	default:
		log.Printf("[ClientConnection] Unknown message type: %s", msg.Type)
	}
}
