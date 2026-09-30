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
}

// Transcribe 把音频（wav/mp3/m4a/webm）转写成文字。
//
// 三条硬约束，都是被真实网关行为逼出来的：
//  1. 目标由 resolver 决定（网关自动发现 / 用户手工指定 / env 兜底）。
//  2. 空文本算失败——上游返回 200 但没有内容不能当成功。
//  3. 命中 LooksLikeMissingAudio 一律判失败并说明原因——网关会收下音频却丢掉它，
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
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	transport := NormalizeTransport(target.Transport)
	if transport == TransportAuto {
		// 外部服务按 OpenAI 兼容约定走 /audio/transcriptions；网关在探测阶段
		// 已经把真实可用的形态写进 Target，这里只处理 auto 残留。
		transport = TransportTranscriptions
		if target.Channel == ChannelGateway {
			transport = TransportChatAudio
		}
	}

	var text, usedTransport string
	switch transport {
	case TransportChatAudio:
		status, chatText, usage, chatErr := tryChatAudio(ctx, t.client, target.BaseURL, target.APIKey, target.Model, audio)
		if chatErr != nil {
			return nil, fmt.Errorf("stt %s %d: %s", target.Model, status, firstLine(chatErr))
		}
		if LooksLikeMissingAudio(chatText) || (usage.TotalCharacters == 0 && strings.TrimSpace(chatText) == "") {
			return nil, fmt.Errorf("stt %s: upstream accepted the request but dropped the audio "+
				"(this gateway does not forward input_audio); pick a model whose probe status is ok", target.Model)
		}
		text, usedTransport = chatText, TransportChatAudio
	case TransportTranscriptions:
		status, trText, trErr := t.transcriptions(ctx, target, audio, filename)
		if trErr != nil {
			return nil, fmt.Errorf("stt %s %d: %s", target.Model, status, firstLine(trErr))
		}
		text, usedTransport = trText, TransportTranscriptions
	default:
		return nil, fmt.Errorf("stt: unsupported transport %q", transport)
	}

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
	}
	if secs, ok := wavDurationSeconds(audio); ok && secs > 0 {
		res.DurationMS = int64(secs * 1000)
		if perHour := target.CostUSDPerHour; perHour > 0 {
			res.CostCents = perHour * secs / 3600 * 100
		}
	}
	return res, nil
}

func (t *Transcriber) transcriptions(ctx context.Context, target *Target, audio []byte, filename string) (int, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return 0, "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return 0, "", err
	}
	_ = w.WriteField("model", target.Model)
	_ = w.WriteField("response_format", "json")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(target.BaseURL, "/")+"/audio/transcriptions", &buf)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+target.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", fmt.Errorf("%s", truncate(strings.TrimSpace(string(raw)), 300))
	}
	var apiResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return resp.StatusCode, "", fmt.Errorf("response is not JSON: %s", truncate(string(raw), 160))
	}
	return resp.StatusCode, apiResp.Text, nil
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
