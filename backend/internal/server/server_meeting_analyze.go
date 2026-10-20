// server_meeting_analyze.go — 会议「实时分析」action（2026-10-07 轮新增）。
//
// POST /api/meetings/{id}/analyze —— 对会议当前转写做网关侧 LLM 滚动分析：
// 摘要（增量合并）、要点、决定、行动项、开放问题与给主持人的实时提示
// （hints）。与规则版 summarize（server_meeting.go，落库覆盖 m.Summary）
// 的分工：本 action 是**录制中的只读视图**——不写会议状态/不覆盖摘要，
// 前端周期轮询刷新， 会议 finalize 后仍用 summarize 出正式纪要。
//
// 滚动契约（与网关 /v1/audio/analyze 对齐，见 llmgateway/audio_transform.go）：
//   - 每会议内存态持有 {上轮 summary, 已喂游标(rune), 上轮结果}；
//   - 转写有新增 → 只喂新增片段 + 上轮 summary，网关回合并后的全文摘要；
//   - 转写无新增 → 直接回上轮结果（unchanged=true），零 LLM 花费；
//   - 转写变短（重转写）→ 游标自动归零重析；
//   - 进程重启丢游标 → 下次从头全量分析，无正确性风险（转写本体在库）。
//
// ⚠⚠ §937（D1）：轮询**只取游标之后的新增段**（meetingStore.TranscriptTailScoped），
//
//	而不是把整份转写读进来再在 Go 里切 ⇒ ⇒ 每轮从库里搬走的字节数
//	∝ **这次新增了多少**，而不是 ∝ 全文 ⇒ ⇒ 一场 N 小时的会议按 P 秒轮询，
//	累计搬运量从 ∝ N²/P 降到 ∝ N（这是 §924 客户端整场重发的**服务端孪生**）。
//	⇒ ⇒ 尾巴的**口径只有一份**（meeting/transcript.go）⇒ PG 版与内存版不会分叉。
//
// LLM 走对话流的同一份 workspace 网关配置（ResolveGatewayForUser），成本
// 归属该 workspace 的 key；model 传空时按 preferred 首选 → auto 兜底，与
// resolveChatModel 同口径。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

// meetingAnalyzeTTL 是单会议滚动状态的闲置回收线：超过该时长没有新调用
// 就丢弃（录制会话早已结束）。轮询 UI 的节奏是分钟级，2h 足够宽。
const meetingAnalyzeTTL = 2 * time.Hour

// meetingAnalyzeTimeout 覆盖一次网关 analyze 调用（实测 minimax-text-01
// ~14s；慢上游 + 长转写按 90s 配，与 refine 客户端同量级）。
const meetingAnalyzeTimeout = 90 * time.Second

type meetingAnalyzeLive struct {
	priorSummary string
	sentRunes    int
	lastResult   *llmgateway.AnalyzeResult
	lastUsed     time.Time
}

// meetingAnalyzeStates 是全服务单例的滚动状态册。key=meetingID；锁只护
// map 与状态读写，LLM 调用在锁外（一次调用可达十几秒，持锁会串死所有
// 会议的轮询）。
var meetingAnalyzeStates = struct {
	mu sync.Mutex
	m  map[string]*meetingAnalyzeLive
}{m: map[string]*meetingAnalyzeLive{}}

// meetingAnalyzeGateway 是可测试性钩子：非 nil 时替代默认的 workspace
// 网关解析（httptest 桩）。生产路径保持 nil。
var meetingAnalyzeGateway func(s *Server, userID, workspaceID string) (*llmgateway.Client, bool)

func meetingAnalyzeGatewayFor(s *Server, userID, workspaceID string) (*llmgateway.Client, bool) {
	if meetingAnalyzeGateway != nil {
		return meetingAnalyzeGateway(s, userID, workspaceID)
	}
	cfg := s.ResolveGatewayForUser(userID, workspaceID)
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return nil, false
	}
	return llmgateway.NewClient(cfg.BaseURL, cfg.APIKey), true
}

type meetingAnalyzeIn struct {
	Model string `json:"model"`
	Style string `json:"style"`
	// PriorSummary 由客户端持有状态时直传（无状态用法）；为空则用服务端
	// 滚动缓存。两者都空 = 全量分析。
	PriorSummary string `json:"prior_summary"`
	// Reset 清空该会议的滚动游标（重转写/用户手动重析）。
	Reset     bool `json:"reset"`
	MaxPoints int  `json:"max_points"`
}

// handleMeetingLiveAnalyze 服务 POST /api/meetings/{id}/analyze。
func (s *Server) handleMeetingLiveAnalyze(w http.ResponseWriter, r *http.Request, meetingID string) {
	if meetingID == "" {
		writeError(w, http.StatusBadRequest, "missing meeting id")
		return
	}
	if s.meetingStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
		return
	}
	var body meetingAnalyzeIn
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
	}
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)

	// ── 滚动状态（锁内取，锁外用）──────────────────────────────────
	//
	// ⚠ §937（D1）：这里**只取游标**，不再为了算游标而把整份转写读进来。
	meetingAnalyzeStates.mu.Lock()
	st, exists := meetingAnalyzeStates.m[meetingID]
	fromRune := 0
	cachedPrior := ""
	var cachedResult *llmgateway.AnalyzeResult
	hasResult := false
	if exists {
		fromRune = st.sentRunes
		cachedPrior = st.priorSummary
		cachedResult = st.lastResult
		hasResult = cachedResult != nil
	}
	meetingAnalyzeStates.mu.Unlock()

	if body.Reset {
		// 显式重置 → 从头析（与旧实现同一条判据）。
		fromRune = 0
		cachedPrior = ""
		cachedResult = nil
		hasResult = false
	}

	// ⚠⚠ §937（D1）：**只取尾部**（游标起的新增段）+ 全文字符数。
	//
	//	修复前：GetScoped ⇒ 每轮把**整份转写**从库里搬进进程
	//	⇒ ⇒ 一场 4 小时的会议按 30 秒轮询 ⇒ 累计搬运量 ∝ 时长²
	//	⇒ ⇒ 这是 §924（客户端整场重发）的**服务端孪生**。
	//
	//	修复后：每轮只搬「这次新增的那一段」⇒ 每轮成本 ∝ 新增量。
	tail, totalRunes, err := s.meetingStore.TranscriptTailScoped(meetingID, uid, workspaceID, fromRune)
	if err != nil {
		writeError(w, http.StatusNotFound, "meeting not found")
		return
	}
	if totalRunes < fromRune {
		// 转写变短（重转写覆盖）→ 游标归零重析（与旧实现同一条判据）。
		fromRune = 0
		cachedPrior = ""
		cachedResult = nil
		hasResult = false
		tail, totalRunes, err = s.meetingStore.TranscriptTailScoped(meetingID, uid, workspaceID, 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "meeting not found")
			return
		}
	}

	// ⚠ 空转写守卫：**只在读到整份时**判定（fromRune==0 ⇒ tail 就是全文）
	//   ⇒ 与旧实现的 strings.TrimSpace(m.Transcript)=="" 是同一条判据。
	//
	//   fromRune>0 时不做这个判定：那时「上一次读到全文时它非空」已经成立
	//   （sentRunes>0 ⇒ 上一次 analyze 成功过 ⇒ 那时转写非空）⇒
	//   ⇒ 唯一差别是「转写被换成一段**等长**的纯空白」：旧实现会 400、
	//   新实现会继续（§937 的登记项 R3）。
	if fromRune == 0 && strings.TrimSpace(tail) == "" {
		writeError(w, http.StatusBadRequest, "meeting has no transcript, transcribe first")
		return
	}

	client, ok := meetingAnalyzeGatewayFor(s, uid, workspaceID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "LLM gateway not configured")
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" && len(s.ResolveGatewayForUser(uid, workspaceID).PreferredModels) > 0 {
		model = s.ResolveGatewayForUser(uid, workspaceID).PreferredModels[0]
	}
	if model == "" {
		model = "auto"
	}

	prior := strings.TrimSpace(body.PriorSummary)
	if prior == "" {
		prior = cachedPrior
	}
	// ⚠ §937：segment 现在就是**尾读**拿到的那一段（游标起的新增）⇒
	//   旧实现是「整读之后在 Go 里切」⇒ 结果逐字相同，但搬运量差一个全文。
	segment := tail
	unchanged := segment == "" && hasResult
	var cached *llmgateway.AnalyzeResult
	if unchanged {
		cached = cachedResult
	}
	// 防御：segment 空且无缓存（理论不可达——转写非空 + sentRunes>0 才可能，
	// 重置后必全量）→ 退回全量分析而不是给网关发空 transcript。
	if segment == "" && !hasResult {
		whole, _, err := s.meetingStore.TranscriptTailScoped(meetingID, uid, workspaceID, 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "meeting not found")
			return
		}
		segment = whole
	}

	if unchanged {
		writeJSON(w, http.StatusOK, map[string]any{
			"meeting_id":       meetingID,
			"unchanged":        true,
			"transcript_runes": totalRunes,
			"segment_runes":    0,
			"analyze":          cached,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), meetingAnalyzeTimeout)
	defer cancel()
	res, err := client.AnalyzeTranscription(ctx, llmgateway.AnalyzeRequest{
		Model:        model,
		Transcript:   segment,
		PriorSummary: prior,
		Style:        body.Style,
		Language:     "zh",
		MaxPoints:    body.MaxPoints,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "live analyze failed: "+err.Error())
		return
	}

	// ── 提交新状态（锁内）────────────────────────────────────────
	//
	// ⚠ §937：状态条目在**提交时**才建（而不是像旧实现那样在读库之后立刻建）
	//   ⇒ ⇒ 404/400/503 这三条早退路径**不留垃圾条目**（旧实现同口径）。
	meetingAnalyzeStates.mu.Lock()
	st, exists = meetingAnalyzeStates.m[meetingID]
	if !exists {
		st = &meetingAnalyzeLive{lastUsed: time.Now()}
		meetingAnalyzeStates.m[meetingID] = st
	}
	st.priorSummary = res.Summary
	st.sentRunes = totalRunes
	st.lastResult = res
	st.lastUsed = time.Now()
	// 顺手回收闲置会议（每次调用扫一遍；会议量级下可忽略）。
	now := time.Now()
	for k, v := range meetingAnalyzeStates.m {
		if now.Sub(v.lastUsed) > meetingAnalyzeTTL {
			delete(meetingAnalyzeStates.m, k)
		}
	}
	meetingAnalyzeStates.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"meeting_id":       meetingID,
		"unchanged":        false,
		"transcript_runes": totalRunes,
		"segment_runes":    utf8.RuneCountInString(segment),
		"analyze":          res,
	})
}
