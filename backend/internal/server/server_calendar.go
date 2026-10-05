package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/calendar"
)

// Calendar HTTP surface.
//
// Ownership comes exclusively from authenticated JWT claims, never from the
// request body or headers — same rule as the rest of the server. That is what
// makes a cross-tenant id read as 404 instead of leaking another tenant's row.

// maxCalendarBody mirrors the scheduled-task limit: this endpoint carries only
// a small text payload, so anything larger is a mistake or an attack.
const maxCalendarBody = 2 << 20

func decodeCalendarJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	limited := io.LimitReader(r.Body, maxCalendarBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(data) > maxCalendarBody {
		return fmt.Errorf("request body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func writeCalendarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, calendar.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, calendar.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// handleCalendarEvents serves GET (list in a range) and POST (create).
func (s *Server) handleCalendarEvents(w http.ResponseWriter, r *http.Request) {
	if s.calendarService == nil || !s.calendarService.Available() {
		writeError(w, http.StatusServiceUnavailable, "calendar store not configured")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		from, to, err := calendarRangeFromQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		entries, err := s.calendarService.Feed(r.Context(), wsID, userID, from, to, true)
		if entries == nil && err != nil {
			// 连日程事件都读不出来 —— 这是真故障，照实报错。
			writeCalendarError(w, err)
			return
		}
		// entries 非空 + err 非空 = 部分来源降级（见 calendar.BuildFeed）：
		// 200 返回已取到的数据，把原因留在服务端日志里，不让用户看到一个
		// 「明明有任务却空空如也」的日历而不自知。
		if err != nil {
			log.Printf("[calendar] feed 部分来源失败（ws=%s user=%s）: %v", wsID, userID, err)
		}
		// envelope 的键名由前端 calendarApi.unwrapEntries 认（认 entries，其余忽略）。
		// 抽成函数是为了让契约测试能对着**真正被返回的那个 map** 断言，
		// 而不是对着测试里自己抄的副本断言 —— 后者与字面量比字面量，永远绿。
		writeJSON(w, http.StatusOK, calendarFeedEnvelope(entries, from, to))
	case http.MethodPost:
		var in calendar.EventInput
		if err := decodeCalendarJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		created, err := s.calendarService.CreateEvent(r.Context(), wsID, userID, in)
		if err != nil {
			writeCalendarError(w, err)
			return
		}
		s.Write(r, "calendar.event.create", created.ID, AuditFields{Success: true, Detail: "created calendar event"})
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// calendarFeedEnvelope 构造 feed 响应体。
//
// 单独抽出来是为了让测试能对着**真正被返回的那个 map** 断言键名。
// 前端 features/calendar/api.ts 的 unwrapEntries 认 `entries`；改了键名而
// 忘了改前端时，接口照常 200、日历却静默变成空的。
func calendarFeedEnvelope(entries []calendar.FeedEntry, from, to int64) map[string]interface{} {
	if entries == nil {
		// nil 切片序列化成 null，而前端 unwrapEntries 的 Array.isArray(null)
		// 是 false —— 它会安全地退回空数组（今天不会白屏），但那让「entries
		// 永远是数组」这条契约变成**碰巧**成立。显式归一化，让它无条件为真。
		entries = []calendar.FeedEntry{}
	}
	return map[string]interface{}{
		"entries":      entries,
		"from":         from,
		"to":           to,
		"serverTimeMs": time.Now().UnixMilli(),
	}
}

// handleCalendarEventOperations serves /api/calendar/events/{id}.
func (s *Server) handleCalendarEventOperations(w http.ResponseWriter, r *http.Request) {
	if s.calendarService == nil || !s.calendarService.Available() {
		writeError(w, http.StatusServiceUnavailable, "calendar store not configured")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/calendar/events/"), "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "event id is required")
		return
	}
	// 单条事件的读写只按 workspace 定位（与 list 的 shared 可见性不同）：
	// 改一个事件必须锁定到它自己的租户，不该因为「同工作区可见」而放行。
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		event, err := s.calendarService.GetEvent(r.Context(), id, wsID)
		if err != nil {
			writeCalendarError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, event)
	case http.MethodPatch, http.MethodPut:
		var in calendar.EventInput
		if err := decodeCalendarJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, err := s.calendarService.UpdateEvent(r.Context(), id, wsID, in)
		if err != nil {
			writeCalendarError(w, err)
			return
		}
		s.Write(r, "calendar.event.update", updated.ID, AuditFields{Success: true, Detail: "updated calendar event"})
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := s.calendarService.DeleteEvent(r.Context(), id, wsID); err != nil {
			writeCalendarError(w, err)
			return
		}
		s.Write(r, "calendar.event.delete", id, AuditFields{Success: true, Detail: "deleted calendar event"})
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET, PATCH, PUT or DELETE only")
	}
}

// defaultCalendarRangeDays is used when the client omits from/to. One month of
// days covers a month grid plus the adjacent-month padding a 42-cell grid needs.
const defaultCalendarRangeDays = 45

// calendarRangeFromQuery reads the [from, to) window in unix seconds.
//
// Absent parameters default to "now → now + defaultCalendarRangeDays" so a
// bare GET returns something useful instead of an error; an explicitly
// malformed value is still an error, because silently substituting a range for
// a typo would show the user a confidently wrong calendar.
func calendarRangeFromQuery(r *http.Request) (int64, int64, error) {
	q := r.URL.Query()
	var from, to int64
	var err error
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		from, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid from: must be unix seconds")
		}
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		to, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid to: must be unix seconds")
		}
	}
	if from == 0 && to == 0 {
		now := time.Now().Unix()
		return now, now + defaultCalendarRangeDays*86400, nil
	}
	if from == 0 {
		from = to - defaultCalendarRangeDays*86400
	}
	if to == 0 {
		to = from + defaultCalendarRangeDays*86400
	}
	if to <= from {
		return 0, 0, fmt.Errorf("to must be greater than from")
	}
	return from, to, nil
}
