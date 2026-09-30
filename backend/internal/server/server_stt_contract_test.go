package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

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
