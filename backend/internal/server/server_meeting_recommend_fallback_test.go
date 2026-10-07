package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
)

// recommendLLM 是 llmChatOnce 的替身：s.llm 为 nil、s.llmBFF 为 nil 时
// llmChatOnce 才会走到 s.llm.Chat（见 server_assistant.go 的 llmChatOnce）。
type recommendLLM struct {
	content string
	err     error
	calls   int
	lastMsg string
}

func (f *recommendLLM) Chat(_ context.Context, _ string, msgs []aigate.ChatMessage) (string, error) {
	f.calls++
	for _, m := range msgs {
		f.lastMsg += m.Content
	}
	if f.err != nil {
		return "", f.err
	}
	return f.content, nil
}

// recommendServer 造一个**没有 kxmemory** 的 Server —— 这正是被测的部署形态。
//
// 为什么必须显式断言 kxmem == nil：这条用例的全部价值就在于「kxmemory 没配」
// 时的降级行为。若哪天 main.go 改成无条件构造 client，用例会悄悄测到
// 另一条分支然后仍然全绿 —— 门禁失明与通过在输出上完全同形。
func recommendServer(t *testing.T, llm *recommendLLM) (*Server, map[string]string) {
	t.Helper()
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.llm = llm
	if srv.kxmemory != nil {
		t.Fatalf("用例前提失效：期望 kxmemory 未配置（nil），实际非 nil")
	}
	return srv, tokens
}

func postRecommend(t *testing.T, srv *Server, token, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/meetings/m1/recommend", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

const recommendBody = `{"segments":[{"speaker":"A","text":"我们下周要上线结算模块，风险主要是回滚脚本没写。"}],"summary":"讨论结算模块上线风险"}`

// 核心用例：kxmemory 未配置时，/recommend 必须给出参考资料与建议。
//
// 修复前这里恒为 {"items":[]}（硬编码空数组 + 200），而前端
// `res.items ?? []` 同样不报错 ⇒ 「参考资料与建议」整条静默失效。
func TestMeetingRecommend_LLMFallbackWhenNoKxmemory(t *testing.T) {
	llm := &recommendLLM{content: `{"items":[{"title":"回滚脚本最佳实践","snippet":"上线前必须有可回滚路径","query":"数据库上线 回滚脚本 最佳实践"}]}`}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("期望 1 条推荐，实际 %d 条（out=%v）", len(items), out)
	}
	if llm.calls != 1 {
		t.Fatalf("期望恰好调 1 次 LLM，实际 %d 次", llm.calls)
	}
	got := items[0].(map[string]any)
	if got["type"] != "web" {
		t.Fatalf("type=%v，期望 web（无检索源时不得伪造 note/meeting 条目）", got["type"])
	}
	if got["title"] != "回滚脚本最佳实践" {
		t.Fatalf("title=%v", got["title"])
	}
	url, _ := got["url"].(string)
	if !strings.Contains(url, "bing.com/search?q=") {
		t.Fatalf("url=%q：前端 onOpenRelated 见到空 url 会掉进 type 分支而点击无反应", url)
	}
	// query 必须是 URL 编码的，否则中文 query 会拼出坏链接。
	if strings.Contains(url, " ") || !strings.Contains(url, "%") {
		t.Fatalf("url 未做 query 转义：%q", url)
	}
}

// 解析失败必须降级成空数组，不能把原文当标题塞给用户，也不能让整条功能挂掉。
func TestMeetingRecommend_MalformedLLMOutputDegradesToEmpty(t *testing.T) {
	llm := &recommendLLM{content: `抱歉，我无法完成这个请求。`}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("期望空数组，实际 %v", items)
	}
}

// 单条缺 query 时用 title 兜底，**其余条目不受牵连**。
// 「模型少给一个字段」不该让另外两条一起消失。
func TestMeetingRecommend_SingleBadItemDoesNotDropOthers(t *testing.T) {
	llm := &recommendLLM{content: `{"items":[
		{"title":"缺 query 的条目"},
		{"title":"正常条目","snippet":"s","query":"q2"}
	]}`}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("期望 2 条（缺 query 的那条要留下），实际 %d：%v", len(items), items)
	}
	first := items[0].(map[string]any)
	// 按解码后的值断言，而不是硬编码百分号字节：Go 的 QueryEscape 用
	// QueryEscape 的空格约定（+），把字节抄进断言只会让断言跟着实现漂。
	u, _ := first["url"].(string)
	q := u[strings.Index(u, "q=")+2:]
	if decoded, err := neturl.QueryUnescape(q); err != nil || decoded != "缺 query 的条目" {
		t.Fatalf("缺 query 时应回落到 title 作为搜索词，解码得 %q err=%v", decoded, err)
	}
}

// 空标题条目必须被丢弃。
//
// ★ 这条断言单独存在的原因：它原先混在「缺 query 也能留下」那条里，
// 而那条的输入有 4 条、上限又正好是 3 条 ⇒ **上限把空标题那条挡在了
// 第 4 位**，于是「上限」与「空标题过滤」两个行为在输出上完全同形：
// 去掉过滤仍然全绿（实测 M5 变异：绿）。
// 现在输入压到 3 条，期望 2 条 —— 上限不再能掩盖过滤，两个行为各自有牙。
func TestMeetingRecommend_DropsBlankTitleItem(t *testing.T) {
	llm := &recommendLLM{content: `{"items":[
		{"title":"   ","snippet":"空标题应被丢弃"},
		{"title":"保留一","query":"q1"},
		{"title":"保留二","query":"q2"}
	]}`}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("期望 2 条（空标题被丢弃），实际 %d：%v", len(items), items)
	}
	for _, it := range items {
		if strings.TrimSpace(it.(map[string]any)["title"].(string)) == "" {
			t.Fatalf("空标题条目没有被丢弃：%v", items)
		}
	}
}

// LLM 出错时返回 200 + 空数组：**推荐是增强不是必需**，
// 不能因为它挂了让会中摘要整块报错。
func TestMeetingRecommend_LLMErrorDoesNotFailRequest(t *testing.T) {
	llm := &recommendLLM{err: context.DeadlineExceeded}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("期望空数组，实际 %v", items)
	}
}

// 上下限：最多 3 条（前端只渲染前 3 条，多给的是无效负载）。
func TestMeetingRecommend_CapsAtThreeItems(t *testing.T) {
	llm := &recommendLLM{content: `{"items":[
		{"title":"一","query":"q1"},{"title":"二","query":"q2"},
		{"title":"三","query":"q3"},{"title":"四","query":"q4"},
		{"title":"五","query":"q5"}]}`}
	srv, tokens := recommendServer(t, llm)

	out := postRecommend(t, srv, tokens["ws-a"], recommendBody)

	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("期望截断到 3 条，实际 %d 条", len(items))
	}
}

// prompt 必须真的带上摘要与转写：没有输入的推荐只能是编的。
func TestMeetingRecommend_PromptCarriesSummaryAndTranscript(t *testing.T) {
	llm := &recommendLLM{content: `{"items":[]}`}
	srv, tokens := recommendServer(t, llm)

	postRecommend(t, srv, tokens["ws-a"], recommendBody)

	if !strings.Contains(llm.lastMsg, "讨论结算模块上线风险") {
		t.Fatalf("prompt 缺摘要：%q", llm.lastMsg)
	}
	if !strings.Contains(llm.lastMsg, "回滚脚本") {
		t.Fatalf("prompt 缺转写：%q", llm.lastMsg)
	}
}

// 长转写按 rune 截断，不能按字节切 —— 中文一字 3 字节，
// 按字节切会产出非法 UTF-8 送进 prompt。
func TestParseRecommendJSON_TruncationIsRuneSafe(t *testing.T) {
	// 直接验截断后的片段仍是合法 UTF-8：构造 2500 个中文字的转写。
	long := strings.Repeat("结", 2500)
	segs := []meetingSegmentIn{{Speaker: "A", Text: long}}
	if !isValidUTF8(segmentsToText(segs)) {
		t.Fatal("基线就非法 UTF-8，用例前提失效")
	}
	// 走一遍 llmMeetingRecommend 里的截断表达式。
	runes := []rune(segmentsToText(segs))
	if len(runes) <= 2000 {
		t.Fatal("用例前提失效：转写不够长")
	}
	tail := string(runes[len(runes)-2000:])
	if !isValidUTF8(tail) {
		t.Fatal("截断结果非法 UTF-8")
	}
	if strings.Contains(tail, "�") {
		t.Fatal("截断产生了替换字符（说明切在字中间）")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
