package server

// §942 判据：「笔记的**删除链**」（DELETE /api/notes/{id} 与「同 id 再写一次」）。
//
// ⚠ 为什么查这条链（它此前**一次都没查过**）：§939–§941 查的是「建笔记」那半条
//   （落点 / 广播 / 正文上限），而「**删笔记**」这半条 —— 全仓 0 条判据。
//   ⇒ ⇒ ⇒ 而它正好是属主那条要求（「成本只取决于存储的大小」）的**另一半**：
//   建的那半条查完了，**回收**的那半条没人管。
//
// ⚠ 修复前实测（§942 勘察时当场量的，探针用完即删）：
//
//	DELETE 不存在的 id  ⇒ **500** {"error":"note not found or already deleted"}
//	DELETE 别人的 id    ⇒ **500** 同上
//	POST → DELETE → 再 POST 同一个 id ⇒ 201
//	  ⇒ ListScoped 看得见的 = [别的笔记]   ⇒ **它不在任何读路径里**
//	  ⇒ 墓碑清单 = [它]                  ⇒ **墓碑还说它被删了**
//	  ⇒ ⇒ ⇒ ⇒ **哑弹**：最新正文在库里，但服务端看不见、客户端还会主动删掉它
//
// ⚠ 判据量的是**HTTP 响应**与**落库之后的行**，不是源码文本。
//
// ⚠ 需要真 PG：notes.Store 只有 PostgreSQL 一个实现 ⇒ 没有 PG 时用例会 skip
//   （退出码仍是 0）⇒ ⇒ 变异批次用 forbid_skip=True 把「被 skip」变成失败。

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
	s942Owner = "user-s942-owner"
	s942Other = "user-s942-other"
	s942Ws    = "ws-s942"
)

// s942Since 是墓碑清单的**水位线**。
//
// ⚠⚠ **陷阱**：`notes.Store.ListDeletedIDsScoped` 在 `sinceSec <= 0` 时
//
//	**直接返回空**（store.go 的早退）⇒ ⇒ ⇒ 用 since=0 去问墓碑，得到的
//	「空清单」是**量具错了**，不是产品行为
//	⇒ ⇒⇒ 本轮第一版探针就踩了这个 ⇒ ⇒⇒ 墓碑相关的断言**必须**用 since > 0。
var s942Since = time.Now().Unix() - 3600

func s942Token(t *testing.T, srv *Server, user, ws string) string {
	t.Helper()
	tok, err := srv.jwtSigner.SignWithWorkspace(user, "admin", ws)
	if err != nil {
		t.Fatalf("签 token: %v", err)
	}
	return tok
}

func s942PostNote(t *testing.T, srv *Server, token, id, content string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"id": id, "title": "n", "content": content, "contentType": "text",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

func s942DeleteNote(t *testing.T, srv *Server, token, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/notes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

func s942Tombstones(t *testing.T, srv *Server, user, ws string) []string {
	t.Helper()
	ids, err := srv.notesStore.ListDeletedIDsScoped(context.Background(), user, ws, s942Since, 500)
	if err != nil {
		t.Fatalf("ListDeletedIDsScoped: %v", err)
	}
	return ids
}

func s942Contains(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// TestS942DeletedNoteResurrectedBySameIdBecomesVisible 同 id 再写一次 ⇒ 它必须
// **回来**（可见 + 墓碑消失）。
//
// ⚠ D1 的行为判据：**不是**「SET 列表里有没有 deleted_at」这种源码形状，
//
//	而是「用户重写了一遍这条笔记，它能不能被看见」。
//
// ⚠ 修复前的形态（**哑弹**）：POST → DELETE → 再 POST ⇒ 201
//
//	⇒ 正文被更新了，但 `deleted_at` 还挂着
//	⇒ ⇒ 所有读路径都当它不存在，**而墓碑清单里仍然列着它**
//	⇒ ⇒ ⇒ 客户端会按墓碑把本地那份也删掉
//	⇒ ⇒ ⇒ **用户的最新正文在服务端库里，但服务端自己看不见**。
//
// ⚠ 三条腿缺一不可：
//
//	① **读路径看得见**（ListScoped / GetByIDScoped）
//	② **内容是最新那份**（不是旧的）
//	③ **墓碑里没有它了**（墓碑就是 deleted_at 那一列）
//	   —— 少 ③ 就是「复活了但别的设备还会把它删掉」。
//
// ⚠ 另有一腿是**前提**：DELETE 之后（再 POST 之前）墓碑里**必须**有它
//
//	⇒ ⇒ ⇒ 否则「墓碑清单本来就是空的」⇒ ⇒ ⇒ 这条判据就没有区分力
//	⇒ ⇒ ⇒ （★ 前提必须钉住，否则测的是一个恒空的东西）。
func TestS942DeletedNoteResurrectedBySameIdBecomesVisible(t *testing.T) {
	srv, _ := newS939Server(t)
	ctx := context.Background()
	token := s942Token(t, srv, s942Owner, s942Ws)

	code, body := s942PostNote(t, srv, token, "s942-revive", "第一版")
	if code != http.StatusCreated {
		t.Fatalf("第一次 POST ⇒ %d %s（期望 201）", code, body)
	}
	code, body = s942DeleteNote(t, srv, token, "s942-revive")
	if code != http.StatusOK {
		t.Fatalf("DELETE ⇒ %d %s（期望 200）", code, body)
	}

	// 前提：删掉之后，墓碑里**必须**有它（否则下面那条断言恒真）。
	tombs := s942Tombstones(t, srv, s942Owner, s942Ws)
	if !s942Contains(tombs, "s942-revive") {
		t.Fatalf("删掉之后墓碑清单里没有它 = %v ⇒ **前提不成立**（本判据失去区分力）", tombs)
	}
	// 前提：删掉之后，读路径看不见它。
	row, err := srv.notesStore.GetByIDScoped(ctx, "s942-revive", s942Owner, s942Ws)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row != nil {
		t.Fatalf("删掉之后读路径还能看见它 ⇒ 前提不成立（软删没生效？）")
	}

	// 同 id 再写一次 ⇒ 它必须**回来**。
	code, body = s942PostNote(t, srv, token, "s942-revive", "第二版")
	if code != http.StatusCreated {
		t.Fatalf("再 POST 同一个 id ⇒ %d %s（期望 201）", code, body)
	}

	// 腿 ①：读路径看得见。
	rows, err := srv.notesStore.ListScoped(ctx, s942Owner, s942Ws, "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == "s942-revive" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListScoped 看不见 s942-revive（D1：**哑弹** —— 正文在库里但读路径当它不存在）：%v", rows)
	}
	row, err = srv.notesStore.GetByIDScoped(ctx, "s942-revive", s942Owner, s942Ws)
	if err != nil || row == nil {
		t.Fatalf("GetByIDScoped ⇒ row=%v err=%v（D1：复活后必须能被单独取到）", row, err)
	}
	// 腿 ②：内容是**最新**那份。
	if row.Content != "第二版" {
		t.Errorf("复活后的正文=%q, want %q（内容没更新？）", row.Content, "第二版")
	}
	// 腿 ③：墓碑里没有它了。
	tombs = s942Tombstones(t, srv, s942Owner, s942Ws)
	if s942Contains(tombs, "s942-revive") {
		t.Errorf("复活后墓碑清单里**仍然**有它 = %v ⇒ 别的设备会按墓碑把它删掉", tombs)
	}
}

// TestS942DeleteMissingNoteIsNotFound 「不存在 / 不是我的」必须回 **404**。
//
// ⚠ D2：修复前**所有**删除错误都回 500（含「不存在」）⇒ ⇒ ⇒ 客户端以为
//
//	「重试有用」，而它永远重试都不会成功。
//	⇒ ⇒ ⇒ 而同一批接口的 GET / PUT（以及**会议链**的 DELETE）都回 404
//	⇒ ⇒ ⇒ ⇒ 形态分叉。
//
// ⚠ 三条臂：
//
//	臂 1（不存在的 id）= 判据
//	臂 2（**别人**的 id）= 判据（顺带证明「不是我的」也是 404，不是 403）
//	臂 3（**已经删过**的 id）= 判据（同一类）
//	⇒ ⇒ ⇒ **负控**：自己的笔记删得掉 ⇒ 200 + {"deleted":true}
func TestS942DeleteMissingNoteIsNotFound(t *testing.T) {
	srv, _ := newS939Server(t)
	ctx := context.Background()
	ownerToken := s942Token(t, srv, s942Owner, s942Ws)
	otherToken := s942Token(t, srv, s942Other, s942Ws)

	// 负控：自己的笔记删得掉。
	code, body := s942PostNote(t, srv, ownerToken, "s942-mine", "内容")
	if code != http.StatusCreated {
		t.Fatalf("POST ⇒ %d %s（期望 201）", code, body)
	}
	code, body = s942DeleteNote(t, srv, ownerToken, "s942-mine")
	if code != http.StatusOK {
		t.Fatalf("DELETE 自己的笔记 ⇒ %d %s（期望 200）", code, body)
	}
	if !strings.Contains(body, `"deleted":true`) {
		t.Errorf("DELETE 自己的笔记的 body=%s（期望含 deleted:true）", body)
	}

	// 臂 1：不存在。
	code, body = s942DeleteNote(t, srv, ownerToken, "s942-does-not-exist")
	if code != http.StatusNotFound {
		t.Errorf("DELETE 不存在的 id ⇒ %d %s —— 期望 **404**（500 会让客户端以为「重试有用」）", code, body)
	}
	if strings.Contains(body, "already deleted") {
		t.Errorf("404 的 body 泄露了内部错误串：%s", body)
	}

	// 臂 2：别人的。
	code, _ = s942PostNote(t, srv, ownerToken, "s942-victim", "受害者的正文")
	if code != http.StatusCreated {
		t.Fatalf("受害者 POST ⇒ %d（期望 201）", code)
	}
	code, body = s942DeleteNote(t, srv, otherToken, "s942-victim")
	if code != http.StatusNotFound {
		t.Errorf("DELETE 别人的 id ⇒ %d %s —— 期望 **404**", code, body)
	}
	// 负控：受害者的笔记**必须还在**（⇒ 归属守卫没被带跑偏）。
	row, err := srv.notesStore.GetByIDScoped(ctx, "s942-victim", s942Owner, s942Ws)
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if row == nil || row.Content != "受害者的正文" {
		t.Errorf("受害者的笔记被改了或没了：row=%v（别人的 DELETE 必须一行都动不了）", row)
	}

	// 臂 3：已经删过的。
	code, _ = s942PostNote(t, srv, ownerToken, "s942-twice", "x")
	if code != http.StatusCreated {
		t.Fatalf("POST ⇒ %d（期望 201）", code)
	}
	if code, _ = s942DeleteNote(t, srv, ownerToken, "s942-twice"); code != http.StatusOK {
		t.Fatalf("第一次 DELETE ⇒ %d（期望 200）", code)
	}
	code, body = s942DeleteNote(t, srv, ownerToken, "s942-twice")
	if code != http.StatusNotFound {
		t.Errorf("DELETE 已经删过的 id ⇒ %d %s —— 期望 **404**", code, body)
	}
}
