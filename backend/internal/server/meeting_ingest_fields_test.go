// ingest 端点（POST /api/meetings）的跨语言字段契约门。
//
// 为什么要有这道门（docs/design §110.1）
//
//	摘要端点（summarize）与精校端点（refine）都有承重门
//	（summary_request_wire_test.go：TestMeetingRequestTopLevelFieldsMatchTheFrontend，
//	逐条对账前端 JSON.stringify 的键与 Go 的 json tag，任一侧改名/增删即红），
//	**唯独 ingest 端点没有**。
//	结果是：前端 meetings.ts:178 发 10 个字段，Go 的 CreateMeetingRequest
//	只有 `id` / `title` 两个 tag，其余 7 个被 encoding/json **静默丢弃**
//	（实测见 meeting_sync_identity_test.go 的 TestMeetingSyncIdentityStatus：
//	location/participants/startedAt/durationMs/summary/refinedTranscript/noteId
//	全部 present=false）。
//
// ⚠ 这道门**不主张「这 7 个字段该落云端」** ——
//
//	那已经被查清是产品取舍：
//	  ① frontend/src/features/meetings/meetings-store.ts 的 updateMeeting()
//	     已经把这 7 个字段落进**本地** local_meetings（snake_case 那半张 map：
//	     refined_transcript / note_id / duration_ms / summary / …）
//	  ⇒ 服务端丢弃它们不是「数据丢了」，是「云端这份本来就不全」。
//	  ② server_meeting_ingest.go:106-107 的注释自己写明了这一点：
//	     「这不是『覆盖写』，是**同步同一份事实**……这里只改 title，
//	       不碰任何另一路才写的字段」。
//
//	所以「该落哪些字段」必须由用户拍板（§95.9 的待拍板项），本门**不代拍**。
//
// 这道门守的是另一件**不需要任何产品决定**的事：
//
//	**漂移** —— 前端加/改名一个字段、Go 那边没同步，而**没人注意到差集变了**。
//	现状差集被钉成一张**具名清单**（knownDroppedTags）。
//	差集一旦与清单不符：
//	  - 新增了不在清单里的键 ⇒ 要么该补 Go tag，要么该把它登记进清单。**必须有人决定**。
//	  - 清单里的键消失了 ⇒ 有人已经把它补到 Go 上了 ⇒ 本门报红，提示**可以收窄清单**。
//
// 为什么不能写成 requireSameKeys：
//
//	那等于现在就宣布「这 7 个字段不该发」，那正是那条未决决定。
//	本门只保证「差集没有悄悄变化」。
package server

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// knownDroppedTags 是**已查清、并登记过**的「前端发了但 Go 没有 json tag」的键。
//
// ⚠ 这是**现状快照**，不是「正确清单」——改成它就等于替用户做了那条产品决定。
//
// 来源：meeting_sync_identity_test.go 的 TestMeetingSyncIdentityStatus 实测读数
//
//	（present=false 的 7 个）+ jsonTags(CreateMeetingRequest{}) 只有 [id title]。
//
// ⚠⚠ `status` **不在这里**，它属第三类（meeting_sync_identity_test.go 的注释已指出）：
//
//	前端发了 `status`，Go 的 CreateMeetingRequest 没有这个 tag，
//	但 store 会把它**覆写成常量**再写进响应 ⇒ 它在返回体里 present=true。
//	所以按「json tag 差集」算它是丢弃，按「返回体」算它是入库 —— 两个口径的差集不同。
//	列进 knownDroppedTags 会与实测读数矛盾（实测说 present=true），
//	所以单列成 knownOverwrittenFields。
var knownDroppedTags = []string{
	"location",
	"participants",
	"startedAt",
	"durationMs",
	"summary",
	"refinedTranscript",
	"noteId",
}

// knownOverwrittenFields 是「前端发了、Go 没 json tag、但 store 用**自己算的值**写回」，
// 也就是请求里的值**根本没被采纳**的那一类。
// 实测（TestMeetingSyncIdentityStatus）：`status` 被覆写成常量。
var knownOverwrittenFields = []string{"status"}

// TestMeetingIngestRequestFieldsDrift 把 ingest 端点的「前端发的键」与
// 「Go 的 json tag」逐条对账，但**只对账差集有没有变**，不断言差集该是空。
//
// 负控见 TestMeetingIngestDriftGuardHasTeeth。
func TestMeetingIngestRequestFieldsDrift(t *testing.T) {
	src := frontendSource(t)

	// syncMeeting() 的请求体：body: JSON.stringify({ … })
	body := functionBody(t, src, "async syncMeeting(")
	at := strings.Index(body, "JSON.stringify(")
	if at < 0 {
		t.Fatal("syncMeeting() 里找不到 JSON.stringify —— 前端改了发法，本门的前提失效")
	}
	sent := objectKeys(t, "syncMeeting() 请求体", matchedBraces(t, body, at+len("JSON.stringify(")))

	// 真源码必须抠够：现状是 10 个键。若这里只有 2-3 个，
	// 说明抠取器坏了（或前端真的把请求瘦身了）——必须先分清，别直接报字段对不上。
	if len(sent) < 8 {
		t.Fatalf("从真源码只抠出 %d 个键（%v）—— 抠取器坏了，或前端真的改了发法；先查清楚再判契约", len(sent), sent)
	}

	// tag 差集里混着**两类**字段，必须先分开，否则清单会与实测读数打架：
	//   ① knownDroppedTags      —— Go 没 tag、store 也没写 ⇒ 真丢弃
	//   ② knownOverwrittenFields —— Go 没 tag、但 store 用自己的值写回 ⇒ 请求值没被采纳
	// 第一版把 status 混进 knownDroppedTags，门立刻报红 —— 那是**清单错了**，不是仓库坏了。
	all := missingTags(t, sent, meeting.CreateMeetingRequest{})
	overwrittenSeen := map[string]bool{}
	for _, k := range knownOverwrittenFields {
		if hasKey(all, k) {
			overwrittenSeen[k] = true
		}
	}
	var got []string
	for _, k := range all {
		if !overwrittenSeen[k] {
			got = append(got, k)
		}
	}

	// 两类都要与已知清单一致，否则报红。
	for _, k := range knownOverwrittenFields {
		if !overwrittenSeen[k] {
			t.Errorf("knownOverwrittenFields 里的 %q 不在 tag 差集里了 —— 它已经被补到 Go 侧（json tag 有了）。\n"+
				"  **可以把它从 knownOverwrittenFields 移除**；若这不是有意为之，请查是谁加的 tag。", k)
		}
	}
	if len(got) == 0 && len(knownDroppedTags) == 0 {
		t.Log("差集为空：前端发的字段全部有 json tag 了")
		return
	}
	if strings.Join(got, ",") == strings.Join(knownDroppedTags, ",") {
		t.Logf("丢弃类差集未变（%d 个）：%v", len(got), got)
		t.Logf("   被覆写类（请求值未采纳）：%v", knownOverwrittenFields)
		t.Logf("   这些字段前端在发、Go 侧不接。**本地库 updateMeeting() 已落**，")
		t.Logf("   所以不是「数据丢了」，而是「云端这份本来就不全」——属未决的产品取舍（§95.9）。")
		return
	}

	added, removed := diffStrings(knownDroppedTags, got)
	for _, k := range added {
		t.Errorf("丢弃类差集里多出 %q —— 前端新发了一个 Go 没有 tag、store 也不写的字段，它正在被静默丢弃。\n"+
			"  处置二选一（**都需要有人决定，本门不代拍**）：\n"+
			"    ① 给 CreateMeetingRequest 补 json tag，让它真的落库；\n"+
			"    ② 确认云端不需要它，把 %q 登记进 knownDroppedTags 并写明理由。\n"+
			"  ⚠ 放着不管的话，它会像现在的 7 个一样一直悄悄丢掉。", k, k)
	}
	for _, k := range removed {
		t.Errorf("丢弃类差集里的 %q 不见了 —— 它已经被补到 Go 侧（json tag 有了），"+
			"**可以收窄 knownDroppedTags 了**。若这不是有意为之，请查是谁加的 tag。", k)
	}
	if len(added) == 0 && len(removed) == 0 {
		t.Errorf("键集合相同但顺序不同 ⇒ 下游有依赖字段顺序的地方，或抠取器不稳定。\n"+
			"  已知清单: %v\n  真源码:   %v", knownDroppedTags, got)
	}
}

// TestMeetingIngestDriftGuardHasTeeth 证明上一条**不是恒真**。
//
// 上一条只在差集**变化**时转红；如果 diffStrings 或者比较逻辑恒返回「相同」，
// 它会永远绿。所以必须有三臂：
//
//	① 多一个键   ⇒ 必须报红
//	② 少一个键   ⇒ 必须报红
//	③ 完全一致   ⇒ 必须放行（负控：门不是「见红就红」）
func TestMeetingIngestDriftGuardHasTeeth(t *testing.T) {
	base := append([]string(nil), knownDroppedTags...)

	for _, c := range []struct {
		name        string
		mutate      func([]string) []string
		wantAdded   int
		wantRemoved int
	}{
		{"多一个键", func(s []string) []string { return append(s, "__probeNewField") }, 1, 0},
		{"少一个键", func(s []string) []string { return s[:len(s)-1] }, 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := c.mutate(base)
			added, removed := diffStrings(base, got)
			if len(added) != c.wantAdded {
				t.Fatalf("added 应为 %d 个，实得 %d（%v）—— diffStrings 无牙", c.wantAdded, len(added), added)
			}
			if len(removed) != c.wantRemoved {
				t.Fatalf("removed 应为 %d 个，实得 %d（%v）—— diffStrings 无牙", c.wantRemoved, len(removed), removed)
			}
			if strings.Join(base, ",") == strings.Join(got, ",") {
				t.Fatalf("负控失效：变异后与原清单相同 ⇒ 判据不会转红")
			}
			t.Logf("%s ⇒ 多 %d / 少 %d ⇒ 上一条会报红（已验证 diff 有牙）", c.name, len(added), len(removed))
		})
	}

	t.Run("完全一致必须放行", func(t *testing.T) {
		added, removed := diffStrings(base, append([]string(nil), base...))
		if len(added) != 0 || len(removed) != 0 {
			t.Fatalf("完全一致时不应有差异，实得 added=%v removed=%v", added, removed)
		}
		t.Log("一致 ⇒ 放行 ⇒ 门不是「见红就红」")
	})
}

// diffStrings 返回 want 相对 got 的增删（保持 got 的顺序）。
func diffStrings(want, got []string) (added, removed []string) {
	have := map[string]bool{}
	for _, s := range got {
		have[s] = true
	}
	inWant := map[string]bool{}
	for _, s := range want {
		inWant[s] = true
		if !have[s] {
			removed = append(removed, s)
		}
	}
	for _, s := range got {
		if !inWant[s] {
			added = append(added, s)
		}
	}
	return added, removed
}
