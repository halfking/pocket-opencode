package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

type Server struct {
	cfg           config.Config
	nps           adapter.NPSAdapter
	opencode      adapter.OpenCodeAdapter
	taskStore     *task.Store
	registry      *registry.Registry
	configAdapter adapter.OpenCodeConfigAdapter
	wsHub         *ws.Hub
	upgrader      websocket.Upgrader
}

func New(cfg config.Config, nps adapter.NPSAdapter, opencode adapter.OpenCodeAdapter, taskStore *task.Store, reg *registry.Registry, configAdapter adapter.OpenCodeConfigAdapter) *Server {
	hub := ws.NewHub()
	go hub.Run()
	
	return &Server{
		cfg:           cfg,
		nps:           nps,
		opencode:      opencode,
		taskStore:     taskStore,
		registry:      reg,
		configAdapter: configAdapter,
		wsHub:         hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // 允许所有源，生产环境应该更严格
			},
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/sessions/", s.handleSessions)
	mux.HandleFunc("/api/sessions", s.handleAllSessions) // 新增：获取所有会话
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTaskOperations)
	mux.HandleFunc("/api/config/models", s.handleModelConfig)
	mux.HandleFunc("/api/config/reload", s.handleConfigReload)
	mux.HandleFunc("/api/config/models/test", s.handleModelTest)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/app/check-update", s.handleCheckUpdate)
	mux.HandleFunc("/api/app/download", s.handleDownloadAPK)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	var instances []model.PocketInstance

	// 优先使用 Registry 中的实例
	if s.registry != nil {
		instances = s.registry.ListInstances()
	}

	// 如果 Registry 为空，从 NPS 获取
	if len(instances) == 0 {
		instances = s.collectInstances(r)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"instances": instances,
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	// 支持两种方式：
	// 1. instance_id (新方式，推荐)
	// 2. instance (兼容旧方式)
	instanceID := r.URL.Query().Get("instance_id")
	instanceBaseURL := r.URL.Query().Get("instance")

	if instanceID != "" {
		// 新方式：通过 Registry 查找
		if s.registry == nil {
			http.Error(w, "registry not configured", http.StatusServiceUnavailable)
			return
		}

		apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		instanceBaseURL = apiBaseURL
	}

	if instanceBaseURL == "" {
		http.Error(w, "missing instance_id or instance query param", http.StatusBadRequest)
		return
	}

	if s.opencode == nil {
		http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
		return
	}

	sessions, err := s.opencode.ListSessions(r.Context(), instanceBaseURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessions": sessions,
	})
}

// handleAllSessions 获取所有会话列表（支持过滤和分页）
func (s *Server) handleAllSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencode == nil {
		http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
		return
	}

	// 获取查询参数
	instanceID := r.URL.Query().Get("instance_id")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 20
	offset := 0
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// 如果指定了 instance_id，只获取该实例的会话
	if instanceID != "" {
		var instanceBaseURL string
		if s.registry != nil {
			apiBase, err := s.registry.GetInstanceAPIBase(instanceID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			instanceBaseURL = apiBase
		} else {
			http.Error(w, "registry not configured", http.StatusServiceUnavailable)
			return
		}

		sessions, err := s.opencode.ListSessions(r.Context(), instanceBaseURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 应用分页
		start := offset
		end := offset + limit
		if start > len(sessions) {
			start = len(sessions)
		}
		if end > len(sessions) {
			end = len(sessions)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": sessions[start:end],
			"total":    len(sessions),
			"limit":    limit,
			"offset":   offset,
		})
		return
	}

	// 获取所有实例的会话（如果没有指定 instance_id）
	var allSessions []adapter.OpenCodeSession
	if s.registry != nil {
		instances := s.registry.ListInstances()
		for _, inst := range instances {
			// 通过 registry 获取实例的 API base URL
			apiBase, err := s.registry.GetInstanceAPIBase(inst.ID)
			if err != nil {
				log.Printf("Failed to get API base for instance %s: %v", inst.ID, err)
				continue
			}
			
			sessions, err := s.opencode.ListSessions(r.Context(), apiBase)
			if err != nil {
				log.Printf("Failed to list sessions for instance %s: %v", inst.ID, err)
				continue
			}
			allSessions = append(allSessions, sessions...)
		}
	}

	// 应用分页
	start := offset
	end := offset + limit
	if start > len(allSessions) {
		start = len(allSessions)
	}
	if end > len(allSessions) {
		end = len(allSessions)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessions": allSessions[start:end],
		"total":    len(allSessions),
		"limit":    limit,
		"offset":   offset,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		http.Error(w, "task store not configured", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// 任务来自指定 OpenCode 实例的 /session API（一个 Session = 一个开发任务）。
		instanceID := r.URL.Query().Get("instance_id")
		if instanceID == "" {
			http.Error(w, "missing instance_id query param (required for OpenCode task lookup)", http.StatusBadRequest)
			return
		}
		if s.registry == nil {
			http.Error(w, "registry not configured", http.StatusServiceUnavailable)
			return
		}
		instanceBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		if s.opencode == nil {
			http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
			return
		}

		remoteTasks, err := s.opencode.ListRemoteTasks(r.Context(), instanceBaseURL, "", 100)
		if err != nil {
			log.Printf("Failed to fetch OpenCode sessions for instance %s: %v", instanceID, err)
			http.Error(w, fmt.Sprintf("failed to fetch tasks from instance: %v", err), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": remoteTasks})

	case http.MethodPost:
		var req task.Task
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ID == "" || req.Title == "" {
			http.Error(w, "missing required fields", http.StatusBadRequest)
			return
		}
		if err := s.taskStore.CreateTask(r.Context(), &req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		// 广播任务创建事件
		s.broadcastTaskEvent("task_created", &req)
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(req)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTaskOperations(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		http.Error(w, "task store not configured", http.StatusServiceUnavailable)
		return
	}

	// Parse task ID from path: /api/tasks/{id}/...
	path := r.URL.Path[len("/api/tasks/"):]
	if path == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}

	// Check for /attach-session
	if r.Method == http.MethodPost && len(path) > 0 {
		parts := splitPath(path)
		if len(parts) == 2 && parts[1] == "attach-session" {
			s.handleAttachSession(w, r, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "sessions" {
			s.handleTaskSessions(w, r, parts[0])
			return
		}
	}

	// GET /api/tasks/{id}
	if r.Method == http.MethodGet {
		taskID := path
		task, err := s.taskStore.GetTask(r.Context(), taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (s *Server) handleAttachSession(w http.ResponseWriter, r *http.Request, taskID string) {
	var req task.SessionLink
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.TaskID = taskID
	if req.InstanceID == "" || req.SessionID == "" {
		http.Error(w, "missing instanceId or sessionId", http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "primary"
	}

	if err := s.taskStore.AttachSession(r.Context(), req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 广播会话附加事件
	s.broadcastSessionEvent("session_attached", &req)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (s *Server) handleTaskSessions(w http.ResponseWriter, r *http.Request, taskID string) {
	links, err := s.taskStore.ListSessionsForTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sessions": links})
}

func (s *Server) handleModelConfig(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	if s.registry == nil {
		http.Error(w, "registry not configured", http.StatusServiceUnavailable)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if s.configAdapter == nil {
			http.Error(w, "config adapter not configured", http.StatusServiceUnavailable)
			return
		}
		config, err := s.configAdapter.GetModelConfig(r.Context(), apiBaseURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"config": config})

	case http.MethodPut:
		if s.configAdapter == nil {
			http.Error(w, "config adapter not configured", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Config adapter.ModelConfig `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := s.configAdapter.UpdateModelConfig(r.Context(), apiBaseURL, &req.Config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	if s.registry == nil || s.configAdapter == nil {
		http.Error(w, "service not configured", http.StatusServiceUnavailable)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.configAdapter.ReloadConfig(r.Context(), apiBaseURL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"reloadedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	var req struct {
		ProviderID string `json:"providerId"`
		ModelID    string `json:"modelId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if s.registry == nil || s.configAdapter == nil {
		http.Error(w, "service not configured", http.StatusServiceUnavailable)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.configAdapter.TestModel(r.Context(), apiBaseURL, req.ProviderID, req.ModelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		clientID = r.RemoteAddr
	}

	client := ws.NewClient(s.wsHub, conn, clientID)
	s.wsHub.Register(client)

	// 启动读写协程
	go client.WritePump()
	go client.ReadPump()
}

// broadcastTaskEvent 广播任务事件
func (s *Server) broadcastTaskEvent(eventType string, task *task.Task) {
	if s.wsHub != nil {
		s.wsHub.Broadcast(eventType, task)
	}
}

// broadcastSessionEvent 广播会话事件
func (s *Server) broadcastSessionEvent(eventType string, link *task.SessionLink) {
	if s.wsHub != nil {
		s.wsHub.Broadcast(eventType, link)
	}
}

func (s *Server) collectInstances(r *http.Request) []model.PocketInstance {
	if s.nps == nil {
		return defaultInstances()
	}

	clients, err := s.nps.ListClients(r.Context())
	if err != nil || len(clients) == 0 {
		return defaultInstances()
	}

	instances := make([]model.PocketInstance, 0, len(clients))
	for _, client := range clients {
		instances = append(instances, model.PocketInstance{
			ID:              client.Name,
			DisplayName:     client.Name,
			Environment:     "unknown",
			NPSClientID:     client.ID,
			Capabilities:    []string{"session", "summary", "pty"},
			Health:          "healthy",
			LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return instances
}

func defaultInstances() []model.PocketInstance {
	return []model.PocketInstance{
		{
			ID:              "demo-main",
			DisplayName:     "Demo Main",
			Environment:     "local",
			NPSClientID:     1,
			Capabilities:    []string{"session", "summary", "pty"},
			Health:          "healthy",
			LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func splitPath(path string) []string {
	result := []string{}
	current := ""
	for _, ch := range path {
		if ch == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// VersionInfo 版本信息结构
type VersionInfo struct {
	Version      string   `json:"version"`
	BuildNumber  int      `json:"buildNumber"`
	DownloadURL  string   `json:"downloadUrl"`
	FileSize     int64    `json:"fileSize"`
	Changelog    []string `json:"changelog"`
	ForceUpdate  bool     `json:"forceUpdate"`
	ReleaseDate  string   `json:"releaseDate"`
}

// loadVersionConfig 从配置文件加载版本信息
func (s *Server) loadVersionConfig() (*VersionInfo, error) {
	configPath := os.Getenv("POCKET_VERSION_CONFIG_PATH")
	if configPath == "" {
		// 默认路径：相对于可执行文件的 config/version.json
		configPath = "config/version.json"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		// 如果文件不存在，使用默认配置
		log.Printf("Warning: version config not found at %s, using defaults: %v", configPath, err)
		return &VersionInfo{
			Version:     "1.2.0",
			BuildNumber: 2,
			DownloadURL: "http://14.103.169.56:8088/api/app/download",
			FileSize:    4200000,
			Changelog: []string{
				"✨ 全新移动端 UI 设计",
				"✨ 添加登录系统",
				"🐛 修复若干已知问题",
			},
			ForceUpdate: false,
			ReleaseDate: time.Now().Format("2006-01-02"),
		}, nil
	}

	var versionInfo VersionInfo
	if err := json.Unmarshal(data, &versionInfo); err != nil {
		return nil, fmt.Errorf("failed to parse version config: %w", err)
	}

	log.Printf("Loaded version config: v%s build %d from %s", versionInfo.Version, versionInfo.BuildNumber, configPath)
	return &versionInfo, nil
}

// handleCheckUpdate 检查应用更新
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type CheckUpdateRequest struct {
		CurrentVersion  string `json:"currentVersion"`
		CurrentBuild    int    `json:"currentBuild"`
		Platform        string `json:"platform"`
		DeviceModel     string `json:"deviceModel"`
	}

	type CheckUpdateResponse struct {
		HasUpdate   bool         `json:"hasUpdate"`
		Latest      *VersionInfo `json:"latest,omitempty"`
		ForceUpdate bool         `json:"forceUpdate"`
		Message     string       `json:"message"`
	}

	var req CheckUpdateRequest
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			req.CurrentVersion = "1.0.0"
			req.CurrentBuild = 1
		}
	} else {
		req.CurrentVersion = r.URL.Query().Get("version")
		if req.CurrentVersion == "" {
			req.CurrentVersion = "1.0.0"
		}
	}

	// 从配置文件加载最新版本信息
	latestVersion, err := s.loadVersionConfig()
	if err != nil {
		log.Printf("Error loading version config: %v", err)
		http.Error(w, "failed to load version info", http.StatusInternalServerError)
		return
	}

	// 简单的版本比较
	hasUpdate := req.CurrentVersion < latestVersion.Version || req.CurrentBuild < latestVersion.BuildNumber

	resp := CheckUpdateResponse{
		HasUpdate:   hasUpdate,
		ForceUpdate: latestVersion.ForceUpdate,
		Message:     "当前已是最新版本",
	}

	if hasUpdate {
		resp.Latest = latestVersion
		resp.Message = "发现新版本"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleDownloadAPK 下载 APK
func (s *Server) handleDownloadAPK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// APK 文件路径（实际部署时应该指向真实的 APK 文件）
	apkPath := "/data/www/pocket.kxpms.cn/downloads/opencode-pocket-latest.apk"

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", "attachment; filename=opencode-pocket.apk")
	
	http.ServeFile(w, r, apkPath)
}
