// Package stt is the cloud speech-to-text engine behind POST /api/stt/transcribe.
//
// The app records locally (sherpa-onnx on native) and falls back to the cloud
// here when the on-device engine is unavailable or low-confidence. Which cloud
// model answers is *not* hardcoded any more: it is resolved per request from
// the user's STT settings plus a live probe of the LLM gateway, so a gateway
// that starts serving an ASR model is picked up without an app update, and the
// user can always override the choice in Settings.
//
// The API key stays server-side so it never ships inside the APK.
package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Scope 是一次转写请求的用户/工作区作用域。
// 转写目标可能按用户不同（设置页可手工调整），所以解析器必须看到作用域，
// 不能只靠进程级配置。
type Scope struct {
	UserID      string
	WorkspaceID string
}

// Resolver 解析出本次转写要用的目标。由 server 层注入（读用户设置 + 网关探测缓存）。
type Resolver func(ctx context.Context, scope Scope) (*Target, error)

// Transcriber 按目标执行转写。
type Transcriber struct {
	resolve Resolver
	client  *http.Client
	timeout time.Duration
}

// NewTranscriber 保留旧的静态目标构造（env 兜底路径与既有测试都依赖它）。
func NewTranscriber(apiKey, model, baseURL string) *Transcriber {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "whisper-large-v3-turbo"
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	t := &Transcriber{client: &http.Client{Timeout: 120 * time.Second}, timeout: 120 * time.Second}
	if strings.TrimSpace(apiKey) == "" {
		// 无 key：解析器直接报错，保持既有 502 契约。
		t.resolve = func(context.Context, Scope) (*Target, error) {
			return nil, fmt.Errorf("stt not configured: no API key for %s", baseURL)
		}
		return t
	}
	t.resolve = func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: baseURL, APIKey: apiKey, Model: model,
			Transport: TransportAuto, Channel: ChannelExternal, Label: "env",
		}, nil
	}
	return t
}

// NewResolver 构造按请求解析目标的转写器。
func NewResolver(r Resolver) *Transcriber {
	return &Transcriber{resolve: r, client: &http.Client{Timeout: 120 * time.Second}, timeout: 120 * time.Second}
}

// SetHTTPClient 供测试注入。
func (t *Transcriber) SetHTTPClient(c *http.Client) { t.client = c }

// Result 是一次成功转写的结果。
type Result struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	CostCents  float64 `json:"costCents,omitempty"`
	Model      string  `json:"model,omitempty"`
	Transport  string  `json:"transport,omitempty"`
	Channel    string  `json:"channel,omitempty"`
	Label      string  `json:"label,omitempty"`
	DurationMS int64   `json:"durationMs,omitempty"`

	// Provider 是本次实际使用的转写模板 id。
	//
	// 为什么回填它：调障时第一句要问的是「这次到底走的哪个模板」。
	// 只看 model 不够 —— 同一个 asr-1.0 既是 MiniMax 模板的默认模型，
	// 也可能是用户在外部服务里手填的模型名，协议完全不同。
	Provider string `json:"provider,omitempty"`

	// Diarized 为 true 时 Segments 带有服务端给出的说话人标签。
	//
	// 为什么单独一个布尔而不靠「Segments 非空」判断：开了 verbose_json
	// 但音频只有一个人说话时，上游仍会返回 segments，而 speaker 字段可能
	// 为空/null。上层要区分的是「**没有**说话人信息」与「有，且只有一个」。
	Diarized bool `json:"diarized,omitempty"`

	// Segments 是上游按说话人切分的结果（仅在请求了 verbose_json 且上游
	// 真的返回了 segments 时非空）。
	Segments []SpeakerSegment `json:"segments,omitempty"`
}

// SpeakerSegment 是一段带说话人标签的转写。
//
// 时间单位统一为**毫秒**，与 MeetingSegment.startMs/endMs 同口径
// （前端 MeetingSegment 用的就是 ms，两处不一致会引入一次静默的
// 「时间戳看起来像 1970 年」的错误）。
type SpeakerSegment struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

// Transcribe 把音频（wav/mp3/m4a/webm）转写成文字。
//
// 三条硬约束，都是被真实网关行为逼出来的：
//  1. 目标由 resolver 决定（网关自动发现 / 用户手工指定 / env 兜底）。
//  2. 空文本算失败——上游返回 200 但没有内容不能当成功。
//  3. 命中 LooksLikeMissingAudio 一律判失败并说明原因——网关会收下音频却丢掉它，
//
// Transcribe 把音频（wav/mp3/m4a/webm）转写成文字，使用空作用域（进程级兜底）。
func (t *Transcriber) Transcribe(ctx context.Context, audio []byte, filename string) (*Result, error) {
	return t.TranscribeFor(ctx, Scope{}, audio, filename)
}

// TranscribeFor 同 Transcribe，但带上用户/工作区作用域以解析出该用户的目标。
//
// 三条硬约束，都是被真实网关行为逼出来的：
//  1. 目标由 resolver 决定（网关自动发现 / 用户手工指定 / env 兜底）。
//  2. 空文本算失败——上游返回 200 但没有内容不能当成功。
//  3. 命中 LooksLikeMissingAudio 一律判失败并说明原因——网关会收下音频却丢掉它，
//     然后返回一段「您似乎没有附上录音文件」的幻觉文本。
func (t *Transcriber) TranscribeFor(ctx context.Context, scope Scope, audio []byte, filename string) (*Result, error) {
	return t.transcribeFor(ctx, scope, audio, filename, false)
}

// transcribeFor 是 TranscribeFor 的内层形态，多一个「不要增强特性」的开关。
//
// ★ §120 为什么需要它：buildVerboseOptions 会按**模型能力 + 本次音频时长**
//
//	  自动开 diarization / 词级时间戳（见 transcribe.go:240-265）。
//	  而**切块链路的两个调用方根本不读这些字段**：
//
//		TranscribeFull        逐 ≤25 秒块调，只取 Result.Text 收进 SegmentResult
//		IncrementalTranscriber 逐 5~8 秒块调，只取 Result.Text 做增量合并
//
//		SegmentResult（full.go:70）只有 Index/StartSec/EndSec/Text/Error，
//		IncrementalResult 也没有说话人字段 —— 即两处都**付了钱拿回来就扔**。
//
//		为什么要 forcePlain 而不是把它们接上去：§31.3 已经论证过，
//		服务端分离要在**整段音频**上做才有意义（跨段一致），
//		而这两条链路都在送出去之前先切碎了 ⇒ 拼起来的说话人身份是碎的，
//		接上去等于给用户一份「Speaker 1 / Speaker 2 / Speaker 1」的噪音。
//		§31.3 还记着一次教训：把 segments 加进前端类型后查消费者，零个，于是撤回。
//		⇒ 对称的做法是**别为扔掉的东西付钱**，而不是再接一遍没人读的数据。
//
//		⚠ 诚实的边界：本函数只改**请求参数**（response_format / provider /
//		  timestamp_granularities），取文本的代码路径完全相同（都是 apiResp.Text）。
//		  但「上游在 verbose_json 与 json 下返回的文字是否逐字相同」本轮**没有样本可证**
//		  （无网关凭据/无真实音频）。若哪天要撤回，这里是唯一需要实测的点。
func (t *Transcriber) transcribeFor(ctx context.Context, scope Scope, audio []byte, filename string, forcePlain bool) (*Result, error) {
	if t == nil || t.resolve == nil {
		return nil, fmt.Errorf("stt engine not configured")
	}
	target, err := t.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("stt not configured: no transcription target resolved")
	}
	if strings.TrimSpace(target.APIKey) == "" {
		return nil, fmt.Errorf("stt not configured: missing API key for %s", target.BaseURL)
	}
	if strings.TrimSpace(target.Model) == "" {
		return nil, fmt.Errorf("stt not configured: no ASR model selected")
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("stt: empty audio")
	}
	if filename == "" {
		filename = "audio.wav"
	}
	// 语种归一化放在这里而不是各个 Target 构造点：Target 有 5 处构造
	// （设置页外部/网关自动/网关手动、试转外部/网关、env 兜底），漏一处就等于
	// 中文录音在那个入口上被按英语转写。复制一份再改，不动调用方的结构体。
	shallow := *target
	shallow.Language = NormalizeLanguage(target.Language)
	target = &shallow
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	transport := NormalizeTransport(target.Transport)
	if transport == TransportAuto {
		// 统一默认：OpenAI 兼容 /audio/transcriptions。
		// 网关（llm.kxpms.cn）自 2026-10 起已提供该端点，并在内部适配
		// MiniMax speech_to_text / 智谱 multipart / 小米 chat-audio。
		// 旧默认「网关 → chat-audio」会绕过统一入口，且与接入标准冲突。
		// 探测阶段若判定只能走 chat-audio，会把 Transport 写死进 Target，
		// 不会落到本分支的 auto 残留。
		transport = TransportTranscriptions
	}

	// ★ chat-audio 不是「转写模板」而是一种**形态**：它打的是 /chat/completions +
	// input_audio，响应是 OpenAI 风格的 choices[].message，而不是任何 ASR 的
	// 识别结果结构。所以它留在 switch 里，不进 provider 注册表 ——
	// 硬塞进注册表会让 Provider 接口背上「有的实现不走 ASR 端点」的别扭。
	// 模板化覆盖的是「ASR 端点之间怎么发」这一层，chat-audio 不属于那一层。
	if transport == TransportChatAudio {
		status, chatText, usage, chatErr := tryChatAudio(ctx, t.client, target.BaseURL, target.APIKey, target.Model, audioFormatFromFilename(filename), audio)
		if chatErr != nil {
			return nil, fmt.Errorf("stt %s %d: %s", target.Model, status, firstLine(chatErr))
		}
		if LooksLikeMissingAudio(chatText) || (usage.TotalCharacters == 0 && strings.TrimSpace(chatText) == "") {
			return nil, fmt.Errorf("stt %s: upstream accepted the request but dropped the audio "+
				"(this gateway does not forward input_audio); pick a model whose probe status is ok", target.Model)
		}
		return t.finish(ctx, target, chatText, TransportChatAudio, nil, false, audio)
	}

	// 其余一律走 provider 注册表（transcriptions / sse 都从这里进）。
	status, pText, segs, dia, pErr := t.viaProvider(ctx, target, audio, filename, forcePlain, transport == TransportSSE)
	if pErr != nil {
		if status != 0 {
			return nil, fmt.Errorf("stt %s %d: %s", target.Model, status, firstLine(pErr))
		}
		return nil, pErr
	}
	usedTransport := transport
	if usedTransport == "" || usedTransport == TransportAuto {
		usedTransport = TransportTranscriptions
	}
	return t.finish(ctx, target, pText, usedTransport, segs, dia, audio)
}

// viaProvider 经模板注册表执行一次转写。
//
// 为什么模板 id 解析不出来时**在这里就报错**、而不是回退到默认模板：
// 回退的症状是「配了 A 家的地址，按 B 家的协议发出去」——
// 轻则 404，重则上游静默忽略参数、返回一个看起来合理但错误的转写。
// 显式报错让配置错误在第一次转写时就说清楚。
func (t *Transcriber) viaProvider(ctx context.Context, target *Target, audio []byte, filename string, forcePlain, wantStream bool) (int, string, []SpeakerSegment, bool, error) {
	providerID := ProviderForTarget(target)
	p := LookupProvider(providerID)
	if p == nil {
		return 0, "", nil, false, fmt.Errorf(
			"stt: 未知的转写模板 %q（可选：%s）", providerID, strings.Join(ProviderIDs(), "、"))
	}
	provReq := ProviderRequest{
		Target: target, Audio: audio, Filename: filename,
		WantStream: wantStream, Plain: forcePlain,
		// 时长解析不出来时传 0：buildVerboseOptions 会保守地不开增强特性，
		// 不影响普通转写（与既有 transcriptions() 的口径一致）。
		DurationSec: func() float64 {
			if d, ok := wavDurationSeconds(audio); ok {
				return d
			}
			return 0
		}(),
	}
	req, err := p.BuildRequest(ctx, provReq)
	if err != nil {
		return 0, "", nil, false, fmt.Errorf("stt %s: %w", target.Model, err)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", nil, false, err
	}
	defer resp.Body.Close()

	if wantStream {
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			pr, perr := p.ParseResponse(resp.StatusCode, raw)
			if perr == nil {
				return resp.StatusCode, pr.Text, nil, false, nil
			}
			return resp.StatusCode, "", nil, false, perr
		}
		pr, perr := p.ParseStream(resp.Body, nil)
		if perr != nil {
			return resp.StatusCode, "", nil, false, perr
		}
		segs, dia := diarizationVerdict(pr)
		return resp.StatusCode, pr.Text, segs, dia, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, "", nil, false, err
	}
	pr, err := p.ParseResponse(resp.StatusCode, raw)
	if err != nil {
		// ★ 降级重试（diarization 接线里最要紧的一条）：开了分离开关而上游拒绝
		//（长录音的 408/500/503 diarization_unavailable），**同一段音频关掉开关就能转成功**。
		// 本项目主场景是会议，超过 15 分钟的会议是常态。
		if resp.StatusCode != http.StatusOK && isDiarizationRejection(resp.StatusCode, raw) {
			return t.viaProvider(ctx, target, audio, filename, true, wantStream)
		}
		return resp.StatusCode, "", nil, false, err
	}
	segs, dia := diarizationVerdict(pr)
	return resp.StatusCode, pr.Text, segs, dia, nil
}

// diarizationVerdict 判定「这次是否真的拿到了说话人」。
//
// 判据与 OpenAI 侧 speakerSegmentsFrom 的注释一致，但**放在这里**统一执行：
// 两个模板对「speaker 是否可信」的约定不同（MiniMax 单词录音也回 S1，
// OpenAI 侧可能给空串），所以判定必须读 Provider 回填的结果，
// 而不是靠某个模板的 JSON 形状。
func diarizationVerdict(pr *ProviderResponse) ([]SpeakerSegment, bool) {
	if pr == nil || len(pr.Segments) == 0 {
		return nil, false
	}
	for _, s := range pr.Segments {
		if strings.TrimSpace(s.Speaker) != "" {
			return pr.Segments, true
		}
	}
	return pr.Segments, false
}

// finish 收敛两条链路的公共收尾：去 think 标签、查空文本、查「没收到音频」、
// 回填时长与成本。
//
// 抽出来的理由是 chat-audio 与 provider 两条路**都要**过这四道守卫。
// 之前 chat-audio 走的是独立的早返回路径，等于把「空文本算失败」
// 与「AudioIgnored 幻觉算失败」这两条守卫只挂在 transcriptions 上 ——
// 少一道守卫的地方就是下一个静默缺陷的位置。
func (t *Transcriber) finish(ctx context.Context, target *Target, text, usedTransport string, segs []SpeakerSegment, diarized bool, audio []byte) (*Result, error) {
	_ = ctx
	text = strings.TrimSpace(stripThinkTags(text))
	if text == "" {
		return nil, fmt.Errorf("stt %s: upstream returned empty transcript", target.Model)
	}
	if LooksLikeMissingAudio(text) {
		return nil, fmt.Errorf("stt %s: upstream claims it received no audio, so this is not a transcript: %s",
			target.Model, truncate(text, 120))
	}
	res := &Result{
		Text: text, Confidence: 0.95,
		Model: target.Model, Transport: usedTransport,
		Channel: target.Channel, Label: target.Label,
		Provider: ProviderForTarget(target),
		Diarized: diarized,
		Segments: segs,
	}
	if secs, ok := wavDurationSeconds(audio); ok && secs > 0 {
		res.DurationMS = int64(secs * 1000)
		if perHour := target.CostUSDPerHour; perHour > 0 {
			res.CostCents = perHour * secs / 3600 * 100
		}
	}
	return res, nil
}

// verboseOptions 汇总这一次请求要带哪些「增强特性」。
type verboseOptions struct {
	// ResponseFormat 是 multipart 的 response_format 字段。
	ResponseFormat string
	// WordGranularity 为 true 时发 timestamp_granularities[]=word。
	WordGranularity bool
	// ProviderJSON 是 provider 字段的 JSON 串；空串表示不发。
	// 只有需要透传 Azure 侧开关（diarization / phraseList）时才非空。
	ProviderJSON string
}

// buildVerboseOptions 依据「模型能力 + 本次音频时长」决定要开哪些增强特性。
//
// ★ 这里就是 §9.1 记着「本仓尚未接线」的那三个开参。接线的同时必须解决
// 一个此前没人提的约束：**MAI 开了 diarization 就只支持约 15 分钟**，
// 而本项目主场景是会议，长会议是常态（见 ShouldRequestDiarization 的注释）。
// 因此按音频时长决定，而不是「模型支持就开」。
func buildVerboseOptions(target *Target, durationSec float64) verboseOptions {
	opts := verboseOptions{ResponseFormat: "json"}

	// 支持词级时间戳的模型：改用 verbose_json 并要 word 粒度。
	// 注意 WordTimestamps 与 Diarization 是**独立**的两个能力：
	// 前者失败顶多少一批时间戳，后者失败会整段 503（见下）。
	wordTS := supportsWordTimestamps(target.Model)
	diar := ShouldRequestDiarization(target.Model, durationSec)

	if !wordTS && !diar {
		return opts
	}
	opts.ResponseFormat = "verbose_json"
	if wordTS {
		opts.WordGranularity = true
	}
	if diar {
		opts.ProviderJSON = diarizationProviderJSON
	}
	return opts
}

// diarizationProviderJSON 是 OpenRouter 透传给 Azure MAI-Transcribe 的分离开关。
//
// 形态来自 OpenRouter 官方模型页与 quickstart：
//
//	"provider": { "options": { "azure": { "diarization": { "enabled": true } } } }
//
// ★ 为什么用 provider.options.azure 这层透传而不是顶层字段：MAI-Transcribe 是
// Azure Speech 的模型，这些开关在 Azure 的 REST 定义里，OpenRouter 只是原样转发。
// 写成顶层字段上游会忽略，**且不报错**——那就是一个「开了参但没生效」的静默缺陷。
const diarizationProviderJSON = `{"options":{"azure":{"diarization":{"enabled":true}}}}`

// recommendedWordTSOverride 是**测试钩子**，让判据能独立验证
// 「词级时间戳的参数名映射」而不必先改 ModelOption 里的能力声明。
//
// ⚠ 它在生产文件里，而不是 _test.go 里 —— 因为它要被 supportsWordTimestamps
// 引用，而那个函数在生产路径上。若只放在测试文件，
// `go build ./...` 会直接失败（实测踩过：undefined: recommendedWordTSOverride）。
//
// 安全性：它是包内私有、**没有任何写入者**（无 setter、无 env、无配置项），
// 所以生产二进制里恒为 false ⇒ 走原逻辑。它不构成一条「能被外部打开的开关」。
var recommendedWordTSOverride = false

func supportsWordTimestamps(model string) bool {
	// 见 recommendedWordTSOverride 的注释：仅测试会置真。
	if recommendedWordTSOverride {
		return true
	}
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.WordTimestamps
		}
	}
	return false
}

// transcriptions 走 OpenAI 兼容的 /audio/transcriptions。
//
// forcePlain=true 时**不带任何增强特性**（不请求 verbose_json、不带分离开关）。
//
// ★ 它现在有**两个**调用方（§120 之前只有一个，注释在这里烂掉过一轮）：
//  1. 降级重试：上游拒绝了 diarization 时，带开关重试一次（transcriptions 内部）；
//  2. **不读那些字段的调用方**：TranscribeFull 与 IncrementalTranscriber
//     只取 Result.Text ⇒ 不该为它们请求分离与词级时间戳。
//
// ⚠ 与 §121 的耦合（改段长时必读）：forcePlain 是**无条件**覆盖 verbose 的，
//
//	不看 audio 长度。所以即使有人把 maxSegmentSec 从 25 秒调大，
//	**TranscribeFull 也仍然不会请求 diarization** ——
//	「只调段长」单独做，对说话人标签没有任何效果。
//	要同时让分离生效，必须把这里的 forcePlain 改成
//	「段长 ≥ 某个阈值才不 forcePlain」的条件式。
//	否则会出现最难自查的现象：段长调了、什么都没变。
func (t *Transcriber) transcriptions(ctx context.Context, target *Target, audio []byte, filename string, forcePlain bool) (int, string, []SpeakerSegment, bool, error) {
	// 时长只用于判断能不能开 diarization/词级时间戳；解析不出来（=0）时
	// ShouldRequestDiarization 会保守地不开。不影响普通转写。
	durationSec, hasDuration := wavDurationSeconds(audio)
	if !hasDuration {
		durationSec = 0
	}
	verbose := buildVerboseOptions(target, durationSec)
	if forcePlain {
		verbose = verboseOptions{ResponseFormat: "json"}
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return 0, "", nil, false, err
	}
	if _, err := fw.Write(audio); err != nil {
		return 0, "", nil, false, err
	}
	_ = w.WriteField("model", target.Model)
	_ = w.WriteField("response_format", verbose.ResponseFormat)
	if verbose.WordGranularity {
		// multipart 里数组参数的惯例写法是 `名字[]`，OpenAI/OpenRouter 都认。
		_ = w.WriteField("timestamp_granularities[]", "word")
	}
	if verbose.ProviderJSON != "" {
		_ = w.WriteField("provider", verbose.ProviderJSON)
	}
	// 语种必须显式给：不传时 whisper / gpt-4o-transcribe 会自己猜，
	// 中文会议录音会被当成英语（见 Target.Language 的注释）。
	lang := NormalizeLanguage(target.Language)
	if lang != "" {
		_ = w.WriteField("language", lang)
	}
	// 简体偏置。2026-10-01 本机用 faster-whisper 实测（见 handoff §14）：
	// 简体的「帮我记一下明天要买牛奶和面包」被识别成繁体的
	// 「幫我記一下明天要買牛奶和麵包」——用字全对，只是字形不对。
	// 这是 **whisper 系模型的共同行为**，而设置页预置的外部候选里就有
	// openai/whisper-large-v3-turbo，用户写简体笔记却拿到繁体正文。
	// OpenAI 官方给的解法就是给一段普通话 initial_prompt 做偏置。
	//
	// 为什么放在这里（transcriptions 的 multipart 构造）而不是各个 Target
	// 构造点：和 language 归一化同一条理由 —— 构造点有 5 处，漏一处就等于
	// 那个入口仍然吐繁体。放单一入口则天然覆盖全部外部转写通道。
	//
	// 只在中文时加：给英文录音塞一段中文 prompt 纯属添乱。
	// ChatAudio 通道（网关多模态）没有 prompt 字段，那条路走的是聊天接口，
	// 由模型自己决定输出字形，不在此处处理。
	if strings.HasPrefix(lang, "zh") {
		_ = w.WriteField("prompt", SimplifiedChineseBiasPrompt)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(target.BaseURL, "/")+"/audio/transcriptions", &buf)
	if err != nil {
		return 0, "", nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+target.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", nil, false, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, "", nil, false, err
	}
	if resp.StatusCode != http.StatusOK {
		// ★ 降级而非失败（这是 diarization 接线里最要紧的一条）：
		// 开了分离开关而上游拒绝（长录音的 408/500/503 diarization_unavailable），
		// **同一段音频关掉开关就能转成功**。所以这里重试一次不带增强特性，
		// 拿不到说话人标签，但一定拿得到文字。
		//
		// 为什么必须这样：本项目主场景是会议，超过 15 分钟的会议是常态。
		// 若无脑开，用户会得到「短会议正常、长会议整段转写失败」这种
		// 极难自查的现象，而且他挑这个模型正是因为它是会议首选。
		if verbose.ProviderJSON != "" && isDiarizationRejection(resp.StatusCode, raw) {
			return t.transcriptions(ctx, target, audio, filename, true /* forcePlain */)
		}
		return resp.StatusCode, "", nil, false, fmt.Errorf("%s", truncate(strings.TrimSpace(string(raw)), 300))
	}
	var apiResp struct {
		Text     string `json:"text"`
		Segments []struct {
			Speaker string  `json:"speaker"`
			Text    string  `json:"text"`
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return resp.StatusCode, "", nil, false, fmt.Errorf("response is not JSON: %s", truncate(string(raw), 160))
	}

	segs, diarized := speakerSegmentsFrom(apiResp.Segments)
	return resp.StatusCode, apiResp.Text, segs, diarized, nil
}

// rawSpeakerSegment 是上游 verbose_json 里的一条 segment（解包后形态）。
type rawSpeakerSegment struct {
	Speaker string
	Text    string
	Start   float64
	End     float64
}

// speakerSegmentsFrom 把上游分段转成本仓形态，并判定「是否真的拿到了说话人」。
//
// ★ 为什么「有 segments」不等于「有说话人标签」：
// 开了 verbose_json 之后上游**一定**会给 segments（按语言/时间切），
// 但只有开了 diarization 且音频里确实有多个人时，speaker 才非空。
// 上层要区分这两种情况，所以 Diarized 只在**真的有 speaker 值**时为 true。
//
// 另一个必须处理的事实：speaker 可能是**数字**（MAI 返回 "0"/"1"）或
// 空串。空串的 segment 仍然保留（它的文字是有效内容），只是不算「被分离」。
// 丢掉它的文字才是真丢内容。
func speakerSegmentsFrom(raw []struct {
	Speaker string  `json:"speaker"`
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}) ([]SpeakerSegment, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	out := make([]SpeakerSegment, 0, len(raw))
	diarized := false
	for _, r := range raw {
		text := strings.TrimSpace(r.Text)
		if text == "" {
			continue
		}
		spk := strings.TrimSpace(r.Speaker)
		if spk != "" {
			diarized = true
		}
		out = append(out, SpeakerSegment{
			Speaker: spk,
			Text:    text,
			// 上游给的是**秒**（OpenAI/OpenRouter 的 verbose_json 口径），
			// 本仓内部统一毫秒。漏乘 1000 会让时间戳变成「1970 年的头一秒」，
			// 而那在 UI 上看起来只是「时间不太对」，极难发现。
			StartMS: int64(r.Start * 1000),
			EndMS:   int64(r.End * 1000),
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, diarized
}

// isDiarizationRejection 判断上游的错误是不是「拒绝了 diarization 这个开关」。
//
// 判据有两部分，缺一不可：
//  1. 状态码在 408/500/503 —— 微软文档对超长录音 + diarization 的三种返回；
//  2. 响应体里出现 diarization 相关的错误标识。
//
// 为什么不能只看状态码：500/503 对任何上游故障都会出现（限流、网关抖动）。
// 若把它们一律当成「分离被拒」而重试，会在真正的故障上多打一次请求，
// 把一次故障变成两次，还可能把限流打得更狠。反过来，只看错误文本则会漏掉
// 上游改了文案的情况——所以两者都查，**宁可漏重试也不误重试**（漏重试的后果是
// 用户看到一条明确的上游错误，误重试的后果是掩盖真实故障）。
func isDiarizationRejection(status int, body []byte) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusServiceUnavailable:
	default:
		return false
	}
	lowered := strings.ToLower(string(body))
	for _, marker := range []string{"diarization_unavailable", "diarization", "speaker"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// thinkRe 剥掉推理模型的 <think>…</think> 前缀。
// 网关上的部分模型带 reasoning，<think> 里的话不是转写内容。
var thinkOpen = "<think>"
var thinkClose = "</think>"

func stripThinkTags(s string) string {
	for {
		i := strings.Index(s, thinkOpen)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], thinkClose)
		if j < 0 {
			return strings.TrimSpace(s[:i])
		}
		s = s[:i] + s[i+j+len(thinkClose):]
	}
}

// wavDurationSeconds 从 WAV 头解析时长；非 PCM WAV 或解析失败返回 ok=false。
func wavDurationSeconds(data []byte) (float64, bool) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}
	var byteRate uint32
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := binary.LittleEndian.Uint32(data[pos+4 : pos+8])
		body := pos + 8
		if id == "fmt " && body+16 <= len(data) {
			byteRate = binary.LittleEndian.Uint32(data[body+8 : body+12])
		}
		if id == "data" {
			if byteRate == 0 {
				return 0, false
			}
			return float64(size) / float64(byteRate), true
		}
		pos = body + int(size) + (int(size) % 2)
	}
	return 0, false
}

// AudioBase64 供测试与调试复用：把任意字节转成标准 base64。
func AudioBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// audioFormatFromFilename 从上传文件名的扩展名推断 input_audio.format。
// 与网关侧的推断口径一致（网关 audioFileFormat 同一张表）：小米桥接仅
// 接受 wav/mp3，其它扩展名原样透传、由上游错误明示。
func audioFormatFromFilename(filename string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), ".")) {
	case "wav", "mp3", "webm", "ogg", "oga", "m4a", "mp4", "flac", "aac", "opus":
		return strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	default:
		return "wav"
	}
}
