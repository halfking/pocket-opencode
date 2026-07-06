package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// pluginUpgrader 是插件 WebSocket 的默认 upgrader（开发兼容）。
// 生产环境应使用 Server.upgrader（带 origin 检查）。
var pluginUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 开发默认允许所有源
	},
}

// handlePluginWebSocket handles WebSocket connections for plugins and managers
func (s *Server) handlePluginWebSocket(w http.ResponseWriter, r *http.Request) {
	// Get connection type from query parameter
	connType := r.URL.Query().Get("type")
	id := r.URL.Query().Get("id")

	if id == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket — 使用 server 的 upgrader（带 origin 检查）
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	switch connType {
	case "plugin":
		s.handlePluginConnection(conn, id)
	case "manager":
		s.handleManagerConnection(conn, id)
	case "client":
		s.handleClientConnection(conn, id)
	default:
		conn.Close()
		log.Printf("Unknown connection type: %s", connType)
	}
}

func (s *Server) handlePluginConnection(conn *websocket.Conn, id string) {
	log.Printf("Plugin connection: %s", id)

	pluginConn := &ws.PluginConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.PluginMetadata{
			InstanceID:  id,
			DisplayName: id,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterPlugin(pluginConn)

	// Start read and write pumps
	go pluginConn.WritePump()
	go pluginConn.ReadPump()
}

func (s *Server) handleManagerConnection(conn *websocket.Conn, id string) {
	log.Printf("Manager connection: %s", id)

	managerConn := &ws.ManagerConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.ManagerMetadata{
			InstanceID:  id,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterManager(managerConn)

	// Start read and write pumps
	go managerConn.WritePump()
	go managerConn.ReadPump()
}

func (s *Server) handleClientConnection(conn *websocket.Conn, id string) {
	log.Printf("Client connection: %s", id)

	clientConn := &ws.ClientConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.ClientMetadata{
			UserID:      id,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterClient(clientConn)

	// Start read and write pumps
	go clientConn.WritePump()
	go clientConn.ReadPump()
}

// handlePluginStatus returns current plugin hub status
func (s *Server) handlePluginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := map[string]interface{}{
		"instances": s.pluginHub.GetConnectedInstances(),
		"managers":  s.pluginHub.GetConnectedManagers(),
		"clients":   s.pluginHub.GetConnectedClients(),
		"timestamp": time.Now().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, status)
}

// handleSendCommand sends a command to an instance
func (s *Server) handleSendCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string          `json:"instanceID"`
		Command    string          `json:"command"`
		Data       json.RawMessage `json:"data,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	message := ws.Message{
		Type:    req.Command,
		Payload: req.Data,
	}

	if err := s.pluginHub.SendCommandToInstance(req.InstanceID, message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "command sent",
	})
}
