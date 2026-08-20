package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// handleMeetings dispatches GET (list) / POST (create) on /api/meetings.
func (s *Server) handleMeetings(w http.ResponseWriter, r *http.Request) {
	if s.meetingStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleListMeetings(w, r)
	case http.MethodPost:
		s.handleCreateMeeting(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleListMeetings returns the workspace-scoped list with a total counter so
// the mobile UI can render pagination without a second round-trip.
func (s *Server) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	list, err := s.meetingStore.ListScoped(uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list meetings: "+err.Error())
		return
	}
	if list == nil {
		list = []*meeting.Meeting{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"meetings": list,
		"total":    len(list),
	})
}

// handleCreateMeeting decodes the request body and stores a new meeting in the
// caller's workspace scope.
func (s *Server) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	var req meeting.CreateMeetingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "未命名会议"
	}
	m, err := s.meetingStore.CreateScoped(req, uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create meeting: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
	s.wsHub.Broadcast("meeting.synced", map[string]string{"meetingId": m.ID, "userId": uid})
}

// finalizeMeetingRefine 精翻完成后：笔记入库 + 待办任务 + 更新 meeting 缓存
func (s *Server) finalizeMeetingRefine(
	ctx context.Context,
	uid, meetingID string,
	result map[string]any,
	meta meetingMetaIn,
) map[string]any {
	noteID := strVal(result["note_id"])
	if noteID == "" {
		noteID = strVal(result["noteId"])
	}
	refined := strVal(result["refined_transcript"])
	if refined == "" {
		refined = strVal(result["refinedTranscript"])
	}

	if noteID == "" && refined != "" {
		if id, err := s.ingestMeetingNote(ctx, uid, meetingID, refined, meta); err == nil {
			noteID = id
		} else {
			log.Printf("[meeting] note ingest failed %s: %v", meetingID, err)
		}
	}

	todos := extractTodos(result)
	tasksCreated := s.ingestMeetingTasks(ctx, uid, meetingID, todos)

	if noteID != "" {
		result["note_id"] = noteID
	}
	result["tasks_created"] = tasksCreated

	// 更新 PG meeting 缓存
	if s.meetingStore != nil {
		uidForStore := uid
		if uidForStore == "" {
			uidForStore = "local"
		}
		// finalizeMeetingRefine doesn't carry a *http.Request — fall back to the
		// legacy single-tenant default workspace so the store check still works
		// for non-S0 callsites.
		workspaceIDForStore := "default"
		if existing, err := s.meetingStore.GetScoped(meetingID, uidForStore, workspaceIDForStore); err == nil {
			if meta.Title != "" {
				existing.Title = meta.Title
			}
			existing.Status = "refined"
			if err := s.meetingStore.UpdateScoped(existing, uidForStore, workspaceIDForStore); err != nil {
				log.Printf("[meeting] update after refine %s: %v", meetingID, err)
			}
		} else {
			// Fallback for legacy clients: create a stub so cloud list still surfaces the meeting.
			created, cerr := s.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: meta.Title}, uidForStore, workspaceIDForStore)
			if cerr != nil {
				log.Printf("[meeting] create stub after refine %s: %v", meetingID, cerr)
			} else {
				created.Status = "refined"
				if uerr := s.meetingStore.UpdateScoped(created, uidForStore, workspaceIDForStore); uerr != nil {
					log.Printf("[meeting] update stub after refine %s: %v", meetingID, uerr)
				}
			}
		}
	}

	s.wsHub.Broadcast("meeting.refined", map[string]any{
		"meetingId": meetingID, "noteId": noteID, "tasksCreated": tasksCreated,
	})
	return result
}

func (s *Server) ingestMeetingNote(
	ctx context.Context, uid, meetingID, content string, meta meetingMetaIn,
) (string, error) {
	if s.notesStore == nil {
		return "", fmt.Errorf("notes store not configured")
	}
	title := meta.Title
	if title == "" {
		title = "会议纪要"
	}
	now := time.Now().Unix()
	n := notes.Note{
		ID:             randomID("note"),
		UserID:         uid,
		Title:          title,
		Snippet:        truncateStr(content, 500),
		ContentType:    "voice",
		Domain:         "work",
		CreatedByVoice: true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.notesStore.Upsert(ctx, &n); err != nil {
		return "", err
	}
	s.wsHub.Broadcast("note.created", &n)
	go s.classifyNoteAsync(n)
	log.Printf("[meeting] created note %s from meeting %s", n.ID, meetingID)
	return n.ID, nil
}

func (s *Server) ingestMeetingTasks(ctx context.Context, uid, meetingID string, todos []todoItem) int {
	if s.taskStore == nil || len(todos) == 0 {
		return 0
	}
	created := 0
	for _, td := range todos {
		if strings.TrimSpace(td.Text) == "" {
			continue
		}
		t := &task.Task{
			ID:          randomID("task"),
			Title:       td.Text,
			Description: fmt.Sprintf("来自会议 %s", meetingID),
			Status:      "pending",
			Priority:    mapTodoPriority(td.Priority),
			Source:      "meeting",
		}
		if err := s.taskStore.CreateTask(ctx, t); err != nil {
			log.Printf("[meeting] create task failed: %v", err)
			continue
		}
		created++
		s.wsHub.Broadcast("task.created", t)
	}
	if created > 0 {
		log.Printf("[meeting] created %d tasks from meeting %s (user=%s)", created, meetingID, uid)
	}
	return created
}

type todoItem struct {
	Text     string
	Priority string
	Due      string
}

func extractTodos(result map[string]any) []todoItem {
	var out []todoItem
	for _, key := range []string{"todos", "action_items"} {
		raw, ok := result[key]
		if !ok {
			continue
		}
		arr, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, item := range arr {
			switch v := item.(type) {
			case string:
				if v != "" {
					out = append(out, todoItem{Text: v})
				}
			case map[string]any:
				text := strVal(v["text"])
				if text == "" {
					continue
				}
				out = append(out, todoItem{
					Text:     text,
					Priority: strVal(v["priority"]),
					Due:      strVal(v["due"]),
				})
			}
		}
	}
	// structured_minutes.action_items
	if sm, ok := result["structured_minutes"].(map[string]any); ok {
		if arr, ok := sm["action_items"].([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					text := strVal(m["text"])
					if text != "" {
						out = append(out, todoItem{Text: text, Priority: strVal(m["priority"])})
					}
				}
			}
		}
	}
	return out
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func mapTodoPriority(p string) string {
	switch strings.ToLower(p) {
	case "urgent", "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

// kxmemory refine 响应转 map 并 ingest
func (s *Server) finalizeKxRefine(
	ctx context.Context, uid, meetingID string,
	resp *kxmemory.MeetingRefineResponse, meta meetingMetaIn,
) map[string]any {
	result := map[string]any{
		"refined_transcript": resp.RefinedTranscript,
		"translations":       resp.Translations,
		"structured_minutes": resp.StructuredMinutes,
		"todos":              resp.Todos,
	}
	if resp.NoteID != "" {
		result["note_id"] = resp.NoteID
	}
	return s.finalizeMeetingRefine(ctx, uid, meetingID, result, meta)
}
