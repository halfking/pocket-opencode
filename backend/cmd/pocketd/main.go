package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/db"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
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
		taskStore  *task.Store  // nil-safe: nil when pool is nil
		notesStore *notes.Store
		emailStore *email.Store
		vaultStore *vault.Store
	)
	if pool != nil {
		ts, err := task.NewStore(pool)
		if err != nil { log.Fatalf("task store: %v", err) }
		taskStore = ts
		ns, err := notes.NewStore(pool)
		if err != nil { log.Fatalf("notes store: %v", err) }
		notesStore = ns
		es, err := email.NewStore(pool)
		if err != nil { log.Fatalf("email store: %v", err) }
		emailStore = es
		vs, err := vault.NewStore(pool)
		if err != nil { log.Fatalf("vault store: %v", err) }
		vaultStore = vs
		log.Println("Module stores initialized (PG)")
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
	var kxmem *kxmemory.Client
	if cfg.KxMemoryBaseURL != "" {
		kxmem = kxmemory.NewClient(cfg.KxMemoryBaseURL, cfg.JWTSecret)
		log.Printf("kxmemory AI orchestrator enabled: %s", cfg.KxMemoryBaseURL)
	} else {
		log.Println("INFO: POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled")
	}

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

	srv := server.New(cfg, npsAdapter, opencodeAdapter, taskStore, reg, configAdapter,
		notesStore, emailStore, vaultStore, transcriber, mcpClient, embedder, llm, kxmem, nil /* opencodeManager, TODO: construct */)

	// Phase 5: 启动 ACC 任务后台同步（5 分钟一次把 ACC 任务拉取到本地）
	taskScheduler := tasksync.New(mcpClient, taskStore, 5*60*1_000_000_000) // 5min
	taskScheduler.Start(context.Background())
	defer taskScheduler.Stop()

	addr := ":" + cfg.HTTPPort
	log.Printf("pocketd listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

// ---- llm-gateway 适配器（把 llm-gateway OpenAI 兼容协议适配到 aigate 接口）----

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
