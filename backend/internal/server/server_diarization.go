package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/diarization"
)

// handleMeetingDiarize 处理会议说话人分离请求
//
// POST /api/meetings/:id/diarize
// 请求体: 无（从会议关联的音频文件读取）
// 或 multipart/form-data: audio=音频文件
func (s *Server) handleMeetingDiarize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 从URL路径提取meeting ID: /api/meetings/:id/diarize
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	meetingID := ""
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "meetings" {
		meetingID = pathParts[2]
	}

	if meetingID == "" {
		writeError(w, http.StatusBadRequest, "meeting ID required")
		return
	}

	// 检查说话人分离服务是否配置
	if s.diarizationClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": "diarization service not configured",
		})
		return
	}

	// 从请求读取音频（multipart上传）
	err := r.ParseMultipartForm(100 << 20) // 100MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse multipart: "+err.Error())
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio file required")
		return
	}
	defer file.Close()

	// 读取音频数据
	audioData, err := io.ReadAll(io.LimitReader(file, 100<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read audio: "+err.Error())
		return
	}

	// 调用说话人分离服务
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := s.diarizationClient.Diarize(ctx, diarization.DiarizeRequest{
		Audio:       audioData,
		Filename:    header.Filename,
		MinSpeakers: 1,
		MaxSpeakers: 10,
	})

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// 返回说话人分离结果
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"segments":      result.Segments,
		"speakerCount":  result.SpeakerCount,
		"durationMs":    result.DurationMS,
		"processTimeMs": result.ProcessTimeMS,
	})
}

// handleDiarizeHealth 检查说话人分离服务健康状态
//
// GET /api/diarize/health
func (s *Server) handleDiarizeHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	if s.diarizationClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    "not configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err := s.diarizationClient.Health(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
	})
}
