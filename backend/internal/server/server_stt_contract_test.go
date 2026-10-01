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
