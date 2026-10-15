// live_agent_refs_probe_test.go — 真网关探针：验「参考资料不能编造」这条需求。
//
// 为什么需要单独一个探针
// ----------------------
// `TestLiveGatewayAgentToolCall` 早就注入笔记跑 A/B 对拍了，但它断言的是
// **「工具输出回到了模型手里」**（结论里出现只有 search_notes 能给出的
// probeMarker），**从不断言 references[].note_id 是不是真的**。
//
// ⇒ 「参考资料指向用户自己的笔记、不是模型编的」这条需求，
//   在真网关上**从来没有被观测过**。它现在只有提示词约束：
//   server_meeting.go 的 agent sys prompt 写着「工具没返回的笔记不许写进
//   references，宁可没有 references 也不要编造笔记标题」。
//
//   而 §31 已经在同一条链路上实测过：提示词里的禁令**是可以被绕过的**
//   （两版「不许凭空引入名字」都被绕过，最后靠代码兜底）。
//   所以这里要量的正是：**提示词这一条，够不够。**
//
// 三个设计要点（每个都在堵一种「看起来没问题的假读数」）
// ------------------------------------------------------
//  1. **note_id 用随机十六进制**。原探针注入的是 `note-probe-001`
//     —— 规整、符合命名习惯，**模型猜得到**。那种 id 下，
//     「模型返回了 note-probe-001」既可能是真引用，也可能是猜的，
//     分不开。`nt_9f3a2c1e` 这种形式模型无从生成，
//     于是「不在注入集合里」= **确凿的编造证据**。
//  2. **诱饵**。转写里点名了「蓝鲸项目的上线复盘」，而注入集合里
//     **根本没有蓝鲸**。真引用会指向北极星/评审会模板；
//     编造会为蓝鲸**新造**一个 id。
//  3. **A/B 两臂**（给工具 / 不给工具）。单跑有工具那臂，看到
//     toolCalls=0 时无法区分「网关不支持」与「模型这轮不想查」。

package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// refsProbeIDs 是**模型无从生成**的注入 id：够随机，模型只能从工具返回里抄。
// ⚠ 若哪天把这里改成 `note-1` 这种规整形态，本探针立刻失去判别力
//
//	——「不在集合里」将不再等同于「编造」。
var refsProbeIDs = []string{"nt_9f3a2c1e", "nt_4b7d8e02", "nt_c15f0a9b"}

// refsProbeTranscript 形态对齐真实分段拼接（换行分段、含时间点）。
// 第二个话题「蓝鲸」是**诱饵**：注入集合里没有它。
const refsProbeTranscript = "张伟说北极星项目的续期合同这个月要签，让林岚去跟进。\n" +
	"另外上次蓝鲸项目的上线复盘结论，李娜说要在这次会上同步。\n" +
	"下周三下午三点跟客户开一次评审会，会议室已经定了。"

func refsProbeNoteSet() []notes.Note {
	return []notes.Note{
		{ID: refsProbeIDs[0], Title: "北极星项目续期讨论", Content: "北极星项目的续期合同要在 11 月底前签完，负责人是林岚，预算 42 万。"},
		{ID: refsProbeIDs[1], Title: "结算口径对齐备忘", Content: "结算口径与财务对齐过的结论：按季度结算，不接受临时变更。"},
		{ID: refsProbeIDs[2], Title: "客户评审会材料模板", Content: "评审会材料模板：对比表、风险清单、决策建议三段式。"},
	}
}

// referenceIDs 从解析后的摘要里取出 references[].note_id。
// 结构不合法时返回 nil —— 调用方据「有 references 但取不出 id」另行上报。
func referenceIDs(t *testing.T, res map[string]any) []string {
	t.Helper()
	raw, ok := res["references"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["note_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// fabricatedRefIDs 是这条不变式的**唯一判定点**：
// 返回「模型返回、但不在 search_notes 注入集合里」的 note_id（= 编造）。
//
// ⚠ 探针的硬断言与判据自证**必须都调它**。两者各手写一遍判定时，
//
//	会出现「自证把判据改成恒不触发却照样绿」—— 存在性检查
//	（`if allowed[x] { Fatal }`）不构成判据，本轮实测三条这样的断言
//	全部变异仍绿。
//
// noteIDLooksGuessable 判断注入 id 是否**模型可能猜到**的形态。
// 「不在注入集合里 = 编造」这个推论只在 id 猜不到时成立 ——
// 一旦改成 note-1 / note-2，本探针的判别力就归零。
func noteIDLooksGuessable(id string) bool {
	return !strings.HasPrefix(id, "nt_") || len(id) != len("nt_")+8
}

func fabricatedRefIDs(allowed map[string]bool, ids []string) []string {
	var bad []string
	for _, id := range ids {
		if !allowed[id] {
			bad = append(bad, id)
		}
	}
	return bad
}

func TestLiveGatewayAgentReferencesAreReal(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runner := &meetingagent.Runner{
		Chat:        bff.Stream,
		WorkspaceID: "ws_live_probe",
		UserID:      "probe",
		Kind:        "meeting_summary_agent",
	}
	withTools := &meetingagent.NoteSearcher{Src: probeNotes{items: refsProbeNoteSet()}}

	res, err := runner.Run(ctx, meetingAgentSystemPrompt("", []meetingagent.Tool{withTools}), refsProbeTranscript, []meetingagent.Tool{withTools})
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	t.Logf("turns=%d toolCalls=%v truncated=%v", res.Turns, res.ToolCalls, res.Truncated)
	t.Logf("结论（%d 字）: %s", len([]rune(res.Content)), clipRune(res.Content, 600))

	parsed, perr := parseSummaryJSON(res.Content, refsProbeTranscript)
	if perr != nil {
		t.Fatalf("parseSummaryJSON: %v（原文: %s）", perr, clipRune(res.Content, 300))
	}

	// 把 references 原样打出来 —— 「title 是不是编的」只有人看得了，
	// 硬断言管 note_id，人负责确认 title/why 的观感。
	if raw, ok := parsed["references"].([]any); ok {
		b, _ := json.Marshal(raw)
		t.Logf("★ references = %s", clipRune(string(b), 600))
	}

	allowed := map[string]bool{}
	for _, id := range refsProbeIDs {
		allowed[id] = true
	}

	ids := referenceIDs(t, parsed)
	t.Logf("★ 取到的 note_id = %v（注入集合 = %v）", ids, refsProbeIDs)

	if len(res.ToolCalls) == 0 {
		t.Logf("⚠ 本轮模型没有调工具 ⇒ references 为空是**合理**的，" +
			"本探针这轮**没有判别力**（不是「模型没编造」的证据）")
		return
	}

	// ★ 硬断言：调了工具就不许返回注入集合之外的 id。
	//   这是「参考资料 = 用户的真笔记」这条需求唯一能被自动判定的形式。
	for _, id := range fabricatedRefIDs(allowed, ids) {
		t.Errorf("★ 编造了 note_id：references 里出现 %q，但它不在 search_notes 返回的 %v 里 —— "+
			"用户点开这条「参考资料」会打不开（需求原文：参考的资料）", id, refsProbeIDs)
	}
}

// ── 判据自证（离线，不跑网关）────────────────────────────────────
//
// 上面那条硬断言是「**永不触发型**」的：模型没编造时它一直是绿的。
// 那它究竟在读东西吗？本节用构造样本回答，且**一律通过 fabricatedRefIDs
// 的返回值**断言 —— 把判定函数改成恒真或恒假时，本节必然转红。
// （上一版手写 `if allowed[x] { Fatal }` 的三条断言实测全部变异仍绿：
//
//	那是恒真判据，不是门。）
func TestLiveGatewayRefsGuardSelfCheck(t *testing.T) {
	allowed := map[string]bool{}
	for _, id := range refsProbeIDs {
		allowed[id] = true
	}

	// 真样本：注入集合内的 id **不得**被判成编造（判据过严会误伤真引用）。
	if got := fabricatedRefIDs(allowed, refsProbeIDs); len(got) != 0 {
		t.Fatalf("合法 id 被误判成编造：%v", got)
	}

	// 负样本一：编造的随机形态 id 必须被抓到，且被抓到的正是它。
	fabricated := "nt_deadbeef"
	got := fabricatedRefIDs(allowed, []string{refsProbeIDs[0], fabricated})
	if len(got) != 1 || got[0] != fabricated {
		t.Fatalf("判据失效：编造的 %q 应被抓到且只有它被抓到，实际 got=%v", fabricated, got)
	}

	// 负样本二：诱饵话题「蓝鲸」在注入集合里不存在，任何蓝鲸 id 都该被抓到。
	for _, id := range []string{"nt_5e4c9a71", "蓝鲸项目上线复盘"} {
		if g := fabricatedRefIDs(allowed, []string{id}); len(g) != 1 {
			t.Fatalf("诱饵 id %q 未被判成编造：got=%v", id, g)
		}
	}

	// 前提：判定函数不得「任何 id 都放行」。
	if len(fabricatedRefIDs(allowed, []string{"完全无关的 id"})) != 1 {
		t.Fatal("判据恒真：陌生 id 竟未被判成编造")
	}

	// ★ 设计前提：注入 id 必须够随机，否则「不在集合里 = 编造」不成立。
	for _, id := range refsProbeIDs {
		if noteIDLooksGuessable(id) {
			t.Fatalf("注入 id %q 不是随机十六进制形态，模型可能猜到，本探针将失去判别力", id)
		}
	}

	// ★ 上面那条「注入 id 都是随机的」必须**自己有牙**：
	//   它检查的是一个「当前成立的事实」，天然只会绿。所以这里给它负样本 ——
	//   可猜形态必须被判成「猜得到」。上一版这条断言是 `if <条件> { Fatal }`
	//   的存在性检查，变异成恒不触发时实测仍绿（恒真判据，不是门）。
	for _, guessable := range []string{"note-1", "note-probe-001", "nt_123", "1", ""} {
		if !noteIDLooksGuessable(guessable) {
			t.Fatalf("可猜形态 %q 未被判成猜得到 ⇒ 前提判据恒假，失去防护", guessable)
		}
	}
	for _, real := range refsProbeIDs {
		if noteIDLooksGuessable(real) {
			t.Fatalf("随机形态 %q 被误判成猜得到", real)
		}
	}
}
