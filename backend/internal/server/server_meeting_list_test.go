// server_meeting_list_test.go —— §936：GET /api/meetings 的 handler 级判据。
//
// 为什么要在 handler 层再钉一遍（meeting 包的 list_test.go 已经钉了口径）：
//
//	① **旧实现下这条必红** ⇒ 它是「D1 真的被修掉了」的证据，不是新口径的自证。
//	   （旧实现：total = len(被 LIMIT 截断后的列表) ⇒ `?limit=2` 时 total=2，
//	     而作用域里其实有 3 场 ⇒ 期望 3 ⇒ 红。）
//	② handler 层是**客户端真正看到的东西** ⇒ 口径写在 store 层、但
//	   **兑现**在 handler 层 ⇒ 中间任何一层忘了把 total 传出去，这里就红。
//
// ⚠ 本文件**不依赖 PG**：handler 默认用内存版 store ⇒ 任何环境都跑。
// ⚠ 跨 store 一致性在 meeting/list_test.go（需要真 PG，无 PG 时 skip）。
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// s936List 走真实 HTTP 读列表，返回 (行, total, 原始 body)。
func s936List(t *testing.T, h http.Handler, token, query string) ([]meeting.Meeting, int, string) {
	t.Helper()
	rr := serveWorkspaceJSON(t, h, http.MethodGet, "/api/meetings"+query, token, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/meetings%s 应 200，实际 %d body=%s",
			query, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	var resp struct {
		Meetings []meeting.Meeting `json:"meetings"`
		Total    int               `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list body: %v body=%s", err, strings.TrimSpace(rr.Body.String()))
	}
	return resp.Meetings, resp.Total, rr.Body.String()
}

// s936Create 建一场会议（走真实 POST，与生产同一条路）。
func s936Create(t *testing.T, h http.Handler, token, title string) {
	t.Helper()
	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", token, `{"title":"`+title+`"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("创建会议应 201，实际 %d body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
}

// TestS936MeetingListTotalIsScopeTotal 钉住 D1：
// **total 是作用域内的总条数，不是本页行数**。
//
// ⚠ 旧实现下 `?limit=2` 会返回 total=2 ⇒ 这条断言红 ⇒ 它是「D1 被修掉」的证据。
func TestS936MeetingListTotalIsScopeTotal(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()
	tok := tokens["ws-a"]

	for i := range 3 {
		s936Create(t, h, tok, "会议"+string(rune('A'+i)))
	}

	// 不带参数 ⇒ 全部返回。
	rows, total, _ := s936List(t, h, tok, "")
	if len(rows) != 3 || total != 3 {
		t.Fatalf("全量列表应是 3 行 / total=3，实际 %d 行 / total=%d", len(rows), total)
	}

	// ★ D1 的核心断言：限了条数，**total 不跟着变**。
	rows, total, _ = s936List(t, h, tok, "?limit=2")
	if len(rows) != 2 {
		t.Fatalf("?limit=2 应返回 2 行，实际 %d 行", len(rows))
	}
	if total != 3 {
		t.Fatalf("?limit=2 时 total 应仍是 3（作用域内的总条数），实际 %d ⇒ "+
			"§936 的 D1 仍在（total = 本页行数）", total)
	}
	// ★ 前提自证：旧实现下 total 会等于本页行数 ⇒ 上面的断言**不是恒真**。
	if total == len(rows) {
		t.Fatalf("前提不成立：total(%d) 与本页行数(%d) 相同 ⇒ 用例量不到「total≠本页行数」",
			total, len(rows))
	}

	// 翻页：offset 必须真的跳过。
	rows, total, _ = s936List(t, h, tok, "?limit=2&offset=2")
	if len(rows) != 1 {
		t.Fatalf("?limit=2&offset=2 应返回 1 行，实际 %d 行", len(rows))
	}
	if total != 3 {
		t.Fatalf("翻页时 total 应仍是 3，实际 %d", total)
	}

	// offset 越过末尾 ⇒ 空页，但 total 仍是 3。
	rows, total, _ = s936List(t, h, tok, "?limit=2&offset=10")
	if len(rows) != 0 {
		t.Fatalf("offset 越过末尾应返回空页，实际 %d 行", len(rows))
	}
	if total != 3 {
		t.Fatalf("空页时 total 应仍是 3，实际 %d", total)
	}
}

// TestS936MeetingListSinceKeepsScopeTotal 钉住 since 的行为与 total 的关系。
//
// ⚠ 口径：total 是「**过滤后**、分页前」的条数 ⇒
//
//	带 since 时它是「这一批有多少条变了」，不带 since 时是「作用域内共多少场」。
//	⇒ ⇒ 分页器（不带 since）拿到的仍是「共 N 场」⇒ 注释里承诺的
//	「一个来回渲染分页」这次真的能兑现。
func TestS936MeetingListSinceKeepsScopeTotal(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()
	tok := tokens["ws-a"]

	for i := range 3 {
		s936Create(t, h, tok, "会议"+string(rune('A'+i)))
	}

	past := time.Now().Add(-time.Hour).UnixMilli()
	future := time.Now().Add(time.Hour).UnixMilli()
	pastQ := "?since=" + strconv.FormatInt(past, 10)
	futureQ := "?since=" + strconv.FormatInt(future, 10)

	// 水位线在过去 ⇒ 全部命中。
	rows, total, _ := s936List(t, h, tok, pastQ)
	if len(rows) != 3 || total != 3 {
		t.Fatalf("水位线在过去应 3 行 / total=3，实际 %d 行 / total=%d", len(rows), total)
	}

	// 水位线在未来 ⇒ 一条都没有。
	rows, total, _ = s936List(t, h, tok, futureQ)
	if len(rows) != 0 || total != 0 {
		t.Fatalf("水位线在未来应 0 行 / total=0，实际 %d 行 / total=%d", len(rows), total)
	}

	// 增量 + 分页：total 是**这一批**的条数，不是本页行数。
	rows, total, _ = s936List(t, h, tok, pastQ+"&limit=1")
	if len(rows) != 1 {
		t.Fatalf("增量 + limit=1 应返回 1 行，实际 %d 行", len(rows))
	}
	if total != 3 {
		t.Fatalf("增量 + limit=1 时 total 应是这一批的 3，实际 %d", total)
	}
}

// TestS936MeetingListClampsBadParams 钉住「非法参数不炸库」。
//
// ⚠ 分页是**可选增强** ⇒ 客户端把 limit 写成 "abc" / "-1" 是手滑，
// 不是攻击 ⇒ ⇒ 夹取（而不是 400 / 500）才是合适的姿态。
// ⚠ 真正的**上限**在 meeting 包的 NormalizeListOptions 里（那里另有判据）；
// 这里守的是「handler 这一层不因参数非法而失败」。
func TestS936MeetingListClampsBadParams(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()
	tok := tokens["ws-a"]

	for i := range 3 {
		s936Create(t, h, tok, "会议"+string(rune('A'+i)))
	}

	for _, q := range []string{
		"?limit=abc",
		"?limit=-1",
		"?limit=0",
		"?limit=999999",
		"?offset=-5",
		"?offset=abc",
		"?since=abc",
		"?since=0",
		"?since=-1",
		"?limit=abc&offset=xyz&since=zzz",
	} {
		rows, total, body := s936List(t, h, tok, q)
		if len(rows) != 3 {
			t.Fatalf("%q 应仍返回全部 3 行，实际 %d 行 body=%s", q, len(rows), body)
		}
		if total != 3 {
			t.Fatalf("%q 的 total 应仍是 3，实际 %d body=%s", q, total, body)
		}
	}
}

// TestS936MeetingListTotalIsWorkspaceScoped 守住 total 的作用域。
//
// ⚠ 为什么必须单独守：total 来自一条**独立的 COUNT 查询**（§936 新增）
// ⇒ ⇒ 那条查询的 WHERE 少写一个字段就会**跨作用域串数**
// ⇒ ⇒ 而串数在响应里**看不出来**（只有数字变大）⇒ 必须钉。
func TestS936MeetingListTotalIsWorkspaceScoped(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()

	s936Create(t, h, tokens["ws-a"], "A 的会")
	s936Create(t, h, tokens["ws-a"], "A 的会 2")
	s936Create(t, h, tokens["ws-b"], "B 的会")

	rowsA, totalA, _ := s936List(t, h, tokens["ws-a"], "")
	if len(rowsA) != 2 || totalA != 2 {
		t.Fatalf("ws-a 应看到 2 行 / total=2，实际 %d 行 / total=%d", len(rowsA), totalA)
	}
	rowsB, totalB, _ := s936List(t, h, tokens["ws-b"], "")
	if len(rowsB) != 1 || totalB != 1 {
		t.Fatalf("ws-b 应看到 1 行 / total=1，实际 %d 行 / total=%d", len(rowsB), totalB)
	}
	// 翻页也不能把别人的会议算进 total。
	_, totalA2, _ := s936List(t, h, tokens["ws-a"], "?limit=1")
	if totalA2 != 2 {
		t.Fatalf("ws-a 翻页时 total 应仍是 2，实际 %d ⇒ COUNT 漏了作用域过滤", totalA2)
	}
}
