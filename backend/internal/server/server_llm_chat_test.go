package server

// server_llm_chat_test.go — /api/llm/chat 迁移动态网关（BFF）后的行为回归。
//
// ⚠⚠ §921 起，本文件**还**守着**全仓「长度限制」清单**（`lengthLimitRegistry`）
//   —— ⚠ 因为闸有 4 道 A 类字节闸散在两个 handler（`server_assistant.go` 与
//   `server_llmbff.go`）里，而「它们在哪个文件」这件事在 §918 被人工核对时**漏了一道**
//   ⇒ 本文件后半段的清单门是那道漏的**结构性补丁**。
//   ⇒ 清单门与本文件原有的跨语言常量门是**两件事**：前者守「有没有漏登记」，
//     后者守「登记的值与服务端一不一致」。
//
// 关键契约（2026-09-05 迁移，见 runbook §14/§15）：
//  1. llmBFF 已装配时，即使静态 s.llm 为 nil（启动时未配 POCKET_LLM_API_KEY），
//     /api/llm/chat 也必须 200 —— 旧的恒 503 分支已消除；
//  2. 网关未配置（provider 缺失 → ErrNotConfigured）→ 503，错误信息指向
//     动态网关配置入口（/api/llm-gateway/config）；
//  3. 响应形状保持 {"content","model"}，前端（meetings.ts 兜底路径）无需改动。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
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

// §921：全仓「长度限制」清单 —— **防漏门**。
//
// ⚠ 为什么要有它（这是本节的起因）：§918 逐条人工核对「A 类内容长度闸全仓只有 3 道」
//
//	⇒ §919 实测发现**第 4 道**（/api/llm/stream 的 32000）⇒ **人工清单 ≠ 全集**
//	⇒ 缺陷复发（本仓这一族已经复发四次：§916 / §917 / §918 / §919）。
//	⇒ 本门把清单变成**数据**，并用 **go/parser（官方 AST）**把全集扫出来逐处比对。
//
// ⚠ 三条口径（每一条都是本节的**量具事故**换来的，三次连栽）：
//
//	① **必须用 go/parser**，不能正则、也不能手写剥注释：
//	   · 正则的 `[^;]+` 贪婪吃到 `{` ⇒ 阈值变成 "16000 {" ⇒ 候选 **0 处**
//	   · 手写逐字符状态机 ⇒ 未闭合的 `"` 把后面整段当字符串 ⇒ 候选 **0 处**
//	   · 行级块注释状态机 ⇒ 块注释吃掉 **14 处**真实代码（含 4 道 A 类闸）
//	   ⇒ 三次都表现为「0 命中」⇒ 记忆里那条「0 命中的第一嫌疑是量具坏了」当场命中。
//	② **必须钉住候选的绝对条数**（65）⇒ 否则扫描器坏了门也绿（**假绿**）。
//	③ 键 = 「文件 + 表达式文本」⇒ 同一键可重复 ⇒ 门用**多重集**比对。
//
// ⚠ **已知局限**（明确写出，不假装覆盖）：
//
//	· **只管 `len()` 形态**。请求体上限（`http.MaxBytesReader` / `io.LimitReader(r.Body, …)`，
//	  §918 的 C 类：全局 2 MiB + 聊天 32 MiB + 移动端 1 MiB）**不在本清单内**
//	  ⇒ 新增一道请求体上限，本门**不会**红。
//	· 只认**字面量阈值**（含 `N<<K`）⇒ 阈值来自常量或变量时扫不到。
//	· 只扫 `backend/internal/`（不含 `cmd/`）。
//	· kind 只是**人工分类**（A = 客户端可提交字段上的限制）：门能防「新增一处却没登记」，
//	  并用**第二份钉住清单**（lengthLimitRegistryAKeys）保证「清单里的 A 类集合」与
//	  「被钉住的 A 类集合」一致 ⇒ **A↔O 对调**（条数守恒的那种）**也会红**。
//	  ⚠ 但「这个键到底算不算客户端可提交」本身仍是**人工事实**：门保证的是**一致**，
//	    保证不了**正确** ⇒ 本节实测人工分类就错了 4 处（2 条 O→A、2 条 A→O）⇒
//	    所以每条 A 类的 note 都写成**带证据**的形式（来源 + 拒绝还是截断 + 单位），好让下一个人复核。
var lengthLimitRegistry = []struct {
	file, expr, kind, note string
}{
	{"adapter/disk/codex.go", "len(rest) > 20", "O", ""},
	{"adapter/disk/parse.go", "len(tag) > 60", "O", ""},
	{"agentbridge/store.go", "len(s) >= 2", "O", ""},
	{"agentbridge/store.go", "len(s) >= 2", "O", ""},
	{"auth/email_codes.go", "len(s) > 254", "A", "客户端提交的**邮箱地址**（发送验证码路径 email_codes.go:73 ⇒ ErrEmailInvalid）；`len(s)`=**字节** ⇒ 254 字节。**可达性：可达·不可区分**（超长邮箱与「格式不对」回**同一个**错误 ⇒ 用户看不出是「太长」）⇒ 用户可改短重试 ⇒ 只登记不修"},
	{"db/pg.go", "len(s) > 63", "O", ""},
	{"email/fetcher.go", "len(s) > 64", "O", ""},
	{"email/invoice.go", "len(raw) > 64 << 10", "O", ""},
	{"email/invoice_file.go", "len(data) >= 12", "O", ""},
	{"email/invoice_file.go", "len(data) >= 3", "O", ""},
	{"email/invoice_file.go", "len(data) >= 8", "O", ""},
	{"email/invoice_harvest.go", "len(b) > 1024", "O", ""},
	{"email/invoice_harvest.go", "len(out) > 10", "O", ""},
	{"email/invoice_harvest.go", "len(u) > 2000", "O", ""},
	{"email/store_folders.go", "len(entries) > 200", "O", ""},
	{"email/store_folders.go", "len(ids) > 200", "O", ""},
	{"email/store_folders.go", "len(ids) > 500", "O", ""},
	{"email/store_folders.go", "len(keys) > 500", "O", ""},
	{"feishu/sheet.go", "len(values) > 5000", "O", ""},
	{"finance/recognizer.go", "len(m) >= 2", "O", ""},
	{"finance/recognizer.go", "len(m) >= 3", "O", ""},
	{"mcp/client.go", "len(msg) > 512", "O", ""},
	{"mcp/client.go", "len(parts) >= 2", "O", ""},
	{"migration/service.go", "len(last) > 200", "O", ""},
	{"migration/service.go", "len(text) > 500", "O", "从 **adapter 消息派生**的状态摘要（**截断** 500 字节，不是拒绝）"},
	{"orchestrator/acc_dispatcher.go", "len(prompt) > 64", "O", ""},
	{"rss/sharecard.go", "len(lines) > 4", "O", ""},
	{"rss/sharecard.go", "len(summaryLines) > 6", "O", ""},
	{"server/flashcards_handler.go", "len([]rune(name)) > 80", "A", "客户端提交的**卡片名**；`[]rune` ⇒ **码元**闸（80 码元，不是 80 字节）；400「name too long (max 80)」。**可达性：可达·可区分**（⚠ 但**错误信息没写单位**：80 码元 ≠ 80 字节）⇒ 用户可改短重试 ⇒ 只登记不修"},
	{"server/flashcards_handler.go", "len(parts) >= 2", "O", ""},
	{"server/flashcards_handler.go", "len(parts) >= 2", "O", ""},
	{"server/flashcards_handler.go", "len(parts) >= 3", "O", ""},
	{"server/llm_gateway_admin_client.go", "len(trimmed) > 200", "O", "**网关错误体**截断（服务端派生，不是客户端提交的字段）"},
	{"server/llm_gateway_nodes_proxy.go", "len(s) > 64", "O", ""},
	{"server/mobile_api.go", "len(tokenString) > 7", "O", ""},
	{"server/mobile_idempotency.go", "len(c.entries) > 4096", "O", ""},
	{"server/mobile_idempotency.go", "len(c.entries) > 4096", "O", ""},
	{"server/server.go", "len(path) > 6", "O", ""},
	{"server/server.go", "len(path) > 8", "O", ""},
	{"server/server.go", "len(path) > 8", "O", ""},
	{"server/server_assistant.go", "len(audioData) > 25 << 20", "A", "客户端**上传**的音频（解码后字节）；400「audio too large (max 25 MB)」。⚠ **可达性：不可达**（**§922 实测**）：前端发的是 JSON+base64（体积 ×4/3）⇒ 外层 25 MiB 卡的是**请求体** ⇒ 解码后最多 ~18.75 MiB ⇒ **永远到不了 25 MiB** ⇒ 这条文案对 JSON 形态是**死文案**（multipart 形态才可达）⇒ §922 已把「太大」与「无效」分开（413 + `stt_audio_too_large`）"},
	{"server/server_assistant.go", "len(body.Messages) > 50", "A", "消息**条数**（计数）。**可达性：不可达**（前端每次只发 1~2 条）⇒ 登记"},
	{"server/server_assistant.go", "len(body.Subject) > 500", "A", "邮件主题 500 **字节**（§918 已修：前端按字节裁尾保前缀）。**可达性：可达·可区分**（400 带单位）⇒ 已修"},
	{"server/server_assistant.go", "len(body.Text) > 16000", "A", "embed 文本 16000 **字节**（§917 已修：切段器两道字节保险）。**可达性：可达·可区分** ⇒ 已修"},
	{"server/server_assistant.go", "len(m.Content) > 32000", "A", "chat 消息 32000 **字节**（§916 已修：按字节装配 + 转写取尾部）。**可达性：可达·可区分** ⇒ 已修"},
	{"server/server_assistant.go", "len(runes) > 60", "O", ""},
	{"server/server_chatagent.go", "len(result) > 20", "O", ""},
	{"server/server_email_classify_gateway.go", "len([]rune(summary)) > 60", "O", ""},
	{"server/server_email_folders.go", "len(name) > 200", "A", "客户端提交的**邮箱文件夹名**；400「folder name too long」。**可达性：可达·可区分**（⚠ **错误信息没写单位**）⇒ 用户可改短重试 ⇒ 只登记不修"},
	{"server/server_email_purge.go", "len(body.IDs) > 100", "A", "ID **条数**（计数）；400「too many ids」。⚠ **可达性：可达·不可区分**（**§922 实测**：前端旧实现一次全发 ⇒ 选中 >100 封时服务端 400 被调用方**空 catch 吞掉** ⇒ **本地已清、服务端没清**（正文其实没清掉），界面毫无提示）⇒ **§922 已修**：前端按上限分批（跨语言契约钉住）+ 失败出声"},
	{"server/server_llmbff.go", "len(body.Messages) > 50", "A", "消息**条数**（计数）。**可达性：不可达**（localagent 最多 1 system + 1 user + 12×2 = **26 条**，`maxSteps` 默认 12）⇒ 登记（⚠ 若 `maxSteps` 提到 25 以上就会撞）"},
	{"server/server_llmbff.go", "len(body.Tools) > 20", "A", "工具**条数**（计数）。**可达性：不可达**（前端 `ChatStreamInput` **没有 tools 字段** ⇒ 从不发）⇒ 登记"},
	{"server/server_llmbff.go", "len(m.Content) > 32000", "A", "stream 消息 32000 **字节**（§919 已修：`fitToStreamGate`）。⚠ **可达性：可达·可区分**，但 §919 的修复**只管 `meetings-ai.ts` 那条路**；**localagent 那条路没有字节闸** ⇒ 现在靠两个魔数兜着（`toolResultLimit=4000` 字符 ⇒ ≤12000 字节；`maxSteps=12` ⇒ ≤26 条）⇒ 这两个数**与服务端的闸没有契约** ⇒ 登记（⚠ 调大任一个就会开始 400）"},
	{"server/server_marketplace.go", "len(parts) >= 3", "O", ""},
	{"server/server_marketplace.go", "len(s) > 1024", "O", ""},
	{"server/session_transcript.go", "len([]rune(summary)) > 400", "O", ""},
	{"server/session_transcript.go", "len(runes) > 80", "O", ""},
	{"server/task_collaboration_handler.go", "len(clientEventID) > 80", "A", "客户端提交的**事件 ID**；超限返回 error。**可达性：不可达**（前端生成 `client-<base36>-<8>` ≈ **23 字符**）⇒ 登记"},
	{"stt/sniff.go", "len(data) >= 12", "O", ""},
	{"stt/sniff.go", "len(data) >= 12", "O", ""},
	{"stt/sniff.go", "len(data) >= 2", "O", ""},
	{"stt/sniff.go", "len(data) >= 3", "O", ""},
	{"stt/sniff.go", "len(data) >= 4", "O", ""},
	{"stt/sniff.go", "len(data) >= 4", "O", ""},
	{"stt/sniff.go", "len(data) >= 4", "O", ""},
}

// lengthLimitRegistryAKeys = 清单里**必须**是 A 类的那些键（文件 + 表达式文本）。
//
// ⚠⚠ **为什么不能只钉「A 类条数」**（这是本节实测出来的，不是假想）：
//
//	本节把 4 条改对了分类（**2 条 O→A、2 条 A→O**）⇒ **A 类条数恰好不变（13 → 13）**
//	⇒ 只钉条数的话，门对「A↔O 对调」**完全无感、照样全绿**。
//	⇒ 条数是**守恒量**，对调是它的盲区。
//
// ⇒ 改为钉**键的集合**：集合在 A↔O 对调下**不守恒** ⇒ 门立刻红。
//
// ⚠ **仍然存在的局限**（不假装覆盖）：「这个键到底算不算客户端可提交」本身是
//
//	**人工事实**。这份钉住清单只能保证「清单与钉住清单**一致**」，
//	保证不了「事实**正确**」⇒ 本节已实证人工分类会错（4 处），所以每条 A 类的
//	note 都写成了**带证据**的形式（来源 + 是拒绝还是截断 + 单位），好让下一个人能复核。
var lengthLimitRegistryAKeys = []lengthLimitGateKey{
	{"auth/email_codes.go", "len(s) > 254"},
	{"server/flashcards_handler.go", "len([]rune(name)) > 80"},
	{"server/server_assistant.go", "len(audioData) > 25 << 20"},
	{"server/server_assistant.go", "len(body.Messages) > 50"},
	{"server/server_assistant.go", "len(body.Subject) > 500"},
	{"server/server_assistant.go", "len(body.Text) > 16000"},
	{"server/server_assistant.go", "len(m.Content) > 32000"},
	{"server/server_email_folders.go", "len(name) > 200"},
	{"server/server_email_purge.go", "len(body.IDs) > 100"},
	{"server/server_llmbff.go", "len(body.Messages) > 50"},
	{"server/server_llmbff.go", "len(body.Tools) > 20"},
	{"server/server_llmbff.go", "len(m.Content) > 32000"},
	{"server/task_collaboration_handler.go", "len(clientEventID) > 80"},
}

// lengthLimitGateKey 是清单/扫描共用的键。
type lengthLimitGateKey struct {
	file, expr string
}

// scanLengthLimits 用 **go/parser** 扫出 backend/internal/ 下所有
// 「len(x) 与字面量阈值比较」的出现处。
//
// ⚠ 它**故意**与清单生成器同款：同一套判定（BasicLit 或 `N<<K`、
//
//	阈值 ≥ 2、只看 `>` / `>=`）⇒ 清单与扫描口径**逐字一致**，不会互相漂。
func scanLengthLimits(t *testing.T) map[lengthLimitGateKey]int {
	t.Helper()
	fset := token.NewFileSet()
	got := map[lengthLimitGateKey]int{}
	root := ".."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// ⚠ 跳过 `testdata` / `vendor`：Go 工具链本来就不编译这两个目录。
			//   · testdata 里可能有**故意写坏**的 .go（供测试解析失败用）⇒ 解析失败会让本门 Fatal；
			//   · vendor 里全是第三方代码 ⇒ 会扫出一堆与本仓无关的 `len(x) > N`。
			if d.Name() == "testdata" || d.Name() == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败：%v（门依赖官方 AST ⇒ 解析不了就不能比对）", p, perr)
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		ast.Inspect(f, func(n ast.Node) bool {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || (be.Op != token.GTR && be.Op != token.GEQ) {
				return true
			}
			call, ok := be.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "len" || len(call.Args) != 1 {
				return true
			}
			threshold := ""
			switch y := be.Y.(type) {
			case *ast.BasicLit:
				if y.Kind == token.INT {
					threshold = y.Value
				}
			case *ast.BinaryExpr:
				// ⚠ `len(x) > 64 << 10` 这类：右侧不是 BasicLit
				//   ⇒ 只认 BasicLit 的口径会漏掉它们（实测漏 2 处，
				//   其中一处正是 §918 记的「音频 25 MiB」）。
				if y.Op == token.SHL {
					if l, ok3 := y.X.(*ast.BasicLit); ok3 && l.Kind == token.INT {
						threshold = exprText(fset, be.Y)
					}
				}
			}
			if threshold == "" {
				return true
			}
			base := strings.TrimSpace(strings.SplitN(threshold, "<<", 2)[0])
			v, cerr := strconv.Atoi(base)
			if cerr != nil || v < 2 {
				return true
			}
			op := ">"
			if be.Op == token.GEQ {
				op = ">="
			}
			expr := fmt.Sprintf("len(%s) %s %s", exprText(fset, call.Args[0]), op, threshold)
			got[lengthLimitGateKey{rel, expr}]++
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 %s 失败：%v", root, err)
	}
	return got
}

// exprText 打印一个 AST 表达式的源码文本（与清单生成器同款 ⇒ 逐字可比）。
func exprText(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, e)
	return b.String()
}

// §921 的门本体：**清单与 AST 扫出来的全集必须逐处一致**（双向）。
//
// ⚠ 双向：只查「清单里的每条都还在」⇒ 新增一处没人登记就不会红（那正是本节要防的）；
//
//	只查「扫到的每条都在清单里」⇒ 清单里留着一行过期记录也没人发现。
func TestLengthLimitRegistryCoversEveryLengthGate(t *testing.T) {
	got := scanLengthLimits(t)

	// ⚠ 量具自证：候选数必须**恰好**等于清单条数。
	//   ⇒ 扫描器坏了（读到 0 处）时这道门会红，而不是**恒绿**。
	//   ⚠ 这不是假设：本节的探针连续两次读到「0 处」（正则贪婪 / 状态机卡住）。
	if len(got) == 0 {
		t.Fatalf("量具失效：AST 扫出 0 处长度限制 ⇒ 门会变成空气门（改判据前先看扫描器）")
	}
	total := 0
	for _, n := range got {
		total += n
	}
	if total != len(lengthLimitRegistry) {
		// ⚠⚠ 用 Errorf **不是** Fatalf：后面那段「逐处点名」必须**也跑**
		//   ⇒ 否则「新增了一处没人登记」只会被报成「总数不对」，
		//   维护者还得自己去找是哪一处 ⇒ 判据的**可用性**打折。
		//   ⚠ 这不是假设：变异 V3/V4 第一次跑时，红的**全部**是这一条，
		//   点名的那段根本没执行 ⇒ 「能定位到具体哪一处」当时**没被证明**。
		t.Errorf("长度限制出现处共 %d 处，清单只有 %d 条 ⇒ 差 %d 处未登记（下面会逐处点名）",
			total, len(lengthLimitRegistry), total-len(lengthLimitRegistry))
	}

	want := map[lengthLimitGateKey]int{}
	for _, r := range lengthLimitRegistry {
		want[lengthLimitGateKey{r.file, r.expr}]++
	}
	// ① 清单里多出来的（已失效的行）
	for k, n := range want {
		if got[k] == 0 {
			t.Errorf("清单里的 %s %q 在源码里已经找不到了 ⇒ 它已失效，请更新或删掉这一行", k.file, k.expr)
			continue
		}
		if got[k] != n {
			t.Errorf("清单里 %s %q 记了 %d 处，源码里实际 %d 处 ⇒ 清单与现实不符", k.file, k.expr, n, got[k])
		}
	}
	// ② 源码里多出来的（**新增却没人登记** ⇒ 本门要抓的就是这个）
	for k, n := range got {
		if want[k] == 0 {
			t.Errorf("源码里新增了一处长度限制：%s %q（%d 处）⇒ 必须登记到 lengthLimitRegistry，"+
				"否则前端侧没人知道这道闸的存在（§918 漏掉 /api/llm/stream 就是这么漏的）", k.file, k.expr, n)
		}
	}

	// ③ A 类（客户端可提交字段上的限制）：**钉住键的集合**，不是钉条数。
	//
	// ⚠⚠ 只钉条数是**守恒量盲区**：本节实测把 4 条改对分类（2 条 O→A、2 条 A→O）
	//   ⇒ A 类条数恰好不变（13 → 13）⇒ 只钉条数时门**全绿**，对 A↔O 对调**完全看不见**。
	//   ⇒ 集合在 A↔O 对调下**不守恒** ⇒ 这里必须红。
	aRows := 0
	aSet := map[lengthLimitGateKey]bool{}
	for _, r := range lengthLimitRegistry {
		switch r.kind {
		case "A":
			aRows++
			aSet[lengthLimitGateKey{r.file, r.expr}] = true
		case "O":
		default:
			t.Errorf("清单里 %s %q 的 kind=%q 非法（只能 A 或 O）", r.file, r.expr, r.kind)
		}
	}
	// ⚠ 量具自证：钉住清单不能是空的 ⇒ 否则下面的集合比对会变成空气门。
	if len(lengthLimitRegistryAKeys) == 0 {
		t.Fatalf("量具失效：lengthLimitRegistryAKeys 是空的 ⇒ A 类比对会变成空气门（改判据前先看钉住清单）")
	}
	// ⚠ 集合会**塌缩**：清单里若出现**重复的 A 类键**，集合比对会漏掉那个重复项。
	if aRows != len(aSet) {
		t.Errorf("A 类有 %d 行但只有 %d 个不同的键 ⇒ 清单里有**重复的 A 类键** ⇒ 集合比对会漏（重复项让集合塌缩）",
			aRows, len(aSet))
	}
	pinned := map[lengthLimitGateKey]bool{}
	for _, k := range lengthLimitRegistryAKeys {
		pinned[k] = true
	}
	for k := range aSet {
		if !pinned[k] {
			t.Errorf("清单里 %s %q 标成了 A 类，但 lengthLimitRegistryAKeys 里没有它 ⇒ A 类集合被改了（"+
				"若同时把别的一条 A 改成 O，**条数守恒、只钉条数根本看不出来**）", k.file, k.expr)
		}
	}
	for k := range pinned {
		if !aSet[k] {
			t.Errorf("lengthLimitRegistryAKeys 里的 %s %q 在清单里不是 A 类 ⇒ 两份清单不一致", k.file, k.expr)
		}
	}
	if len(aSet) != 13 {
		t.Errorf("A 类（客户端可提交字段上的限制）实际 %d 条，期望 13 条 ⇒ "+
			"要么有人把 A 类改成了 O 类，要么新增了一处客户端可提交的限制没分类", len(aSet))
	}

	// ④ §922：**每条 A 类都必须写明「可达性」结论**。
	//
	// ⚠ 为什么必须有：清单只说「有一道闸」，不说「它会不会真的咬到用户」——
	//   §922 的可达性审计就卡在这：13 条里有 **4 条根本触发不了**（外层闸先挡）、
	//   **2 条的错误信息指不到点**（用户改了也不知道是「太长」）。
	//   ⇒ 没有这条强制，下一个人加闸时**不会**去想「它可达吗、用户看得懂吗」。
	//
	// ⚠ 这条只保证「**写下了结论**」，保证不了「结论正确」——可达性是**人工事实**
	//   （同 ④ 的局限：kind 也是人工事实）。它的作用是**逼人回源码读一遍**。
	verdictRe := regexp.MustCompile(`可达性：(不可达|可达·可区分|可达·不可区分)`)
	for _, r := range lengthLimitRegistry {
		if r.kind != "A" {
			continue
		}
		if !verdictRe.MatchString(r.note) {
			t.Errorf("清单里 %s %q 是 A 类，但 note 里没有「可达性：<结论>」⇒ "+
				"§922 的可达性审计对它**没有结论** ⇒ 请回源码读一遍并补上"+
				"（结论只能是「不可达」/「可达·可区分」/「可达·不可区分」三种之一；"+
				"「不可达」通常意味着有更外层的闸先挡着）", r.file, r.expr)
		}
	}
}
