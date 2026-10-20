package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// handleMeetingRouter dispatches /api/meetings/{id}/{action} and per-meeting
// GET/DELETE. Action set: summary, recommend, refine (kxmemory-backed),
// transcribe (STT), summarize (rule-based) and analyze (live LLM rolling
// summary + hints, read-only). Bare {id} supports GET/DELETE.
func (s *Server) handleMeetingRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/meetings/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "meeting id required")
		return
	}
	meetingID := parts[0]

	// Bare id: GET / DELETE on a single meeting.
	if len(parts) == 1 || parts[1] == "" {
		if s.meetingStore == nil {
			writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
			return
		}
		uid := s.userIDFromRequest(r)
		workspaceID := s.workspaceIDFromRequest(r)
		switch r.Method {
		case http.MethodGet:
			m, err := s.meetingStore.GetScoped(meetingID, uid, workspaceID)
			if err != nil {
				writeError(w, http.StatusNotFound, "meeting not found")
				return
			}
			writeJSON(w, http.StatusOK, m)
		case http.MethodDelete:
			if err := s.meetingStore.DeleteScoped(meetingID, uid, workspaceID); err != nil {
				writeError(w, http.StatusNotFound, "meeting not found")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET/DELETE only")
		}
		return
	}

	// Action suffixes: all require POST.
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	action := parts[1]
	switch action {
	case "summary":
		s.handleMeetingSummary(w, r, meetingID)
	case "recommend":
		s.handleMeetingRecommend(w, r, meetingID)
	case "refine":
		s.handleMeetingRefine(w, r, meetingID)
	case "transcribe":
		s.handleTranscribeMeeting(w, r, meetingID)
	case "summarize":
		s.handleSummarizeMeeting(w, r, meetingID)
	case "analyze":
		// 2026-10-07 轮：录制中的实时 LLM 分析（滚动摘要 + hints），只读
		// 不落库；正式纪要仍走 summarize。见 server_meeting_analyze.go。
		s.handleMeetingLiveAnalyze(w, r, meetingID)
	default:
		writeError(w, http.StatusNotFound, "unknown meeting action: "+action)
	}
}

// handleTranscribeMeeting runs the configured STT pipeline (or a no-op stub if
// STT is not configured) and updates the meeting status / transcript under the
// caller's workspace scope. Returns 404 if the meeting is missing or owned by
// another workspace.
func (s *Server) handleTranscribeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing meeting id")
		return
	}
	if s.meetingStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	m, err := s.meetingStore.GetScoped(id, uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "meeting not found")
		return
	}
	m.Status = "transcribing"
	_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBodyBytes)
	audioData, err := io.ReadAll(r.Body)
	if err != nil {
		m.Status = "failed"
		_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)
		writeError(w, http.StatusBadRequest, "failed to read audio data")
		return
	}
	defer r.Body.Close()
	if len(audioData) == 0 {
		m.Status = "failed"
		_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)
		writeError(w, http.StatusBadRequest, "empty audio data")
		return
	}
	// 转写目标由该用户的语音转写设置解析，不再写死 Groq Whisper；
	// 会议录音停止链路是同步等推理结果的，30s 太紧，给到 120s。
	scope := stt.Scope{UserID: uid, WorkspaceID: workspaceID}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	// 2026-10-05：filename 此前硬编码 "meeting.wav"，而前端 MediaRecorder
	// 的真实产物是 webm/opus——webm 字节顶着 .wav 名字上传，网关按 wav
	// 推断格式、小米按 wav 解码直接 400（实测）。先按容器魔数嗅探真实
	// 格式；嗅探不出再回退默认名。
	// ⚠ §457（2026-10-11 登记，**未修**）：本调用方走的是 `TranscribeFor`，
	// 它把 transcribeFor 的最后一个参数传 **false** ⇒ 会向网关要
	// `response_format=verbose_json` + `timestamp_granularities[]=word` + 分离。
	// 而**本 handler 的响应只有 `{status, meeting_id}`**（见下面的 writeJSON）
	// ⇒ 这些增强数据**拿回来直接丢掉**。
	//
	// 与 §120（`chunked_no_discard_gate_test.go`「切块链路不许为丢弃的增强特性付费」）
	// 是同一条原则，但 §120 只覆盖了 TranscribeFull / IncrementalTranscriber，
	// **没有覆盖 TranscribeFor 的三个生产调用方**（试转页 / 本处 / `/api/stt/transcribe`）——
	// 其中试转页**确实**消费了（它回 `segments` 与 `diarized`），另两个没有。
	//
	// ⚠ 为什么不就地修：本机没有网关凭据，**改的是发给真实网关的请求参数**，
	// 「不传分离后上游是否仍 200、是否换出别的失败面」无法自证。
	// ⇒ 登记为 #24 待拍；修法是本处改走 plain 形态（试转页保持开）。
	result, err := s.transcriber.TranscribeFor(ctx, scope, audioData, stt.FilenameForAudio(audioData, "meeting.wav"))
	if err != nil {
		m.Status = "failed"
		_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)
		// 已带错误码的（stt_unavailable: …）原样回传。api/error-message.ts 取
		// 第一个冒号前的 [a-z0-9_]+ 当错误码；再套 "transcription failed: "
		// 前缀会让它匹配失败，用户就只能看到通用「服务端错误」。
		msg := err.Error()
		if !strings.HasPrefix(msg, "stt_unavailable:") {
			msg = "transcription failed: " + msg
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	m.Transcript = result.Text
	m.Status = "transcribed"
	if err := s.meetingStore.UpdateScoped(m, uid, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist transcript")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "transcribed",
		"meeting_id": id,
	})
}

// handleSummarizeMeeting produces a rule-based summary + key decisions +
// action items from the meeting's stored transcript, then persists them.
func (s *Server) handleSummarizeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing meeting id")
		return
	}
	if s.meetingStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	m, err := s.meetingStore.GetScoped(id, uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "meeting not found")
		return
	}
	if strings.TrimSpace(m.Transcript) == "" {
		writeError(w, http.StatusBadRequest, "meeting has no transcript, transcribe first")
		return
	}
	m.Status = "summarizing"
	_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)

	summary, err := meeting.SummarizeTranscript(m.Transcript, m.Title)
	if err != nil {
		m.Status = "failed"
		_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)
		writeError(w, http.StatusInternalServerError, "summarization failed: "+err.Error())
		return
	}
	m.Summary = summary.Summary
	m.KeyDecisions = summary.KeyDecisions
	m.ActionItems = summary.ActionItems
	m.Status = "done"
	if err := s.meetingStore.UpdateScoped(m, uid, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist summary")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type meetingSegmentIn struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Lang    string `json:"lang"`
	StartMs int    `json:"start_ms"`
	EndMs   int    `json:"end_ms"`
}

type meetingMetaIn struct {
	Title        string   `json:"title"`
	Participants []string `json:"participants"`
	Location     string   `json:"location"`
	// Topic 是**会议主题**，2026-10-07（§138.3）接入。
	//
	// 为什么它此前是条死路：UI 里有主题输入框（`MeetingSettingsSheet.vue`
	// 绑 `:topic="meeting.topic"`）、store 里有 `LocalMeeting.topic`、
	// `createMeeting` 也收它 —— 但它**从来没进过这个请求体**，
	// 所以 `metaTermHint` 读不到、精校拿不到。
	//
	// ★ 而主题恰恰是**专有名词**所在。§138.3 在真音频上实测到：
	//   `mimo-v2.5-asr` 把「悬界芯片」转成（对的）「悬界」，
	//   `minimax-asr-1.0` 转成（错的）「玄介芯片」——
	//   而「悬界芯片客户评审会」正是仓内语料里的**会议主题**。
	//   ⇒ 用户在主题里写对了产品名，精校却拿不到它 ⇒ 这类错修不了。
	//
	// ⚠ 加它有代价，必须实测（见 live_refine_topic_term_test.go 的
	//   TestLiveGatewayRefineTermHintTopic 配对实验）：
	//   提示词每多一段「可信上下文」，模型就多一条「把不相关的词对齐进去」的
	//   通路 —— §35 实测过名单类上下文把「张伟/李娜」对齐成「赵敏/孙磊」。
	//   所以「只测到无害」不够，必须量到**有益**。
	//
	// ★★★ 2026-10-07（§146）：**我原先写在 metaTermHint 里的理由被自己的实验证伪了**，
	//   在这里更正，以免下一个读到那段注释的人以为「产品名只能放主题」：
	//
	//	§146 的 A/B（3 臂 × 3 次，三臂交错，真网关）读数：
	//	  OFF  无元数据           修好 0/3
	//	  TITLE 产品名放 Title    修好 3/3
	//	  TOPIC 产品名放 Topic    修好 3/3
	//
	//	⇒ **被证实**的是「专有名词进了术语表就能修好坏词」（0/3 → 3/3，零越界）；
	//	  这比 §138.3 那轮 n=1 的 ON/OFF 强得多。
	//	⇒ **被证伪**的是「必须新增 topic 字段」这条必要性理由 ——
	//	  产品名放在 Title 里**一样** 3/3。
	//	⇒ 所以本字段留下**不是因为 title 装不下**，而是因为它多一个输入面：
	//	  用户把产品/项目名写在「主题」里，而 Title 常常是「客户评审会筹备」这类泛称。
	//	  这是**产品事实**，不是模型行为结论 —— 模型行为那条已被证伪。
	//
	// ⚠ 2026-10-07（§159）登记的**已知缺口**：本字段被**两条链共用**
	//   （`meetingSummaryBody.Meta` 与 `meetingRefineBody.Meta` 都是 `meetingMetaIn`），
	//   但**只有精校链真的消费它**：
	//
	//	llmMeetingSummary(ctx, r, segs, prev)  ← 压根不收 meta
	//	buildSummaryPrompt(transcript, prev)  ← 没有 meta 形参
	//
	//   ⇒ 摘要请求里带过来的 topic 会被**静默丢掉**（§64.6 的形状）。
	//   这不是「摘要已支持主题」，是「契约共用导致声明溢出到用不到的链」。
	//   要真让摘要用上主题，得把 meta 接进 buildSummaryPrompt（改提示词，需复核）。
	//
	//   为什么**不**拆成两个类型（`meetingSummaryMetaIn` 少一个字段）：
	//     ① 三个共有字段会被抄成两份 ⇒ 同一缺陷必然出现在两处（§80 的教训）；
	//     ② `meetingMetaIn{Topic: …}` 这类复合字面量在测试里有几十处，
	//        一旦 Topic 挪进嵌入结构体，Go **不允许**在复合字面量里设提升字段 ⇒ 全仓编译失败。
	//   ⇒ 代价远大于收益。改成在前端把「声明」与 Go 的「解码」对齐（§159 门钉这一层），
	//     并在这里显式登记溢出，删掉前端那一个 `topic?:` 即可回退。
	Topic string `json:"topic"`
}

// meetingSummaryBody / meetingRefineBody 是两个 handler 的请求体。
//
// ★ 2026-10-07（§92）提成具名类型。此前两者都是 handler 内的**匿名结构体**，
//
//	而跨语言契约门（§85 refine_meta_wire_test.go、§92 summary_request_wire_test.go）
//	只能把字段**抄一份**进测试文件 —— 抄错任何一侧，两边照样全绿。
//
//	这正是 §80 那个教训的变体：「同一份形状抄两遍 ⇒ 同一缺陷必然出现在两处」。
//	提成本类型后，判据直接引用它，形状只剩**一个**来源；
//	改 tag / 增删字段时，判据会跟着生产代码一起编译不过或转红。
type meetingSummaryBody struct {
	Segments    []meetingSegmentIn `json:"segments"`
	PrevSummary string             `json:"prev_summary"`
	Meta        meetingMetaIn      `json:"meta"`
}

type meetingRefineBody struct {
	Segments    []meetingSegmentIn `json:"segments"`
	TargetLangs []string           `json:"target_langs"`
	Meta        meetingMetaIn      `json:"meta"`
}

func (s *Server) handleMeetingSummary(w http.ResponseWriter, r *http.Request, meetingID string) {
	var body meetingSummaryBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Segments) == 0 {
		writeError(w, http.StatusBadRequest, "segments required")
		return
	}

	// ★ 2026-10-07：agent 与它的一次性回落**必须各拿各的预算**。
	//
	// 原来两者共用这一个 ctx。算一笔账（生产配置 llmBFF 已装配 ⇒ 一定走 agent）：
	//
	//	kxmemory        404，快速失败（不耗预算）
	//	agent           DefaultMaxTurns = 3 ⇒ 最多 3 次**串行** LLM 往返
	//	一次性 chat 回落  用的是**同一个 ctx**
	//
	// 而网关 auto 当前解析到推理模型 deepseek-v4-flash，
	// 同一条提示词实测首字节 24~142s（§35.7）。
	// ⇒ agent 只要走满一轮，ctx 就过期了；**回落拿到的是一个死 context**，
	//   必然立刻失败，整个端点返回 502。
	//
	// ⇒ 「agent 失败时必须回落到一次性 chat」（§11/§14 的原意）
	//    在生产上**从来没有真正生效过** —— 不是回落质量差，是回落根本没跑成。
	//
	// 修法：agent 用一个**独立子预算**，回落另起一个从 r.Context() 派生的
	// 新 ctx（仍受客户端断开影响，只是不再继承已耗尽的预算）。
	//
	// ⚠ 绝对数值仍需人工拍板（见 §35.7 的三层预算问题）：
	//   这里只修「回落不该继承死预算」这个**结构性问题**，
	//   不擅自抬高全局超时。
	const summaryBudget = 45 * time.Second
	const agentBudget = 30 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), summaryBudget)
	defer cancel()

	// Try kxmemory first
	if s.kxmemory != nil {
		resp, err := s.kxmemory.MeetingSummary(ctx, kxmemory.MeetingSummaryRequest{
			MeetingID:   meetingID,
			Segments:    toKxSegments(body.Segments),
			PrevSummary: body.PrevSummary,
			Meta:        kxmemory.MeetingMeta{Title: body.Meta.Title, Participants: body.Meta.Participants},
		})
		if err == nil {
			s.wsHub.Broadcast("meeting.summary_updated", map[string]any{
				"meetingId": meetingID,
				"summary":   resp.Summary,
			})
			writeJSON(w, http.StatusOK, resp)
			return
		}
		log.Printf("[meeting] kxmemory summary fallback for %s: %v", meetingID, err)
	}

	// LLM fallback（2026-09-05 迁移动态网关：llmBFF 已装配时 llmMeetingSummary
	// 内部走 llmChatOnce → BFF；s.llm 仅在 BFF 未装配的部署兜底）
	if s.llmBFF == nil && s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	// 智能体优先（2026-10-06）：需求原文「这个需要有一个智能体来完成这些」。
	// agent 与原来的一次性 chat 的差别不是形式 —— 它能自己调 search_notes
	// 去查用户自己的笔记，把「参考资料」从模型编的变成查到的。
	//
	// **它失败时必须回落到一次性 chat**，而不是报错：总结是主链路，
	// agent 是增强。回落是隐式的（下面的 err != nil 分支走原路径），
	// 所以净效果是「有 agent 用 agent，没有就用老办法」，调用方无感。
	//
	// ⚠ 2026-10-07：agent 必须有**自己的**预算。若沿用外层 ctx，
	//   agent 跑满一轮就把预算耗光，下面的回落拿到死 context（见上面那段）。
	agentCtx, agentCancel := context.WithTimeout(r.Context(), agentBudget)
	result, used := s.meetingSummaryViaAgent(agentCtx, r, body.Segments, body.PrevSummary)
	agentCancel()
	if used {
		if result == nil {
			log.Printf("[meeting] agent summary failed for %s, falling back to one-shot", meetingID)
		} else {
			result["agent"] = true
			s.wsHub.Broadcast("meeting.summary_updated", map[string]any{
				"meetingId": meetingID,
				"summary":   result["summary"],
			})
			writeJSON(w, http.StatusOK, result)
			return
		}
	}
	result, err := s.llmMeetingSummary(ctx, r, body.Segments, body.PrevSummary)
	if err != nil {
		writeError(w, http.StatusBadGateway, "summary failed: "+err.Error())
		return
	}
	s.wsHub.Broadcast("meeting.summary_updated", map[string]any{
		"meetingId": meetingID,
		"summary":   result["summary"],
	})
	writeJSON(w, http.StatusOK, result)
}

// meetingSummaryViaAgent 尝试用有界智能体出会议摘要。
//
// 返回 used=false 表示「本部署不具备 agent 条件，调用方应走原路径」
// （llmBFF 未装配 —— agent 必须走流式才能拿到 tool_calls，见 llmbff.Delta）。
// 返回 (nil, true) 表示「跑了但失败」，调用方回落到一次性 chat。
func (s *Server) meetingSummaryViaAgent(
	ctx context.Context, r *http.Request, segs []meetingSegmentIn, prev string,
) (map[string]any, bool) {
	if s.llmBFF == nil {
		return nil, false
	}
	transcript := segmentsToText(segs)
	if transcript == "" {
		return nil, true
	}
	// 尾部而非头部：滚动摘要关心的是**刚刚发生了什么**，
	// 开头那段早在上一轮总结过了。
	const maxTranscriptRunes = 4000
	if runes := []rune(transcript); len(runes) > maxTranscriptRunes {
		transcript = string(runes[len(runes)-maxTranscriptRunes:])
	}

	runner := &meetingagent.Runner{
		Chat:        s.llmBFF.Stream,
		WorkspaceID: s.workspaceIDFromRequest(r),
		UserID:      s.userIDFromRequest(r),
		Kind:        "meeting_summary_agent",
	}
	tools := s.meetingTools(runner.UserID, runner.WorkspaceID)

	res, err := runner.Run(ctx, meetingAgentSystemPrompt(prev, tools), transcript, tools)
	if err != nil {
		log.Printf("[meeting] agent run error: %v", err)
		return nil, true
	}
	if res.Truncated {
		// 到达轮数上限：内容是**工具输出**不是模型结论，直接用它当摘要
		// 会把一坨检索结果糊到用户界面上。宁可回落到一次性 chat。
		log.Printf("[meeting] agent truncated after %d turns, falling back", res.Turns)
		return nil, true
	}
	parsed, fellBack := parseSummaryJSONWithFallback(res.Content, transcript)
	if fellBack {
		// ⚠ 2026-10-08 补：回落成原始转写**不等于**拿到了一次成功的摘要。
		//
		// emptySummary() 的形状是 summary = 原始转写前 500 字节、四个数组全空。
		// 以前这里只在 perr != nil 时回落，而 parseSummaryJSON 的**两条**返回路径
		// 都返回 nil error ⇒ 那个分支可证不可达 ⇒ 回落形状照样被打上 agent_turns
		// 并以 (parsed, true) 上抛 ⇒ handler 广播 meeting.summary_updated、
		// 标 result["agent"]=true、返回 HTTP 200。
		//
		// ⇒ 用户看到「智能体摘要已更新」，内容是自己的转写片段，零错误提示。
		// 对照精校侧：parseRefineJSON 在同样情况下返回 **error**，调用方据此置
		// refine_fallback，界面不会说「精翻完成」。摘要侧缺的就是这个区分。
		//
		// 处置走**已写好的**回落（return nil, true → 一次性 chat），不新造第三种
		// 终态：一次性 chat 要么给出真摘要，要么诚实报 502。
		// ⚠ 代价（属产品取舍，未定）：agent 不可用时这一次摘要要付两次 LLM 调用。
		// 今天这个代价已经以「静默假成功」的形式存在，只是代价换成了用户信任。
		log.Printf("[meeting] agent summary unusable (turns=%d, content %d runes), falling back to one-shot",
			res.Turns, len([]rune(res.Content)))
		return nil, true
	}
	// Agent 标志让前端/审计能区分「查过资料的摘要」与「一次性的摘要」。
	parsed["agent_turns"] = res.Turns
	if len(res.ToolCalls) > 0 {
		parsed["agent_tools"] = res.ToolCalls
	}
	return parsed, true
}

// meetingTools 按部署形态给出这次摘要**实际注册**的工具集。
//
// ⚠ §469：这段是从 meetingSummaryViaAgent 里抽出来的，不是为了好看 ——
//
//	它是「提示词与接线同源」这条不变量的**另一半**：
//	提示词按 tools 分支，而 tools 由本函数从 s.notesStore 推导。
//	两段都留在一个函数里时，只有「源码里出现过某个字面量」那种门能守，
//	而那种门分不清「真的注册了」与「文件里出现过」。
//
// ⚠ 返回 nil（不是空切片）是无意的，但别改：meetingagent.Runner 对两者
//
//	的处理完全一致，而门断言的就是 nil —— 少写一层归一化就少一个可漂移点。
func (s *Server) meetingTools(userID, workspaceID string) []meetingagent.Tool {
	if s.notesStore == nil {
		return nil
	}
	// ⚠ 作用域必须一起传（§330.5）：NoteSearcher 内部要按 (user, workspace)
	// 过滤，漏任一个都会检索不到 —— 或更糟，跨工作区泄漏。
	// ⚠ 这里必须写成**单行**：`server_meeting_agent_wiring_test.go:41`
	//    的接线判据断言的是字面量 `Src: s.notesStore`；
	//    多行字面量会被 gofmt 对齐成 `Src:         s.notesStore`
	//    而那个判据就红了 —— **它结构性禁止给 NoteSearcher 加字段**。
	searcher := meetingagent.NoteSearcher{Src: s.notesStore}
	searcher.UserID, searcher.WorkspaceID = userID, workspaceID
	return []meetingagent.Tool{&searcher}
}

// meetingAgentSystemPrompt 给会议纪要智能体立规矩。
//
// 三条约束都是踩过才知道要写的：
//   - 允许查笔记、禁止编造引用：没有这一步，模型会为了「参考资料」去编
//     看起来很像真的笔记标题。
//   - 强制 JSON：调用方要的是结构化字段（action_items 带 due），
//     自由文本只能退回 parseSummaryJSON 的兜底。
//   - 最多查两次：工具循环最常见的失败是反复查同一个词。
//
// ⚠⚠ §469（2026-10-11）：tools 参数**不是**可选润色，是这条缺陷的修法本体。
//
// §328.7 用真网关坐实过：notesStore 为 nil（remote-only 部署）时
// server_meeting.go 一个工具都不注册，而旧签名无条件宣传 search_notes
// ⇒ 模型去调用一个不存在的工具，调用漏进正文。那批运行 9/12 在智能体层失败，
// 而 §109 的降级提示因 `agent != true` 为假**一句话都不说**，用户只看到摘要空着。
//
// ⇒ 改法不是「工具为空时说句客套话」，而是**让提示词与接线同源**：
// 提示词里提不提 search_notes，由**实际注册的工具集**推导。
//
// ⚠ 为什么按 `Definition().Function.Name` 而不是类型断言 `*meetingagent.NoteSearcher`：
//
//	名字是**模型唯一看得见的东西**（Runner 把 Definition 发给网关），
//	也是探针里 recordingTool 包装后仍然成立的那一层。
//	类型断言会被包装击穿 ⇒ 探针的 with-tools 臂会误拿到「工具为空」提示词，
//	而那一批读数正是用来量这个缺陷的。
//
// ⚠ withSearch=true 分支的产物与 §469 之前的提示词**逐字节相同** ——
//
//	刻意如此：这是唯一有真网关基线的变体，改措辞会把基线作废。
func meetingAgentSystemPrompt(prev string, tools []meetingagent.Tool) string {
	withSearch := promptHasTool(tools, meetingagent.SearchNotesToolName)

	head := `你是会议纪要助手。只根据给定转写总结，禁止编造未说出口的人名、数字、决议。

`
	if withSearch {
		head += `你可以调用 search_notes 检索用户自己过去的笔记，以便在总结里引用真实记录。
**引用必须来自工具返回的内容**：工具没返回的笔记不许写进 references，
宁可没有 references 也不要编造笔记标题。

`
	}

	// ⚠ 公共部分**只写一遍**：§468 那次「同一个集合被手写 N 遍」的教训 ——
	//   两份变体若各自内嵌一份 JSON 契约，改 schema 时必然只改一处。
	body := `严格返回 JSON（不要 markdown 代码围栏）：
{"summary":"","key_points":[],"action_items":[{"text":"","assignee":"","due":""}],"decisions":[],"open_questions":[],"references":[{"title":"","note_id":"","why":""}]}

- action_items 只放转写里真实提到、且需要有人去做的事。
- due 保留用户原话（如「明天下午三点」），不要自己换算成日期。
`
	if withSearch {
		body += `- references 最多 3 条，每条都要能对应到工具返回的 note_id。
- 最多调用 search_notes 两次。`
	} else {
		// 这三行与 §328.7 探针里的 toollessPromptAdd 逐字相同 ——
		// 那是当时手工手术的目标文案，现在是**生产文案本身**。
		body += `- 当前**没有可用的笔记检索工具**：不要调用任何工具，也不要把工具调用写进正文。
- references 必须是空数组。用户要求提供参考资料时，不要编造，
  在 summary 里说明当前无法检索笔记。`
	}

	sys := head + body
	if prev != "" {
		sys += "\n\n已有摘要（在此基础上更新，不要重复它已经说过的）：\n" + prev
	}
	return sys
}

// promptHasTool 报 tools 里有没有一个**模型看得见的**同名工具。
//
// ⚠ nil 元素跳过而不是 panic：tools 由调用方拼装，探针的 noTools 臂
//
//	本来就是 nil 切片，将来若出现「append 了 nil」也不该把摘要打挂。
func promptHasTool(tools []meetingagent.Tool, name string) bool {
	for _, t := range tools {
		if t == nil {
			continue
		}
		if t.Definition().Function.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleMeetingRecommend(w http.ResponseWriter, r *http.Request, meetingID string) {
	var body struct {
		Segments []meetingSegmentIn `json:"segments"`
		Summary  string             `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if s.kxmemory != nil {
		resp, err := s.kxmemory.MeetingRecommend(ctx, kxmemory.MeetingRecommendRequest{
			MeetingID: meetingID,
			Segments:  toKxSegments(body.Segments),
			Summary:   body.Summary,
		})
		if err == nil {
			s.wsHub.Broadcast("meeting.recommend_updated", map[string]any{
				"meetingId": meetingID,
				"items":     resp.Items,
			})
			// ★ 显式声明 fallback=false：kxmemory 是**主**路径，
			//   它返回空就是「库里确实没有相关内容」，不是降级。
			//   键在这里必须出现，否则前端分不出「主源说没有」与「压根没走到主源」。
			//
			// ⛔ §563 勘误：上面第三行要求的那个区分，**当前响应做不到**。
			//   本行的 fallback=false（主源说没有）与下面 LLM 兜底分支的
			//   `"fallback": fellBack`（压根没走主源，内容是模型现编的）
			//   在响应里**完全同形**。
			//   原因：fellBack 来自 parseRecommendJSONWithFallback，
			//   而那个函数只在**解析失败**时返回 true，解析成功一律 false。
			//   ⇒ 这个键的实际语义是「模型有没有好好说话」，不是「有没有走主源」。
			//   ⇒ 而 deploy/bin/deploy-local.sh:92 是 POCKET_KXMEMORY_BASE_URL=（空）、
			//     main.go:411 只在非空时构造 client ⇒ 默认部署形态恒走 LLM 兜底
			//     ⇒ 前端两处诚实提示语（MeetingInsightPanel.vue:114、
			//       LiveSummaryPanel.vue:43）默认形态下**一句都不会说**。
			//   见 §563；特征化判据 TestMeetingRecommend_FallbackFlagIsNotAPathSignal。
			writeJSON(w, http.StatusOK, map[string]any{"items": resp.Items, "fallback": false})
			return
		}
		log.Printf("[meeting] kxmemory recommend fallback for %s: %v", meetingID, err)
	}

	// LLM 兜底（2026-10-06）。
	//
	// 此前这里是硬编码 `writeJSON(..., {"items": []any{}})`，注释写「memory
	// search requires kxmemory」。但 kxmemory 是**可选**依赖：
	// config.go 的默认值是 `getEnv("POCKET_KXMEMORY_BASE_URL", "")`，
	// 而 cmd/pocketd/main.go 只在 `cfg.KxMemoryBaseURL != ""` 时才构造 client
	// ⇒ 没配这个环境变量时 s.kxmemory == nil，/recommend **恒返回空**。
	//
	// 后果不是「少一个锦上添花的推荐」：用户需求里「在总结同时给出参考资料
	// 与建议」这条，在最常见的部署形态下**整条静默失效**，而且返回 200 +
	// 空数组，前端 `res.items ?? []` 同样不报错 —— 从 UI 到日志都看不出
	// 「这里本该有东西」。同一条链上的 summary / refine 都有 LLM 兜底，
	// 只有 recommend 没有，三条降级链不齐。
	//
	// 兜底产出的是「话题 + 建议」而不是伪造的记忆条目：没有 kxmemory 就
	// 检索不到用户自己的笔记/邮件，硬造 note/email 类型只会点开是死链。
	if s.llmBFF == nil && s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	items, fellBack, err := s.llmMeetingRecommendWithFallback(ctx, r, body.Segments, body.Summary)
	if err != nil {
		log.Printf("[meeting] llm recommend failed for %s: %v", meetingID, err)
		// ⚠ 这里此前是「失败也报 200 + 空列表」：一次**失败**在响应里
		// 与「模型确实没建议」完全同形 ⇒ 前端整块静默消失，用户无从知道。
		// fallback=true 让前端能在空的时候说一句实话（§109 已在 references 上
		// 立过同一条规矩）。
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "fallback": true})
		return
	}
	s.wsHub.Broadcast("meeting.recommend_updated", map[string]any{
		"meetingId": meetingID,
		"items":     items,
	})
	// ⚠⚠ §563：**这一行是整个推荐链上信息量最低、后果最大的一行。**
	//
	// 走到这里只有一种可能：`s.kxmemory == nil`（默认部署形态，见 :625-628）
	// 或 kxmemory 调用出错 —— 也就是说，**主源压根没参与**，
	// 这里的 items 全部来自模型凭记忆现编。
	//
	// 而 fallback 却报 false（只要模型答得合法就是 false），与上面主源分支的
	// 「库里确实没有相关内容」在响应里完全同形 ⇒ 前端无法说出一句「这是模型的建议，
	// 不是你库里的资料」。而 kxmemory 未配是**默认形态**，不是边缘情形。
	//
	// ⇒ 缺的是一个**路径信号**（如 source: kxmemory|llm），不是再调 parse 的判据。
	//   ⛔ 但加键属于契约变更、要连前端两处提示语一起改，**属产品口径，登记不代拍**。
	//   ⇒ 现状由 TestMeetingRecommend_FallbackFlagIsNotAPathSignal 钉住，
	//     哪天真加了 source，那条判据会红并提示同步更新 §563 与前端。
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "fallback": fellBack})
}

// llmMeetingRecommend 在没有 kxmemory 时用 LLM 产出「参考资料与建议」。
//
// 为什么产出 type="web"：没有 kxmemory 就没有检索源，能给的只有**话题线索**
// 与**行动建议**，而不是用户库里真实存在的笔记/邮件。前端 onOpenRelated 对
// web 类型走 item.url 打开外链，对 note/meeting 走站内路由 —— 所以这里必须
// 配一个真实可打开的 URL（搜索结果页），否则会点进一个不存在的站内页面。
//
// 为什么不自己联网搜：server 侧出网要过 SSRF 校验，且「实时搜索」会把一条
// 会议推荐变成一个外部依赖 + 一份需要缓存/超时/去重的子系统。把「去哪儿找」
// 做成一个用户点得动的搜索链接，是这个阶段更诚实的形态 —— 建议是 LLM 的，
// 事实核验交给用户点开的那一页。
// llmMeetingRecommend 保留两值签名（3 处既有判据直接调它）。
//
// ⚠ 它的 error 返回值语义不变，但「返回空列表」这件事此前**没有任何信号**，
// 调用方分不出「模型确实没建议」与「这条链路压根没拿到结果」。
// 需要区分的调用方用 llmMeetingRecommendWithFallback。
func (s *Server) llmMeetingRecommend(ctx context.Context, r *http.Request, segs []meetingSegmentIn, summary string) ([]map[string]any, error) {
	items, _, err := s.llmMeetingRecommendWithFallback(ctx, r, segs, summary)
	return items, err
}

// llmMeetingRecommendWithFallback 多返回一个「这次是回落」的标志。
//
// 为什么需要：/recommend 的失败形态是**空列表**，而空列表也是一个正当结果
// （提示词明写「没有值得建议的就返回空数组」）⇒ 两者在响应里长得一模一样。
// ⇒ 前端只能靠 `recommendations.length` 判断，那等于没有判断：
// 整块「相关 · 笔记 / 知识库 / 网络」静默消失，而用户需求里
// 「在总结同时给出参考资料与建议」这条**正在被无声地放弃**。
// 这与 §109 修过的 references 静默消失是同一类，且那次已经确立了
// 「降级必须说得出来」的规矩（§30：宁可少说一句，不可说错一句）。
func (s *Server) llmMeetingRecommendWithFallback(ctx context.Context, r *http.Request, segs []meetingSegmentIn, summary string) ([]map[string]any, bool, error) {
	transcript := segmentsToText(segs)
	if len(segs) == 0 {
		return nil, false, fmt.Errorf("segments required")
	}
	// 转写可能很长，而这条链路是**滚动**推荐（不是每 30 秒一次）：
	// 前端的闸是「主题切换 **或** 新增 ≥3 段 **或** 距上次 ≥30s」四选一
	// （`useLiveSummary.shouldRefreshSummary`），密集讨论时比 30 秒更频繁，
	// 而且**每轮都会跟着跑一次**。把预算花在最新的若干段上就够了，
	// summary 已经承载了全局信息。
	//
	// 按 rune 截而不是按字节：中文一字 3 字节，按字节切会落在字的中间，
	// 产出非法 UTF-8 送进 prompt。同文件的 truncateStr 记的是同一个坑
	// 在**写库**方向的后果（PG 直接报 SQLSTATE 22021）。
	const maxRunes = 2000
	if runes := []rune(transcript); len(runes) > maxRunes {
		transcript = string(runes[len(runes)-maxRunes:])
	}
	prompt := fmt.Sprintf(
		"根据下面这场会议的转写与已有摘要，给出最多 3 条「值得进一步了解的方向」或「可以立刻采取的建议」。\n"+
			"严格返回 JSON（不要 markdown）：{\"items\":[{\"title\":\"简短标题\",\"snippet\":\"一句话说明为什么相关或该怎么做\",\"query\":\"用于搜索的关键词\"}]}\n"+
			"要求：title 不超过 20 字；query 用中文关键词，不要包含会议原文的整句；没有值得建议的就返回空数组。\n\n"+
			"已有摘要：\n%s\n\n转写（末尾片段）：\n%s",
		summary, transcript,
	)
	content, _, err := s.llmChatOnce(ctx, r, s.cfg.LLMModel, []aigate.ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		if isTruncatedOutput(err) {
			// ⚠ 截断**不是**调用失败：请求成功了，只是模型没把话说完。
			// 走与 parseRecommendJSON 失败**同一条**路（空列表）——
			// 「模型不听话」不该让整条功能挂掉，也不该让用户以为请求失败了。
			log.Printf("[meeting] recommend output truncated, rendering empty: %v", err)
			return []map[string]any{}, true, nil
		}
		return nil, false, err
	}
	items, fellBack := parseRecommendJSONWithFallback(content)
	return items, fellBack, nil
}

// parseRecommendJSON 解析 LLM 返回的推荐条目。
//
// 容错取向与同文件的 parseSummaryJSON 一致：**模型不听话不能让整条功能挂掉**。
// 解析失败返回空数组（前端据此不渲染这一块），而不是把整段原文当标题塞给用户。
// 单条缺字段只丢那一条 —— 模型少给一个 query 不该让另外两条一起消失。
// parseRecommendJSON 保留单值签名（既有判据直接调它），语义不变。
func parseRecommendJSON(content string) []map[string]any {
	items, _ := parseRecommendJSONWithFallback(content)
	return items
}

// parseRecommendJSONWithFallback 多返回一个「这次是回落」的标志。
//
// ⚠ 关键区分：**解析失败**（回落）与**模型确实没建议**（`{"items":[]}`）
// 返回的都是空切片，但在响应里长得一模一样。
// 只有前者需要告诉用户「这次没拿到」；后者说了就是噪音
// （§30 的不对称原则：宁可少说一句，不可说错一句）。
func parseRecommendJSONWithFallback(content string) ([]map[string]any, bool) {
	// ⚠ 2026-10-08（§223.1）补：**先探 items 键在不在**，再解结构。
	//
	// 第一版只判「JSON 能不能解开」，于是 `{"error":"no candidates"}`、
	// `{"message":"..."}` 这类**合法但没有 items 键**的响应被判成
	// 「模型答了且没有值得建议的建议」——那是在**替模型编造意图**。
	// 用户看到的仍然是一句「模型确实没什么建议」，而真相是这条链路压根
	// 没按契约回话。
	//
	// ⇒ 判据用例会一直带着这个形态：合法 JSON ≠ 遵守了契约。
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(extractJSON(content)), &probe); err != nil {
		return []map[string]any{}, true
	}
	// ⚠ 下面这两道守卫在 Go 里**部分重叠**：items 键缺失时 probe 给出的是
	// **空** RawMessage，json.Unmarshal 对空切片必然报错 ⇒ 第二道也会拦住。
	// 所以变异脚本里「只关掉第一道」的那条**不会红**（它是非隔离变异）。
	// ⇒ 保留第一道是因为它承载**意图**：「合法 JSON ≠ 遵守契约」这句话
	// 必须在这段代码里看得见，否则后来人只会看到「解析失败才回落」，
	// 误以为 `{"error":"no candidates"}` 是模型的正当回答。
	// ⇒ 别把第一道当唯一防线，也别因为它"看起来冗余"就顺手删掉理由。
	rawItems, hasItems := probe["items"]
	if !hasItems {
		return []map[string]any{}, true
	}

	var parsed struct {
		Items []struct {
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
			Query   string `json:"query"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawItems, &parsed.Items); err != nil {
		// items 键在但形状不对（字符串/对象/数字）⇒ 同样是契约违反。
		return []map[string]any{}, true
	}
	out := make([]map[string]any, 0, len(parsed.Items))
	for i, it := range parsed.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		snippet := strings.TrimSpace(it.Snippet)
		query := strings.TrimSpace(it.Query)
		if query == "" {
			query = title
		}
		out = append(out, map[string]any{
			"type":    "web",
			"id":      fmt.Sprintf("llm-rec-%d-%s", i, title),
			"title":   title,
			"snippet": snippet,
			"score":   0.5,
			// 前端 onOpenRelated 见到 url 就新开窗口；没有 url 的 web 条目
			// 会掉进下面的 type 分支而哪个都不匹配 ⇒ 点击静默无反应。
			"url": "https://www.bing.com/search?q=" + neturl.QueryEscape(query),
		})
		if len(out) == 3 {
			break
		}
	}
	// ⚠ 注意这里返回 **false**：模型**答了**且解析成功，哪怕结果是空数组，
	// 那也是「模型认为没有值得建议的建议」，不是「这次没拿到」。
	// 判据（TestRecommendFallbackFlagDistinguishesEmptyFromFailed）钉住这一格：
	// 把它写成 true ⇒ 每次会议都会弹「没拿到建议」，那正是 §30 反对的噪音。
	return out, false
}

func (s *Server) handleMeetingRefine(w http.ResponseWriter, r *http.Request, meetingID string) {
	var body meetingRefineBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Segments) == 0 {
		writeError(w, http.StatusBadRequest, "segments required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)

	// Fail closed before spending an upstream refine call: the meeting must be
	// visible in the caller's workspace, exactly like transcribe/summarize.
	if s.meetingStore != nil {
		if _, err := s.meetingStore.GetScoped(meetingID, uid, workspaceID); err != nil {
			writeError(w, http.StatusNotFound, "meeting not found")
			return
		}
	}

	if s.kxmemory != nil {
		resp, err := s.kxmemory.MeetingRefine(ctx, kxmemory.MeetingRefineRequest{
			MeetingID:   meetingID,
			Segments:    toKxSegments(body.Segments),
			TargetLangs: body.TargetLangs,
		})
		if err == nil {
			result := s.finalizeKxRefine(ctx, uid, workspaceID, meetingID, resp, body.Meta)
			writeJSON(w, http.StatusOK, result)
			return
		}
		log.Printf("[meeting] kxmemory refine fallback for %s: %v", meetingID, err)
	}

	// LLM fallback（同 summary：llmBFF 装配时走 llmChatOnce → 动态网关）
	if s.llmBFF == nil && s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	result, err := s.llmMeetingRefine(ctx, r, body.Segments, body.TargetLangs, body.Meta)
	if err != nil {
		writeError(w, http.StatusBadGateway, "refine failed: "+err.Error())
		return
	}
	result = s.finalizeMeetingRefine(ctx, uid, workspaceID, meetingID, result, body.Meta)
	writeJSON(w, http.StatusOK, result)
}

func toKxSegments(segs []meetingSegmentIn) []kxmemory.MeetingSegment {
	out := make([]kxmemory.MeetingSegment, len(segs))
	for i, s := range segs {
		out[i] = kxmemory.MeetingSegment{
			Speaker: s.Speaker, Text: s.Text, Lang: s.Lang,
			StartMs: s.StartMs, EndMs: s.EndMs,
		}
	}
	return out
}

func (s *Server) llmMeetingSummary(ctx context.Context, r *http.Request, segs []meetingSegmentIn, prev string) (map[string]any, error) {
	transcript := segmentsToText(segs)
	// ★ 2026-10-06 真网关实测后重排：JSON 契约必须**两条分支都写全**。
	//
	// 原实现在 prev != "" 时换成「请更新摘要，严格返回**相同** JSON 格式」，
	// 而「相同」指向的格式在这一条消息里**从没出现过**。真网关连跑五轮：
	// 第 1 轮（有 schema）key_points=2、action_items=0；
	// 第 2 轮起 key_points/action_items **双双归零**，第 3 轮 summary **为空**。
	//
	// 后果直接打在需求上：滚动摘要没有 action_items ⇒
	// 「明天下午三点」这类时间点进不了日程（§10 那条需求）；
	// summary 为空 ⇒ 用户看到空白摘要面板，且不报任何错。
	prompt := buildSummaryPrompt(transcript, prev)
	model := s.cfg.LLMModel
	content, _, err := s.llmChatOnce(ctx, r, model, []aigate.ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		if isTruncatedOutput(err) {
			// 截断与「解析失败」对用户是同一件事：没拿到可用摘要。
			// 归到 emptySummary 这条路（回落原始转写），而不是 502 ——
			// 后者会让用户以为请求失败了，可实际只是模型没说完。
			log.Printf("[meeting] summary output truncated, falling back to raw transcript: %v", err)
			return emptySummary(transcript), nil
		}
		return nil, err
	}
	return parseSummaryJSON(content, transcript)
}

// buildSummaryPrompt 拼会议摘要提示词（首轮 / 滚动两形态）。
//
// ⚠ 抽成纯函数有两个理由，**第二个才是重点**：
//
//  1. 可测。
//
//  2. **延迟测量的导出器必须调它本人**。
//     此前 export_summary_prompt_test.go 因为 llmMeetingSummary 会发网络请求，
//     改成在测试里**手抄**这两行字面量，并在注释里声称
//     「下面断言两条分支的字面量与源码完全一致（拼不上就立刻红）」——
//     而**那条断言从来没有被写出来**。注释在替一个不存在的守卫背书，
//     而 §66 的摘要链延迟测量正是走这条手抄路径走的。
//
//     「函数会发网络请求所以只能手抄」不成立：拆出纯函数后，
//     导出器与生产**共用同一个字符串**，漂移在结构上就不可能发生。
func buildSummaryPrompt(transcript, prev string) string {
	if prev != "" {
		// ⚠ 转写只在「新增转写」这一处出现一次。别再拼一遍 ——
		//   滚动轮每多一次转写，输入就多一份，45s 预算直接翻倍。
		return fmt.Sprintf(
			"已有摘要：\n%s\n\n新增转写：\n%s\n\n请更新摘要。\n严格返回 JSON（不要 markdown），字段与下面完全一致：%s%s",
			prev, transcript, summaryJSONSchema, summaryFieldRules,
		)
	}
	return fmt.Sprintf(
		"请为以下会议转写生成摘要，严格返回 JSON（不要 markdown）：%s%s\n\n转写：\n%s",
		summaryJSONSchema, summaryFieldRules, transcript,
	)
}

// summaryFieldRules 是摘要链的字段要求（**紧凑版**，115 字符）。
//
// ⚠⚠ 2026-10-07（§66.10）：这份常量**撤过一次、又加回来**，因为两次都用了**错的指标**。
//
// 【第一轮错在哪】§66.3 我用「due 字符串**数量**」当指标，量到 haiku 的 due 从 6~7 掉到 2，
// 判成「加规则有害」并**撤回**。那个读数是真的，那个**结论是错的**。
//
// 【真相】`待定` / `未指定` 是**非空字符串** ⇒ 被「数量」指标**计成成功**，
// 而它们在产品侧 `resolveTodoDue` 全部返回 null ⇒ 一条都进不了日程。
// 真网关实抓（BEFORE 臂，29 条非空 due，逐条喂产品侧解析器本人）：
//
//	claude-haiku-4-5   19 条 →  6 条能解析 =  32%
//	glm-5.2             10 条 →  9 条能解析 =  90%
//	claude-haiku-4-5   19 条里 13 条是「待定 / 未指定」占位符
//
// 【用解析率重算同一组 A/B】
//
//	                        haiku              glm
//	BEFORE  569 字符（无规则）  6/19  =  32%     9/10  =  90%
//	COMPACT 684 字符（本常量）  6/6   = 100%     7/7   = 100%
//	610 字符大版（已删）        7/7   = 100%     4/4   = 100%
//
// ⇒ 规则块把「能进日程的 due」从 32% 拉到 **100%**。
//
//	那些「消失的 due」不是被压掉了，是**占位符消失了** ——
//	模型改成**只写真有期限的那些**，这正是我们要的。
//
// 【为什么取 115 字符而不是 610】§66.3 量到大版会让 `glm-5.2` 从
// 「能完成」变成「85s 上限超时」；本版延迟与无规则版**持平**
// （haiku 6.3~7.4s、glm 28.0~32.9s，基线 5.4~7.8s / 28.8~40.1s）。
//
// ★ 教训：量「产出」必须量**产出能不能用**。
//
//	计数会把占位符算成成功，把质量提升读成质量下降 —— 方向正好是反的。
const summaryFieldRules = `

要求：action_items 只放转写里真实提到、且需要有人去做的事，不要放纯讨论与闲聊；due 保留用户原话里的时间表述（如「下周三下午三点」「十一月底之前」），不要自己换算成日期，也不要用「那之前」「到时候」这类指代词。`

// summaryJSONSchema 是会议摘要的 JSON 契约。所有要求模型返回摘要的提示词
// 都必须把它**原文带上**：跨轮次时模型看不到上一条的提示词，
// 「返回相同格式」这类指代会落空（2026-10-06 真网关实测，见 llmMeetingSummary）。
const summaryJSONSchema = `{"summary":"","key_points":[],"action_items":[{"text":"","assignee":"","due":""}],"decisions":[],"open_questions":[]}`

func (s *Server) llmMeetingRefine(ctx context.Context, r *http.Request, segs []meetingSegmentIn, langs []string, meta meetingMetaIn) (map[string]any, error) {
	transcript := segmentsToText(segs)
	langHint := ""
	if len(langs) > 0 {
		langHint = fmt.Sprintf("翻译目标语言：%s。", strings.Join(langs, ", "))
	}
	prompt := buildRefinePrompt(transcript, langHint, meta)
	model := s.cfg.LLMModel
	content, _, err := s.llmChatOnce(ctx, r, model, []aigate.ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		if isTruncatedOutput(err) {
			// 截断 ⇒ 正文多半是半截 JSON。它和 parseRefineJSON 失败是**同一种情况**
			// （拿到的东西不能用），所以归到同一条回落 + refine_fallback 标记，
			// 前端据此说「未生效」，而不是弹一次「精翻失败」。
			log.Printf("[meeting] refine output truncated, falling back to raw transcript: %v", err)
			return refineFallbackPayload(transcript), nil
		}
		return nil, err
	}
	parsed, err := parseRefineJSON(content)
	if err != nil {
		// ⚠ 2026-10-06 补的信号。
		//
		// 解析失败时**不能只**原样返回原始转写：接口照样 200、前端照样
		// 渲染出「精校结果」，用户会以为精校跑过了 —— 而实际上模型返回的
		// JSON 被截断或根本没遵守契约，拿到的是自己的原话。
		//
		// 这是「静默伪装成成功」：与 §11 智能体 truncated 回落、
		// §8 随手记 404 属同一族。降级本身是对的（宁可给原文也别给空），
		// 缺的是**告诉调用方降级发生了**。
		log.Printf("[meeting] llm refine fell back to raw transcript: %v", err)
		return refineFallbackPayload(transcript), nil
	}
	// ⚠⚠ 2026-10-07：termHint 自己也可能造成「静默误改」，必须在这里拦。
	//
	// 实测（TestLiveGatewayRefineFixesASRErrors 配对实验，gpt-4o-mini）：
	//
	//	转写  张伟和李娜都会参加，王强负责做会议纪要。   ← 完全正确
	//	名单  赵敏、孙磊（转写里这两个人一个都没有）
	//	无元数据  原样返回
	//	有元数据  「**赵敏和孙磊**都会参加，王强负责做会议纪要。」
	//
	// 两次往提示词里加禁令都没压住（第二次是「名单上没有的人名多半是正确的」，
	// 复测仍复现）。**结论不是「再写一句提示词」，而是：**
	// 凡是靠提示词约束「不许凭空引入名字」的地方，都必须有代码兜底 ——
	// 提示词是**请求**，代码才是**保证**（与 §23 提醒成敗从不上报、
	// §17 静默降级是同一条纪律的另一面）。
	guarded, rejected := guardRefineResult(parsed, transcript, meta)
	if rejected {
		log.Printf("[meeting] llm refine rejected: refined transcript contains a participant absent from source")
	}
	return guarded, nil
}

// guardRefineResult 对解析后的精校结果做**确定性**兜底检查。
//
// 抽成纯函数是为了让它可测：拦不拦得住这件事不能靠真网关去验，
// 而「调用点有没有真的调用它」由 TestRefineGuardIsWiredUpAtCallSite 单独钉。
// 两者缺一不可 ——
//
//	只测本函数 ⇒ 把调用整段删掉，门照样全绿（§35.6 变异 G1/G2/G3 实测）
//	只测接线 ⇒ 函数体里恒真的判据，抓不到（G4/G5 实测）
func guardRefineResult(parsed map[string]any, transcript string, meta meetingMetaIn) (map[string]any, bool) {
	refined, _ := parsed["refined_transcript"].(string)
	if refined == "" {
		return parsed, false
	}
	// 判据：参会人名单里的词，若出现在精校结果里、却**不在**原始转写里，
	// 那就是模型自己加进去的。命中即整体回落原始转写 + 打标记 ——
	// 宁可这一次不精校，也不能把参会人的名字换掉
	// （§30：误改用户发现不了，漏修一眼能看出）。
	if leak := participantLeak(refined, meta, transcript); leak != "" {
		fb := refineFallbackPayload(transcript)
		fb["refine_rejected"] = true
		// ⚠⚠ §908/D77：`refine_rejected` 现在有**两个**成因，前端要分开说。
		//   不带这个字段时，前端会说「检测到与参会人名单不符的人名」——
		//   而这次拦下的真实原因是「模型回显不完」，用户会被指向一个错的原因。
		//   ⇒ 桶名是对外承诺（`refine-outcome-notice.ts` 自己写着这条纪律）。
		fb["refine_rejected_reason"] = "participant_leak"
		return fb, true
	}
	// ⚠⚠ §907/D76：精校稿明显比原文短 ⇒ 模型在**缩写**，不是在纠错。
	//   原先这里**只有参会人那一条检查**，缩写是全漏的 ——
	//   而缩写的后果比「精校没生效」严重得多：前端
	//   `ingestMeetingArtifacts` 用 `refine.refinedTranscript || meeting.transcript`
	//   建笔记 ⇒ 缩写稿成了**笔记正文** ⇒ §898 起那套段级向量索引到的
	//   也就只有被缩写后的那一部分 ⇒ 长会议尾部**从未被索引过**，且零诊断。
	if reason := refineShrinkage(refined, transcript); reason != "" {
		log.Printf("[meeting] llm refine rejected: %s", reason)
		fb := refineFallbackPayload(transcript)
		fb["refine_rejected"] = true
		fb["refine_rejected_reason"] = "shrinkage" // §908/D77：与名单污染**分开说**
		return fb, true
	}
	return parsed, false
}

// refineMinLenRatio 精校稿最短能到原文的多少（按 rune）。
//
// ⚠⚠ 阈值是**算出来的**，不是拍的：
//
//	误伤余量（真实转写 `.artifacts/asr-s788/seg-asr/merged.txt`，3341 字 / 51 行）：
//	  模型若把每行的 `[姓名] ` 前缀**全部去掉**（本任务只要求纠错、不要求保留说话人）
//	  ⇒ 2 字名损失 7.6%、3 字名损失 9.2% ⇒ 仍高于 0.70，**不会误伤**。
//
//	要拦的是什么（§901 补充的真实字符↔时长读数 271 字/分钟）：
//	  1 小时 ≈ 16,260 字 / 2 小时 ≈ 32,520 字 / 4 小时 ≈ 65,040 字
//	  而这条链要求模型**原样回显**整篇 ⇒ 4 小时 ≈ 65,040 token 的输出，
//	  远超常见 8k/16k 上限 ⇒ 长会议上模型**必然缩写**。
//	  只回显 10% 时（6,504 字）远低于 0.70 ⇒ 被拦下、回落原文。
//
// ⚠ 取 0.70 是**故意宽松**：只拦「灾难性缩写」，不校准「刚好多少算异常」。
//
//	⛔ 这个阈值**未用真网关校准**（网关 key 401）⇒ 已登记为待属主定的一个数。
//	回落方向是**原文**，与本文件既有的取舍一致（「不误改」优先于「多修一点」）。
const refineMinLenRatio = 0.70

// refineMinRunesForRatio 比例判据的**绝对下限**（rune）。
//
// ⚠ 没有它，比例判据会在极短文本上乱判（见 refineShrinkage 里的说明）。
const refineMinRunesForRatio = 200

// refineShrinkage 返回「精校稿被缩写得不像纠错」的说明串；长度正常则返回 ""。
// ⚠ 按 rune 而不是按字节：中文一字 3 字节，按字节比会把正常的纠错判成缩写。
func refineShrinkage(refined, transcript string) string {
	orig := len([]rune(transcript))
	if orig < refineMinRunesForRatio {
		// ⚠⚠ §907：原文太短时**比例没有意义**。
		//   这是被既有判据撞出来的（`meeting_sync_id_upsert_test.go` 的夹具是
		//   15 字原文 / 4 字精校稿，桩数据）—— 一条 15 字的转写本来就不是会议，
		//   「精校后还剩 27%」在它身上既可能是缩写、也可能只是桩，两边分不开。
		//   ⇒ 绝对下限取 200 rune：按 §901 校准的 271 字/分钟，
		//     200 字 ≈ **44 秒发言**，远低于任何真实会议，又高于任何桩夹具。
		return ""
	}
	got := len([]rune(refined))
	if float64(got) >= float64(orig)*refineMinLenRatio {
		return ""
	}
	return fmt.Sprintf("refined transcript shrank to %d/%d runes (%.0f%%, floor %d%%) - "+
		"the model abbreviated instead of correcting, falling back to raw transcript",
		got, orig, float64(got)/float64(orig)*100, int(refineMinLenRatio*100))
}

// participantLeak 返回「**原本不在转写里**、却出现在结果里」的参会人姓名。
//
// ⚠ 2026-10-07（§54.3 / §55）加了一条放行：**模型把同音错改对**，
// 与「模型引入外人」在这个判据下原本完全同形，代价是实测 100% 丢产出。
//
//	真网关实测（glm-5.2，同一段真 ASR 转写）：
//	  原文   …跟王总还有**林兰**开个客户评审会…      名单含 林岚
//	  精校   …跟王总还有**林岚**开个客户评审会…   ← 模型改对了
//	  旧判据 名单里的「林岚」在精校里、不在原文里 ⇒ 判成模型引入外人
//	        ⇒ refine_rejected ⇒ **整份精校 + 全部 3 条行动项被丢弃**
//
// 而 §35 实测的**真危害**是另一个形态：
//
//	原文   张伟和李娜都会参加，王强负责做会议纪要。   ← 完全正确
//	名单   赵敏、孙磊（转写里这两个人一个都没有）
//	精校   **赵敏和孙磊**都会参加，王强负责做会议纪要。  ← 把正确改错
//
// ⇒ 判别信号：**替换掉的那个源名，姓氏位是不是保留下来了。**
//
//	林兰 → 林岚   姓氏位「林」保留   ⇒ 同音错（声母韵母相同，字形不同）
//	张伟 → 赵敏   姓氏位「张」→「赵」  ⇒ 换人了
//	李娜 → 孙磊   姓氏位「李」→「孙」  ⇒ 换人了
//
// 为什么用「姓氏位」而不是拼音：真正的同音信息在前端
// `meeting-roster-homophone.ts` 的 GB2312 拼音表里（§45），
// 在 Go 侧复制一份 6763 字表就是**同一份映射写两遍**（§53 的教训）。
// 而汉字姓名里同音错几乎只错在**名**，不错在姓 —— 姓氏位是保留下来的那个。
//
// ⚠ 这条放行的**边界**，写出来别假装没有：
//   - 「同姓 + 同长度」不蕴含同音。`王总→王伟` 也会被放行。
//     实测里没出现过这种替换，但它是这条规则的真实放行面。
//   - 只在「**源名已从精校里消失**」时成立。若模型只是**追加**了林岚
//     而原文的林兰还在，那就不是替换，是引入 ⇒ 仍然拦。
//
// ⚠ 刻意**只查参会人，不查主题**：
//
//	查参会人 ⇒ 拦住实测复现的那种误改（把转写里的人名换成名单里的人）
//	也拦不住 Polaris 那种**正确修复**吗？—— 拦得住，所以不能查。
//
//	目标组「音译专名」那条正是靠主题里的 `Polaris` 修好的，
//	若把主题也纳入硬拦，那条成功会被自己的守卫判失败（§35.6 同型）。
//	主题造成的另一种误改（模型把标题整段塞进输出）实测只出现在
//	「只给主题、不给名单」的臂上，而那一臂不是要发布的形态。
func participantLeak(refined string, meta meetingMetaIn, transcript string) string {
	if refined == "" {
		return ""
	}
	for _, p := range meta.Participants {
		w := strings.TrimSpace(p)
		if w == "" || strings.Contains(transcript, w) || !strings.Contains(refined, w) {
			continue
		}
		// §55 放行：它替换掉的是转写里一个「同姓等长」的名字。
		if replacedSameSurnameName(transcript, refined, w) {
			continue
		}
		return w
	}
	return ""
}

// replacedSameSurnameName 判断 `want`（精校里出现、原文里没有的名单人名）
// 是不是**替换掉**了转写里某个同姓等长的名字。
//
// 三个条件同时成立才算「替换」：
//  1. 原文里存在同长度切片 `s`，`s[0] == want[0]`（**姓氏位保留**）
//  2. `s` 与 `want` **恰好只差一个字**（同音错通常只错一个音节）
//  3. `s` **不再出现在精校里**（确实被消耗掉了，不是追加了一个新名字）
//
// ⚠ 条件 2 不是「加上更严」而已，**第一版没有它时这条规则是漏的**：
//
//	src「林兰会代表技术部参加」/ refined「林岚华会代表技术部参加」/ 名单「林岚华」
//	——窗口扫到「林兰会」，首字同为「林」就放行了，可它**根本不是人名**
//	（「会」是动词）。汉字姓名里同音错几乎只错在名、不错在姓，
//	长度相同时也只差一个字 ⇒ 两条合起来才把上面这种垃圾窗口挡住。
//
// 对照两组实测形态：
//
//	林兰 → 林岚   同姓、差 1 字、源名消失   ⇒ 放行
//	张伟 → 赵敏   姓氏位就变了             ⇒ 拦
//	林兰会 → 林岚华 同姓但差 2 字           ⇒ 拦（见上）
//	张三 → 李三   姓氏位变了               ⇒ 拦
//
// ⚠ 放行面的诚实边界：「同姓 + 只差一字」不蕴含同音。
//
//	`王总→王伟`、`欧阳明月→欧阳雪月` 这类会被放行。实测里没出现过，
//	但它是这条规则真实的放行面，不假装没有。
//
// 长度 1 的名单项**天然**不在放行面里，不需要额外的长度守卫：
// n==1 时「cand[0] == want[0]」等价于「cand == want」，而 want 不在原文里
// （外层 participantLeak 已保证）⇒ 该切片不可能存在。
// ——第一版**写了** `if len(wr) < 2 { return false }`，变异 N4 删掉它之后
// **全部用例仍然绿**：那是段不承重的代码（等价变异），
// 按「不承重的守卫要删掉」已移除，而不是留着装作它在防护。
func replacedSameSurnameName(transcript, refined, want string) bool {
	wr := []rune(want)
	tr := []rune(transcript)
	n := len(wr)
	if len(tr) < n {
		return false
	}
	for i := 0; i+n <= len(tr); i++ {
		cand := tr[i : i+n]
		if cand[0] != wr[0] {
			continue // 姓氏位变了 ⇒ 换人了
		}
		if hammingRuneDiff(cand, wr) != 1 {
			continue // 差的不止一个字 ⇒ 不是同音错（见上）
		}
		if strings.Contains(refined, string(cand)) {
			continue // 源名还在精校里 ⇒ 是追加，不是替换
		}
		return true
	}
	return false
}

// hammingRuneDiff 数两个等长 rune 切片有多少位不同。
func hammingRuneDiff(a, b []rune) int {
	d := 0
	for i := range a {
		if a[i] != b[i] {
			d++
		}
	}
	return d
}

// refineFallbackPayload 是精校降级时的返回体。
//
// ⚠「合法但空」与「非法」对用户是同一件事，必须归到同一条回落（§21）。
// 这里是两条降级共用的同一个载荷：
// refine_fallback —— JSON 解析失败（§17）
// refine_rejected —— 结果里混进了转写中不存在的人名（本节）
// refineFallbackPayload 是精校降级时的返回体。
//
// ⚠ §426：`refine_input_runes` 是**新增的诊断量**，它让「回落」这件事从
// 「我回落了」变成「我回落了，而且我当时喂进去的是 N 个字」。
//
// 为什么需要它：`ErrOutputTruncated` 只认**输出**侧（finish_reason=length），
// 而**输入**装不下时上游返 4xx，不匹配它 ⇒ 后端 502 ⇒ 前端把同一份超大请求体
// 原样再发一次给 /api/llm/chat ⇒ 同样失败 ⇒ 用户拿回原始转写。
// ⚠ 那两次请求都**拿不到任何说明原因的东西**，于是「会议太长」与「模型没听话」
// 在日志和界面里完全同形。
//
// 按 rune 而不是按字节：中文一字 3 字节，同文件 truncateStr 记的是同一个坑
// （按字节切会落在字的中间）。另外两条链的截断（agent 4000 / recommend 2000）
// 也都是按 rune，所以这个量与它们**同口径**、可以直接比。
//
// ⚠ 它**不**是阈值、**不**触发任何行为分支：#17 的「多长算长」仍然待拍板。
// 这里只是把服务端本来就知道的量记下来，让那个阈值**以后能从日志实测**，
// 而不是继续拿一份猜的数去定架构。
func refineFallbackPayload(transcript string) map[string]any {
	return map[string]any{
		"refined_transcript": transcript,
		"translations":       map[string]string{},
		"structured_minutes": map[string]any{"agenda": []any{}, "decisions": []any{}, "action_items": []any{}, "next_meeting": nil},
		"todos":              []any{},
		"refine_fallback":    true,
		"refine_input_runes": utf8.RuneCountInString(transcript),
	}
}

// buildRefinePrompt 拼精校提示词。
//
// ⚠ 抽成纯函数**不是为了整洁，是为了让它可测**。
//
// 这段提示词里塞着「只改错不改写」四条禁令 + JSON 契约 + 元数据术语表，
// 而它们全部埋在一个发网络请求的函数里 —— 源码字符串扫描是本会话反复
// 踩坑的判据形态（注释一改就失明、格式一改就够不到）。
//
// buildRefinePrompt 是**纯输入纯输出**：给它 transcript/langHint/meta，
// 直接断言返回字符串里主题、参会人、禁令、schema 各自在不在。
// 判据从「源码里有没有这几个字」升级成「**真的拼进最终提示词了没有**」。
// metaTermHint 把会议元数据拼成**术语表**提示块；没有可用信息时返回空串。
//
// ★ §106 抽成具名函数不是为了整洁，是为了**让摘要链能测「要不要也用它」**：
//
//	那个问题原先连问都问不出来 —— 术语表是 buildRefinePrompt 体内的一段局部变量，
//	而摘要链的 buildSummaryPrompt 签名里根本没有 meta ⇒ 无法做配对实验。
//	⚠ 本函数是**纯提取**，输出与提取前逐字相同（§106.3 用门钉住）。
func metaTermHint(meta meetingMetaIn) string {
	var termHint string
	if t := strings.TrimSpace(meta.Title); t != "" {
		termHint += "会议主题：" + t + "。"
	}
	// ⚠ 2026-10-07（§138.3）：主题里装的是**专有名词**（产品/项目/客户名），
	//   而这正是 ASR 错得最多的那一类（实测 §138.3：
	//   `minimax-asr-1.0` 把「悬界芯片」抄成「玄介芯片」，
	//   而「悬界芯片客户评审会」正是仓内语料的主题写法）。
	//
	//   ⚠⚠ 2026-10-07（§146）**更正下面那句曾经的理由**。原文写的是
	//   「放在 Title 后面而不是合并进它，是为了让模型知道这是一条独立可信来源」——
	//   §146 的 A/B 实测（3 臂 × 3 次：OFF 0/3、TITLE 3/3、TOPIC 3/3）显示：
	//   **产品名放在 Title 里一样 3/3**，所以「必须独立成一条」这条理由**不成立**。
	//   ⇒ 现在的说法：独立标签是**未被测过**的保守写法（怕两条并成一条会被合并阅读），
	//     它**不是**「缺了就会坏」—— 留着无害，但别把它的必要性当既成事实。
	//
	//   ⚠ 主题也可能写成一句**不含专有名词**的普通描述
	//   （「本周例会」），那它对纠正没有帮助、也不该造成伤害 ——
	//   这条「无害」由 TestLiveGatewayRefineTermHintTopic 的负控臂实测。
	//
	//   ⚠ 标签刻意与 Title 区分开：Title 已经占用「会议主题：」这几个字，
	//   两个字段共用同一个标签会让模型以为是**同一条信息出现了两次**，
	//   从而降低它对其中一条的注意力。
	if tp := strings.TrimSpace(meta.Topic); tp != "" {
		termHint += "会议议题：" + tp + "。"
	}
	if ps := nonEmpty(meta.Participants); len(ps) > 0 {
		termHint += "参会人：" + strings.Join(ps, "、") + "。"
	}
	// ⚠ 2026-10-07（§64.6）：Location 一直**在请求体里**（前端确实发
	//   `location: meeting?.location`），而这段只读 Title 与 Participants
	//   ⇒ 地点被发过来又静静丢掉，与 §48 那三项同型。
	//
	//   加进来的依据是**实测无害**：真网关 4/4 样本把转写里**没有**的地点词
	//   「星海科技总部三楼」放进元数据，0/4 被塞进精校结果，
	//   原有同音纠正 4/4 未退化、三个时间点 4/4 未被改掉。
	//
	//   ⚠ 只测到「无害」，**没测到「有益」** —— 本轮素材里没有
	//   「地点被听错」的真实样本。要确认它真有用，得再取一段那种转写。
	if l := strings.TrimSpace(meta.Location); l != "" {
		termHint += "会议地点：" + l + "。"
	}
	if termHint != "" {
		termHint = "以下信息是**可信的上下文**，转写中与之高度相似的错误词组应按它纠正：" +
			termHint + "\n注意：只按这份上下文纠正**明显对应**的错，**不要**把原文里的词" +
			"替换成上下文里出现过、但并不对应的词。\n" +
			// ⚠⚠ 2026-10-07 真网关实测加的第二道禁令，不是预防性设计。
			//   配对实验（TestLiveGatewayRefineFixesASRErrors，14 条 × 2 臂）量到：
			//
			//	转写  张伟和李娜都会参加，王强负责做会议纪要。   ← 完全正确
			//	名单  赵敏、孙磊（转写里这两个人一个都没有）
			//	OFF   原样返回
			//	ON    「**赵敏和孙磊**都会参加，王强负责做会议纪要。」
			//
			//   ⇒ 模型把转写里的人名**对齐**进了名单。这不是「没修好」，
			//     这是**把正确的内容改成了错的**，而且用户看不出来
			//     （§30 的不对称原则：误改比漏修危险得多）。
			//   ⇒ 第一条禁令只说了「不要替换成不对应的词」，但模型
			//     把「张伟→赵敏」理解成了「对应的」（因为赵敏在名单里），
			//     于是绕过了禁令。**禁令要堵住这个具体误解，不能只堵通例。**
			"**特别地**：转写里出现、但名单上没有的人名，**多半是正确的**" +
			"（可能是列席者、临时加入的人、或没被记录进名单的人），" +
			"**不要**把它换成名单里的另一个人名。\n"
	}
	return termHint
}

func buildRefinePrompt(transcript, langHint string, meta meetingMetaIn) string {
	// ★ 2026-10-07 补：把**已经随请求带进来的**会议元数据当术语表喂进去。
	//
	// 为什么这是接线不是新功能：meta.Title 与 meta.Participants 一直在
	// handleMeetingRefine 的请求体里（并被转给了 kxmemory），只是
	// LLM 兜底分支**一个都没用**。所以不需要新的数据来源、不需要新的采集。
	//
	// 为什么它能修 §30 测出的那类修不了的错误：§30.2 的分界线是
	// 「拼写接近 vs 毫不相干」。`页百零一`→CI、`接单`→git、
	// `countedgettotal`→calculateTotal 属于后者 —— 语言模型无从推断，
	// 但**给了名单/主题就有了依据**。
	//
	// ⚠ 同时也是对 §30.1 那处改错（`康复`→`根`）的正面回应：
	//   那个错的根是模型**在没有依据的地方乱猜**。给它依据，
	//   猜的冲动应当下降 —— 但**必须实测确认改错数没有上升**，
	//   光看「多修了几处」是不够的（那可能是拿改错换的）。
	termHint := metaTermHint(meta)
	// ★ 2026-10-07 真网关实测后重写提示词。原文写的是「**请润色**以下会议转写」——
	//   「润色」这个词本身就是缺陷的根：它**要求**模型改写文本，而这条链路
	//   的职责是**纠错**，不是改写。实测（TestLiveGatewayRefineFixesASRErrors，
	//   喂 §27 测出的真实 ASR 错误样本）结果：
	//
	//	输入  把user下划线ID改成UID，然后跑一遍页百零一，看有没有回归。
	//	输出  [发言人] 把 user_ID 改成 UID，然后跑一遍 101，看有没有回归。   ← CI 被改成 101
	//	输入  张伟和李娜都会参加，王强负责做会议纪要。          ← 这句本来完全正确
	//	输出  [发言人] 张伟和李娜两位均已确认参加会议；会议纪要由王强负责记录。  ← 被改写
	//	输入  请把接口升级到v2.5版本，并同步更新Challog。
	//	输出  [发言人] 请将接口升级至 v2.5 版本，并同步更新 Changelog（变更日志）。
	//	                                                          ↑ 加了原文没有的内容
	//
	// ⇒ 1 处术语（Tafad→Kafka）修好了，但同时：**把正确的也改了**、
	//   **引入了原文没有的内容**、**把 CI 改成了 101**。
	//   对会议纪要产品来说，「精校结果与原始转写语义偏离」比「不纠错」严重得多 ——
	//   用户无法信任它，也就等于没有它。
	//
	// ⚠⚠ 改写提示词**第一版**还犯了一个错，同一批样本复测才发现：
	//   我在禁令里写了「明显的数字错误」也要纠正，模型于是去**猜数字** ——
	//   原文「下周一就是十号」被改成「十二号」（原文本来是对的）。
	//   它无从知道正确答案是几，猜的那一下必然错，而用户看不出来。
	//   ⇒ 这条已删。**纠正范围必须限定在「能从上下文确定」的错误上。**
	//
	// 改后的实测取舍（同一批样本，第二版）：
	//	纯中文正确句 → 原样保留   ← 核心目标达成（第一版会把它改写掉）
	//	Challog     → Changelog   ← 修好，且不再加「（变更日志）」
	//	十号        → 十二号      ← 已由删数字禁令修掉
	//	Tafad       → 未修        ← 代价：纠错变保守
	//	「不误改」优先于「多修一点」：误改用户发现不了（他不知道原文是什么），
	//	漏修用户一眼能看出来。与 anchorCoverage「宁高不低」是同一条取舍。
	return fmt.Sprintf(
		"%s%s以下是会议录音的自动转写结果，其中可能有语音识别错误。请**纠正明显的识别错误**，"+
			"并严格返回 JSON：{\"refined_transcript\":\"\",\"translations\":{},\"structured_minutes\":"+
			"{\"agenda\":[],\"decisions\":[],\"action_items\":[{\"text\":\"\",\"assignee\":\"\",\"due\":\"\"}],\"next_meeting\":\"\"},"+
			"\"todos\":[{\"text\":\"\",\"assignee\":\"\",\"due\":\"\"}]}\n\n"+
			"★ structured_minutes.action_items 与 todos 的要求（缺了它们，下游拿不到任何东西）：\n"+
			"1. 只放转写里**真实提到、且需要有人去做**的事；纯讨论、闲聊、状态汇报不要放。\n"+
			"2. **text 用用户原话里的动作**（如「准备对比表」「确认预算」），不要改写成更正式的说法。\n"+
			"3. **due 保留用户原话**（如「下周三下午三点」「十一月底之前」），"+
			"**不要自己换算成日期**；转写里没提期限就留空字符串。\n"+
			// ⚠ 2026-10-07（§65）：真网关实测 glm-5.2 把 due 填成 **「那之前」** ——
			//   那是**指代词**，脱离上下文不成立。产品侧 `resolveTodoDue` 实测解析失败
			//   ⇒ 那条行动项**永远进不了日程，且不报任何错**。
			//   所以「保留原话」必须补一句边界：保留的是**时间表述**，不是代词。
			"   ★ **due 必须能脱离上下文独立成立**：转写里的「那之前」「到时候」「之后」"+
			"这类**指代**，要换成它所指的**具体时间**"+
			"（如「对比表要在那之前准备好」且上文是「评审会定在下周三下午三点」"+
			" ⇒ due 写「下周三下午三点之前」）。实在指不出具体时间就留空字符串。\n"+
			"4. assignee 填转写里明确指派的人；没指派就留空字符串，不要猜。\n"+
			"5. 没有可提取的事项时返回空数组 `[]` —— 但请先确认转写里真的没有。\n"+
			"6. action_items 与 todos 内容一致即可（下游两处都读）。\n\n"+
			// ⚠ 2026-10-07（§56）：next_meeting 此前**没有任何要求**，
			// 范例还是 null ⇒ 真网关实测多数轮次返回 null。
			// 而前端 meetings.ts:250 是消费它的 —— 字段在、没人填，
			// 「下周三下午三点开会」这个最直观的日程候选因此拿不到。
			"★ next_meeting 的要求（用户需求：时间点自动加入计划日程）：\n"+
			"1. 若转写里提到**下一次**会议/评审/约见的时间点，"+
			"填成一句带该时间点的短句（可含主题，如「下周三下午三点客户评审会」）。\n"+
			"2. **保留用户原话**里的时间表述，不要自己换算成日期。\n"+
			"3. 只填**下次**那一个；本次会议自己的时间**不算**。\n"+
			"4. 没有下次会议时间点时填空字符串 \"\"，不要填 null，也不要编一个。\n\n"+
			"refined_transcript 的硬性要求：\n"+
			"1. **只改错，不改写**。句子结构、措辞、信息量必须与原文一致；"+
			"拿不准的地方**原样保留**，不要为了通顺而调整。\n"+
			"2. **不得增加原文没有的内容**（包括括注、解释、补充说明）。\n"+
			"3. **不得删除原文有的内容**。\n"+
			"4. **不得改动数字**。你无法从转写判断哪个数字才是对的，猜的那一下必然是错的。\n"+
			"5. 只纠正能从上下文确定的错误：英文单词/缩写/标识符被听错"+
			"（如字母被读成中文谐音、下划线被读成词）、专有名词、同音字。\n"+
			"6. 保留每行开头的 [说话人] 标记，格式与原文一致。\n\n转写：\n%s",
		langHint, termHint, transcript,
	)
}

// segmentsToText 把分段转写拼成送进 LLM 的文本。
//
// ⚠ 2026-10-07（§100）：这里**曾**改成「合并同说话人相邻段」，
// 因为需求原文是「音频是切段的…需要将不同的段放在一起进行校对合并」，
// 而 §81 实测「悬界芯片」仍断在两行。**改动已撤回**，理由见下：
//
// 真网关 A/B（难样本：3 个专名都被切片边界劈开、8 段、3 个时间点）：
//
//	claude-haiku-4-5    旧：缺专名=[CI]           6.5s
//	                   新：缺专名=[CI]           6.0s   ← 完全相同
//	doubao-seed-2-0-mini 旧：finish="length"（截断）14.3s
//	                   新：超时等响应头            ——
//
// ⇒ **合并没有可测收益**，而在那类预算紧的模型上还多了一个超时信号。
//
//	本仓的纪律是「被证伪的结论不许写成门禁」，所以不落这个改动。
//
// 结论另有一层：**§81 那个「仍断行」的观察，大概率不是切段边界造成的** ——
// haiku 在**不合并**（每段各占一行、专名被劈成两行）的情况下照样能把
// 「悬界芯片」拼完整。§81 当时用的是 glm-5.2，而该模型在真实长度上
// 本就经常超时（§66 的 0/3），「仍断行」更可能是那次调用本身没跑完。
//
// ★ 但 §100 有一条**确凿**的副产物：`segmentsToText` 与界面**零耦合**，
//
//	「改它会影响气泡行数」这个挂了很久的顾虑是**假的**
//	（三条证据与机械反证见 segments_merge_gate_test.go 与文档 §100.2）。
//	也就是说：若将来有别的理由要改这个函数，它**不需要**先过「气泡」那一关。
func segmentsToText(segs []meetingSegmentIn) string {
	var b strings.Builder
	for _, s := range segs {
		speaker := s.Speaker
		if speaker == "" {
			speaker = "说话人"
		}
		fmt.Fprintf(&b, "[%s] %s\n", speaker, s.Text)
	}
	return b.String()
}

// stripThinkBlocks 去掉 reasoning 模型在正式输出前吐的 <think>…</think>。
//
// ⚠ 2026-10-09（§338）：这不是洁癖，是**实测会丢整场总结**的缺陷。
//
// 真实失败形状（真网关响应，2026-10-09 23:2x，见 §337.6）：
//
//	<think>
//	…分析…（模型在这里**预告了整份 JSON**）
//	```json
//	{ "summary": "…", … }
//	```
//	</think>
//	```json
//	{ "summary": "…", … }
//	```
//
// 后面两处「第一个 `{` 到最后一个 `}`」的取法会从 think 里的那份预告
// 一路切到真 JSON 的结尾 ⇒ **把两份 JSON 拼在一起** ⇒ Unmarshal 报
// `Extra data` ⇒ 整场总结回落到 emptySummary。
//
// 为什么「未闭合的 think」按原样返回而不是切掉
// ------------------------------------------------
// 未闭合有两种可能：① 模型确实忘了闭合；② 我们把一个跨行的 JSON
// 误判成 think。后者若按「切到末尾」处理，会**丢掉真 JSON** ——
// 用一个未证实的假设换一个已知失败，明显不划算。
// ⇒ 未闭合 ⇒ **原样返回**，行为与修复前逐字相同。
// 这条取舍有负样本钉着（TestStripThinkBlocks）。
//
// ⚠ 全仓有**两处**同款「第一个 { 到最后一个 }」的取法，本函数是它们的
// 单一真源：extractJSON（会议摘要）与 server_email_summary.go（邮件摘要）。
// 两处的**失败语义不同**（前者返回原文、后者返回空串），所以只共享这一步，
// 各自保留后面的取法 —— 不顺手把两边合成一个函数。
func stripThinkBlocks(text string) string {
	for {
		i := strings.Index(text, "<think>")
		if i < 0 {
			return text
		}
		j := strings.Index(text[i:], "</think>")
		if j < 0 {
			return text // 未闭合 ⇒ 原样返回，见上面的理由
		}
		text = text[:i] + text[i+j+len("</think>"):]
	}
}

func extractJSON(text string) string {
	text = stripThinkBlocks(text) // §338：先剥推理段，否则会切出两份 JSON 的拼接
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

// parseSummaryJSONWithFallback 是 parseSummaryJSON 的「带回落标志」版本。
//
// 为什么要多这个出口：parseSummaryJSON 的两条返回路径**都**返回 nil error
// （「JSON 非法」与「合法但空」都回落成 emptySummary + nil），
// 于是调用方拿不到任何信号去区分「模型给的摘要」与「原始转写冒充的摘要」。
// 而这两者对用户是**同一件事**：面板刷新了、徽标写着智能体、内容却不是摘要。
// ⇒ **回落是一个必须能被调用方看见的事实**，不能只存在于返回值的形状里。
//
// fellBack=true 表示「返回的不是摘要，是 emptySummary 回落形状」，
// 调用方据此走自己已经写好的回落路径，而不是把它当成功上抛。
func parseSummaryJSONWithFallback(content, fallback string) (map[string]any, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return emptySummary(fallback), true
	}
	// ⚠ 2026-10-06 补的第二道兜底（判定逻辑原样保留，只多了 fellBack 标志）。
	//
	// 上面那道只拦「JSON 非法」。但模型完全可能返回**合法却空**的 JSON：
	// 真网关连跑滚动摘要时，第 3 轮就返回了 {"summary":"", "key_points":[], …}，
	// 于是 summary 是空串 —— 解析一路绿灯，用户的摘要面板却什么都没有，
	// 连一个错误都不报。
	//
	// 「合法但空」与「非法」对用户是**同一件事**：没拿到摘要。
	// 所以两道必须归到同一处去：宁可回落到原始转写（用户至少认得出
	// 那是自己说过的话），也不要假装成功。
	if s, _ := parsed["summary"].(string); strings.TrimSpace(s) == "" {
		return emptySummary(fallback), true
	}
	return parsed, false
}

// parseSummaryJSON 保留原两值签名，不动它的调用点。
//
// ⚠ 它的 error 返回值因此**恒为 nil**。这不是遗漏，是
// meeting_response_contract_test.go 明确断言「回落分支不应返回 error」
// 所钉住的后果；「有没有拿到摘要」只有 fellBack 标志说得清，
// 新代码请用 parseSummaryJSONWithFallback。
func parseSummaryJSON(content, fallback string) (map[string]any, error) {
	parsed, _ := parseSummaryJSONWithFallback(content, fallback)
	return parsed, nil
}

// emptySummary 是摘要不可用时的统一回落形状。
func emptySummary(fallback string) map[string]any {
	return map[string]any{
		"summary":        truncateStr(fallback, 500),
		"key_points":     []any{},
		"action_items":   []any{},
		"decisions":      []any{},
		"open_questions": []any{},
		// ⚠ 2026-10-08（§220）：**回落必须自带信号**，否则这张 map 与真摘要
		// 在结构上完全一样，调用方分不出来。
		//
		// 它的 summary 字段是**原始转写的前 500 字节**、四个数组全空，
		// 却被 handler 原样广播 + HTTP 200 上抛 ⇒ 用户看到「摘要已更新」，
		// 内容是自己的转写片段，零待办零日程，零错误提示。
		//
		// ⚠ 这张 map 有**两条**生产出口，§218 只堵掉了智能体那条：
		//   ① llmMeetingSummary 的截断分支（isTruncatedOutput）；
		//   ② llmMeetingSummary 末尾的 parseSummaryJSON 回落分支。
		// 两条都必须带这个键，所以信号只能长在 emptySummary 里面。
		//
		// 键名与精校侧的 refine_fallback 同源（同一句话：「我回落了」），
		// 并已进 meeting_response_contract_test.go 的跨端键表。
		"summary_fallback": true,
		// ⚠ §426：与 refineFallbackPayload 里的 refine_input_runes 同源同口径。
		//   这条链（一次性回落）**不带转写截断**（另两条带：agent 4000 /
		//   recommend 2000），所以它的输入规模是四条链里唯一可能失控的
		//   —— 把量记下来是给 #17 的阈值一份可实测的依据，不是提前替它定数。
		"summary_input_runes": utf8.RuneCountInString(fallback),
	}
}

func parseRefineJSON(content string) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return nil, err
	}
	// ⚠ 2026-10-06 补的空值闸（与 parseSummaryJSON 同一类）。
	//
	// 上面只拦「JSON 非法」。模型完全可能返回**合法却空**的
	// {"refined_transcript":"", …} —— 真网关实测滚动摘要时就见过这种形态。
	// 那时这里会返回成功，调用方把**空的精校结果**当精校成果返回：
	// 用户屏幕上「精校结果」一片空白，比回落成原始转写糟糕得多
	// （回落至少还有内容），而且 refine_fallback 不会被置位
	// ⇒ 界面还会照实弹「精翻完成」。
	//
	// 「合法但空」与「非法」对用户是同一件事：没拿到精校结果。
	// 所以归成同一条路：返回 error，让调用方走它已经写好的回落 +
	// refine_fallback 标记。
	if s, _ := parsed["refined_transcript"].(string); strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("refine 返回空 refined_transcript（模型未产出可用精校）")
	}
	return parsed, nil
}

// truncateStr 按字节截断到最多 n 字节，但**回退到完整字符边界**。
//
// 为什么不直接 s[:n]：中文一个字 3 字节，切在字的中间会产出非法 UTF-8。
// 2026-10-02 实测该串被 PostgreSQL 直接拒绝：
//
//	ERROR: invalid byte sequence for encoding "UTF8" (SQLSTATE 22021)
//
// 也就是说后果不是"存成乱码"，而是**写入直接失败**。本函数有三条调用点
// 的结果会入库或返回给客户端，全都会踩到：
//
//	server_email_summary.go:226   邮件 AI 摘要 → SetClassificationScoped 写库
//	server_meeting_note_ingest.go  笔记 snippet → 写库（§939 起搬到这里）
//	server_meeting.go:437         会议摘要兜底 → JSON 返回
//
// 保留"字节上限"是有意的：这些上限都是按字节定的（500/200），改成字符上限
// 会让中文内容的体积涨 3 倍。回退最多丢 3 个字节，代价可以忽略。
//
// 同包 invoice_pdf.go 里的 truncateRunes 是**字符**上限 + 省略号，两种语义
// 各有其用，不要混用。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// handleSttTranscribeBase64 — 支持 base64 音频转写（云端 Groq Whisper）
func decodeAudioBase64(encoded string) ([]byte, error) {
	// 支持 data:audio/webm;base64,xxx 格式
	if idx := strings.Index(encoded, ","); idx >= 0 {
		encoded = encoded[idx+1:]
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// nonEmpty 过滤掉空串与纯空白项。
//
// 参会人列表是用户手填的，里面混着空串和空格是常态；直接把 ", , 张伟"
// 拼进提示词会让那份「可信上下文」本身变脏，模型也会顺着脏上下文猜。
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	return out
}
