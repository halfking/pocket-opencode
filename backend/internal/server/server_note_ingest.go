package server

// §941：「随手记 → 笔记」这条链的**共用口径**。
//
// ⚠ 为什么会单独一个文件（理由与 §938/§939 同款）：
//
//	服务端有**两条**建笔记的路径：
//	  - 随手记链：  POST /api/notes → handleNotes → notesStore.Upsert
//	  - 会议精校链：handleMeetingRefine → finalizeMeetingRefine → ingestMeetingNote
//
//	§939 把**会议那条**的口径抽成了共用的一份（server_meeting_note_ingest.go），
//	而**随手记那条**仍然是内联写在 handler 里的
//	⇒ ⇒ ⇒ 「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」（§80 记过的形状）
//	⇒ ⇒ ⇒ 落点、广播目标、落库/广播/分类的**序列**这三件事都必须收到一份。
//
// ★ 本文件抽的东西（缺一件，缺陷就会以另一种形态回来）：
//
//	 1. **落点**（noteTarget）：租户 + 属主，**只认 JWT**，永远不认 body；
//	 2. **广播目标**（broadcastTarget）：定向、且**非零**（零值 = 全体广播）；
//	 3. **落库 → 广播 → 异步分类**的序列（ingestNote）：一个地方写死；
//	 4. **正文上限**（noteBodyLimitBytes / readNoteBody）：这条链只有**一份**上限，
//	    且「太大」与「格式错」**可区分**。
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/background"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// noteTarget 是「一条笔记」的**落点口径**（全仓一份）。
//
// ⚠ 修复前这条口径在两个地方各写了一遍：
//
//	会议链：  meetingNoteTarget{WorkspaceID, OwnerID, MeetingID}（§939 抽的）
//	随手记链：handleNotes POST 里内联的 n.UserID = uid; n.WorkspaceID = wsID
//
// ⇒ ⇒ ⇒ 「落点只认 JWT 不认 body」这条**安全不变量**当时有两个来源
// ⇒ ⇒ ⇒ 而 §939 的 D1（落进空租户 ⇒ 所有人都查不到）恰恰就是**其中一份抄错了**
// ⇒ ⇒ ⇒ 这类形状必须收成一份，而不是各写各的。
type noteTarget struct {
	// WorkspaceID 是这条笔记所属的租户。
	WorkspaceID string
	// OwnerID 是这条笔记的属主（也是广播的唯一目标）。
	OwnerID string
}

// newNoteTarget 从调用方身份推出落点（**归一只在这一处**）。
//
// ⚠ 为什么这里还要归一（userIDFromRequest / workspaceIDFromRequest
// **已经**各自带了一个回落）：
//
//	那两个回落是**读取**时的兜底，而这里是**写入**时的兜底 ⇒ 两次兜底
//	可能给出不同的字符串（读取兜底与写入兜底漂移 ⇒ 一条笔记「读得到、
//	写不进」或反之）⇒ ⇒ ⇒ 归一必须只在一处做，两条链都走它。
func newNoteTarget(uid, workspaceID string) noteTarget {
	if uid == "" {
		uid = "local"
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	return noteTarget{WorkspaceID: workspaceID, OwnerID: uid}
}

// broadcastTarget 是这条笔记的**广播目标**。
//
// ⚠ 零值 target 会退化成**全局广播**（websocket/hub.go 的 BroadcastTo：
// 「target 为零值时退化为全局广播」）⇒ ⇒ ⇒ 这里必须保证 UserID 非空，
// 否则一次「广播目标忘了填」就会变成「把笔记推给全体在线用户」。
//
// ⚠ 为什么**只**按 UserID 过滤、**不**按 WorkspaceID 过滤（读码 + 实测结论）：
//
//	· 载荷里带着 workspaceId（notes.Note 的 JSON tag 是 workspaceId）
//	  ⇒ 前端 notes-store.ts 的 handleServerEvent **按载荷里的 workspaceId 归档**
//	  ⇒ ⇒ ⇒ 同一个人另一个租户的设备即使收到这条 note.created，
//	  也不会把它误归档到当前租户 ⇒ **不是**越权，也不是误归档。
//	· 反过来，加上 WorkspaceID 过滤会**打断**「同一个人跨租户设备的笔记同步」：
//	  租户 B 下的设备将再也收不到租户 A 的笔记。
//	⇒ ⇒ ⇒ 5 个笔记广播点（note.created / note.classified /
//	  note.classification_failed / note.ssot_conflict / 会议链的 note.created）
//	  **口径一致**（都按 UserID）⇒ 这不是「分叉」，是**统一**。
//	⇒ ⇒ ⇒ 「同一用户跨租户广播」**不是缺陷**，详见主文档 §941.3 的阴性结论。
func (t noteTarget) broadcastTarget() ws.BroadcastTarget {
	return ws.BroadcastTarget{UserID: t.OwnerID}
}

// ingestNote 落库 + 定向广播 + 异步分类（**一条链一份**）。
//
// ⚠ 为什么这三件事必须放在一起：
//
//	修复前它们在两个地方各写了一遍（会议链 ingestMeetingNote、
//	随手记链 handleNotes POST）⇒ ⇒ ⇒ §80 形状。
//	⇒ 其中「落点来自 JWT 不来自 body」这条**安全不变量**，
//	只有在**一个**地方写死才守得住：
//	⇒ 这里**无条件覆写** n.UserID / n.WorkspaceID
//	⇒ ⇒ ⇒ 哪怕调用方把 body 里的 userId/workspaceId 塞进来也没用。
//
// ⚠ 顺序：**先落库，后广播**。
//
//	⇒ 广播是给别的设备看的「这条笔记有了」；先落库 ⇒ 收到广播的设备
//	立刻去拉时，那一行**一定已经在**（否则会拉到 404，客户端看起来像丢了一条）。
func (s *Server) ingestNote(ctx context.Context, target noteTarget, n *notes.Note) error {
	if s.notesStore == nil {
		return errors.New("notes store not configured")
	}
	// ★ 落点只认 target（= 从 JWT 推导出来的），**不认 body**。
	n.UserID = target.OwnerID
	n.WorkspaceID = target.WorkspaceID
	if err := s.notesStore.Upsert(ctx, n); err != nil {
		return err
	}
	s.wsHub.BroadcastTo(target.broadcastTarget(), "note.created", n)
	// 异步调 kxmemory（非阻塞）
	background.Go1("note/classify", s.classifyNoteAsync, *n)
	return nil
}

// writeNoteWriteError 把「写笔记失败」映射成 HTTP 语义。
//
// ⚠ §941 D3：修复前**所有**写入错误都回 **500**
// ⇒ ⇒ ⇒ 「撞 id 但那一行不是自己的」（notes.ErrNoteOwnershipConflict）
// 被说成「服务端出错了」⇒ ⇒ ⇒ 客户端会以为**重试有用**
// ⇒ ⇒ ⇒ 而它永远重试都不会成功（重试一万次也是同一行别人的）。
// ⇒ ⇒ ⇒ 归属冲突必须回 **409 + CodeConflict**
// （与仓库既有的冲突语义一致：mobile_endpoint_scope.go 的 CodeConflict）。
//
// ⚠ 别的错误仍然是 500 ⇒ **不改变既有语义**。
func (s *Server) writeNoteWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, notes.ErrNoteOwnershipConflict) {
		s.writeStructuredError(w, r, http.StatusConflict, CodeConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// noteBodyLimitBytes 是「随手记 → 笔记」这条链的**正文上限**（全仓一份）。
//
// ⚠ §941：此前这条链有**三份**上限口径：
//
//	POST /api/notes       → 全局 requestBodyLimitMiddleware 的 2 MiB
//	PUT  /api/notes/{id}  → handler 里**硬编码字面量**的 io.LimitReader(r.Body, 1<<20)
//	mobile decodeJSONBody → maxJSONBodyBytes = 1 MiB（另一个域，刻意更严）
//
// ⇒ ⇒ ⇒ POST 收 2 MiB、PUT 只收 1 MiB ⇒ **同一条笔记写得进去、改不回来**
// ⇒ ⇒ ⇒ 而 PUT 那条闸是**字面量** ⇒ 改它没有名字可查，两处很容易漂。
//
// ⚠ 取值：**与全局口径同值**（2 MiB），**刻意不下调到 1 MiB** ——
//
//	下调会**缩小**今天已经能写进来的请求体
//	（仓外移动端今天能写 1.5 MiB 的笔记，**实测**服务端收得下）
//	⇒ 那是**破坏性变更**，不是修复 ⇒ ⇒ ⇒ 统一的方向必须是「向上收口」。
//
// ⚠ mobile 的 1 MiB **不动**：那是移动控制面自己的域
// （mobile_endpoint_scope.go 的 decodeJSONBody），它比全局更严是**刻意的**
// ⇒ ⇒ ⇒ 这里只把「笔记链内部」的两份收成一份。
const noteBodyLimitBytes = maxRequestBodyBytes

// readNoteBody 读这条链的请求体，并把「太大」与「读失败」**分开**。
//
// ⚠ §941 D1：此前两条路径各自处理超限，结果是：
//
//	POST：json.NewDecoder(r.Body).Decode(&n)
//	      ⇒ 超限时 JSON 被**截断** ⇒ decode 失败 ⇒ 400 "invalid body"
//	      ⇒ **与「JSON 坏了」完全同形**
//	PUT ：io.ReadAll(io.LimitReader(r.Body, 1<<20))
//	      ⇒ 超限时 JSON 被**截断** ⇒ 400 "invalid request body: unexpected end of JSON input"
//	      ⇒ **与「JSON 坏了」完全同形**（**实测**：1.5 MiB 的 PUT 拿到的就是这句）
//
// ⇒ ⇒ ⇒ 客户端分不出「太大」与「格式错」⇒ ⇒ ⇒ 用户看到的是「你的 JSON 坏了」，
//
//	而真实原因是「正文太长」⇒ ⇒ ⇒ 「失败必须可区分」这条被破坏了。
//
// ⚠ 修法与 §922 的 STT 链**同形状**（那是本仓已被接受的写法）：
//
//	先读出来 ⇒ 「读超了」（*http.MaxBytesError）⇒ **413 + 稳定错误码**
//	             「JSON 坏了」⇒ 仍是 **400**（**不改变既有语义**）
//
// ⚠ 为什么**先读出来**、而不是「边解码边判断」：
//
//	json.Decoder 找到**第一个完整 JSON 值**就停
//	⇒ 一个 3 MiB 的 body 若前 1 MiB 恰好是一个完整 JSON 对象
//	⇒ ⇒ 解码**成功**，剩下的 2 MiB **被静默丢弃**
//	⇒ ⇒ ⇒ 那道「上限」根本没生效（超限的请求被**部分处理**并回 201）
//	⇒ ⇒ ⇒ 读完再解 ⇒ 超限就是超限 ⇒ 不会处理半个请求。
func (s *Server) readNoteBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	// 显式再套一层：让这条链的上限**写在 handler 里**（不依赖中间件是否挂载），
	// 且与全局同值 ⇒ 两层是同一个数字，不会分叉。
	r.Body = http.MaxBytesReader(w, r.Body, noteBodyLimitBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			// 「太大」有自己的状态码与稳定错误码 ⇒ 客户端能区分
			// 「重试小一点有用」与「你的 JSON 坏了，重试没用」。
			s.writeStructuredError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				fmt.Sprintf("request body exceeds the maximum allowed size (%d MiB)", noteBodyLimitBytes>>20))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return nil, false
	}
	return raw, true
}

// decodeNoteJSON 把已读出的正文解进 dst。
//
// ⚠ **只**负责「JSON 坏了」这一种结论（400）；
// 「太大」在 readNoteBody 里就分出去了（413）⇒ ⇒ ⇒ 两者**不会**同形。
//
// ⚠ 用 json.NewDecoder(bytes.NewReader(raw)) 而不是 json.Unmarshal(raw, dst)：
//
//	前者与修复前的 json.NewDecoder(r.Body).Decode(dst) **语义相同**
//	（允许尾部有多余内容），后者会把尾部多余内容判成错误
//	⇒ ⇒ ⇒ 这里**不改变**既有语义（§922 的负控纪律：
//	「不能顺手把『无效』也改了」）。
func decodeNoteJSON(raw []byte, dst any) error {
	return json.NewDecoder(bytes.NewReader(raw)).Decode(dst)
}
