package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// handleScheduledTasks handles the collection endpoint. Ownership is derived
// exclusively from authenticated JWT claims; request headers and payload user
// IDs are never accepted as authority.
func (s *Server) handleScheduledTasks(w http.ResponseWriter, r *http.Request) {
	if s.scheduledTaskStore == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduled task store not configured")
		return
	}
	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		enabledOnly := r.URL.Query().Get("enabled") == "true"
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.scheduledTaskStore.ListTasksScoped(r.Context(), userID, workspaceID, enabledOnly, limit)
		if err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": items})
	case http.MethodPost:
		var input scheduledtask.TaskInput
		if err := decodeScheduledTaskJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		input.Defaults()
		if err := validateTaskInput(input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.validateTaskExecutionAvailability(input.Kind); err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		sch, _ := scheduledtask.NewSchedule(input.ScheduleKind, input.ScheduleExpr, input.Timezone)
		now := time.Now().Unix()
		t := &scheduledtask.Task{
			ID: scheduledtask.NewID(), WorkspaceID: workspaceID, UserID: userID,
			Name: input.Name, Description: input.Description, Kind: input.Kind,
			ScheduleKind: input.ScheduleKind, ScheduleExpr: input.ScheduleExpr,
			Timezone: input.Timezone, Payload: input.Payload, Enabled: *input.Enabled,
			MaxRuns: input.MaxRuns, CooldownSec: input.CooldownSec, TimeoutSec: input.TimeoutSec,
			CreatedAt: now, UpdatedAt: now,
		}
		if sch != nil && t.Enabled {
			t.NextRunAt = sch.ComputeNext(now)
		}
		if err := s.scheduledTaskStore.CreateTask(r.Context(), t); err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		s.Write(r, "scheduler.task.create", t.ID, AuditFields{Success: true, Detail: "created scheduled task"})
		writeJSON(w, http.StatusCreated, t)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleScheduledTaskOperations(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/scheduled-tasks/")
	if strings.HasPrefix(strings.Trim(path, "/"), "preview") {
		s.handleScheduledTaskPreview(w, r)
		return
	}
	if s.scheduledTaskStore == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduled task store not configured")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "missing scheduled task id")
		return
	}
	id := parts[0]
	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	if len(parts) == 1 {
		s.handleScheduledTaskItem(w, r, id, userID, workspaceID)
		return
	}
	if len(parts) == 2 && parts[1] == "runs" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		runs, err := s.scheduledTaskStore.ListRuns(r.Context(), id, userID, workspaceID, limit)
		if err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"runs": runs})
		return
	}
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		if s.scheduledTaskScheduler == nil {
			writeError(w, http.StatusServiceUnavailable, "scheduled task scheduler not configured")
			return
		}
		err := s.scheduledTaskScheduler.TriggerNow(r.Context(), id, userID, workspaceID)
		if err != nil {
			if errors.Is(err, scheduledtask.ErrTaskNotFound) {
				writeError(w, http.StatusNotFound, err.Error())
			} else if errors.Is(err, scheduledtask.ErrDisabled) || errors.Is(err, scheduledtask.ErrStoreUnavailable) {
				writeError(w, http.StatusServiceUnavailable, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		s.Write(r, "scheduler.task.trigger", id, AuditFields{Success: true, Detail: "manual trigger"})
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"triggered": true, "taskId": id})
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) handleScheduledTaskItem(w http.ResponseWriter, r *http.Request, id, userID, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		t, err := s.scheduledTaskStore.GetTaskScoped(r.Context(), id, userID, workspaceID)
		if err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	case http.MethodPatch, http.MethodPut:
		current, err := s.scheduledTaskStore.GetTaskScoped(r.Context(), id, userID, workspaceID)
		if err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		var patch scheduledTaskPatchInput
		if err := decodeScheduledTaskJSON(r, &patch); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		input := patch.merge(current)
		if err := validateTaskInput(input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// PATCH 路径只在客户端真正修改 Kind 时校验执行能力,避免对未改动
		// kind 的 PATCH 返回误导性的 503。Defaults() 留作 POST 专属语义:
		// PATCH 不应把未提供的字段清零或补默认。
		if patch.Kind != nil {
			if err := s.validateTaskExecutionAvailability(input.Kind); err != nil {
				writeError(w, http.StatusServiceUnavailable, err.Error())
				return
			}
		}
		sch, _ := scheduledtask.NewSchedule(input.ScheduleKind, input.ScheduleExpr, input.Timezone)
		next := int64(0)
		if input.Enabled != nil && *input.Enabled && sch != nil {
			next = sch.ComputeNext(time.Now().Unix())
		}
		t, err := s.scheduledTaskStore.UpdateTaskScoped(r.Context(), id, userID, workspaceID, input, next)
		if err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		s.Write(r, "scheduler.task.update", id, AuditFields{Success: true, Detail: "updated scheduled task"})
		writeJSON(w, http.StatusOK, t)
	case http.MethodDelete:
		if err := s.scheduledTaskStore.DeleteTaskScoped(r.Context(), id, userID, workspaceID); err != nil {
			writeScheduledTaskStoreError(w, err)
			return
		}
		s.Write(r, "scheduler.task.delete", id, AuditFields{Success: true, Detail: "deleted scheduled task"})
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET, PATCH, PUT or DELETE only")
	}
}

func (s *Server) handleScheduledTaskPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var input struct {
		ScheduleKind scheduledtask.ScheduleKind `json:"scheduleKind"`
		ScheduleExpr string                     `json:"scheduleExpr"`
		Timezone     string                     `json:"timezone"`
	}
	if err := decodeScheduledTaskJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sch, err := scheduledtask.NewSchedule(input.ScheduleKind, input.ScheduleExpr, input.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scheduledtask.NextRunPreview{Next: sch.Preview(time.Now().Unix())})
}

func decodeScheduledTaskJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	const maxBody = 2 << 20
	limited := io.LimitReader(r.Body, maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(data) > maxBody {
		return fmt.Errorf("request body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *Server) validateTaskExecutionAvailability(kind scheduledtask.Kind) error {
	if kind != scheduledtask.KindLocalAgent && kind != scheduledtask.KindCloudDispatch {
		return nil
	}
	if s == nil || s.orchestrator == nil {
		return fmt.Errorf("%s execution is not configured", kind)
	}
	if kind == scheduledtask.KindLocalAgent {
		local := s.orchestrator.Local()
		if local == nil || !local.IsAvailable() {
			return fmt.Errorf("local_agent execution is not configured")
		}
		return nil
	}
	cloud := s.orchestrator.Cloud()
	if cloud == nil || !cloud.IsAvailable() {
		return fmt.Errorf("cloud_dispatch execution is not configured")
	}
	return nil
}

func validateTaskInput(in scheduledtask.TaskInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("name is required")
	}
	validKind := false
	for _, kind := range scheduledtask.AllKinds() {
		if in.Kind == kind {
			validKind = true
			break
		}
	}
	if !validKind {
		return fmt.Errorf("unsupported task kind %q", in.Kind)
	}
	if _, err := scheduledtask.NewSchedule(in.ScheduleKind, in.ScheduleExpr, in.Timezone); err != nil {
		return err
	}
	if in.MaxRuns < 0 || in.CooldownSec < 0 || in.TimeoutSec <= 0 {
		return fmt.Errorf("maxRuns/cooldownSec must be non-negative and timeoutSec must be positive")
	}
	if len(in.Payload) == 0 || !json.Valid(in.Payload) {
		return fmt.Errorf("payload must be valid JSON")
	}
	return nil
}

func writeScheduledTaskStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, scheduledtask.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, scheduledtask.ErrStoreUnavailable) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
