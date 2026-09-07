package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// gatewayFormats 是设置页「消息格式」下拉框的可选项（对齐 llm-gateway-go
// 暴露的端点族）。pocketd 客户端当前仅实现 openai-chat；其余值先存储与展示，
// 对话链路适配登记后续（选非默认时前端有提示）。
var gatewayFormats = map[string]bool{
	"openai-chat":        true,
	"anthropic-messages": true,
	"openai-responses":   true,
}

const defaultGatewayFormat = "openai-chat"

func normalizeGatewayFormat(f string) string {
	if f == "" {
		return defaultGatewayFormat
	}
	if gatewayFormats[f] {
		return f
	}
	return defaultGatewayFormat
}

type llmGatewayState struct {
	BaseURL string   `json:"baseURL"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
	// Format：网关调用协议（见 gatewayFormats）。
	Format string `json:"format"`
	// PreferredModels：用户勾选的常用模型；非空时前端模型选择器只展示这些
	// （模型目录过大时降噪），空 = 展示全部。
	PreferredModels []string `json:"preferredModels"`
}

// defaultLLMGatewayState returns the env-backed fallback used when no DB row
// has been persisted for the workspace. We keep it deterministic so GET is
// idempotent across requests.
//
// 2026-08-31: 优先 POCKET_LLM_GATEWAY_URL env；未设置时回落到
// opencode.DefaultLLMGatewayBaseURL（已切换为 https://llm.kxpms.cn/v1）。APIKey
// 同样 env-first，常量 DefaultLLMGatewayAPIKey 仅作为 dev seed 兜底；preferred
// 模型列表来自 opencode.DefaultLLMGatewayPreferredModels。
func defaultLLMGatewayState() llmGatewayState {
	models := append([]string(nil), opencode.DefaultLLMGatewayPreferredModels...)
	preferred := append([]string(nil), opencode.DefaultLLMGatewayPreferredModels...)
	return llmGatewayState{
		BaseURL:         envOr("POCKET_LLM_GATEWAY_URL", opencode.DefaultLLMGatewayBaseURL),
		APIKey:          pickAPIKey(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"), opencode.DefaultLLMGatewayAPIKey),
		Models:          models,
		Format:          defaultGatewayFormat,
		PreferredModels: preferred,
	}
}

// pickAPIKey returns the first non-empty candidate; falls back to the bundled
// dev default so a freshly-bootstrapped instance still has a working gateway
// without forcing operators to set POCKET_LLM_GATEWAY_API_KEY in env.
func pickAPIKey(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// EnsureLLMGatewayDefaults seeds a default config for any workspace that has no
// active row yet. Idempotent: existing active rows are skipped, returning
// (already, false). The cache is updated in-place so a subsequent LoadConfig
// never re-reads a stale row.
//
// This runs once during boot from cmd/pocketd/main.go before LoadLLMGatewayFromDB
// to guarantee that dev / reset instances receive the bundled defaults without
// forcing every operator to ship their own env.
func (s *Server) EnsureLLMGatewayDefaults(workspaceIDs ...string) {
	if s.llmGWStore == nil {
		return
	}
	def := defaultLLMGatewayState()
	seen := make(map[string]struct{}, len(workspaceIDs))
	for _, wsID := range workspaceIDs {
		wsID = strings.TrimSpace(wsID)
		if wsID == "" {
			wsID = "default"
		}
		if _, dup := seen[wsID]; dup {
			continue
		}
		seen[wsID] = struct{}{}

		existing, err := s.llmGWStore.LoadConfig(context.Background(), wsID)
		if err != nil {
			// 密文不可解（如 JWT secret 轮换后 cipher 校验失败）或行损坏：
			// 用 env 默认配置覆写毒化行，而不是永远跳过——否则每次启动都
			// 解密失败并静默回退，租户配置再也救不回来。
			log.Printf("[llm-gateway] default-seed LoadConfig failed for %s: %v; self-healing with env defaults", wsID, err)
			if saveErr := s.llmGWStore.SaveConfig(context.Background(), wsID, def); saveErr != nil {
				log.Printf("[llm-gateway] default-seed self-heal SaveConfig failed for %s: %v", wsID, saveErr)
			}
			continue
		}
		if existing != nil {
			continue // 已有 row，幂等跳过
		}

		if err := s.llmGWStore.SaveConfig(context.Background(), wsID, def); err != nil {
			log.Printf("[llm-gateway] default-seed SaveConfig failed for %s: %v", wsID, err)
			continue
		}
		log.Printf("[llm-gateway] default-seed inserted for workspace=%s baseURL=%s preferred=%d", wsID, def.BaseURL, len(def.PreferredModels))
	}
}

// llmGatewayCache holds the per-workspace gateway state. The map is keyed by
// workspace id; the special key "default" is reserved for callers that have
// not been scoped (e.g. system-level probes). All operations are guarded by
// mu so concurrent POST/GET do not race on the underlying structs.
type llmGatewayCache struct {
	mu    sync.RWMutex
	state map[string]*llmGatewayState
}

func newLLMGatewayCache() *llmGatewayCache {
	return &llmGatewayCache{state: map[string]*llmGatewayState{}}
}

func (c *llmGatewayCache) get(workspaceID string) llmGatewayState {
	if workspaceID == "" {
		workspaceID = "default"
	}
	c.mu.RLock()
	st, ok := c.state[workspaceID]
	c.mu.RUnlock()
	if ok {
		c.mu.RLock()
		copy := *st
		copy.Models = append([]string(nil), st.Models...)
		copy.PreferredModels = append([]string(nil), st.PreferredModels...)
		c.mu.RUnlock()
		return copy
	}
	return defaultLLMGatewayState()
}

// has reports whether an explicit (env-loaded or POST /config persisted)
// entry exists for the workspace. Unlike get(), it does NOT fall back to
// defaultLLMGatewayState — the default carries a hardcoded BaseURL, which
// would make "unconfigured" indistinguishable from "configured".
func (c *llmGatewayCache) has(workspaceID string) bool {
	if workspaceID == "" {
		workspaceID = "default"
	}
	c.mu.RLock()
	_, ok := c.state[workspaceID]
	c.mu.RUnlock()
	return ok
}

// replace swaps the cached state for a workspace; the caller's struct is
// copied so subsequent caller-side mutations cannot leak into the cache.
func (c *llmGatewayCache) replace(workspaceID string, st llmGatewayState) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	snapshot := st
	snapshot.Models = append([]string(nil), st.Models...)
	c.mu.Lock()
	c.state[workspaceID] = &snapshot
	c.mu.Unlock()
}

// updateModels merges new model ids into the cached state (used by the test
// endpoint after a successful list call).
func (c *llmGatewayCache) updateModels(workspaceID string, ids []string) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.state[workspaceID]
	if !ok {
		return
	}
	st.Models = append([]string(nil), ids...)
}

// gatewaySnapshot returns the active state for the request's workspace.
func (s *Server) gatewaySnapshot(workspaceID string) llmGatewayState {
	if s.llmGWCache == nil {
		return defaultLLMGatewayState()
	}
	return s.llmGWCache.get(workspaceID)
}

// ResolveGateway 返回某 workspace 当前生效的网关连接配置（env 默认值与运行时
// /api/llm-gateway/config 配置合并后的结果），供 LLM BFF 在请求时按需构造客户端。
// 返回 GatewayConfig（已剥离内部缓存结构），调用方据此决定是否需要返回 503。
func (s *Server) ResolveGateway(workspaceID string) GatewayConfig {
	st := s.gatewaySnapshot(workspaceID)
	return GatewayConfig{
		BaseURL: st.BaseURL, APIKey: st.APIKey, Models: st.Models,
		Format: normalizeGatewayFormat(st.Format), PreferredModels: st.PreferredModels,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (s *Server) handleLLMGatewayConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.gatewaySnapshot(s.workspaceIDFromRequest(r))
		// PG 往返可能把空 slice 变 null（json.Marshal(nil)="null"），出口统一
		// 兜底为 []——前端模板直接读 models.length，null 会让设置页挂载崩溃
		// （真机 P3 轮实测）。
		if st.Models == nil {
			st.Models = []string{}
		}
		if st.PreferredModels == nil {
			st.PreferredModels = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"baseURL": st.BaseURL, "apiKeySet": st.APIKey != "", "apiKey": maskKey(st.APIKey),
			"models": st.Models, "source": "pocketd",
			"format": normalizeGatewayFormat(st.Format), "preferredModels": st.PreferredModels,
			"formats": []string{"openai-chat", "anthropic-messages", "openai-responses"},
		})
	case http.MethodPost:
		var req struct {
			BaseURL         string   `json:"baseURL"`
			APIKey          string   `json:"apiKey"`
			Models          []string `json:"models"`
			Format          string   `json:"format"`
			PreferredModels []string `json:"preferredModels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.BaseURL == "" {
			http.Error(w, "baseURL required", http.StatusBadRequest)
			return
		}
		if err := validateGatewayURL(req.BaseURL); err != nil {
			http.Error(w, "invalid baseURL: "+err.Error(), http.StatusBadRequest)
			return
		}

		workspaceID := s.workspaceIDFromRequest(r)
		current := s.gatewaySnapshot(workspaceID)
		if req.APIKey != "" {
			current.APIKey = req.APIKey
		}
		if current.APIKey == "" {
			http.Error(w, "apiKey required for first configuration", http.StatusBadRequest)
			return
		}
		current.BaseURL = req.BaseURL
		if req.Models != nil {
			current.Models = append([]string(nil), req.Models...)
		}
		current.Format = normalizeGatewayFormat(req.Format)
		if req.PreferredModels != nil {
			current.PreferredModels = append([]string(nil), req.PreferredModels...)
		}
		if current.Models == nil {
			current.Models = []string{}
		}
		if current.PreferredModels == nil {
			current.PreferredModels = []string{}
		}

		// Order matters: persist first, then publish. If SaveConfig or
		// pushConfigToOpenCode fails we keep the previously cached state so
		// concurrent GET callers still observe a consistent config until the
		// client retries. Returning an error here is the right signal to the
		// UI; silently rolling back to the previous in-memory config is what
		// we already do.
		if s.llmGWStore != nil {
			if err := s.llmGWStore.SaveConfig(r.Context(), workspaceID, current); err != nil {
				log.Printf("[llm-gateway] persist config failed: %v", err)
				http.Error(w, "persist config failed", http.StatusInternalServerError)
				return
			}
		}
		// push 到上游 OpenCode 实例是尽力而为的下游同步：配置本身已持久化，
		// push 失败（如未配 POCKET_OPENCODE_CONFIG_TOKEN、实例离线）不应让
		// 保存请求整体失败——否则设置页永远报错且缓存不刷新（2026-09-07 E2E）。
		if err := s.pushConfigToOpenCode(r, workspaceID, current); err != nil {
			log.Printf("[llm-gateway] push config to opencode instances failed (non-fatal): %v", err)
		}
		if s.llmGWCache != nil {
			s.llmGWCache.replace(workspaceID, current)
		}
		// 审计：baseURL/apiKey 配置变更。detail 只暴露 baseURL host 与
		// 是否携带 apiKey，绝不写 apiKey 原文（也绝不走 redact —— 这里
		// 就不让它进入 detail 字符串）。
		apiKeySet := req.APIKey != ""
		s.WriteWithClaims(&authClaims{UserID: claimsUserFromContextOrEmpty(r), WorkspaceID: workspaceID},
			"llm_gateway.config.updated", "llm_gateway_config",
			AuditFields{
				Success: true,
				Detail:  fmt.Sprintf("base_url=%s api_key_set=%t models_set=%t", baseURLHostOnly(req.BaseURL), apiKeySet, req.Models != nil),
			})
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "baseURL": current.BaseURL, "models": current.Models})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLLMGatewayTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workspaceID := s.workspaceIDFromRequest(r)
	st := s.gatewaySnapshot(workspaceID)
	if st.APIKey == "" {
		http.Error(w, "apiKey not set", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := validateOutboundURL(st.BaseURL); err != nil {
		http.Error(w, "invalid gateway URL", http.StatusBadRequest)
		return
	}
	modelsURL, err := outboundModelsURL(st.BaseURL)
	if err != nil {
		http.Error(w, "invalid gateway URL", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		http.Error(w, "invalid gateway URL", http.StatusBadRequest)
		return
	}
	req.Header.Set("Authorization", "Bearer "+st.APIKey)
	resp, err := safeOutboundHTTPClient().Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMGatewayResponseBytes+1))
	if err != nil || len(body) > maxLLMGatewayResponseBytes {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "invalid gateway response"})
		return
	}
	if resp.StatusCode >= 400 {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "status": resp.StatusCode, "error": "gateway returned an error"})
		return
	}
	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &listResp) == nil && len(listResp.Data) > 0 {
		ids := make([]string, 0, len(listResp.Data))
		for _, model := range listResp.Data {
			ids = append(ids, model.ID)
		}
		if s.llmGWCache != nil {
			s.llmGWCache.updateModels(workspaceID, ids)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": resp.StatusCode, "models": s.gatewaySnapshot(workspaceID).Models})
}

func (s *Server) handleLLMGatewayModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := s.gatewaySnapshot(s.workspaceIDFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]interface{}{"baseURL": st.BaseURL, "models": st.Models})
}

// pushConfigToOpenCode 把工作区网关配置同步到本工作区自有的 OpenCode 实例，
// 走 stock OpenCode 的官方运行时配置契约 PATCH /global/config（merge 语义、
// 立即生效并持久化，见 docs/opencode-contract.md §3.1）。早先对接的
// PUT /api/config/models + POST /api/config/reload 在 stock opencode 上并不
// 存在——其 SPA 兜底对任意未知路径返回 200 text/html，只看状态码会假成功
// （2026-09-07 复核结论，HANDOFF §4.1.1）。
//
// 成功判定：2xx 且 Content-Type 为 application/json 且响应体是合法 JSON 对象。
// A missing token is an explicit failure when instances are configured.
//
// 出站走 gatewayHTTPClient（而非硬禁私网的 safeOutboundHTTPClient）：注册实例
// 本就常驻内网/本机（本地部署即 127.0.0.1:4096），硬禁会让同步功能在任何本地
// 形态下都无法工作。与 fix #3 的 validateGatewayURL 同一开关语义——
// POCKET_LLM_GATEWAY_ALLOW_PRIVATE 显式放行私网/loopback；云元数据端点无论
// 开关与否始终拦截，DNS 重绑定防护保留。
func (s *Server) pushConfigToOpenCode(r *http.Request, workspaceID string, st llmGatewayState) error {
	if s.registry == nil || s.opencode == nil {
		return nil
	}
	// ListInstancesForWorkspace deliberately also returns operator-provisioned
	// shared instances (WorkspaceID == ""). Those must NOT receive per-tenant
	// gateway secrets: a shared instance is visible to every workspace, so each
	// tenant's save would overwrite the same process and leak its APIKey to the
	// others. Only push to instances this workspace actually owns.
	instances := make([]model.PocketInstance, 0)
	for _, inst := range s.registry.ListInstancesForWorkspace(workspaceID) {
		if inst.WorkspaceID == workspaceID && workspaceID != "" {
			instances = append(instances, inst)
		}
	}
	if len(instances) == 0 {
		return nil
	}
	token := strings.TrimSpace(os.Getenv("POCKET_OPENCODE_CONFIG_TOKEN"))
	if token == "" {
		return fmt.Errorf("OpenCode config push requires POCKET_OPENCODE_CONFIG_TOKEN")
	}
	patch := buildPocketProviderPatch(st)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := gatewayHTTPClient(10 * time.Second)

	for _, inst := range instances {
		baseURL := strings.TrimRight(inst.APIBaseURL, "/")
		if baseURL == "" {
			return fmt.Errorf("OpenCode instance %s has no API base URL", inst.ID)
		}
		if err := patchGlobalConfigWithAuth(ctx, client, baseURL+"/global/config", patch, token); err != nil {
			return fmt.Errorf("push config to %s: %w", inst.ID, err)
		}
	}
	return nil
}

// pocketProviderID 是 Pocket 网关在上游实例配置里的 provider 键，与
// internal/opencode.BuildOpenCodeConfigContent 的 seed 保持一致。
const pocketProviderID = "openai-compatible-pocket"

// buildPocketProviderPatch 构造 PATCH /global/config 的合并文档：只提交
// provider 子文档，不触碰实例上的其他配置（含用户自选的默认 model）。
func buildPocketProviderPatch(st llmGatewayState) map[string]interface{} {
	models := make(map[string]interface{}, len(st.Models))
	for _, modelID := range st.Models {
		models[modelID] = map[string]interface{}{"name": modelID}
	}
	return map[string]interface{}{
		"provider": map[string]interface{}{
			pocketProviderID: map[string]interface{}{
				"name": "Pocket LLM Gateway",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": st.BaseURL,
					"apiKey":  st.APIKey,
				},
				"models": models,
			},
		},
	}
}

// patchGlobalConfigWithAuth 提交合并文档并按"真 JSON 契约"校验响应：
// 2xx 且 Content-Type 为 application/json 且响应体可解析为 JSON 对象——
// stock opencode 的 SPA 兜底会对未知路径返回 200 text/html，必须判为失败。
func patchGlobalConfigWithAuth(ctx context.Context, client *http.Client, url string, doc interface{}, token string) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		return fmt.Errorf("upstream responded %s (Content-Type %q, want application/json) — not an OpenCode JSON API endpoint (SPA fallback?)", resp.Status, ct)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return fmt.Errorf("response body is not a JSON object: %w", err)
	}
	return nil
}

// maskKey 返回设置页展示用的掩码：保留 sk- 前缀 + 末 6 位，多把 key 并存时
// 借此识别当前绑定的是哪一把。短 key 露出前后缀会泄漏过多内容，整体打码。
func maskKey(s string) string {
	if len(s) < 12 {
		return "******"
	}
	return s[:3] + "******" + s[len(s)-6:]
}

// LoadLLMGatewayFromDB reloads the per-workspace config for every workspace
// in the supplied list. A workspace with no saved row keeps the env defaults.
// If workspaceIDs is empty we still preload the "default" workspace so the
// single-tenant fallback continues to work.
func (s *Server) LoadLLMGatewayFromDB(workspaceIDs ...string) {
	if s.llmGWStore == nil {
		return
	}
	ids := workspaceIDs
	if len(ids) == 0 {
		ids = []string{"default"}
	}
	for _, wsID := range ids {
		if wsID == "" {
			wsID = "default"
		}
		st, err := s.llmGWStore.LoadConfig(context.Background(), wsID)
		if err != nil {
			// 与 EnsureLLMGatewayDefaults 同理：不可解密/损坏的行自愈为
			// env 默认配置，避免每次启动重复解密失败且无法恢复。
			log.Printf("[llm-gateway] load from DB failed for %s: %v; self-healing with env defaults", wsID, err)
			def := defaultLLMGatewayState()
			if saveErr := s.llmGWStore.SaveConfig(context.Background(), wsID, def); saveErr != nil {
				log.Printf("[llm-gateway] self-heal SaveConfig failed for %s: %v", wsID, saveErr)
				continue
			}
			st = &def
		}
		if st == nil {
			continue
		}
		if s.llmGWCache == nil {
			s.llmGWCache = newLLMGatewayCache()
		}
		s.llmGWCache.replace(wsID, *st)
		log.Printf("[llm-gateway] loaded config from DB: workspace=%s baseURL=%s models=%d", wsID, st.BaseURL, len(st.Models))
	}
}
