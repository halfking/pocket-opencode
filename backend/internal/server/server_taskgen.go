package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/taskgen"
)

// handleMeetingAnalyze 分析会议内容并生成任务
//
// POST /api/meetings/:id/analyze
// 请求体: {"content": "...", "summary": "...", "participants": [...]}
func (s *Server) handleMeetingAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 从URL提取meeting ID
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	meetingID := ""
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "meetings" {
		meetingID = pathParts[2]
	}

	if meetingID == "" {
		writeError(w, http.StatusBadRequest, "meeting ID required")
		return
	}

	// 解析请求
	var req struct {
		Content      string   `json:"content"`
		Summary      string   `json:"summary"`
		Participants []string `json:"participants"`
		Keywords     []string `json:"keywords"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	// 创建任务生成器
	generator := taskgen.NewTaskGenerator(taskgen.Config{
		LLMBaseURL:    s.cfg.LLMGatewayURL,
		LLMAPIKey:     s.cfg.LLMGatewayAPIKey,
		ACCRuntimeURL: s.cfg.ACCRuntimeURL,
		ACCSecret:     s.cfg.ACCRuntimeSecret,
	})

	// 分析并生成任务
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result, err := generator.AnalyzeAndGenerateTasks(ctx, taskgen.AnalyzeRequest{
		Content:      req.Content,
		Summary:      req.Summary,
		Participants: req.Participants,
		Keywords:     req.Keywords,
	})

	if err != nil {
		writeError(w, http.StatusInternalServerError, "analysis failed: "+err.Error())
		return
	}

	// 返回结果
	writeJSON(w, http.StatusOK, result)
}

// handleTaskSubmit 提交任务给智能体
//
// POST /api/tasks/:id/submit
func (s *Server) handleTaskSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 从URL提取task ID
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	taskID := ""
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "tasks" {
		taskID = pathParts[2]
	}

	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task ID required")
		return
	}

	// 解析请求
	var task taskgen.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid task: "+err.Error())
		return
	}

	// 创建任务生成器
	generator := taskgen.NewTaskGenerator(taskgen.Config{
		ACCRuntimeURL: s.cfg.ACCRuntimeURL,
		ACCSecret:     s.cfg.ACCRuntimeSecret,
	})

	// 提交任务
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := generator.SubmitToAgent(ctx, &task); err != nil {
		writeError(w, http.StatusInternalServerError, "submit failed: "+err.Error())
		return
	}

	// 返回结果
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"task_id":       task.ID,
		"agent_task_id": task.AgentTaskID,
		"status":        task.Status,
	})
}

// handleTaskStatus 查询任务状态
//
// GET /api/tasks/:id/status
func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	// 从URL提取task ID
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	taskID := ""
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "tasks" {
		taskID = pathParts[2]
	}

	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task ID required")
		return
	}

	agentTaskID := r.URL.Query().Get("agent_task_id")
	if agentTaskID == "" {
		writeError(w, http.StatusBadRequest, "agent_task_id required")
		return
	}

	// 创建任务生成器
	generator := taskgen.NewTaskGenerator(taskgen.Config{
		ACCRuntimeURL: s.cfg.ACCRuntimeURL,
		ACCSecret:     s.cfg.ACCRuntimeSecret,
	})

	// 查询状态
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, err := generator.GetTaskStatus(ctx, agentTaskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}

	// 返回结果
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"task_id":       taskID,
		"agent_task_id": agentTaskID,
		"status":        status,
	})
}
