// live_note_search_scope_probe_test.go — 复现「会议智能体的 search_notes 查不到任何笔记」。
//
// ⚠⚠ 这是一个**已知缺陷的复现探针**，不是「证明它对」的判据。
// 它默认 skip；带上 POCKET_PROBE_SCOPE=1 跑会**故意变红**，红的那条就是缺陷本身。
//
// 缺陷
// ----
// `meetingagent.NoteSearcher.Execute`（backend/internal/meetingagent/tools.go:88）
// 写死 `n.Src.ListScoped(ctx, "", "", "")` —— **userID 与 workspaceID 都是空串**。
// 而 `notes.Store.ListScoped`（backend/internal/notes/store.go:180-201）是
//
//	FROM notes WHERE user_id = $1 AND workspace_id = $2 AND deleted_at IS NULL
//
// 只把 **workspaceID == ""** 回落成 "default"，**userID 没有回落**。
// 笔记行的 user_id 由鉴权写死：`server_assistant.go:356` 是 `n.UserID = uid`，
// `server_meeting_ingest.go:223` 是 `UserID: uid` —— 生产里**永远非空**。
//
// ⇒ 查询恒为 `user_id = '' AND workspace_id = 'default'` ⇒ **零行**
// ⇒ search_notes 恒回「没有找到与「x」相关的笔记。」
// ⇒ 用户需求里的「给出一些参考的资料」在生产中**永远检索不到任何东西**，
//
//	既不会编造（这是好消息），也永远拿不到真笔记（这是坏消息）。
//
// 为什么是结构性的而不是漏传一个参数
// ---------------------------------
// `NoteSearcher` 结构体（tools.go:33-37）只有 `Src` 与 `Now` 两个字段，
// **没有任何地方能装 userID/workspaceID**；
// `server_meeting.go:413-422` 明明已经取到了
// `s.userIDFromRequest(r)` / `s.workspaceIDFromRequest(r)`，
// 但只传给了 `Runner` 自己，没有传给工具。
//
// ⚠ 与本会话 §328 的关系（本探针推翻了一部分自己的读数）
// --------------------------------------------------------
// §328 的 A 臂（注入 3 条笔记）测出「5/6 引用了真 note_id」，
// **那个 A 臂在生产里不可达**：本探针用的 `probeNotes.ListScoped`
// （live_gateway_probe_test.go:38）**忽略全部三个 scope 参数**直接返回夹具 ⇒
// 夹具把被测行为屏蔽掉了。同理 §328 的 C/E 臂（工具回「没有找到」）
// 并不是「用户没有相关笔记」，而是**「查询作用域恒空」**。
// ⇒ §328.7 那句「守住它的是工具的诚实回答」在**机制层面**仍成立
//
//	（工具确实给了模型一个合法的「没找到」出口），
//	但它描述的那个状态**生产根本到不了**。
//
// 用法：
//
//	POCKET_PROBE_SCOPE=1 go test ./internal/server/ -run TestNoteSearcherScopeProbe -v
package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// scopeRecordingNotes 记录 ListScoped 收到的三个作用域参数。
//
// ⚠ 它**故意**不做任何过滤：只记录，不拦截。
// 目的是把「工具到底传了什么」变成一个可判定的读数，
// 而不是再让一个忽略作用域的夹具把缺陷盖住（这正是 §328 A 臂犯的错）。
type scopeRecordingNotes struct {
	calls []string
}

func (s *scopeRecordingNotes) ListScoped(_ context.Context, userID, workspaceID, domain string) ([]notes.Note, error) {
	s.calls = append(s.calls, "userID="+userID+" workspaceID="+workspaceID+" domain="+domain)
	return nil, nil
}

func TestNoteSearcherScopeProbe(t *testing.T) {
	if os.Getenv("POCKET_PROBE_SCOPE") != "1" {
		t.Skip("作用域探针：带 POCKET_PROBE_SCOPE=1 跑。" +
			"**修之前它红（缺陷复现），修之后它绿（判据）** —— 两种状态都刻意保留。")
	}

	src := &scopeRecordingNotes{}
	tool := &meetingagent.NoteSearcher{
		Src:         src,
		UserID:      "u-caller",
		WorkspaceID: "ws-caller",
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"北极星"}`))
	if err != nil {
		t.Fatalf("Execute 报错: %v", err)
	}

	if len(src.calls) != 1 {
		t.Fatalf("期望 ListScoped 被调 1 次，实得 %d 次：%v", len(src.calls), src.calls)
	}
	got := src.calls[0]
	t.Logf("★ ListScoped 实际收到：%s", got)
	t.Logf("★ 工具返回：%s", out)

	// ★ 判据本体：作用域**必须**被传下去。
	// 修之前这三行全红（就是 §330 的缺陷本体）。
	if got == "userID= workspaceID= domain=" {
		t.Errorf("★ search_notes 仍以空作用域查询：生产里 notes 表 user_id 恒非空 ⇒ 检索恒零结果。%s", got)
	}
	if !strings.Contains(got, "userID=u-caller") {
		t.Errorf("★ userID 没有传到数据源：%s", got)
	}
	// ⚠ 漏 workspaceID 比漏 userID 更危险：那是**跨工作区泄漏**。
	if !strings.Contains(got, "workspaceID=ws-caller") {
		t.Errorf("★ workspaceID 没有传到数据源（漏它 = 同一用户跨工作区互相看到笔记）：%s", got)
	}
}

// scopeEnforcingNotes 是**会按作用域过滤**的替身：
// 作用域不匹配就返回空，匹配才给笔记。
//
// ⚠ 为什么必须有它（§330.3 的同一个教训，反过来用）：
// `scopeRecordingNotes` 只记录不拦截，它能证明「传了什么」，
// 但证明不了「传对了以后真的检索得到」。
// 而本会话 §328 的 A 臂之所以是假读数，正是因为夹具**忽略**了作用域 ——
// 一个忽略条件的替身会让「修好」与「没修」输出完全相同。
type scopeEnforcingNotes struct {
	items []notes.Note
	last  string
}

func (s *scopeEnforcingNotes) ListScoped(_ context.Context, userID, workspaceID, domain string) ([]notes.Note, error) {
	s.last = "userID=" + userID + " workspaceID=" + workspaceID
	if userID != "u-caller" || workspaceID != "ws-caller" {
		return nil, nil // 作用域不符 ⇒ 零行（与真实 SQL 同形）
	}
	return s.items, nil
}

// TestNoteSearcherScopeEnforcedRetrieval 证明「作用域对了就真的检索得到」。
//
// 它是 §330.5 修法的**阳性对照**：没有它，「作用域传下去了」这条判据
// 可能因为下游根本不读而照样绿。
func TestNoteSearcherScopeEnforcedRetrieval(t *testing.T) {
	src := &scopeEnforcingNotes{items: []notes.Note{
		{ID: "nt_scope1", Title: "北极星项目续期讨论", Content: "北极星项目的续期合同要在 11 月底前签完，负责人是林岚。"},
	}}
	tool := &meetingagent.NoteSearcher{Src: src, UserID: "u-caller", WorkspaceID: "ws-caller"}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"北极星"}`))
	if err != nil {
		t.Fatalf("Execute 报错: %v", err)
	}
	t.Logf("作用域：%s", src.last)
	t.Logf("工具返回：%s", out)

	// 阳性：作用域正确 ⇒ 必须检索得到。
	if strings.Contains(out, "没有找到") {
		t.Fatalf("作用域正确却仍检索不到 ⇒ 修法没修到根上：%s", out)
	}
	if !strings.Contains(out, "nt_scope1") || !strings.Contains(out, "北极星") {
		t.Fatalf("检索结果里没有那条笔记：%s", out)
	}

	// 阴性对照：**空作用域**必须检索不到 —— 这正是 §330 的缺陷形态，
	// 也是证明「上面那条绿不是因为工具不看作用域」的唯一办法。
	bad := &meetingagent.NoteSearcher{Src: &scopeEnforcingNotes{items: src.items}}
	outBad, err := bad.Execute(context.Background(), json.RawMessage(`{"query":"北极星"}`))
	if err != nil {
		t.Fatalf("Execute 报错: %v", err)
	}
	if !strings.Contains(outBad, "没有找到") {
		t.Fatalf("阴性对照失效：空作用域竟然检索到了东西 ⇒ 这条判据恒真，不构成证据。%s", outBad)
	}
}
