// meeting_sync_identity_test.go — §96：**会议 ID 与字段**契约（POST /api/meetings）。
//
// §92 那道门（summary_request_wire_test.go）守的是 `/summary` 与 `/refine` 的
// **请求体字段**。本文件守的是另一个交接点，而且是**两个**：
//
//	① 请求体字段：前端 syncMeeting() 发 10 个 camelCase 键，
//	   meeting.CreateMeetingRequest **现在 10 个全带 json tag**（§708 实测）。
//	② **身份**：store 现在**接受客户端提供的 id**（§708 实测）；
//	   `nextMeetingID` 降级为「客户端没带 id 时」的兜底。
//
// ⚠⏱ **下面这段实测读数是 §96.2 当时的（修复前）**，已全部过时：
//
//	POST /api/meetings            → 当时 id=mtg_...（请求里的 id 被忽略）
//	                                且 10 个键只有 title/status 出现，
//	                                status 被 store 覆写成常量 "recording"
//	POST /api/meetings/<前端ID>/refine  → 当时 404（LLM 调用数 = 0）
//
// ⭐ **现在的实测读数见本文件末尾那行 `t.Logf("① …")`** —— 它是运行时打出来的，
//
//	不随代码过时。**§718 修完分类逻辑后实测：① 10 个键 → 原样入库 6 + 覆写 0 + 丢弃 4**，
//	tag 实为全部 10 个；其中 `status` 的值 `refined` **等于请求发的值**
//	⇒ **请求值已被采纳**，`overwritten` 那一类已随 §708 的修复失效（恒 0 的分类槽）。
//	⏱ 此前此处写的是「入库 5 + 覆写 1」——那是**旧判法**（按键名硬编码 `k == "status"`）
//	的读数，§718 已改为**按值比较**，故计数由 5/1 变成 6/0。
//
// ★ §105 已落地「ID 怎么对账」：**方案 A**（按 client id upsert，同作用域；
//
//	跨作用域撞号退回服务端 ID）。它一次同时修好上面读数里的 refine 404 与
//	§104 的「一次录音两行」，且 id 列本来就是 TEXT PRIMARY KEY，**不需要迁移**。
//	承重门在 meeting_sync_id_upsert_test.go；本文件里与 ID 相关的读数已删除。
//
// ★ 本文件仍只把**与修法无关的不变量**写成承重门，把「那 9 个字段要不要真的
//
//	入库」留成现状登记 + 待拍板（补列要写迁移，是产品取舍）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// syncIdentityLLM 只为「上游到底被调了几次」计数。
//
// ⚠ 必须自己实现而不是复用 recommendLLM：那条 stub 的字段（calls）是别的门
//
//	在断言的东西，耦合过去等于让两个门共享可变状态，改一个会连累另一个。
type syncIdentityLLM struct{ calls int }

func (f *syncIdentityLLM) Chat(context.Context, string, []aigate.ChatMessage) (string, error) {
	f.calls++
	return `{"refined_transcript":"精校结果","todos":[],"structured_minutes":{}}`, nil
}

// syncIdentityServer 造一个与生产同形的 Server：默认装配的内存 store（生产注入
// PGStore，但 CreateScoped/GetScoped 的 ID 语义与字段解码**完全同形** ——
// 都是"自己生成 ID"、"只解 tag 上有的键"，见 meeting/store.go:82 与 pg_store.go:157）。
//
// 断言 kxmemory == nil：被测形态是「没配 kxmemory」，与 recommend 那道门一致。
// 若哪天 main.go 改成无条件构造 client，用例会悄悄测到另一条分支然后仍然全绿。
func syncIdentityServer(t *testing.T) (*Server, map[string]string, *syncIdentityLLM) {
	t.Helper()
	srv, tokens := newWorkspaceIsolationServer(t)
	if srv.meetingStore == nil {
		t.Fatalf("用例前提失效：meetingStore 为 nil —— 生产 server.New 一定会装配（server.go:282），" +
			"测不到它就等于没测生产形态")
	}
	if srv.kxmemory != nil {
		t.Fatalf("用例前提失效：期望 kxmemory 未配置（nil），实际非 nil")
	}
	llm := &syncIdentityLLM{}
	srv.llm = llm
	return srv, tokens, llm
}

func syncIdentityDo(t *testing.T, srv *Server, token, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// syncBody 就是前端 meetingsApi.syncMeeting 在生产里发出的那一份请求体
// （camelCase，10 个键，取自 frontend/src/api/meetings.ts:178-189）。
//
// ⚠ 它是**测试夹具**，不是「字段清单 SSOT」。真正的字段清单由
//
//	TestMeetingSyncKeyExtractorHasTeeth 从前端源码里现抠 —— 那条门不依赖本常量。
const syncBody = `{
  "id": "meeting-1791309378069-a7f3az",
  "title": "悬界芯片客户评审会",
  "location": "上海张江 12 号楼",
  "participants": ["张伟", "林岚", "王总"],
  "startedAt": 1791309378069,
  "durationMs": 1834000,
  "summary": "对齐悬界芯片二期量产节点",
  "refinedTranscript": "张伟：我们二期在十一月底量产。",
  "noteId": "note-xu7a2",
  "status": "refined"
}`

const refineSegmentsBody = `{"segments":[{"speaker":"张伟","text":"二期十一月底量产。"}],"target_langs":["en"],"meta":{"title":"悬界芯片客户评审会","participants":["张伟","林岚"],"location":"上海张江"}}`

// ── 承重门 1：守卫必须在花上游调用之前就关上 ────────────────────────

// TestMeetingRefineFailClosedBeforeUpstream 钉住 server_meeting.go:592-599 的语义：
// meeting 在调用方作用域里查不到时，**在联系上游之前**就 404。
//
// ★ 为什么要单独钉「LLM 调用数 = 0」：只断言状态码是 404 的话，
//
//	把守卫挪到 llmMeetingRefine 之后、只在写响应时才 404，同样全绿 ——
//	而那意味着**每次无效精校都要先烧一次真实模型的完整 token 与 90s 预算**。
//	「状态码对」与「钱没白花」是两件事，必须分别承重。
//
//	这条与 §96 的修法无关：无论 ID 最后怎么对账，"store 里没有 ⇒ 不许联系上游"
//	都必须成立（它同时是 workspace 隔离的兜底，workspace_isolation_refine_test.go 守另一头）。
func TestMeetingRefineFailClosedBeforeUpstream(t *testing.T) {
	srv, tokens, llm := syncIdentityServer(t)
	tok := tokens["ws-a"]

	// 这个 ID 形态与前端一致，但库里从来没有它。
	const ghostID = "meeting-1791309378069-a7f3az"
	rr := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings/"+ghostID+"/refine", refineSegmentsBody)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("库里没有的会议必须 404，实际 %d body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if llm.calls != 0 {
		t.Fatalf("守卫本该在上游调用之前关上，实际却调了 %d 次 LLM —— "+
			"无效精校会照烧真实 token 与 90s 预算，而 HTTP 上只看得到一个 404", llm.calls)
	}
}

// ── 承重门 2：201 返回的 ID 必须查得到（服务端对自己刚建的行是自洽的）──

// TestCreatedMeetingIsRetrievableByReturnedID 钉住「POST /api/meetings 返回的那个
// id，在同一作用域里查得到」。
//
// 这是与修法无关的底线：任何 ID 对账方案（服务端认客户端 id / 客户端保存服务端 id）
// 都必须让「服务端发出去的 ID 是它自己承认的 ID」。若哪天为了图省事让 201 回一个
// 没入库的 ID（比如直接回显请求里的 id），本条立刻转红。
func TestCreatedMeetingIsRetrievableByReturnedID(t *testing.T) {
	srv, tokens, _ := syncIdentityServer(t)
	tok := tokens["ws-a"]

	rr := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings", syncBody)
	if rr.Code != http.StatusCreated {
		t.Fatalf("创建会议应返回 201，实际 %d body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	var created struct {
		ID           string `json:"id"`
		OwnerID      string `json:"owner_id"`
		WorkspaceID  string `json:"workspace_id"`
		Title        string `json:"title"`
		RecordingURL string `json:"recording_url"`
		CreatedAt    string `json:"created_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode 201 body: %v", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		t.Fatalf("201 响应里没有 id —— 客户端无从知道该用哪个 ID 调后续端点：%s", rr.Body.String())
	}
	// 用**响应里那个** ID 回查，作用域取响应里的 owner/workspace（不取 token 的），
	// 否则这条测的是「token 能不能查到」而不是「刚建的那行在不在」。
	got, err := srv.meetingStore.GetScoped(created.ID, created.OwnerID, created.WorkspaceID)
	if err != nil {
		t.Fatalf("201 返回的 id %q 在库里查不到：%v —— 服务端发出去的 ID 不是它自己承认的 ID",
			created.ID, err)
	}
	if got.Title != "悬界芯片客户评审会" {
		t.Fatalf("回查到的 title=%q，与发过去的标题不符", got.Title)
	}
	if created.CreatedAt == "" {
		t.Errorf("201 响应缺 created_at ⇒ 客户端排序/增量同步拿不到时间基准：%+v", created)
	}
	if created.RecordingURL != "" {
		t.Errorf("RecordingURL=%q 非空：用例夹具没发这个字段，服务端不该凭空造一个", created.RecordingURL)
	}
}

// ── 现状登记（**不是门**） ──────────────────────────────────────────

// TestMeetingSyncIdentityStatus 只做读数，不判定成败。
//
// ★ §105 已把 **ID 对账**落地（client id upsert）并升级为承重门
//
//	（meeting_sync_id_upsert_test.go）。本函数里与 ID / refine 状态码有关的
//	读数已删除 —— 留在一个「不判定成败」的函数里报一个已经修好的读数，
//	等于让现状登记变成过期文档。
//
// ★ 为什么那 9 个字段（location/participants/startedAt/durationMs/summary/
//
//	refinedTranscript/noteId/status）仍不写成承重门 —— 它是**产品取舍**：
//	要真入库得给 meetings 表加列并写迁移，而「补哪些列」本仓无权代拍。
//
//	拍板后请**删掉本函数**，把结论改写成承重断言。
func TestMeetingSyncIdentityStatus(t *testing.T) {
	srv, tokens, _ := syncIdentityServer(t)
	tok := tokens["ws-a"]

	rr := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings", syncBody)
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode 201 body: %v", err)
	}

	// 前端发的 10 个键，逐条报告「有没有真的入库」。
	// ⚠ 判据在**返回体**上做而不是猜 store 里有没有：CreateMeetingRequest
	//   少 tag 时 encoding/json 会静默丢弃，而 store 写进去的列由
	//   Meeting 结构体决定 —— 返回体正是「服务端承认的列」的权威来源。
	//
	// ⚠ 计数必须分**三类**而不是两类。第一版只分「认 / 丢」，于是日志打出
	//   「认 2、丢 7」—— 2+7=9，与 10 对不上。差额是 `status`：它在返回体里
	//   **存在**，但值是 store 覆写的常量，与请求里的值无关。
	//   把它算进「认」是撒谎（请求的 status 没被采纳），算进「丢」也是撒谎
	//   （它确实出现在响应里）。⇒ 三类：原样入库 / 被覆写 / 静默丢弃。
	var sent map[string]any
	if err := json.Unmarshal([]byte(syncBody), &sent); err != nil {
		t.Fatalf("decode 夹具 body: %v", err)
	}
	kept, overwritten, dropped := 0, 0, 0
	for _, k := range []string{"id", "title", "location", "participants", "startedAt",
		"durationMs", "summary", "refinedTranscript", "noteId", "status"} {
		v, present := created[k]
		// ⚠ §717：分类**必须按「读回的值是否等于请求发的值」**，不能按键名硬编码。
		//   旧版写死 `case present && k == "status"` ⇒ 一律判成「被 store 覆写」，
		//   但实测读回 value=refined **等于夹具请求的 "status":"refined"**
		//   ⇒ 请求值**已被采纳**（§708 的修复）⇒ 旧判法会**报出与代码相反的事实**。
		reqVal, reqSent := sent[k]
		switch {
		case present && reqSent && fmt.Sprintf("%v", v) != fmt.Sprintf("%v", reqVal):
			overwritten++
			t.Logf("   %-18s present=true  value=%v  ≠ 请求发的 %v ⇒ store 用自己的值写回，请求值没被采纳",
				k, v, reqVal)
		case present:
			kept++
			t.Logf("   %-18s present=true  value=%v%s", k, v,
				map[bool]string{true: "（= 请求发的值）", false: ""}[reqSent && fmt.Sprintf("%v", v) == fmt.Sprintf("%v", reqVal)])
		default:
			dropped++
			t.Logf("   %-18s present=false ← 被 encoding/json 静默丢弃（CreateMeetingRequest 没有这个 tag）", k)
		}
	}
	if kept+overwritten+dropped != len(sent) {
		t.Fatalf("分类计数 %d+%d+%d 与前端实发键数 %d 对不上 —— 读数口径本身坏了",
			kept, overwritten, dropped, len(sent))
	}
	t.Logf("① 前端发 %d 个键 ⇒ 原样入库 %d 个 + 被 store 覆写成常量 %d 个 + 静默丢弃 %d 个；"+
		"CreateMeetingRequest 的 tag 实为 %v",
		len(sent), kept, overwritten, dropped, jsonTags(t, meeting.CreateMeetingRequest{}))
}

// ── 负控：证明字段抠取器有牙 ────────────────────────────────────────

// TestMeetingSyncKeyExtractorHasTeeth 从**前端源码**现抠 syncMeeting() 实际
// stringify 的键，证明抠取器在违规样本上真的会咬住。
//
// ⚠ 与 §92 那道的负控同一条纪律，但这一条更关键：本包其余门用的是
//
//	**夹具里的固定 key 集**，唯独这里要现抠。若抠取器坏了（比如把
//	`payload.x` 这种点号取值当成键、或把简写键漏掉），抠出来会是个空集或
//	残缺集，而空集在「只做登记」的门里根本不会转红 —— 于是缺口继续隐形。
//
//	所以本条**必须**在真源码上先证明能抠出 10 个键，再在违规样本上证明会少。
func TestMeetingSyncKeyExtractorHasTeeth(t *testing.T) {
	src := frontendSource(t) // 已剥注释
	body := functionBody(t, src, "async syncMeeting(")
	at := strings.Index(body, "JSON.stringify(")
	if at < 0 {
		t.Fatal("syncMeeting() 里找不到 JSON.stringify —— 前端改了发法，本门的前提失效")
	}
	got := objectKeys(t, "syncMeeting() 请求体", matchedBraces(t, body, at+len("JSON.stringify(")))

	// 真源码必须抠满：CreateMeetingRequest 只有 1 个 tag，所以现状是 10 vs 1。
	if len(got) < 8 {
		t.Fatalf("从真源码只抠出 %d 个键（%v）—— 抠取器坏了，不是字段少了", len(got), got)
	}
	t.Logf("真源码抠出 %d 个键：%v", len(got), got)
	t.Logf("Go 侧 CreateMeetingRequest 的 tag 只有 %v ⇒ 差集 %v 是被静默丢弃的字段",
		jsonTags(t, meeting.CreateMeetingRequest{}), missingTags(t, got, meeting.CreateMeetingRequest{}))

	// 负控：少一个键 / 改一个名，必须与合规清单不同 —— 否则门无牙。
	want := jsonTags(t, meeting.CreateMeetingRequest{})
	for _, c := range []struct{ name, src string }{
		{"少一个键", `async syncMeeting(p){ http('/api/meetings',{method:'POST',body:JSON.stringify({ id: p.id, title: p.title, location: p.location, participants: p.participants, startedAt: p.startedAt, summary: p.summary, refinedTranscript: p.refinedTranscript, noteId: p.noteId, status: p.status })}) }`},
		{"键改名", `async syncMeeting(p){ http('/api/meetings',{method:'POST',body:JSON.stringify({ id: p.id, title: p.title, venue: p.location, participants: p.participants, startedAt: p.startedAt, durationMs: p.durationMs, summary: p.summary, refinedTranscript: p.refinedTranscript, noteId: p.noteId, status: p.status })}) }`},
	} {
		t.Run(c.name, func(t *testing.T) {
			// ⚠ pad 让锚点不落在第 0 个字符：解析器若把绝对偏移传进已切片字符串，
			//   在 pad=0 的样本上恰好也对（§92.3 踩过这个坑）。
			b := functionBody(t, "const pad = 'x'\n"+c.src, "async syncMeeting(")
			i := strings.Index(b, "JSON.stringify(")
			k := objectKeys(t, c.name, matchedBraces(t, b, i+len("JSON.stringify(")))
			if strings.Join(k, ",") == strings.Join(want, ",") {
				t.Fatalf("负控失效：违规样本抠出的 %v 与合规清单相同", k)
			}
			if strings.Join(k, ",") == strings.Join(got, ",") {
				t.Fatalf("负控失效：违规样本 %v 与真源码 %v 相同 ⇒ 抠取器没咬住", k, got)
			}
			t.Logf("违规样本抠出 %v（合规清单 %v / 真源码 %v）⇒ 判据会转红", k, want, got)
		})
	}
}

// missingTags 返回前端发了但 Go 结构体没有对应 tag 的键（保持前端顺序）。
//
// ⚠ 必须把 t 传进来：jsonTags 内部第一行就是 t.Helper()，传 nil 会在
//
//	*t 的互斥量上当场 panic，而不是给出可读的失败信息。
func missingTags(t *testing.T, sent []string, v any) []string {
	t.Helper()
	have := map[string]bool{}
	for _, tag := range jsonTags(t, v) {
		have[tag] = true
	}
	var out []string
	for _, k := range sent {
		if !have[k] {
			out = append(out, k)
		}
	}
	return out
}
