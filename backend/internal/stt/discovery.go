package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 自动发现：从网关 /models 里挑出可能做语音转写的模型，并**逐个真探测**，
// 而不是只看模型名。2026-10-01 实测 llm.kxpms.cn 的三条硬事实决定了这里的形状：
//
//  1. /models 会把 gpt-audio / gpt-audio-mini 标成 modality=audio，把
//     mimo-v2.5-asr 这种纯 ASR 模型错标成 modality=text —— 所以候选筛选必须
//     「modality + 名字」双路，单看 modality 会漏掉 mimo。
//  2. 目录里有 ≠ 能用：这三个模型全部 503 no_candidate（网关列了但没有上游）。
//     不探测就报「已配置可用」是假的。
//  3. 网关的 /chat/completions 会**收下音频却把它丢掉**，返回 200 + 一段
//     「您似乎没有附上录音文件」的幻觉文本。必须显式识别这种情况，否则转写
//     文本会带着一句废话静默写进用户的会议记录。

// asrNameRe 命中即视为 ASR 候选（配合 modality 一起判定）。
var asrNameRe = regexp.MustCompile(`(?i)(asr|whisper|transcri|speech|audio|omni|voice)`)

// ttsNameRe 命中即视为语音**合成**（TTS）模型，需要排除。
//
// 2026-10-01 真机实测发现的缺陷：asrNameRe 里的 `voice` 会把
// `mimo-v2.5-tts-voiceclone`、`mimo-v2.5-tts-voicedesign` 这两个**语音合成**
// 模型拉进 ASR 候选。代价有三，都不是"看着别扭"这种小事：
//  1. 探测预算是硬约束（maxProbeCandidates=6，网关限流实测 12 次/分钟），
//     两个注定失败的槽位被 TTS 吃掉，真正可能可用的 ASR 模型反而探不到；
//  2. 设置页会把"语音合成模型"列在"语音转写模型"分组下，用户完全看不懂；
//  3. 真机录音失败文案里出现 `mimo-v2.5-tts-voiceclone=网关无上游 provider`——
//     用户看到"转写失败"却收到两个合成模型名，比只报一句通用文案更困惑。
var ttsNameRe = regexp.MustCompile(`(?i)(tts|voice-?clon|voice-?design|voice-?id|text-?to-?speech|speak)`)

// strongASRRe 是强 ASR 标记：名字里同时带 TTS 词和这些词时，仍然按 ASR 算。
// 例：`whisper-tts` 这种混合命名不应该被 ttsNameRe 误杀。
var strongASRRe = regexp.MustCompile(`(?i)(asr|whisper|transcri|speech-?to-?text|stt)`)

// GatewayModel 是网关 /models 里的一个条目。
type GatewayModel struct {
	ID       string `json:"id"`
	Modality string `json:"modality"`
	Family   string `json:"family"`
}

// IsASRCandidate 判定一个网关模型是否值得探测。
func IsASRCandidate(m GatewayModel) bool {
	// 先排 TTS：合成模型不可能做转写，除非名字里另有强 ASR 标记。
	if ttsNameRe.MatchString(m.ID) && !strongASRRe.MatchString(m.ID) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(m.Modality), "audio") {
		return true
	}
	return asrNameRe.MatchString(m.ID)
}

// 探测结论。
const (
	// ProbeOK：这次探测真的拿到了转写文本。
	ProbeOK = "ok"
	// ProbeNoProvider：网关列了模型但没有可用上游（503 no_candidate）。
	ProbeNoProvider = "no_provider"
	// ProbeEndpointMissing：目标传输形态的端点不存在（404）。
	ProbeEndpointMissing = "endpoint_missing"
	// ProbeAudioIgnored：上游返回 200，但音频被静默丢弃（幻觉文本）。
	ProbeAudioIgnored = "audio_ignored"
	// ProbeFailed：其他错误。
	ProbeFailed = "failed"
)

// Candidate 是一个网关 ASR 候选的探测结果。
type Candidate struct {
	Model    string `json:"model"`
	Modality string `json:"modality"`
	Family   string `json:"family"`
	// Status 取 Probe* 常量。
	Status string `json:"status"`
	// Transport 探测成功时的传输形态。
	Transport string `json:"transport,omitempty"`
	// SampleText 探测时上游返回的文本片段（成功时是转写结果，失败时用于说明原因）。
	SampleText string `json:"sampleText,omitempty"`
	Detail     string `json:"detail,omitempty"`
	ProbedAt   int64  `json:"probedAt"`
}

// Usable 报告该候选是否可以真正用来转写。
func (c Candidate) Usable() bool { return c.Status == ProbeOK }

// DiscoveryResult 是一次网关扫描的完整结论。
type DiscoveryResult struct {
	BaseURL    string      `json:"baseURL"`
	TotalModels int        `json:"totalModels"`
	Candidates []Candidate `json:"candidates"`
	ScannedAt  int64       `json:"scannedAt"`
	// Error 非空表示连模型目录都没拉到（例如 key 无效）。
	Error string `json:"error,omitempty"`
}

// UsableCandidates 只返回探测通过的候选。
func (d DiscoveryResult) UsableCandidates() []Candidate {
	out := make([]Candidate, 0, len(d.Candidates))
	for _, c := range d.Candidates {
		if c.Usable() {
			out = append(out, c)
		}
	}
	return out
}

// Best 给出当前最该用的候选：探测通过列表的第一个。
func (d DiscoveryResult) Best() (Candidate, bool) {
	uc := d.UsableCandidates()
	if len(uc) == 0 {
		return Candidate{}, false
	}
	return uc[0], true
}

// maxProbeCandidates 限制单次扫描的探测数量：探测是真实出网请求，
// 而网关限流很紧（实测 12 次/分钟），无上限会把设置页点成 429。
const maxProbeCandidates = 6

// DiscoveryCache 按「地址+key 指纹」缓存探测结论，TTL 10 分钟。
// 设置页反复打开不应该反复打网关。
type DiscoveryCache struct {
	mu   sync.Mutex
	ttl  time.Duration
	now  func() time.Time
	data map[string]cacheEntry
}

type cacheEntry struct {
	result  DiscoveryResult
	expires time.Time
}

// NewDiscoveryCache 构造缓存；ttl <= 0 时用 10 分钟。
func NewDiscoveryCache(ttl time.Duration) *DiscoveryCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &DiscoveryCache{ttl: ttl, now: time.Now, data: map[string]cacheEntry{}}
}

func cacheKey(baseURL, apiKey string) string {
	// key 只留指纹，绝不进缓存 key 的明文形态被日志打出来。
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "|" + shortFingerprint(apiKey)
}

func shortFingerprint(s string) string {
	if s == "" {
		return "nokey"
	}
	sum := 0
	for _, r := range s {
		sum = sum*31 + int(r)
	}
	return fmt.Sprintf("k%x", sum)
}

func (c *DiscoveryCache) get(baseURL, apiKey string) (DiscoveryResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[cacheKey(baseURL, apiKey)]
	if !ok || c.now().After(e.expires) {
		return DiscoveryResult{}, false
	}
	return e.result, true
}

func (c *DiscoveryCache) put(baseURL, apiKey string, r DiscoveryResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[cacheKey(baseURL, apiKey)] = cacheEntry{result: r, expires: c.now().Add(c.ttl)}
}

// Peek 返回缓存中的结论（不触发探测）。
func (c *DiscoveryCache) Peek(baseURL, apiKey string) (DiscoveryResult, bool) {
	return c.get(baseURL, apiKey)
}

// Seed 预置一条探测结论。
//
// 这是**测试注入点**，不是生产路径：生产上探测结论只能由 Discover 产生。
// 之所以要导出它，是因为 internal/server 的测试要构造「网关探测已跑完、结论是
// no_provider」这种确定性前置状态，而 put/get 都是本包私有方法，跨包够不着。
// 2026-10-01 并入 feat/2026-10-01-stt-service 时恢复出来的 server 测试就依赖它。
func (c *DiscoveryCache) Seed(baseURL, apiKey string, r DiscoveryResult) {
	c.put(baseURL, apiKey, r)
}

// ListGatewayModels 拉取网关模型目录。
func ListGatewayModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]GatewayModel, error) {
	body, status, err := getJSON(ctx, client, strings.TrimRight(baseURL, "/")+"/models", apiKey)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %d", status)
	}
	var resp struct {
		Data []GatewayModel `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("models response is not JSON: %w", err)
	}
	return resp.Data, nil
}

// Discover 扫描网关并逐个探测候选模型。
// 已有新鲜缓存时直接返回缓存。
func Discover(ctx context.Context, client *http.Client, cache *DiscoveryCache, baseURL, apiKey string, force bool) (DiscoveryResult, error) {
	if !force && cache != nil {
		if r, ok := cache.Peek(baseURL, apiKey); ok {
			return r, nil
		}
	}
	res := DiscoveryResult{BaseURL: strings.TrimRight(baseURL, "/"), ScannedAt: time.Now().Unix()}
	models, err := ListGatewayModels(ctx, client, res.BaseURL, apiKey)
	if err != nil {
		res.Error = err.Error()
		if cache != nil {
			cache.put(baseURL, apiKey, res)
		}
		return res, err
	}
	res.TotalModels = len(models)
	for _, m := range models {
		if IsASRCandidate(m) {
			res.Candidates = append(res.Candidates, Candidate{
				Model: m.ID, Modality: m.Modality, Family: m.Family, ProbedAt: time.Now().Unix(),
			})
		}
	}
	// 探测顺序：先探推荐预置的三个（设置页默认关心的），再探其余。
	probed := map[string]bool{}
	probe := func(model string) {
		if probed[model] || len(probed) >= maxProbeCandidates {
			return
		}
		probed[model] = true
		out := ProbeModel(ctx, client, res.BaseURL, apiKey, model)
		for i := range res.Candidates {
			if res.Candidates[i].Model == model {
				res.Candidates[i].Status = out.Status
				res.Candidates[i].Transport = out.Transport
				res.Candidates[i].SampleText = out.SampleText
				res.Candidates[i].Detail = out.Detail
				res.Candidates[i].ProbedAt = out.ProbedAt
			}
		}
	}
	for _, opt := range RecommendedGatewayModels() {
		probe(opt.Model)
	}
	for _, c := range res.Candidates {
		probe(c.Model)
	}
	// 没进探测列表的候选标成未探测，别让设置页显示成「不可用」。
	for i := range res.Candidates {
		if res.Candidates[i].Status == "" {
			res.Candidates[i].Status = ProbeFailed
			res.Candidates[i].Detail = "not probed (probe budget exhausted)"
		}
	}
	if cache != nil {
		cache.put(baseURL, apiKey, res)
	}
	return res, nil
}

// ProbeModel 用一段合成音频真实打一次目标模型，判定它到底能不能转写。
// 依次尝试两种传输形态：OpenAI 兼容 /audio/transcriptions，然后
// chat/completions + input_audio。
func ProbeModel(ctx context.Context, client *http.Client, baseURL, apiKey, model string) Candidate {
	out := Candidate{Model: model, ProbedAt: time.Now().Unix()}
	audio := ToneWAV(8000, 400)

	// 形态 1：OpenAI 兼容 /audio/transcriptions
	status, text, transport, err := tryTranscriptions(ctx, client, baseURL, apiKey, model, "probe.wav", audio)
	switch {
	case err == nil && status == http.StatusOK:
		if LooksLikeMissingAudio(text) {
			out.Status, out.Transport, out.SampleText, out.Detail = ProbeAudioIgnored, transport, text, "upstream returned 200 but did not consume the audio"
			return out
		}
		if strings.TrimSpace(text) == "" {
			out.Status, out.Transport, out.Detail = ProbeFailed, transport, "upstream returned empty text"
			return out
		}
		out.Status, out.Transport, out.SampleText = ProbeOK, transport, text
		return out
	case isNoProvider(status, err):
		out.Status, out.Detail = ProbeNoProvider, providerDetail(status, err)
		return out
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		// 端点不存在，换下一种形态继续。
	default:
		out.Status, out.Detail = ProbeFailed, firstLine(err)
		if status != 0 {
			out.Detail = fmt.Sprintf("http %d: %s", status, firstLine(err))
		}
		return out
	}

	// 形态 2：chat/completions + input_audio
	status2, text2, usage, err2 := tryChatAudio(ctx, client, baseURL, apiKey, model, audio)
	if err2 != nil || status2 != http.StatusOK {
		if isNoProvider(status2, err2) {
			out.Status, out.Detail = ProbeNoProvider, providerDetail(status2, err2)
			return out
		}
		// 走到这里说明 /audio/transcriptions 已经是 404/405（见上面的 switch）。
		// 如果 chat 侧同样是 404/405，那就是「这个网关压根没开转写端点」，
		// 而不是「探测过程出错了」。
		//
		// 不单独判出去的后果很具体：会落到 ProbeFailed，而
		// server_stt_settings.go 的 describeProbe 对 ProbeFailed 拼的是
		// "探测失败(" + Detail + ")"，Detail 里带着上游返回的原始响应体，
		// 于是设置页直接把这坨东西甩给用户：
		//   探测失败(http 404: {"error":{"code":"no_candidate",…}})
		// ProbeEndpointMissing 本来就配了「无转写端点」这句中文文案，
		// 但在这之前生产代码从没给它赋过值，是个死常量。
		// TestProbeClassifiesBothTransportsMissing 守住这条。
		if status2 == http.StatusNotFound || status2 == http.StatusMethodNotAllowed {
			out.Status, out.Detail = ProbeEndpointMissing,
				"网关未提供转写端点（/audio/transcriptions 与 chat 音频输入均为 404/405）"
			return out
		}
		detail := firstLine(err2)
		if status2 != 0 {
			detail = fmt.Sprintf("http %d: %s", status2, detail)
		}
		if status == http.StatusNotFound && detail == "" {
			detail = "neither /audio/transcriptions nor chat audio input is available"
		}
		out.Status, out.Detail = ProbeFailed, detail
		return out
	}
	if LooksLikeMissingAudio(text2) || usage.TotalCharacters == 0 {
		out.Status, out.Transport, out.SampleText = ProbeAudioIgnored, TransportChatAudio, text2
		out.Detail = "upstream returned 200 but the audio was dropped (gateway does not forward input_audio)"
		return out
	}
	if strings.TrimSpace(text2) == "" {
		out.Status, out.Transport, out.Detail = ProbeFailed, TransportChatAudio, "upstream returned empty text"
		return out
	}
	out.Status, out.Transport, out.SampleText = ProbeOK, TransportChatAudio, text2
	return out
}

// ChatUsage 是网关 chat/completions 响应里的用量字段。
// TotalCharacters 是网关自己的计数器：实测音频被丢掉时它是 0，
// 这是比文本匹配更硬的证据，所以两个信号都用。
type ChatUsage struct {
	TotalCharacters int `json:"total_characters"`
}

func tryTranscriptions(ctx context.Context, client *http.Client, baseURL, apiKey, model, filename string, audio []byte) (int, string, string, error) {
	var buf bytes.Buffer
	boundary := "pocketsttprobe"
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Disposition: form-data; name=\"file\"; filename=\"" + filename + "\"\r\n")
	buf.WriteString("Content-Type: audio/wav\r\n\r\n")
	buf.Write(audio)
	buf.WriteString("\r\n--" + boundary + "\r\n")
	buf.WriteString("Content-Disposition: form-data; name=\"model\"\r\n\r\n" + model + "\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Disposition: form-data; name=\"response_format\"\r\n\r\njson\r\n")
	buf.WriteString("--" + boundary + "--\r\n")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/audio/transcriptions", &buf)
	if err != nil {
		return 0, "", TransportTranscriptions, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", TransportTranscriptions, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", TransportTranscriptions, fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return resp.StatusCode, "", TransportTranscriptions, fmt.Errorf("response is not JSON: %s", truncate(string(raw), 160))
	}
	return resp.StatusCode, parsed.Text, TransportTranscriptions, nil
}

func tryChatAudio(ctx context.Context, client *http.Client, baseURL, apiKey, model string, audio []byte) (int, string, ChatUsage, error) {
	var usage ChatUsage
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "Transcribe the audio verbatim. Output only the transcript."},
				{"type": "input_audio", "input_audio": map[string]string{
					"data": base64.StdEncoding.EncodeToString(audio), "format": "wav",
				}},
			},
		}},
		"max_tokens": 512,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, "", usage, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, "", usage, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", usage, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", usage, fmt.Errorf("%s", truncate(strings.TrimSpace(string(raw)), 300))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage ChatUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return resp.StatusCode, "", usage, fmt.Errorf("response is not JSON: %s", truncate(string(raw), 160))
	}
	usage = parsed.Usage
	if len(parsed.Choices) == 0 {
		return resp.StatusCode, "", usage, fmt.Errorf("response has no choices")
	}
	return resp.StatusCode, extractText(parsed.Choices[0].Message.Content), usage, nil
}

// extractText 兼容 content 为字符串或内容块数组两种形态。
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
		}
		return sb.String()
	}
	return truncate(string(raw), 200)
}

// missingAudioRe 命中即认为「上游没收到音频」，返回的是幻觉文本而非转写。
//
// 2026-10-01 审计：补了独立的 `don'?t` / `do not` / `does not` 形式，并加上
// see|find|detect|listen。原来只覆盖 `didn't receive/get/hear` 与 `not see`，
// 于是网关最常见的一句「I don't see any audio file attached to this message.」
// 匹配不上 —— 而这正是本函数要拦的那类幻觉。漏掉就意味着它被当成转写结果写进
// 会议记录，且不报任何错。新增措辞与「正常转写」用例无交集（已回归验证）。
var missingAudioRe = regexp.MustCompile(`(?i)(no audio|(?:didn'?t|don'?t|do not|does not) (?:see|receiv|get|hear|find|detect|access|listen)|not (?:see|receiv|receiv\w*|access|accessible)|cannot (?:access|listen|hear)|unable to (?:access|listen|hear)|can'?t (?:access|listen|hear)|没有(?:附上|收到|听到|音频|录音|文件)|未(?:收到|听到)|无法(?:访问|听到|读取)(?:音频|录音)|抱歉.{0,12}(?:没有|未)|please (?:re-?upload|upload|provide|send) (?:the |your )?(?:audio|recording|file))`)

// LooksLikeMissingAudio 识别「200 但音频被丢弃」的幻觉回复。
// 2026-10-01 实测网关 auto 模型：拿到 base64 音频后回答
// 「您好，您似乎没有附上录音文件。请重新上传需要转写的音频」。
// 如果不拦，用户会得到一段含这句废话的会议记录，而且没有任何报错。
func LooksLikeMissingAudio(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	return missingAudioRe.MatchString(t)
}

// ToneWAV 生成指定采样率与时长的单声道 8bit PCM WAV（用于连通性探测）。
// 探测只需要「端点是否接受音频并返回文本」，不需要真实语音；
// 真实语音准确性由设置页的「用当前录音试转」验证。
func ToneWAV(sampleRate, durationMS int) []byte {
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	if durationMS <= 0 {
		durationMS = 400
	}
	n := sampleRate * durationMS / 1000
	data := make([]byte, n)
	for i := range data {
		// 440Hz 方波，音量压低，避免任何真实语音能量特征。
		if (i/(sampleRate/440))%2 == 0 {
			data[i] = 40
		} else {
			data[i] = 216
		}
	}
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+len(data)))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // PCM
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // mono
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // block align
	_ = binary.Write(buf, binary.LittleEndian, uint16(8))  // bits
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

func isNoProvider(status int, err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return status == http.StatusServiceUnavailable &&
		(strings.Contains(msg, "no_candidate") || strings.Contains(msg, "no available provider"))
}

func providerDetail(status int, err error) string {
	if err == nil {
		return fmt.Sprintf("http %d", status)
	}
	return truncate(strings.TrimSpace(err.Error()), 300)
}

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	return truncate(strings.TrimSpace(err.Error()), 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func getJSON(ctx context.Context, client *http.Client, url, apiKey string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return raw, resp.StatusCode, err
}
