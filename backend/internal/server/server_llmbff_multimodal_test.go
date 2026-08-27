package server

// server_llmbff_multimodal_test.go — 多模态（图片附件）链路的 hermetic 单测。
//
// 覆盖：
//  1. llmgateway.ContentParts 的组装与 JSON wire format（纯文本不变形、
//     带图时生成 OpenAI content parts 数组）；
//  2. /api/llm/stream 对图片的入参校验：scheme 白名单、数量上限、单张大小
//     上限；合法请求越过校验进入 BFF（provider 未配置时以 SSE error 帧返回，
//     状态码仍为 200——这是既有契约）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

func TestContentPartsPureTextStaysString(t *testing.T) {
	got := llmgateway.ContentParts("你好", nil)
	s, ok := got.(string)
	if !ok || s != "你好" {
		t.Fatalf("expected plain string passthrough, got %#v", got)
	}

	// 与 ChatMessage 的 wire format 一致：纯文本 content 仍是 JSON 字符串。
	msg := llmgateway.ChatMessage{Role: "user", Content: llmgateway.ContentParts("hi", nil)}
	raw, _ := json.Marshal(msg)
	if !strings.Contains(string(raw), `"content":"hi"`) {
		t.Fatalf("pure-text wire format changed: %s", raw)
	}
}

func TestContentPartsBuildsOpenAIParts(t *testing.T) {
	got := llmgateway.ContentParts("看图说话", []string{
		"data:image/png;base64,AAAA",
		"https://example.com/b.jpg",
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("content parts is not a JSON array: %s", raw)
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (1 text + 2 images), got %d", len(parts))
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看图说话" {
		t.Fatalf("first part must carry the text: %#v", parts[0])
	}
	for i, want := range []string{"data:image/png;base64,AAAA", "https://example.com/b.jpg"} {
		p := parts[i+1]
		if p["type"] != "image_url" {
			t.Fatalf("part %d type = %#v", i+1, p["type"])
		}
		url := p["image_url"].(map[string]any)["url"]
		if url != want {
			t.Fatalf("part %d url = %v, want %v", i+1, url, want)
		}
	}

	// 文本为空时不生成空 text part（纯图消息）。
	got2 := llmgateway.ContentParts("", []string{"https://x/y.png"})
	raw2, _ := json.Marshal(got2)
	if strings.Contains(string(raw2), `"type":"text"`) {
		t.Fatalf("empty text should not emit a text part: %s", raw2)
	}
}

// multimodalStreamServer 构造一个 llmBFF 已接线（provider 为 nil）的测试服
// 务器：校验类 400 在触碰 provider 前返回；合法请求会以 SSE error 帧暴露
// "no provider configured"，从而证明请求穿过了校验层。
func multimodalStreamServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.SetLLMBFF(llmbff.NewService(nil, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("mm-user", "member", "ws-mm")
	if err != nil {
		t.Fatal(err)
	}
	return srv, tok
}

func TestLLMStreamRejectsBadImageScheme(t *testing.T) {
	srv, tok := multimodalStreamServer(t)
	body := `{"model":"auto","messages":[{"role":"user","content":"看图","images":["http://example.com/a.png"]}]}`
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("http:// image must be rejected, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "https://") {
		t.Fatalf("error should explain the scheme allowlist: %s", rr.Body.String())
	}
}

func TestLLMStreamRejectsTooManyImages(t *testing.T) {
	srv, tok := multimodalStreamServer(t)
	images := make([]string, maxChatImagesPerMessage+1)
	for i := range images {
		images[i] = "data:image/png;base64,AAAA"
	}
	raw, _ := json.Marshal(map[string]any{
		"model":    "auto",
		"messages": []any{map[string]any{"role": "user", "content": "x", "images": images}},
	})
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, string(raw))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "too many images") {
		t.Fatalf("expected 400 too many images, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLLMStreamRejectsOversizedImage(t *testing.T) {
	srv, tok := multimodalStreamServer(t)
	huge := "data:image/png;base64," + strings.Repeat("A", maxChatImageBytes+1)
	raw, _ := json.Marshal(map[string]any{
		"model":    "auto",
		"messages": []any{map[string]any{"role": "user", "content": "x", "images": []string{huge}}},
	})
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, string(raw))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// 纵深防御：全局请求体上限（decode 失败 → invalid body）与 handler 内的
	// 逐图 6MB 校验（image too large）任一命中都必须拒绝，绝不能放行到上游。
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized image must be rejected with 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLLMStreamAcceptsDataImageAndReachesBFF(t *testing.T) {
	srv, tok := multimodalStreamServer(t)
	body := `{"model":"auto","messages":[{"role":"user","content":"描述这张图","images":["data:image/png;base64,iVBORw0KGgo="]}],"max_tokens":16}`
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// 既有契约：SSE 200 起流后，BFF 错误以 data 帧表达。provider 未配置时
	// 应出现 ErrNotConfigured，而不是校验层 400——证明多模态请求已放行。
	if rr.Code != http.StatusOK {
		t.Fatalf("valid multimodal request must pass validation, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no provider configured") {
		t.Fatalf("expected ErrNotConfigured SSE frame, got: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", rr.Header().Get("Content-Type"))
	}
}
