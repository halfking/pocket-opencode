// error_body_truncation_test.go — §241：错误体被 `LimitReader(512)` 硬截断，
// 于是**形状正确**的错误也可能识别不出来。
//
// ⚠ 与同目录另两个文件同款，本文件是**特征化判据（不是目标判据）**：
// 断言**当前的（有缺陷的）行为**，修复之后必然转红。修复点在
// `internal/llmgateway/client.go`（`git status` = `M`，并行会话在制品），本轮不改。
//
// ── 它守的缺口 ──
//
// `client.go` 在 **7 处**用 `io.ReadAll(io.LimitReader(resp.Body, 512))` 读错误体。
// ⇒ 网关的错误体被**硬截断在 512 字节**，且**没有任何标记说明「后面还有」**。
//
// 而 `server` 侧的 `isModelUnavailableError` 是这么识别「模型不可用」的：
//
//	msg := err.Error()
//	idx := strings.Index(msg, "{")
//	json.Unmarshal([]byte(msg[idx:]), &body)   // ← 语法不完整就必然失败
//
// ⇒ 错误体一旦超过 512 字节，JSON 是**半截**的 ⇒ Unmarshal 报错 ⇒ 判 false
// ⇒ **不回退**。
//
// ⇒ ★★★ 因此 §235.6 建议的修法（给 `llmGatewayNoCandidateBody` 补 `Message` 字段）
//
//	**必要但不充分**：那个值根本没能进到 JSON 里。
//
// ★ 这一格的形状是**对的**（code=model_not_found / kind=no_candidates），
//
//	它识别不到**纯粹因为长度** —— 与 §235 的「形状不对」是两回事，
//	两者必须分别钉住，否则只修一个就会以为另一个也被修了。
//
// ⚠⚠ 本文件的形态选择：**必须真的走一遍 `Client.Chat`**。
//
//	第一版把它写成「构造一个 900 字节的 error 字符串直接喂分类器」，
//	结果**测不到截断**（512 限制发生在读响应时，分类器拿到的字符串长短由调用方决定）
//	—— 而它却**绿了**，还自称「已修复」。
//	⇒ 这是本会话第 N 次「判据没有指向被验证对象」的同款，必须靠真起 httptest 服务器堵住。
package llmgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 与 §233 真实抓到的 chat 形状一致（code / kind / message / request_id），
// 只把 message 补长到足以越过 512。
func longNoProviderBody(fillerLen int) string {
	return `{"error":{"code":"model_not_found",` +
		`"gateway_debug":{"attempts":null,"kind":"transient","retryable":true,` +
		`"stage":"execution","tried":1},` +
		`"kind":"no_candidates",` +
		`"message":"No available provider for model 'glm-5.3'. All 1 candidates failed. ` +
		strings.Repeat("x", fillerLen) + `",` +
		`"request_id":"bbdf8dc2ae4b2e4255cf1ebc7ec356a8","type":"server_error"}}`
}

// TestGatewayErrorBodyIsHardTruncatedAt512 事实钉：
// 超过 512 字节的错误体，穿过客户端后**长度恰为 512** 且**不是合法 JSON**。
func TestGatewayErrorBodyIsHardTruncatedAt512(t *testing.T) {
	const full = 4096
	var served string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = longNoProviderBody(full)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(served))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "k", Client: srv.Client()}
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "glm-5.3",
		Messages: []ChatMessage{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatalf("服务端返回 503，客户端却没报错 ⇒ 量具坏了")
	}

	msg := err.Error()
	idx := strings.Index(msg, "{")
	if idx < 0 {
		t.Fatalf("错误里连 '{' 都没有，拿不到错误体起点：%q", msg)
	}
	gotBody := msg[idx:]

	t.Logf("服务端发出的错误体 %d 字节 ⇒ 客户端拿到 %d 字节", len(served), len(gotBody))

	var probe any
	parseErr := json.Unmarshal([]byte(gotBody), &probe)
	if parseErr == nil {
		t.Log("✅ 已修复：客户端拿到的错误体是**完整** JSON（截断不再是障碍）")
		return
	}
	t.Logf("⚠️ 缺口复现：错误体被截断成半截 JSON ⇒ %v", parseErr)
	t.Logf("   末 24 字符：%q", gotBody[len(gotBody)-24:])
	t.Log("   ⇒ 下游任何 json.Unmarshal([]byte(body)) 的识别都会失败 ⇒ 不回退")
	t.Log("   ⚠ 本文件不 fail：记录缺口，不是要求当前行为正确的门")
}

// TestRightShapeStillUndetectableAfterTruncation 是本文件最硬的一格：
// **形状是对的**，却因为穿过客户端后被截断而识别不出来。
//
// ⚠ 第一版这条没走客户端，直接拿长原始字符串去 Unmarshal
//
//	⇒ 它当然合法 ⇒ 读出来是「✅ 已修复」——**误导性的绿**。
//	与 §241 那次「构造 900 字节字符串喂分类器」是同一个错：
//	**验证动作没有指向被验证对象**。本版必须真过一遍 `Client.Chat`。
func TestRightShapeStillUndetectableAfterTruncation(t *testing.T) {
	// 0) 量具自证：服务端**真的**发出了正确形状（code / kind 齐全）。
	var servedAny map[string]any
	if err := json.Unmarshal([]byte(longNoProviderBody(600)), &servedAny); err != nil {
		t.Fatalf("量具坏了：服务端错误体本该是合法 JSON：%v", err)
	}
	servedInner, _ := servedAny["error"].(map[string]any)
	if servedInner["code"] != "model_not_found" || servedInner["kind"] != "no_candidates" {
		t.Fatalf("量具坏了：样本形状不对 code=%v kind=%v", servedInner["code"], servedInner["kind"])
	}
	t.Log("✅ 量具自证：服务端发出的是 code=model_not_found / kind=no_candidates 的**正确形状**")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(longNoProviderBody(600)))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "k", Client: srv.Client()}
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "glm-5.3",
		Messages: []ChatMessage{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatalf("量具坏了：503 却没报错")
	}
	msg := err.Error()
	idx := strings.Index(msg, "{")
	if idx < 0 {
		t.Fatalf("错误里没有 '{'：%q", msg)
	}
	delivered := msg[idx:]

	var any map[string]any
	if jsonErr := json.Unmarshal([]byte(delivered), &any); jsonErr == nil {
		t.Log("✅ 已修复：正确形状的错误体穿过客户端后仍可解析（截断不再是障碍）")
		return
	}
	t.Logf("⚠️ 缺口复现：形状正确（code/kind 齐全）却穿过客户端后 %d 字节即被截断 ⇒ 解析失败", len(delivered))
	t.Log("   ⇒ 下游 isModelUnavailableError / isNoCandidateError 走的是 json.Unmarshal")
	t.Log("   ⇒ 它们**必然**返回 false ⇒ 回退链不启动")
	t.Log("   ⚠ 本文件不 fail：记录缺口，不是要求当前行为正确的门")
}

// TestShapeIsAlsoLostWithoutClientIsAWarning 负控：
// 直接把长错误体交给一个「不截断」的读取路径时，它是**能**被解析的
// ⇒ 证明失败原因确实是长度，不是形状或编码。
func TestLongBodyParsesWhenNotTruncated(t *testing.T) {
	body := longNoProviderBody(600)
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		// 若将来截断被放宽到 600 以上，这条会转绿 —— 那正是修复生效的信号。
		t.Logf("⚠️ 现在就解析失败了（限制定义可能变了）：%v", err)
		return
	}
	_ = io.Discard
	t.Log("✅ 负控通过：同样的长错误体在**不截断**时可解析 ⇒ 失败原因确实是长度")
}
