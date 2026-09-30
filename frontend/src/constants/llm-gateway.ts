/**
 * LLM 网关前端默认配置 —— 与后端 `internal/opencode/config_writer.go` 的
 * DefaultLLMGatewayBaseURL / DefaultLLMGatewayPreferredModels 同源。
 *
 * ## 为什么要在前端也存一份
 *
 * 设置页（/settings/llm-gateway）正常路径是 GET /api/llm-gateway/config 拉后端
 * seed 好的配置。但离线 / 后端未起 / 首次安装尚未 seed 时，这个请求会失败，表单
 * 就是全空——用户看到空白框，得先知道地址长什么样才填得下去。
 *
 * 这份常量只做「后端拿不到时的兜底预填」，**不参与任何凭据判断**：API Key 永远
 * 不下发到前端（后端只回 apiKeySet + 掩码），前端这份里也没有 key。真正的生效值
 * 仍以后端 seed / 用户保存为准（见 SettingsLLMGateway.vue 的 onMounted）。
 */

/** 自家网关（OpenAI 兼容，含 /v1 后缀）。 */
export const DEFAULT_GATEWAY_BASE_URL = 'https://llm.kxpms.cn/v1'

/** 消息格式：当前对话链路唯一实现的是 openai-chat。 */
export const DEFAULT_GATEWAY_FORMAT = 'openai-chat'

/**
 * 默认勾选的常用模型（写入 preferredModels）。
 * 非空时聊天等模型选择器只展示这些；空 = 展示网关 /v1/models 返回的全部。
 * 顺序与后端常量一致（auto 模式按此顺序降级）。
 */
export const DEFAULT_GATEWAY_PREFERRED_MODELS: readonly string[] = [
  'glm-5.2',
  'minimax-m3',
  'kimi-k3',
  'claude-sonnet-5',
  'gpt-5.6-terra',
  'claude-opus-5',
  'claude-fable-5',
  'gpt-5.6-sol',
  'gemini-3.5-flash',
]
