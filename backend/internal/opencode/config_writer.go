package opencode

import (
	"encoding/json"
	"fmt"
)

// DefaultLLMGatewayBaseURL 默认 LLM Gateway 端点（OpenAI 兼容 /v1/...）。
// 用户可在 SettingsView 修改后通过 POST /api/llm-gateway/config 热更新。
//
// 2026-09-30 切回 https://llm.kxpms.cn/v1（用户指定的正式网关，实测 /v1/models
// 200、608 个模型、流式首字节 ~300ms）；此前 2026-09-21 曾切到 llmgo.kxpms.cn，
// 并把 llm.kxpms.cn 列入 obsolete 强制改写——两处已一并回退，否则设置页
// 填 llm.kxpms.cn 会被 rewriteObsoleteGateway 打回 llmgo，配置形同虚设。
// POCKET_LLM_GATEWAY_URL 仍可覆盖。reset 后首次启动 pocketd 会自动写 seed。
const DefaultLLMGatewayBaseURL = "https://llm.kxpms.cn/v1"

// DefaultLLMGatewayAPIKey 自家网关（llm.kxpms.cn）的默认租户 Key，让全新实例
// 不配任何 env 也能在设置页直接连通、对话可用。生效优先级：
// POCKET_LLM_GATEWAY_API_KEY（env / 部署 .env）> 本常量。
// 要换成别的租户或彻底去掉内置密钥，把 env 设成自己那把 key 即可覆盖；
// 注意本常量是明文入库的共享密钥，仅适用于私有部署仓库。
const DefaultLLMGatewayAPIKey = "sk-6tGLjzlzUIOuMxh6qhOVRK9eznOTVAkQ3JxRZrvWECrK51YV"

// DefaultLLMGatewayPreferredModels 默认「常用模型」列表，写入 seed 的
// preferredModels。catalog models 仍由「测试连接」拉取后写入。
//
// 2026-09-30 用户指定：设置页默认勾选下列 9 个（按用户给出的顺序写死），
// 前面的 claude-opus-4-8 / claude-sonnet-4-6 / gpt-5.6 / gpt-5.5 / gpt-5.4 /
// deepseek-v4-pro / mimo-v2.5-pro 已从默认勾选里移除（仍可在设置页手动加回，
// 目录来自网关 /v1/models）。
//
// 旧顺序的实测记录（保留作为后续调整依据，见下）：
// 本列表同时是 auto 模式的降级链顺序（llmbff_provider_adapters.go 的
// nextFallbackModel 按序取候选，每个候选 20s 尝试窗）。真机上 auto 模式实测
// 首问 25.3s：首选候选吃满 20s 尝试窗后降级到 claude-opus-4-8 才拿到回答。
//
// 但**刻意没有重排**：对 llm.kxpms.cn 做了三轮独立探测（每模型 30s/45s 窗口），
// 结论互相打架——
//   claude-fable-5  4.2s ✅ / 5.0s ✅ / 30s 超时 ❌
//   claude-sonnet-5 45s 超时 ❌ / 4.4s ✅
//   minimax-m3      4.0s ✅ / 1.7s ✅ / 2.0s ✅
//   gpt-5.4          45s 超时 ❌ / 30s 超时 ❌
//   glm-5.2          HTTP 200 但 0 个 content delta（三轮一致）
// 也就是说上游可用性是波动的，单次或两次探测不足以支撑「把谁排前面」的结论，
// 那只会把噪声固化进默认值。20s 尝试窗本身也不宜调低——代码注释记录过
// kimi-k3 长 prompt 首 token 实测 >20s，调低会误杀慢而可用的模型。
// 网关侧模型可用性治理由网关负责，应用侧保持"链式降级 + 进度帧"这一既有设计。
// 注意：本列表同时是 auto 模式的降级链顺序，改动会同时改变默认首选模型。
var DefaultLLMGatewayPreferredModels = []string{
	"glm-5.2",
	"minimax-m3",
	"kimi-k3",
	"claude-sonnet-5",
	"gpt-5.6-terra",
	"claude-opus-5",
	"claude-fable-5",
	"gpt-5.6-sol",
	"gemini-3.5-flash",
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