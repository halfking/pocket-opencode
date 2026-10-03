package server

// /api/learning — the Learning Core HTTP contract
// (docs/学习muse/04-数据模型与API契约.md §2.2).
//
// Route shape:
//
//	POST   /api/learning/items                  capture a material (idempotent)
//	GET    /api/learning/items                  list captured materials
//	GET    /api/learning/items/due              today's due summary
//	PATCH  /api/learning/items/{id}             move an item through the funnel
//	POST   /api/learning/reminders              create/update a reminder
//	GET    /api/learning/reminders              list reminders
//	POST   /api/learning/reminders/{id}/snooze  push it into the future
//	POST   /api/learning/reminders/{id}/ack     silence it for good
//	POST   /api/learning/schedule               server-authoritative FSRS step
//
// The user identity always comes from the JWT (s.userIDFromRequest); a body
// that carries userId is ignored, matching the flashcards handler contract.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/learning"
)

// handleLearningRouter dispatches everything under /api/learning/.
//
// The sub-paths are matched exactly, in the order documented in
// docs/学习muse/04-数据模型与API契约.md §2.2. Anything unknown is a 404 —
// silently treating a typo as "list" is how reminders get duplicated.
func (s *Server) handleLearningRouter(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/learning/"), "/")
	switch {
	case rest == "":
		writeError(w, http.StatusNotFound, "not found")
	case rest == "schedule":
		s.handleLearningSchedule(w, r)
	case rest == "streak":
		s.handleLearningStreak(w, r)
	case rest == "items" || rest == "items/due":
		s.handleLearningItemsRouter(w, r, rest)
	case rest == "reminders":
		s.handleLearningReminders(w, r)
	case strings.HasPrefix(rest, "reminders/"):
		s.handleLearningReminderOps(w, r, strings.TrimPrefix(rest, "reminders/"))
	case strings.HasPrefix(rest, "items/"):
		// /items/{id} — stage transition. Matched before the default 404.
		id := strings.Trim(strings.TrimPrefix(rest, "items/"), "/")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if s.learningService == nil {
			writeError(w, http.StatusServiceUnavailable, "learning store not configured")
			return
		}
		s.learningUpdateItem(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// handleLearningItemsRouter serves /api/learning/items and its sub-resources.
func (s *Server) handleLearningItemsRouter(w http.ResponseWriter, r *http.Request, rest string) {
	if s.learningService == nil {
		writeError(w, http.StatusServiceUnavailable, "learning store not configured")
		return
	}
	if rest == "items/due" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		s.learningDueSummary(w, r)
		return
	}
	if rest == "items" {
		s.handleLearningCollection(w, r)
		return
	}
	// /items/{id} — stage transition.
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.learningUpdateItem(w, r, parts[1])
}

func (s *Server) handleLearningCollection(w http.ResponseWriter, r *http.Request) {
	if s.learningService == nil {
		writeError(w, http.StatusServiceUnavailable, "learning store not configured")
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.learningCapture(w, r)
	case http.MethodGet:
		s.learningListItems(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) learningCapture(w http.ResponseWriter, r *http.Request) {
	var req learning.CaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	item, existed, err := s.learningService.Capture(
		r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r), req, func() string {
			return "litem-" + generateUUID()
		})
	if err != nil {
		// Validation and store failures share one code here; the message
		// distinguishes them for the client without leaking internals.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if existed {
		// Re-capturing the same source is not an error: the client gets 200
		// so a double tap does not surface as a failure toast.
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(item)
}

func (s *Server) learningListItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.learningService.List(
		r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r),
		q.Get("stage"), q.Get("sourceKind"), ParseLimit(q.Get("limit"), 50, 200))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (s *Server) learningDueSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.learningService.DueSummary(
		r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r))
	if err != nil {
		log.Printf("[learning] due summary failed: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

func (s *Server) learningUpdateItem(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "PATCH only")
		return
	}
	var body struct {
		Stage string `json:"stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !learning.ValidStage(body.Stage) {
		writeError(w, http.StatusBadRequest, "stage must be one of inbox|learning|review|mastered|archived")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)
	if err := s.learningStore().UpdateStage(r.Context(), wsID, userID, id, body.Stage); err != nil {
		if errors.Is(err, learning.ErrNotFound) {
			writeError(w, http.StatusNotFound, "learning item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "stage": body.Stage})
}

func (s *Server) handleLearningReminders(w http.ResponseWriter, r *http.Request) {
	if s.learningService == nil {
		writeError(w, http.StatusServiceUnavailable, "learning store not configured")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req learning.UpsertReminderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		reminder, err := s.learningService.UpsertReminder(
			r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r), req, func() string {
				return "lrem-" + generateUUID()
			})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(reminder)
	case http.MethodGet:
		reminders, err := s.learningService.ListReminders(
			r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r),
			r.URL.Query().Get("state"), ParseLimit(r.URL.Query().Get("limit"), 50, 200))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"reminders": reminders})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleLearningReminderOps(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id, op := parts[0], parts[1]
	// Shape check before the store check: an unknown operation is a routing
	// error (404) regardless of whether the service is configured, and it must
	// not be reported as a configuration problem.
	if op != "snooze" && op != "ack" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if s.learningService == nil {
		writeError(w, http.StatusServiceUnavailable, "learning store not configured")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	switch {
	case op == "snooze" && r.Method == http.MethodPost:
		var body struct {
			Minutes int64 `json:"minutes"`
		}
		// An empty body means "one hour", which is the only sensible default
		// for a snooze button.
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Minutes == 0 {
			body.Minutes = 60
		}
		until, err := s.learningService.SnoozeReminder(r.Context(), wsID, userID, id, body.Minutes)
		if err != nil {
			if errors.Is(err, learning.ErrNotFound) {
				writeError(w, http.StatusNotFound, "reminder not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "snoozedUntil": until})

	case op == "ack" && r.Method == http.MethodPost:
		if err := s.learningService.AckReminder(r.Context(), wsID, userID, id); err != nil {
			if errors.Is(err, learning.ErrNotFound) {
				writeError(w, http.StatusNotFound, "reminder not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "state": "acked"})

	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// handleLearningStreak serves GET /api/learning/streak.
//
// tz_offset is the client's seconds-east-of-UTC offset. The learning tables
// store unix seconds with no timezone column, so the day boundary cannot be
// derived server-side; an absent or unparseable value means UTC days rather
// than an error, because a wrong-but-plausible streak is a better failure mode
// than a 400 on a cosmetic query parameter.
func (s *Server) handleLearningStreak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.learningService == nil {
		writeError(w, http.StatusServiceUnavailable, "learning store not configured")
		return
	}
	var tzOffset int64
	if v := r.URL.Query().Get("tz_offset"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < -14*3600 || parsed > 14*3600 {
			writeError(w, http.StatusBadRequest, "tz_offset must be seconds east of UTC within ±14h")
			return
		}
		tzOffset = parsed
	}
	view, err := s.learningService.Streak(r.Context(), s.workspaceIDFromRequest(r), s.userIDFromRequest(r), tzOffset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

// handleLearningSchedule exposes the server-side scheduler so a client can ask
// for the next state of a card without shipping a second FSRS implementation.
// Per ADR-002 this is additive: the existing client-side preview still runs,
// but the server value is the authority.
func (s *Server) handleLearningSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var in learning.ScheduleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.Rating < learning.RatingAgain || in.Rating > learning.RatingEasy {
		writeError(w, http.StatusBadRequest, "rating must be 1..4")
		return
	}
	if in.State < learning.StateNew || in.State > learning.StateRelearning {
		writeError(w, http.StatusBadRequest, "state must be 0..3")
		return
	}
	if in.Now == 0 {
		in.Now = time.Now().UTC().Unix()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(learning.Schedule(in))
}

// learningStore exposes the underlying store for the handlers that need a
// single-row write (stage transitions) without duplicating the service.
func (s *Server) learningStore() *learning.Store {
	if s.learningService == nil {
		return nil
	}
	return s.learningService.Store()
}
