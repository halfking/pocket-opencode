// stt_probe_json_body_test.go —— 缺陷8 的门。
//
// 缺陷8（2026-10-06 真机复现）：设置页「试转」按钮 100% 失败。
//
//   前端 sttSettingsApi.probe（frontend/src/api/stt-settings.ts:290，
//   被 SettingsSTT.vue:566 的「试转」调用）发的是
//     JSON.stringify({ audioBase64, filename, model, channel, baseURL, transport })
//   而 readSTTAudio 对非 multipart 一律「整个请求体当音频」，
//   于是服务端把那段 JSON 文本当成音频送进转写器。
//   上游回 400「Param Incorrect / invalid audio format」，
//   页面显示「试转失败」——而这恰恰是用户判断转写通不通的唯一入口，
//   现象与「转写功能整体坏了」完全同形。
//
//   实测（同一段 16kHz 4.6s 真实语音、同一个 /api/stt/probe）：
//     JSON {audioBase64} 形态 → 400 invalid audio format
//     multipart 形态         → ok=true，文本完全正确
//
//   同仓另外三个端点（transcribe / transcribe-full / transcribe-incremental）
//   都用 decodeBase64Audio 解析 JSON，probe 是唯一漏掉的一个。
//
// ★ 本门的主断言是**字节级**的：上游 ASR 收到的必须恰好是解码后的音频，
//   而不是 JSON 信封。这条断言是二值的（要么收到 X，要么收到信封），没有中间态；
//   删掉 JSON 分支它立刻失败。修复前的行为已在真机侧实测（400），不在这里重复。
//
// 负控放在本文件末尾：坏 base64 必须被明确拒绝、空 audioBase64 必须落回原始路径。

package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// capturingASR 记录上游 ASR 实际收到的音频字节与文件名。
type capturingASR struct {
	Audio    []byte
	Filename string
	Calls    int
}

// tapCapturingASR 装一个记录型的假 ASR：抄下它真正收到的音频字节与文件名。
// 复用 installFakeASR 的连接/设置手法（SSRF 私网放行 + external 设置 + 注入 client），
// 但**必须在自己的 handler 内解析 multipart** —— body 只能顺序消费一次，
// 读两遍（先 ReadForm 再断言）只会拿到空字节，于是判据恒假。
func tapCapturingASR(t *testing.T, srv *Server, reply string) *capturingASR {
	t.Helper()
	cap := &capturingASR{}

	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "1")
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "1")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		cap.Calls++
		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
			if boundary := params["boundary"]; boundary != "" {
				mr := multipart.NewReader(r.Body, boundary)
				for {
					part, perr := mr.NextPart()
					if perr != nil {
						break
					}
					if part.FormName() == "file" {
						cap.Filename = part.FileName()
						cap.Audio, _ = io.ReadAll(part)
					}
					_ = part.Close()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"` + reply + `"}`))
	}))
	t.Cleanup(upstream.Close)

	if err := srv.saveSTTSettings("shared-user", "ws-a", sttSettingsPayload{
		Channel:         stt.ChannelExternal,
		ExternalBaseURL: upstream.URL + "/v1",
		ExternalModel:   "gpt-4o-mini-transcribe",
	}, "sk-fake"); err != nil {
		t.Fatalf("保存假 ASR 设置失败: %v", err)
	}
	srv.SetSTTHTTPClient(upstream.Client())
	return cap
}

func probeJSON(t *testing.T, h http.Handler, token string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/stt/probe", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ★ 主断言：JSON 形态必须被解成音频送下去，而不是把信封本身当音频。
func TestSttProbeAcceptsJSONAudioBase64(t *testing.T) {
	// 造一段有辨识度的字节：一段 WAV 头 + 特征序列。
	// 不用纯文本，真实音频是二进制，更接近真机形态。
	audio := append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), []byte("POCKET-PROBE-8-MARKER-0123456789")...)

	srv, _ := sttTestServer(t, "")
	cap := tapCapturingASR(t, srv, "试转结果：OK")
	h := srv.Handler()

	rr := probeJSON(t, h, wsAToken(t), map[string]any{
		"audioBase64": base64.StdEncoding.EncodeToString(audio),
		"filename":    "recording.webm",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		OK    bool   `json:"ok"`
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if !out.OK {
		t.Fatalf("JSON 形态应成功，got error=%s", out.Error)
	}
	if out.Text != "试转结果：OK" {
		t.Errorf("文本未透传：%q", out.Text)
	}
	if cap.Calls != 1 {
		t.Fatalf("上游应被调用 1 次，实际 %d", cap.Calls)
	}
	// ★ 判别力核心：上游收到的是解码后的音频，不是 JSON 信封。
	if string(cap.Audio) != string(audio) {
		t.Errorf("★ 上游收到的不是解码后的音频。\n  期望 %d 字节: %q\n  实得 %d 字节: %q\n  （拿到 JSON 信封 ⇒ JSON 分支不存在或未命中，这就是缺陷8）",
			len(audio), truncateB(audio), len(cap.Audio), truncateB(cap.Audio))
	}
	if cap.Filename != "recording.webm" {
		t.Errorf("filename 未透传：%q", cap.Filename)
	}
}

// 回归：原始音频请求体（不是 JSON）仍走老路径。
func TestSttProbeStillAcceptsRawAudioBody(t *testing.T) {
	audio := []byte("RIFF\x00\x00\x00\x00WAVEfmt RAW-PATH-MARKER")
	srv, _ := sttTestServer(t, "")
	cap := tapCapturingASR(t, srv, "raw ok")
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/stt/probe", strings.NewReader(string(audio)))
	req.Header.Set("Authorization", "Bearer "+wsAToken(t))
	req.Header.Set("Content-Type", "audio/wav")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if string(cap.Audio) != string(audio) {
		t.Errorf("原始音频路径被 JSON 分支误伤：期望 %q 实得 %q", truncateB(audio), truncateB(cap.Audio))
	}
}

// 回归：multipart 形态仍然可用（设置页与手工测试都用它）。
func TestSttProbeStillAcceptsMultipart(t *testing.T) {
	audio := []byte("RIFF\x00\x00\x00\x00WAVEfmt MULTIPART-MARKER")
	srv, _ := sttTestServer(t, "")
	cap := tapCapturingASR(t, srv, "multipart ok")
	h := srv.Handler()

	var body strings.Builder
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\n")
	body.Write(audio)
	body.WriteString("\r\n--X--\r\n")

	req := httptest.NewRequest(http.MethodPost, "/api/stt/probe", strings.NewReader(body.String()))
	req.Header.Set("Authorization", "Bearer "+wsAToken(t))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if string(cap.Audio) != string(audio) {
		t.Errorf("multipart 路径被破坏：期望 %q 实得 %q", truncateB(audio), truncateB(cap.Audio))
	}
}

// ── 负控：证明 JSON 分支是有条件的，不是「什么 JSON 都当音频解」 ──────────────

// 负控1：audioBase64 是坏 base64 ⇒ 必须被明确拒绝，而不是原样把 JSON 送下去。
func TestNegativeControlBadBase64IsRejected(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	cap := tapCapturingASR(t, srv, "不该被调用")
	h := srv.Handler()

	rr := probeJSON(t, h, wsAToken(t), map[string]any{
		"audioBase64": "这不是合法的 base64 !!!",
		"filename":    "a.wav",
	})
	if rr.Code == http.StatusOK {
		var out struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		if out.OK {
			t.Fatalf("★ 门有洞：坏 base64 被判为成功，上游收到了 %q", truncateB(cap.Audio))
		}
	}
	if cap.Calls != 0 {
		t.Errorf("坏 base64 不该打到上游，实际调用 %d 次（收到 %q）", cap.Calls, truncateB(cap.Audio))
	}
}

// 负控2：JSON 里没有 audioBase64 ⇒ 必须落回「原始请求体」路径，
// 而不是被 JSON 分支吞掉当成空音频。
func TestNegativeControlJSONWithoutAudioBase64FallsThrough(t *testing.T) {
	raw := []byte(`{"model":"mimo-v2.5-asr","note":"NO-AUDIO-FIELD"}`)
	srv, _ := sttTestServer(t, "")
	cap := tapCapturingASR(t, srv, "fallthrough")
	h := srv.Handler()

	rr := probeJSON(t, h, wsAToken(t), map[string]any{"model": "mimo-v2.5-asr", "note": "NO-AUDIO-FIELD"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	// 守卫是 `AudioBase64 != ""`，所以没有该字段时整段 JSON 按原始音频处理 ——
	// 收尾条件要如实写出来：这里断言的正是「守卫存在」，若守卫被写成
	// 只判 err == nil，这条会拿到解码结果而不是原始 JSON。
	if cap.Calls != 1 {
		t.Fatalf("上游应被调用 1 次，实际 %d", cap.Calls)
	}
	if string(cap.Audio) != string(raw) {
		t.Errorf("无 audioBase64 时应落回原始请求体：期望 %q 实得 %q", truncateB(raw), truncateB(cap.Audio))
	}
}

func truncateB(b []byte) string {
	const n = 48
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
