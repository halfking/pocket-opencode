package server

// §943 判据：「会议本体写入链」（POST /api/meetings）的**端到端**行为。
//
// ⚠⚠ 本节查出来两个缺陷（**修复前实测**，探针用完即删）：
//
//	D1（越权覆盖）：PG 版 `notes`… 不，`meeting.PGStore.CreateScoped` 的
//	   `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	   ⇒ 用户 B POST 用户 A 的会议 id ⇒ **201** ⇒ 那一行的
//	   title / transcript / summary / location **全被换成 B 的内容**
//	   （owner_id / workspace_id 不在 SET 列表里 ⇒ 那行仍属于 A）
//	   ⇒ ⇒ ⇒ **A 自己读回来看到的是 B 的正文** ⇒ 篡改**静默**发生，用户无感
//	   ⇒ ⇒ ⇒ 而且 B 的响应里带回了 **A 的 owner_id 与 workspace_id**
//	   （跨用户信息泄露）
//
//	D2（广播没有作用域）：`meeting.synced` 用 `wsHub.Broadcast`（**全体广播**）
//	   ⇒ 载荷里带着**别人的** `meetingId` 与 `userId`
//	   ⇒ ⇒ ⇒ 它就是 D1 的 **id 泄露通道**（实测：另一个用户确实收到了
//	   `{"meetingId":"...","userId":"..."}`）⇒ ⇒ ⇒⇒ 两条合起来就是一条
//	   **完整的跨用户篡改链**。
//
// ⚠ 判据量的是**HTTP 响应**与**落库之后的行**，不是源码文本。
//
// ⚠ 需要真 PG：生产走的是 PG 版（`cmd/pocketd` 注入 meeting.PGStore），
//   而**内存版**当时早就有护栏 ⇒ ⇒ ⇒ 只测内存版会漏掉生产行为
//   ⇒ ⇒ ⇒ 本文件的用例**一律**装 PG 版。
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

const (
	s943Owner = "user-s943-owner"
	s943Other = "user-s943-other"
	s943Ws    = "ws-s943"
)

// s943Server 造一个**带真 PG 会议库**的 Server。
//
// ⚠ 为什么必须显式注入 PG 版：默认注入的是内存版（`server.New` 的默认值），
// 而内存版**早就有**撞号护栏 ⇒ ⇒ ⇒ 判据跑在内存版上会**全绿**而生产是破的
// ⇒ ⇒ ⇒⇒ 这正是本节最贵的那个发现（「门禁跑在内存版上」）。
func s943Server(t *testing.T) *Server {
	t.Helper()
	pool := mustTestPool(t)
	store, err := meeting.NewPGStore(context.Background(), pool)
	if err != nil {
		t.Fatalf("meeting.NewPGStore: %v", err)
	}
	srv, _ := newTestServerWithAuth(t)
	srv.SetMeetingStore(store)
	return srv
}

func s943Token(t *testing.T, srv *Server, user, ws string) string {
	t.Helper()
	tok, err := srv.jwtSigner.SignWithWorkspace(user, "admin", ws)
	if err != nil {
		t.Fatalf("签 token: %v", err)
	}
	return tok
}

// s943PostMeeting POST 一场会议，返回状态码与响应体。
func s943PostMeeting(t *testing.T, srv *Server, token, id, title, body string) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"id": id, "title": title, "refinedTranscript": body,
		"summary": "摘要-" + title, "location": "地点-" + title,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/meetings", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

func s943GetMeeting(t *testing.T, srv *Server, token, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/meetings/"+url.PathEscape(id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// TestS943CreateMeetingCannotOverwriteAnotherUsersMeeting 跨用户撞 id ⇒ 不得覆盖。
//
// ⚠ 三条腿缺一不可：
//
//	① 受害者的行**一字未改**（标题 + 正文，读回来验，不是看源码）；
//	② 提交者**拿不到**受害者的那一行（他的作用域里没有它）；
//	③ 提交者**被换号**（他拿到的是自己的一行，而不是别人的那一行）。
//
// ⚠ 负控（同一用例内自带）：受害者**自己**用同一个 id 重发 ⇒ 仍然是幂等更新
//
//	⇒ ⇒⇒ 这一腿证明「守卫没有把正常路径也挡掉」。
func TestS943CreateMeetingCannotOverwriteAnotherUsersMeeting(t *testing.T) {
	srv := s943Server(t)
	ownerTok := s943Token(t, srv, s943Owner, s943Ws)
	otherTok := s943Token(t, srv, s943Other, s943Ws)

	const victimID = "meeting-s943-victim"
	const victimTitle = "受害者的会议"
	const victimBody = "受害者的正文：预算八十万。"

	code, body := s943PostMeeting(t, srv, ownerTok, victimID, victimTitle, victimBody)
	if code != http.StatusCreated {
		t.Fatalf("受害者建会应 201，实际 %d body=%s", code, strings.TrimSpace(body))
	}

	// 攻击者拿**同一个 id** 来写。
	code, body = s943PostMeeting(t, srv, otherTok, victimID, "攻击者的会议", "攻击者的正文")
	if code != http.StatusCreated {
		t.Fatalf("攻击者那一发应 201（换号，不是报错），实际 %d body=%s", code, strings.TrimSpace(body))
	}
	var attacker struct {
		ID          string `json:"id"`
		OwnerID     string `json:"owner_id"`
		WorkspaceID string `json:"workspace_id"`
		Title       string `json:"title"`
		Transcript  string `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(body), &attacker); err != nil {
		t.Fatalf("decode 攻击者响应: %v body=%s", err, strings.TrimSpace(body))
	}
	if attacker.ID == victimID {
		t.Fatalf("攻击者拿到了受害者的 id（%q）⇒ 那一行被他接管了", attacker.ID)
	}
	if attacker.OwnerID != s943Other {
		t.Errorf("攻击者拿到的行 owner_id=%q（want %q）⇒ 它没有落在自己名下", attacker.OwnerID, s943Other)
	}
	if attacker.OwnerID == s943Owner {
		t.Fatalf("响应里带回了受害者的 owner_id=%q ⇒ 跨用户信息泄露", attacker.OwnerID)
	}
	if attacker.Title != "攻击者的会议" || attacker.Transcript != "攻击者的正文" {
		t.Errorf("攻击者拿到的不是自己写的内容：title=%q transcript=%q", attacker.Title, attacker.Transcript)
	}

	// ① 受害者的行必须**一字未改**。
	code, body = s943GetMeeting(t, srv, ownerTok, victimID)
	if code != http.StatusOK {
		t.Fatalf("受害者读自己的会议应 200，实际 %d body=%s", code, strings.TrimSpace(body))
	}
	var victim struct {
		Title      string `json:"title"`
		Transcript string `json:"transcript"`
		Summary    string `json:"summary"`
		Location   string `json:"location"`
		OwnerID    string `json:"owner_id"`
	}
	if err := json.Unmarshal([]byte(body), &victim); err != nil {
		t.Fatalf("decode 受害者响应: %v body=%s", err, strings.TrimSpace(body))
	}
	if victim.Title != victimTitle {
		t.Errorf("受害者的标题变成了 %q（want %q）⇒ **越权覆盖**", victim.Title, victimTitle)
	}
	if victim.Transcript != victimBody {
		t.Errorf("受害者的正文变成了 %q（want %q）⇒ **越权覆盖**", victim.Transcript, victimBody)
	}
	if victim.Summary != "摘要-"+victimTitle {
		t.Errorf("受害者的摘要变成了 %q", victim.Summary)
	}
	if victim.Location != "地点-"+victimTitle {
		t.Errorf("受害者的地点变成了 %q", victim.Location)
	}
	if victim.OwnerID != s943Owner {
		t.Errorf("受害者的行 owner_id 变成了 %q", victim.OwnerID)
	}

	// ② 攻击者读不到受害者的那一行。
	code, _ = s943GetMeeting(t, srv, otherTok, victimID)
	if code != http.StatusNotFound {
		t.Errorf("攻击者读受害者的会议应 404，实际 %d ⇒ 归属守卫漏了", code)
	}

	// ③ 负控：受害者**自己**用同一个 id 重发 ⇒ 仍然是幂等更新（不是 409、不是换号）。
	code, body = s943PostMeeting(t, srv, ownerTok, victimID, "受害者的会议（改了标题）", victimBody)
	if code != http.StatusCreated {
		t.Fatalf("受害者同 id 重发应 201（幂等），实际 %d body=%s", code, strings.TrimSpace(body))
	}
	var again struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &again); err != nil {
		t.Fatalf("decode 幂等响应: %v", err)
	}
	if again.ID != victimID {
		t.Errorf("受害者同 id 重发被换了号（%q → %q）⇒ 守卫把正常路径也挡掉了", victimID, again.ID)
	}
}

// s943DialWS 连一条 WebSocket。
func s943DialWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("WS 拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// s943ReadWS 在 deadline 之前读帧，返回第一条 type == wantType 的消息。
func s943ReadWS(conn *websocket.Conn, wantType string, deadline time.Time) (string, bool) {
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

// TestS943MeetingSyncedBroadcastReachesOnlyOwner `meeting.synced` 只发给属主。
//
// ⚠ D2 的**行为**判据（不是源码形状判据）：修复前是 wsHub.Broadcast
// ⇒ **全体广播** ⇒ 每个在线用户的设备都会收到这条消息，
// 而载荷里带着**别人的** `meetingId` ⇒ ⇒ ⇒ 它是 D1 的 id 泄露通道。
//
// ⚠ 两条臂缺一不可：
//
//	臂 A（属主必须收到）= **正控**：证明「定向广播这条路本身是通的」
//	  —— 否则臂 B 的「别人收不到」可能只是因为「谁都没收到」
//	  （量具坏了，而不是被测对象对了）。
//	臂 B（别人必须收不到）= D2 的真判据。
func TestS943MeetingSyncedBroadcastReachesOnlyOwner(t *testing.T) {
	srv := s943Server(t)
	ownerTok := s943Token(t, srv, s943Owner, s943Ws)
	otherTok := s943Token(t, srv, s943Other, s943Ws)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ownerConn := s943DialWS(t, ts, ownerTok)
	otherConn := s943DialWS(t, ts, otherTok)

	// 注册是**异步**的（hub.Run 收 register channel）⇒ 必须等两个都在册。
	deadline := time.Now().Add(5 * time.Second)
	for srv.wsHub.GetClientCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("两个 WS 客户端没都注册进 hub（count=%d）⇒ 本测试失去判别力",
				srv.wsHub.GetClientCount())
		}
		time.Sleep(10 * time.Millisecond)
	}

	code, body := s943PostMeeting(t, srv, ownerTok, "meeting-s943-bcast", "属主的会", "属主的正文")
	if code != http.StatusCreated {
		t.Fatalf("属主建会应 201，实际 %d body=%s", code, strings.TrimSpace(body))
	}

	// 臂 A（正控）：属主必须收到。
	_, ownerGot := s943ReadWS(ownerConn, "meeting.synced", time.Now().Add(5*time.Second))
	if !ownerGot {
		t.Fatalf("属主没收到 meeting.synced ⇒ 定向广播这条路本身坏了（量具坏了，不是被测对象对）")
	}

	// 臂 B：另一个用户**必须收不到**。
	// ⚠ 宽限期取 1s：hub 在**同一个** Run 循环迭代里把消息塞进各客户端的
	//   send 队列 ⇒ 若走全体广播，两条连接是**同时**被投递的
	//   ⇒ 属主都收到了，1s 内对方不可能还没收到。
	raw, otherGot := s943ReadWS(otherConn, "meeting.synced", time.Now().Add(1*time.Second))
	if otherGot {
		t.Fatalf("另一个用户收到了别人的 meeting.synced ⇒ 载荷 %s ⇒ D2：全体广播把别人的会议 id 泄给了所有人",
			strings.TrimSpace(raw))
	}
}
