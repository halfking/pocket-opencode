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
	m, err := s.meetingStore.GetScoped(meetingID, uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "meeting not found")
		return
	}
	if strings.TrimSpace(m.Transcript) == "" {
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

	// ── 滚动状态（锁内取，锁外用）──────────────────────────────────
	totalRunes := utf8.RuneCountInString(m.Transcript)
	meetingAnalyzeStates.mu.Lock()
	st, exists := meetingAnalyzeStates.m[meetingID]
	if !exists {
		st = &meetingAnalyzeLive{lastUsed: time.Now()}
		meetingAnalyzeStates.m[meetingID] = st
	}
	if body.Reset || totalRunes < st.sentRunes {
		// 显式重置，或转写变短（重转写覆盖）→ 从头析。
		*st = meetingAnalyzeLive{lastUsed: time.Now()}
	}
	prior := strings.TrimSpace(body.PriorSummary)
	if prior == "" {
		prior = st.priorSummary
	}
	segment := ""
	if totalRunes > st.sentRunes {
		runes := []rune(m.Transcript)
		segment = string(runes[st.sentRunes:])
	}
	unchanged := segment == "" && st.lastResult != nil
	var cached *llmgateway.AnalyzeResult
	if unchanged {
		cached = st.lastResult
	}
	// 防御：segment 空且无缓存（理论不可达——转写非空 + sentRunes>0 才可能，
	// 重置后必全量）→ 退回全量分析而不是给网关发空 transcript。
	if segment == "" && st.lastResult == nil {
		segment = m.Transcript
	}
	meetingAnalyzeStates.mu.Unlock()

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
	meetingAnalyzeStates.mu.Lock()
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
