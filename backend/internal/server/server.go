package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/agent"
	"github.com/halfking/pocket-opencode/backend/internal/agentbridge"
	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	cs "github.com/halfking/pocket-opencode/backend/internal/chat_summary"
	"github.com/halfking/pocket-opencode/backend/internal/chatagent"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/feishu"
	"github.com/halfking/pocket-opencode/backend/internal/finance"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/lobster"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/migration"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/notify"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/quota"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/halfking/pocket-opencode/backend/internal/snippet"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
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
	cfg                    config.Config
	nps                    adapter.NPSAdapter
	opencode               adapter.OpenCodeAdapter
	taskStore              *task.Store
	scheduledTaskStore     *scheduledtask.Store
	scheduledTaskScheduler *scheduledtask.Scheduler
	registry               *registry.Registry
	configAdapter          adapter.OpenCodeConfigAdapter
	wsHub                  *ws.Hub
	pluginHub              *ws.PluginHub // Plugin/Manager WebSocket Hub
	upgrader               websocket.Upgrader
	// Phase 0: 个人助理模块 store 与依赖
	notesStore       *notes.Store
	emailStore       *email.Store
	vaultStore       vaultSyncStorer
	snippetStore     *snippet.Store
	meetingStore     *meeting.Store
	chatSummaryStore *cs.Store
	transcriber      *stt.Transcriber // nil = 云端 STT 兜底未配置
	mcpClient        *mcp.Client      // nil = ACC 任务整合未配置（Phase 5 才激活）
	// Phase C: 无状态 AI 网关（嵌入/LLM 代理）。nil = 未配置，对应 handler 返回 503。
	embedder aigate.Embedder
	llm      aigate.LLMClient
	// 后端集成：kxmemory AI 编排（分类/SSOT/总结）
	kxmemory kxmemory.Client // 接口类型（默认 *kxmemory.HTTPClient 实现）
	// OpenCode 管理器
	opencodeManager *opencode.Manager // nil = OpenCode 管理未启用
	// OpenCode 域事件/许可/提问管理（Phase V3：真实任务与会话接入）
	eventMgr *opencode.EventStreamManager
	permMgr  *opencode.PermissionManager
	quesMgr  *opencode.QuestionManager

	// Auth
	userStore        *auth.UserStore
	jwtSigner        *auth.Signer
	biometricStore   *auth.BiometricStore
	webAuthnVerifier auth.WebAuthnVerifierIface // nil = WebAuthn 签名验证未启用（降级到 P0 stub）
	parseAssertionFn assertionParser            // 测试可替换的 WebAuthn assertion 解析器；生产为 nil（走默认）
	identityStore    *identity.Store            // nil = S0-A 未启用，handler 降级到单租户
	// C3/C4: 邮箱注册 + 验证码登录 + 忘记密码。nil = 未启用（handler 返回 503）
	codeStore   *auth.CodeStore
	smtpClient  *notify.Client
	// C5: RedClaw auth-agent 镜像客户端（可选；nil = 镜像路径 disabled）
	redclawAuthClient *redclaw.AuthClient
	// S0-B: unified LLM BFF。nil = 未配置（POCKET_LLM_* 未设且无网关配置），handler 返回 503。
	llmBFF           *llmbff.Service
	llmBFFSummarizer llmbff.Summarizer
	// P3 成本配额骨架：Enforcer 在 BFF 调用前做 Check；当前默认
	// AlwaysAllowStrategy，仅用于审计 / 可观测。EnforceMode=false 时
	// 永远 Allow；未来替换为真实预算策略即可上线硬拒绝。
	quotaEnforcer *quota.Enforcer
	// S0-C: Lobster Vault 加密镜像同步 store。nil = 同步路由返回 503。
	lobsterSync *lobster.SyncStore
	// S0-D: Agent Bridge。nil = /api/agents 返回 503。
	agentBridge *agentbridge.Bridge
	agentStore  *agentbridge.Store
	// S0-E: Notification Center。nil = /api/notifications 返回 503。
	notifySvc *notifycenter.Service
	// AI 对话智能体角色管理（PG Store 或 SQLiteStore 都实现 StoreIface）。
	// nil = /api/chat-agents 返回 503。
	chatAgentStore chatagent.StoreIface
	// 智能体云端同步（仅 PG 模式可用；SQLite 模式下此字段保持 nil）。
	chatAgentSync *chatagent.SyncStore
	notifyStore   *notifycenter.Store

	// ACP 通用 Agent Adapter Registry（W5 新增，与 s.opencode 并存）。
	// 老 handler 仍走 s.opencode；新 diagnostics / health-check 用 s.agents。
	agents *agent.Registry

	// Email
	emailCrypto    *email.Crypto
	emailPending   *email.PendingOAuth
	emailScheduler *email.Scheduler
	emailFetcher   *email.Fetcher
	financeStore   *finance.Store

	dataDir string // 数据目录

	llmGWStore LLMGatewayConfigStore // nil = 无持久化 backend，配置不写入店内/DB
	llmGWCache *llmGatewayCache      // nil = 仅依赖 env 默认配置

	// LLM Gateway 运维控制面：已注册网关节点 + admin API 客户端。
	// nil = 无 PG 或 master key 缺失，/api/llm-gateway/nodes 返回 503。
	gatewayNodes  *GatewayNodeStore
	gatewayClient *gatewayAdminClient

	// 会话迁移方案：跨主机迁移编排服务（nil = registry/adapter/pluginHub 未就绪）
	migrationSvc *migration.Service

	// RedClaw 企业后端桥接（nil = 未配置，对应 handler 返回 503）
	redclawBridge *redclaw.Bridge

	// Audit 审计日志存储（nil-safe；production 下若 PG 初始化失败会保持 nil，
	// 由 main.go 的 fail-closed 检查做兜底）。
	auditStore redclaw.AuditStoreFull

	// 移动端离线重放的 session create 幂等缓存（SEC-06）
	mobileCreates *mobileCreateCache
}

func isProductionConfig(cfg config.Config) bool {
	return cfg.IsProduction()
}

// New 构造 Server。Phase 0 扩展：新增 notes/email/vault store、STT transcriber、ACC MCP client。
// Phase C 扩展：新增 embedder/llm 无状态 AI 网关。
// 后端集成：新增 kxmemory 客户端（AI 编排服务）。
// OpenCode 扩展：新增 opencodeManager（实例和会话管理）。
// Auth + Email: 新增 userStore/jwtSigner/emailCrypto/emailPending/emailScheduler/emailFetcher/dataDir。
// 这些依赖都允许为 nil（对应功能降级），由各 handler 自行判断。
func New(cfg config.Config, nps adapter.NPSAdapter, opencode adapter.OpenCodeAdapter, taskStore *task.Store, reg *registry.Registry, configAdapter adapter.OpenCodeConfigAdapter, notesStore *notes.Store, emailStore *email.Store, vaultStore vaultSyncStorer, transcriber *stt.Transcriber, mcpClient *mcp.Client, embedder aigate.Embedder, llm aigate.LLMClient, kxmem kxmemory.Client, opencodeManager *opencode.Manager, userStore *auth.UserStore, jwtSigner *auth.Signer, emailCrypto *email.Crypto, emailPending *email.PendingOAuth, emailScheduler *email.Scheduler, emailFetcher *email.Fetcher, dataDir string, pool *pgxpool.Pool) *Server {
	return newServer(cfg, nps, opencode, taskStore, reg, configAdapter, notesStore, emailStore, vaultStore, transcriber, mcpClient, embedder, llm, kxmem, opencodeManager, userStore, jwtSigner, emailCrypto, emailPending, emailScheduler, emailFetcher, dataDir, true, pool)
}

// newServer builds a Server and optionally starts its long-lived websocket hubs.
// Handler tests that do not exercise websocket or plugin traffic may disable those
// workers to avoid leaking goroutines for the lifetime of the test process.
func newServer(cfg config.Config, nps adapter.NPSAdapter, opencode adapter.OpenCodeAdapter, taskStore *task.Store, reg *registry.Registry, configAdapter adapter.OpenCodeConfigAdapter, notesStore *notes.Store, emailStore *email.Store, vaultStore vaultSyncStorer, transcriber *stt.Transcriber, mcpClient *mcp.Client, embedder aigate.Embedder, llm aigate.LLMClient, kxmem kxmemory.Client, opencodeManager *opencode.Manager, userStore *auth.UserStore, jwtSigner *auth.Signer, emailCrypto *email.Crypto, emailPending *email.PendingOAuth, emailScheduler *email.Scheduler, emailFetcher *email.Fetcher, dataDir string, startHubs bool, pool *pgxpool.Pool) *Server {
	hub := ws.NewHub()
	if startHubs {
		go hub.Run()
	}

	// Initialize Plugin Hub
	pluginHub := ws.NewPluginHub()
	if startHubs {
		go pluginHub.Run()
	}
	// 会话迁移方案：把 Registry 注入 PluginHub，
	// 使边端插件/manager 的 instance.register / heartbeat 能写入 Registry
	// （origin=registered），/api/instances 即可展示真实实例。
	if reg != nil {
		pluginHub.SetInstanceRegistrar(reg)
	}

	s := &Server{
		cfg:              cfg,
		nps:              nps,
		opencode:         opencode,
		taskStore:        taskStore,
		registry:         reg,
		configAdapter:    configAdapter,
		wsHub:            hub,
		pluginHub:        pluginHub,
		notesStore:       notesStore,
		emailStore:       emailStore,
		vaultStore:       vaultStore,
		snippetStore:     snippet.NewStore(),
		meetingStore:     meeting.NewStore(),
		chatSummaryStore: cs.NewStore(),
		transcriber:      transcriber,
		mcpClient:        mcpClient,
		embedder:         embedder,
		llm:              llm,
		kxmemory:         kxmem,
		opencodeManager:  opencodeManager,
		userStore:        userStore,
		jwtSigner:        jwtSigner,
		emailCrypto:      emailCrypto,
		emailPending:     emailPending,
		emailScheduler:   emailScheduler,
		emailFetcher:     emailFetcher,
		dataDir:          dataDir,
		financeStore:     finance.NewStore(),
		auditStore: func() redclaw.AuditStoreFull {
			production := isProductionConfig(cfg)
			if pool == nil {
				if production {
					log.Printf("[Server] audit PG initialization failed: postgres pool is nil")
					return nil
				}
				return redclaw.NewAuditStore()
			}
			pgStore, err := redclaw.NewPGAuditStore(pool)
			if err == nil {
				return pgStore
			}
			log.Printf("[Server] audit PG initialization failed: %v", err)
			if production {
				return nil
			}
			return redclaw.NewAuditStore()
		}(),
		mobileCreates: newMobileCreateCache(),
		llmGWCache:    newLLMGatewayCache(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     buildOriginChecker(cfg.AllowedOrigins, cfg.DevAuth),
		},
	}

	// 初始化 RedClaw 桥接（如果配置了 RedClaw）
	if s.cfg.RedClawBaseURL != "" {
		rcCfg := redclaw.ClientConfig{
			BaseURL:    s.cfg.RedClawBaseURL,
			Secret:     s.cfg.RedClawSecret,
			TenantID:   s.cfg.RedClawTenantID,
			TimeoutSec: s.cfg.RedClawTimeoutSec,
		}
		rcClient, err := redclaw.NewClient(rcCfg)
		if err != nil {
			log.Printf("[Server] Failed to initialize RedClaw client: %v", err)
		} else {
			s.redclawBridge = redclaw.NewBridge(rcClient, s.pushRedClawEvent)
			s.redclawBridge.Start()
			log.Println("[Server] RedClaw bridge initialized")
		}
	}

	return s
}

// SetOpenCodeManagers 由 main.go 在 server.New 之后注入 OpenCode 域管理器。
// 使用 setter 而非扩展 New 签名，避免参数膨胀。所有 manager 允许为 nil。
func (s *Server) SetMeetingStore(ms *meeting.Store) {
	s.meetingStore = ms
}

func (s *Server) SetOpenCodeManagers(ocMgr *opencode.Manager, eventMgr *opencode.EventStreamManager, permMgr *opencode.PermissionManager, quesMgr *opencode.QuestionManager) {
	s.opencodeManager = ocMgr
	s.eventMgr = eventMgr
	s.permMgr = permMgr
	s.quesMgr = quesMgr
}

// SetLLMGatewayStore 注入 LLM 网关配置持久化 store（PG 或 SQLite fallback）。
func (s *Server) SetLLMGatewayStore(store LLMGatewayConfigStore) {
	s.llmGWStore = store
	if s.llmGWCache == nil {
		s.llmGWCache = newLLMGatewayCache()
	}
}

// LLMGatewayStore 返回内部 store（main 装配阶段判断是否需要预加载）。
func (s *Server) LLMGatewayStore() LLMGatewayConfigStore { return s.llmGWStore }

// SetGatewayNodeStore 注入网关节点注册表，并构造配套的 admin API 客户端。
// store 为 nil 时 /api/llm-gateway/nodes 全部返回 503（无 PG 或缺 master key）。
func (s *Server) SetGatewayNodeStore(store *GatewayNodeStore) {
	s.gatewayNodes = store
	if store != nil {
		s.gatewayClient = newGatewayAdminClient(store)
	}
}

// GatewayNodeStore 返回节点注册表，供 main 装配阶段做 legacy 导入。
func (s *Server) GatewayNodeStore() *GatewayNodeStore { return s.gatewayNodes }

// SetMigrationService 注入会话跨主机迁移编排服务（registry/adapter/pluginHub 就绪后由 main 装配）。
func (s *Server) SetMigrationService(svc *migration.Service) {
	s.migrationSvc = svc
}

// SetAuthExt 注入 Phase C 邮箱验证码扩展依赖。三个参数均允许 nil：
//
//	codeStore        — 必填才能让 send-code/code-login/register/forgot-password 工作；
//	                   nil 时对应 handler 返回 503（PG 未初始化）。
//	smtpClient       — 可选；nil 表示 SMTP 未配置，此时 send-code 仍可生成验证码
//	                   并入库，但不发邮件（POCKET_SMTP_DEBUG_ECHO 可让响应回显）。
//	redclawAuthClient — 可选；nil 或调用失败时镜像路径 fail-soft，不阻塞本地。
func (s *Server) SetAuthExt(codeStore *auth.CodeStore, smtpClient *notify.Client, redclawAuthClient *redclaw.AuthClient) {
	s.codeStore = codeStore
	s.smtpClient = smtpClient
	s.redclawAuthClient = redclawAuthClient
}

// CodeStore 返回注入的验证码 store（nil = 未注入）。供装配期判断。
func (s *Server) CodeStore() *auth.CodeStore { return s.codeStore }

// SetBiometricStore 注入生物识别凭证 store。nil = PG/生物识别功能未启用。
func (s *Server) SetBiometricStore(store *auth.BiometricStore) {
	s.biometricStore = store
}

// SetWebAuthnVerifier 注入 WebAuthn 签名验证器。
func (s *Server) SetWebAuthnVerifier(verifier auth.WebAuthnVerifierIface) {
	s.webAuthnVerifier = verifier
}

// BiometricStore 返回生物识别凭证 store，供启动装配和测试使用。
func (s *Server) BiometricStore() *auth.BiometricStore { return s.biometricStore }

// SetIdentityStore 注入 S0-A Identity Core store。nil = 身份/工作空间功能降级
// （登录仍可用，但无 workspace 隔离/邀请/设备管理）。
func (s *Server) SetIdentityStore(store *identity.Store) {
	s.identityStore = store
}

// SetLLMBFF 注入 S0-B 统一 LLM BFF service + 可选 summarizer。
// provider 由调用方通过 NewLLMGatewayBFFProvider 包装；nil service = 503。
func (s *Server) SetLLMBFF(svc *llmbff.Service, sum llmbff.Summarizer) {
	s.llmBFF = svc
	if sum != nil {
		s.llmBFFSummarizer = sum
	}
}

// SetQuotaEnforcer 注入 P3 配额执行器；nil 时 handler 跳过预算检查。
func (s *Server) SetQuotaEnforcer(e *quota.Enforcer) {
	s.quotaEnforcer = e
}

// QuotaEnforcer 返回注入的执行器；未注入时返 nil。供启动期 env
// 开关（QUOTA_ENFORCE_MODE 等）按需触发 SetEnforceMode。
func (s *Server) QuotaEnforcer() *quota.Enforcer { return s.quotaEnforcer }

// SetLobsterSync 注入 S0-C Lobster Vault 加密镜像同步 store。
func (s *Server) SetLobsterSync(store *lobster.SyncStore) {
	s.lobsterSync = store
}

// SetAgentBridge 注入 S0-D Agent Bridge + store。nil bridge = /api/agents 503。
func (s *Server) SetAgentBridge(b *agentbridge.Bridge, store *agentbridge.Store) {
	s.agentBridge = b
	s.agentStore = store
}

// SetScheduledTaskStore wires the PostgreSQL-backed automation definition
// store after Server construction. Keeping this as a setter avoids expanding
// the already compatibility-sensitive New constructor.
func (s *Server) SetScheduledTaskStore(store *scheduledtask.Store) {
	s.scheduledTaskStore = store
}

// SetScheduledTaskScheduler wires manual-trigger access and scheduler
// observability to the HTTP layer.
func (s *Server) SetScheduledTaskScheduler(scheduler *scheduledtask.Scheduler) {
	s.scheduledTaskScheduler = scheduler
}

func (s *Server) ScheduledTaskStore() *scheduledtask.Store { return s.scheduledTaskStore }
func (s *Server) ScheduledTaskScheduler() *scheduledtask.Scheduler {
	return s.scheduledTaskScheduler
}

// RedClawBridge returns the configured RedClaw bridge for trusted server-side
// automation wiring. HTTP callers still go through authenticated handlers.
func (s *Server) RedClawBridge() *redclaw.Bridge { return s.redclawBridge }

// AgentBridge returns the configured OpenCode Agent Bridge for automation
// executors. The bridge itself enforces workspace-scoped agent lookup.
func (s *Server) AgentBridge() *agentbridge.Bridge { return s.agentBridge }

// LLMBFF returns the configured unified LLM service for automation executors.
func (s *Server) LLMBFF() *llmbff.Service { return s.llmBFF }

// KxmemoryClient returns the configured kxmemory client for automation
// executors. The interface keeps the scheduler independent of HTTP details.
func (s *Server) KxmemoryClient() kxmemory.Client { return s.kxmemory }

// SetNotifyCenter 注入 S0-E Notification Center。
func (s *Server) SetNotifyCenter(svc *notifycenter.Service, store *notifycenter.Store) {
	s.notifySvc = svc
	s.notifyStore = store
}

// SetChatAgentStore 注入 AI 对话智能体角色管理 store。
// 接受 chatagent.StoreIface，PG Store 和 SQLiteStore 都满足。
func (s *Server) SetChatAgentStore(store chatagent.StoreIface) {
	s.chatAgentStore = store
}

// SetChatAgentSync 注入智能体云端同步 store（Acc PG）。
func (s *Server) SetChatAgentSync(sync *chatagent.SyncStore) {
	s.chatAgentSync = sync
}

// SetAgentRegistry 注入 ACP agent registry（W5 新增）。
func (s *Server) SetAgentRegistry(reg *agent.Registry) {
	s.agents = reg
}

// PluginHub 返回内部的 PluginHub，供 main 装配迁移服务等需要下发命令的组件复用。
func (s *Server) PluginHub() *ws.PluginHub { return s.pluginHub }

// WSHub 返回内部的业务事件 Hub，供 S0-E Notification Center 等需要前台推送
// 的组件复用（避免把私有字段暴露成公开）。
func (s *Server) WSHub() *ws.Hub { return s.wsHub }

// AuditStore 暴露内部审计存储，供 pocketd 装配旁路导出（如 FileExporter
// 落盘轮转 / 外部 SIEM 转发）。只读语义由 AuditStore 自身保证。
func (s *Server) AuditStore() redclaw.RangeStore {
	return s.auditStore
}

const (
	maxRequestBodyBytes = 2 << 20
	maxAudioBodyBytes   = 25 << 20
	// /api/llm/stream 多模态上限：单消息最多 4 张图、单张 6MB；按 base64
	// data URL 计算含 JSON 包装后的总量上限约为 4 × 8MB ≈ 32MB。
	maxChatBodyBytes = 32 << 20
)

func requestBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxRequestBodyBytes)
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/stt/transcribe"),
			strings.HasPrefix(r.URL.Path, "/api/meetings/") && strings.HasSuffix(r.URL.Path, "/transcribe"):
			limit = maxAudioBodyBytes
		case r.URL.Path == "/api/llm/stream" && r.Method == http.MethodPost:
			limit = maxChatBodyBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware sets baseline hardening headers on every
// response. HSTS is suppressed in dev mode (POCKET_DEV_AUTH=true) because
// locking a developer machine into HTTPS breaks local iteration.
//
// The other three headers are always on:
//   - X-Frame-Options: DENY         — anti-clickjacking
//   - X-Content-Type-Options: nosniff — anti-MIME-sniff
//   - Referrer-Policy: no-referrer   — do not leak request paths
//
// Setting them once at the edge keeps the contract uniform across all
// routes; downstream handlers must not remove these.
func securityHeadersMiddleware(next http.Handler, devAuth bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if !devAuth {
			// 2-year HSTS with subdomains, as recommended by OWASP.
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/instances", s.requireAuth(s.handleInstances))
	mux.HandleFunc("/api/sessions/", s.requireAuth(s.handleSessions))
	mux.HandleFunc("/api/sessions", s.requireAuth(s.handleAllSessions)) // 新增：获取所有会话
	mux.HandleFunc("/api/tasks", s.requireAuth(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.requireAuth(s.handleTaskOperations))
	// P1 双向 MCP：委派任务创建到 ACC（acc_create_task）。与 /api/tasks 的
	// source=acc 只读守卫分开——这里是显式的写路径，返回 ACC 创建的任务。
	mux.HandleFunc("/api/tasks/delegate", s.requireAuth(s.handleDelegateTask))
	mux.HandleFunc("/api/scheduled-tasks", s.requireAuth(s.handleScheduledTasks))
	mux.HandleFunc("/api/scheduled-tasks/", s.requireAuth(s.handleScheduledTaskOperations))
	mux.HandleFunc("/api/config/models", s.requireAuth(s.handleModelConfig))
	mux.HandleFunc("/api/config/reload", s.requireAuth(s.handleConfigReload))
	mux.HandleFunc("/api/config/models/test", s.requireAuth(s.handleModelTest))
	mux.HandleFunc("/ws", s.requireAuth(s.handleWebSocket))
	mux.HandleFunc("/api/app/check-update", s.handleCheckUpdate)
	mux.HandleFunc("/api/app/download", s.handleDownloadAPK)
	// 飞书事件回调 (m.kxpms.cn/callback/feishu 由 56 nginx 转发到 9010)
	mux.HandleFunc("/callback/feishu", s.handleFeishuCallback)

	// ---- Phase 0: 个人助理模块路由 ----
	// 认证
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	// Phase C: 邮箱验证码 / 注册 / 忘记密码 / 验证码登录 / 已登录改密码
	mux.HandleFunc("/api/auth/send-code", s.handleAuthSendCode)
	mux.HandleFunc("/api/auth/register", s.handleAuthRegister)
	mux.HandleFunc("/api/auth/code-login", s.handleAuthCodeLogin)
	mux.HandleFunc("/api/auth/forgot-password", s.handleAuthForgotPassword)
	mux.HandleFunc("/api/auth/reset-password", s.requireAuth(s.handleAuthResetPassword))
	// 生物认证（server_biometric.go；webAuthnVerifier 未配置时降级为 P0 stub）
	mux.HandleFunc("/api/auth/biometric/register/begin", s.requireAuth(s.handleBiometricRegisterBegin))
	mux.HandleFunc("/api/auth/biometric/register/finish", s.requireAuth(s.handleBiometricRegisterFinish))
	mux.HandleFunc("/api/auth/biometric/login/begin", s.handleBiometricLoginBegin)
	mux.HandleFunc("/api/auth/biometric/login/finish", s.handleBiometricLoginFinish)
	mux.HandleFunc("/api/auth/biometric/credentials", s.requireAuth(s.handleBiometricCredentials))
	mux.HandleFunc("/api/auth/biometric/credentials/", s.requireAuth(s.handleBiometricCredentialOps))
	// S0-A: Identity Core（工作空间 / 成员 / 设备）
	mux.HandleFunc("/api/workspaces", s.requireAuth(s.handleWorkspaces))
	mux.HandleFunc("/api/workspaces/", s.requireAuth(s.handleWorkspaceOps))
	// 语音笔记
	mux.HandleFunc("/api/notes", s.requireAuth(s.handleNotes))
	mux.HandleFunc("/api/notes/", s.requireAuth(s.handleNoteOperations))
	// 代码片段
	mux.HandleFunc("/api/snippets", s.requireAuth(s.handleSnippets))
	mux.HandleFunc("/api/snippets/", s.requireAuth(s.handleSnippetOps))
	// 会议管理 — /api/meetings & /api/meetings/ 注册在更下方（STT 之后）。
	// 早期 "handleMeetingOps" handler 在 S0–S2 合并中丢失；其路由会被下方
	// handleMeetingRouter (summary/recommend/refine) 与 handleMeetings (list/create)
	// 重新接管。先前的 HandleFunc 是 ServeMux 重复注册会在 Handler() 触发 panic，
	// 必须移除。
	// 聊天总结
	mux.HandleFunc("/api/chat-summaries", s.requireAuth(s.handleChatSummaries))
	mux.HandleFunc("/api/chat-summaries/", s.requireAuth(s.handleChatSummaryOps))
	// 邮箱助手
	mux.HandleFunc("/api/email/accounts", s.requireAuth(s.handleEmailAccounts))
	// /api/email/accounts/ subtree handles {id} (GET/PUT/DELETE) and
	// {id}/test-smtp (POST). See handleEmailAccountOps for routing.
	mux.HandleFunc("/api/email/accounts/", s.requireAuth(s.handleEmailAccountOps))
	mux.HandleFunc("/api/email/summaries", s.requireAuth(s.handleEmailSummaries))
	mux.HandleFunc("/api/email/summaries/", s.requireAuth(s.handleEmailSummaryOps))
	mux.HandleFunc("/api/email/vacations", s.requireAuth(s.handleEmailVacations))
	mux.HandleFunc("/api/email/vacations/", s.requireAuth(s.handleEmailVacationOps))
	mux.HandleFunc("/api/email/send", s.requireAuth(s.handleEmailSend))
	mux.HandleFunc("/api/emails", s.requireAuth(s.handleEmails))
	mux.HandleFunc("/api/emails/sync", s.requireAuth(s.handleEmailSync))
	mux.HandleFunc("/api/emails/", s.requireAuth(s.handleEmailOps))
	// /api/email/accounts/test-smtp is intentionally NOT registered — the
	// {id}/test-smtp path is dispatched by handleEmailAccountOps so the
	// {id} is actually a workspace-scoped account id rather than the
	// literal "test-smtp" suffix.
	// OAuth 流程：start 返回 authorization URL；callback 由 email 包提供，
	// 不走 requireAuth（OAuth provider 用 state 而非 JWT 验证回调合法性）。
	mux.HandleFunc("/api/email/oauth/start", s.requireAuth(s.startEmailOAuth))
	mux.HandleFunc("/callback/email/oauth", s.handleEmailOAuthCallback())
	// 密码箱（子树，含 /api/vault/sync/latest）
	mux.HandleFunc("/api/vault/sync/", s.requireAuth(s.handleVaultSync))
	// STT 云端兜底（消耗外部 API 配额，必须认证）
	mux.HandleFunc("/api/stt/transcribe", s.requireAuth(s.handleSttTranscribe))
	mux.HandleFunc("/api/meetings", s.requireAuth(s.handleMeetings))
	mux.HandleFunc("/api/meetings/", s.requireAuth(s.handleMeetingRouter))
	// 记账
	mux.HandleFunc("/api/finance", s.requireAuth(s.handleFinance))
	mux.HandleFunc("/api/finance/", s.requireAuth(s.handleFinanceOps))
	// S0-C: Lobster Vault 加密镜像同步（e2ee assets 跨设备同步）
	mux.HandleFunc("/api/assets/sync", s.requireAuth(s.handleAssetSync))
	// Phase C: 无状态 AI 网关（仅转发嵌入/LLM，不存数据）
	mux.HandleFunc("/api/embed", s.requireAuth(s.handleEmbed))
	mux.HandleFunc("/api/llm/chat", s.requireAuth(s.handleLLMChat))
	// S0-B: 统一 LLM BFF（流式 + 用量查询）。老的 /api/llm/chat 保留兼容，
	// llmBFF 启用时优先走 BFF；未启用时回退到老 handler。
	mux.HandleFunc("/api/llm/stream", s.requireAuth(s.handleLLMBFFStream))
	mux.HandleFunc("/api/llm/usage", s.requireAuth(s.handleLLMBFFUsage))
	mux.HandleFunc("/api/llm/quota", s.requireAuth(s.handleLLMBFFQuota))
	mux.HandleFunc("/api/llm/models", s.requireAuth(s.handleLLMBFFModels))
	mux.HandleFunc("/api/integration/status", s.requireAuth(s.handleIntegrationStatus))

	// OpenCode 管理 API（会话/实例数据属于认证用户可见范围）
	mux.HandleFunc("/api/opencode/sessions", s.requireAuth(s.handleOpenCodeSessions))
	mux.HandleFunc("/api/opencode/sessions/", s.requireAuth(s.handleOpenCodeSessionOperations))
	mux.HandleFunc("/api/opencode/instances/", s.requireAuth(s.handleOpenCodeInstanceOperations))
	mux.HandleFunc("/api/opencode/cache/refresh", s.requireAuth(s.handleOpenCodeRefreshCache))
	mux.HandleFunc("/api/opencode/dispatch", s.requireAuth(s.handleOpenCodeDispatch))
	// S0-D: Agent Bridge（list/get/create/send）。底层复用 opencode adapter。
	mux.HandleFunc("/api/agents", s.requireAuth(s.handleAgents))
	mux.HandleFunc("/api/agents/", s.requireAuth(s.handleAgentOps))
	// S0-E: Notification Center（inbox + rules）
	mux.HandleFunc("/api/notifications", s.requireAuth(s.handleNotifications))
	mux.HandleFunc("/api/notifications/", s.requireAuth(s.handleNotificationOps))
	mux.HandleFunc("/api/notifications/rules", s.requireAuth(s.handleNotificationRules))

	// AI 对话智能体角色管理
	mux.HandleFunc("/api/chat-agents", s.requireAuth(s.handleChatAgentsList))
	mux.HandleFunc("/api/chat-agents/sync/upload", s.requireAuth(s.handleChatAgentSyncUpload))
	mux.HandleFunc("/api/chat-agents/sync/download", s.requireAuth(s.handleChatAgentSyncDownload))
	mux.HandleFunc("/api/chat-agents/sync/status", s.requireAuth(s.handleChatAgentSyncStatus))
	mux.HandleFunc("/api/chat-agents/", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		// 分发到 GET/:id, POST, PUT/:id, DELETE/:id
		if r.URL.Path == "/api/chat-agents/" && r.Method == http.MethodPost {
			s.handleChatAgentsCreate(w, r)
		} else if strings.HasPrefix(r.URL.Path, "/api/chat-agents/") {
			switch r.Method {
			case http.MethodGet:
				s.handleChatAgentsGet(w, r)
			case http.MethodPut:
				s.handleChatAgentsUpdate(w, r)
			case http.MethodDelete:
				s.handleChatAgentsDelete(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		} else {
			writeError(w, http.StatusNotFound, "not found")
		}
	}))

	// 会话迁移方案：跨主机迁移 API
	mux.HandleFunc("/api/migration", s.requireAuth(s.handleMigration))
	mux.HandleFunc("/api/migration/preview", s.requireAuth(s.handleMigrationPreview))

	// ---- Phase V3: LLM Gateway 配置管理 ----
	// 用户在 Settings 改 llmgo.kxpms.cn URL / API Key；pocketd 写入 OpenCode 配置
	mux.HandleFunc("/api/llm-gateway/config", s.requireAuth(s.handleLLMGatewayConfig))
	mux.HandleFunc("/api/llm-gateway/test", s.requireAuth(s.handleLLMGatewayTest))
	mux.HandleFunc("/api/llm-gateway/models", s.requireAuth(s.handleLLMGatewayModels))

	// ---- 网关运维控制面：节点注册 + admin API 白名单代理 + 实时请求流 ----
	// 读操作任何已认证用户可用；写操作（凭据上下线/探测）要求 admin 角色，
	// 由 handler 内的 requireGatewayAdmin 把关并落审计。
	mux.HandleFunc("/api/llm-gateway/nodes", s.requireAuth(s.handleLLMGatewayNodes))
	mux.HandleFunc("/api/llm-gateway/nodes/", s.requireAuth(s.handleLLMGatewayNodes))

	// ---- Phase 1.1: 诊断 / 健康端点 ----
	mux.HandleFunc("/api/diagnostics/kxmemory", s.requireAuth(s.handleDiagnosticsKxmemory))
	mux.HandleFunc("/api/diagnostics/agents", s.requireAuth(s.handleDiagnosticsAgents))

	// ---- Phase V3: 移动端真实会话交互 API ----
	// SSE / Prompt / Interrupt / Messages / Create — 转发到 OpenCode 上游
	mux.HandleFunc("/api/mobile/sessions", s.requireAuth(s.handleMobileSessionRouter))
	mux.HandleFunc("/api/mobile/sessions/", s.requireAuth(s.handleMobileSessionRouter))
	mux.HandleFunc("/api/mobile/approvals", s.requireAuth(s.handleMobileApprovalRouter))
	mux.HandleFunc("/api/mobile/approvals/", s.requireAuth(s.handleMobileApprovalRouter))
	mux.HandleFunc("/api/mobile/events/snapshot", s.requireAuth(s.handleMobileEventsSnapshot))

	// Plugin/Manager WebSocket routes
	mux.HandleFunc("/plugin/ws", s.requireAuth(s.handlePluginWebSocket))
	mux.HandleFunc("/api/plugin/status", s.requireAuth(s.handlePluginStatus))
	mux.HandleFunc("/api/plugin/command", s.requireAuth(s.handleSendCommand))

	// RedClaw 企业后端集成（代理/配额消耗必须认证）
	mux.HandleFunc("/api/redclaw/health", s.requireAuth(s.handleRedClawHealth))
	mux.HandleFunc("/api/redclaw/chat", s.requireAuth(s.handleRedClawChat))
	mux.HandleFunc("/api/redclaw/knowledge/search", s.requireAuth(s.handleRedClawKnowledgeSearch))

	// Audit 审计日志
	mux.HandleFunc("/api/audit/logs", s.requireAuth(s.handleAuditLogs))
	mux.HandleFunc("/api/audit/export", s.requireAuth(s.handleAuditExport))

	// ---- 产品方案/PPT API ----
	mux.HandleFunc("/api/presentations", s.requireAuth(s.handlePresentations))
	mux.HandleFunc("/api/presentations/render", s.requireAuth(s.handleRenderPresentation))

	// 中间件顺序（外→内）：
	//   requestBodyLimit   限制请求体大小
	//   recovery           panic 兜底
	//   logging            请求日志
	//   cors               跨域头
	//   securityHeaders    加固响应头
	//   longLivedPath      清除长连接 SSE 的写 deadline
	//   mux                业务路由
	//
	// longLivedPath 必须位于 mux 之外（看不清路径就扫不到前缀），同时在
	// securityHeaders 之内（中间件包装层越少，NewResponseController 越可能
	// 穿透到底层 ResponseWriter）。
	handler := recoveryMiddleware(
		s.loggingMiddleware(
			corsMiddleware(
				securityHeadersMiddleware(
					longLivedPathMiddleware(mux),
					s.cfg.DevAuth,
				),
				s.cfg.AllowedOrigins,
				s.cfg.DevAuth,
			),
		),
	)
	return requestIDMiddleware(requestBodyLimitMiddleware(handler))
}

// longLivedPaths 列出需要把 http.Server.WriteTimeout 拉满的端点。
//
// http.Server 上配置了 WriteTimeout: 30s（见 cmd/pocketd/main.go），对普通请求
// 是必要的 Slowloris 防护，但对 SSE 长连接会在 30s 处直接掐断一条本该长活的流。
//
// 历史上曾要求每个 handler 自己用 http.NewResponseController.SetWriteDeadline
// 清掉本连接 deadline。问题是：这条契约散落在每个 handler 里很容易遗漏，移动
// 端 session event / 插件 WS 等端点就有过这种被掐断的故障。
//
// 把判断提到中间件层之后，所有 SSE/长连接路由只需要挂上前缀白名单即可。
// WebSocket 升级走的是 gorilla/websocket 自己的 deadline，不在 WriteTimeout 管辖
// 范围，所以这里只覆盖 SSE 路径。
var longLivedPaths = []string{
	"/api/llm/stream",         // S0-B LLM BFF 流式聊天
	"/api/llm-gateway/nodes/", // 网关运维控制面（live-stream 等）
	"/api/mobile/sessions/",   // 移动端 session SSE（含 /event）
	"/api/llmbff/stream",      // 同上的历史别名（保留兼容）
}

func longLivedPathMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		for _, p := range longLivedPaths {
			if strings.HasPrefix(path, p) {
				// 只清"当前连接"的写 deadline，不动全局 server.WriteTimeout。
				// 中间件包装过的 ResponseWriter（cors/logging/recovery）通常仍
				// 支持 NewResponseController；不支持的则降级到被 30s 掐断。
				rc := http.NewResponseController(w)
				_ = rc.SetWriteDeadline(time.Time{})
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler, allowedOrigins string, devAuth bool) http.Handler {
	originChecker := buildOriginChecker(allowedOrigins, devAuth)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originChecker(r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "3600")

		if r.Method == "OPTIONS" {
			if origin != "" && !originChecker(r) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
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
		instances = s.registry.ListInstancesForWorkspace(s.workspaceIDFromRequest(r))
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
	// 支持 instance_id（通过 Registry 解析可信 URL）。不再接受调用方直接提供的 instance URL。
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	if s.registry == nil {
		http.Error(w, "registry not configured", http.StatusServiceUnavailable)
		return
	}

	instanceBaseURL, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
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
			apiBase, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)

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
		instances := s.registry.ListInstancesForWorkspace(s.workspaceIDFromRequest(r))

		for _, inst := range instances {
			// 通过 registry 获取实例的 API base URL
			apiBase, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), inst.ID)

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
			tasks, hasMore, err := s.taskStore.ListTasksCursorScoped(r.Context(), s.workspaceIDFromRequest(r), limit, createdAt, id)
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
						ID:           p.ID,
						Title:        p.Title,
						Status:       p.Status,
						Priority:     "normal",
						WorkstreamID: workstreamID,
						Source:       "acc",
						CreatedAt:    time.Unix(now, 0),
						UpdatedAt:    time.Unix(now, 0),
					})
				}
			}
		}

		// 2. OpenCode 实例会话（HTTP adapter）
		if (source == "" || source == "opencode") && instanceID != "" && s.opencode != nil && s.registry != nil {
			apiBaseURL, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)
			if err == nil {
				remoteTasks, err := s.opencode.ListRemoteTasks(r.Context(), apiBaseURL, "", limit)
				if err != nil {
					log.Printf("Failed to fetch OpenCode sessions for instance %s: %v", instanceID, err)
				} else {
					now := time.Now().Unix()
					for _, rt := range remoteTasks {
						allTasks = append(allTasks, task.Task{
							ID:           rt.ID,
							Title:        rt.Title,
							Status:       rt.Status,
							Priority:     "normal",
							WorkstreamID: instanceID, // OpenCode 实例 ID 即 workstream
							Source:       "opencode",
							CreatedAt:    time.Unix(now, 0),
							UpdatedAt:    time.Unix(now, 0),
						})
					}
				}
			}
		}

		// 3. 本地任务（PG store，nil-safe 降级）
		if (source == "" || source == "local") && s.taskStore != nil {
			localTasks, err := s.taskStore.ListTasksScoped(r.Context(), s.workspaceIDFromRequest(r))
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
		// 提前 body 解析用于 source 校验：即使 taskStore 为 nil 也要
		// 拦截 source=acc POST——避免攻击者借助 remote-only 模式探测。
		var req task.Task
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		// P3 §5 「企业集成只读」：POST 不允许向 ACC 写入。当前 mcp.Client
		// 本身没有写 method（Capabilities.Write=false），但请求体里显式
		// source=acc 仍暗示客户端试图把任务推到 ACC。这里 fail-closed，
		// 写 audit；与 docs/优化v4/04 §7.4 「ACC 写操作必须 capability +
		// 审批就绪」一致——目前未满足。
		if req.Source == "acc" {
			s.Write(r, "task.post.rejected", "task:"+req.ID, AuditFields{
				Success: false,
				Detail:  "reason=acc_write_disabled",
			})
			http.Error(w, "source=acc is read-only; POST not allowed", http.StatusForbidden)
			return
		}
		if s.taskStore == nil {
			http.Error(w, "local task store not configured (remote-only mode)", http.StatusServiceUnavailable)
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
		if req.Status == "" {
			req.Status = "active"
		}
		if !isValidTaskStatus(req.Status) {
			http.Error(w, "invalid task status", http.StatusBadRequest)
			return
		}
		if req.Status == "completed" {
			http.Error(w, "new tasks cannot be completed", http.StatusConflict)
			return
		}
		// 租户来自已认证 claims，忽略请求体里的 workspaceId，避免调用方把任务
		// 写进别人的 workspace。
		req.WorkspaceID = s.workspaceIDFromRequest(r)
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

// handleDelegateTask 实现 POST /api/tasks/delegate：把任务创建委派给 ACC
// （经 mcp.Client.CreateTask → acc_create_task），返回 ACC 创建的任务
// （source=acc）。与 /api/tasks 的 POST 不同——那里对 source=acc 是 fail-closed
// 的只读守卫，本端点才是显式写路径，供移动端「经 pocketd 触发 ACC 建任务」。
//
// 请求体：{ "kind"?, "title", "description"? }。title 必填。
func (s *Server) handleDelegateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mcpClient == nil {
		http.Error(w, "ACC MCP client not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	args := map[string]interface{}{"title": req.Title}
	if req.Kind != "" {
		args["kind"] = req.Kind
	}
	if req.Description != "" {
		args["description"] = req.Description
	}

	out, err := s.mcpClient.CreateTask(r.Context(), args)
	if err != nil {
		log.Printf("[tasks/delegate] CreateTask failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// ACC 以 toolJSON 返回 JSON 字符串；原样回传，并附 source=acc 标识。
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"source": "acc",
		"raw":    out,
	})
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

	// Check for task subresources.
	if len(path) > 0 {
		parts := splitPath(path)
		if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "attach-session" {
			s.handleAttachSession(w, r, parts[0])
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodPost) && len(parts) == 2 && parts[1] == "sessions" {
			s.handleTaskSessions(w, r, parts[0])
			return
		}
		if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "accept" {
			s.handleAcceptTask(w, r, parts[0])
			return
		}
	}

	// GET /api/tasks/{id}
	if r.Method == http.MethodGet {
		taskID := path
		task, err := s.taskStore.GetTaskScoped(r.Context(), taskID, s.workspaceIDFromRequest(r))
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
		workspaceID := s.workspaceIDFromRequest(r)
		current, err := s.taskStore.GetTaskScoped(r.Context(), path, workspaceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if update.Status != nil && !isValidTaskStatus(*update.Status) {
			http.Error(w, "invalid task status", http.StatusBadRequest)
			return
		}
		var updated *task.Task
		if update.Status != nil && *update.Status == "completed" {
			updated, err = s.taskStore.CompleteTaskScoped(r.Context(), path, workspaceID, update)
		} else {
			updated, err = s.taskStore.UpdateTaskScoped(r.Context(), path, workspaceID, update)
		}
		if err != nil {
			if errors.Is(err, task.ErrPendingApprovals) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if current.Status != updated.Status {
			s.auditTaskStatusChange(r, updated, current.Status)
		}
		s.broadcastTaskEvent("task_updated", updated)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updated)
		return
	}

	// DELETE /api/tasks/{id} — 删除任务及其会话关联
	if r.Method == http.MethodDelete {
		if err := s.taskStore.DeleteTaskScoped(r.Context(), path, s.workspaceIDFromRequest(r)); err != nil {
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

	if err := s.taskStore.AttachSessionScoped(r.Context(), req, s.workspaceIDFromRequest(r)); err != nil {
		// 任务不在当前 workspace 时与"不存在"同义，返回 404 而非 500。
		if strings.Contains(err.Error(), "task not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 广播会话附加事件
	s.broadcastSessionEvent("session_attached", &req)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

// handleAcceptTask implements POST /api/tasks/{id}/accept. Acceptance is the
// dedicated sub-resource for the terminal `accepted` verdict on a `completed`
// task. The actor is taken from authenticated claims (never the body); the
// evidence_bundle is decoded into the structured payload. See
// docs/superpowers/specs/2026-08-17-task-acceptance-evidence-design.md.
func (s *Server) handleAcceptTask(w http.ResponseWriter, r *http.Request, taskID string) {
	var req struct {
		EvidenceBundle task.EvidenceBundle `json:"evidenceBundle"`
		Note           string              `json:"note,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// Top-level note folds into bundle.note so the two-arg form is a
	// convenience, not an alternate source of truth.
	if req.Note != "" && req.EvidenceBundle.Note == "" {
		req.EvidenceBundle.Note = req.Note
	}

	actor := s.userIDFromRequest(r)
	if actor == "" || actor == "local" {
		http.Error(w, "missing authenticated actor", http.StatusUnauthorized)
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	previous, err := s.taskStore.GetTaskScoped(r.Context(), taskID, workspaceID)
	if err != nil {
		// Cross-workspace and missing-task are indistinguishable at the wire.
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	accepted, err := s.taskStore.AcceptTaskScoped(r.Context(), taskID, workspaceID, actor, req.EvidenceBundle)
	if err != nil {
		if errors.Is(err, task.ErrTaskNotCompletable) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	s.auditTaskStatusChange(r, accepted, previous.Status)
	s.broadcastTaskEvent("task_updated", accepted)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(accepted)
}

func (s *Server) pendingApprovalsForTask(ctx context.Context, taskID, workspaceID string) (int, error) {
	if s.permMgr == nil || s.quesMgr == nil {
		return 0, fmt.Errorf("task approval managers are not configured")
	}
	links, err := s.taskStore.ListSessionsForTaskScoped(ctx, taskID, workspaceID)
	if err != nil {
		return 0, err
	}
	pending := 0
	for _, link := range links {
		pending += len(s.permMgr.ListPending(link.InstanceID, link.SessionID))
		pending += len(s.quesMgr.ListPending(link.InstanceID, link.SessionID))
	}
	return pending, nil
}

func isValidTaskStatus(status string) bool {
	switch status {
	case "active", "blocked", "completed":
		return true
	default:
		return false
	}
}

func taskStatusUpdateError(current *task.Task, requestedStatus *string) (int, string) {
	if requestedStatus == nil {
		return 0, ""
	}
	if !isValidTaskStatus(*requestedStatus) {
		return http.StatusBadRequest, "invalid task status"
	}
	if *requestedStatus == "completed" && current.PendingApprovals > 0 {
		return http.StatusConflict, "task has pending approvals"
	}
	return 0, ""
}

func (s *Server) auditTaskStatusChange(r *http.Request, updated *task.Task, previousStatus string) {
	if updated == nil {
		return
	}
	detail := fmt.Sprintf("from=%s;to=%s;pending_approvals=%d;request_id=%s",
		previousStatus, updated.Status, updated.PendingApprovals, s.requestIDFromContext(r))
	s.Write(r, "task.status.changed", "task:"+updated.ID, AuditFields{
		Detail:  detail,
		Success: true,
	})
}

func (s *Server) handleTaskSessions(w http.ResponseWriter, r *http.Request, taskID string) {
	links, err := s.taskStore.ListSessionsForTaskScoped(r.Context(), taskID, s.workspaceIDFromRequest(r))
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

	apiBaseURL, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)
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

	apiBaseURL, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)
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

	apiBaseURL, err := s.registry.GetInstanceAPIBaseForWorkspace(s.workspaceIDFromRequest(r), instanceID)
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
	claims := s.claimsFromContext(r)
	if claims == nil {
		log.Printf("WebSocket connection missing authenticated claims")
		return
	}
	workspaceID := claims.WorkspaceID
	if workspaceID == "" {
		workspaceID = "default"
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		clientID = r.RemoteAddr
	}

	client := ws.NewClientWithIdentity(s.wsHub, conn, clientID, claims.UserID, workspaceID)
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

// pushRedClawEvent RedClaw 桥接事件回调
func (s *Server) pushRedClawEvent(event redclaw.BridgeEvent) {
	// 通过 WebSocket Hub 广播事件
	if s.wsHub != nil {
		s.wsHub.Broadcast(event.Type, event.Payload)
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
// In dev mode (devAuth=true), localhost and 127.0.0.1 are allowed.
func buildOriginChecker(allowedOrigins string, devAuth bool) func(r *http.Request) bool {
	originSet := make(map[string]bool)
	if allowedOrigins != "" {
		for _, origin := range strings.Split(allowedOrigins, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				originSet[origin] = true
			}
		}
	}

	return func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true
		}

		if originSet[origin] {
			return true
		}

		if devAuth {
			u, err := url.Parse(origin)
			if err == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" &&
				(u.Scheme == "http" || u.Scheme == "https") &&
				(u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
				return true
			}
		}

		return false
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
	Version     string   `json:"version"`
	BuildNumber int      `json:"buildNumber"`
	DownloadURL string   `json:"downloadUrl"`
	FileSize    int64    `json:"fileSize"`
	Changelog   []string `json:"changelog"`
	ForceUpdate bool     `json:"forceUpdate"`
	ReleaseDate string   `json:"releaseDate"`
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
		CurrentVersion string `json:"currentVersion"`
		CurrentBuild   int    `json:"currentBuild"`
		Platform       string `json:"platform"`
		DeviceModel    string `json:"deviceModel"`
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
