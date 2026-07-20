package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/agent"
	"github.com/halfking/pocket-opencode/backend/internal/agentbridge"
	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/db"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/lobster"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/migration"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/server"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/tasksync"
	"github.com/halfking/pocket-opencode/backend/internal/vault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// Ensure data directory exists (still used for version.json, APK cache, etc.)
	dataDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// ---- Phase 0: shared PostgreSQL pool (replaces per-module SQLite) ----
	// 可选依赖：未配置时降级为"无本地任务存储"模式，仅依赖 ACC/llm-gateway 等远程服务
	var pool *pgxpool.Pool
	if cfg.PostgresDSN != "" {
		p, err := db.New(context.Background(), cfg.PostgresDSN)
		if err != nil {
			log.Fatalf("Failed to connect to Postgres: %v", err)
		}
		pool = p
		log.Println("Postgres pool initialized")
		defer pool.Close()
	} else {
		log.Println("WARN: POCKET_POSTGRES_DSN not set, running in remote-only mode (no local task cache)")
	}

	// ---- Module stores (all share the pool) ----
	var (
		taskStore  *task.Store // nil-safe: nil when pool is nil
		notesStore *notes.Store
		emailStore *email.Store
		vaultStore *vault.Store
	)
	if pool != nil {
		ts, err := task.NewStore(pool)
		if err != nil {
			log.Fatalf("task store: %v", err)
		}
		taskStore = ts
		ns, err := notes.NewStore(pool)
		if err != nil {
			log.Fatalf("notes store: %v", err)
		}
		notesStore = ns
		es, err := email.NewStore(pool)
		if err != nil {
			log.Fatalf("email store: %v", err)
		}
		emailStore = es
		vs, err := vault.NewStore(pool)
		if err != nil {
			log.Fatalf("vault store: %v", err)
		}
		vaultStore = vs
		log.Println("Module stores initialized (PG)")
	}

	// ---- Auth (multi-user, JWT) ----
	var (
		userStore *auth.UserStore
		jwtSigner *auth.Signer
	)
	if pool != nil {
		if err := auth.EnsureSchema(context.Background(), pool); err != nil {
			log.Fatalf("auth schema: %v", err)
		}
		us, err := auth.NewUserStore(pool)
		if err != nil {
			log.Fatalf("user store: %v", err)
		}
		userStore = us
		if n, _ := us.CountUsers(context.Background()); n == 0 {
			user := cfg.DevAuthUser
			pass := cfg.DevAuthPass
			if user == "" {
				user = "admin"
			}
			if pass == "" {
				if !cfg.DevAuth {
					log.Printf("WARN: users table empty and POCKET_DEV_AUTH not set; refusing to auto-create admin/admin")
				} else {
					pass = "admin"
				}
			}
			if pass != "" {
				if err := us.InsertUser(context.Background(), &auth.User{ID: "user-" + user, Username: user, Role: "admin"}, pass); err != nil {
					log.Printf("WARN: bootstrap first user %q: %v", user, err)
				} else {
					log.Printf("Bootstrap: created first admin user %q", user)
				}
			}
		}
		jwtSigner = auth.NewSigner(cfg.JWTSecret, 24*time.Hour)
		log.Println("Auth: user store + JWT signer initialized")
	} else if cfg.DevAuth {
		// Dev 模式无 PG 时：仍然 init JWT signer，让 requireAuth 通过（用户可用外部 JWT）。
		// userStore 仍 nil，所以 /api/auth/login 会 503；但其它 requireAuth 路由可用。
		jwtSigner = auth.NewSigner(cfg.JWTSecret, 24*time.Hour)
		log.Println("Dev mode: JWT signer initialized without user store (login disabled)")
	}

	// ---- 后端集成: kxmemory AI 编排服务（分类/SSOT/总结）----
	// 提前到这里构造，因为 email scheduler 也要用它（DailySummary）。
	var kxmem kxmemory.Client
	if cfg.KxMemoryBaseURL != "" {
		kxmem = kxmemory.NewClientWithPaths(cfg.KxMemoryBaseURL, cfg.JWTSecret, kxmemory.DefaultRetryConfig, kxmemory.Paths{
			NoteClassify:  cfg.KxMemoryNoteClassifyPath,
			EmailClassify: cfg.KxMemoryEmailClassifyPath,
			DailySummary:  cfg.KxMemoryDailySummaryPath,
		})
		log.Printf("kxmemory AI orchestrator enabled: %s (paths: note=%s email=%s summary=%s)",
			cfg.KxMemoryBaseURL,
			cfg.KxMemoryNoteClassifyPath,
			cfg.KxMemoryEmailClassifyPath,
			cfg.KxMemoryDailySummaryPath,
		)
	} else {
		log.Println("INFO: POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled")
	}

	// ---- Email crypto + fetcher + scheduler ----
	var (
		emailCrypto    *email.Crypto
		emailPending   *email.PendingOAuth
		emailFetcher   *email.Fetcher
		emailScheduler *email.Scheduler
	)
	if pool != nil {
		key, err := email.EnsureMasterKey(cfg.EmailMasterKey, dataDir)
		if err != nil {
			log.Printf("WARN: email master key: %v — email fetcher disabled", err)
		} else {
			if cfg.EmailMasterKey == "" {
				log.Printf("WARN: POCKET_EMAIL_MASTER_KEY not set; auto-generated key persisted to %s/email_master.key", dataDir)
			}
			ec, err := email.NewCrypto(key)
			if err != nil {
				log.Printf("WARN: email crypto init: %v — fetcher disabled", err)
			} else {
				emailCrypto = ec
				emailPending = email.NewPendingOAuth()
				go emailPending.GCLoop(context.Background())
				if emailStore != nil {
					emailFetcher = email.NewFetcher(emailStore, emailCrypto)
					emailScheduler = email.NewScheduler(emailStore, emailFetcher, cfg.EmailFetchEnabled)
					// 注入 kxmemory 客户端（可选）：未配置时 DailySummary 自动降级到 log-only。
					if kxmem != nil {
						emailScheduler.SetKxmemory(kxmem)
					}
					// OAuth refresh：可选启用，调用方需要同时提供 provider
					// client credentials 才能真正刷新 access token。
					oauthRefresher := email.NewDefaultOAuthRefresher()
					providers := []email.OAuthProviderConfig{}
					if cfg.EmailGoogleClientID != "" && cfg.EmailGoogleClientSecret != "" {
						providers = append(providers, email.OAuthProviderConfig{
							ProviderID:   "google",
							TokenURL:     "https://oauth2.googleapis.com/token",
							ClientID:     cfg.EmailGoogleClientID,
							ClientSecret: cfg.EmailGoogleClientSecret,
						})
					}
					if cfg.EmailMicrosoftClientID != "" && cfg.EmailMicrosoftClientSecret != "" {
						providers = append(providers, email.OAuthProviderConfig{
							ProviderID:   "outlook",
							TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
							ClientID:     cfg.EmailMicrosoftClientID,
							ClientSecret: cfg.EmailMicrosoftClientSecret,
						})
					}
					emailScheduler.SetOAuthRefresher(oauthRefresher, providers)
					// 时区：默认 UTC+8（中国大陆）；可由 POCKET_TIMEZONE_OFFSET_SEC 覆盖。
					emailScheduler.SetTimezoneOffset(cfg.TimezoneOffsetSec)
					emailScheduler.Start(context.Background())
					defer emailScheduler.Stop()
					log.Printf("Email scheduler started (fetch_enabled=%v, kxmemory=%v, tz_offset=%ds, oauth_providers=%d)",
						cfg.EmailFetchEnabled, kxmem != nil, cfg.TimezoneOffsetSec, len(providers))
				}
			}
		}
	}

	// ---- STT cloud fallback (Groq Whisper Large v3 Turbo) ----
	var transcriber *stt.Transcriber
	if cfg.GroqAPIKey != "" {
		transcriber = stt.NewTranscriber(cfg.GroqAPIKey, "", "")
		log.Println("STT cloud fallback enabled (Groq Whisper Large v3 Turbo)")
	} else {
		log.Println("WARNING: POCKET_GROQ_API_KEY not set; STT cloud fallback disabled")
	}

	// ---- ACC task integration (Phase 5; construct now if configured) ----
	// mcp.Client was previously dead code; here we instantiate it when the
	// ACC MCP endpoint is configured so task handlers can fetch ACC tasks.
	var mcpClient *mcp.Client
	if cfg.MCPBaseURL != "" {
		mcpClient = mcp.NewClient(cfg.MCPBaseURL, cfg.MCPAPIKey, cfg.MCPInsecureTLS)
		log.Printf("ACC MCP client configured: %s", cfg.MCPBaseURL)
	}

	// ---- Phase C: 无状态 AI 网关（嵌入/LLM 代理，不存用户数据）----
	// 优先级：如果配置了 llm-gateway-go，优先代理到企业网关（享受流量治理）
	var embedder aigate.Embedder
	var llm aigate.LLMClient

	if cfg.LLMGatewayURL != "" && cfg.LLMGatewayAPIKey != "" {
		// 企业网关模式：代理到 llm-gateway-go（统一流量治理/审计/限流）
		gwClient := llmgateway.NewClient(cfg.LLMGatewayURL, cfg.LLMGatewayAPIKey)
		embedder = &llmGatewayEmbedderAdapter{gwClient, cfg.EmbedModel}
		llm = &llmGatewayLLMAdapter{gwClient}
		log.Printf("LLM/Embed gateway enabled (enterprise): %s", cfg.LLMGatewayURL)
	} else {
		// 直连模式：直接转发 OpenAI/Groq（Phase C 默认）
		if cfg.EmbedAPIKey != "" {
			embedder = aigate.NewEmbedder(cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
			log.Printf("Embed gateway enabled (direct): model=%s", cfg.EmbedModel)
		} else {
			log.Println("WARNING: POCKET_EMBED_API_KEY not set; /api/embed disabled")
		}
		if cfg.LLMAPIKey != "" {
			llm = aigate.NewLLM(cfg.LLMBaseURL, cfg.LLMAPIKey)
			log.Printf("LLM gateway enabled (direct): base=%s model=%s", cfg.LLMBaseURL, cfg.LLMModel)
		} else {
			log.Println("WARNING: POCKET_LLM_API_KEY not set; /api/llm/chat disabled")
		}
	}

	// ---- 后端集成: kxmemory AI 编排服务（分类/SSOT/总结）----
	// 注意：kxmem 已在 email scheduler block 之前构造（被 DailySummary 使用）。
	// 这里只做防御性检查：如果用户跳过了前面 block（比如未来重构），避免重复构造。

	// ---- Adapters (unchanged) ----
	var npsAdapter adapter.NPSAdapter
	if cfg.NPSAuthKey != "" {
		log.Printf("Using NPS Web API adapter: %s", cfg.NPSBaseURL)
		npsAdapter = adapter.NewNPSWebAPIAdapter(cfg.NPSBaseURL, cfg.NPSAuthKey)
	} else {
		log.Println("Using static NPS adapter (demo mode)")
		npsAdapter = adapter.NewStaticNPSAdapter()
	}

	var timeoutMS int
	timeoutMS, _ = strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}
	log.Printf("Using OpenCode HTTP adapter (timeout: %dms)", timeoutMS)
	opencodeAdapter := adapter.NewOpenCodeHTTPAdapter(timeoutMS)
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)

	reg := registry.NewRegistry()
	if cfg.OpenCodeInstancesJSON != "" {
		configs, err := registry.ParseConfigJSON(cfg.OpenCodeInstancesJSON)
		if err != nil {
			log.Printf("Warning: Failed to parse OpenCode instances config: %v", err)
		} else {
			if err := reg.LoadFromConfig(configs); err != nil {
				log.Printf("Warning: Failed to load instances from config: %v", err)
			} else {
				log.Printf("Loaded %d OpenCode instances from config", len(configs))
			}
		}
	}

	// 启用自动发现：扫描 localhost + LAN 端口发现 OpenCode 实例（60s 间隔）
	// 会话迁移方案：支持完整 /24 扫描、自定义端口、额外主机（ACC/NPS 注入）
	discoveryFunc := registry.NetworkDiscovery(
		registry.WithFullSubnetScan(cfg.DiscoveryFullSubnet),
		registry.WithPorts(cfg.DiscoveryPorts),
		registry.WithExtraHosts(cfg.DiscoveryExtraHosts),
	)
	if cfg.DiscoveryFullSubnet {
		log.Printf("⚠️ 启用完整 /24 子网扫描（生产慎用，开销较大）")
	}
	reg.EnableAutoDiscovery(discoveryFunc, 60*time.Second)
	go reg.StartAutoDiscovery(context.Background())

	srv := server.New(cfg, npsAdapter, opencodeAdapter, taskStore, reg, configAdapter,
		notesStore, emailStore, vaultStore, transcriber, mcpClient, embedder, llm, kxmem, nil, /* opencodeManager (set below) */
		userStore, jwtSigner,
		emailCrypto, emailPending,
		emailScheduler, emailFetcher,
		dataDir)

	// 把 server 的 WS hub 反向注入 email scheduler，让 OAuth revocation
	// 事件能精确投递给当前用户（email.oauth.revoked）。ws.Hub 已经实现
	// OAuthBroadcaster 接口。
	if emailScheduler != nil {
		emailScheduler.SetBroadcaster(srv.WSHub())
	}

	// ---- LLM Gateway 配置持久化（PG 可用时从数据库加载）----
	if pool != nil {
		lgStore, err := server.NewLLMGatewayStore(pool)
		if err != nil {
			log.Printf("WARN: LLM gateway store init failed: %v", err)
		} else {
			srv.SetLLMGatewayStore(lgStore)
			srv.LoadLLMGatewayFromDB()
			log.Println("LLM gateway config persistence enabled (PG)")
		}
	}

	// ---- S0-A: Identity Core (workspaces / members / devices) ----
	if pool != nil {
		identStore, err := identity.New(pool)
		if err != nil {
			log.Printf("WARN: identity store init failed: %v", err)
		} else {
			srv.SetIdentityStore(identStore)
			log.Println("Identity Core enabled (workspaces/members/devices)")
		}
	}

	// ---- S0-B: Unified LLM BFF (stream + usage tracking) ----
	// 仅在企业网关模式下启用：BFF 需要一个支持 stream 的 Provider，目前只有
	// llmgateway.Client 满足。直连模式（aigate）的 BFF 适配器留到后续 sprint。
	if cfg.LLMGatewayURL != "" && cfg.LLMGatewayAPIKey != "" {
		gwClientForBFF := llmgateway.NewClient(cfg.LLMGatewayURL, cfg.LLMGatewayAPIKey)
		provider := server.NewLLMGatewayBFFProvider(gwClientForBFF)
		var recorder llmbff.Recorder = llmbff.NoopRecorder{}
		var summarizer llmbff.Summarizer
		if pool != nil {
			if usageStore, err := llmbff.NewUsageStore(pool); err != nil {
				log.Printf("WARN: llm usage store init failed: %v", err)
			} else {
				recorder = usageStore
				summarizer = usageStore
			}
		}
		srv.SetLLMBFF(llmbff.NewService(provider, recorder), summarizer)
		log.Println("LLM BFF enabled (stream + usage tracking)")
	}

	// ---- S0-C: Lobster Vault 加密镜像同步 ----
	if pool != nil {
		if ls, err := lobster.NewSyncStore(pool); err != nil {
			log.Printf("WARN: lobster sync store init failed: %v", err)
		} else {
			srv.SetLobsterSync(ls)
			log.Println("Lobster Vault sync enabled (e2ee asset mirror)")
		}
	}

	// ---- S0-D: Agent Bridge（统一远端 opencode 实例为 Agent 抽象）----
	if pool != nil {
		if abStore, err := agentbridge.New(pool); err != nil {
			log.Printf("WARN: agent bridge store init failed: %v", err)
		} else {
			creator, resolver, attacher := server.NewAgentBridgeAdapters(opencodeAdapter, reg, taskStore)
			bridge := agentbridge.NewBridge(abStore, creator, resolver, attacher)
			srv.SetAgentBridge(bridge, abStore)
			log.Println("Agent Bridge enabled (unified agent dispatch)")
		}
	}

	// ---- S0-E: Notification Center（inbox + rules + 前台 WS 推送）----
	if pool != nil {
		if ncStore, err := notifycenter.New(pool); err != nil {
			log.Printf("WARN: notify center store init failed: %v", err)
		} else {
			// 前台 WS sender 复用现有 wsHub；后台 APNs/FCM 留 Noop（部署期接证书）。
			wsSender := notifycenter.NewWebsocketSender(srv.WSHub())
			svc := notifycenter.NewService(ncStore, wsSender)
			srv.SetNotifyCenter(svc, ncStore)
			log.Println("Notification Center enabled (inbox + rules + WS foreground push)")
		}
	}

	// ---- OpenCode 域管理器装配（Phase V3: 真实任务与会话接入）----
	// 在 server.New 之后再装配，因为 manager 持有 opencodeAdapter/registry 引用。
	// noopHistoryStore 是 HistoryStore 的零开销实现——真实持久化交给 OpenCode 自身（server-side SQLite）。
	ocMgr := opencode.NewManager(reg, opencodeAdapter, noopHistoryStore{})
	eventMgr := opencode.NewEventStreamManager(reg, opencodeAdapter)
	permMgr := opencode.NewPermissionManager(reg, opencodeAdapter, opencode.PermissionManagerOptions{PollInterval: 3 * time.Second}, eventMgr) // Phase 1.2: 传入 eventStream
	quesMgr := opencode.NewQuestionManager(reg, opencodeAdapter, opencode.QuestionManagerOptions{PollInterval: 3 * time.Second}, eventMgr)     // Phase 1.3: 传入 eventStream

	// 启动后台循环
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	defer mgrCancel()

	// 让管理器在主进程退出时关闭（defer 调用，但 goroutine 在 mgrCancel 之后才退出）
	defer eventMgr.Close()
	defer permMgr.Close()
	defer quesMgr.Close()

	// 把 OpenCode 上游事件回灌给 ocMgr，驱动 active/idle 推断
	go func() {
		instances := reg.ListInstances()
		for _, inst := range instances {
			instanceID := inst.ID
			ctx, cancel := context.WithCancel(mgrCtx)
			defer cancel()
			ch, cleanup, err := eventMgr.Subscribe(ctx, opencode.SubscribeOptions{InstanceID: instanceID, BufferSize: 128})
			if err != nil {
				log.Printf("warn: subscribe events for %s failed: %v", instanceID, err)
				continue
			}
			defer cleanup()
			go func(c <-chan opencode.DomainEvent, iid string) {
				for evt := range c {
					if evt.SessionID != "" {
						ocMgr.OnSessionEvent(evt.SessionID, evt.Type)
					}
				}
			}(ch, instanceID)
		}
	}()

	// 每 30s 刷新一次 idle/active（兜底：长时间无事件则视为 idle）
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-mgrCtx.Done():
				return
			case <-t.C:
				ocMgr.RefreshStatuses(5 * time.Minute)
			}
		}
	}()

	log.Printf("OpenCode domain managers wired: eventMgr + permMgr + quesMgr + ocMgr (refreshStatus 30s)")

	// 把 manager 注入 server（用 setter 而非扩展 New，避免 26+ 参数）
	srv.SetOpenCodeManagers(ocMgr, eventMgr, permMgr, quesMgr)

	// ---- W5: 注入 ACP agent registry ----
	// 当前 stage：
	//   1. OpenCode HTTP adapter（向后兼容）
	//   2. ACP stdio adapter（新增，支持 Codex/Claude Code 等 CLI agents）
	agentReg := agent.NewRegistry()
	
	// 1. 注册 OpenCode HTTP adapter
	opencodeAgentAdapter := agent.NewOpenCodeAdapter(opencodeAdapter)
	_ = agentReg.Register(agent.AgentRef{Type: "opencode", Target: ""}, opencodeAgentAdapter)
	// 用 instanceMap 把 instance_id 也映射到 opencode adapter（兼容旧 query）
	for _, ref := range reg.ListInstances() {
		apiBase, err := reg.GetInstanceAPIBase(ref.ID)
		if err != nil {
			continue
		}
		_ = agentReg.Register(
			agent.AgentRef{Type: "opencode", Target: apiBase},
			opencodeAgentAdapter,
			ref.ID,
		)
	}
	
	// 2. 注册 ACP stdio adapter（新增）
	acpStdioAdapter := agent.NewACPStdioAdapter()
	// 示例：注册 agent_echo（测试用）
	if agentEchoPath := os.Getenv("AGENT_ECHO_PATH"); agentEchoPath != "" {
		_ = agentReg.Register(
			agent.AgentRef{Type: "acp-stdio", Target: agentEchoPath},
			acpStdioAdapter,
			"agent-echo-1", // instance_id for legacy query
		)
		log.Printf("Registered ACP stdio agent: agent_echo at %s", agentEchoPath)
	}
	// 示例：注册 Claude CLI（如果存在）
	if claudePath := os.Getenv("CLAUDE_CLI_PATH"); claudePath != "" {
		_ = agentReg.Register(
			agent.AgentRef{Type: "acp-stdio", Target: claudePath},
			acpStdioAdapter,
			"claude-1", // instance_id
		)
		log.Printf("Registered ACP stdio agent: Claude CLI at %s", claudePath)
	}
	
	srv.SetAgentRegistry(agentReg)
	log.Printf("ACP agent registry wired: %d adapter(s)", len(agentReg.All()))

	// 会话迁移方案：装配跨主机迁移服务（registry + opencodeAdapter + pluginHub + taskStore）。
	// 任一依赖为 nil 时迁移服务内部降级。taskStore 可能为 nil（remote-only 模式），
	// 迁移服务此时跳过逻辑会话映射，但仍可下发命令。
	migrationSvc := migration.New(reg, opencodeAdapter, srv.PluginHub(), taskStore)
	srv.SetMigrationService(migrationSvc)
	log.Println("会话迁移服务已装配（/api/migration, /api/migration/preview）")

	// Phase 5: 启动 ACC 任务后台同步（5 分钟一次把 ACC 任务拉取到本地）
	taskScheduler := tasksync.New(mcpClient, taskStore, 5*60*1_000_000_000) // 5min
	taskScheduler.Start(context.Background())
	defer taskScheduler.Stop()

	// HTTP server 配置超时，防止 Slowloris 攻击和资源耗尽
	addr := ":" + cfg.HTTPPort
	server := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,  // 读取请求头超时
		ReadTimeout:       30 * time.Second,  // 读取整个请求超时
		WriteTimeout:      30 * time.Second,  // 写响应超时（注意：对 SSE/WebSocket 长连接需特殊处理）
		IdleTimeout:       120 * time.Second, // Keep-Alive 连接空闲超时
	}
	log.Printf("pocketd listening on %s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ---- llm-gateway 适配器（把 llm-gateway OpenAI 兼容协议适配到 aigate 接口）----

// noopHistoryStore 实现 opencode.HistoryStore 的零开销空实现。
// 当前 Pocket 不在本地持久化 OpenCode 会话历史——由 OpenCode 自身（~/.local/share/opencode/db.sqlite）持久化，
// Pocket 只代理视图。如未来需要本地副本，替换为 PG 实现即可。
type noopHistoryStore struct{}

func (noopHistoryStore) SaveEvent(ctx context.Context, sessionID string, event *opencode.HistoryEvent) error {
	return nil
}

func (noopHistoryStore) GetHistory(ctx context.Context, sessionID string, limit int) ([]*opencode.HistoryEvent, error) {
	return nil, nil
}

// llmGatewayEmbedderAdapter 把 llmgateway.Client 适配为 aigate.Embedder
type llmGatewayEmbedderAdapter struct {
	client *llmgateway.Client
	model  string
}

func (a *llmGatewayEmbedderAdapter) Embed(ctx context.Context, text string) ([]float32, string, error) {
	resp, err := a.client.Embed(ctx, llmgateway.EmbeddingRequest{
		Model: a.model,
		Input: text,
	})
	if err != nil {
		return nil, "", err
	}
	if len(resp.Data) == 0 {
		return nil, "", fmt.Errorf("empty embedding response")
	}
	return resp.Data[0].Embedding, resp.Model, nil
}

// llmGatewayLLMAdapter 把 llmgateway.Client 适配为 aigate.LLMClient
type llmGatewayLLMAdapter struct {
	client *llmgateway.Client
}

func (a *llmGatewayLLMAdapter) Chat(ctx context.Context, model string, messages []aigate.ChatMessage) (string, error) {
	gwMessages := make([]llmgateway.ChatMessage, len(messages))
	for i, m := range messages {
		gwMessages[i] = llmgateway.ChatMessage{Role: m.Role, Content: m.Content}
	}
	resp, err := a.client.Chat(ctx, llmgateway.ChatRequest{
		Model:    model,
		Messages: gwMessages,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty llm response")
	}
	return resp.Choices[0].Message.Content, nil
}
