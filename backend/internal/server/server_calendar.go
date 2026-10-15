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
		// 200 返回已取到的数据，原因**同时**放进响应体的 partial 键。
		//
		// ⚠ §118：此前这里只有 log.Printf，而 feed.go 的注释写着
		// 「The client degrades, but the operator sees why」——
		// **那句话是假的**：客户端拿到的是 200 + 少了几条，零信号。
		// 用户看到的日历就是「少了任务截止和定时任务」，而界面与真空日历
		// 一模一样。这不是理论风险：feed.go 的同一段注释记着
		// 「tasks has no timezone column 这个坏列名就是这么上线过一次」。
		// ⇒ 降级信号必须活到用户眼前，否则那句注释只是在安慰写代码的人。
		if err != nil {
			log.Printf("[calendar] feed 部分来源失败（ws=%s user=%s）: %v", wsID, userID, err)
		}
		// envelope 的键名由前端 calendarApi.unwrapFeed 认。
		// 抽成函数是为了让契约测试能对着**真正被返回的那个 map** 断言，
		// 而不是对着测试里自己抄的副本断言 —— 后者与字面量比字面量，永远绿。
		writeJSON(w, http.StatusOK, calendarFeedEnvelope(entries, from, to, err != nil))
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
// 前端 features/calendar/api.ts 的 unwrapFeed 认 `entries` 与 `partial`；
// 改了键名而忘了改前端时，接口照常 200、日历却静默变成空的。
//
// ★ partial 恒存在（不为 true 时就是 false），不是「降级时才加键」。
//
//	两种设计的差别在**门能不能证明信号没丢**：按需加键时，
//	「正常响应里没有 partial」与「降级响应漏了 partial」在文本上长得一样，
//	只有跑一次降级分支才验得到；而降级分支要构造一个坏掉的数据源。
//	恒存在则正反两个方向都能在同一条普通用例里断言。
//	（与 §109 的 agent 键相反：那把的缺省是 undefined，因为那里存在
//	  「老数据没有这个键，语义是不知道」的真实状态，这里没有。）
func calendarFeedEnvelope(entries []calendar.FeedEntry, from, to int64, partial bool) map[string]interface{} {
	if entries == nil {
		// nil 切片序列化成 null，而前端 unwrapFeed 的 Array.isArray(null)
		// 是 false —— 它会安全地退回空数组（今天不会白屏），但那让「entries
		// 永远是数组」这条契约变成**碰巧**成立。显式归一化，让它无条件为真。
		entries = []calendar.FeedEntry{}
	}
	return map[string]interface{}{
		"entries":      entries,
		"from":         from,
		"to":           to,
		"serverTimeMs": time.Now().UnixMilli(),
		"partial":      partial,
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
