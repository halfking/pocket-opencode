// Package stt — gateway_compat_test.go
//
// 2026-10-05 网关音频端点轮的兼容性回归锁。三条都是「网关+小米 MiMo
// 上游实测」逼出来的行为（证据见 llm-gateway-go
// docs/design/2026-10-03-audio-transcription-gateway-plan.md §7/§9）：
//
//  1. chat-audio 形态不得带 text part（小米 400 "must not include text
//     parts"；经网关还会被归并成 503 transient，表象更具误导性）；
//  2. input_audio.format 按上传扩展名推断（硬编码 wav 会把 mp3 数据
//     错标）；
//  3. 网关 /v1/audio/transcriptions 的 503 body 是 error.code=no_provider，
//     isNoProvider 必须认出来，否则设置页把「网关无上游」显示成
//     「探测失败(http 503: …)」。
package stt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTryChatAudioHasNoTextPart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"type":"text"`) {
			t.Errorf("chat-audio payload must not carry text parts (xiaomi 400s): %s", truncateForTest(string(body), 200))
		}
		if !strings.Contains(string(body), `"format":"mp3"`) {
			t.Errorf("input_audio.format must follow the upload extension, got: %s", truncateForTest(string(body), 200))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"回环文本"}}],"usage":{"total_characters":4}}`))
	}))
	defer srv.Close()

	status, text, usage, err := tryChatAudio(context.Background(), srv.Client(), srv.URL, "k", "mimo-v2.5-asr", "mp3", []byte("fake-mp3"))
	if err != nil || status != http.StatusOK {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if text != "回环文本" || usage.TotalCharacters != 4 {
		t.Fatalf("text=%q usage=%+v", text, usage)
	}
}

func TestTryChatAudioFormatDefaultsToWav(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"format":"wav"`) {
			t.Errorf("empty format must default to wav, got: %s", truncateForTest(string(body), 200))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}],"usage":{"total_characters":1}}`))
	}))
	defer srv.Close()

	if _, _, _, err := tryChatAudio(context.Background(), srv.Client(), srv.URL, "k", "m", "", []byte("x")); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestIsNoProviderRecognizesGatewayAudioCode(t *testing.T) {
	// 复刻网关 /v1/audio/transcriptions 503 body（2026-10-05 实测形状）。
	gatewayBody, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":    "no_provider",
			"message": "No audio provider available for model",
			"type":    "server_error",
		},
	})
	if !isNoProvider(http.StatusServiceUnavailable, errWithString(string(gatewayBody))) {
		t.Fatalf("gateway audio no_provider body must classify as ProbeNoProvider, got body=%s", gatewayBody)
	}
	// 反例：别的 503 不误伤。
	if isNoProvider(http.StatusServiceUnavailable, errWithString(`{"error":{"code":"overloaded"}}`)) {
		t.Fatalf("unrelated 503 must not classify as no_provider")
	}
	if isNoProvider(http.StatusBadGateway, errWithString(`no_provider`)) {
		t.Fatalf("non-503 status must not classify as no_provider")
	}
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

func errWithString(s string) error { return stringErr(s) }

func truncateForTest(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func TestDetectAudioFormatMagicBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"wav", append([]byte("RIFF"), append(make([]byte, 4), []byte("WAVE")...)...), "wav"},
		{"webm ebml", []byte{0x1A, 0x45, 0xDF, 0xA3, 0x00}, "webm"},
		{"mp3 id3", append([]byte("ID3"), 0, 0, 0, 0), "mp3"},
		{"mp3 framesync", []byte{0xFF, 0xFB, 0x90, 0x00}, "mp3"},
		{"ogg", append([]byte("OggS"), 0, 0, 0, 0), "ogg"},
		{"flac", append([]byte("fLaC"), 0, 0, 0, 0), "flac"},
		{"garbage", []byte("hello world this is not audio"), ""},
	}
	for _, c := range cases {
		if got := DetectAudioFormat(c.data); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
