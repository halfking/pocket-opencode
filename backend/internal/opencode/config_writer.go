package opencode

import (
	"encoding/json"
	"fmt"
)

// DefaultLLMGatewayBaseURL 默认 LLM Gateway 端点（OpenAI 兼容 /v1/...）。
// 用户可在 SettingsView 修改后通过 POST /api/llm-gateway/config 热更新。
//
// 2026-08-31 调整为 https://llm.kxpms.cn/v1（与内部门关服务 .kxpms.cn 路径一致）；
// POCKET_LLM_GATEWAY_URL 仍可覆盖。reset 后首次启动 pocketd 会自动写 seed。
const DefaultLLMGatewayBaseURL = "https://llm.kxpms.cn/v1"

// DefaultLLMGatewayPreferredModels 默认「常用模型」列表，写入 seed 的
// preferredModels。catalog models 仍由「测试连接」拉取后写入。
// API Key 只从 POCKET_LLM_GATEWAY_API_KEY 注入，禁止把租户密钥写进仓库。
var DefaultLLMGatewayPreferredModels = []string{
	"claude-fable-5",
	"claude-opus-4-8",
	"claude-sonnet-4-6",
	"claude-sonnet-5",
	"gpt-5.6",
	"gpt-5.5",
	"gpt-5.4",
	"glm-5.2",
	"minimax-m3",
	"deepseek-v4-pro",
	"mimo-v2.5-pro",
}

// LLMGatewayConfig 描述注入到 OpenCode 的 LLM Gateway 配置。
//
// OpenCode 上游支持"openai-compatible" provider：给定 baseURL + apiKey + 模型列表，
// 即可让 OpenCode 把所有 LLM 请求通过这个 baseURL 走。对应到 llm-gateway-go 的
// OpenAI 兼容 /v1/chat/completions、/v1/models 等端点。
type LLMGatewayConfig struct {
	BaseURL string   `json:"baseURL"` // e.g. https://llm.kxpms.cn/v1
	APIKey  string   `json:"apiKey"`  // sk-...
	Models  []string `json:"models"`  // 可用模型 id 列表；为空时使用 gateway 返回的 /v1/models
}

// BuildOpenCodeConfigContent 构造 OPENCODE_CONFIG_CONTENT JSON 字符串。
//
// 产出结构遵循 OpenCode V1 schema（packages/core/src/v1/config/provider.ts）：
//   provider.<id>.npm = "@ai-sdk/openai-compatible"
//   provider.<id>.options.baseURL + apiKey
//   model = <providerID>/<modelID>
//
// 注入方式：
//   - 若 pocketd 拉起 opencode 子进程：写入环境变量 OPENCODE_CONFIG_CONTENT
//   - 若 opencode 已在跑：调 PUT /api/config/providers（V1）或写 ~/.config/opencode/config.json + reload
func BuildOpenCodeConfigContent(cfg LLMGatewayConfig, defaultModel string) (string, error) {
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("baseURL required")
	}
	if cfg.APIKey == "" {
		return "", fmt.Errorf("apiKey required")
	}
	if defaultModel == "" && len(cfg.Models) > 0 {
		defaultModel = cfg.Models[0]
	}
	if defaultModel == "" {
		defaultModel = "gpt-4o"
	}

	models := make(map[string]map[string]interface{}, len(cfg.Models))
	for _, m := range cfg.Models {
		models[m] = map[string]interface{}{"name": m}
	}

	providerID := "openai-compatible-pocket"
	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			providerID: map[string]interface{}{
				"name":    "Pocket LLM Gateway",
				"npm":     "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": cfg.BaseURL,
					"apiKey":  cfg.APIKey,
				},
				"models": models,
			},
		},
		"model": fmt.Sprintf("%s/%s", providerID, defaultModel),
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(out), nil
}