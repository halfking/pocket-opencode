package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// handlePluginWebSocket handles WebSocket connections for plugins and managers.
//
// 身份边界：连接的 workspace/user 只来自已认证 JWT claims，query 参数只提供
// 实例路由用的 id。这样一个 workspace 的调用方无法接管或冒认别的租户实例。
func (s *Server) handlePluginWebSocket(w http.ResponseWriter, r *http.Request) {
	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	workspaceID := claims.WorkspaceID
	if workspaceID == "" {
		workspaceID = "default"
	}

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
		s.handlePluginConnection(conn, id, workspaceID, claims.UserID)
	case "manager":
		s.handleManagerConnection(conn, id, workspaceID, claims.UserID)
	case "client":
		s.handleClientConnection(conn, id, workspaceID, claims.UserID)
	default:
		conn.Close()
		log.Printf("Unknown connection type: %s", connType)
	}
}

func (s *Server) handlePluginConnection(conn *websocket.Conn, id, workspaceID, userID string) {
	log.Printf("Plugin connection: %s (workspace=%s)", id, workspaceID)

	pluginConn := &ws.PluginConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.PluginMetadata{
			InstanceID:  id,
			WorkspaceID: workspaceID,
			UserID:      userID,
			DisplayName: id,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterPlugin(pluginConn)

	// Start read and write pumps
	go pluginConn.WritePump()
	go pluginConn.ReadPump()
}

func (s *Server) handleManagerConnection(conn *websocket.Conn, id, workspaceID, userID string) {
	log.Printf("Manager connection: %s (workspace=%s)", id, workspaceID)

	managerConn := &ws.ManagerConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.ManagerMetadata{
			InstanceID:  id,
			WorkspaceID: workspaceID,
			UserID:      userID,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterManager(managerConn)

	// Start read and write pumps
	go managerConn.WritePump()
	go managerConn.ReadPump()
}

func (s *Server) handleClientConnection(conn *websocket.Conn, id, workspaceID, userID string) {
	log.Printf("Client connection: %s (workspace=%s)", id, workspaceID)

	clientConn := &ws.ClientConnection{
		ID:   id,
		Conn: conn,
		Send: make(chan []byte, 256),
		Hub:  s.pluginHub,
		Metadata: ws.ClientMetadata{
			UserID:      userID,
			WorkspaceID: workspaceID,
			ConnectedAt: time.Now(),
		},
	}

	s.pluginHub.RegisterClient(clientConn)

	// Start read and write pumps
	go clientConn.WritePump()
	go clientConn.ReadPump()
}

// handlePluginStatus returns the caller workspace's plugin hub status
func (s *Server) handlePluginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	status := map[string]interface{}{
		"instances": s.pluginHub.GetConnectedInstances(workspaceID),
		"managers":  s.pluginHub.GetConnectedManagers(workspaceID),
		"clients":   s.pluginHub.GetConnectedClients(workspaceID),
		"timestamp": time.Now().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, status)
}

// handleSendCommand sends a command to an instance owned by the caller workspace
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
	if req.InstanceID == "" || req.Command == "" {
		http.Error(w, "instanceID and command are required", http.StatusBadRequest)
		return
	}

	message := ws.Message{
		Type:    req.Command,
		Payload: req.Data,
	}

	err := s.pluginHub.SendCommandToInstance(s.workspaceIDFromRequest(r), req.InstanceID, message)
	switch {
	case errors.Is(err, ws.ErrInstanceNotConnected):
		http.Error(w, "instance not connected", http.StatusNotFound)
		return
	case errors.Is(err, ws.ErrCommandQueueFull):
		http.Error(w, "instance command queue is full", http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "command sent",
	})
}
