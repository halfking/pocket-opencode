package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 本文件钉的是**「上游 200 + 空文本不得被当成成功」这条守卫的覆盖面**。
//
// 为什么需要它（一次真实纠错的产物，§654）：
//   简报 #31 原来登记「上游返回 200 + 空文本被当成功落库」。本轮按 `full.go`
//   的聚合逻辑推断出一条「守卫存在但判据选错」的链条，正控把它打掉了 ——
//   真守卫在**更下一层**：`transcribe.go` 的 `finish()`（:344-346）把
//   「TrimSpace 后为空」直接变成 error。`full.go` 那个 `Succeeded == 0` 的守卫
//   只是**第二道**，它看到的是第一道已经筛过的结果。
//
//   ⇒ 这正是「读源码推出来的链条不能当证据」的又一条。所以现在把这个
//   **已被源码证实的结论**钉成常驻回归。
//
// 为什么要钉「覆盖面」而不是钉「某一条链路」：
//   `finish` 自己的注释写着「之前 chat-audio 走的是独立的早返回路径，等于把
//   『空文本算失败』与『AudioIgnored 幻觉算失败』这两条守卫只挂在
//   transcriptions 上 —— 少一道守卫的地方就是下一个静默缺陷的位置」。
//   这是本仓自己认过的回归形状：加一条新链路时忘了走 `finish`，
//   就是同一个静默缺陷换个入口再来一次。
//
// ⚠ 「空文本算不算失败」本来登记为待拍板的产品口径（#31），但**代码里已经
//   实现为「算失败」**，且是两条链路共用的收尾。所以本文件不代拍，只钉现状。
// ─────────────────────────────────────────────────────────────────────────────

// emptyTextStub 是可控上游：transcriptions 端点回 `{"text": textBody}`，
// chat-audio 端点回 OpenAI 风格的 choices[].message.content。
type emptyTextStub struct {
	textBody     string
	totalChars   int
	transcriptsN int
	chatAudioN   int
}

func (u *emptyTextStub) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if readMultipartAudio(r) == nil {
			// 没解析出音频 ⇒ 这次调用不算数。否则「段数」会被虚高，
			// 让后续断言看起来像在量调用次数，其实量的是请求数。
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"no audio field"}}`))
			return
		}
		u.transcriptsN++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"` + u.textBody + `"}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		u.chatAudioN++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + u.textBody +
			`"}}],"usage":{"total_characters":` + strconv.Itoa(u.totalChars) + `}}`))
	})
	return mux
}

func emptyTextTranscriber(t *testing.T, srv *httptest.Server, transport string) *Transcriber {
	t.Helper()
	tr := NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: srv.URL + "/v1", APIKey: "k", Model: "stub-model",
			Transport: transport, Channel: ChannelExternal,
		}, nil
	})
	tr.SetHTTPClient(srv.Client())
	return tr
}

// emptyTextWav 是 60 秒（2 秒语音 + 1 秒静音）× 20 周期，在 25 秒默认上限下
// 必然切成多段。必须真超限：短样本只会切出 1 段，就量不到「逐段调用」。
func emptyTextWav() []byte { return pcmWAV(16000, 2, 1, 20) }

// ── 正控 ──────────────────────────────────────────────────────────────────────
// 没有这条，下面那些「空文本必须报错」的断言可能是恒真的
// （即量具测的是「stub 有没有被调到」，而不是「空文本走没走漏」）。
func TestEmptyTextGuardPositiveControlNonEmptySucceeds(t *testing.T) {
	stub := &emptyTextStub{textBody: "这是一段真实内容", totalChars: 8}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	res, err := emptyTextTranscriber(t, srv, TransportTranscriptions).
		Transcribe(context.Background(), emptyTextWav(), "a.wav")
	if err != nil {
		t.Fatalf("正控失败：有文本时必须成功，实际 err=%v", err)
	}
	if res == nil || res.Text == "" {
		t.Fatalf("正控失败：期望拿到非空文本，实际 %#v", res)
	}
	if stub.transcriptsN == 0 {
		t.Fatal("正控失败：上游一次都没被调用 ⇒ 下面那些断言量不到东西")
	}
}

// ── 主断言 1：transcriptions 链路 ────────────────────────────────────────────
func TestEmptyTextIsRejectedOnTranscriptions(t *testing.T) {
	stub := &emptyTextStub{textBody: "", totalChars: 0}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	_, err := emptyTextTranscriber(t, srv, TransportTranscriptions).
		Transcribe(context.Background(), emptyTextWav(), "a.wav")
	if err == nil {
		t.Fatal("上游 200 + 空文本必须报错（否则空串会被当成转写内容落库）")
	}
	if !strings.Contains(err.Error(), "empty transcript") {
		t.Errorf("期望错误来自 finish 的空文本守卫，实际 = %v", err)
	}
	if stub.transcriptsN == 0 {
		t.Fatal("上游一次都没被调用 ⇒ 这条断言量的是桩而不是被测对象")
	}
}

// ── 主断言 2：chat-audio 链路（这条链另有一道早返回，必须分别钉）────────────
//
// chat-audio 上游对「没收到音频」的回执是 `total_characters: 0` + 一句提示，
// transcribe.go 在进 `finish` **之前**就拦了它。所以这里分两个 case：
//
//	case A（total_characters=0）⇒ 命中那道早返回，报 "dropped the audio"
//	case B（total_characters=5 但 content 空）⇒ 一路进 finish，
//	       被 finish 的空文本守卫拦下，报 "empty transcript"
//
// 只有 case B 才真正证明「chat-audio 也走 finish」—— case A 在 finish
// 被绕开的情况下依然会红，是**守不住 finish 覆盖面**的那一种。
func TestEmptyTextIsRejectedOnChatAudio(t *testing.T) {
	cases := []struct {
		name       string
		totalChars int
		wantMsg    string
	}{
		{"上游回没收到音频的早返回", 0, "dropped the audio"},
		{"上游回空内容但计了字符数（唯一能证明走过 finish 的那一支）", 5, "empty transcript"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &emptyTextStub{textBody: "", totalChars: tc.totalChars}
			srv := httptest.NewServer(stub.handler(t))
			defer srv.Close()

			_, err := emptyTextTranscriber(t, srv, TransportChatAudio).
				Transcribe(context.Background(), emptyTextWav(), "a.wav")
			if err == nil {
				t.Fatalf("chat-audio 上游给空内容必须报错，实际成功返回")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("期望错误含 %q，实际 = %v", tc.wantMsg, err)
			}
			if stub.chatAudioN == 0 {
				t.Fatal("chat/completions 端点一次都没被调用 ⇒ 断言量的是桩")
			}
		})
	}
}

// ── 主断言 3：整场多段的空文本也不能被「任一段成功」掩盖 ──────────────────────
// `full.go` 那个 `Succeeded == 0` 守卫容易被误读成主守卫。它其实是第二道：
// 逐段调用已经各自被 finish 拦下，于是每段都走 err 分支、Failed 段数 == 总段数。
// 钉住这个事实，防止有人把第二道误当第一道、又把第一道删掉。
func TestFullTranscribeOnAllEmptyTextFailsRatherThanReportingSuccess(t *testing.T) {
	stub := &emptyTextStub{textBody: "", totalChars: 0}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	res, err := emptyTextTranscriber(t, srv, TransportTranscriptions).
		TranscribeFull(context.Background(), Scope{}, emptyTextWav(), "a.wav")
	if err == nil {
		t.Fatalf("整场空文本必须报错而不是成功返回；实际 Text=%q Succeeded=%d Failed=%d",
			res.Text, res.Succeeded, res.Failed)
	}
	if stub.transcriptsN < 2 {
		t.Fatalf("量具自证失败：60 秒应逐段调用 ≥2 次，实际 %d 次", stub.transcriptsN)
	}
	if !strings.Contains(err.Error(), "没有任何有效文本") {
		t.Errorf("期望错误来自 full.go 的聚合守卫，实际 = %v", err)
	}
}

// ── 主断言 4：源码级钉「覆盖面」（防止将来新增链路时忘了走 finish）──────────
//
// 这条是本文件真正的承重墙：上面三条只能证明**当前这两条**链有守卫，
// 而新增链路（将来某个 Provider/形态）忘了走 finish 是最可能的回归形状。
// 这里钉的是「`Transcribe` 里 `finish` 的调用点恰好 2 处，且都在两条链的收尾」。
func TestFinishRemainsTheSingleConvergencePointForBothTransports(t *testing.T) {
	src := readSTTFile(t, "transcribe.go")
	// 锚点自证：先确认守卫本身还在，再数调用点。
	if !strings.Contains(src, `return nil, fmt.Errorf("stt %s: upstream returned empty transcript", target.Model)`) {
		t.Fatal("锚点失效：transcribe.go 里已找不到 finish 的空文本守卫。" +
			"守卫可能被删除或改写 —— 若这是有意的，请回来核 #31 的口径并改本文件")
	}
	// 调用点计数。注意这里数的是 `t.finish(` 而不是 `finish(`，
	// 免得把注释里提到的名字算进去。
	calls := strings.Count(src, "t.finish(")
	if calls != 2 {
		t.Errorf("期望 transcribe.go 里恰好 2 处 `t.finish(`（chat-audio + provider 两条链收尾），"+
			"实际 %d 处。新增链路时若忘了过 finish，空文本就会从新入口静默落库；"+
			"若是有意新增，请把本文件改成数据驱动（列出每条链并各钉一次）", calls)
	}
}

// readSTTFile 读同包下的源码文件。`go test` 的工作目录就是包目录，
// 所以相对路径就是事实侧本身 —— 不复制常量，只读原文。
func readSTTFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}
