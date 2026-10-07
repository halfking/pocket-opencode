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

// TransportSSE 是第四种形态：服务端真流式（边收边下发增量文本）。
//
// 2026-10-01 调研结论——本组里**只有**两家支持：
//
//	MiniMax asr-1.0      POST /v1/speech_to_text       stream=true → SSE
//	                    （流式与 verbose_json/srt/vtt **互斥**，要二选一）
//	智谱   glm-asr-2512   POST /v4/audio/transcriptions stream=true → SSE
//	                    （事件 transcript.text.delta / .done，结束 data: [DONE]）
//
// OpenRouter 的 /audio/transcriptions **不支持流式**（官方称上游约 60 秒超时）。
// 所以「便宜」与「即时出字」在 OpenRouter 上不可兼得——这是服务能力的事实
// 约束，不是本项目的实现取舍。设置页因此把「能否即时出字」标成**模型属性**，
// 而不是全局开关：用户选了 OpenRouter 省钱，就只能接受分段式即时（本地 VAD
// 切片后逐段出字），而不是逐字。
const TransportSSE = "sse"

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
	case TransportSSE:
		return TransportSSE
	default:
		return TransportAuto
	}
}

// Target 是一次转写请求解析出来的实际目标。
type Target struct {
	BaseURL        string  `json:"baseURL"`
	Model          string  `json:"model"`
	Transport      string  `json:"transport"`
	Channel        string  `json:"channel"`
	Label          string  `json:"label"`
	CostUSDPerHour float64 `json:"costUsdPerHour,omitempty"`

	// Language 是发给上游的语种提示（OpenAI 兼容的 `language` 表单字段）。
	//
	// 为什么必须有：whisper / gpt-4o-transcribe 系列在**不传** language 时靠模型
	// 自己猜语种，中文会议录音会被判成英语，输出夹英文或直接转错。本项目的场景
	// （会议、笔记语音录入）几乎全是中文，所以默认 zh；用户可以改成 en/ja 等。
	// 空字符串 = 不发这个字段（给那些不接受 language 的服务留退路）。
	Language string `json:"language,omitempty"`

	// APIKey 只在服务端内存里流转，不进 JSON。
	APIKey string `json:"-"`
}

// DefaultLanguage 是未显式指定语种时的默认值。
//
// 与 server 层探测用的 p.Language 默认值保持一致（都是 zh）：探测时假定中文，
// 真转写时也必须假定中文，否则「探测通过、实际转写跑偏」会变成一个极难查的坑。
const DefaultLanguage = "zh"

// SimplifiedChineseBiasPrompt 是中文转写时随请求带上的简体偏置提示。
//
// 2026-10-01 本机用 faster-whisper 实测（handoff §14）：语音内容是
// 「帮我记一下明天要买牛奶和面包」，识别结果是**繁体**的
// 「幫我記一下明天要買牛奶和麵包」—— 用字全对，只是字形不对。
// 加上这段提示后同一段音频输出一字不差的简体。
//
// 这是 whisper 系模型的**共同行为**，不是某个部署的怪癖：whisper 的训练
// 语料繁简混杂，简体音频也可能被解码成繁体。而设置页预置的外部候选里
// 就有 openai/whisper-large-v3-turbo，用户写简体笔记却拿到繁体正文，
// 属于产品必须处理的字形问题。
//
// 措辞照 OpenAI 官方给的做法：一句普通话 + 明确要求简体。
// 只在语种为中文时发送（见 Transcriber.transcriptions）。
const SimplifiedChineseBiasPrompt = "以下是普通话的句子，请用简体中文输出。"

// NormalizeLanguage 归一化语种：空 → DefaultLanguage；统一小写并把 zh-CN 之类
// 的地区后缀收敛成 zh（上游只认 ISO-639-1，传 zh-CN 会被拒或被忽略）。
func NormalizeLanguage(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "":
		return DefaultLanguage
	case "zh-cn", "zh_cn", "zh-hans", "cmn":
		return "zh"
	default:
		return v
	}
}

// ModelOption 是设置页展示的一条推荐模型。
//
// 2026-10-01 实测 llm.kxpms.cn/v1：/models 共 604 个模型，其中 modality=audio
// 只有 gpt-audio / gpt-audio-mini，另有 mimo-v2.5-asr（ASR 模型但网关把它标成
// text）。这三个是网关侧唯一值得预置的候选，当前都返回 503 no_candidate
// （网关列了模型但没有可用上游），因此设置页必须显示真实探测状态而不是
// 假装「已配置可用」。
type ModelOption struct {
	Model      string  `json:"model"`
	Group      string  `json:"group"`
	Note       string  `json:"note"`
	BaseURL    string  `json:"baseURL,omitempty"`
	USDPerHour float64 `json:"usdPerHour,omitempty"`
	Accuracy   string  `json:"accuracy,omitempty"`

	// Streaming 标记该模型是否支持**服务端真流式**（SSE 边收边下发）。
	// 与「能否出字」是两件事：所有模型都能出字，只有部分能边收边出。
	// 设置页据此提示用户「选这个会晚一点出字，但便宜」。
	Streaming bool `json:"streaming,omitempty"`

	// MaxSeconds 是该服务单次请求的音频时长上限（0 = 未知）。
	//
	// 这个字段直接决定「全量转写」能不能一把梭：智谱限 30 秒、MiniMax 限
	// 500 秒、OpenRouter 约 60 秒（上游超时）。超限必须先切段，所以全量
	// 转写服务能力必须内置切分，不能指望「上传整段让上游处理」。
	MaxSeconds int `json:"maxSeconds,omitempty"`
}

// RecommendedGatewayModels 网关侧预置：网关模型目录里与语音转写相关的模型。
// 顺序即设置页展示顺序。
//
// ⚠ 2026-10-06/07 ASR 多供应商轮实测重写（scripts/verify-gateway-audio-multi.mjs，
// 网关 build 2484，TTS 合成已知真值回环）：mimo-v2.5-asr 与 minimax-asr-1.0
// zh/en 全对（en 词级 98.7-100%），glm-asr 数据面已接通但在库智谱凭据余额
// 不足（429/1113，充值后零改动可用）。gpt-audio 系仍是「列了但没有上游」。
func RecommendedGatewayModels() []ModelOption {
	return []ModelOption{
		{Model: "mimo-v2.5-asr", Group: "gateway", Note: "实测可用（2026-10-06 zh/en 全对）· 网关 /models 未标 audio，勿按目录过滤掉它"},
		{Model: "minimax-asr-1.0", Group: "gateway", Note: "实测可用（2026-10-06 zh/en 全对）· 网关 speech-to-text 传输，SSE 流式，500s 限长", Streaming: true, MaxSeconds: 500, USDPerHour: 0.38},
		{Model: "glm-asr", Group: "gateway", Note: "数据面已接通但智谱凭据余额不足（2026-10-06 429/1113）· 充值后可用；30s 限长 + 热词", Streaming: true, MaxSeconds: 30, USDPerHour: 0.50},
		{Model: "gpt-audio-mini", Group: "gateway", Note: "网关列了但实测 503 no_provider（2026-10-06）"},
		{Model: "gpt-audio", Group: "gateway", Note: "网关列了但实测 503 no_provider（2026-10-06）"},
	}
}

// RecommendedExternalModels 外部 ASR 预置：网络调研（2026-10-01）出的高精度/低成本组合。
//
// 本轮（2026-10-01 用户点名「找 openrouter / minimax / glm 里便宜的转写 LLM」）
// 重新调研后的排序，**按「便宜且够用」到「高精度」**排列，逐条标注可信度：
//
//	#1 qwen/qwen3-asr-0.6b        ≈$0.012/h  OpenRouter 转写端点。官方模型页称
//	                                           「30 语言 + 22 种中文方言」——本组里
//	                                           唯一明确支持中文方言的极低价项。
//	#2 whisper-large-v3-turbo     ≈$0.012/h  同价位，99+ 语言生态最成熟，可拿
//	                                           word/segment 时间戳（verbose_json）。
//	#3 qwen/qwen3-asr-1.7b        ≈$0.027/h  0.6B 在嘈杂/专业术语场景偏弱时的升级档。
//	#4 asr-1.0 (MiniMax)          $0.38/h    **唯一官方支持 SSE 流式** + 说话人分离
//	                                           + ≤500 秒长音频（会议不用切段）。
//	#5 glm-asr-2512 (智谱)         ¥0.06/min   **唯一国产「标准 OpenAI 路径 + SSE +
//	                                           热词表」三合一**；但限 30 秒/次。
//	#6 gpt-4o-mini-transcribe     $0.18/h    生态最稳的保底档。
//
// 计费口径的重要提醒（调研原话，别在实现里换算错）：
//   - OpenRouter 的 pricing.prompt 字段**单位未公开**。只有 Whisper 三兄弟可交叉
//     验证为「美元/秒」（whisper-1 = 0.0001 = OpenAI 官方 $0.006/分钟）。其余模型的
//     「$/hr = prompt × 3600」是**推断**，不是官方声明。
//   - 因此这里的 USDPerHour 只能当**量级参考**展示给用户，不能用于结算或预算告警。
//     真实成本以响应里的 usage.seconds / usage.cost 为准（见 transcribe.go 的
//     usage 回填逻辑）。
//   - token 计价的模型（gpt-4o-transcribe 等）没有「每分钟固定价」，不做折算。
func RecommendedExternalModels() []ModelOption {
	return []ModelOption{
		{
			Model: "qwen/qwen3-asr-0.6b", Group: "external",
			BaseURL:    "https://openrouter.ai/api/v1",
			Note:       "最低价档，官方称支持 22 种中文方言",
			USDPerHour: 0.012,
			MaxSeconds: 60,
			Accuracy:   "OpenRouter 转写端点，pricing.prompt 0.00000333（单位推断）；0.6B 小模型嘈杂环境精度弱于 1.7B；不支持流式（上游约 60 秒超时）",
		},
		{
			Model: "openai/whisper-large-v3-turbo", Group: "external",
			BaseURL:    "https://openrouter.ai/api/v1",
			Note:       "最低价档，99+ 语言，可取 word 级时间戳",
			USDPerHour: 0.012,
			MaxSeconds: 60,
			Accuracy:   "pricing.prompt 0.00000333（该字段单位已由 whisper-1 交叉验证为美元/秒）；不支持流式",
		},
		{
			Model: "qwen/qwen3-asr-1.7b", Group: "external",
			BaseURL:    "https://openrouter.ai/api/v1",
			Note:       "0.6B 精度不够时的升级档",
			USDPerHour: 0.027,
			MaxSeconds: 60,
			Accuracy:   "pricing.prompt 0.0000075（单位推断）；方言覆盖同 0.6B；不支持流式",
		},
		{
			Model: "gpt-4o-mini-transcribe", Group: "external",
			BaseURL:    "https://api.openai.com/v1",
			Note:       "生态最稳的保底档",
			USDPerHour: 0.18,
			MaxSeconds: 600,
			Accuracy:   "AA 多语 WER 指数 0.0447；$3.0/千分钟；不支持 timestamps（400）",
		},
		{
			Model: "gpt-4o-transcribe", Group: "external",
			BaseURL:    "https://api.openai.com/v1",
			Note:       "高精度档，方言/噪声/多人",
			USDPerHour: 0.36,
			MaxSeconds: 600,
			Accuracy:   "AA WER 0.0396；$6.0/千分钟",
		},
		{
			Model: "asr-1.0", Group: "external",
			BaseURL:    "https://api.minimaxi.com/v1",
			Note:       "官方支持 SSE 流式 + 说话人分离，可整段 500 秒",
			USDPerHour: 0.38,
			Streaming:  true,
			MaxSeconds: 500,
			Accuracy:   "官方定价页直标 $0.38/hour，无换算歧义；20 语种含 zh/yue；流式与 verbose_json/srt/vtt 互斥；路径非 OpenAI 标准",
		},
		{
			Model: "glm-asr-2512", Group: "external",
			BaseURL:    "https://open.bigmodel.cn/api/paas/v4",
			Note:       "国产唯一「标准 OpenAI 路径 + SSE 流式 + 热词表」，限 30 秒/次",
			USDPerHour: 0.50,
			Streaming:  true,
			MaxSeconds: 30,
			Accuracy:   "0.06 元/分钟 ≈ 3.6 元/小时；支持中文 + 英文 + 8 种方言；热词 ≤100 条；仅 wav/mp3",
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
