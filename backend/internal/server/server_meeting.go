package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// handleMeetingRouter dispatches /api/meetings/{id}/{action}
func (s *Server) handleMeetingRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	// /api/meetings/{id}/summary | recommend | refine
	path := strings.TrimPrefix(r.URL.Path, "/api/meetings/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusNotFound, "meeting action required")
		return
	}
	meetingID := parts[0]
	action := parts[1]

	switch action {
	case "summary":
		s.handleMeetingSummary(w, r, meetingID)
	case "recommend":
		s.handleMeetingRecommend(w, r, meetingID)
	case "refine":
		s.handleMeetingRefine(w, r, meetingID)
	default:
		writeError(w, http.StatusNotFound, "unknown meeting action: "+action)
	}
}

type meetingSegmentIn struct {
	Speaker  string  `json:"speaker"`
	Text     string  `json:"text"`
	Lang     string  `json:"lang"`
	StartMs  int     `json:"start_ms"`
	EndMs    int     `json:"end_ms"`
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

	if s.kxmemory != nil {
		resp, err := s.kxmemory.MeetingRefine(ctx, kxmemory.MeetingRefineRequest{
			MeetingID:   meetingID,
			Segments:    toKxSegments(body.Segments),
			TargetLangs: body.TargetLangs,
		})
		if err == nil {
			result := s.finalizeKxRefine(ctx, uid, meetingID, resp, body.Meta)
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
	result = s.finalizeMeetingRefine(ctx, uid, meetingID, result, body.Meta)
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
			"key_points":      []any{},
			"action_items":    []any{},
			"decisions":       []any{},
			"open_questions":  []any{},
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
