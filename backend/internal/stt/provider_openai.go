// Package stt — provider_openai.go
//
// OpenAI 兼容的 /audio/transcriptions 模板。
//
// 为什么把它也做成一个 Provider（而不是继续留在 Transcriber 的 switch 里）：
// 注册表是**闭合**的 —— 只有注册进来的模板才可能被选中。
// 「未注册的 provider id 会被显式报错」这条性质，只有在 switch 变成查表之后
// 才成立；而它挡掉的是一整类最难查的缺陷：拿 A 家的地址配 B 家的模型，
// 代码按 B 的参数发出去，上游要么静默忽略（不报错、结果不对），
// 要么返回一个指不到真因的错误。
//
// 本文件的行为**逐条对齐**既有的 transcriptions() 实现（2026-10-08），
// 不新增也不删减语义：language 走表单字段、词级时间戳走
// timestamp_granularities[]、简体偏置走 prompt、diarization 拒绝时降级重试。
// 那些注释里的实测依据（繁体输出、AudioIgnored 幻觉、长录音分离被拒）继续有效。
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

type openaiProvider struct{}

var _ Provider = openaiProvider{}

func (openaiProvider) ID() ProviderID { return ProviderOpenAI }
func (openaiProvider) Label() string  { return "OpenAI 兼容（/audio/transcriptions）" }

// BuildRequest 构造 OpenAI 兼容请求。
//
// 与 MiniMax 的三处差异都在这里体现（这也是本文件存在的意义）：
// language 是**表单字段**、粒度参数是 timestamp_granularities[]、
// 路径是 /audio/transcriptions。
func (openaiProvider) BuildRequest(ctx context.Context, r ProviderRequest) (*http.Request, error) {
	if r.Target == nil {
		return nil, fmt.Errorf("openai: nil target")
	}
	durationSec := r.DurationSec
	if durationSec <= 0 {
		durationSec = 0
	}
	verbose := buildVerboseOptions(r.Target, durationSec)
	if r.Plain {
		verbose = verboseOptions{ResponseFormat: "json"}
	}
	// OpenAI 兼容层的 /audio/transcriptions 不支持 stream=true（SSE 是另一条路，
	// 由 TransportChatAudio 覆盖）。所以这里显式拒绝而不是悄悄发一个
	// 会被上游忽略的参数 —— 静默忽略会让「我开了流式」变成一个假承诺。
	if r.WantStream {
		return nil, fmt.Errorf("openai: %s 不支持流式（/audio/transcriptions 无 SSE）", ProviderOpenAI)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", r.Filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(r.Audio); err != nil {
		return nil, err
	}
	_ = w.WriteField("model", r.Target.Model)
	_ = w.WriteField("response_format", verbose.ResponseFormat)
	if verbose.WordGranularity {
		_ = w.WriteField("timestamp_granularities[]", "word")
	}
	if verbose.ProviderJSON != "" {
		_ = w.WriteField("provider", verbose.ProviderJSON)
	}
	lang := NormalizeLanguage(r.Target.Language)
	if lang != "" {
		_ = w.WriteField("language", lang)
	}
	// 简体偏置：whisper 系会把简体的中文输出成繁体（2026-10-01 faster-whisper 实测）。
	// 放在单一入口（而不是各 Target 构造点）才能覆盖全部外部通道。
	if strings.HasPrefix(lang, "zh") {
		_ = w.WriteField("prompt", SimplifiedChineseBiasPrompt)
	}
	w.Close()

	url := strings.TrimRight(r.Target.BaseURL, "/") + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Target.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, nil
}

func (openaiProvider) ParseResponse(status int, body []byte) (*ProviderResponse, error) {
	if status != http.StatusOK {
		return nil, fmt.Errorf("%s", truncate(strings.TrimSpace(string(body)), 300))
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
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("response is not JSON: %s", truncate(string(body), 160))
	}
	segs, _ := speakerSegmentsFrom(apiResp.Segments)
	out := &ProviderResponse{Text: strings.TrimSpace(apiResp.Text), Segments: segs}
	if len(segs) > 0 {
		out.SpeakerCount = countDistinctSpeakers(segs)
	}
	return out, nil
}

// ParseStream 对本 provider 直接报不支持，而不是返回空结果。
//
// 为什么不用「返回空结果」：调用方会把它当成「转写成功但没文字」，
// 那是个静默缺陷。诚实报错让上层知道该换模板。
func (openaiProvider) ParseStream(io.Reader, func(string) error) (*ProviderResponse, error) {
	return nil, fmt.Errorf("openai: %s 不支持 SSE 流式；需要流式请用网关通道或 MiniMax 模板", ProviderOpenAI)
}

// countDistinctSpeakers 数一下分段里有几种 speaker 标签。
func countDistinctSpeakers(segs []SpeakerSegment) int {
	seen := map[string]struct{}{}
	for _, s := range segs {
		if v := strings.TrimSpace(s.Speaker); v != "" {
			seen[v] = struct{}{}
		}
	}
	return len(seen)
}

// init 注册内置模板。
//
// 为什么在这里 init 而不是散落在各自的包里：注册表是**单一真相**，
// 「有哪些模板可选」必须在一个地方能列全。散开注册的话，
// 新增一个 Provider 的人很容易忘记注册，然后得到一个
// 「代码在、但 LookupProvider 返回 nil」的死 provider。
func init() {
	RegisterProvider(minimaxProvider{})
	RegisterProvider(openaiProvider{})
	RegisterProvider(zhipuProvider{})
}
