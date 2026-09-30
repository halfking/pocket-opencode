// Package stt 的「转写目标」模型：一次转写到底该打哪个地址、用哪个模型、
// 走哪种传输形态。原实现把 Groq + whisper-large-v3-turbo 写死在 Transcriber 里，
// 导致网关（llm.kxpms.cn）里新出现的 ASR 模型完全用不上，设置页也无从调整。
//
// 这里把「目标」抽象出来，两个来源：
//   - gateway：用户已配置的 LLM 网关（自动发现其中可做 ASR 的模型）
//   - external：任意 OpenAI 兼容 /audio/transcriptions 的外部 ASR 服务
package stt

import "strings"

// 转写通道（设置页可手工调整）。
const (
	// ChannelAuto：优先用网关里已探测可用的 ASR 模型，没有再退到外部服务。
	ChannelAuto = "auto"
	// ChannelGateway：只用网关。
	ChannelGateway = "gateway"
	// ChannelExternal：只用外部 ASR 服务。
	ChannelExternal = "external"
)

// 传输形态。网关并不一定有 OpenAI 兼容的 /audio/transcriptions
// （2026-10-01 实测 llm.kxpms.cn/v1 该端点 404），所以另一种可行形态是
// chat/completions + input_audio —— 但网关有可能「收下请求却丢掉音频」
// 并返回一段幻觉文本，这由 transcribe 的音频回传校验兜住。
const (
	TransportAuto           = "auto"
	TransportTranscriptions = "transcriptions"
	TransportChatAudio      = "chat-audio"
)

// NormalizeChannel 归一化通道名；无法识别时回落到 auto。
func NormalizeChannel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ChannelGateway:
		return ChannelGateway
	case ChannelExternal:
		return ChannelExternal
	default:
		return ChannelAuto
	}
}

// NormalizeTransport 归一化传输形态；无法识别时回落到 auto（由探测决定）。
func NormalizeTransport(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case TransportTranscriptions:
		return TransportTranscriptions
	case TransportChatAudio:
		return TransportChatAudio
	default:
		return TransportAuto
	}
}

// Target 是一次转写请求解析出来的实际目标。
type Target struct {
	BaseURL       string  `json:"baseURL"`
	Model         string  `json:"model"`
	Transport     string  `json:"transport"`
	Channel       string  `json:"channel"`
	Label         string  `json:"label"`
	CostUSDPerHour float64 `json:"costUsdPerHour,omitempty"`

	// APIKey 只在服务端内存里流转，不进 JSON。
	APIKey string `json:"-"`
}

// ModelOption 是设置页展示的一条推荐模型。
//
// 2026-10-01 实测 llm.kxpms.cn/v1：/models 共 604 个模型，其中 modality=audio
// 只有 gpt-audio / gpt-audio-mini，另有 mimo-v2.5-asr（ASR 模型但网关把它标成
// text）。这三个是网关侧唯一值得预置的候选，当前都返回 503 no_candidate
//（网关列了模型但没有可用上游），因此设置页必须显示真实探测状态而不是
// 假装「已配置可用」。
type ModelOption struct {
	Model    string  `json:"model"`
	Group    string  `json:"group"`
	Note     string  `json:"note"`
	BaseURL  string  `json:"baseURL,omitempty"`
	USDPerHour float64 `json:"usdPerHour,omitempty"`
	Accuracy string  `json:"accuracy,omitempty"`
}

// RecommendedGatewayModels 网关侧预置：网关模型目录里唯一与语音转写相关的三个。
// 顺序即设置页展示顺序（按「便宜/够用」到「最通用」）。
func RecommendedGatewayModels() []ModelOption {
	return []ModelOption{
		{Model: "gpt-audio-mini", Group: "gateway", Note: "网关 modality=audio，mini 档最省"},
		{Model: "mimo-v2.5-asr", Group: "gateway", Note: "网关 ASR 模型（目录里被标成 text）"},
		{Model: "gpt-audio", Group: "gateway", Note: "网关 modality=audio，通用档"},
	}
}

// RecommendedExternalModels 外部 ASR 预置：网络调研（2026-10）出的高精度/低成本组合。
// 成本口径来自各官方定价页，换算成美元/小时，便于设置页横向比较。
func RecommendedExternalModels() []ModelOption {
	return []ModelOption{
		{
			Model: "gpt-4o-mini-transcribe", Group: "external",
			BaseURL: "https://api.openai.com/v1",
			Note:     "低成本档，日常会议/笔记够用",
			USDPerHour: 0.18,
			Accuracy:   "Artificial Analysis 多语 WER 指数 0.0447；$3.0/千分钟",
		},
		{
			Model: "whisper-large-v3-turbo", Group: "external",
			BaseURL: "https://api.groq.com/openai/v1",
			Note:     "最低价档，速度因子高，适合长录音",
			USDPerHour: 0.04,
			Accuracy:   "AA WER 0.0462；约 $0.667/千分钟（二手来源，下单前以控制台为准）",
		},
		{
			Model: "gpt-4o-transcribe", Group: "external",
			BaseURL: "https://api.openai.com/v1",
			Note:     "高精度档，方言/噪声/多人场景",
			USDPerHour: 0.36,
			Accuracy:   "AA WER 0.0396；$6.0/千分钟",
		},
	}
}

// RecommendedModels 设置页展示的全部预置（网关组 + 外部组）。
func RecommendedModels() []ModelOption {
	out := RecommendedGatewayModels()
	return append(out, RecommendedExternalModels()...)
}

// KnownUSDPerHour 返回已知模型的美元/小时价格，未知返回 0（不猜价）。
// 未知就是未知：报价缺失时宁可显示「未知」，也不要让成本看板悄悄归零。
func KnownUSDPerHour(model string) float64 {
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.USDPerHour
		}
	}
	return 0
}
