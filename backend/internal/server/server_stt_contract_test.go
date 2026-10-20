package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// TestSttTranscribeResponseCarriesCostAndDuration 成功响应必须带成本与时长。
//
// frontend/src/api/stt.ts 的 cloudTranscribe() 已经在读 res.costCents，
// 但这个端点过去只回 {text, confidence}——契约写了一半。后果是云端转写的
// 成本统计永远是 undefined，而且**不报任何错**，属于最难查的那类缺口。
// 负控对照：把 handleSttTranscribe 的响应 map 改回两个字段，本条立刻转红。
func TestSttTranscribeResponseCarriesCostAndDuration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"今天下午三点开项目评审会。"}`)
	}))
	defer upstream.Close()

	srv, tokens := newWorkspaceIsolationServer(t)
	srv.SetSTTHTTPClient(upstream.Client())
	srv.transcriber = stt.NewResolver(func(context.Context, stt.Scope) (*stt.Target, error) {
		return &stt.Target{
			BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "gpt-4o-mini-transcribe",
			Transport: stt.TransportTranscriptions, Channel: stt.ChannelExternal,
			CostUSDPerHour: 0.18, Language: "zh",
		}, nil
	})

	body := &strings.Builder{}
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\n")
	body.Write(stt.ToneWAV(8000, 1000))
	body.WriteString("\r\n--X--\r\n")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader(body.String()))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Text       string  `json:"text"`
		CostCents  float64 `json:"costCents"`
		DurationMS int64   `json:"durationMs"`
		Model      string  `json:"model"`
		Channel    string  `json:"channel"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if got.Text == "" {
		t.Errorf("text 为空")
	}
	// 1 秒 WAV、0.18 美元/小时 → 0.005 美分
	if got.CostCents <= 0 {
		t.Errorf("costCents=%v，云端转写的成本统计会一直是 undefined", got.CostCents)
	}
	if got.DurationMS < 900 || got.DurationMS > 1100 {
		t.Errorf("durationMs=%d，1 秒 WAV 解析失败", got.DurationMS)
	}
	if got.Model != "gpt-4o-mini-transcribe" || got.Channel != "external" {
		t.Errorf("响应里没有这次用的模型/通道：model=%q channel=%q", got.Model, got.Channel)
	}
}

// STT 未装配时必须返回**带错误码**的 503。
//
// 前端 api/error-message.ts:extractErrorCode() 只认 `code: 说明` 这种
// 前缀格式，`ERROR_CODE_I18N_KEYS` 里 stt_unavailable → errors.sttNotConfigured。
// 不带码就只能落到通用 not configured 文案（"该功能尚未完成配置"），
// 用户不知道该去配语音转写服务。
func TestSttTranscribeUnconfiguredCarriesErrorCode(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = nil

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader("raw audio"))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured STT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "stt_unavailable:") {
		t.Fatalf("响应必须带 stt_unavailable 错误码前缀，实际：%s", rr.Body.String())
	}
}

func TestSttTranscribeAcceptsRawAudio(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader("raw audio"))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "audio/webm; codecs=opus")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("raw STT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "invalid body") {
		t.Fatalf("raw audio was decoded as JSON: %s", rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("raw STT content type=%q, want application/json", got)
	}
}

func TestSttTranscribeRejectsEmptyRawAudio(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty raw STT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "empty audio data") {
		t.Fatalf("empty raw STT body=%s", rr.Body.String())
	}
}

func TestSttTranscribeRejectsUnsupportedContentType(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader("raw audio"))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unsupported STT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unsupported content type") {
		t.Fatalf("unsupported STT body=%s", rr.Body.String())
	}
}

// §922：音频**太大**必须与「请求体无效」分开。
//
// 缺陷本体（**实测**）：前端发的是 JSON + base64（`frontend/src/api/stt.ts`），
// 而服务端对 `/api/stt/transcribe*` 的**整个请求体**限 25 MiB
// （`requestBodyLimitMiddleware` 的 `maxAudioBodyBytes`）。
// ⇒ body 超限时，`json.NewDecoder(r.Body).Decode()` 读到的是**被截断的半截 JSON**
// ⇒ decode 必然失败 ⇒ 旧实现回 **400 "invalid body"**
// ⇒「音频太大」被说成「请求体无效」⇒ 用户完全无从判断
// （**实测**：26 MiB 的 base64 body 拿到的就是 400 "invalid body"）。
//
// 修法：先读 body，并把两种结论分开 ——「读超了」（*http.MaxBytesError）
// ⇒ **413 + stt_audio_too_large**（前端 `extractErrorCode` 取得到 ⇒ 有中文文案）；
// 「JSON 坏了」⇒ 仍是 400 "invalid body"（**不改变既有语义**）。
//
// ⚠ 附带一条**实测事实**（本节读数）：因为前端发的是 base64（体积 ×4/3），
// 而外层卡的是**请求体** ⇒ 25 MiB 的 body 解码后只有 ~18.75 MiB
// ⇒ 服务端那道**内层**的 `len(audioData) > 25<<20` 闸**永远不触发**
// ⇒ 它那条 "audio too large (max 25 MB)" 文案对 JSON 形态是**死文案**。
func TestSttTranscribeOversizedBodyIsNotReportedAsInvalidBody(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")

	// body 必须**超过外层上限**（25 MiB）才会碰到 MaxBytesReader。
	// 用合法 base64 字符（'A'），长度取 4 的倍数 ⇒ JSON 本身是合法的
	// ⇒ 这样返回 400 就只可能是因为「被截断了」，不会与「JSON 坏了」混淆。
	big := strings.Repeat("A", (25<<20)+4096)
	body := `{"audioBase64":"` + big + `","filename":"a.wav"}`
	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限 STT status=%d body=%s —— 期望 413：「太大」要说「太大」，不能混进「请求体无效」",
			rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "stt_audio_too_large") {
		t.Fatalf("超限 STT body=%s —— 缺少稳定错误码 stt_audio_too_large ⇒ 前端映射不到中文文案",
			rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "invalid body") {
		t.Fatalf("超限 STT 被说成「请求体无效」：%s", rr.Body.String())
	}
}

// 负控：**真的**坏 JSON 仍然回 400 "invalid body"。
//
// ⚠ 这条不许被上面的改动带跑偏：把「太大」和「无效」分开是对的，
// 但不能顺手把「无效」也改成 413 ⇒ 客户端就分不清「重试有用」还是「重试没用」。
func TestSttTranscribeStillReportsInvalidJSONAsBadRequest(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader(`{"audioBase64":`))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 的 STT status=%d body=%s —— 期望 400（不是 413）", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid body") {
		t.Fatalf("坏 JSON 的 STT body=%s —— 期望仍是 invalid body", rr.Body.String())
	}
}
func TestAudioFilenameForContentType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		want        string
	}{
		{contentType: "audio/webm; codecs=opus", want: "audio.webm"},
		{contentType: "audio/mpeg", want: "audio.mp3"},
		{contentType: "audio/x-custom", want: "audio.bin"},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := audioFilenameForContentType(tc.contentType); got != tc.want {
				t.Fatalf("audioFilenameForContentType(%q)=%q, want %q", tc.contentType, got, tc.want)
			}
		})
	}
}
