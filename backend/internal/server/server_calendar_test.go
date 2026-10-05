package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/calendar"
)

// 日历路由的 HTTP 层契约。未注入 calendarService 时必须 503 而不是 200+空
// 日历 —— 空日历与正常日历在界面上长得一样，503 至少让用户知道要配数据库。

func TestCalendarRoutesRequireAuth(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	for _, path := range []string{"/api/calendar/events", "/api/calendar/events/cal_x"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			t.Errorf("%s: unauthenticated request must not be served, got 200", path)
		}
	}
}

func TestCalendarEventsWithoutStoreIs503(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?from=1000&to=2000", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when the calendar store is absent, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCalendarEventOperationsWithoutStoreIs503(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events/cal_x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCalendarRejectsUnsupportedMethod(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodPut, "/api/calendar/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// 没注入 store 时先撞 503；有 store 时才轮到方法校验。这条只断言
	// **不是 200**，避免在两种实现下都成立却什么都不验。
	if rr.Code == http.StatusOK {
		t.Fatalf("PUT /api/calendar/events must not succeed, got 200")
	}
}

// calendarRangeFromQuery 的默认值与错误分支。这里不需要数据库。
func TestCalendarRangeFromQuery(t *testing.T) {
	t.Run("缺省时给出可用的默认窗口", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/calendar/events", nil)
		from, to, err := calendarRangeFromQuery(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if to <= from {
			t.Fatalf("default range must be non-empty, got %d..%d", from, to)
		}
		// 45 天，覆盖 42 格月宫格。
		if got := to - from; got != defaultCalendarRangeDays*86400 {
			t.Errorf("default span = %d seconds, want %d", got, defaultCalendarRangeDays*86400)
		}
	})

	t.Run("显式区间被采用", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?from=1000&to=2000", nil)
		from, to, err := calendarRangeFromQuery(req)
		if err != nil {
			t.Fatal(err)
		}
		if from != 1000 || to != 2000 {
			t.Errorf("got %d..%d, want 1000..2000", from, to)
		}
	})

	t.Run("只给 from 时补一个默认长度", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?from=1000", nil)
		from, to, err := calendarRangeFromQuery(req)
		if err != nil {
			t.Fatal(err)
		}
		if from != 1000 || to <= from {
			t.Errorf("got %d..%d", from, to)
		}
	})

	t.Run("只给 to 时向前补", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?to=9000", nil)
		from, to, err := calendarRangeFromQuery(req)
		if err != nil {
			t.Fatal(err)
		}
		if to != 9000 || from >= to {
			t.Errorf("got %d..%d", from, to)
		}
	})

	t.Run("畸形数字报错而不是静默换成默认窗口", func(t *testing.T) {
		for _, q := range []string{"?from=abc", "?to=xyz", "?from=abc&to=2000"} {
			req := httptest.NewRequest(http.MethodGet, "/api/calendar/events"+q, nil)
			if _, _, err := calendarRangeFromQuery(req); err == nil {
				t.Errorf("%s must be rejected", q)
			}
		}
	})

	t.Run("倒序区间报错", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?from=2000&to=1000", nil)
		if _, _, err := calendarRangeFromQuery(req); err == nil {
			t.Error("reversed range must be rejected")
		}
	})
}

func TestCalendarCreateRejectsUnknownFields(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	// 客户端若多发一个字段必须报错，而不是被静默忽略 —— 那会让「我改了设置
	// 但没生效」这类问题极难排查。
	body := `{"title":"x","startAt":1000,"endAt":2000,"allDay":false,"timezone":"Asia/Shanghai","visibility":"private","surpriseField":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/calendar/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// 未注入 store 时先撞 503；有 store 时应为 400。两者都不是 2xx。
	if rr.Code == http.StatusOK || rr.Code == http.StatusCreated {
		t.Fatalf("unknown field must not be accepted, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCalendarCreateRequiresBody(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodPost, "/api/calendar/events", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusCreated {
		t.Fatalf("empty body must not create an event, got 201")
	}
}

// 信封形状：前端 calendarApi.unwrapEntries 只认 `entries`。
// 这里对**处理器真正用的那个构造函数**断言，而不是对着测试里抄的字面量 —
// 后者是恒真的：字面量跟它自己的副本比，永远绿。
func TestCalendarFeedEnvelopeContract(t *testing.T) {
	env := calendarFeedEnvelope([]calendar.FeedEntry{}, 1000, 2000)
	for _, key := range []string{"entries", "from", "to", "serverTimeMs"} {
		if _, ok := env[key]; !ok {
			t.Errorf("feed envelope missing key %q (frontend unwrapEntries depends on it)", key)
		}
	}
	if len(env) != 4 {
		t.Errorf("feed envelope has %d keys, want 4: %v", len(env), env)
	}
	// 序列化后 entries 必须仍是数组：nil 切片会序列化成 null，
	// 而前端的 Array.isArray(null) === false → 静默变成空日历。
	raw, err := json.Marshal(calendarFeedEnvelope(nil, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Entries *[]map[string]interface{} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Entries == nil {
		t.Error("entries must serialize as [] not null — nil slice would make Array.isArray() false in the client")
	}
}
