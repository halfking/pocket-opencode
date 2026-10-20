package server

// §941 判据：「随手记 → 笔记」这条链（POST /api/notes 与 PUT /api/notes/{id}）。
//
// ⚠ 修复前这条链踩了三个坑：
//
//	D1 正文上限**三份口径**，且「太大」与「格式错」**同形**：
//	   POST 走全局 2 MiB（requestBodyLimitMiddleware）、
//	   PUT 走 handler **自己**的一条 1 MiB **字面量**闸
//	   ⇒ ⇒ ⇒ 同一条笔记**写得进去、改不回来**
//	   （**实测**：POST 1.5 MiB ⇒ 201、落库 1,536,000 字节；
//	   同样 1.5 MiB 的 PUT ⇒ 400 "invalid request body: unexpected end of
//	   JSON input"）⇒ ⇒ ⇒ 而 PUT 那条 400 与「JSON 坏了」**完全同形**
//	   ⇒ ⇒ ⇒ 客户端分不出「太大」与「格式错」。
//	D2 「落库 → 广播 → 异步分类」这段序列**抄了两遍**（会议链 + 随手记链）
//	   ⇒ ⇒ ⇒ §80 的形状（「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」）
//	   ⇒ ⇒ ⇒ §939 的 D1（会议笔记落进空租户）就是这么来的。
//	D3 归属冲突（撞 id 但那一行**不是自己的**）被映射成 **500**
//	   ⇒ ⇒ ⇒ 客户端以为「重试有用」，而它永远重试都不会成功。
//
// ⚠ 判据量的是**HTTP 响应**与**落库之后的行**，不是源码文本。
//   「源码里出现了 payload_too_large」这种形状守卫证明不了
//   「客户端真的分得出太大与格式错」。
//
// ⚠ 需要真 PG：notes.Store 只有 PostgreSQL 一个实现 ⇒ 没有内存版可测
//   ⇒ ⇒ 落点 / 归属冲突这两件事**没有别的实现能测**。
//   没有 PG 时这些用例会 skip（退出码仍是 0）⇒ ⇒ 变异批次用
//   forbid_skip=True 把「被 skip」变成失败（见 scripts/mutations/s941.py）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	s941Owner     = "user-s941"
	s941OtherUser = "user-s941-other"
	s941Workspace = "ws-s941"
	s941OtherWs   = "ws-s941-other"
)

// s941Token 签一个带 workspace 的 token。
func s941Token(t *testing.T, srv *Server, user, workspace string) string {
	t.Helper()
	tok, err := srv.jwtSigner.SignWithWorkspace(user, "admin", workspace)
	if err != nil {
		t.Fatalf("签 token: %v", err)
	}
	return tok
}

// s941PostNote POST 一条正文为 contentBytes 字节的笔记。
func s941PostNote(t *testing.T, srv *Server, token, id string, contentBytes int) (int, string) {
	t.Helper()
	payload := map[string]any{
		"id":          id,
		"title":       "大笔记",
		"content":     strings.Repeat("a", contentBytes),
		"contentType": "text",
	}
	return s941PostRaw(t, srv, token, payload)
}

// s941PostRaw POST 一段**原始** body（用于「真的坏 JSON」这类形态）。
func s941PostRaw(t *testing.T, srv *Server, token string, payload any) (int, string) {
	t.Helper()
	var body string
	switch v := payload.(type) {
	case string:
		body = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("序列化 body: %v", err)
		}
		body = string(b)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// s941PutNote PUT 一条笔记的正文（contentBytes 字节）。
func s941PutNote(t *testing.T, srv *Server, token, id string, contentBytes int) (int, string) {
	t.Helper()
	payload := map[string]any{"content": strings.Repeat("a", contentBytes)}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化 body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/notes/"+id, strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// TestS941NoteBodyLimitIsIdenticalOnPostAndPut POST 与 PUT 的正文上限必须
// 是**同一个数字**。
//
// ⚠ D1 的行为判据：**不是**「源码里有没有某个常量」，而是
// 「同一条笔记，写得进去就改得回来」。
//
// ⚠ 1.5 MiB 这个取值是**有意的**：它**跨过旧 PUT 那道 1 MiB 字面量闸**，
// 而**仍在**全局 2 MiB 之内 ⇒ ⇒ ⇒ 修复前 POST 收它、PUT 拒它
// ⇒ ⇒ ⇒ 这个取值能同时暴露「两份口径」与「修好了」。
func TestS941NoteBodyLimitIsIdenticalOnPostAndPut(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	const contentBytes = 1500 * 1024

	postCode, postBody := s941PostNote(t, srv, token, "s941-limit", contentBytes)
	if postCode != http.StatusCreated {
		t.Fatalf("POST 1.5 MiB ⇒ %d %s（期望 201）", postCode, s941TruncBody(postBody))
	}

	putCode, putBody := s941PutNote(t, srv, token, "s941-limit", contentBytes)
	if putCode != http.StatusOK {
		t.Fatalf("PUT 1.5 MiB ⇒ %d %s —— **同一条笔记写得进去却改不回来**（D1：两条路径两份上限口径）",
			putCode, s941TruncBody(putBody))
	}

	// 落库的行必须**真的是** 1.5 MiB（不是被截断的 1 MiB）。
	row, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-limit", s941Owner, s941Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil {
		t.Fatalf("库里没有 s941-limit ⇒ POST 根本没落库（前一条判据失去意义）")
	}
	if len(row.Content) != contentBytes {
		t.Errorf("落库正文长度=%d 字节, want %d（正文被截断了 ⇒ 「写得进去」是假的）",
			len(row.Content), contentBytes)
	}
}

// TestS941OversizedNoteBodyIsTooLargeNotInvalidJSON 「太大」必须是
// **413 + 稳定错误码**，且**不得**与「JSON 坏了」同形。
//
// ⚠ 这是「失败必须可区分」那条纪律：客户端要能分辨
// 「重试小一点有用」与「你的 JSON 坏了，重试没用」。
//
// ⚠ 修法与 §922 的 STT 链同形状（先读出来 ⇒ 读超了判 413）。
func TestS941OversizedNoteBodyIsTooLargeNotInvalidJSON(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	// 超过全局上限（2 MiB）+ 一点余量。
	const overBytes = (2 << 20) + 4096

	postCode, postBody := s941PostNote(t, srv, token, "s941-over", overBytes)
	if postCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST 超限 ⇒ %d %s —— 期望 413（「太大」不能说成「请求体无效」）",
			postCode, s941TruncBody(postBody))
	}
	if !strings.Contains(postBody, CodePayloadTooLarge) {
		t.Errorf("POST 超限的 body=%s —— 缺少稳定错误码 %q ⇒ 客户端映射不到「太大」",
			s941TruncBody(postBody), CodePayloadTooLarge)
	}
	if strings.Contains(postBody, "invalid body") {
		t.Errorf("POST 超限被说成「请求体无效」：%s", s941TruncBody(postBody))
	}

	// PUT 必须**同样**是 413（不是 400，也不是「JSON 解析错误」）。
	putCode, putBody := s941PutNote(t, srv, token, "s941-over", overBytes)
	if putCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT 超限 ⇒ %d %s —— 期望 413", putCode, s941TruncBody(putBody))
	}
	if !strings.Contains(putBody, CodePayloadTooLarge) {
		t.Errorf("PUT 超限的 body=%s —— 缺少稳定错误码 %q", s941TruncBody(putBody), CodePayloadTooLarge)
	}
	if strings.Contains(putBody, "invalid request body") {
		t.Errorf("PUT 超限被说成「JSON 解析错误」：%s", s941TruncBody(putBody))
	}
}

// TestS941OversizedNoteBodyLeavesNoRow **负控**：超限的请求**不得**留下半条笔记。
//
// ⚠ 为什么这条重要：修复前 POST 用的是**流式解码**
// （json.NewDecoder(r.Body).Decode）⇒ Decoder 找到**第一个完整 JSON 值**就停
// ⇒ ⇒ ⇒ 一个 3 MiB 的 body 若前 1 MiB 恰好是一个完整 JSON 对象，
// ⇒ ⇒ ⇒ 解码**成功**、剩下 2 MiB **被静默丢弃** ⇒ ⇒ ⇒ 那道「上限」根本没生效
// ⇒ ⇒ ⇒ 修复方式是「先读出来再解」⇒ 读完才发现超限 ⇒ 整条请求被拒。
func TestS941OversizedNoteBodyLeavesNoRow(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	const overBytes = (2 << 20) + 4096
	code, body := s941PostNote(t, srv, token, "s941-half", overBytes)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST 超限 ⇒ %d %s（期望 413）", code, s941TruncBody(body))
	}

	row, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-half", s941Owner, s941Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row != nil {
		t.Fatalf("超限的请求**仍然落了库**（正文长度=%d）⇒ 上限没生效、请求被半处理了",
			len(row.Content))
	}
}

// TestS941OversizedBodyWithCompleteJSONPrefixIsRejected 超限的 body 即使
// **前面**有一段完整的 JSON，也不能被「处理一半」。
//
// ⚠ 为什么这条必须**单独**测：修复前 POST 用的是**流式解码**
// （json.NewDecoder(r.Body).Decode）⇒ Decoder 找到**第一个完整 JSON 值**就停
// ⇒ ⇒ ⇒ 「{"id":…,"content":"ok"}」后面再跟 3 MiB 垃圾
// ⇒ ⇒ ⇒ 解码**成功**（201），后面 3 MiB **被静默丢弃**
// ⇒ ⇒ ⇒ 那道「上限」根本没生效 ⇒ ⇒ ⇒ 请求被**半处理**了
// ⇒ ⇒ ⇒ 「返回 201 却只处理了 1% 的 body」是最难查的一种错。
//
// ⚠ 修复方式是「先读出来再解」⇒ 读完才发现超限 ⇒ 整条请求被拒。
func TestS941OversizedBodyWithCompleteJSONPrefixIsRejected(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	prefix := `{"id":"s941-prefix","title":"前缀","content":"ok","contentType":"text"}`
	body := prefix + strings.Repeat("x", 3<<20)

	code, resp := s941PostRaw(t, srv, token, body)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("前缀完整 + 3 MiB 尾巴 ⇒ %d %s —— 期望 413（上限必须对**整个** body 生效）",
			code, s941TruncBody(resp))
	}

	row, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-prefix", s941Owner, s941Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row != nil {
		t.Fatalf("超限的请求**仍然落了库**（正文长度=%d）⇒ 请求被半处理了（上限没生效）",
			len(row.Content))
	}
}

// TestS941BrokenNoteJSONIsStillBadRequest **负控**：**真的**坏 JSON 仍然是 400。
//
// ⚠ 这条不许被上面的改动带跑偏：把「太大」和「无效」分开是对的，
// 但不能顺手把「无效」也改成 413 ⇒ 客户端就分不清「重试有用」还是「重试没用」。
// （§922 的同款负控：server_stt_contract_test.go。）
func TestS941BrokenNoteJSONIsStillBadRequest(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	code, body := s941PostRaw(t, srv, token, `{"content":`)
	if code != http.StatusBadRequest {
		t.Fatalf("坏 JSON ⇒ %d %s —— 期望 400（不是 413）", code, s941TruncBody(body))
	}
	if strings.Contains(body, CodePayloadTooLarge) {
		t.Errorf("坏 JSON 被标成「太大」：%s", s941TruncBody(body))
	}
}

// TestS941NoteOwnershipConflictIsConflictNotServerError 归属冲突必须回 **409**。
//
// ⚠ D3：修复前**所有**写入错误都回 500 ⇒ ⇒ ⇒ 「撞 id 但那一行不是自己的」
// 被说成「服务端出错了」⇒ ⇒ ⇒ 客户端会以为重试有用，而它永远重试都不会成功。
func TestS941NoteOwnershipConflictIsConflictNotServerError(t *testing.T) {
	srv, _ := newS939Server(t)
	victimToken := s941Token(t, srv, s941Owner, s941Workspace)
	attackerToken := s941Token(t, srv, s941OtherUser, s941Workspace)

	// 受害者先建一条。
	code, body := s941PostNote(t, srv, victimToken, "s941-victim", 16)
	if code != http.StatusCreated {
		t.Fatalf("受害者建笔记 ⇒ %d %s（期望 201）", code, s941TruncBody(body))
	}

	// 攻击者用**同一个 id** ⇒ 归属冲突。
	code, body = s941PostNote(t, srv, attackerToken, "s941-victim", 16)
	if code != http.StatusConflict {
		t.Fatalf("撞别人的 id ⇒ %d %s —— 期望 409（500 会让客户端以为「重试有用」）",
			code, s941TruncBody(body))
	}
	if !strings.Contains(body, CodeConflict) {
		t.Errorf("撞别人的 id 的 body=%s —— 缺少稳定错误码 %q", s941TruncBody(body), CodeConflict)
	}

	// 负控：**别的** id 必须照常成功（证明上面那个 409 不是「谁都发不了」）。
	code, body = s941PostNote(t, srv, attackerToken, "s941-own", 16)
	if code != http.StatusCreated {
		t.Fatalf("攻击者用自己的 id ⇒ %d %s（期望 201）", code, s941TruncBody(body))
	}

	// 受害者的正文**不得**被改（这条是 §939 的归属守卫，§941 顺带钉住它没被带跑偏）。
	row, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-victim", s941Owner, s941Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil {
		t.Fatalf("受害者的笔记不见了 ⇒ 归属守卫把行删了")
	}
	if len(row.Content) != 16 {
		t.Errorf("受害者的正文长度=%d, want 16（被别人覆盖了）", len(row.Content))
	}
}

// TestS941QuickNoteIgnoresBodySuppliedOwnership body 里的归属字段必须被忽略。
//
// ⚠ D2 的行为判据：随手记链的落点**只认 JWT**。
//
//	notes.Note 的 JSON tag 是 userId / workspaceId ⇒ body **能**带上它们
//	⇒ ⇒ ⇒ 所以「不被忽略」是可观测的（不是空谈）。
func TestS941QuickNoteIgnoresBodySuppliedOwnership(t *testing.T) {
	srv, _ := newS939Server(t)
	token := s941Token(t, srv, s941Owner, s941Workspace)

	code, body := s941PostRaw(t, srv, token, map[string]any{
		"id":          "s941-own",
		"title":       "归属测试",
		"content":     "内容",
		"contentType": "text",
		"userId":      "someone-else",
		"workspaceId": s941OtherWs,
	})
	if code != http.StatusCreated {
		t.Fatalf("POST ⇒ %d %s（期望 201）", code, s941TruncBody(body))
	}

	row, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-own", s941Owner, s941Workspace)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil {
		t.Fatalf("调用方租户下查不到这条笔记 ⇒ 落点丢了租户（§939 D1 的形状）")
	}
	if row.WorkspaceID != s941Workspace {
		t.Errorf("workspace_id=%q, want %q（落点不认 JWT）", row.WorkspaceID, s941Workspace)
	}
	if row.UserID != s941Owner {
		t.Errorf("user_id=%q, want %q（落点不认 JWT）", row.UserID, s941Owner)
	}

	// 负控：在**别的**租户下查不到（证明上一条不是因为「到处都能查到」）。
	other, err := srv.notesStore.GetByIDScoped(context.Background(), "s941-own", s941Owner, s941OtherWs)
	if err != nil {
		t.Fatalf("GetByIDScoped(别的租户): %v", err)
	}
	if other != nil {
		t.Fatalf("别的租户下也查得到 ⇒ 落点根本没生效")
	}
}

// TestS941BothNoteChainsUseTheSameBroadcastTarget 两条链的广播目标必须
// 是**同一份**。
//
// ⚠ D2 的「口径一份」判据：随手记链（handleNotes POST）与会议精校链
//
//	（ingestMeetingNote）此前各自写了一份 BroadcastTarget
//	⇒ ⇒ ⇒ §939 证明过「抄两份 ⇒ 其中一份会退化」（D2 全体广播）。
//	⇒ ⇒ ⇒ 现在它们**从同一个函数**派生 ⇒ 物理上不可能再分叉。
//
// ⚠ 逐字段比对，而不是「两个值相等就算过」——
//
//	ws.BroadcastTarget 只有两个字段，比对本身就是逐字段 ✔
//	（若将来加了第三个字段，这条判据会自动跟着变严）。
func TestS941BothNoteChainsUseTheSameBroadcastTarget(t *testing.T) {
	quick := newNoteTarget(s941Owner, s941Workspace).broadcastTarget()
	meeting := newMeetingNoteTarget(s941Owner, s941Workspace, "mtg-s941").broadcastTarget()

	if quick.UserID != meeting.UserID {
		t.Errorf("两条链的广播 UserID 不同：随手记=%q 会议=%q", quick.UserID, meeting.UserID)
	}
	if quick.WorkspaceID != meeting.WorkspaceID {
		t.Errorf("两条链的广播 WorkspaceID 不同：随手记=%q 会议=%q",
			quick.WorkspaceID, meeting.WorkspaceID)
	}
	if quick.UserID == "" && quick.WorkspaceID == "" {
		t.Errorf("广播目标是零值 ⇒ hub 会退化成**全局广播**")
	}
}

// TestS941QuickNoteBroadcastReachesOnlyOwner 随手记链的广播只发给属主。
//
// ⚠ 两条臂缺一不可：
//
//	臂 A（属主必须收到）= **正控**：证明「定向广播这条路本身是通的」
//	  —— 否则臂 B 的「别人收不到」可能只是因为「谁都没收到」
//	  （量具坏了，而不是被测对象对了）。
//	臂 B（别人必须收不到）= 真正的判据。
func TestS941QuickNoteBroadcastReachesOnlyOwner(t *testing.T) {
	srv, _ := newS939Server(t)
	ownerToken := s941Token(t, srv, s941Owner, s941Workspace)
	otherToken := s941Token(t, srv, s941OtherUser, s941Workspace)

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

	code, body := s941PostNote(t, srv, ownerToken, "s941-broadcast", 16)
	if code != http.StatusCreated {
		t.Fatalf("POST ⇒ %d %s（期望 201）", code, s941TruncBody(body))
	}

	// 臂 A（正控）
	_, ownerGot := readS939WSMessage(ownerConn, "note.created", time.Now().Add(5*time.Second))
	if !ownerGot {
		t.Fatalf("属主没收到 note.created ⇒ 定向广播这条路本身坏了（量具坏了，不是被测对象对）")
	}

	// 臂 B：另一个用户**必须收不到**。
	_, otherGot := readS939WSMessage(otherConn, "note.created", time.Now().Add(1*time.Second))
	if otherGot {
		t.Fatalf("另一个用户收到了这条笔记 ⇒ 广播没有作用域（D2 的形状：全体广播）")
	}
}

// TestS941CrossWorkspaceBroadcastCarriesItsOwnWorkspace **现状钉住**：
// 同一个人**另一个租户**的设备会收到这条 note.created，而载荷里带着
// **这条笔记自己的** workspaceId。
//
// ⚠ 为什么这不是缺陷（阴性结论，详见主文档 §941.3）：
//
//	· 前端 notes-store.ts 的 handleServerEvent **按载荷里的 workspaceId 归档**
//	  ⇒ ⇒ ⇒ 另一个租户的设备收到后，**归档到那条笔记自己的租户**，
//	  不会误归档到当前租户 ⇒ 不是越权，也不是错位。
//	· 反过来，若给广播加上 WorkspaceID 过滤，就会**打断**
//	  「同一个人跨租户设备的笔记同步」⇒ ⇒ ⇒ 所以本节**刻意不加**那个过滤。
//
// ⚠ 这条判据的作用是**把决定钉住**：将来若产品口径变了（要断掉跨租户同步），
//
//	改的是**这一条**（有意地改），而不是无意中把它带跑偏。
func TestS941CrossWorkspaceBroadcastCarriesItsOwnWorkspace(t *testing.T) {
	srv, _ := newS939Server(t)
	ownerToken := s941Token(t, srv, s941Owner, s941Workspace)
	otherWsToken := s941Token(t, srv, s941Owner, s941OtherWs)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	otherWsConn := dialS939WS(t, ts, otherWsToken)

	deadline := time.Now().Add(5 * time.Second)
	for srv.wsHub.GetClientCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("WS 客户端没注册进 hub ⇒ 本测试失去判别力")
		}
		time.Sleep(10 * time.Millisecond)
	}

	code, body := s941PostNote(t, srv, ownerToken, "s941-crossws", 16)
	if code != http.StatusCreated {
		t.Fatalf("POST ⇒ %d %s（期望 201）", code, s941TruncBody(body))
	}

	raw, got := readS939WSMessage(otherWsConn, "note.created", time.Now().Add(5*time.Second))
	if !got {
		t.Fatalf("同一人另一个租户的设备**没有**收到 ⇒ 跨租户设备同步被断了（本节刻意不断）")
	}
	// 载荷里必须带着**这条笔记自己的**租户 ⇒ 客户端据此归档 ⇒ 不错位。
	//
	// ⚠ 量具坑：广播出去的是 WS **信封**（websocket/hub.go 的 Message）：
	//	{"type":"note.created","payload":{...}}
	//	⇒ ⇒ ⇒ workspaceId 在 **payload 里面**，不在顶层
	//	⇒ ⇒ ⇒ 把它当扁平对象解析 ⇒ workspaceId 恒为 ""（第一次写这条判据时
	//	就是这么写错的 ⇒ 量具坏了，不是被测对象坏了）。
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID          string `json:"id"`
			WorkspaceID string `json:"workspaceId"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("解析广播载荷失败: %v（raw=%s）", err, s941TruncBody(raw))
	}
	if envelope.Type != "note.created" {
		t.Fatalf("信封 type=%q, want note.created（raw=%s）", envelope.Type, s941TruncBody(raw))
	}
	if envelope.Payload.ID != "s941-crossws" {
		t.Errorf("信封 payload.id=%q, want s941-crossws（拿到的不是这条笔记的广播）",
			envelope.Payload.ID)
	}
	if envelope.Payload.WorkspaceID != s941Workspace {
		t.Errorf("广播载荷的 workspaceId=%q, want %q（载荷不带自己的租户 ⇒ 客户端会按默认租户归档 ⇒ 错位）",
			envelope.Payload.WorkspaceID, s941Workspace)
	}
}

// s941TruncBody 截断响应体，避免日志刷屏。
func s941TruncBody(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
