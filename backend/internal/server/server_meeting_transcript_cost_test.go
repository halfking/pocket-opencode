// server_meeting_transcript_cost_test.go —— §937：服务端会议链的「每次操作成本」
// 判据。
//
// 本文件守的是**两条**与「会议能开多长」直接相关的不变式：
//
//	D1（读放大）：/api/meetings/{id}/analyze 的**轮询**每轮只能把「这次新增的
//	  那一段」搬进进程 ⇒ ⇒ 每轮搬运量 ∝ 新增量，而不是 ∝ 全文
//	  ⇒ ⇒ 累计搬运量从 ∝ 时长² 降到 ∝ 时长。
//
//	D2（写放大 + 丢失更新窗口）：只改 title/status 的两个写入点
//	  （精校收尾、幂等同步）**不得重写 transcript 列**
//	  ⇒ ⇒ 写放大 ∝ 时长² 被去掉，且并发写入的新转写不会被覆盖回旧值。
//
// ⚠ 为什么用**计数包装器**而不是量时间：时间读数受机器负载影响（本仓反复
//
//	踩过「性能结论必须给噪声带」），而「搬走了多少字节」是**确定性**的量
//	⇒ ⇒ 判据钉的是量，不是钟。
//
// ⚠ 为什么钉「**相对量**」：第 1 轮轮询**必然**要读全文（模型得先看到全文）
//
//	⇒ ⇒ 绝对值随语料长度变 ⇒ ⇒ 断言落在「第 2 轮的增量 ≪ 全文」这个**比值**上。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// s937CountingStore 记录「每次从 store 取回/写回多少**转写字节**」的包装器。
//
// ⚠ 它包的是**接口**（meeting.MeetingStore）⇒ ⇒ 两个实现（内存/PG）都能装它
//
//	⇒ ⇒ 判据量的是「跨存储边界的字节数」，与实现无关。
type s937CountingStore struct {
	meeting.MeetingStore
	readBytes  atomic.Int64
	writeBytes atomic.Int64
	getCalls   atomic.Int64
	tailCalls  atomic.Int64
	updateCall atomic.Int64
	metaCall   atomic.Int64
}

func (c *s937CountingStore) GetScoped(id, ownerID, workspaceID string) (*meeting.Meeting, error) {
	c.getCalls.Add(1)
	m, err := c.MeetingStore.GetScoped(id, ownerID, workspaceID)
	if m != nil {
		c.readBytes.Add(int64(len(m.Transcript)))
	}
	return m, err
}

func (c *s937CountingStore) TranscriptTailScoped(id, ownerID, workspaceID string, fromRune int) (string, int, error) {
	c.tailCalls.Add(1)
	tail, total, err := c.MeetingStore.TranscriptTailScoped(id, ownerID, workspaceID, fromRune)
	c.readBytes.Add(int64(len(tail)))
	return tail, total, err
}

func (c *s937CountingStore) UpdateScoped(m *meeting.Meeting, ownerID, workspaceID string) error {
	c.updateCall.Add(1)
	if m != nil {
		c.writeBytes.Add(int64(len(m.Transcript)))
	}
	return c.MeetingStore.UpdateScoped(m, ownerID, workspaceID)
}

func (c *s937CountingStore) UpdateMetaScoped(id, ownerID, workspaceID string, title, status *string) (time.Time, error) {
	c.metaCall.Add(1)
	return c.MeetingStore.UpdateMetaScoped(id, ownerID, workspaceID, title, status)
}

// s937LongTranscript 合成一场「1 小时量级」的转写。
//
// ⚠ 语料按 §901 实测的 271 字/分钟（1 小时 ≈ 16,260 字）合成 ⇒
//
//	⇒ 数字量级与真实长会议同阶，不是玩具数据。
func s937LongTranscript() string {
	return strings.Repeat("张三: 讨论二季度预算与排班系统的对接细节。", 600)
}

// TestS937AnalyzePollMovesOnlyNewBytes 钉住 D1：轮询的**第 2 轮**只能搬新增段。
//
// ⚠ 变异（把 handler 退回「整读 + Go 里切」）会让本条**红在字节数**上
//
//	⇒ ⇒ 它验的就是「读放大」这件事本身，不是「handler 调了哪个方法」。
func TestS937AnalyzePollMovesOnlyNewBytes(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/analyze" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"summary":"合并后的全文摘要","key_points":["k"],"decisions":[],"action_items":[],"open_questions":[],"hints":[],"topics":[]}`))
	}))
	t.Cleanup(gw.Close)

	srv, tokens := newWorkspaceIsolationServer(t)
	t.Cleanup(func() { meetingAnalyzeGateway = nil })
	meetingAnalyzeGateway = func(_ *Server, _, _ string) (*llmgateway.Client, bool) {
		return llmgateway.NewClient(gw.URL, "sk-test"), true
	}

	counter := &s937CountingStore{MeetingStore: srv.meetingStore}
	srv.meetingStore = counter
	h := srv.Handler()

	long := s937LongTranscript()
	m, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{
		Title: "长会议", RefinedTranscript: long,
	}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("create meeting: %v", err)
	}

	// 第 1 轮：全量（首轮必须看到全文，这是**应该**的）。
	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("first analyze status=%d body=%s", rr.Code, rr.Body.String())
	}
	firstRead := counter.readBytes.Load()
	if firstRead < int64(len(long))/2 {
		t.Fatalf("第 1 轮应读全文（模型得先看到全文），实际只读了 %d 字节（全文 %d）", firstRead, len(long))
	}

	// 追加 50 个字（≈150 字节）。
	added := strings.Repeat("李四: 排班下周定。", 5)
	live, err := srv.meetingStore.GetScoped(m.ID, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	live.Transcript = long + added
	if err := srv.meetingStore.UpdateScoped(live, "shared-user", "ws-a"); err != nil {
		t.Fatalf("append: %v", err)
	}
	// ⚠ 清掉「追加」这一次写入的记账 ⇒ 下面只量**轮询**读了多少。
	counter.writeBytes.Store(0)

	before := counter.readBytes.Load()
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("second analyze status=%d body=%s", rr.Code, rr.Body.String())
	}
	delta := counter.readBytes.Load() - before

	// ⭐ 核心断言：第 2 轮只搬「新增的那一段」。
	if delta > int64(len(added))+512 {
		t.Fatalf("第 2 轮轮询搬了 %d 字节，新增段只有 %d 字节 ⇒ 仍在整读（D1 没修掉）",
			delta, len(added))
	}
	// ⭐ 相对量：远小于全文（留足余量，同时对「整读」这个形态有区分力）。
	if delta > int64(len(long))/4 {
		t.Fatalf("第 2 轮搬了 %d 字节 = 全文的 1/4 以上（全文 %d）⇒ 读放大仍在", delta, len(long))
	}
	t.Logf("§937 D1 实测：第 1 轮 %d 字节 → 第 2 轮 %d 字节（新增 %d 字节）", firstRead, delta, len(added))
}

// TestS937RefineFinalizeDoesNotRewriteTranscript 钉住 D2 的**精校收尾**一半：
// 只改 title/status ⇒ 不得重写转写。
//
// ⚠ 这条同时钉住「**不覆盖**并发写入的新转写」：收尾写的是 title/status，
//
//	转写必须**一字不动**。
func TestS937RefineFinalizeDoesNotRewriteTranscript(t *testing.T) {
	srv, _ := newWorkspaceIsolationServer(t)
	counter := &s937CountingStore{MeetingStore: srv.meetingStore}
	srv.meetingStore = counter

	long := s937LongTranscript()
	m, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{
		Title: "原标题", RefinedTranscript: long,
	}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	counter.writeBytes.Store(0)
	counter.readBytes.Store(0)

	result := map[string]any{"refined_transcript": "精校稿"}
	srv.finalizeMeetingRefine(context.Background(), "shared-user", "ws-a", m.ID, result,
		meetingMetaIn{Title: "新标题"})

	if got := counter.writeBytes.Load(); got != 0 {
		t.Fatalf("精校收尾写回了 %d 字节转写（它只该改 title/status）⇒ 写放大仍在（D2 没修掉）", got)
	}
	if counter.updateCall.Load() != 0 {
		t.Fatalf("精校收尾走了 UpdateScoped（整行写）%d 次 ⇒ 应走定向更新", counter.updateCall.Load())
	}
	if counter.metaCall.Load() != 1 {
		t.Fatalf("精校收尾应走 1 次定向更新，实际 %d 次", counter.metaCall.Load())
	}

	after, err := srv.meetingStore.GetScoped(m.ID, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Title != "新标题" {
		t.Fatalf("标题没改：%q", after.Title)
	}
	if after.Status != "refined" {
		t.Fatalf("状态没改：%q", after.Status)
	}
	if after.Transcript != long {
		t.Fatalf("转写被动过：len %d → %d", len(long), len(after.Transcript))
	}
}

// TestS937CreateIdempotentDoesNotRewriteTranscript 钉住 D2 的**幂等同步**一半。
//
// ⚠ 场景：同一个客户端 id 再 POST 一次（§105 的幂等语义）⇒ 走「已存在 ⇒
//
//	只改标题」的分支 ⇒ ⇒ 那条分支不得重写整行（不碰转写）。
func TestS937CreateIdempotentDoesNotRewriteTranscript(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	counter := &s937CountingStore{MeetingStore: srv.meetingStore}
	srv.meetingStore = counter
	h := srv.Handler()

	long := s937LongTranscript()
	body := `{"id":"mtg-fixed-1","title":"标题一","refinedTranscript":"` + long + `"}`
	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", tokens["ws-a"], body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	// ⚠ 清掉「首次创建」的记账 ⇒ 下面只量**第二次**（幂等分支）。
	counter.writeBytes.Store(0)
	counter.readBytes.Store(0)

	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", tokens["ws-a"],
		`{"id":"mtg-fixed-1","title":"标题二"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("idempotent create status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := counter.writeBytes.Load(); got != 0 {
		t.Fatalf("幂等同步写回了 %d 字节转写（它只该改标题）⇒ 写放大仍在（D2 没修掉）", got)
	}
	if counter.metaCall.Load() != 1 {
		t.Fatalf("幂等同步应走 1 次定向更新，实际 %d 次", counter.metaCall.Load())
	}

	var resp struct {
		Title      string `json:"title"`
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if resp.Title != "标题二" {
		t.Fatalf("标题没改成新值：%q", resp.Title)
	}
	if resp.Transcript != long {
		t.Fatalf("响应里的转写被改了：len %d → %d", len(long), len(resp.Transcript))
	}
}
