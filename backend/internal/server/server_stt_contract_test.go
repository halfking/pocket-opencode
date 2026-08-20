package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

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
