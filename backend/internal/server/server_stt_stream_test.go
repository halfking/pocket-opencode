package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// testToneWAV builds a 16-bit PCM mono WAV of the requested seconds.
// Constant tone on purpose: the splitter only needs decodable audio.
func testToneWAV(seconds int) []byte {
	sr := 16000
	n := sr * seconds
	pcm := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(3000 * math.Sin(float64(i)*0.05))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	var buf bytes.Buffer
	byteRate := uint32(sr * 2)
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sr))
	_ = binary.Write(&buf, binary.LittleEndian, byteRate)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

// fakeASRUpstream is an OpenAI-compatible fake upstream. Text is returned in
// call order so cross-chunk dedup can be asserted deterministically.
type fakeASRUpstream struct {
	texts []string
	calls int
	// seenSeconds records the duration of every chunk we were handed, so a
	// test can prove no single request exceeds the tightest provider limit.
	seenSeconds []float64
}

func (f *fakeASRUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls++
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		if file, _, err := r.FormFile("file"); err == nil {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(file)
			file.Close()
			if secs, ok := stt.ToneDurationSeconds(buf.Bytes()); ok {
				f.seenSeconds = append(f.seenSeconds, secs)
			}
		}
	}
	txt := "fallback"
	if f.calls-1 < len(f.texts) {
		txt = f.texts[f.calls-1]
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"text":"` + txt + `"}`))
}

// flakyUpstream fails on the 2nd call, succeeds otherwise.
type flakyUpstream struct{ calls int }

func (f *flakyUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(32 << 20)
	f.calls++
	if f.calls == 2 {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"text":"成功段内容"}`))
}

// installFakeASRUpstream wires a fake upstream as the user's external ASR
// settings, so target resolution runs for real and only the last hop is faked.
func installFakeASRUpstream(t *testing.T, srv *Server, up http.Handler) *httptest.Server {
	t.Helper()
	// httptest listens on 127.0.0.1, which validateGatewayURL rejects by
	// default (SSRF guard). Opt in explicitly, scoped to this test.
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "1")
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	if err := srv.saveSTTSettings("shared-user", "ws-a", sttSettingsPayload{
		Channel:         stt.ChannelExternal,
		ExternalBaseURL: ts.URL + "/v1",
		ExternalModel:   "gpt-4o-mini-transcribe",
	}, "test-key"); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	srv.SetSTTHTTPClient(ts.Client())
	return ts
}

func sttPostJSON(t *testing.T, srv *Server, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func TestSttTranscribeFullAggregatesLongAudio(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	up := &fakeASRUpstream{texts: []string{"第一段内容", "第二段内容", "第三段内容"}}
	installFakeASRUpstream(t, srv, up)

	// 60s of PCM WAV exceeds the 25s default segment, so it must be split.
	rr := sttPostJSON(t, srv, "/api/stt/transcribe-full", wsAToken(t), map[string]any{
		"audioBase64": base64.StdEncoding.EncodeToString(testToneWAV(60)),
		"filename":    "long.wav",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		OK        bool   `json:"ok"`
		Text      string `json:"text"`
		Succeeded int    `json:"succeeded"`
		Failed    int    `json:"failed"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if !out.OK {
		t.Fatalf("full transcription should succeed, error=%s", out.Error)
	}
	if out.Succeeded < 2 {
		t.Errorf("60s audio should split into multiple successful chunks, got %d", out.Succeeded)
	}
	if !strings.Contains(out.Text, "第一段内容") || !strings.Contains(out.Text, "第二段内容") {
		t.Errorf("aggregated text should contain each chunk in order: %q", out.Text)
	}
	// Every chunk we forwarded must fit the tightest provider limit (30s).
	for i, s := range up.seenSeconds {
		if s > 30.5 {
			t.Errorf("chunk %d was %.2fs, over the 30s provider limit", i, s)
		}
	}
}

func TestSttTranscribeFullReportsPartialFailure(t *testing.T) {
	// One failed chunk must not discard the chunks that did succeed.
	srv, _ := sttTestServer(t, "")
	installFakeASRUpstream(t, srv, &flakyUpstream{})

	rr := sttPostJSON(t, srv, "/api/stt/transcribe-full", wsAToken(t), map[string]any{
		"audioBase64": base64.StdEncoding.EncodeToString(testToneWAV(60)),
		"filename":    "long.wav",
	})
	var out struct {
		OK        bool   `json:"ok"`
		Text      string `json:"text"`
		Succeeded int    `json:"succeeded"`
		Failed    int    `json:"failed"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK {
		t.Fatal("partial failure must still return ok=true so successful chunks are kept")
	}
	if out.Failed < 1 {
		t.Errorf("failed chunk count should be reported, got %d", out.Failed)
	}
	if out.Succeeded < 1 {
		t.Error("expected at least one successful chunk")
	}
	if !strings.Contains(out.Text, "转写失败") {
		t.Errorf("failed chunk must leave a visible placeholder: %q", out.Text)
	}
}

func TestSttIncrementalAccumulatesAcrossChunks(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	up := &fakeASRUpstream{texts: []string{"今天下午三点", "开项目评审会"}}
	installFakeASRUpstream(t, srv, up)

	token := wsAToken(t)
	b64 := base64.StdEncoding.EncodeToString(testToneWAV(5))

	rr1 := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", token, map[string]any{
		"audioBase64": b64, "filename": "c.wav", "sessionId": "s1",
		"startSec": 0, "endSec": 5, "silenceCut": true,
	})
	var o1 struct {
		OK    bool   `json:"ok"`
		Text  string `json:"text"`
		Delta string `json:"delta"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr1.Body.Bytes(), &o1); err != nil {
		t.Fatalf("decode 1: %v body=%s", err, rr1.Body.String())
	}
	if !o1.OK || o1.Error != "" {
		t.Fatalf("chunk 1 should succeed: ok=%v error=%q", o1.OK, o1.Error)
	}
	if o1.Text != "今天下午三点" {
		t.Errorf("chunk 1 text=%q", o1.Text)
	}

	rr2 := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", token, map[string]any{
		"audioBase64": b64, "filename": "c.wav", "sessionId": "s1",
		"startSec": 5, "endSec": 10, "silenceCut": true, "isFinal": true,
	})
	var o2 struct {
		OK      bool   `json:"ok"`
		Text    string `json:"text"`
		IsFinal bool   `json:"isFinal"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &o2); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if !strings.Contains(o2.Text, "今天下午三点") || !strings.Contains(o2.Text, "开项目评审会") {
		t.Errorf("cumulative text should contain both chunks: %q", o2.Text)
	}
	if !o2.IsFinal {
		t.Error("IsFinal not propagated")
	}
}

func TestSttIncrementalDeduplicatesOverlappingChunks(t *testing.T) {
	// Chunks overlap in time; the shared words must not be duplicated.
	srv, _ := sttTestServer(t, "")
	up := &fakeASRUpstream{texts: []string{
		"今天下午三点开项目评审会",
		"三点开项目评审会请准备进度报告",
	}}
	installFakeASRUpstream(t, srv, up)

	token := wsAToken(t)
	b64 := base64.StdEncoding.EncodeToString(testToneWAV(5))
	var last struct {
		Text string `json:"text"`
	}
	for i := 0; i < 2; i++ {
		body := map[string]any{
			"audioBase64": b64, "filename": "c.wav", "sessionId": "overlap",
			"startSec": i * 4, "endSec": i*4 + 5, "silenceCut": true,
		}
		if i == 1 {
			body["isFinal"] = true
		}
		rr := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", token, body)
		if err := json.Unmarshal(rr.Body.Bytes(), &last); err != nil {
			t.Fatalf("decode: %v body=%s", err, rr.Body.String())
		}
	}
	if last.Text != "今天下午三点开项目评审会请准备进度报告" {
		t.Errorf("overlap should be deduplicated, got %q", last.Text)
	}
}

func TestSttIncrementalSeparateSessionsDoNotShareText(t *testing.T) {
	// Distinct sessionId must mean distinct state, otherwise a second meeting's
	// transcript continues from the first.
	srv, _ := sttTestServer(t, "")
	up := &fakeASRUpstream{texts: []string{"甲段", "乙段"}}
	installFakeASRUpstream(t, srv, up)

	token := wsAToken(t)
	b64 := base64.StdEncoding.EncodeToString(testToneWAV(5))
	sttPostJSON(t, srv, "/api/stt/transcribe-incremental", token, map[string]any{
		"audioBase64": b64, "sessionId": "a", "silenceCut": true, "isFinal": true,
	})
	rr := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", token, map[string]any{
		"audioBase64": b64, "sessionId": "b", "silenceCut": true, "isFinal": true,
	})
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "乙段" {
		t.Errorf("new session must start from zero, got %q", out.Text)
	}
}

func TestSttIncrementalRequiresSessionID(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	installFakeASRUpstream(t, srv, &fakeASRUpstream{})

	rr := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", wsAToken(t), map[string]any{
		"audioBase64": base64.StdEncoding.EncodeToString(testToneWAV(2)),
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing sessionId must be 400 (without it every chunk is standalone "+
			"text and boundary repeats pile up), got %d", rr.Code)
	}
}

func TestSttIncrementalRejectsOversizedAudio(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	installFakeASRUpstream(t, srv, &fakeASRUpstream{})
	huge := strings.Repeat("A", maxIncrementalAudioBytes*4/3+1024)
	rr := sttPostJSON(t, srv, "/api/stt/transcribe-incremental", wsAToken(t), map[string]any{
		"audioBase64": huge, "sessionId": "x",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("oversized audio must be 400, got %d", rr.Code)
	}
}

func TestSttIncrementalRejectsNonPost(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/stt/transcribe-incremental", nil)
	req.Header.Set("Authorization", "Bearer "+wsAToken(t))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should be 405, got %d", rr.Code)
	}
}

func TestSttStreamEndpointsRequireAuth(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	// These endpoints spend billable API quota, so they must never be open.
	for _, path := range []string{"/api/stt/transcribe-full", "/api/stt/transcribe-incremental"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			t.Errorf("%s allowed an unauthenticated call", path)
		}
	}
}
