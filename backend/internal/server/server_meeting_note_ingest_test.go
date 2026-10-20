package server

// §939 判据：服务端「会议 → 笔记」这条链。
//
// ⚠ 修复前这条链踩了四个坑（其中三个会互相掩盖）：
//
//	D1 落点丢租户：ingestMeetingNote **根本不传 workspace** ⇒ notes 行的
//	   workspace_id 是**空串**（不是 'default'！—— INSERT 显式传了该列，
//	   列的 DEFAULT 'default' 根本不生效）⇒ 而所有作用域读路径都按
//	   workspace_id = $2 过滤 ⇒ ⇒ ⇒ 这条笔记对用户和所有代码路径
//	   **完全不可见**（前端那条注释记的就是同一个症状：会议自动生成的手记
//	   在笔记列表里永远看不见，且不报任何错）。
//	D2 广播没有作用域：wsHub.Broadcast ⇒ **全体广播** ⇒ 每个在线用户的
//	   设备都会收到这条会议笔记并落进**自己的**本地库。
//	   ⚠ 与 D1 有顺序依赖：先修 D1 不修 D2，会把「笔记没人看见」升级成
//	   「笔记写进了别人的库」（payload 一旦带上真的 workspace_id 就会被
//	   推到每一个人面前）⇒ ⇒ ⇒ 两个成因必须**一起**修。
//	D3 没有幂等：id 是 randomID ⇒ 同一场会议精校两次 ⇒ 两条笔记，
//	   旧那条成孤儿（用户改过的正文留在旧条上）。
//	D4 正文只留前 500 字节：这是**存储取舍**（要不要为精校正文留全文副本）
//	   ⇒ 已挂在简报第 161 条 ①（§937 登记）⇒ 本节**不代拍**，只钉住「现状是截断」。
//
// ⚠ 判据量的是**落库之后的行**与**查回之后的结果**，不是源码文本。
//   「源码里出现了 workspace_id」这种形状守卫证明不了「真的落进了那个租户」。
//
// ⚠ 需要真 PG：notes.Store 只有 PostgreSQL 一个实现 ⇒ 没有内存版可测
//   ⇒ ⇒ 租户落点 / 幂等 / 归属守卫这三件事**没有别的实现能测**。
//   没有 PG 时这些用例会 skip（退出码仍是 0）⇒ ⇒ 变异批次用
//   forbid_skip=True 把「被 skip」变成失败（见 scripts/mutations/s939.py）。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

const (
	s939Workspace = "ws-s939"
	s939Owner     = "user-s939"
	s939OtherUser = "user-s939-other"
)

// newS939Server 造一个带真 PG 笔记库的 Server。
func newS939Server(t *testing.T) (*Server, *notes.Store) {
	t.Helper()
	pool := mustTestPool(t)
	store, err := notes.NewStore(pool)
	if err != nil {
		t.Fatalf("notes.NewStore: %v", err)
	}
	srv, _ := newTestServerWithAuth(t)
	srv.notesStore = store
	return srv, store
}

// ── 纯函数层（不需要 PG）────────────────────────────────────────────

// TestS939MeetingNoteIDIsDerivedAndStable 同一场会议必须永远得到同一个 id。
//
// ⚠ 这是幂等的**前提**：notes 表主键是 id、冲突走 ON CONFLICT ⇒
// id 稳定 ⇒ 重复精校就是更新同一行，而不是多插一条。
func TestS939MeetingNoteIDIsDerivedAndStable(t *testing.T) {
	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-1")
	again := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-1")
	if target.noteID() != again.noteID() {
		t.Errorf("同一场会议派出了两个 id: %q vs %q", target.noteID(), again.noteID())
	}
	other := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-2")
	if target.noteID() == other.noteID() {
		t.Errorf("两场不同会议派出了同一个 id: %q", target.noteID())
	}
	if got := target.noteID(); got[:len(meetingNoteIDPrefix)] != meetingNoteIDPrefix {
		t.Errorf("派生 id=%q, want 以 %q 开头（命名空间要与设备本地 id 区分）",
			got, meetingNoteIDPrefix)
	}
}

// TestS939BroadcastTargetIsScopedToOwner 广播目标必须**非零**且指向属主。
//
// ⚠ 零值 target 在 hub 里会**退化成全局广播**（BroadcastTo 的注释写得很
// 清楚）⇒ ⇒ 「忘了填 OwnerID」这件事必须在这里被挡住。
func TestS939BroadcastTargetIsScopedToOwner(t *testing.T) {
	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-1")
	bt := target.broadcastTarget()
	if bt.UserID != s939Owner {
		t.Errorf("广播目标 UserID=%q, want %q", bt.UserID, s939Owner)
	}
	if bt.UserID == "" && bt.WorkspaceID == "" {
		t.Errorf("广播目标是零值 ⇒ hub 会退化成**全局广播**（D2：会议笔记被推给所有在线用户）")
	}
	// 负控：workspace 归一后，租户不该被拼进广播目标
	// （随手记那条链的既有口径是 UserID-only ⇒ 两条链必须同形）。
	if bt.WorkspaceID != "" {
		t.Errorf("广播目标 WorkspaceID=%q, want 空（与随手记那条链同口径：只按 UserID 过滤）",
			bt.WorkspaceID)
	}
}

// TestS939MeetingNoteTargetNormalizesEmptyScope 空归属/空租户要归一。
//
// ⚠ 与 §938 的 meetingTodoTarget **同口径**（同一个 workspace 归一
// ⇒ 会议缓存、会议待办、会议笔记落在同一个租户里）。
func TestS939MeetingNoteTargetNormalizesEmptyScope(t *testing.T) {
	target := newMeetingNoteTarget("", "", "mtg-s939-x")
	if target.OwnerID != "local" {
		t.Errorf("空归属归一为 %q, want \"local\"", target.OwnerID)
	}
	if target.WorkspaceID != "default" {
		t.Errorf("空租户归一为 %q, want \"default\"", target.WorkspaceID)
	}
}

// ── 真 PG 层（量落库之后的行）────────────────────────────────────────

// TestS939MeetingNoteLandsInCallerWorkspace 笔记必须落进**调用方**的租户。
//
// ⚠ D1 的行为判据：不是「代码里写了 workspace_id」，而是
// 「ListScoped / GetByIDScoped 查得到」+「别的租户查不到」。
func TestS939MeetingNoteLandsInCallerWorkspace(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()
	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-tenant")
	if _, err := srv.ingestMeetingNote(ctx, target, "会议正文：张伟确认了预算。", meetingMetaIn{Title: "评审会"}); err != nil {
		t.Fatalf("ingestMeetingNote: %v", err)
	}

	rows, err := store.ListScoped(ctx, s939Owner, s939Workspace, "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("调用方租户 %q 下的笔记=%d 条, want 1（D1：落错租户 ⇒ 用户看不见）: %#v",
			s939Workspace, len(rows), rows)
	}
	got := rows[0]
	if got.WorkspaceID != s939Workspace {
		t.Errorf("workspace_id=%q, want %q（D1：落点丢了 ⇒ 谁也查不到）",
			got.WorkspaceID, s939Workspace)
	}
	if got.UserID != s939Owner {
		t.Errorf("user_id=%q, want %q", got.UserID, s939Owner)
	}

	// 单条按 id 查也要得到（GetByIDScoped 是详情/编辑/删除的入口）。
	single, err := store.GetByIDScoped(ctx, got.ID, s939Owner, s939Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if single == nil {
		t.Fatalf("GetByIDScoped(%s) 查不到（D1：这条笔记对用户不可见）", got.ID)
	}

	// D1 的另一半：不能有东西漏进遗留单租户桶（那是跨租户可见）。
	leaked, err := store.ListScoped(ctx, s939Owner, "default", "")
	if err != nil {
		t.Fatalf("ListScoped(default): %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("default 桶里落了 %d 条会议笔记（D1：租户落点错了 ⇒ 跨租户可见）: %#v",
			len(leaked), leaked)
	}
}

// TestS939MeetingNoteIsIdempotentAcrossRefines 同一场会议精校两次 ⇒ 只有一行。
//
// ⚠ D3：修复前 id 是 randomID ⇒ 两次精校 ⇒ 两条笔记 ⇒ 笔记列表里出现
// 同名的两条，且用户在旧那条上改过的正文被留成孤儿。
func TestS939MeetingNoteIsIdempotentAcrossRefines(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()
	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-idem")

	first, err := srv.ingestMeetingNote(ctx, target, "第一版正文", meetingMetaIn{Title: "评审会"})
	if err != nil {
		t.Fatalf("第一次 ingestMeetingNote: %v", err)
	}
	before, err := store.GetByIDScoped(ctx, first, s939Owner, s939Workspace)
	if err != nil || before == nil {
		t.Fatalf("取第一次落库的行失败: err=%v row=%v", err, before)
	}

	second, err := srv.ingestMeetingNote(ctx, target, "第二版正文（更长的精校稿）", meetingMetaIn{Title: "评审会"})
	if err != nil {
		t.Fatalf("第二次 ingestMeetingNote: %v", err)
	}
	if first != second {
		t.Errorf("两次精校派出了两个 id: %q vs %q（D3：没有幂等 ⇒ 同一场会两条笔记）", first, second)
	}

	rows, err := store.ListScoped(ctx, s939Owner, s939Workspace, "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("两次精校后租户下有 %d 条笔记, want 1（D3：重复精校又插了一条）: %#v",
			len(rows), rows)
	}
	// 第二次必须是**更新**（正文换成新的），不是「两次都成功但内容是旧的」。
	if rows[0].Snippet == before.Snippet {
		t.Errorf("第二次精校后正文没变: %q ⇒ 走的是「插了但没更新」", rows[0].Snippet)
	}
}

// TestS939MeetingNoteUpsertRepairsWrongWorkspace 已经落错租户的行要能被搬回来。
//
// ⚠ 这是 D1 修法对**存量**是否生效的判据：ON CONFLICT 的 SET 列表里
// 少了 workspace_id ⇒ 落错租户的行**永远改不回来** ⇒ 同一个 id 再写一次
// 也只改别的列。
func TestS939MeetingNoteUpsertRepairsWrongWorkspace(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()
	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-repair")

	// 预置一条**落错租户**的行（模拟 §939 修复前写进去的那条：workspace_id=''）。
	stale := notes.Note{
		ID:          target.noteID(),
		UserID:      s939Owner,
		WorkspaceID: "",
		Title:       "评审会",
		Snippet:     "旧正文",
		ContentType: "voice",
		Domain:      "work",
	}
	if err := store.Upsert(ctx, &stale); err != nil {
		t.Fatalf("预置落错租户的行: %v", err)
	}

	if _, err := srv.ingestMeetingNote(ctx, target, "新正文", meetingMetaIn{Title: "评审会"}); err != nil {
		t.Fatalf("ingestMeetingNote: %v", err)
	}

	row, err := store.GetByIDScoped(ctx, target.noteID(), s939Owner, s939Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil {
		t.Fatalf("重精校后仍然查不到这条笔记 ⇒ 落错租户的行没被搬回来")
	}
	if row.WorkspaceID != s939Workspace {
		t.Errorf("workspace_id=%q, want %q（存量落错租户的行永远改不回来）",
			row.WorkspaceID, s939Workspace)
	}
}

// TestS939UpsertRejectsAnotherUsersNoteId 不能改**别人**的笔记。
//
// ⚠ 这是 §939 派生 id 带来的**配套守卫**：id 从「随机猜不到」变成
// 「可推算」⇒ ⇒ ⇒ 必须同时把「按 id 猜别人的行」这条攻击面关掉。
// ⚠ 负控：对方那一行必须**一个字都没被改**。
func TestS939UpsertRejectsAnotherUsersNoteId(t *testing.T) {
	_, store := newS939Server(t)
	ctx := context.Background()

	victim := notes.Note{
		ID:          "note_mtg_victim-meeting",
		UserID:      s939OtherUser,
		WorkspaceID: s939Workspace,
		Title:       "别人的会议纪要",
		Snippet:     "别人的正文",
		ContentType: "voice",
		Domain:      "work",
	}
	if err := store.Upsert(ctx, &victim); err != nil {
		t.Fatalf("预置受害者的行: %v", err)
	}

	attacker := victim
	attacker.UserID = s939Owner
	attacker.Title = "攻击者改的标题"
	attacker.Snippet = "攻击者写的正文"
	err := store.Upsert(ctx, &attacker)

	// ⚠ 归因要能区分**两种退化**（一条变异只改一个维度 ⇒ 红了哪几条
	//   才能定位到改坏的是哪一处）⇒ 这里把「有没有报错」与「对方那行
	//   有没有被改」两件事**分别**观测，并给出**互不相同**的措辞：
	//     ① err=nil 且对方那行被改  ⇒ 守卫（WHERE 谓词）整体没了；
	//     ② err=nil 但对方那行没动  ⇒ 守卫在，只是**没把冲突报出来**；
	//     ③ err 不是冲突错误         ⇒ 报了个别的错（另案）。
	row, verr := store.GetByIDScoped(ctx, victim.ID, s939OtherUser, s939Workspace)
	if verr != nil || row == nil {
		t.Fatalf("取受害者的行失败: err=%v row=%v", verr, row)
	}
	overwritten := row.Title != victim.Title || row.Snippet != victim.Snippet

	switch {
	case errors.Is(err, notes.ErrNoteOwnershipConflict):
		// 守卫完全生效：报了错，且对方那行一个字都没动（下面统一断言）。
	case err == nil && overwritten:
		t.Errorf("撞别人的 id 时 err=<nil> 且受害者的笔记被改了（归属守卫整体失效）")
	case err == nil && !overwritten:
		t.Errorf("撞别人的 id 时 err=<nil>（守卫在但没把冲突报出来 ⇒ 调用方会以为写成功了）")
	default:
		t.Errorf("撞别人的 id 时 err=%v（既不是 ErrNoteOwnershipConflict，也没改对方的行）", err)
	}

	// 负控：对方那一行必须**一个字都没被改**。
	if overwritten {
		t.Errorf("受害者的笔记被改了: title=%q snippet=%q（want %q / %q）",
			row.Title, row.Snippet, victim.Title, victim.Snippet)
	}
}

// TestS939DifferentMeetingsGetDifferentNotes **负控**：不得跨会议去重。
//
// ⚠ 幂等是「同一场会议」内的幂等，不是「所有会议共用一条」。
func TestS939DifferentMeetingsGetDifferentNotes(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()
	for _, id := range []string{"mtg-s939-a", "mtg-s939-b"} {
		target := newMeetingNoteTarget(s939Owner, s939Workspace, id)
		if _, err := srv.ingestMeetingNote(ctx, target, "正文 "+id, meetingMetaIn{Title: "评审会"}); err != nil {
			t.Fatalf("ingestMeetingNote(%s): %v", id, err)
		}
	}
	rows, err := store.ListScoped(ctx, s939Owner, s939Workspace, "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("两场会议落库后有 %d 条笔记, want 2（跨会议被去重了 ⇒ 一场会的笔记丢了）: %#v",
			len(rows), rows)
	}
}

// TestS939FinalizeMeetingRefineLandsNoteInCallerWorkspace 端到端：精校收尾
// 建的笔记必须落在调用方租户里。
//
// ⚠ 这一条量的是**真正的调用点**（finalizeMeetingRefine），不是直接调
// ingestMeetingNote ⇒ ⇒ ⇒ 口径在调用点被漏掉也会红。
func TestS939FinalizeMeetingRefineLandsNoteInCallerWorkspace(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()

	result := map[string]any{
		"refined_transcript": "张伟：二期在十一月底量产。",
		"todos":              []any{},
	}
	out := srv.finalizeMeetingRefine(ctx, s939Owner, s939Workspace, "mtg-s939-e2e",
		result, meetingMetaIn{Title: "评审会"})

	noteID, _ := out["note_id"].(string)
	if noteID == "" {
		t.Fatalf("精校响应里没有 note_id: %#v", out)
	}
	row, err := store.GetByIDScoped(ctx, noteID, s939Owner, s939Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil {
		t.Fatalf("精校建出的笔记 %q 在调用方租户里查不到（D1：落点丢了）", noteID)
	}
	if row.WorkspaceID != s939Workspace {
		t.Errorf("workspace_id=%q, want %q", row.WorkspaceID, s939Workspace)
	}
}

// ── 行为判据：广播只到属主（真 WS）───────────────────────────────

// dialS939WS 起一条**真** WebSocket 连接并返回客户端。
//
// ⚠ 为什么要真连接：hub 的 Client 字段（send/userID/workspaceID）都不导出，
//
//	从 server 包**看不到**「谁收到了哪条消息」⇒ ⇒ ⇒ 纯函数层只能验
//	broadcastTarget() 这个**值**，验不了**调用点**有没有用对它
//	（把调用点的 BroadcastTo 改成 Broadcast，纯函数层照样全绿）。
//	⇒ 唯一诚实的做法是接一条真连接，看消息到底落到谁那里。
func dialS939WS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("WS 拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readS939WSMessage 在 deadline 之前读帧，返回第一条 type == wantType 的消息。
// 超时或连接断开都返回 ("", false)。
func readS939WSMessage(conn *websocket.Conn, wantType string, deadline time.Time) (string, bool) {
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return "", false
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", false
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if env.Type == wantType {
			return string(data), true
		}
	}
}

// TestS939MeetingNoteBroadcastReachesOnlyOwner 会议笔记**只**发给属主。
//
// ⚠ D2 的**行为**判据（不是源码形状判据）：修复前是 wsHub.Broadcast
//
//	⇒ **全体广播** ⇒ 每个在线用户的设备都会收到这条会议笔记，
//	而前端 notes-store.ts 的 handleServerEvent **不做任何用户/租户过滤**
//	⇒ 直接把别人的会议笔记写进自己的设备本地库（跨用户数据泄漏）。
//
// ⚠ 两条臂缺一不可：
//
//	臂 A（属主必须收到）= **正控**：证明「定向广播这条路本身是通的」
//	  —— 否则臂 B 的「别人收不到」可能只是因为「谁都没收到」
//	  （量具坏了，而不是被测对象对了）。
//	臂 B（别人必须收不到）= D2 的真判据。
func TestS939MeetingNoteBroadcastReachesOnlyOwner(t *testing.T) {
	srv, _ := newS939Server(t)
	ctx := context.Background()

	ownerToken, err := srv.jwtSigner.SignWithWorkspace(s939Owner, "admin", s939Workspace)
	if err != nil {
		t.Fatalf("签属主 token: %v", err)
	}
	otherToken, err := srv.jwtSigner.SignWithWorkspace(s939OtherUser, "admin", s939Workspace)
	if err != nil {
		t.Fatalf("签另一个用户 token: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ownerConn := dialS939WS(t, ts, ownerToken)
	otherConn := dialS939WS(t, ts, otherToken)

	// 注册是**异步**的（hub.Run 收 register channel）⇒ 必须等两个都在册。
	deadline := time.Now().Add(5 * time.Second)
	for srv.wsHub.GetClientCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("两个 WS 客户端没都注册进 hub（count=%d）⇒ 本测试失去判别力",
				srv.wsHub.GetClientCount())
		}
		time.Sleep(10 * time.Millisecond)
	}

	target := newMeetingNoteTarget(s939Owner, s939Workspace, "mtg-s939-broadcast")
	if _, err := srv.ingestMeetingNote(ctx, target, "会议正文：预算八十万。", meetingMetaIn{Title: "评审会"}); err != nil {
		t.Fatalf("ingestMeetingNote: %v", err)
	}

	// 臂 A（正控）：属主必须收到 note.created。
	_, ownerGot := readS939WSMessage(ownerConn, "note.created", time.Now().Add(5*time.Second))
	if !ownerGot {
		t.Fatalf("属主没收到 note.created ⇒ 定向广播这条路本身坏了（量具坏了，不是被测对象对）")
	}

	// 臂 B：另一个用户**必须收不到**。
	// ⚠ 宽限期取 1s：hub 在**同一个** Run 循环迭代里把消息塞进各客户端的
	//   send 队列 ⇒ 若走全体广播，两条连接是**同时**被投递的
	//   ⇒ 属主都收到了，1s 内对方不可能还没收到。
	_, otherGot := readS939WSMessage(otherConn, "note.created", time.Now().Add(1*time.Second))
	if otherGot {
		t.Fatalf("另一个用户收到了这条会议笔记（D2：全体广播 ⇒ 会议笔记被推进别人的设备本地库）")
	}
}

// TestS939RefineResultWithNoteIDSkipsLocalNote **负控**（明知它绿）：
// 精校结果里**已经带了** note_id 时，服务端**不**再自己建笔记。
//
// ⚠ 这条登记的是一个**形态分叉**，不是本节要修的东西：
//
//	finalizeMeetingRefine 里 `if noteID == "" && refined != ""` ⇒
//	  kxmemory 那条链（finalizeKxRefine）如果在上游就建好了笔记
//	  （resp.NoteID != ""），服务端**根本不会**调 ingestMeetingNote
//	  ⇒ ⇒ ⇒ D1/D2/D3 **只发生在**「服务端自己建笔记」的那条路径上
//	  （LLM 兜底链，或 kxmemory 响应里没有 note_id）。
//
// ⇒ 写成负控，是为了让后人**不会**以为 §939 的修法覆盖了全部精校路径：
//
//	上游建好的笔记存在 kxmemory 侧，**不在**本地 notes 表里
//	⇒ 本地笔记列表仍然看不到它（那是**另一个**缺口 ⇒ 已登记进简报）。
func TestS939RefineResultWithNoteIDSkipsLocalNote(t *testing.T) {
	srv, store := newS939Server(t)
	ctx := context.Background()

	result := map[string]any{
		"note_id":            "note-from-upstream",
		"refined_transcript": "上游已经建好笔记了。",
	}
	out := srv.finalizeMeetingRefine(ctx, s939Owner, s939Workspace, "mtg-s939-upstream",
		result, meetingMetaIn{Title: "评审会"})

	if got := out["note_id"]; got != "note-from-upstream" {
		t.Fatalf("note_id=%v, want %q（上游建的 id 必须原样透传）", got, "note-from-upstream")
	}
	rows, err := store.ListScoped(ctx, s939Owner, s939Workspace, "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("上游已带 note_id 时服务端又建了 %d 条本地笔记 ⇒ 形态分叉变了: %#v", len(rows), rows)
	}
}
