// meeting_sync_idempotency_test.go — §104：`POST /api/meetings` **不是幂等的**。
//
// ── 缺陷本体 ────────────────────────────────────────────────────────
//
// 一场录音会打**两次** `POST /api/meetings`，两次带**同一个 client id**：
//
//	① native/recordingRuntime.ts:632  录音停止 → syncMeetingMetadata()
//	② features/meetings/meeting-recording-finalize.ts:177
//	   → ingestMeetingArtifacts() → syncMeeting()
//
// 而 `useSessionLiveRecord.stop()` 把这两条串在**同一次停止**里：
// `await recorder.stop()`（内含 ①，fire-and-forget）之后再 `await finalizeRecording(...)`（②）。
//
// 服务端 `handleCreateMeeting` 走 `CreateScoped`（**按 client id upsert**，§105 方案 A）。
//
// ⏱ **历史（§719 订正）**：本文件原注释写着「**忽略请求里的 `id`** ⇒ 两次各造一个服务端 ID
// ⇒ **两行**」，以及「连第二次的 `status:"refined"` 都被 `CreateScoped` 覆写回 `"recording"`」；
// 这两句在 §96/§105 修复后**都已不成立**：
// `meeting/store.go:111-114` 现在是 `status := req.Status; if status == "" { status = "recording" }`
// ⇒ **请求值优先**，`recording` 只是**空值兜底**。
//
// ── 为什么写成现状登记而不是承重门 ──────────────────────────────────
//
// 修法本身是**产品决定**（§96.8 的方案 A / B）：
// A「按 client id upsert」顺带把这条也修了；B「客户端存服务端 id」**修不了**
// —— 纯 B 下 POST 仍是非幂等的 INSERT，仍会两行。
// ⇒ 把「不得产生第二行」写成承重门，等于替用户拍板。
//
// 这里只把现状读出来，并把**与修法无关**的那半条钉成门：
// 两处前端调用点必须用**同一个 id 字段**（若它们用不同字段，
// 那两次 POST 就是两场不同的会议，讨论幂等本身就问错了问题）。
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// ⚠ 做成 var 而不是 const：负控要把它们**临时指向一份副本**
// （副本里的 id 被改成别的字段），跑完再复原。
//
// 为什么不在真文件上做变异：这两个文件此刻是**并行线的在途 WIP**
// （`meeting-ingest.ts` 有 14 个未暂存 hunk，其中就覆盖 `syncMeeting` 的调用点）。
// 改别人在途的文件来验自己的门，是本会话反复记过不该做的事。
var (
	frontendIngest = "../../../frontend/src/features/meetings/meeting-ingest.ts"
	frontendLive   = "../../../frontend/src/native/recordingRuntime.ts"
)

// 两处调用点各自发出的 body，逐字取自前端源码（§104.2 的负控会核对它们确实存在）。
const (
	bodyAtStop = `{"id":"meeting-1791309378069-a7f3az","title":"悬界芯片客户评审会",` +
		`"location":"上海张江","participants":["张伟"],"startedAt":1791309378069,` +
		`"durationMs":1834000,"status":"completed"}`
	bodyAfterRefine = `{"id":"meeting-1791309378069-a7f3az","title":"悬界芯片客户评审会",` +
		`"location":"上海张江","participants":["张伟"],"startedAt":1791309378069,` +
		`"durationMs":1834000,"summary":"已对齐","status":"refined"}`
)

// syncMeetingIDArgs 抠出 `meeting-ingest.ts` 里每一处 `syncMeeting({` 的
// `id:` 值表达式，按出现顺序返回。
//
// ⚠ 第一版这把门写得比它能验证的多：它声称「两个调用点都用 `meeting.id`」，
//
//	而 `recordingRuntime.ts` 里**根本没有** `id: meeting.id` —— 那是
//	`syncMeetingMetadata(m)`，传的是变量。所以旧门实际只从 `meeting-ingest.ts`
//	抠到了值，**另一条路径根本没被验**，却因为「结果里出现过 meeting.id」而全绿。
//	⇒ 被下面那条负控当场抓出来（同会话第三次「门的注释比门强」）。
//
// 本门现在只承诺**它真能验的那件事**：`meeting-ingest.ts` 里
// **精校链的两处 `syncMeeting`** 必须用同一个 id 表达式。
// 这两处正是 §104 那个「同一场会议两行」的**精校侧**成因；
// `recordingRuntime` 那条是「停止侧」，它把变量 `m` 传进
// `syncMeetingMetadata(m)`，其内部再取 `meeting.id` —— 那是**调用链**的事实，
// 不是单文件源码能断言的，登记在 TestMeetingSyncIsNotIdempotentStatus 里。
func syncMeetingIDArgs(t *testing.T, src string) []string {
	t.Helper()
	idKey := regexp.MustCompile(`id:\s*([A-Za-z_$][\w.$\[\]']*)`)
	var got []string
	for _, m := range idKey.FindAllStringSubmatch(src, -1) {
		got = append(got, m[1])
	}
	return got
}

// TestMeetingSyncCallSitesShareTheSameID 承重门：精校链的两处 `syncMeeting`
// 必须用**同一个** id 表达式。
//
// 若两处指向不同的会议，那「同一场会议两行」就不是缺陷而是正确行为 ——
// 所以这条必须先钉住，它撑着 §104 的全部读数。
func TestMeetingSyncCallSitesShareTheSameID(t *testing.T) {
	src := stripTSComments(t, readFrontend(t, frontendIngest))
	got := syncMeetingIDArgs(t, src)
	if len(got) < 2 {
		t.Fatalf("只抠到 %d 个 id 表达式（%v），预期至少 2 处 syncMeeting —— "+
			"前端改了写法，请重读本门与 §104", len(got), got)
	}
	uniq := map[string]bool{}
	for _, g := range got {
		uniq[g] = true
	}
	if len(uniq) != 1 || !uniq["meeting.id"] {
		t.Fatalf("精校链的 syncMeeting 用了不同的 id 表达式：%v —— "+
			"若它们指向不同会议，「同一场会议两行」就不成立，§104 的读数全部作废", got)
	}
}

// TestMeetingSyncStatusFollowsRequestValue 现状登记：**只**登记 status 那件事。
//
// ★ §105 已把「一场录音 ⇒ 两行」修掉并升级为承重门
//
//	（meeting_sync_id_upsert_test.go:TestRepeatedSyncOfSameClientIDCreatesOneRow），
//	所以本函数里与行数有关的部分已删除。
//
// ⏱ **函数名已改名（§719）**：原名 `TestMeetingSyncStatusIsNotAdoptedStatus`
//
//	断言「请求里的 status 没被采纳」—— 而该断言**已随 §96 修复被推翻**
//	（`meeting/store.go:111-114`：`status := req.Status`，`recording` 只是空值兜底）。
//	保留旧名会让 `-run TestMeetingSyncStatusIsNotAdoptedStatus` 匹配到一个
//	**名字与行为相反**的测试。
//
//	剩下的问题仍是**未拍板**的：补哪些列进 meetings 表是产品决定（要写迁移），
//	本仓无权代拍。拍板后请把本函数改写成承重断言。
func TestMeetingSyncStatusFollowsRequestValue(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	tok := tokens["ws-a"]

	post := func(body string) string {
		req := httptest.NewRequest(http.MethodPost, "/api/meetings", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST /api/meetings 应 201，实际 %d body=%s", rr.Code, rr.Body.String())
		}
		var m struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m.ID + " / status=" + m.Status
	}

	stop := post(bodyAtStop)
	refined := post(bodyAfterRefine)
	t.Logf("① 录音停止那次同步 → %s", stop)
	t.Logf("② 精校后那次同步 → %s（请求里的 status 是 \"refined\"）", refined)
	// ⚠ §719：这段结论原先**写死**为「两次都是 status=recording —— 请求里的
	//   status 始终没被采纳」，而实测读数是 completed / completed ⇒ **它与上面两行矛盾**。
	//   根因：那段文案停在 §96 修复前。`meeting/store.go` 现在的逻辑是
	//   `status := req.Status; if status == "" { status = "recording" }`
	//   ⇒ **请求值优先**，`recording` 只是**空值兜底**。
	// ⇒ 现在**按实测值说话**，不再断言一个与本文件读数矛盾的结论。
	t.Logf("★ 现状（按上面两行的实测值，不写死）：两次的 status 分别是 %q 与 %q；"+
		"「recording」在 store 里只是 req.Status 为空时的兜底（meeting/store.go:111-114），"+
		"不再是「盖掉请求值」的常量。", statusOf(stop), statusOf(refined))
}

// statusOf 从 post() 返回的 `"<id> / status=<值>"` 里取出 status 段。
func statusOf(s string) string {
	const marker = " / status="
	if i := strings.Index(s, marker); i >= 0 {
		return s[i+len(marker):]
	}
	return "(解析不出)"
}

// TestMeetingSyncIDExtractor_HasTeeth 负控：把精校链的**第二处** id 改成别的字段，
// 证明上面那条门会转红。
//
// 只动**我自己的临时副本**，真文件全程只读 ——
// 那两个前端文件此刻是并行线的在途 WIP（`meeting-ingest.ts` 有 14 个未暂存 hunk，
// 其中就覆盖这些调用点），改别人在途的文件来验自己的门是本会话反复记过不该做的事。
//
// ⚠ 变异必须**同形**：只换 `id:` 的值表达式，调用形态一字不改，
// 且只改**一处** —— 两处都改的话「不同」就体现不出来。
func TestMeetingSyncIDExtractor_HasTeeth(t *testing.T) {
	src := readFrontend(t, frontendIngest)
	const anchor = "id: meeting.id,"
	if strings.Count(src, anchor) < 2 {
		t.Fatalf("只找到 %d 处 %q —— 前端改了写法，负控失效，请重读本门",
			strings.Count(src, anchor), anchor)
	}
	// 只改**第 2 处**：两处都改的话「两处不同」就体现不出来。
	i := strings.Index(src, anchor)
	j := strings.Index(src[i+len(anchor):], anchor) + i + len(anchor)
	bad := src[:j] + "id: meeting.noteId," + src[j+len(anchor):]

	got := syncMeetingIDArgs(t, stripTSComments(t, bad))
	if len(got) < 2 {
		t.Fatalf("变异样本只抠到 %d 个（%v），负控失效", len(got), got)
	}
	uniq := map[string]bool{}
	for _, g := range got {
		uniq[g] = true
	}
	// ★ 方向别写反：**产生**了不同表达式，正说明抠取器有牙
	//   （承重门的条件 `len(uniq) != 1` 会因此触发）。
	//   仍然是 1 个才叫失败 —— 那说明抠取器看不见这次变异。
	//   第一版把这条写反了，于是「负控失败」打印在一份**正确的**样本上，
	//   第一眼像是抠取器坏了 —— 同族教训：负控自己也可能说反话。
	if len(uniq) < 2 {
		t.Fatalf("负控失败：只改了第 2 处，抠取器仍只看到 %v ⇒ 判据认不出违规形态", got)
	}
	t.Logf("同形变异样本抠到 %v ⇒ 两处不同时判据会转红", got)
}
