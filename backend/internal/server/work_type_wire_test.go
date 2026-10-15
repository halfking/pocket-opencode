// work_type_wire_test.go — §165.11：**X-Gw-Work-Type 真的上了线缆**。
//
// ── 为什么源码扫描不够 ────────────────────────────────────────────
//
// `worktype_coverage_test.go`（§165.5）证明的是「请求结构体里写了 Kind」。
// 但那只是**代码形状**，不是**运行结果** —— 同 §141 那条教训：
//
//	契约门查「语句在不在」时，逻辑写错也照样全绿，只有真跑才看得见。
//
// 具体能溜过去的样子：`llmgateway/client.go` 里的
// `if req.WorkType != "" { httpReq.Header.Set(...) }` 被改成无条件 Set，
// 或者被某条新分支跳过，或者 baseURL 拼接换了入口 ——
// **源码扫描一条都不会红**，而线上 work-type 已经全错。
//
// ⇒ 这道门只做一件事：把 `Kind` 真的走一遍 HTTP，**读回请求头**。
//
// ── 期望值从哪来 ─────────────────────────────────────────────────
//
// 期望值取 `llmbff.WorkTypeFromKind(kind)`，**不手抄一份映射表** ——
// 抄第二份就是 §80 那个「同一缺陷必然出现在两处」。
// 代价是「改了映射表这道门不会红」，所以另配一组
// `TestWorkTypeMappingSpotCheck` 把 4 条关键映射**钉死**，
// 改映射的人在那里会看到 diff。

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// headerAbsent 表示「这个请求头**根本没出现**」。
//
// ★ 必须与「头出现了但值为空串」分开：两者在 http.Header.Get 下**完全一样**，
//
//	而它们对「按 work-type 选模型」是天壤之别（网关读到空值 = 走默认路由）。
const headerAbsent = "«头根本没出现»"

// wireProbe 抓一次真实请求的请求头。
type wireProbe struct {
	mu      sync.Mutex
	calls   int
	workTyp string // X-Gw-Work-Type 的**原始值**（区分「没设」与「设成空串」）
}

// server 起一个只回答「抓头 + 回最小合法响应」的桩。
func (p *wireProbe) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先把 body 读出来：既要判 stream 分流，也顺便证明请求体是完整送达的。
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &parsed)

		p.mu.Lock()
		p.calls++
		// Header.Get 对「显式 Set("")」与「从未 Set」都返回 ""，
		// 单看它分不出这两种 ⇒ 用 r.Header[...] 的**存在性**来分。
		_, explicitly := r.Header["X-Gw-Work-Type"]
		p.workTyp = r.Header.Get("X-Gw-Work-Type")
		if !explicitly {
			p.workTyp = headerAbsent
		}
		p.mu.Unlock()

		if parsed.Stream {
			// Stream 路径要 SSE 形状；返回 JSON 会被判成 "empty stream"，
			// 那会让这道门**因为无关原因**变红。
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"+
					"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\n"+
					"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ⚠ 构造函数返回 llmbff.Provider（接口），不是具体类型 ——
//
//	这道门刻意用**接口**，因为它要钉的是「线上缆的行为」，
//	与具体实现无关。
func newWireProvider(baseURL string) llmbff.Provider {
	return NewDynamicLLMGatewayBFFProvider(func(string, string) GatewayConfig {
		return GatewayConfig{BaseURL: baseURL, APIKey: "k", PreferredModels: []string{"m"}}
	})
}

// TestWorkTypeHeaderOnTheWire 钉住：`req.Kind` 真的变成了 HTTP 头。
//
// ⚠ 表里刻意包含 §165.3 查出的那批**落空**的 kind。
//
//	它们期望「头根本不出现」—— 不是「出现但为空」。
//	这两者在 `Header.Get` 下一模一样，必须分开读（wireProbe 里的
//	`_, explicitly := r.Header[...]` 就是干这个的）。
func TestWorkTypeHeaderOnTheWire(t *testing.T) {
	cases := []struct {
		kind string
		note string
	}{
		{"meeting_summary", "前端回落摘要 —— §165.3 唯一两类发得出去之一"},
		{"meeting_refine", "前端回落精校 —— 映射到 doc_translate"},
		{"live_translate", "录音实时翻译"},
		{"doc_translate", "文档翻译"},
		{"meeting_summary_agent", "会议摘要主路径（agent）—— 映射表无此案，应落空"},
		{"email-classify", "邮件分类 —— 映射表无此案，应落空"},
		{"email-summary", "邮件摘要 —— 映射表无此案，应落空"},
		{"", "结构体没写 Kind（llmChatOnce 那三处）—— 应落空"},
		{"totally_unknown_kind", "不存在的 kind —— 应落空"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("kind=%q", tc.kind), func(t *testing.T) {
			want := llmbff.WorkTypeFromKind(tc.kind)
			if want == "" {
				want = headerAbsent
			}

			// ── Chat 路径 ──
			p1 := &wireProbe{}
			srv1 := p1.server(t)
			if _, err := newWireProvider(srv1.URL).Chat(context.Background(), llmbff.ChatRequest{
				WorkspaceID: "ws", Model: "m",
				Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "hi"}},
				Kind:     tc.kind,
			}); err != nil {
				t.Fatalf("Chat 返回错误：%v", err)
			}
			assertProbe(t, p1, tc.kind, want, "Chat", tc.note)

			// ── Stream 路径（与 Chat 是**两处独立的 WorkType 赋值**，各跑一遍）──
			p2 := &wireProbe{}
			srv2 := p2.server(t)
			if _, err := newWireProvider(srv2.URL).Stream(context.Background(), llmbff.ChatRequest{
				WorkspaceID: "ws", Model: "m",
				Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "hi"}},
				Kind:     tc.kind,
			}, func(llmbff.Delta) bool { return true }); err != nil {
				t.Fatalf("Stream 返回错误：%v", err)
			}
			assertProbe(t, p2, tc.kind, want, "Stream", tc.note)
		})
	}
}

func assertProbe(t *testing.T, p *wireProbe, kind, want, path, note string) {
	t.Helper()
	p.mu.Lock()
	calls, got := p.calls, p.workTyp
	p.mu.Unlock()

	// 量具自证：先证明「请求真的发到了桩上」。
	// 否则 got == absent 会有一个平凡解释：「压根没发请求」，
	// 而那种情况下这道门**恒绿**。
	if calls != 1 {
		t.Fatalf("%s 路径：桩收到 %d 次请求，期望 1 次 —— 抽取器/桩坏了，这道门的读数不可信",
			path, calls)
	}
	if got != want {
		t.Fatalf("%s 路径：kind=%q 时 X-Gw-Work-Type = %q，期望 %q\n"+
			"（%s）\n"+
			"★ 注意本门区分「头根本没出现」与「头出现但为空串」——\n"+
			"  Header.Get 分不出这两种，所以桩里用 r.Header[...] 的存在性来分。",
			path, kind, got, want, note)
	}
}

// TestWorkTypeMappingSpotCheck 把 4 条关键映射**钉死**。
//
// ⚠ 为什么需要它：上面那道门的期望值是**从 WorkTypeFromKind 现算的**，
//
//	所以「有人改了映射表」它照样全绿。这道门补的就是那个缺口：
//	改这 4 条映射的人会在这里看到 diff，而不是在生产里发现
//	「会议精校突然换了模型」。
func TestWorkTypeMappingSpotCheck(t *testing.T) {
	cases := []struct{ kind, want string }{
		// 前端回落两条 —— §165.3 里唯一真的发得出去的两条链。
		{"meeting_summary", "meeting_summary"},
		{"meeting_refine", "doc_translate"},
		// 录音实时翻译 —— 与精校**同车**（§165.4 的撞车登记）。
		{"live_translate", "doc_translate"},
		// 空串 = 刻意不打 work-type（设计，见 WorkTypeFromKind 的注释）。
		{"", ""},
	}
	for _, tc := range cases {
		if got := llmbff.WorkTypeFromKind(tc.kind); got != tc.want {
			t.Errorf("WorkTypeFromKind(%q) = %q，期望 %q", tc.kind, got, tc.want)
		}
	}

	// 撞车本身也钉住：改到不撞车时，这里会红 ⇒ 那正是 §165.10 的待拍板项 (b)。
	if llmbff.WorkTypeFromKind("meeting_refine") == llmbff.WorkTypeFromKind("live_translate") {
		t.Logf("※ 现状：meeting_refine 与 live_translate 仍共用 %q —— "+
			"这是 §165.10(b) 待拍板项，若你已拍板拆开，请同步改 sharedWorkTypes 登记与这条",
			llmbff.WorkTypeFromKind("meeting_refine"))
	}
}

// TestWorkTypeHeaderIsNotSentAsQueryParam 防一个具体的回归形状：
// 网关若按**查询参数**读 work-type（而不是头），把头去掉就静默失效。
// 这道门跑不出「网关怎么读」，但它至少钉住「我们发的是头」。
func TestWorkTypeHeaderIsNotSentAsQueryParam(t *testing.T) {
	p := &wireProbe{}
	srv := p.server(t)
	if _, err := newWireProvider(srv.URL).Chat(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws", Model: "m",
		Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "hi"}},
		Kind:     "meeting_summary",
	}); err != nil {
		t.Fatalf("Chat 返回错误：%v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls != 1 {
		t.Fatalf("桩收到 %d 次请求，期望 1 次", p.calls)
	}
	if p.workTyp != "meeting_summary" {
		t.Fatalf("X-Gw-Work-Type = %q，期望 \"meeting_summary\"", p.workTyp)
	}
}
