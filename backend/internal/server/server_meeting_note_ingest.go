package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notes"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// ⚠ §939：服务端「会议 → 笔记」这条链的**落点口径**。
//
// 为什么会单独一个文件（口径只有一份，见 §938 的同款形状）：
//
//	服务端有**两条**建笔记的路径：
//	  - 会议精校链：handleMeetingRefine → finalizeMeetingRefine → ingestMeetingNote
//	  - 随手记链：  POST /api/notes → handleNotes → notesStore.Upsert
//
// 而 §938 已经把会议待办的口径抽成了共用的一份（server_meeting_todo_ingest.go），
// 理由是「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」（§80 记过的形状）。
// 笔记这一侧当时还**没有**共用 ⇒ 于是「随手记那条写 workspace、会议那条不写」
// 这件事真的发生了（§939 的 D1）⇒ ⇒ ⇒ 口径必须在这里抽成一份。
//
// ★ 本文件抽的三件事（缺一件，缺陷就会以另一种形态回来）：
//
//  1. **落点**：租户（workspace）+ 归属（owner）—— 来自 JWT，不是来自 body；
//  2. **id**：同一场会议的笔记必须是**同一行**（派生 id ⇒ 重复精校幂等）；
//  3. **广播目标**：只发给属主 ⇒ 绝不 `wsHub.Broadcast`（全体广播）。
//
// ⚠ 三者缺一不可，而且**有顺序依赖**：
// 先修「落点」而不修「广播」，会把「笔记没人看见」升级成「笔记写进了别人的库」
// （因为 payload 里一旦带上真的 workspace_id，全体广播就会把它推到每一个
// 在线用户的设备上，由 notes-store.ts 的 handleServerEvent 落进对方本地库）。
// ⇒ ⇒ ⇒ 「看不见」这个症状有**两个**成因，其中一个会掩盖另一个；
// ⇒ ⇒ ⇒ 修法必须**一次把两个都修**，否则第一个修完症状反而更严重。
type meetingNoteTarget struct {
	// noteTarget 是**共用**的落点口径（租户 + 属主）。
	//
	// ⚠ §941：此前这个结构把 WorkspaceID / OwnerID **自己抄了一份**，
	//	而随手记那条链（handleNotes POST）是内联写的
	//	⇒ ⇒ ⇒ 「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」（§80 的形状）
	//	⇒ ⇒ ⇒ 现在收成**一份**（server_note_ingest.go 的 noteTarget），
	//	两条链都从它派生 ⇒ ⇒ ⇒ 「落点只认 JWT 不认 body」只写一次。
	noteTarget
	// MeetingID 是会议 id：它派生出这条笔记的 id。
	MeetingID string
}

// meetingNoteIDPrefix 是派生 id 的前缀。
//
// ⚠ 为什么用**派生 id**而不是 randomID：
// `notes.Store.Upsert` 的主键是 id，冲突时走 ON CONFLICT（§939.2 给它补了
// 归属守卫）⇒ ⇒ ⇒ 同一个 id 再写一次就是**更新同一行** ⇒ ⇒ ⇒
// 「同一个 meeting 精校两次」天然幂等，不会多出第二条笔记，也不会把用户
// 在旧那条上改过的正文留成孤儿。
//
// ⚠ 可预测 id 的代价：id 变得可猜 ⇒ 「按 id 猜别人的行」这条攻击面出现了
// ⇒ ⇒ ⇒ 这就是 §939.3 必须给 ON CONFLICT 补归属守卫的原因
// （不补 = 任何登录用户 POST /api/notes 带别人的 id 就能覆盖别人的正文）。
const meetingNoteIDPrefix = "note_mtg_"

// noteID 派生出这条笔记的行 id。
//
// ⚠ 命名空间：随手记那条链用的是**设备本地 id**（notes-persist.ts 的
// newNoteId()，形如 note-<ts>-<rand>）⇒ ⇒ ⇒ `note_mtg_` 前缀与它不撞。
func (t meetingNoteTarget) noteID() string {
	return meetingNoteIDPrefix + t.MeetingID
}

// broadcastTarget 是这条笔记的**广播目标**。
//
// ⚠ 零值 target 会退化成**全局广播**（websocket/hub.go 的 BroadcastTo：
// 「target 为零值时退化为全局广播」）⇒ ⇒ ⇒ 这里必须保证 UserID 非空，
// 否则一次「广播目标忘了填」就会变成「把会议笔记推给全体在线用户」。
// ⇒ ⇒ ⇒ 判据专门钉这一格（server_meeting_note_ingest_test.go）。
//
// ⚠ §941：这个方法现在**从 noteTarget 提升上来**（共用那一份）——
// 随手记链（handleNotes POST）用的是**同一个**实现
// ⇒ ⇒ ⇒ 两条链的广播口径**物理上**不可能再分叉。
func (t meetingNoteTarget) broadcastTarget() ws.BroadcastTarget {
	return t.noteTarget.broadcastTarget()
}

// newMeetingNoteTarget 从调用方身份推出落点。
//
// ⚠ workspaceID 为空时回落 "default"，与 §938 的 meetingTodoTarget
// **同口径**（同一个 workspace 归一 ⇒ 会议缓存、会议待办、会议笔记
// 落在同一个租户里）⇒ ⇒ ⇒ 改这一处必须同步改那两处。
//
// ⚠ §941：归一本身也收到**共用**的 newNoteTarget 里（随手记链走同一个）
// ⇒ ⇒ ⇒ 「空租户回落 default」只有一个来源。
func newMeetingNoteTarget(uid, workspaceID, meetingID string) meetingNoteTarget {
	return meetingNoteTarget{
		noteTarget: newNoteTarget(uid, workspaceID),
		MeetingID:  meetingID,
	}
}

// ingestMeetingNote 把一场会议的精校稿落成一条笔记。
//
// ⚠ §939 的三个修复点都在这里：
//
//	D1 落点：WorkspaceID 现在**真的写进行里**。
//	   修复前 notes.Note 的 WorkspaceID 是空串 ⇒ notes 表的 workspace_id
//	   列存的是 ''（不是 'default'！—— INSERT 显式传了该列 ⇒ 列的
//	   DEFAULT 'default' 根本不生效）⇒ ⇒ ⇒ 而**所有作用域**读路径
//	   （ListScoped / GetByIDScoped / UpdateNoteScoped / DeleteScoped /
//	   ListDeletedIDsScoped）都按 workspace_id = $2 过滤 ⇒ ⇒ ⇒
//	   ⇒ ⇒ ⇒ **所有生产读/写路径都查不到它**。
//	   （未作用域的 List / GetByID / Delete 确实能碰到它，但那三条
//	   **仓内没有生产调用方** —— store 自己的注释就写着「别用它们」）
//	   ⇒ ⇒ ⇒ **这条笔记对用户完全不可见**。前端那条注释记的就是同一个
//	   症状（会议自动生成的手记在笔记列表里永远看不见，且不报任何错）——
//	   只不过前端已经修了，服务端这一侧漏了，而且形态更重
//	   （'' 连 'default' 作用域都查不到）。
//	   ⚠ §941：这一列现在由**共用**的 ingestNote 写入（不是在这里写）
//	   ⇒ ⇒ ⇒ 「落点只认 JWT」只有**一个**来源 ⇒ 随手记链与会议链
//	   **不可能**再分叉（这正是 D1 当时发生的形状）。
//
//	D2 广播：只发给属主。
//	   修复前是 wsHub.Broadcast ⇒ **全体广播** ⇒ 每个在线用户的设备都会
//	   收到这条会议笔记，并由 notes-store.ts 的 handleServerEvent 落进
//	   对方自己的本地库。与随手记那条链（handleNotes 用 BroadcastTo
//	   {UserID: uid}）形态分叉 ⇒ 「同一件事抄两遍 ⇒ 同一缺陷出现在两处」。
//	   ⚠ §941：广播目标现在也来自**共用**的 noteTarget.broadcastTarget()
//	   ⇒ 随手记链用的是**同一个**实现 ⇒ 两条链的广播口径**物理上**统一。
//
//	D3 幂等：id 由 meetingID 派生 ⇒ 重复精校 = 更新同一行。
//
// ⚠ 仍然**只**存前 500 字节（§333.5 的形状）：Snippet 是列表摘要，
// Content 为空 ⇒ notes.Store.Upsert 回落 content = n.Snippet
// ⇒ ⇒ ⇒ 落库的正文恒为 ≤500 **字节**（中文约 166 字）⇒ 与会议长度无关。
// 这不是本节能定的：要不要为精校正文留全文副本，是**存储取舍**
// （与「不增加大量存储」这条要求直接相关）⇒ 已挂在简报第 161 条 ①（§937 登记；
// §939 的 163 ① 复述）
// ⇒ ⇒ ⇒ 本节不代拍，只把「现状是**截断**不是摘要」这个前提更正过去
// （详见 §939.2.4）。
func (s *Server) ingestMeetingNote(
	ctx context.Context, target meetingNoteTarget, content string, meta meetingMetaIn,
) (string, error) {
	if s.notesStore == nil {
		return "", fmt.Errorf("notes store not configured")
	}
	title := meta.Title
	if title == "" {
		title = "会议纪要"
	}
	now := time.Now().Unix()
	n := notes.Note{
		ID: target.noteID(),
		// ⚠ §941 D2：UserID / WorkspaceID **不在这里写** ——
		//	落点由 ingestNote 统一写入（它无条件覆写成 target 的值）
		//	⇒ ⇒ ⇒ 「落点只认 JWT 不认 body」只有**一个**来源
		//	（§939 D1 的形状就是「抄两份、其中一份抄错」的产物）。
		Title:          title,
		Snippet:        truncateStr(content, 500),
		ContentType:    "voice",
		Domain:         "work",
		CreatedByVoice: true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// ★ §939 D2：定向广播。**不是** wsHub.Broadcast。
	//	§941：落库 → 广播 → 异步分类这三步现在与随手记链**共用同一份**
	//	（server_note_ingest.go 的 ingestNote）⇒ ⇒ ⇒ 两条链不可能再分叉。
	if err := s.ingestNote(ctx, target.noteTarget, &n); err != nil {
		return "", err
	}
	log.Printf("[meeting] created note %s from meeting %s (workspace=%s)",
		n.ID, target.MeetingID, target.WorkspaceID)
	return n.ID, nil
}
