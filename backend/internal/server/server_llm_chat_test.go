package server

// server_llm_chat_test.go — /api/llm/chat 迁移动态网关（BFF）后的行为回归。
//
// 关键契约（2026-09-05 迁移，见 runbook §14/§15）：
//  1. llmBFF 已装配时，即使静态 s.llm 为 nil（启动时未配 POCKET_LLM_API_KEY），
//     /api/llm/chat 也必须 200 —— 旧的恒 503 分支已消除；
//  2. 网关未配置（provider 缺失 → ErrNotConfigured）→ 503，错误信息指向
//     动态网关配置入口（/api/llm-gateway/config）；
//  3. 响应形状保持 {"content","model"}，前端（meetings.ts 兜底路径）无需改动。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// stubChatProvider 记录收到的 model 供断言，并把同一段 content 以流式形态回吐。
//
// ⚠ 2026-10-07：Stream **不再**返回 ErrNotConfigured。
// llmChatOnce（摘要/推荐/精校三条线的统一入口）已从非流式改成消费流式，
// 双桩若还把 Stream 留成「不可用」，测到的就不是产品现在真正走的那条路 ——
// 那正是 TestMeetingSummaryViaBFFWorksWithoutStaticLLM 报 502 的原因。
// 桩必须跟着产品走，否则这道测试守的是一个已经不存在的实现。
type stubChatProvider struct {
	gotModel string
	content  string
	// finishReason 非空时终帧带它（测 finish_reason=length 的降级路径）。
	finishReason string
}

func (p *stubChatProvider) Chat(_ context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	p.gotModel = req.Model
	return &llmbff.ChatResponse{Content: p.content, Model: req.Model}, nil
}

func (p *stubChatProvider) Stream(_ context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	p.gotModel = req.Model
	// 真实网关在正文前会先发一帧只有 model 的进度帧；桩照做，
	// 让 llmChatOnce「model 要每帧取、不能只看最后一帧」那条逻辑真的被走到。
	fn(llmbff.Delta{Model: req.Model})
	fn(llmbff.Delta{Content: p.content})
	fn(llmbff.Delta{Done: true, FinishReason: p.finishReason})
	return &llmbff.Usage{TotalTokens: 1}, nil
}

func (p *stubChatProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func TestLLMChatViaBFFWorksWithoutStaticLLM(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	stub := &stubChatProvider{content: "pong"}
	srv.SetLLMBFF(llmbff.NewService(stub, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("chat-user", "member", "ws-chat")
	if err != nil {
		t.Fatal(err)
	}

	// 前置条件：测试服的静态 s.llm 必须为 nil——旧实现在这里必然 503。
	if srv.llm != nil {
		t.Fatal("precondition: static llm must be nil in test server")
	}

	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok,
		`{"model":"glm-5.2","messages":[{"role":"user","content":"ping"}]}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 via dynamic BFF despite nil s.llm, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if body["content"] != "pong" || body["model"] != "glm-5.2" {
		t.Fatalf("unexpected response body: %s", rr.Body.String())
	}
	if stub.gotModel != "glm-5.2" {
		t.Fatalf("provider received model %q, want glm-5.2", stub.gotModel)
	}
}

func TestLLMChatUnconfiguredGatewayReturns503(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	// provider 为 nil 模拟「BFF 装配了但网关 URL/key 未配置」。
	srv.SetLLMBFF(llmbff.NewService(nil, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("chat-user", "member", "ws-chat")
	if err != nil {
		t.Fatal(err)
	}
	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok,
		`{"model":"auto","messages":[{"role":"user","content":"ping"}]}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when gateway unconfigured, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "llm-gateway") {
		t.Fatalf("error should point at dynamic gateway config: %s", rr.Body.String())
	}
}

// §916：/api/llm/chat 的单条消息上限**按 UTF-8 字节算**（Go 的 `len(string)`），
// 而错误信息原先写的是「chars」⇒ **信息与判定不同形**：
// 任何「按字符数修」的实现，对中文内容会超闸 3 倍（1 个中文字 = 3 字节）。
//
// ⚠ 这道门是**行为**门：它真的把请求打进去，看服务端答什么。
//
//	源码扫描只能证明「代码写在那」，证不了「它真的按字节判」。
//	（§913 的教训：判据要钉**行为**，不是钉源码里那行字。）
func TestLLMChatMessageLengthGateCountsBytesNotChars(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.SetLLMBFF(llmbff.NewService(&stubChatProvider{content: "pong"}, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("chat-user", "member", "ws-chat")
	if err != nil {
		t.Fatal(err)
	}

	post := func(content string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{
			"model":    "glm-5.2",
			"messages": []map[string]string{{"role": "user", "content": content}},
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodPost, "/api/llm/chat", tok, string(body)))
		return rr
	}

	// ① 恰好 32000 **字节** ⇒ 放行（`len > 32000` 为假）
	if rr := post(strings.Repeat("a", 32000)); rr.Code != http.StatusOK {
		t.Fatalf("32000 字节应当放行，却拿到 %d: %s", rr.Code, rr.Body.String())
	}

	// ② 10666 个中文字 = 31998 字节（**字符数远低于 32000**）⇒ 放行
	if rr := post(strings.Repeat("中", 10666)); rr.Code != http.StatusOK {
		t.Fatalf("10666 个中文字（31998 字节）应当放行，却拿到 %d: %s", rr.Code, rr.Body.String())
	}

	// ③ 11000 个中文字 = 33000 字节（**字符数低于 32000**）⇒ 400
	//    这条就是「按字符修」的实现会漏掉的那一类。
	rr := post(strings.Repeat("中", 11000))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("11000 个中文字（33000 字节）应当 400，却拿到 %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "too long") {
		t.Fatalf("400 的原因应当是长度闸，却拿到: %s", rr.Body.String())
	}
}

// §916：前端镜像的闸值必须与服务端一致 —— 两边各写一个数，漂了没人知道。
//
// ⚠ 匹配数必须**恰好 1 处**：改成 0 处（实现换了写法）或 2 处（多了一个同形闸）
//
//	都要**当场红**，而不是悄悄放过 —— 这是 §913「扫描型判据」那族教训的护栏。
func TestLLMChatLengthGateConstantMatchesFrontend(t *testing.T) {
	one := func(src, pattern, what string) string {
		t.Helper()
		all := regexp.MustCompile(pattern).FindAllStringSubmatch(src, -1)
		if len(all) != 1 {
			t.Fatalf("%s 里匹配到 %d 处 %q（要求恰好 1 处）：判据前提变了，请重读 §916",
				what, len(all), pattern)
		}
		return all[0][1]
	}
	goGate := one(readFrontend(t, "server_assistant.go"), `len\(m\.Content\)\s*>\s*(\d+)`, "server_assistant.go")
	bffGate := one(readFrontend(t, "server_llmbff.go"), `len\(m\.Content\)\s*>\s*(\d+)`, "server_llmbff.go")
	frontGate := one(readFrontend(t, "../../../frontend/src/api/meetings.ts"),
		`LLM_CHAT_MAX_CONTENT_BYTES\s*=\s*(\d+)`, "frontend/src/api/meetings.ts")
	// ⚠ §919：/api/llm/stream（server_llmbff.go）是**另一个 handler**，
	//   它有**自己那一份** 32000 ⇒ 改 chat 那道不会自动改 stream 这道。
	//   而前端也**各写各的**（流式走 llm-bff 通道，非流式走 http 通道）
	//   ⇒ 前端镜像常量同样有两份 ⇒ 4 路必须相等。
	streamFrontGate := one(readFrontend(t, "../../../frontend/src/features/meetings/meetings-ai.ts"),
		`LLM_STREAM_MAX_CONTENT_BYTES\s*=\s*(\d+)`, "frontend/src/features/meetings/meetings-ai.ts")
	if goGate != bffGate {
		t.Fatalf("两处服务端闸不一致：server_assistant.go=%s、server_llmbff.go=%s", goGate, bffGate)
	}
	if goGate != frontGate {
		t.Fatalf("前端镜像的闸值 %s ≠ 服务端的 %s ⇒ 前端会按错的尺寸装配提示词 ⇒ 兜底会被 400 拦掉",
			frontGate, goGate)
	}
	if bffGate != streamFrontGate {
		t.Fatalf("前端 /api/llm/stream 镜像的闸值 %s ≠ 服务端该端点的 %s ⇒ 流式会议纪要对长会议必然 400（§919）",
			streamFrontGate, bffGate)
	}
}
