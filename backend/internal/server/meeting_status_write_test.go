package server

// meeting_status_write_test.go — §945 判据：会议**状态写失败**必须让用户看见。
//
// # 这条判据在量什么
//
//	§945 把 6 处 `_ = s.meetingStore.UpdateScoped(...)` 收成两个 helper：
//
//	① `markMeetingProgress`（进度标记："transcribing" / "summarizing"）
//	   ⇒ 写失败**不阻断**本次请求（后面还有终态写；阻断会把一次成功的
//	     转写/摘要变成失败）⇒ 但必须**留痕**。
//	② `setMeetingStatus`（终态："failed"）
//	   ⇒ 写失败**必须让用户看见**（500 + **可区分**的文本）。
//
//	⚠ 为什么终态写失败必须可见（这是 §945 最重的一条）：
//	失败的那一行会**永远停在中间状态**（例如 "summarizing"）
//	⇒ ⇒⇒ 而那个状态**会被增量同步到别的设备**（§939 的同步链）
//	⇒ ⇒⇒⇒ 用户在**所有设备**上都看到「总结中」，而且**永远不会自己结束**
//	⇒ ⇒⇒⇒ 而 HTTP 层此刻会回「摘要失败」⇒ ⇒⇒⇒ **两处状态互相矛盾**
//	⇒ ⇒⇒⇒ ⇒ 「失败必须可区分」在这里的含义是：错误文本要说出
//	⇒ ⇒⇒⇒ ⇒ **真正卡住的地方**，而不是笼统的「失败了」。
//
// # 为什么用**转写**链来验（而不是摘要链）
//
//	⚠ 摘要链的失败分支**今天不可达**：`handleSummarizeMeeting` 在调用
//	  `meeting.SummarizeTranscript` **之前**就判了
//	  `if strings.TrimSpace(m.Transcript) == ""` ⇒ ⇒⇒ 而
//	  `SummarizeTranscript` **唯一**的失败原因就是「transcript 为空」
//	  ⇒ ⇒⇒⇒ ⇒ 所以「摘要失败」这条分支**永远走不到**
//	  ⇒ ⇒⇒⇒ ⇒ ⇒ ⇒ 这条事实已登记在 §945 文档里（**防御性代码，不可达**）。
//	⇒ ⇒⇒ 而转写链的「空音频」分支**是可达的**（POST 空 body）。
//
//	⚠ 转写端点**仓内 0 调用方**（§937 已登记）⇒ ⇒⇒ 它是**隐患不是现症**
//	  ⇒ ⇒⇒ 本判据量的是**实现的失败语义**，不是端点的调用量。
//
// # 三条腿（缺一条就退化成「门绿 ≠ 量具有牙」）
//
//	① 终态写失败 ⇒ **500** 且文本与「音频为空」**可区分**；
//	② 进度写失败 ⇒ **不阻断**（仍然走到正常的 400）；
//	③ 负控：不注入失败 ⇒ 同样是 400 ⇒ 证明 ② 的「没阻断」不是因为
//	   「本来就会 400」。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// s945FailingMeetingStore 在某些状态上让 UpdateScoped 失败。
//
// ⚠ 用**嵌入接口**而不是逐个方法转发：接口以后加方法时，
//
//	这个包装器**自动**跟上 ⇒ ⇒⇒ 不会因为「少实现了一个方法」而编译不过。
type s945FailingMeetingStore struct {
	meeting.MeetingStore
	// failStatuses 是「这些状态写下去会失败」的集合。
	failStatuses map[string]bool
}

func (s *s945FailingMeetingStore) UpdateScoped(m *meeting.Meeting, ownerID, workspaceID string) error {
	if s.failStatuses[m.Status] {
		return errors.New("s945 injected status write failure")
	}
	return s.MeetingStore.UpdateScoped(m, ownerID, workspaceID)
}

// s945StatusServer 造一个带故障注入的 server（返回 server 与内层真 store）。
func s945StatusServer(t *testing.T, failStatuses map[string]bool) *Server {
	t.Helper()
	pool := mustTestPool(t)
	store, err := meeting.NewPGStore(context.Background(), pool)
	if err != nil {
		t.Fatalf("meeting.NewPGStore: %v", err)
	}
	srv, _ := newTestServerWithAuth(t)
	srv.SetMeetingStore(&s945FailingMeetingStore{MeetingStore: store, failStatuses: failStatuses})
	return srv
}

// s945PostTranscribe POST /api/meetings/{id}/transcribe（body 可为空）。
func s945PostTranscribe(t *testing.T, srv *Server, token, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/meetings/"+id+"/transcribe", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// TestS945MeetingTerminalStatusWriteFailureIsVisible 终态写失败 ⇒ 500 + 可区分文本。
func TestS945MeetingTerminalStatusWriteFailureIsVisible(t *testing.T) {
	srv := s945StatusServer(t, map[string]bool{"failed": true})
	tok := s943Token(t, srv, "s945-owner", "s945-ws")

	code, body := s943PostMeeting(t, srv, tok, "s945-mtg-1", "标题", "正文")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("建会议失败：%d %s", code, body)
	}

	code, body = s945PostTranscribe(t, srv, tok, "s945-mtg-1")

	// ① 状态码：必须 500（**不是** 400）。
	//    ⚠ 为什么不是 400：400 说的是「你的请求不对」⇒ ⇒⇒ 而实际是
	//    「服务端没写进去」⇒ ⇒⇒ ⇒ 两者的处置完全不同（400 客户端会改请求，
	//    500 客户端会重试/告警）⇒ ⇒⇒ ⇒ 「失败必须可区分」。
	if code != http.StatusInternalServerError {
		t.Errorf("终态写失败时的状态码 = %d（期望 500）⇒ 错误没被暴露出来。body=%s", code, body)
	}
	// ② 文本：必须说出「真正卡住的地方」。
	if !strings.Contains(body, "could not be persisted") {
		t.Errorf("终态写失败时的响应体没有「状态没写回去」的可区分文本：%s", body)
	}
	// ③ **可区分**，且**不丢原始原因**。
	//
	//	⚠ 我第一版写的是「不许出现 empty audio data」⇒ ⇒⇒ **实测打脸**：
	//	  真实响应是 `"empty audio data, and the meeting status could not be
	//	  persisted (it may stay in progress)"` ⇒ ⇒⇒ 它**同时**说了两件事
	//	  ⇒ ⇒⇒⇒ 那才是对的形态：① 原始原因**不丢**（客户端仍知道音频是空的）
	//	  ⇒ ⇒⇒⇒ ② 新增的失败点**可区分**（客户端知道状态没写回去）
	//	  ⇒ ⇒⇒⇒ ⇒ 所以判据改成「**原文必须在**」+「**必须与基线不同**」。
	if !strings.Contains(body, "empty audio data") {
		t.Errorf("终态写失败时的响应体**丢掉了原始原因**（empty audio data）⇒ 客户端会不知道到底发生了什么：%s", body)
	}
	baseline := s945BaselineEmptyAudioBody(t)
	if body == baseline {
		t.Errorf("终态写失败时的响应体与「音频为空」的基线**逐字相同**⇒ 两种失败**不可区分**：%s", body)
	}
}

// TestS945MeetingProgressStatusWriteFailureDoesNotBlock 进度写失败 ⇒ 不阻断。
//
// ⚠ 期望消息按**实测**写：转写链在音频为空时回的是 400 + "empty audio data"。
func TestS945MeetingProgressStatusWriteFailureDoesNotBlock(t *testing.T) {
	srv := s945StatusServer(t, map[string]bool{"transcribing": true})
	tok := s943Token(t, srv, "s945-owner", "s945-ws")

	code, body := s943PostMeeting(t, srv, tok, "s945-mtg-2", "标题", "正文")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("建会议失败：%d %s", code, body)
	}

	code, body = s945PostTranscribe(t, srv, tok, "s945-mtg-2")

	// 进度写失败**不许**改变本次请求的结局 ⇒ ⇒⇒ 仍然是「音频为空」的 400。
	if code != http.StatusBadRequest {
		t.Errorf("进度写失败时的状态码 = %d（期望 400，即**照常**走到「音频为空」那一步）⇒ 进度写失败把请求**阻断**了。body=%s", code, body)
	}
	if !strings.Contains(body, "empty audio data") {
		t.Errorf("进度写失败时的响应体不是「empty audio data」⇒ 说明请求被提前打断：%s", body)
	}
}

// s945BaselineEmptyAudioBody 取「音频为空」那条基线响应体（用于可区分性比较）。
func s945BaselineEmptyAudioBody(t *testing.T) string {
	t.Helper()
	srv := s945StatusServer(t, nil)
	tok := s943Token(t, srv, "s945-owner", "s945-ws")
	code, body := s943PostMeeting(t, srv, tok, "s945-mtg-baseline", "标题", "正文")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("建会议失败：%d %s", code, body)
	}
	_, body = s945PostTranscribe(t, srv, tok, "s945-mtg-baseline")
	return body
}

// TestS945MeetingStatusWriteBaseline 负控：不注入失败 ⇒ 也是 400 + empty audio data。
//
// ⚠ 为什么必须有这条：上一条断言「进度写失败 ⇒ 400 empty audio data」
//
//	⇒ 如果**没有**这条负控，那么「不阻断」和「本来就会 400」是**分不开**的
//	⇒ ⇒⇒ ⇒ 断言就没有证据强度 ⇒ ⇒⇒ ⇒ 这是「负控 = 让错误实现产生独有症状」。
func TestS945MeetingStatusWriteBaseline(t *testing.T) {
	srv := s945StatusServer(t, nil)
	tok := s943Token(t, srv, "s945-owner", "s945-ws")

	code, body := s943PostMeeting(t, srv, tok, "s945-mtg-3", "标题", "正文")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("建会议失败：%d %s", code, body)
	}

	code, body = s945PostTranscribe(t, srv, tok, "s945-mtg-3")
	if code != http.StatusBadRequest {
		t.Errorf("基线状态码 = %d（期望 400）body=%s", code, body)
	}
	if !strings.Contains(body, "empty audio data") {
		t.Errorf("基线响应体不是「empty audio data」：%s", body)
	}
}
