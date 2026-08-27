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

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
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
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
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
func defaultLLMGatewayState() llmGatewayState {
	return llmGatewayState{
		BaseURL: envOr("POCKET_LLM_GATEWAY_URL", opencode.DefaultLLMGatewayBaseURL),
		APIKey:  os.Getenv("POCKET_LLM_GATEWAY_API_KEY"),
		Models:  []string{},
		Format:  defaultGatewayFormat,
		PreferredModels: []string{},
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
		if err := validateOutboundURL(req.BaseURL); err != nil {
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
		if err := s.pushConfigToOpenCode(r, workspaceID, current); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
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

// pushConfigToOpenCode uses the authenticated Pocket config contract. A
// missing token is an explicit failure when instances are configured.
//
// All HTTP calls share a single request context with a hard 10s deadline
// and reuse safeOutboundHTTPClient so untrusted instance URLs go through
// the same SSRF defenses as /api/llm-gateway/test.
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
	cfg := adapter.ModelConfig{
		DefaultProvider: "openai-compatible-pocket",
		Providers: []adapter.Provider{{
			ID: "openai-compatible-pocket", Name: "Pocket LLM Gateway", Enabled: true,
			APIKey: st.APIKey, BaseURL: st.BaseURL,
			Models: make([]adapter.ModelDefinition, 0, len(st.Models)),
		}},
	}
	for _, modelID := range st.Models {
		cfg.Providers[0].Models = append(cfg.Providers[0].Models, adapter.ModelDefinition{ID: modelID, DisplayName: modelID, Enabled: true})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := safeOutboundHTTPClient()

	for _, inst := range instances {
		baseURL := strings.TrimRight(inst.APIBaseURL, "/")
		if baseURL == "" {
			return fmt.Errorf("OpenCode instance %s has no API base URL", inst.ID)
		}
		if err := putJSONWithAuth(ctx, client, baseURL+"/api/config/models", cfg, token); err != nil {
			return fmt.Errorf("push config to %s: %w", inst.ID, err)
		}
		if err := postWithAuth(ctx, client, baseURL+"/api/config/reload", token); err != nil {
			return fmt.Errorf("reload config on %s: %w", inst.ID, err)
		}
	}
	return nil
}

func putJSONWithAuth(ctx context.Context, client *http.Client, url string, body interface{}, token string) error {
	data, err := json.Marshal(map[string]interface{}{"config": body})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(string(data)))
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
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func postWithAuth(ctx context.Context, client *http.Client, url, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func maskKey(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
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
			log.Printf("[llm-gateway] load from DB failed for %s: %v", wsID, err)
			continue
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
