//go:build stt_live_minimax

// 本文件是**实跑取证**探针（默认不参与编译/测试）。
//
// 跑法：
//
//	STT_LIVE_MINIMAX_KEY='sk-api-…' \
//	POCKET_STT_LIVE_AUDIO=/tmp/sttprobe/zh3.wav \
//	go test ./internal/stt/ -tags stt_live_minimax -run TestLiveMiniMax -v
//
// 为什么用 build tag 而不是读 env 后 skip：
// 「读到 key 就跑」会让**任何人**在本地跑 go test 时都意外地花钱、消耗配额，
// 而 CI 上更不该有出网调用。build tag 让「要不要花钱」变成一个显式选择，
// 且不会因为某天环境变量被继承而突然触发。
//
// 它验证的是**本仓代码路径**（不是 curl）：Transcriber → ProviderForTarget
// → minimaxProvider.BuildRequest → 真 API → ParseResponse/ParseStream。
// curl 只能证明「服务端能用」，证明不了「本仓的请求构造是对的」。
package stt

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// liveClientFor 造一个**直连**的客户端。
//
// 为什么必须直连：本机 HTTP(S)_PROXY 指向 127.0.0.1:7890，实测 MiniMax 域名
// 走代理时 TLS 正常，但为了让读数不受代理层干扰（代理可能改写/缓存响应），
// 这里显式关掉代理。
func liveClientFor() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{Transport: tr, Timeout: 120 * time.Second}
}

func liveKey(t *testing.T) string {
	t.Helper()
	k := strings.TrimSpace(os.Getenv("STT_LIVE_MINIMAX_KEY"))
	if k == "" {
		t.Skip("set STT_LIVE_MINIMAX_KEY to run the live probe (this spends real quota)")
	}
	return k
}

func liveAudio(t *testing.T) []byte {
	t.Helper()
	p := strings.TrimSpace(os.Getenv("POCKET_STT_LIVE_AUDIO"))
	if p == "" {
		t.Skip("set POCKET_STT_LIVE_AUDIO to a real speech wav")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	return b
}

// TestLiveMiniMaxEndToEnd 走完整链路，一次请求拿到文字 + 说话人。
func TestLiveMiniMaxEndToEnd(t *testing.T) {
	key := liveKey(t)
	audio := liveAudio(t)

	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{
				BaseURL: MiniMaxDefaultBaseURL, APIKey: key, Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportTranscriptions,
				Channel: ChannelMiniMax, Language: "zh", Label: "live",
			}, nil
		},
		client: liveClientFor(), timeout: 120 * time.Second,
	}
	res, err := tr.Transcribe(context.Background(), audio, "live.wav")
	if err != nil {
		t.Fatalf("实跑失败: %v", err)
	}
	t.Logf("OK text=%q", res.Text)
	t.Logf("OK provider=%s transport=%s durationMs=%d diarized=%v segs=%d",
		res.Provider, res.Transport, res.DurationMS, res.Diarized, len(res.Segments))
	if res.Text == "" {
		t.Fatalf("空文本")
	}
	if res.Provider != ProviderMiniMax {
		t.Errorf("provider 回填错：%q", res.Provider)
	}
}

// TestLiveMiniMaxStream 走 SSE 链路，验证增量拼接 + duration。
func TestLiveMiniMaxStream(t *testing.T) {
	key := liveKey(t)
	audio := liveAudio(t)

	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{
				BaseURL: MiniMaxDefaultBaseURL, APIKey: key, Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportSSE,
				Channel: ChannelMiniMax, Language: "zh", Label: "live-sse",
			}, nil
		},
		client: liveClientFor(), timeout: 120 * time.Second,
	}
	res, err := tr.Transcribe(context.Background(), audio, "live.wav")
	if err != nil {
		t.Fatalf("实跑流式失败: %v", err)
	}
	t.Logf("OK stream text=%q durationMs=%d transport=%s", res.Text, res.DurationMS, res.Transport)
	if res.Text == "" {
		t.Fatalf("流式空文本")
	}
}

// TestLiveMiniMaxBadKey 验证 401 的错误映射在真上游上成立。
//
// 为什么值得单独跑：minimaxUpstreamError 的分支是用**实测 body** 写的判据，
// 但那是我自己 curl 出来的。若真上游的 401 body 形状不同，映射会掉到 default，
// 错误信息就不再告诉用户「是 key 的问题」。
func TestLiveMiniMaxBadKey(t *testing.T) {
	audio := liveAudio(t)
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{
				BaseURL: MiniMaxDefaultBaseURL, APIKey: "sk-api-definitely-invalid-key",
				Model: "asr-1.0", Provider: ProviderMiniMax,
				Transport: TransportTranscriptions, Language: "zh",
			}, nil
		},
		client: liveClientFor(), timeout: 30 * time.Second,
	}
	_, err := tr.Transcribe(context.Background(), audio, "live.wav")
	if err == nil {
		t.Fatalf("错 key 必须失败")
	}
	t.Logf("OK err=%v", err)
	if !strings.Contains(err.Error(), "鉴权") && !strings.Contains(err.Error(), "401") {
		t.Errorf("错误应指向鉴权问题，实际：%v", err)
	}
}
