package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// llmGatewayState 全局 LLM Gateway 配置（单实例）。
// 生产环境可改为 PG 持久化 + 多租户隔离；当前阶段按"单 admin"足够。
type llmGatewayState struct {
	BaseURL string   `json:"baseURL"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
}

var currentLLMGateway = llmGatewayState{
	BaseURL: envOr("POCKET_LLM_GATEWAY_URL", opencode.DefaultLLMGatewayBaseURL),
	APIKey:  os.Getenv("POCKET_LLM_GATEWAY_KEY"),
	Models:  []string{},
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// handleLLMGatewayConfig GET 读 / POST 写 LLM Gateway 配置
// GET  /api/llm-gateway/config
// POST /api/llm-gateway/config  body: { baseURL, apiKey?, models? }
func (s *Server) handleLLMGatewayConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 安全：API Key 仅返回掩码（脱敏）
		masked := maskKey(currentLLMGateway.APIKey)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"baseURL":   currentLLMGateway.BaseURL,
			"apiKeySet": currentLLMGateway.APIKey != "",
			"apiKey":    masked,
			"models":    currentLLMGateway.Models,
			"source":    "pocketd",
		})
	case http.MethodPost:
		var req struct {
			BaseURL string   `json:"baseURL"`
			APIKey  string   `json:"apiKey"`
			Models  []string `json:"models"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.BaseURL == "" {
			http.Error(w, "baseURL required", http.StatusBadRequest)
			return
		}
		// apiKey 留空表示保留旧值
		if req.APIKey != "" {
			currentLLMGateway.APIKey = req.APIKey
		}
		currentLLMGateway.BaseURL = req.BaseURL
		if req.Models != nil {
			currentLLMGateway.Models = req.Models
		}
		// 持久化到 PostgreSQL（如果配置了）
		if s.llmGWStore != nil {
			if err := s.llmGWStore.SaveConfig(r.Context(), currentLLMGateway); err != nil {
				log.Printf("[llm-gateway] persist config failed: %v", err)
				// 不阻断主流程，仅记日志
			}
		}
		// 触发 OpenCode 配置热更新（如果可达）
		go s.pushConfigToOpenCode()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"baseURL": currentLLMGateway.BaseURL,
			"models":  currentLLMGateway.Models,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLLMGatewayTest POST /api/llm-gateway/test
// 用 gateway baseURL 发一次 dry-run models 列表请求验证连通
func (s *Server) handleLLMGatewayTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if currentLLMGateway.APIKey == "" {
		http.Error(w, "apiKey not set", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	url := currentLLMGateway.BaseURL + "/models"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+currentLLMGateway.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"ok":       false,
			"status":   resp.StatusCode,
			"response": string(body),
		})
		return
	}
	// 解析响应，更新 models 列表
	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listResp); err == nil && len(listResp.Data) > 0 {
		ids := make([]string, 0, len(listResp.Data))
		for _, m := range listResp.Data {
			ids = append(ids, m.ID)
		}
		currentLLMGateway.Models = ids
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"status": resp.StatusCode,
		"models": currentLLMGateway.Models,
	})
}

// handleLLMGatewayModels GET /api/llm-gateway/models
// 返回当前缓存的可用模型列表
func (s *Server) handleLLMGatewayModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"baseURL": currentLLMGateway.BaseURL,
		"models":  currentLLMGateway.Models,
	})
}

// pushConfigToOpenCode 异步把 gateway config 推到所有 OpenCode 实例。
// V1 协议：PUT /config/providers（如果实现）或 reload + 写 ~/.config/opencode/config.json
// V2 协议：尚无对应；先写文件再 reload。
func (s *Server) pushConfigToOpenCode() {
	if s.registry == nil || s.opencode == nil {
		return
	}
	json, err := opencode.BuildOpenCodeConfigContent(opencode.LLMGatewayConfig{
		BaseURL: currentLLMGateway.BaseURL,
		APIKey:  currentLLMGateway.APIKey,
		Models:  currentLLMGateway.Models,
	}, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pushConfigToOpenCode: build config: %v\n", err)
		return
	}

	instances := s.registry.ListInstances()
	for _, inst := range instances {
		baseURL, err := s.registry.GetInstanceAPIBase(inst.ID)
		if err != nil {
			continue
		}
		// V1 端点：POST /config/reload 后 PUT /config/providers
		// 先尝试 reload（若上游支持，注入新 config 后触发）
		reloadURL := baseURL + "/config/reload"
		req, _ := http.NewRequest(http.MethodPost, reloadURL, bytes.NewBufferString(json))
		req.Header.Set("Content-Type", "application/json")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pushConfigToOpenCode[%s]: %v\n", inst.ID, err)
			continue
		}
		resp.Body.Close()
		_ = resp
	}
}

// maskKey 把 sk-... 密钥中间掩码。保留前 4 + 后 4 字符，中间用 **** 替换。
func maskKey(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// LoadLLMGatewayFromDB 从数据库加载最新的 LLM Gateway 配置到内存。
// 在 main.go 中 server 创建后、HTTP 启动前调用。
func (s *Server) LoadLLMGatewayFromDB() {
	if s.llmGWStore == nil {
		return
	}
	st, err := s.llmGWStore.LoadConfig(context.Background())
	if err != nil {
		log.Printf("[llm-gateway] load from DB failed: %v", err)
		return
	}
	if st == nil {
		log.Println("[llm-gateway] no saved config in DB, using env defaults")
		return
	}
	currentLLMGateway.BaseURL = st.BaseURL
	if st.APIKey != "" {
		currentLLMGateway.APIKey = st.APIKey
	}
	if len(st.Models) > 0 {
		currentLLMGateway.Models = st.Models
	}
	log.Printf("[llm-gateway] loaded config from DB: baseURL=%s models=%d", st.BaseURL, len(st.Models))
}