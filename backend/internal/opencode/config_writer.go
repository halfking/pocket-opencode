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

// DefaultLLMGatewayAPIKey 默认 LLM Gateway 租户 API Key（仅在 dev/seed 路径使用）。
// 真实部署应通过 POCKET_LLM_GATEWAY_API_KEY env 注入；此处常量用于确保 reset
// 之后的 dev 实例仍然有可用默认网关，避免前端首次进入设置页全空。
//
// 注意：这是租户共享密钥，仓库提交；生产 portal 上线后请替换为 KMS 引用。
const DefaultLLMGatewayAPIKey = "sk-6tGLjzlzUIOuMxh6qhOVRK9eznOTVAkQ3JxRZrvWECrK51YV"

// DefaultLLMGatewayPreferredModels 默认「常用模型」列表，逗号分隔的原文顺序作为
// preferredModels 初始值写入 seed；models（catalog）首版为空，由用户首次
// 「测试连接」→ POST /api/llm-gateway/test 拉取 {baseURL}/v1/models 后写入。
//
// 与 SettingsLLMGateway.vue VENDOR_RULES 配合渲染「按原厂分组」的常用模型区。
//
// 注意：seed 仅纳入经实测对当前默认网关（llm.kxpms.cn）可正常返回的模型 id。
// 2026-08-31 实测中，`minimax-m3` 与 `kimi-k3` 在该网关的 /v1/chat/completions
// 上会挂死（既不返结果也不返错误，导致前端聊天 60s 超时），已从 seed 中移除；
// 真实部署替换网关或恢复这些模型路由后再加回。
var DefaultLLMGatewayPreferredModels = []string{
	"glm-5.2",
	"claude-sonnet-5",
	"gpt-5.6-terra",
	"claude-opus-5",
	"claude-fable-5",
	"gpt-5.6-sol",
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