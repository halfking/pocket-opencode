package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/feishu"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/migration"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/vault"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// generateUUID generates a simple UUID-like string (Phase 7)
func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type Server struct {
	cfg           config.Config
	nps           adapter.NPSAdapter
	opencode      adapter.OpenCodeAdapter
	taskStore     *task.Store
	registry      *registry.Registry
	configAdapter adapter.OpenCodeConfigAdapter
	wsHub         *ws.Hub
	pluginHub     *ws.PluginHub // Plugin/Manager WebSocket Hub
	upgrader      websocket.Upgrader
	// Phase 0: 个人助理模块 store 与依赖
	notesStore  *notes.Store
	emailStore  *email.Store
	vaultStore  *vault.Store
	transcriber *stt.Transcriber // nil = 云端 STT 兜底未配置
	mcpClient   *mcp.Client      // nil = ACC 任务整合未配置（Phase 5 才激活）
	// Phase C: 无状态 AI 网关（嵌入/LLM 代理）。nil = 未配置，对应 handler 返回 503。
	embedder    aigate.Embedder
	llm         aigate.LLMClient
	// 后端集成：kxmemory AI 编排（分类/SSOT/总结）
	kxmemory    *kxmemory.Client // nil = kxmemory 未配置
	// OpenCode 管理器
	opencodeManager *opencode.Manager // nil = OpenCode 管理未启用
	// OpenCode 域事件/许可/提问管理（Phase V3：真实任务与会话接入）
	eventMgr *opencode.EventStreamManager
	permMgr  *opencode.PermissionManager
	quesMgr  *opencode.QuestionManager

	// Auth
	userStore *auth.UserStore
	jwtSigner *auth.Signer

	// Email
	emailCrypto    *email.Crypto
	emailPending   *email.PendingOAuth
	emailScheduler *email.Scheduler
	emailFetcher   *email.Fetcher

	dataDir string // 数据目录

	llmGWStore *LLMGatewayStore // nil = 无 PG，配置不持久化

	// 会话迁移方案：跨主机迁移编排服务（nil = registry/adapter/pluginHub 未就绪）
	migrationSvc *migration.Service
}

// New 构造 Server。Phase 0 扩展：新增 notes/email/vault store、STT transcriber、ACC MCP client。
// Phase C 扩展：新增 embedder/llm 无状态 AI 网关。
// 后端集成：新增 kxmemory 客户端（AI 编排服务）。
// OpenCode 扩展：新增 opencodeManager（实例和会话管理）。
// Auth + Email: 新增 userStore/jwtSigner/emailCrypto/emailPending/emailScheduler/emailFetcher/dataDir。
// 这些依赖都允许为 nil（对应功能降级），由各 handler 自行判断。
func New(cfg config.Config, nps adapter.NPSAdapter, opencode adapter.OpenCodeAdapter, taskStore *task.Store, reg *registry.Registry, configAdapter adapter.OpenCodeConfigAdapter, notesStore *notes.Store, emailStore *email.Store, vaultStore *vault.Store, transcriber *stt.Transcriber, mcpClient *mcp.Client, embedder aigate.Embedder, llm aigate.LLMClient, kxmem *kxmemory.Client, opencodeManager *opencode.Manager, userStore *auth.UserStore, jwtSigner *auth.Signer, emailCrypto *email.Crypto, emailPending *email.PendingOAuth, emailScheduler *email.Scheduler, emailFetcher *email.Fetcher, dataDir string) *Server {
	hub := ws.NewHub()
	go hub.Run()

	// Initialize Plugin Hub
	pluginHub := ws.NewPluginHub()
	go pluginHub.Run()
	// 会话迁移方案：把 Registry 注入 PluginHub，
	// 使边端插件/manager 的 instance.register / heartbeat 能写入 Registry
	// （origin=registered），/api/instances 即可展示真实实例。
	if reg != nil {
		pluginHub.SetInstanceRegistrar(reg)
	}

	return &Server{
		cfg:             cfg,
		nps:             nps,
		opencode:        opencode,
		taskStore:       taskStore,
		registry:        reg,
		configAdapter:   configAdapter,
		wsHub:           hub,
		pluginHub:       pluginHub,
		notesStore:      notesStore,
		emailStore:      emailStore,
		vaultStore:      vaultStore,
		transcriber:     transcriber,
		mcpClient:       mcpClient,
		embedder:        embedder,
		llm:             llm,
		kxmemory:        kxmem,
		opencodeManager: opencodeManager,
		userStore:       userStore,
		jwtSigner:       jwtSigner,
		emailCrypto:     emailCrypto,
		emailPending:    emailPending,
		emailScheduler:  emailScheduler,
		emailFetcher:    emailFetcher,
		dataDir:         dataDir,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     buildOriginChecker(cfg.AllowedOrigins, cfg.DevAuth),
		},
	}
}

// SetOpenCodeManagers 由 main.go 在 server.New 之后注入 OpenCode 域管理器。
// 使用 setter 而非扩展 New 签名，避免参数膨胀。所有 manager 允许为 nil。
func (s *Server) SetOpenCodeManagers(ocMgr *opencode.Manager, eventMgr *opencode.EventStreamManager, permMgr *opencode.PermissionManager, quesMgr *opencode.QuestionManager) {
	s.opencodeManager = ocMgr
	s.eventMgr = eventMgr
	s.permMgr = permMgr
	s.quesMgr = quesMgr
}

// SetLLMGatewayStore 注入 LLM 网关配置持久化 store（PG pool 可用时）。
func (s *Server) SetLLMGatewayStore(store *LLMGatewayStore) {
	s.llmGWStore = store
}

// SetMigrationService 注入会话跨主机迁移编排服务（registry/adapter/pluginHub 就绪后由 main 装配）。
func (s *Server) SetMigrationService(svc *migration.Service) {
	s.migrationSvc = svc
}

// PluginHub 返回内部的 PluginHub，供 main 装配迁移服务等需要下发命令的组件复用。
func (s *Server) PluginHub() *ws.PluginHub { return s.pluginHub }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/sessions/", s.handleSessions)
	mux.HandleFunc("/api/sessions", s.handleAllSessions) // 新增：获取所有会话
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTaskOperations)
	mux.HandleFunc("/api/config/models", s.requireAuth(s.handleModelConfig))
	mux.HandleFunc("/api/config/reload", s.requireAuth(s.handleConfigReload))
	mux.HandleFunc("/api/config/models/test", s.handleModelTest)
	mux.HandleFunc("/ws", s.requireAuth(s.handleWebSocket))
	mux.HandleFunc("/api/app/check-update", s.handleCheckUpdate)
	mux.HandleFunc("/api/app/download", s.handleDownloadAPK)
	// 飞书事件回调 (m.kxpms.cn/callback/feishu 由 56 nginx 转发到 9010)
	mux.HandleFunc("/callback/feishu", s.handleFeishuCallback)

	// ---- Phase 0: 个人助理模块路由 ----
	// 认证
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	// 语音笔记
	mux.HandleFunc("/api/notes", s.requireAuth(s.handleNotes))
	mux.HandleFunc("/api/notes/", s.requireAuth(s.handleNoteOperations))
	// 邮箱助手
	mux.HandleFunc("/api/email/accounts", s.requireAuth(s.handleEmailAccounts))
	mux.HandleFunc("/api/email/accounts/", s.requireAuth(s.handleEmailAccountOps))
	mux.HandleFunc("/api/email/summaries", s.requireAuth(s.handleEmailSummaries))
	mux.HandleFunc("/api/email/summaries/", s.requireAuth(s.handleEmailSummaryOps))
	mux.HandleFunc("/api/emails", s.requireAuth(s.handleEmails))
	mux.HandleFunc("/api/emails/sync", s.requireAuth(s.handleEmailSync))
	mux.HandleFunc("/api/emails/", s.requireAuth(s.handleEmailOps))
	// 密码箱（子树，含 /api/vault/sync/latest）
	mux.HandleFunc("/api/vault/sync/", s.requireAuth(s.handleVaultSync))
	// STT 云端兜底
	mux.HandleFunc("/api/stt/transcribe", s.handleSttTranscribe)
	// Phase C: 无状态 AI 网关（仅转发嵌入/LLM，不存数据）
	mux.HandleFunc("/api/embed", s.requireAuth(s.handleEmbed))
	mux.HandleFunc("/api/llm/chat", s.requireAuth(s.handleLLMChat))
	
	// OpenCode 管理 API
	mux.HandleFunc("/api/opencode/sessions", s.handleOpenCodeSessions)
	mux.HandleFunc("/api/opencode/sessions/", s.handleOpenCodeSessionOperations)
	mux.HandleFunc("/api/opencode/instances/", s.handleOpenCodeInstanceOperations)
	mux.HandleFunc("/api/opencode/cache/refresh", s.requireAuth(s.handleOpenCodeRefreshCache))
	mux.HandleFunc("/api/opencode/dispatch", s.requireAuth(s.handleOpenCodeDispatch))

	// 会话迁移方案：跨主机迁移 API
	mux.HandleFunc("/api/migration", s.requireAuth(s.handleMigration))
	mux.HandleFunc("/api/migration/preview", s.requireAuth(s.handleMigrationPreview))


	// ---- Phase V3: LLM Gateway 配置管理 ----
	// 用户在 Settings 改 llmgo.kxpms.cn URL / API Key；pocketd 写入 OpenCode 配置
	mux.HandleFunc("/api/llm-gateway/config", s.requireAuth(s.handleLLMGatewayConfig))
	mux.HandleFunc("/api/llm-gateway/test", s.requireAuth(s.handleLLMGatewayTest))
	mux.HandleFunc("/api/llm-gateway/models", s.requireAuth(s.handleLLMGatewayModels))

	// ---- Phase V3: 移动端真实会话交互 API ----
	// SSE / Prompt / Interrupt / Messages / Create — 转发到 OpenCode 上游
	mux.HandleFunc("/api/mobile/sessions", s.requireAuth(s.handleMobileSessionRouter))
	mux.HandleFunc("/api/mobile/sessions/", s.requireAuth(s.handleMobileSessionRouter))

	// Plugin/Manager WebSocket routes
	mux.HandleFunc("/plugin/ws", s.handlePluginWebSocket)
	mux.HandleFunc("/api/plugin/status", s.handlePluginStatus)
	mux.HandleFunc("/api/plugin/command", s.requireAuth(s.handleSendCommand))

	return corsMiddleware(mux)
}

// corsMiddleware 添加 CORS 支持
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 允许所有源（生产环境应该更严格）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "3600")
		
		// 处理 OPTIONS 预检请求
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// handleOpenCodeSessionOperations 处理 OpenCode 会话相关操作的路由分发
func (s *Server) handleOpenCodeSessionOperations(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/opencode/sessions/"):]
	
	// 检查是否是 /history 或 /summary 结尾
	if len(path) > 8 && path[len(path)-8:] == "/history" {
		s.handleOpenCodeSessionHistory(w, r)
		return
	}
	if len(path) > 8 && path[len(path)-8:] == "/summary" {
		s.handleOpenCodeSessionSummary(w, r)
		return
	}
	
	http.Error(w, "not found", http.StatusNotFound)
}

// handleOpenCodeInstanceOperations 处理 OpenCode 实例相关操作的路由分发
func (s *Server) handleOpenCodeInstanceOperations(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/opencode/instances/"):]
	
	// 检查是否是 /stats 结尾
	if len(path) > 6 && path[len(path)-6:] == "/stats" {
		s.handleOpenCodeInstanceStats(w, r)
		return
	}
	
	http.Error(w, "not found", http.StatusNotFound)
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
	// 降级：taskStore 为 nil 时（remote-only 模式）只支持 GET 列出远程任务
	// POST 仍要求 PG；GET 在无 PG 时跳过 local 源，其他源照常

	switch r.Method {
	case http.MethodGet:
		// 🦞 三源任务聚合：按 ?source=local|opencode|acc 过滤，或省略返回所有
		//   source=acc     → 调 ACC MCP（acc_get_tasks），Source=acc
		//   source=opencode→ 按 instance_id 调 OpenCode HTTP adapter，Source=opencode
		//   source=local   → 查本地 PG store，Source=local
		//   省略           → 三源合并 + 按 workstreamId/source 过滤
		// 游标分页：?cursor=xxx&limit=20（仅 source=local 时生效）
		source := r.URL.Query().Get("source")
		instanceID := r.URL.Query().Get("instance_id")
		workstreamID := r.URL.Query().Get("workstream_id")
		cursorStr := r.URL.Query().Get("cursor")
		limit := ParseLimit(r.URL.Query().Get("limit"), 100, 500)

		// 纯本地源 + 游标分页：走 keyset pagination
		if source == "local" && s.taskStore != nil && cursorStr != "" {
			cur := DecodeCursor(cursorStr)
			var createdAt int64
			var id string
			if cur != nil {
				createdAt = cur.CreatedAt
				id = cur.ID
			}
			tasks, hasMore, err := s.taskStore.ListTasksCursor(r.Context(), limit, createdAt, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// 过滤 workstreamID
			if workstreamID != "" {
				filtered := make([]task.Task, 0, len(tasks))
				for _, t := range tasks {
					if t.WorkstreamID == workstreamID {
						filtered = append(filtered, t)
					}
				}
				tasks = filtered
			}
			var resp PaginatedResponse
			if len(tasks) > 0 {
				last := tasks[len(tasks)-1]
				resp = FormatCursorPage(tasks, last.ID, last.CreatedAt.Unix(), hasMore)
			} else {
				resp = FormatCursorPage(tasks, "", 0, false)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		var allTasks []task.Task

		// 1. ACC 任务（经 MCP 客户端）
		if (source == "" || source == "acc") && s.mcpClient != nil {
			statusFilter := r.URL.Query().Get("status")
			parsed, err := s.mcpClient.GetRemoteTasks(r.Context(), statusFilter, limit)
			if err != nil {
				log.Printf("[mcp] fetch ACC tasks failed: %v", err)
				// 不阻断其他源
			} else {
				now := time.Now().Unix()
				for _, p := range parsed {
					allTasks = append(allTasks, task.Task{
						ID:               p.ID,
						Title:            p.Title,
						Status:           p.Status,
						Priority:         "normal",
						WorkstreamID:     workstreamID,
						Source:           "acc",
						CreatedAt:        time.Unix(now, 0),
						UpdatedAt:        time.Unix(now, 0),
					})
				}
			}
		}

		// 2. OpenCode 实例会话（HTTP adapter）
		if (source == "" || source == "opencode") && instanceID != "" && s.opencode != nil && s.registry != nil {
			apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
			if err == nil {
				remoteTasks, err := s.opencode.ListRemoteTasks(r.Context(), apiBaseURL, "", limit)
				if err != nil {
					log.Printf("Failed to fetch OpenCode sessions for instance %s: %v", instanceID, err)
				} else {
					now := time.Now().Unix()
					for _, rt := range remoteTasks {
						allTasks = append(allTasks, task.Task{
							ID:               rt.ID,
							Title:            rt.Title,
							Status:           rt.Status,
							Priority:         "normal",
							WorkstreamID:     instanceID, // OpenCode 实例 ID 即 workstream
							Source:           "opencode",
							CreatedAt:        time.Unix(now, 0),
							UpdatedAt:        time.Unix(now, 0),
						})
					}
				}
			}
		}

		// 3. 本地任务（PG store，nil-safe 降级）
		if (source == "" || source == "local") && s.taskStore != nil {
			localTasks, err := s.taskStore.ListTasks(r.Context())
			if err == nil {
				for _, t := range localTasks {
					if workstreamID != "" && t.WorkstreamID != workstreamID {
						continue
					}
					allTasks = append(allTasks, t)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": allTasks})

	case http.MethodPost:
		if s.taskStore == nil {
			http.Error(w, "local task store not configured (remote-only mode)", http.StatusServiceUnavailable)
			return
		}
		var req task.Task
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		// Phase 7: Auto-generate ID if not provided
		if req.ID == "" {
			req.ID = "task-" + generateUUID()
		}
		if req.Title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}
		if req.Source == "" {
			req.Source = "local" // POST 创建的任务默认为本地源
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

	// PATCH /api/tasks/{id} — 更新任务状态/优先级/标题
	if r.Method == http.MethodPatch {
		var update task.TaskUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		updated, err := s.taskStore.UpdateTask(r.Context(), path, update)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.broadcastTaskEvent("task_updated", updated)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updated)
		return
	}

	// DELETE /api/tasks/{id} — 删除任务及其会话关联
	if r.Method == http.MethodDelete {
		if err := s.taskStore.DeleteTask(r.Context(), path); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.broadcastTaskEvent("task_deleted", &task.Task{ID: path})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
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

// buildOriginChecker creates a WebSocket origin check function.
// If allowedOrigins is set, only those origins are allowed.
// In dev mode (devAuth=true), localhost:* is always allowed.
// Production must set POCKET_ALLOWED_ORIGINS explicitly.
func buildOriginChecker(allowedOrigins string, devAuth bool) func(r *http.Request) bool {
	// Parse allowed origins into a set
	originSet := make(map[string]bool)
	if allowedOrigins != "" {
		for _, o := range strings.Split(allowedOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				originSet[o] = true
			}
		}
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")

		// Dev mode: allow localhost and 127.0.0.1
		if devAuth {
			if origin == "" {
				return true
			}
			if strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
				return true
			}
		}

		// If no origin header (non-browser client), allow
		if origin == "" {
			return true
		}

		// If no allowed origins configured and not dev, allow all (backward compat)
		if len(originSet) == 0 && !devAuth {
			return true
		}

		// Check against allowed set
		return originSet[origin]
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

// handleFeishuCallback 处理飞书事件回调（m.kxpms.cn/callback/feishu）。
// 由 feishu.PublicEntry 包装，传入 wsHub.Broadcast 闭包以推送 WebSocket。
func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	feishu.PublicEntry(s.cfg, func(msgType string, payload interface{}) {
		s.wsHub.Broadcast(msgType, payload)
	})(w, r)
}
