package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
)

// maxConfigResponseBytes 限制上游配置响应体的读取上限，防止异常上游把内存打爆。
const maxConfigResponseBytes = 4 << 20

// ModelConfig 模型配置
type ModelConfig struct {
	Providers       []Provider `json:"providers"`
	DefaultProvider string     `json:"defaultProvider,omitempty"`
	Timeout         int        `json:"timeout,omitempty"`
}

// Provider 模型提供商
type Provider struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	APIKey   string            `json:"apiKey,omitempty"`
	BaseURL  string            `json:"baseURL,omitempty"`
	Models   []ModelDefinition `json:"models"`
	Priority int               `json:"priority,omitempty"`
}

// ModelDefinition 模型定义
type ModelDefinition struct {
	ID            string        `json:"id"`
	DisplayName   string        `json:"displayName"`
	Enabled       bool          `json:"enabled"`
	MaxTokens     int           `json:"maxTokens,omitempty"`
	Temperature   float64       `json:"temperature,omitempty"`
	ContextWindow int           `json:"contextWindow,omitempty"`
	Pricing       *ModelPricing `json:"pricing,omitempty"`
}

// ModelPricing 模型价格
type ModelPricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// OpenCodeConfigAdapter OpenCode 配置适配器接口
type OpenCodeConfigAdapter interface {
	GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error)
	UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error
	ReloadConfig(ctx context.Context, instanceBaseURL string) error
	TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error
}

// OpenCodeConfigHTTPAdapter HTTP 配置适配器。
//
// 2026-09-07 起走 stock OpenCode 的官方运行时配置契约（docs/opencode-contract.md
// §3.1）：GET/PATCH /global/config（global.config.get / global.config.update）。
// 早先对接的 PUT /api/config/models + POST /api/config/reload 在 stock opencode
// 上并不存在——其 SPA 兜底对任意未知路径返回 200 text/html，只校验状态码的旧实现
// 会产生"假成功"。本文件所有请求的成功判定统一为：2xx 且 Content-Type 为
// application/json 且响应体是合法 JSON 对象（见 decodeConfigResponse）。
//
// PATCH /global/config 是深度合并语义且立即生效（写入即持久化）：
//   - UpdateModelConfig 只合并 provider 子文档，不触碰实例上的其他配置；
//   - 因此不再需要独立的 reload 步骤，ReloadConfig 以回读校验代替；
//   - 合并不会删除旧 key：上游缩减模型列表时会残留已下线的模型条目
//     （可接受的取舍——整篇覆盖会连带清掉用户手工配置的其他 provider）。
type OpenCodeConfigHTTPAdapter struct {
	client  *http.Client
	timeout time.Duration
}

// NewOpenCodeConfigHTTPAdapter 创建配置适配器
func NewOpenCodeConfigHTTPAdapter(timeoutMS int) *OpenCodeConfigHTTPAdapter {
	return &OpenCodeConfigHTTPAdapter{
		client:  &http.Client{},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// instanceAuthToken 返回访问上游 OpenCode 实例用的 Bearer token。实例开启了
// 鉴权时由 POCKET_OPENCODE_CONFIG_TOKEN 提供凭据；未配置则不带 Authorization
// 头（与未开鉴权的本地实例保持兼容）。
func instanceAuthToken() string {
	return strings.TrimSpace(os.Getenv("POCKET_OPENCODE_CONFIG_TOKEN"))
}

// doConfigRequest 发送请求并按"真 JSON 契约"校验响应。
// body 为 nil 时不携带请求体。返回解码后的 JSON 对象。
func (a *OpenCodeConfigHTTPAdapter) doConfigRequest(ctx context.Context, method, url string, body []byte) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := instanceAuthToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s failed: %w", method, url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := readTruncated(resp.Body, 256)
		return nil, fmt.Errorf("%s %s returned %d: %s", method, url, resp.StatusCode, snippet)
	}
	return decodeConfigResponse(resp)
}

// decodeConfigResponse 校验上游响应确实是 JSON API 应答（而非 SPA 兜底 HTML）。
func decodeConfigResponse(resp *http.Response) (map[string]interface{}, error) {
	ct := resp.Header.Get("Content-Type")
	mediaType := ct
	if parsed, _, err := mime.ParseMediaType(ct); err == nil {
		mediaType = parsed
	}
	if !strings.Contains(mediaType, "application/json") {
		return nil, fmt.Errorf("upstream responded %s (Content-Type %q, want application/json) — not an OpenCode JSON API endpoint (SPA fallback?)", resp.Status, ct)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response body failed: %w", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("response body is not a JSON object: %w", err)
	}
	return doc, nil
}

// readTruncated 读取最多 n 字节用于错误信息展示。
func readTruncated(r io.Reader, n int) string {
	buf, err := io.ReadAll(io.LimitReader(r, int64(n)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(buf))
}

// globalConfigURL 拼接实例的 /global/config 端点。
func globalConfigURL(instanceBaseURL string) string {
	return strings.TrimRight(instanceBaseURL, "/") + "/global/config"
}

// GetModelConfig 获取模型配置：GET /global/config，把 ConfigV1 的 provider
// 映射回 Pocket 的 ModelConfig 形状（仅供实例配置页展示/回填）。
func (a *OpenCodeConfigHTTPAdapter) GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error) {
	doc, err := a.doConfigRequest(ctx, http.MethodGet, globalConfigURL(instanceBaseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("get model config failed: %w", err)
	}
	return modelConfigFromGlobalConfig(doc), nil
}

// modelConfigFromGlobalConfig 把 ConfigV1（/global/config 响应）映射为 ModelConfig。
func modelConfigFromGlobalConfig(doc map[string]interface{}) *ModelConfig {
	cfg := &ModelConfig{Providers: []Provider{}}
	providers, _ := doc["provider"].(map[string]interface{})
	for id, raw := range providers {
		p := Provider{ID: id, Enabled: true, Models: []ModelDefinition{}}
		if entry, ok := raw.(map[string]interface{}); ok {
			if name, _ := entry["name"].(string); name != "" {
				p.Name = name
			}
			if opts, ok := entry["options"].(map[string]interface{}); ok {
				p.BaseURL, _ = opts["baseURL"].(string)
				p.APIKey, _ = opts["apiKey"].(string)
			}
			if models, ok := entry["models"].(map[string]interface{}); ok {
				for mid := range models {
					p.Models = append(p.Models, ModelDefinition{ID: mid, DisplayName: mid, Enabled: true})
				}
			}
		}
		cfg.Providers = append(cfg.Providers, p)
	}
	if model, _ := doc["model"].(string); model != "" {
		if idx := strings.Index(model, "/"); idx > 0 {
			cfg.DefaultProvider = model[:idx]
		}
	}
	return cfg
}

// providerEntryToGlobalConfig 把 Pocket Provider 翻译成 ConfigV1 的 provider
// 条目。Pocket 网关一律是 OpenAI 兼容端点，npm 固定 @ai-sdk/openai-compatible
// （与 internal/opencode.BuildOpenCodeConfigContent 的 seed 写法一致）。
func providerEntryToGlobalConfig(p Provider) map[string]interface{} {
	models := make(map[string]interface{}, len(p.Models))
	for _, m := range p.Models {
		models[m.ID] = map[string]interface{}{"name": m.DisplayName}
	}
	return map[string]interface{}{
		"name": p.Name,
		"npm":  "@ai-sdk/openai-compatible",
		"options": map[string]interface{}{
			"baseURL": p.BaseURL,
			"apiKey":  p.APIKey,
		},
		"models": models,
	}
}

// UpdateModelConfig 更新模型配置：PATCH /global/config（深度合并、立即生效）。
// 只提交 provider 子文档，不覆盖实例的默认 model 等其他配置。
func (a *OpenCodeConfigHTTPAdapter) UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error {
	if config == nil {
		return fmt.Errorf("config required")
	}
	providers := make(map[string]interface{}, len(config.Providers))
	for _, p := range config.Providers {
		if p.ID == "" {
			continue
		}
		providers[p.ID] = providerEntryToGlobalConfig(p)
	}
	body, err := json.Marshal(map[string]interface{}{"provider": providers})
	if err != nil {
		return fmt.Errorf("marshal config failed: %w", err)
	}
	if _, err := a.doConfigRequest(ctx, http.MethodPatch, globalConfigURL(instanceBaseURL), body); err != nil {
		return fmt.Errorf("update model config failed: %w", err)
	}
	return nil
}

// ReloadConfig 校验配置已生效：官方契约下 PATCH 即时生效并持久化，不存在独立
// 的 reload 端点；这里回读 /global/config 确认实例仍应答 JSON 契约。
func (a *OpenCodeConfigHTTPAdapter) ReloadConfig(ctx context.Context, instanceBaseURL string) error {
	if _, err := a.doConfigRequest(ctx, http.MethodGet, globalConfigURL(instanceBaseURL), nil); err != nil {
		return fmt.Errorf("reload config failed: %w", err)
	}
	return nil
}

// TestModel 测试模型连接：官方契约没有单模型连通性端点，这里校验该
// provider/model 确实存在于实例配置中（配置存在性测试，非推理连通性测试）。
func (a *OpenCodeConfigHTTPAdapter) TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error {
	doc, err := a.doConfigRequest(ctx, http.MethodGet, globalConfigURL(instanceBaseURL), nil)
	if err != nil {
		return fmt.Errorf("test model failed: %w", err)
	}
	providers, _ := doc["provider"].(map[string]interface{})
	entry, ok := providers[providerID].(map[string]interface{})
	if !ok {
		return fmt.Errorf("provider %q not present in instance config", providerID)
	}
	models, _ := entry["models"].(map[string]interface{})
	if _, ok := models[modelID]; !ok {
		return fmt.Errorf("model %q not present under provider %q in instance config", modelID, providerID)
	}
	return nil
}
