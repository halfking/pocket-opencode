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

// transcribeUpstream 造一个可控的 /audio/transcriptions 上游。
type transcribeUpstream struct {
	status  int
	body    string
	lastKey string
	lastMdl string
}

func (u *transcribeUpstream) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		u.lastKey = r.Header.Get("Authorization")
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		u.lastMdl = multipartModel(string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.status)
		_, _ = io.WriteString(w, u.body)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w,
			`{"choices":[{"message":{"content":"您似乎没有附上录音文件。"}}],"usage":{"total_characters":0}}`)
	})
	return mux
}

func engineFor(t *testing.T, srv *httptest.Server, target *Target) *Transcriber {
	t.Helper()
	e := NewResolver(func(context.Context, Scope) (*Target, error) { return target, nil })
	e.SetHTTPClient(srv.Client())
	return e
}

// TestTranscribeForSuccessCarriesModelAndCost 成功转写要把模型/通道/成本一起带出来，
// 否则设置页与成本看板拿不到「这次是谁转的、花了多少」。
func TestTranscribeForSuccessCarriesModelAndCost(t *testing.T) {
	up := &transcribeUpstream{status: 200, body: `{"text":"今天下午三点开项目评审会。"}`}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	// 1 秒 WAV → 时长 1000ms；0.18 美元/小时 → 0.00005 美元 = 0.005 美分
	e := engineFor(t, srv, &Target{
		BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "gpt-4o-mini-transcribe",
		Transport: TransportTranscriptions, Channel: ChannelExternal,
		CostUSDPerHour: 0.18,
	})
	res, err := e.TranscribeFor(context.Background(), Scope{UserID: "u", WorkspaceID: "w"},
		ToneWAV(8000, 1000), "a.wav")
	if err != nil {
		t.Fatalf("TranscribeFor: %v", err)
	}
	if res.Text != "今天下午三点开项目评审会。" {
		t.Errorf("text=%q", res.Text)
	}
	if res.Model != "gpt-4o-mini-transcribe" || res.Transport != TransportTranscriptions {
		t.Errorf("model/transport 没带出来：%+v", res)
	}
	if res.Channel != ChannelExternal {
		t.Errorf("channel=%s", res.Channel)
	}
	if res.DurationMS < 900 || res.DurationMS > 1100 {
		t.Errorf("durationMs=%d want ~1000", res.DurationMS)
	}
	// 0.18/h * (1/3600) h = 0.00005 USD = 0.005 cents
	if res.CostCents < 0.004 || res.CostCents > 0.006 {
		t.Errorf("costCents=%v want ~0.005", res.CostCents)
	}
	if up.lastKey != "Bearer sk-test" {
		t.Errorf("Authorization=%q", up.lastKey)
	}
	if up.lastMdl != "gpt-4o-mini-transcribe" {
		t.Errorf("上游收到的 model=%q", up.lastMdl)
	}
}

// TestTranscribeForRejectsEmptyTranscript 上游 200 但没有文字，不能算成功——
// 否则用户看到的是「录音结束，转写完成」，正文却是空的。
func TestTranscribeForRejectsEmptyTranscript(t *testing.T) {
	up := &transcribeUpstream{status: 200, body: `{"text":"   "}`}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	e := engineFor(t, srv, &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m",
		Transport: TransportTranscriptions, Channel: ChannelExternal})
	_, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
	if err == nil {
		t.Fatal("空转写必须判失败")
	}
	if !strings.Contains(err.Error(), "empty transcript") {
		t.Errorf("错误应说明空转写：%v", err)
	}
}

// TestTranscribeForRejectsHallucinatedTranscript 复刻 2026-10-01 实测的网关行为：
// 200 + 「您似乎没有附上录音文件」。这条守卫是整个功能的安全底线。
func TestTranscribeForRejectsHallucinatedTranscript(t *testing.T) {
	up := &transcribeUpstream{
		status: 200,
		body:   `{"text":"您好，您似乎没有附上录音文件。请重新上传需要转写的音频。"}`,
	}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	e := engineFor(t, srv, &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "auto",
		Transport: TransportTranscriptions, Channel: ChannelExternal})
	_, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
	if err == nil {
		t.Fatal("幻觉文本必须判失败，不能当转写结果")
	}
	if !strings.Contains(err.Error(), "no audio") && !strings.Contains(err.Error(), "没有附上") {
		t.Errorf("错误应点明「上游没收到音频」：%v", err)
	}
}

// TestTranscribeForChatAudioDroppedIsRejected 走 chat-audio 形态时同样要拦。
func TestTranscribeForChatAudioDroppedIsRejected(t *testing.T) {
	up := &transcribeUpstream{status: 200, body: "{}"}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	e := engineFor(t, srv, &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-audio",
		Transport: TransportChatAudio, Channel: ChannelGateway})
	_, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
	if err == nil {
		t.Fatal("chat 形态丢音频必须判失败")
	}
	if !strings.Contains(err.Error(), "dropped the audio") {
		t.Errorf("错误应说明上游丢了音频：%v", err)
	}
}

// TestTranscribeForStripsThinkTags 推理模型会在正文前带 <think>…</think>，
// 那不是转写内容，混进会议记录是噪音。
func TestTranscribeForStripsThinkTags(t *testing.T) {
	up := &transcribeUpstream{
		status: 200,
		body:   `{"text":"<think>用户让我转写这段录音。</think>今天下午三点开会。"}`,
	}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	e := engineFor(t, srv, &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m",
		Transport: TransportTranscriptions, Channel: ChannelExternal})
	res, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
	if err != nil {
		t.Fatalf("TranscribeFor: %v", err)
	}
	if strings.Contains(res.Text, "<think>") || strings.Contains(res.Text, "用户让我转写") {
		t.Errorf("<think> 未被剥掉：%q", res.Text)
	}
	if res.Text != "今天下午三点开会。" {
		t.Errorf("text=%q", res.Text)
	}
}

// TestTranscribeForPassesResolverError 解析失败（两条通道都不通）时，
// 错误必须原样冒泡，用户才能看到「去设置里配一个」。
func TestTranscribeForPassesResolverError(t *testing.T) {
	e := NewResolver(func(context.Context, Scope) (*Target, error) {
		return nil, errNoTargetForTest
	})
	_, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
	if err == nil || !strings.Contains(err.Error(), errNoTargetForTest.Error()) {
		t.Fatalf("解析错误应原样冒泡：%v", err)
	}
}

var errNoTargetForTest = sttTestError("stt_unavailable: 网关暂无可用的语音转写模型；外部语音转写服务未配置 API Key")

type sttTestError string

func (e sttTestError) Error() string { return string(e) }

// TestTranscribeForRejectsUnconfiguredTarget 缺 key / 缺模型要立刻报配置问题，
// 而不是拿着空 key 去打上游换一个更难懂的错误。
func TestTranscribeForRejectsUnconfiguredTarget(t *testing.T) {
	cases := []struct {
		name   string
		target *Target
		want   string
	}{
		{"缺 key", &Target{BaseURL: "https://x/v1", Model: "m", Transport: TransportTranscriptions}, "missing API key"},
		{"缺模型", &Target{BaseURL: "https://x/v1", APIKey: "k", Transport: TransportTranscriptions}, "no ASR model"},
		{"空 target", &Target{}, "missing API key"},
	}
	for _, c := range cases {
		e := NewResolver(func(context.Context, Scope) (*Target, error) { return c.target, nil })
		_, err := e.TranscribeFor(context.Background(), Scope{}, ToneWAV(8000, 100), "a.wav")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：err=%v want 包含 %q", c.name, err, c.want)
		}
	}
}

// TestTranscribeForRejectsEmptyAudio 空音频在出网前就该被拒。
func TestTranscribeForRejectsEmptyAudio(t *testing.T) {
	e := NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{BaseURL: "https://x/v1", APIKey: "k", Model: "m", Transport: TransportTranscriptions}, nil
	})
	if _, err := e.TranscribeFor(context.Background(), Scope{}, nil, "a.wav"); err == nil {
		t.Fatal("空音频必须被拒")
	}
}

// TestNewTranscriberKeepsStaticContract 旧的静态构造（env 兜底）必须保持原契约：
// 没给 key 就直接报错，不出网。
func TestNewTranscriberKeepsStaticContract(t *testing.T) {
	tr := NewTranscriber("", "", "")
	if _, err := tr.Transcribe(context.Background(), ToneWAV(8000, 100), "a.wav"); err == nil {
		t.Fatal("无 key 的静态转写器必须直接报错")
	}
	tr2 := NewTranscriber("sk-x", "", "")
	if _, err := tr2.Transcribe(context.Background(), ToneWAV(8000, 100), "a.wav"); err == nil {
		t.Fatal("无上游可达时也该报错（说明它确实尝试了，而不是静默成功）")
	}
}
