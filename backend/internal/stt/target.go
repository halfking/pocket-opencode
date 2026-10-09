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
	// ChannelMiniMax：只用 MiniMax 原生 API（/v1/speech_to_text）。
	//
	// 为什么不复用 external：MiniMax 的路径、参数名、参数**位置**都不同于
	// OpenAI 兼容层（实测见 provider.go 文件头）。放在 external 下只能靠
	// 「baseURL 含 minimax 就走另一套」的 if 区分，那正是本仓要消灭的形态。
	ChannelMiniMax = "minimax"
)

// 传输形态。
//
// 2026-10-08 起网关已提供 OpenAI 兼容 /v1/audio/transcriptions（内部再适配
// 各上游方言）。客户端默认走 transcriptions；chat-audio 仅作探测回退或
// 用户显式选择。chat-audio 仍可能「收下请求却丢掉音频」并幻觉文本，
// 由 transcribe 的音频回传校验兜住。
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
	case ChannelMiniMax:
		return ChannelMiniMax
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

	// Provider 选定「转写模板」（见 provider.go）。
	//
	// 为什么它与 Transport 是**两个**维度而不是合并：
	//  - Provider 决定「怎么发」（路径、参数名、参数位置、响应结构）
	//  - Transport 决定「发成什么样」（一次性 JSON / SSE / 聊天接口）
	//
	// 真实存在「同一模板两种形态」（MiniMax 的 json 与 stream=true 打同一个
	// 路径，只是 response_format 与 stream 字段不同），也有「同一形态两个模板」
	//（OpenAI 与智谱路径参数名完全一样，只有主机与 SSE 事件名不同）。
	// 合并任何一个维度都会丢掉一种组合。
	//
	// 空串 = 不指定，由 ProviderForTarget 按 baseURL/模型推断
	//（老配置不受影响，见 ProviderForTarget 的注释）。
	Provider string `json:"provider,omitempty"`

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
// 只有 gpt-audio / gpt-audio-mini，另有 mimo-v2.5-asr（ASR 模型但网关
// /models 没把它标成 audio，容易被当成「不是 ASR」而忽略）。
//
// ⚠ 2026-10-06 复测更新了状态，本段结论**已按今天的实测重写**
// （上一版写的是「这三个当前都返回 503 no_candidate」，今天不再成立）：
//
//	POST /v1/audio/transcriptions（真实 16k 中文音频，llmgo.kxpms.cn）
//	  mimo-v2.5-asr     → 200，转写正确，4.6s 音频 0.8s 往返  ← 可用
//	  gpt-audio-mini    → 503 no_provider
//	  gpt-audio         → 503 no_provider
//
// ⇒ 网关虽然列了后两个模型，但没有可用上游；**唯一能用的预置项是
// mimo-v2.5-asr**。设置页仍必须显示真实探测状态而不是假装「已配置可用」——
// 但那是指探测结果，不是指「三个都不可用」。
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

	// Diarization 标记该模型支持**服务端说话人分离**（返回带 speaker 的分段）。
	//
	// ★ 与前端那套本地分离不是一回事，别混为一谈：
	// 前端 `ingest-speech.ts` 用本地 speaker-embedding 逐 5~8 秒短段做
	// profile 聚类，它只见过这一小段音频，跨段的同一个人经常被判成两个人。
	// 服务端分离是在**整段音频**上做的，跨段一致。
	// ⇒ 两者互补，不是替代：短段实时字幕用本地，整段精校用服务端。
	Diarization bool `json:"diarization,omitempty"`

	// WordTimestamps 标记该模型支持词级时间戳（response_format=verbose_json
	// + timestamp_granularities=[word]）。
	//
	// 为什么单独列：这两个能力**都由 response_format 决定**，一起开一起关。
	// 但它们的上游失败模式不同（见 DiarizationMaxSeconds 的注释），
	// 所以设置页要能分别如实展示，而不是笼统写一句「支持高级特性」。
	WordTimestamps bool `json:"wordTimestamps,omitempty"`

	// DiarizationMaxSeconds 是**开启分离后**的时长上限（0 = 未公布/不限）。
	//
	// ★ 这个字段是实测读数，不是从 MaxSeconds 推的，两者含义完全不同：
	// MaxSeconds 是「不开任何增强特性时单次能传多久」；
	// 本字段是「开了 diarization 之后还能传多久」——它**只会更小**。
	//
	// microsoft.ai 官方文档明写：MAI-Transcribe 的 diarization 目前只支持
	// 较短录音，约 15 分钟及以上的请求会返回 408/500/503
	// （diarization_unavailable），而同一段录音关掉分离就能转成功。
	//
	// 本项目的主场景恰恰是会议——**15 分钟以上的会议是常态而不是例外**。
	// 所以「模型支持 diarization」不等于「本项目能用它」，
	// 必须按音频时长决定要不要开，见 ShouldRequestDiarization。
	DiarizationMaxSeconds int `json:"diarizationMaxSeconds,omitempty"`
}

// RecommendedGatewayModels 网关侧预置：走统一入口 POST /v1/audio/transcriptions。
//
// 顺序即设置页展示顺序。2026-10-08 起网关种子了 glm-asr / minimax-asr-1.0；
// 客户端仍打 OpenAI 路径，由网关适配上游方言（勿直连 /v1/speech_to_text）。
// mimo-v2.5-asr 仍是 2026-10-06 实测可用项，排第一；gpt-audio* 可能 503。
func RecommendedGatewayModels() []ModelOption {
	return []ModelOption{
		{Model: "mimo-v2.5-asr", Group: "gateway", Note: "实测可用（2026-10-06 200）· 统一 /v1/audio/transcriptions"},
		{Model: "glm-asr", Group: "gateway", Note: "智谱 ASR · 经网关 multipart 透传（2026-10-08 目录种子）"},
		{Model: "minimax-asr-1.0", Group: "gateway", Note: "MiniMax ASR · 客户端勿改打 speech_to_text，网关内部适配"},
		{Model: "gpt-audio-mini", Group: "gateway", Note: "网关列了但可能 503 no_provider"},
		{Model: "gpt-audio", Group: "gateway", Note: "网关列了但可能 503 no_provider"},
	}
}

// RecommendedExternalModels 外部 ASR 预置：网络调研出的高精度/低成本组合。
//
// ⚠️ 价格与能力都是**会过期的快照**，不是常量。每条都标了信源与复核日期；
//
//	改动前先复核（本轮复核日 2026-10-06）。
//
// 2026-10-06 复核（OpenRouter 官方 speech-to-text 榜单 + 各厂商定价页）
// 相对 10-01 那版的变化：
//
//	#1 新增 microsoft/mai-transcribe-2 —— **本轮最有价值的一条**。
//	    它同时具备本项目一直在缺的三件事：说话人分离、词级时间戳、热词偏置，
//	    而且**明确支持中文**（60 语种含 zh）。此前清单里带 diarization 的只有
//	    MiniMax asr-1.0（$0.38/h，路径非 OpenAI 标准），带热词的只有智谱
//	    glm-asr-2512（¥0.06/min 且限 30 秒/次）。
//	    ⇒ 对「会议录音」这个主场景，它是唯一一个「便宜的 OpenAI 兼容路径 +
//	    三件套齐全」的候选。
//	    ★ $0.10/h 是**限时促销价**（至 2026 年底），不能当长期成本基准。
//	    ★ 三项能力需显式开参才生效，且**本仓尚未接线该开参** —— 选它当前
//	      只能拿到更好的文字，拿不到说话人标签。
//
//	#2 新增 nvidia/nemotron-3.5-asr-streaming-multilingual-0.6b —— 同价位
//	    （$0.000003/秒，与 whisper-turbo 同价）里唯一**原生流式**的，600M 可纯
//	    CPU 跑。⚠️ 但中文 zh-CN 属 broad-coverage 档而非 transcription-ready，
//	    精度不如上面几条，本项目主场景慎选。
//
//	复核后未改动的原 7 条：价格与能力描述与信源一致。
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
			Model: "nvidia/nemotron-3.5-asr-streaming-multilingual-0.6b", Group: "external",
			BaseURL:    "https://openrouter.ai/api/v1",
			Note:       "同最低价，唯一原生流式；600M 可纯 CPU 跑",
			USDPerHour: 0.012,
			Streaming:  true,
			MaxSeconds: 60,
			Accuracy:   "OpenRouter 榜单 2026-10-06 实读 $0.000003/秒；NVIDIA 自报 WER 7.07% < whisper-turbo 7.83%；⚠️ 中文 zh-CN 属 broad-coverage 档（非 transcription-ready），中文精度不如 whisper/qwen 两档，慎选",
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
			Model: "microsoft/mai-transcribe-2", Group: "external",
			BaseURL:    "https://openrouter.ai/api/v1",
			Note:       "会议首选：说话人分离 + 词级时间戳 + 热词，支持中文",
			USDPerHour: 0.10,
			MaxSeconds: 60,
			// 三项增强能力已接线（见 transcribe.go 的 applyVerboseOptions）：
			// 本仓会主动发 response_format=verbose_json +
			// timestamp_granularities=[word] + provider.options.azure.diarization。
			Diarization:           true,
			WordTimestamps:        true,
			DiarizationMaxSeconds: 900,
			Accuracy: "microsoft.ai 2026-10-06 实读：60 语种含 zh；FLEURS WER 3.4%（Artificial Analysis #2）；diarization / word-level timestamps / keyword biasing 三项齐全；" +
				"⚠️ $0.10/h 是限时促销价（至 2026 年底），不可当长期成本基准；" +
				"⚠️ 开启说话人分离后只支持约 15 分钟以内的录音（微软文档：更长会返回 408/500/503 diarization_unavailable），" +
				"长录音本仓自动关掉分离并照常返回文字——即长会议拿得到更好的文字、拿不到说话人标签",
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
			// ⚠ 2026-10-08 从 minimaxi.com 改成 minimax.cn。
			// 原值是**国际站**地址，本仓用户拿国内 key（platform.minimax.cn 申请）
			// 打过去会 401 —— 而错误信息是「login fail」，指向「key 没带」，
			// 不会指向「你打错了站」。这是最贵的一种默认值错误。
			// 下方 TestMiniMaxDefaultBaseURLIsDomesticStation 钉住这个选择。
			BaseURL:    "https://api.minimax.cn",
			Note:       "官方支持 SSE 流式 + 说话人分离，可整段 500 秒",
			USDPerHour: 0.38,
			Streaming:  true,
			MaxSeconds: 500,
			// ⚠ 2026-10-08 实测补正：这三项原来**全是缺省的**，导致
			// 「模型支持说话人分离」这件事在预置数据里根本没被表达出来 ——
			// SupportsDiarization("asr-1.0") 返回 false，于是 ShouldRequestDiarization
			// 永远不开分离，用户勾了「说话人分离」也拿不到标签。
			// 这是本轮实跑（真音频经本仓代码路径）暴露的静默失效，
			// curl 测不出来：curl 只证明服务端能用，证明不了本仓请求没带参。
			//
			// 实测读数（7.086s 单人中文录音，api.minimax.cn）：
			//   response_format=verbose_json          → 200，n_speakers=1, segments[0].speaker="S1"
			//   + timestamp_level=word                → 200，segments 按字切，每段仍带 speaker
			// ⇒ 分离与词级时间戳**都**实测可用。
			Diarization:    true,
			WordTimestamps: true,
			Accuracy: "官方定价页直标 $0.38/hour，无换算歧义；20 语种含 zh/yue；流式与 verbose_json/srt/vtt 互斥（实测 400 (2013)）；" +
				"路径非 OpenAI 标准（/v1/speech_to_text，language 走请求头）；" +
				"⚠ 分离与词级时间戳于 2026-10-08 实测可用（verbose_json 返回 n_speakers 与 speaker 标签）",
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
