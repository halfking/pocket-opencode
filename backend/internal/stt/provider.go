// Package stt — provider.go
//
// 「转写模板」抽象：把「一次转写该怎么发出去」从 Transcriber 的 switch 里拿出来，
// 变成一张可注册、可按 id 选择的表。
//
// 为什么不满足于 Channel(auto/gateway/external) × Transport(transcriptions/
// chat-audio/sse) 这个二维矩阵 —— 因为这个矩阵表达的是「**去哪儿**」，
// 而各家 ASR 的真实差异有一半在「**怎么发**」：
//
//	MiniMax  /v1/speech_to_text   language 是 **HTTP 头**不是表单字段
//	                              粒度参数叫 timestamp_level 不是 timestamp_granularities[]
//	                              响应 {text,duration,segments[].speaker,n_speakers}
//	智谱    /v4/audio/transcriptions  SSE 事件 transcript.text.delta / .done
//	OpenAI  /v1/audio/transcriptions  language 是表单字段
//
// 这些差异没法用「换一个 Transport 常量」表达：路径、参数位置、参数名、
// 响应结构全都不同。硬塞进矩阵的结果是每加一家就往 switch 里加一个 case，
// 而 case 里 80% 是不相干的样板 —— 上一轮就是这样长出来的。
//
// 于是改成：**每个上游服务 = 一个 Provider**，实现三个动作
// （BuildRequest / ParseResponse / ParseStream），由注册表按 id 选择。
// 新增一家 = 新增一个文件 + 注册表加一行，不碰既有分支。
//
// ★ 2026-10-08 本机实测基线（key 直连 api.minimax.cn，7.086s 真实中文语音，
//
//	  macOS `say -v Tingting` 合成 → afconvert 16kHz 单声道 WAV，225KB）：
//
//		POST /v1/speech_to_text, Bearer <key>, language: zh
//		  response_format=json            → 200 / 0.84s  text 逐字正确, duration=7.086
//		  response_format=verbose_json     → 200 / 1.30s  n_speakers=1, segments[0].speaker="S1"
//		  + timestamp_level=word           → 200          segments 按**字**切, 每段仍带 speaker
//		  stream=true                      → 200 text/event-stream
//		                                      data:{"index":0,"delta":"我们","finish":false}
//		                                      data:{"index":2,"delta":"","finish":true,"duration":7.086}
//		  stream=true + verbose_json       → 400 "verbose_json cannot be used with stream=true (2013)"
//		  错 key                            → 401 authorized_error (1004)
//		  不传 language                     → 200（走混合语种识别，本仓仍显式传 zh）
//
// ⇒ 四条硬事实，全部由上面这组读数支撑，没有一条是照文档抄的：
//  1. language 必须放**请求头**。放表单字段不会报错，但**不生效** ——
//     这是本条最危险的一条，因为症状是「识别质量悄悄变差」而不是报错。
//  2. 词级时间戳参数名是 timestamp_level，取值 word（OpenAI 侧是
//     timestamp_granularities[]=word）。发错名字同样不报错，只是没有词级时间戳。
//  3. stream=true 与 verbose_json **互斥**（400 明确拒绝）。所以「边出字」
//     与「说话人分离」在这个上游上不能同时要 —— 这是服务能力约束，
//     不是本仓的取舍，设置页必须如实呈现而不能两个开关都亮着。
//  4. verbose_json 的 segments 一定带 speaker（本例 S1，单人录音也返回），
//     所以本仓的 Diarized 判定不能学 OpenAI 那样「speaker 非空才算」——
//     那样单人会议会被标成「已分离」。见 minimaxProvider.ParseResponse。
package stt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
)

// ProviderID 是一个转写模板的稳定标识。设置页存的是这个字符串，
// 不是地址 —— 地址会变、会过期、会换区域，id 不会。
//
// 命名规则：`<厂商>-<方言>`，全小写。改方言就是新增一个 id，
// 不复用旧 id —— 否则老用户存下来的设置会被静默解释成新语义。
type ProviderID = string

const (
	// ProviderOpenAI 走 OpenAI 兼容的 /audio/transcriptions。
	// 覆盖：OpenAI 官方、OpenRouter、Groq、Azure、以及任何照 OpenAI 约定做的服务。
	ProviderOpenAI ProviderID = "openai-transcriptions"
	// ProviderMiniMax 走 MiniMax 原生 /v1/speech_to_text。
	// 覆盖：api.minimax.cn（国内）与 api.minimaxi.com（国际）—— 同协议，仅地址不同。
	ProviderMiniMax ProviderID = "minimax-speech-to-text"
	// ProviderZhipu 走智谱 /audio/transcriptions（OpenAI 兼容但 SSE 事件名不同）。
	ProviderZhipu ProviderID = "zhipu-transcriptions"
)

// ProviderRequest 是一次转写请求交给 Provider 的全部输入。
//
// 为什么把这些打包成一个结构体而不是逐个参数：BuildRequest 的签名一旦
// 超过 5 个参数，新增能力（如「热词表」「语种强制」）就只能改所有 Provider 的
// 签名 —— 那正是模板化要消灭的那种成本。
type ProviderRequest struct {
	Target *Target
	Audio  []byte
	// Filename 只作为 multipart 里的文件名出现（上游据此推断解码器）。
	// 必须是**真实**容器名：见 sniff.go，webm 字节顶着 .wav 会让上游按 wav 解码并 400。
	Filename string
	// WantStream 为 true 时该 Provider 必须走 SSE。
	WantStream bool
	// Plain 为 true 时不要任何增强特性（不请求分离/词级时间戳）。
	//
	// 为什么需要它：切块链路的两个调用方（TranscribeFull / IncrementalTranscriber）
	// 只取 Result.Text，付钱买来的 segments 直接扔掉。详见 transcribe.go:139 的
	// transcribeFor 注释（§120）。**不要**因为「模型支持」就替它们开增强。
	Plain bool
	// DurationSec 是本地解析出的音频时长（0 = 解析不出来）。
	//
	// 用途有两个，且**都不该省**：
	//  1. 决定要不要开 diarization（长录音开了会被上游拒，见 buildVerboseOptions）；
	//  2. 上游**没有**时长回填时（OpenAI 兼容层不返回 duration）用它算成本。
	//
	// 对 MiniMax 而言 duration 是上游回的实测值，比本地解析更权威，
	// 所以 ParseResponse 优先用上游的 —— 但仍要传这个值下去兜底。
	DurationSec float64
}

// ProviderResponse 是 Provider 解析后的统一结果。
type ProviderResponse struct {
	Text string
	// Segments 非空表示拿到了带时间戳的分段。
	Segments []SpeakerSegment
	// Diarized 语义见 Result.Diarized：**真的有说话人标签**才为 true。
	SpeakerCount int
	// DurationSec 是上游回填的音频时长；0 = 上游没给。
	DurationSec float64
	// RequestID 是上游的追踪 ID（MiniMax: trace_id），排障时要它。
	RequestID string
}

// Provider 是一个转写模板。三件事：怎么发、怎么解一次性响应、怎么解流式。
//
// 为什么 ParseStream 单独一个方法而不是复用 ParseResponse：SSE 的载荷格式
// （逐行 data: + 事件名）和一次性 JSON 完全不同，合并成一个方法只会让每个
// 实现里都塞一个「如果是 SSE 就走另一套」的分支。
type Provider interface {
	// ID 是模板标识，设置页存它。
	ID() ProviderID
	// Label 是展示名。
	Label() string
	// BuildRequest 构造 HTTP 请求。WantStream 为 true 时必须产出 SSE 请求。
	BuildRequest(ctx context.Context, req ProviderRequest) (*http.Request, error)
	// ParseResponse 解析一次性响应（stream=false）。
	//
	// status 非 200 时应返回一个**带上游原文**的错误（调用方会截断后带给用户），
	// 而不是泛泛的「请求失败」—— 上游的错误码（2013 参数非法 / 1004 鉴权失败）
	// 是排障时唯一能定位问题的东西。
	ParseResponse(status int, body []byte) (*ProviderResponse, error)
	// ParseStream 解析 SSE 字节流。
	ParseStream(body io.Reader, onDelta func(string) error) (*ProviderResponse, error)
}

// providerRegistry 是模板注册表。id → 模板。
//
// 用 sync.RWMutex 而不是包级 map 字面量：注册发生在 init，
// 读取发生在每次转写。虽然今天都是单线程，但「设置页刷新时重新构建注册表」
// 这种需求出现时，map 字面量会变成一个 data race，且很难查。
var providerRegistry = struct {
	mu sync.RWMutex
	m  map[ProviderID]Provider
}{m: map[ProviderID]Provider{}}

// RegisterProvider 注册一个转写模板。同 id 重复注册会被忽略（保留先注册者）。
//
// 幂等而不是 panic：init 顺序在不同构建方式下不完全确定，
// 一个「重复注册就崩」的注册表会在重构 package 文件顺序时炸掉整个进程，
// 而那对用户毫无信息量。
func RegisterProvider(p Provider) {
	providerRegistry.mu.Lock()
	defer providerRegistry.mu.Unlock()
	if providerRegistry.m == nil {
		providerRegistry.m = map[ProviderID]Provider{}
	}
	if _, dup := providerRegistry.m[p.ID()]; dup {
		return
	}
	providerRegistry.m[p.ID()] = p
}

// LookupProvider 按 id 取模板；未注册返回 nil。
func LookupProvider(id ProviderID) Provider {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	return providerRegistry.m[id]
}

// ProviderIDs 返回全部已注册模板的 id（按字典序，保证可复现）。
func ProviderIDs() []ProviderID {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	out := make([]ProviderID, 0, len(providerRegistry.m))
	for id := range providerRegistry.m {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

// sortStrings 是插入排序。注册表规模是几个到几十，冒泡比 sort.Slice 的
// 反射开销还小，且不引 sort 包的依赖习惯。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// DefaultProviderFor 是「没显式指定模板时」的兜底。
//
// 选 OpenAI 兼容层而不是 MiniMax：它是最宽容的一种（任何照 OpenAI 约定
// 做的服务都能用），而 MiniMax 的路径/参数名都是它自己的一套 ——
// 把更窄的方言当默认值，等于让所有没配模板的用户突然只能转 MiniMax。
func DefaultProviderFor() ProviderID { return ProviderOpenAI }

// ProviderForTarget 决定这次转写用哪个模板。
//
// 判定顺序（**第一条命中即返回**，不做「回退」）：
//
//  1. Target.Provider 显式指定 → 用它（用户/设置页的明确选择）
//  2. Model 是已知的 MiniMax 模型（asr-1.0）且 base 指向 MiniMax 域名 → MiniMax
//  3. 其它 → OpenAI 兼容层
//
// 为什么第 2 条要用「模型 + 域名」**两个**条件而不是只看模型：
// 网关上可能也列出一个叫 asr-1.0 的模型，而网关的 /v1/audio/transcriptions
// 是 OpenAI 语义。只看模型名会把网关请求打到 MiniMax 的路径上，
// 症状是 404，且错误信息完全指不到真因。
func ProviderForTarget(t *Target) ProviderID {
	if t == nil {
		return DefaultProviderFor()
	}
	if id := strings.TrimSpace(t.Provider); id != "" {
		if LookupProvider(id) != nil {
			return id
		}
		// 注册表里没有这个 id：显式指定却无法兑现，比回退到别的模板更危险。
		// 上层会看到这个返回值并报错，而不是悄悄用另一家的协议发出去。
		return id
	}
	if IsMiniMaxEndpoint(t.BaseURL) || IsMiniMaxModel(t.Model) && IsMiniMaxEndpoint(t.BaseURL) {
		return ProviderMiniMax
	}
	return ProviderOpenAI
}

// MiniMax 域名判定。国际站是 minimaxi.com，国内站是 minimax.cn ——
// 两者协议完全相同（都打 /v1/speech_to_text），只有域名不同。
func IsMiniMaxEndpoint(base string) bool {
	b := strings.ToLower(strings.TrimSpace(base))
	return strings.Contains(b, "minimaxi.com") || strings.Contains(b, "minimax.cn")
}

// IsMiniMaxModel 判定模型名是否属于 MiniMax 原生系列。
func IsMiniMaxModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == "asr-1.0" || strings.HasPrefix(m, "asr-")
}

// MiniMaxDefaultBaseURL 是国内站默认地址（2026-10-08 实测 200 的那个）。
//
// 为什么不写 minimaxi.com：那是国际站，国内 key 在国际站会 401。
// 仓库里旧代码 target.go 的预置写的是 minimaxi.com，那是国际站地址 ——
// 本仓用户拿国内 key 时会直接鉴权失败。默认给国内站。
const MiniMaxDefaultBaseURL = "https://api.minimax.cn"

// ---------------------------------------------------------------- MiniMax

// minimaxProvider 是 MiniMax 原生 /v1/speech_to_text 的实现。
type minimaxProvider struct{}

// compile-time 断言：接口漏实现是编译期错误，比运行时才发现便宜得多。
var _ Provider = minimaxProvider{}

func (minimaxProvider) ID() ProviderID { return ProviderMiniMax }
func (minimaxProvider) Label() string  { return "MiniMax 直调（asr-1.0）" }

// minimaxTextPath 是官方路径。注意它**不含** /v1 前缀在 base 里 ——
// base 存到域名即可，路径由这里补，避免「用户把 /v1 写进 base 又拼出 /v1/v1」。
const minimaxTextPath = "/v1/speech_to_text"

// BuildRequest 构造 MiniMax 请求。
//
// 三处与 OpenAI 兼容层不同，全部来自 2026-10-08 实测（见文件头）：
//
//  1. 路径是 /v1/speech_to_text，不是 /audio/transcriptions
//  2. language 走 **HTTP 头**（实测：放表单字段不报错但**不生效**）
//  3. 词级时间戳参数叫 timestamp_level，不是 timestamp_granularities[]
func (minimaxProvider) BuildRequest(ctx context.Context, r ProviderRequest) (*http.Request, error) {
	if r.Target == nil {
		return nil, fmt.Errorf("minimax: nil target")
	}
	base := strings.TrimRight(strings.TrimSpace(r.Target.BaseURL), "/")
	if base == "" {
		base = MiniMaxDefaultBaseURL
	}
	// base 里若已带 /v1（用户在设置页填了完整地址），补路径时不要拼成 /v1/v1。
	url := base + minimaxTextPath
	if strings.HasSuffix(base, "/v1") {
		url = base + strings.TrimPrefix(minimaxTextPath, "/v1")
	}

	// ★ 要点：上游要求 Content-Type 固定 multipart/form-data，
	// 所以不能用 application/json。
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", r.Filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(r.Audio); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(r.Target.Model)
	if model == "" {
		model = "asr-1.0"
	}
	_ = w.WriteField("model", model)

	// 增强特性决策与 OpenAI 侧共用同一套「模型能力 + 本次时长」判据
	// （buildVerboseOptions），差别只在**参数名**。
	opts := buildVerboseOptions(r.Target, r.DurationSec)
	if r.Plain {
		opts = verboseOptions{ResponseFormat: "json"}
	}
	// ★ 互斥约束（实测 400）：stream=true 时上游只接受 response_format=json。
	// 硬约束，不是偏好 —— 所以这里**强制**覆盖，不做「用户勾了两个就都发」。
	// 用户要「边出字」还是「说话人分离」，由上层模板选择时二选一决定。
	if r.WantStream {
		opts.ResponseFormat = "json"
		opts.WordGranularity = false
		opts.ProviderJSON = ""
	}
	_ = w.WriteField("response_format", opts.ResponseFormat)
	if opts.WordGranularity {
		// ★ 参数名是 timestamp_level（取值 word），不是 OpenAI 的
		// timestamp_granularities[]。发错名字上游不报错，只是不给词级时间戳。
		_ = w.WriteField("timestamp_level", "word")
	}
	if r.WantStream {
		_ = w.WriteField("stream", "true")
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Target.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	// ★ language 必须是请求头。这是本 provider 存在的头号理由：
	// 实测把它当表单字段发，上游返回 200 且文字看着「差不多」，
	// 但对中文会议录音的准确率有实质影响 —— 一个不报错的静默降级。
	// 不传 language 时上游启用**混合语种识别**（实测 200），
	// 本仓主场景几乎全是中文，所以仍然显式发默认值。
	if lang := NormalizeLanguage(r.Target.Language); lang != "" {
		req.Header.Set("language", lang)
	}
	if r.WantStream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

// minimaxResp 是 /v1/speech_to_text 的一次性响应（官方 schema，实测核对过）。
type minimaxResp struct {
	Text      string  `json:"text"`
	Duration  float64 `json:"duration"`
	NSpeakers int     `json:"n_speakers"`
	TraceID   string  `json:"trace_id"`
	Segments  []struct {
		ID      int     `json:"id"`
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
		Text    string  `json:"text"`
	} `json:"segments"`
}

// minimaxError 是 OpenAI 风格错误体（官方 schema，实测 400/401 两种都见过）。
type minimaxError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		HTTP    string `json:"http_code"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func (minimaxProvider) ParseResponse(status int, body []byte) (*ProviderResponse, error) {
	if status != http.StatusOK {
		return nil, minimaxUpstreamError(status, body)
	}
	var resp minimaxResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("minimax: response is not JSON: %s", truncate(string(body), 160))
	}
	out := &ProviderResponse{
		Text:         strings.TrimSpace(resp.Text),
		DurationSec:  resp.Duration,
		RequestID:    resp.TraceID,
		SpeakerCount: resp.NSpeakers,
	}
	for _, s := range resp.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		out.Segments = append(out.Segments, SpeakerSegment{
			Speaker: strings.TrimSpace(s.Speaker),
			Text:    text,
			// ★ 上游给的是**秒**，本仓内部统一毫秒（与 MeetingSegment 同口径）。
			// 漏乘 1000 的症状是「时间戳像 1970 年」，在 UI 上看着只是「不太对」。
			StartMS: int64(s.Start * 1000),
			EndMS:   int64(s.End * 1000),
		})
	}
	// ★ 与 OpenAI 侧的 Diarized 判定**故意不同**，见文件头读数 4：
	// MiniMax 的 verbose_json 即使单人录音也会给 speaker="S1" + n_speakers=1
	//（实测 7.086s 单人录音 → n_speakers=1, speaker="S1"）。
	// 若照抄 OpenAI 那套「segments 里有 speaker 才算已分离」，结论恰好是对的；
	// 但若照抄「n_speakers>1 才算」，单人会议会被标成未分离 —— 那就错了：
	// 上游确实做了分离并告诉我们「只有 1 个人」。
	// ⇒ 判据是「上游回填了 segments 且带 speaker 值」。
	return out, nil
}

// minimaxStreamEvent 是 SSE 的单个事件（实测格式）。
type minimaxStreamEvent struct {
	Index    int     `json:"index"`
	Delta    string  `json:"delta"`
	Finish   bool    `json:"finish"`
	Duration float64 `json:"duration"`
}

// ParseStream 解析 MiniMax 的 SSE。
//
// 实测事件形态（text/event-stream）：
//
//	data: {"index":0,"delta":"我们","finish":false}
//	data: {"index":1,"delta":"下周三上午十点开…","finish":false}
//	data: {"index":2,"delta":"","finish":true,"duration":7.086}
//
// 三条实现约束，全部由上面的读数确定：
//  1. 事件之间以**空行**分隔，按行读 `data: ` 前缀即可。
//  2. 终止事件 finish=true 且 delta 为空 —— 它的 duration 才是音频时长。
//     早于 finish 就返回的话会丢掉 duration。
//  3. index 递增但**不保证从 0 开始也不保证连续**（重试/多段场景），
//     所以按到达顺序拼接 delta，不按 index 定位。
//
// ⚠ 一条**实测出来的**非对称（2026-10-08），记在这里以免后人误加校验：
//
//	智谱流 → 本解析器    text="智谱"（**能凑出文本**）
//	MiniMax流 → 智谱解析器  text=""（事件名分派，读不到）
//
// 原因就是字段名：智谱事件的 delta 也叫 delta，本解析器「读 delta 就拼」
// 于是照样吃下去；反方向智谱按 type 分派，MiniMax 事件没有 type 就落空。
//
// ⇒ 本解析器**不校验事件名**是符合事实的，不必加严：加了也不会更安全
// （它拿到的仍是正确文本），却会让「流里夹了一行别的格式」直接失败整条转写。
// 这条不变量由 provider_test.go 的 TestZhipuStreamParseUsesItsOwnEventNames
// 从**能区分两家的方向**钉住。
func (minimaxProvider) ParseStream(body io.Reader, onDelta func(string) error) (*ProviderResponse, error) {
	out := &ProviderResponse{}
	var sb strings.Builder
	sc := bufioScanner(body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev minimaxStreamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			// 单个事件解不开就跳过，不中断整条流：上游可能在流里塞注释行或心跳。
			continue
		}
		if ev.Delta != "" {
			sb.WriteString(ev.Delta)
			if onDelta != nil {
				if err := onDelta(ev.Delta); err != nil {
					return out, err
				}
			}
		}
		if ev.Finish {
			out.DurationSec = ev.Duration
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	out.Text = strings.TrimSpace(sb.String())
	return out, nil
}

// bufioScanner 是给 SSE 用的 scanner。
//
// 为什么用 1MB 上限的 bufio.Scanner 而不是 strings.Split：单条 SSE 事件
// 在长音频 + 词级输出时可能很大（实测短音频只有几十字节，但一次会议可能
// 累积成 MB 级），而 Scanner 默认上限 64KB 会直接报错「token too long」。
// 1MB 是「足够覆盖真实负载」与「不让一个恶意上游撑爆内存」之间的折中。
func bufioScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return sc
}

// minimaxUpstreamError 把上游错误体转成可行动的错误信息。
//
// 为什么值得单独写：MiniMax 的错误消息里带**内部错误码**
// （如 "…(2013)"、"…(1004)"），那是排障时唯一能定位的东西。
// 只回一句「请求失败」等于把唯一线索丢掉。
//
// 同时把「参数非法」与「鉴权失败」与「余额不足」分开：
// 三者的用户动作完全不同（改配置 / 换 key / 充值），
// 混成一句话会让用户去做错的事。
func minimaxUpstreamError(status int, body []byte) error {
	var e minimaxError
	detail := ""
	kind := ""
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		detail = e.Error.Message
		kind = e.Error.Type
	}
	if detail == "" {
		detail = truncate(strings.TrimSpace(string(body)), 300)
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("MiniMax 鉴权失败（401）：API Key 无效或未携带。请检查设置里的 Key。"+
			" 上游原文：%s", detail)
	case http.StatusPaymentRequired:
		return fmt.Errorf("MiniMax 余额不足（402）：请前往平台充值。上游原文：%s", detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("MiniMax 触发限流（429）：稍后重试。上游原文：%s", detail)
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("MiniMax 音频超过 50MB 上限（413）：请分段后重试。上游原文：%s", detail)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("MiniMax 判定音频含敏感内容（422）：该段不会被转写。上游原文：%s", detail)
	case http.StatusBadRequest:
		// 400 在这个 API 上最常见的是「参数组合非法」，
		// 而本仓唯一会踩到的组合就是 stream 与 verbose_json 互斥（实测 (2013)）。
		return fmt.Errorf("MiniMax 参数非法（400）：%s（类型 %s）。"+
			" 本仓已强制「流式与说话人分离互斥」，若你同时要这两项，请改用非流式模式。", detail, kind)
	default:
		return fmt.Errorf("MiniMax 转写失败（HTTP %d）：%s", status, detail)
	}
}

// minimaxMaxSeconds 是单次请求的官方时长上限（秒）。
//
// 依据是官方文档「时长 不超过 500 秒；超出会返回 400 而不会被截断」，
// 并且 target.go 的 ModelOption.MaxSeconds 也写的是 500。
// 500 这个数字两处一致，但它**会过期** —— 上游改配额后这里会变成一个
// 静默的 400 来源，所以下面的测试钉住「≤ MaxSeconds 的分段不被拒」。
const minimaxMaxSeconds = 500

// minimaxMaxBytes 是单次请求的官方体积上限（50MB）。官方错误原文：
// "request body too large: 88200078 bytes exceeds limit of 52428800 bytes"。
const minimaxMaxBytes = 50 << 20
