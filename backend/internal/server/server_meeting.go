package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// handleMeetingRouter dispatches /api/meetings/{id}/{action} and per-meeting
// GET/DELETE. Action set: summary, recommend, refine (kxmemory-backed),
// transcribe (STT) and summarize (rule-based). Bare {id} supports GET/DELETE.
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
	if s.transcriber != nil {
		result, err := s.transcriber.Transcribe(r.Context(), audioData, "meeting.wav")
		if err != nil {
			m.Status = "failed"
			_ = s.meetingStore.UpdateScoped(m, uid, workspaceID)
			writeError(w, http.StatusBadGateway, "transcription failed: "+err.Error())
			return
		}
		m.Transcript = result.Text
	} else {
		m.Transcript = "（STT 未配置，请设置 POCKET_GROQ_API_KEY）"
	}
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
}

func (s *Server) handleMeetingSummary(w http.ResponseWriter, r *http.Request, meetingID string) {
	var body struct {
		Segments    []meetingSegmentIn `json:"segments"`
		PrevSummary string             `json:"prev_summary"`
		Meta        meetingMetaIn      `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Segments) == 0 {
		writeError(w, http.StatusBadRequest, "segments required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
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

	// LLM fallback
	if s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	result, err := s.llmMeetingSummary(ctx, body.Segments, body.PrevSummary)
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
			writeJSON(w, http.StatusOK, resp)
			return
		}
		log.Printf("[meeting] kxmemory recommend fallback for %s: %v", meetingID, err)
	}

	// No kxmemory: return empty (memory search requires kxmemory)
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

func (s *Server) handleMeetingRefine(w http.ResponseWriter, r *http.Request, meetingID string) {
	var body struct {
		Segments    []meetingSegmentIn `json:"segments"`
		TargetLangs []string           `json:"target_langs"`
		Meta        meetingMetaIn      `json:"meta"`
	}
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

	if s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	result, err := s.llmMeetingRefine(ctx, body.Segments, body.TargetLangs)
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

func (s *Server) llmMeetingSummary(ctx context.Context, segs []meetingSegmentIn, prev string) (map[string]any, error) {
	transcript := segmentsToText(segs)
	prompt := fmt.Sprintf(
		"请为以下会议转写生成摘要，严格返回 JSON（不要 markdown）：{\"summary\":\"\",\"key_points\":[],\"action_items\":[{\"text\":\"\",\"assignee\":\"\",\"due\":\"\"}],\"decisions\":[],\"open_questions\":[]}\n\n转写：\n%s",
		transcript,
	)
	if prev != "" {
		prompt = fmt.Sprintf(
			"已有摘要：\n%s\n\n新增转写：\n%s\n\n请更新摘要，严格返回相同 JSON 格式。",
			prev, transcript,
		)
	}
	model := s.cfg.LLMModel
	content, err := s.llm.Chat(ctx, model, []aigate.ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}
	return parseSummaryJSON(content, transcript)
}

func (s *Server) llmMeetingRefine(ctx context.Context, segs []meetingSegmentIn, langs []string) (map[string]any, error) {
	transcript := segmentsToText(segs)
	langHint := ""
	if len(langs) > 0 {
		langHint = fmt.Sprintf("翻译目标语言：%s。", strings.Join(langs, ", "))
	}
	prompt := fmt.Sprintf(
		"%s请润色以下会议转写，严格返回 JSON：{\"refined_transcript\":\"\",\"translations\":{},\"structured_minutes\":{\"agenda\":[],\"decisions\":[],\"action_items\":[],\"next_meeting\":null},\"todos\":[]}\n\n%s",
		langHint, transcript,
	)
	model := s.cfg.LLMModel
	content, err := s.llm.Chat(ctx, model, []aigate.ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}
	parsed, err := parseRefineJSON(content)
	if err != nil {
		return map[string]any{
			"refined_transcript": transcript,
			"translations":       map[string]string{},
			"structured_minutes": map[string]any{"agenda": []any{}, "decisions": []any{}, "action_items": []any{}, "next_meeting": nil},
			"todos":              []any{},
		}, nil
	}
	return parsed, nil
}

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

func extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func parseSummaryJSON(content, fallback string) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return map[string]any{
			"summary":        truncateStr(fallback, 500),
			"key_points":     []any{},
			"action_items":   []any{},
			"decisions":      []any{},
			"open_questions": []any{},
		}, nil
	}
	return parsed, nil
}

func parseRefineJSON(content string) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// handleSttTranscribeBase64 — 支持 base64 音频转写（云端 Groq Whisper）
func decodeAudioBase64(encoded string) ([]byte, error) {
	// 支持 data:audio/webm;base64,xxx 格式
	if idx := strings.Index(encoded, ","); idx >= 0 {
		encoded = encoded[idx+1:]
	}
	return base64.StdEncoding.DecodeString(encoded)
}
