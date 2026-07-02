package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// handleDiscoverInstances 实例发现 API
// GET /api/opencode/discover
// 触发实例发现，返回发现的实例列表
func (s *Server) handleDiscoverInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.registry == nil {
		http.Error(w, "registry not configured", http.StatusServiceUnavailable)
		return
	}

	// 获取所有实例（包括健康状态）
	instances := s.registry.ListInstances()

	// 返回详细信息
	type InstanceInfo struct {
		ID              string   `json:"id"`
		DisplayName     string   `json:"displayName"`
		Environment     string   `json:"environment"`
		Health          string   `json:"health"`
		Capabilities    []string `json:"capabilities"`
		LastHeartbeat   string   `json:"lastHeartbeat"`
		TaskCount       int      `json:"taskCount"`
		ActiveTaskCount int      `json:"activeTaskCount"`
	}

	result := make([]InstanceInfo, 0, len(instances))
	for _, inst := range instances {
		info := InstanceInfo{
			ID:            inst.ID,
			DisplayName:   inst.DisplayName,
			Environment:   inst.Environment,
			Health:        inst.Health,
			Capabilities:  inst.Capabilities,
			LastHeartbeat: inst.LastHeartbeatAt,
		}

		// 获取任务统计
		if inst.Health == "healthy" && s.opencodeManager != nil {
			sessions, err := s.opencodeManager.GetSessions(r.Context(), inst.ID)
			if err == nil {
				info.TaskCount = len(sessions)
				for _, session := range sessions {
					if session.Status == "busy" || session.Status == "retry" {
						info.ActiveTaskCount++
					}
				}
			}
		}

		result = append(result, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instances": result,
		"total":     len(result),
		"timestamp": time.Now().Unix(),
	})
}

// handleGetInstanceTasks 获取实例的任务列表
// GET /api/opencode/instances/{instance_id}/tasks
func (s *Server) handleGetInstanceTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从路径提取 instance_id
	path := r.URL.Path[len("/api/opencode/instances/"):]
	instanceID := ""
	for i, ch := range path {
		if ch == '/' {
			instanceID = path[:i]
			break
		}
	}

	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	// 获取状态过滤参数
	statusFilter := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// 获取实例的任务（会话）
	sessions, err := s.opencodeManager.GetSessions(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 应用状态过滤
	filteredSessions := sessions
	if statusFilter != "" && statusFilter != "all" {
		filtered := make([]*opencode.CachedSession, 0)
		for _, s := range sessions {
			if s.Status == statusFilter {
				filtered = append(filtered, s)
			}
		}
		filteredSessions = filtered
	}

	// 应用限制
	if len(filteredSessions) > limit {
		filteredSessions = filteredSessions[:limit]
	}

	// 转换为 API 响应格式
	type TaskInfo struct {
		ID           string                 `json:"id"`
		Title        string                 `json:"title"`
		Status       string                 `json:"status"`
		CreatedAt    string                 `json:"createdAt"`
		UpdatedAt    string                 `json:"updatedAt"`
		MessageCount int                    `json:"messageCount"`
		FileChanges  *opencode.FileChangeStats `json:"fileChanges,omitempty"`
		Duration     int64                  `json:"duration"`
		Metadata     map[string]interface{} `json:"metadata,omitempty"`
	}

	tasks := make([]TaskInfo, 0, len(filteredSessions))
	for _, session := range filteredSessions {
		tasks = append(tasks, TaskInfo{
			ID:           session.ID,
			Title:        session.Title,
			Status:       session.Status,
			CreatedAt:    session.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    session.UpdatedAt.Format(time.RFC3339),
			MessageCount: session.MessageCount,
			FileChanges:  session.FileChanges,
			Duration:     session.Duration,
			Metadata:     session.Metadata,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instanceId": instanceID,
		"tasks":      tasks,
		"total":      len(sessions),
		"filtered":   len(tasks),
		"timestamp":  time.Now().Unix(),
	})
}

// handleGetTaskDetail 获取任务详情
// GET /api/opencode/tasks/{task_id}?instance_id=xxx
func (s *Server) handleGetTaskDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从路径提取 task_id
	path := r.URL.Path[len("/api/opencode/tasks/"):]
	taskID := path
	if taskID == "" {
		http.Error(w, "missing task_id", http.StatusBadRequest)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	// 获取任务基本信息
	sessions, err := s.opencodeManager.GetSessions(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var task *opencode.CachedSession
	for _, s := range sessions {
		if s.ID == taskID {
			task = s
			break
		}
	}

	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	// 获取任务历史
	history, err := s.opencodeManager.GetSessionHistory(r.Context(), taskID, 100)
	if err != nil {
		log.Printf("⚠️ 获取任务历史失败: %v", err)
		history = []*opencode.HistoryEvent{} // 降级处理
	}

	// 获取任务摘要
	summary, err := s.opencodeManager.GetSessionSummary(r.Context(), instanceID, taskID)
	if err != nil {
		log.Printf("⚠️ 获取任务摘要失败: %v", err)
		summary = "" // 降级处理
	}

	// 构建详细响应
	type TaskDetail struct {
		ID           string                    `json:"id"`
		InstanceID   string                    `json:"instanceId"`
		Title        string                    `json:"title"`
		Status       string                    `json:"status"`
		CreatedAt    string                    `json:"createdAt"`
		UpdatedAt    string                    `json:"updatedAt"`
		MessageCount int                       `json:"messageCount"`
		FileChanges  *opencode.FileChangeStats `json:"fileChanges"`
		Duration     int64                     `json:"duration"`
		Summary      string                    `json:"summary"`
		History      []*opencode.HistoryEvent  `json:"history"`
		Metadata     map[string]interface{}    `json:"metadata,omitempty"`
	}

	detail := TaskDetail{
		ID:           task.ID,
		InstanceID:   task.InstanceID,
		Title:        task.Title,
		Status:       task.Status,
		CreatedAt:    task.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    task.UpdatedAt.Format(time.RFC3339),
		MessageCount: task.MessageCount,
		FileChanges:  task.FileChanges,
		Duration:     task.Duration,
		Summary:      summary,
		History:      history,
		Metadata:     task.Metadata,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(detail)
}

// handleListAllTasks 列出所有实例的所有任务（聚合视图）
// GET /api/opencode/tasks?status=busy&limit=20
func (s *Server) handleListAllTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	statusFilter := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// 获取所有实例的任务
	allSessions, err := s.opencodeManager.GetAllSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 应用状态过滤
	filteredSessions := allSessions
	if statusFilter != "" && statusFilter != "all" {
		filtered := make([]*opencode.CachedSession, 0)
		for _, s := range allSessions {
			if s.Status == statusFilter {
				filtered = append(filtered, s)
			}
		}
		filteredSessions = filtered
	}

	// 应用限制
	if len(filteredSessions) > limit {
		filteredSessions = filteredSessions[:limit]
	}

	// 转换为响应格式
	type TaskInfo struct {
		ID           string                    `json:"id"`
		InstanceID   string                    `json:"instanceId"`
		Title        string                    `json:"title"`
		Status       string                    `json:"status"`
		CreatedAt    string                    `json:"createdAt"`
		UpdatedAt    string                    `json:"updatedAt"`
		MessageCount int                       `json:"messageCount"`
		FileChanges  *opencode.FileChangeStats `json:"fileChanges,omitempty"`
		Duration     int64                     `json:"duration"`
	}

	tasks := make([]TaskInfo, 0, len(filteredSessions))
	for _, session := range filteredSessions {
		tasks = append(tasks, TaskInfo{
			ID:           session.ID,
			InstanceID:   session.InstanceID,
			Title:        session.Title,
			Status:       session.Status,
			CreatedAt:    session.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    session.UpdatedAt.Format(time.RFC3339),
			MessageCount: session.MessageCount,
			FileChanges:  session.FileChanges,
			Duration:     session.Duration,
		})
	}

	// 按实例分组统计
	instanceStats := make(map[string]int)
	statusStats := make(map[string]int)
	for _, session := range allSessions {
		instanceStats[session.InstanceID]++
		statusStats[session.Status]++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tasks":         tasks,
		"total":         len(allSessions),
		"filtered":      len(tasks),
		"instanceStats": instanceStats,
		"statusStats":   statusStats,
		"timestamp":     time.Now().Unix(),
	})
}
