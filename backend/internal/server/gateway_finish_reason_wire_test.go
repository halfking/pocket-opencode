// gateway_finish_reason_wire_test.go
//
// 钉住 `finish_reason` 从**真网关 HTTP 响应**一路传到 `llmbff.ChatResponse`
// 的那条接缝。
//
// ⚠⚠ 为什么这道门必须存在（它防的是一个我的测试防不到的盲点）：
//
//	`llm_chat_once_stream_test.go` 里的 provider 桩是**直接**给 ChatResponse
//	塞 FinishReason 的。也就是说 —— 如果有人把
//	`llmbff_provider_adapters.go` 里那行 `out.FinishReason = ...` 删掉，
//	那一整组关于「截断必须降级」的测试**照样全绿**，而生产上的防护已经没了。
//	桩把字段直接赋上，就等于替产品做了那个决定；判据必须落在**产品自己**的搬运上。
//
//	这个字段历史上就是这样的：`llmgateway.ChatResponse.Choices[].FinishReason`
// 一直在解码，却**从未往上传**，于是「输出被预算截断」在上层完全不可见。
//	直到 §47 要靠它关掉 §12.4 第 8 条，才发现得自己补这一跳。
//
// 用 httptest 起一个**真的** /v1/chat/completions 端点，而不是 mock 客户端：
//	那样才量得到「解码 → 搬运」这两跳，而不是我把 struct 直接填好。

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

func gatewayServerReturning(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

const finishReasonBody = `{
  "id":"chatcmpl-x","object":"chat.completion","created":1,
  "model":"glm-5.1",
  "choices":[{"index":0,"message":{"role":"assistant","content":"正文"},"finish_reason":"length"}],
  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
}`

func TestFinishReasonSurvivesGatewayToBFF(t *testing.T) {
	ts := gatewayServerReturning(finishReasonBody)
	defer ts.Close()

	prov := NewLLMGatewayBFFProvider(llmgateway.NewClient(ts.URL, "k"))
	if prov == nil {
		t.Fatal("provider 构造失败")
	}
	resp, err := prov.Chat(context.Background(), llmbff.ChatRequest{
		Model:    "glm-5.1",
		Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "正文" {
		t.Fatalf("正文应原样搬运，实得 %q", resp.Content)
	}
	// ★ 这一条就是本文件的牙齿：删掉适配器里那行赋值，这里立刻红。
	if resp.FinishReason != "length" {
		t.Fatalf("★ finish_reason 没有从网关响应搬上来（实得 %q）—— "+
			"这会让 llmChatOnce 的截断闸失效，而所有用桩的测试都察觉不到", resp.FinishReason)
	}
}

func TestFinishReasonStopIsPassedThroughNotCoerced(t *testing.T) {
	ts := gatewayServerReturning(`{
	  "model":"glm-5.1",
	  "choices":[{"index":0,"message":{"role":"assistant","content":"正文"},"finish_reason":"stop"}],
	  "usage":{"total_tokens":3}}`)
	defer ts.Close()

	prov := NewLLMGatewayBFFProvider(llmgateway.NewClient(ts.URL, "k"))
	resp, err := prov.Chat(context.Background(), llmbff.ChatRequest{
		Model:    "glm-5.1",
		Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	// 「stop」必须原样带上来，不能被归一成空：上层据此区分
	// 「这次输出是完整的」与「被截断了」，清空它等于把闸门焊死。
	if resp.FinishReason != "stop" {
		t.Fatalf("finish_reason=stop 应原样透传，实得 %q", resp.FinishReason)
	}
}
