package websocket

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// 控制面错误：命令下发必须显式失败，避免调用方把"未送达"当成功。
var (
	// ErrInstanceNotConnected 表示目标实例在调用方 workspace 内不存在或未连接。
	// 跨 workspace 的实例同样返回该错误，不泄漏其它租户实例的存在性。
	ErrInstanceNotConnected = errors.New("instance not connected in workspace")
	// ErrCommandQueueFull 表示连接发送缓冲已满，命令未送达。
	ErrCommandQueueFull = errors.New("instance command queue is full")
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
	broadcast chan workspaceMessage

	// Register/unregister channels
	registerPlugin    chan *PluginConnection
	unregisterPlugin  chan *PluginConnection
	registerManager   chan *ManagerConnection
	unregisterManager chan *ManagerConnection
	registerClient    chan *ClientConnection
	unregisterClient  chan *ClientConnection

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
	WorkspaceID string    `json:"workspaceID"`
	UserID      string    `json:"userID"`
	DisplayName string    `json:"displayName"`
	Version     string    `json:"version"`
	Environment string    `json:"environment"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ManagerMetadata struct {
	InstanceID  string    `json:"instanceID"`
	WorkspaceID string    `json:"workspaceID"`
	UserID      string    `json:"userID"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ClientMetadata struct {
	UserID      string    `json:"userID"`
	WorkspaceID string    `json:"workspaceID"`
	DeviceID    string    `json:"deviceID"`
	Platform    string    `json:"platform"`
	ConnectedAt time.Time `json:"connectedAt"`
}

// workspaceMessage 把广播事件与其来源 workspace 绑在一起，
// 保证客户端只收到本租户实例的事件。
type workspaceMessage struct {
	workspaceID string
	message     Message
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
		broadcast:         make(chan workspaceMessage, 256),
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

			// Notify clients in the same workspace
			h.broadcastToClients(conn.Metadata.WorkspaceID, Message{
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

			// Notify clients in the same workspace
			h.broadcastToClients(conn.Metadata.WorkspaceID, Message{
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
		case wm := <-h.broadcast:
			h.handleBroadcast(wm)
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

// Broadcast sends a message to the clients of the originating workspace.
func (h *PluginHub) Broadcast(workspaceID string, message Message) {
	h.broadcast <- workspaceMessage{workspaceID: workspaceID, message: message}
}

// handleBroadcast handles broadcasting logic
func (h *PluginHub) handleBroadcast(wm workspaceMessage) {
	switch wm.message.Type {
	case "session.created", "session.updated", "session.completed":
		// Broadcast to clients in the same workspace
		h.broadcastToClients(wm.workspaceID, wm.message)

	case "instance.status":
		// Broadcast to clients in the same workspace
		h.broadcastToClients(wm.workspaceID, wm.message)

	default:
		log.Printf("[PluginHub] Unknown broadcast type: %s", wm.message.Type)
	}
}

// broadcastToClients sends a message to clients belonging to workspaceID.
func (h *PluginHub) broadcastToClients(workspaceID string, message Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("[PluginHub] Failed to marshal message: %v", err)
		return
	}

	for _, client := range h.clients {
		if client.Metadata.WorkspaceID != workspaceID {
			continue
		}
		select {
		case client.Send <- data:
		default:
			// Client buffer full, skip
			log.Printf("[PluginHub] Client %s buffer full, skipping message", client.ID)
		}
	}
}

// SendCommandToInstance sends a command to an instance owned by workspaceID.
// 目标实例不在该 workspace（或未连接）时返回 ErrInstanceNotConnected；
// 缓冲已满返回 ErrCommandQueueFull，调用方必须按失败处理。
func (h *PluginHub) SendCommandToInstance(workspaceID, instanceID string, command Message) error {
	h.mu.RLock()
	plugin, ok := h.plugins[instanceID]
	h.mu.RUnlock()

	if !ok || plugin.Metadata.WorkspaceID != workspaceID {
		// 插件不可用时回退到 manager（同样受 workspace 约束）
		return h.SendCommandToManager(workspaceID, instanceID, command)
	}

	data, err := json.Marshal(command)
	if err != nil {
		return err
	}

	select {
	case plugin.Send <- data:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

// SendCommandToManager sends a command to an instance manager owned by workspaceID.
func (h *PluginHub) SendCommandToManager(workspaceID, instanceID string, command Message) error {
	h.mu.RLock()
	manager, ok := h.managers[instanceID]
	h.mu.RUnlock()

	if !ok || manager.Metadata.WorkspaceID != workspaceID {
		return ErrInstanceNotConnected
	}

	data, err := json.Marshal(command)
	if err != nil {
		return err
	}

	select {
	case manager.Send <- data:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

// sendInstanceListToClient sends the workspace's instance list to a new client
func (h *PluginHub) sendInstanceListToClient(client *ClientConnection) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	instances := make([]map[string]interface{}, 0, len(h.plugins))
	for _, plugin := range h.plugins {
		if plugin.Metadata.WorkspaceID != client.Metadata.WorkspaceID {
			continue
		}
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

// GetConnectedInstances returns connected instances owned by workspaceID.
func (h *PluginHub) GetConnectedInstances(workspaceID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	instances := make([]string, 0, len(h.plugins))
	for id, conn := range h.plugins {
		if conn.Metadata.WorkspaceID != workspaceID {
			continue
		}
		instances = append(instances, id)
	}
	return instances
}

// GetConnectedManagers returns connected managers owned by workspaceID.
func (h *PluginHub) GetConnectedManagers(workspaceID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	managers := make([]string, 0, len(h.managers))
	for id, conn := range h.managers {
		if conn.Metadata.WorkspaceID != workspaceID {
			continue
		}
		managers = append(managers, id)
	}
	return managers
}

// GetConnectedClients returns the number of clients in workspaceID.
func (h *PluginHub) GetConnectedClients(workspaceID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	count := 0
	for _, client := range h.clients {
		if client.Metadata.WorkspaceID == workspaceID {
			count++
		}
	}
	return count
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
	setWebSocketReadLimit(c.Conn)
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
		// Broadcast to clients in the plugin's workspace
		c.Hub.Broadcast(c.Metadata.WorkspaceID, msg)

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
		ID           string   `json:"id"`
		DisplayName  string   `json:"displayName"`
		Version      string   `json:"version"`
		Environment  string   `json:"environment"`
		Capabilities []string `json:"capabilities"`
		APIBaseURL   string   `json:"apiBaseURL"`
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
	// 实例 ID 只认连接握手时已认证的 ID：注册 payload 不得改写别的实例。
	if payload.ID != "" && payload.ID != c.ID {
		log.Printf("[PluginHub] rejecting instance.register: payload id %q != connection id %q", payload.ID, c.ID)
		return
	}
	payload.ID = c.ID
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
	setWebSocketReadLimit(c.Conn)
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
		// Broadcast to clients in the manager's workspace
		c.Hub.Broadcast(c.Metadata.WorkspaceID, msg)

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
	setWebSocketReadLimit(c.Conn)
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
