package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// 信封形状：前端 calendarApi.unwrapFeed 认 `entries` 与 `partial`。
// 这里对**处理器真正用的那个构造函数**断言，而不是对着测试里抄的字面量 —
// 后者是恒真的：字面量跟它自己的副本比，永远绿。
func TestCalendarFeedEnvelopeContract(t *testing.T) {
	env := calendarFeedEnvelope([]calendar.FeedEntry{}, 1000, 2000, false)
	for _, key := range []string{"entries", "from", "to", "serverTimeMs", "partial"} {
		if _, ok := env[key]; !ok {
			t.Errorf("feed envelope missing key %q (frontend unwrapFeed depends on it)", key)
		}
	}
	if len(env) != 5 {
		t.Errorf("feed envelope has %d keys, want 5: %v", len(env), env)
	}
	// 序列化后 entries 必须仍是数组：nil 切片会序列化成 null，
	// 而前端的 Array.isArray(null) === false → 静默变成空日历。
	raw, err := json.Marshal(calendarFeedEnvelope(nil, 1, 2, false))
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

// §118：部分来源降级必须**真的**进响应体，且**不能**在正常响应里假装降级。
//
// 两个方向都要验，缺一个就有一整类实现蒙混过关：
//
//	· 只验降级方向 ⇒ partial 恒为 true 的实现（正常时也提示）照样绿；
//	· 只验正常方向 ⇒ partial 恒为 false 的实现（就是本次修复前的真实行为）照样绿。
//
// ⚠ 这条测试的存在理由是 feed.go 里那句曾经为假的注释：
//
//	「The client degrades, but the operator sees why」——当时只有日志。
//	它咬过一次：tasks 表没有 timezone 列这种坏查询，
//	在用户那里就长成「我明明有任务，日历上什么都没有」。
func TestCalendarFeedEnvelopeCarriesPartialSignal(t *testing.T) {
	t.Run("部分来源失败时 partial 为 true", func(t *testing.T) {
		env := calendarFeedEnvelope([]calendar.FeedEntry{{ID: "event:1"}}, 1, 2, true)
		got, ok := env["partial"].(bool)
		if !ok {
			t.Fatalf("partial must be a bool, got %T (%v) — 前端按 === true 判降级", env["partial"], env["partial"])
		}
		if !got {
			t.Error("partial = false while a source failed ⇒ 降级信号又只进了服务端日志")
		}
		// 降级时 entries 仍要有内容：修的是「告诉用户」，不是「把数据也藏起来」。
		if entries, ok := env["entries"].([]calendar.FeedEntry); !ok || len(entries) != 1 {
			t.Errorf("partial 响应仍须带回已取到的 entries, got %v", env["entries"])
		}
	})

	t.Run("全部来源成功时 partial 为 false（不得谎报降级）", func(t *testing.T) {
		env := calendarFeedEnvelope([]calendar.FeedEntry{{ID: "event:1"}}, 1, 2, false)
		got, ok := env["partial"].(bool)
		if !ok {
			t.Fatalf("partial must be a bool, got %T", env["partial"])
		}
		if got {
			t.Error("partial = true on a healthy feed ⇒ 用户会被无根据地告知「数据不全」")
		}
	})

	t.Run("序列化后仍是 JSON 布尔而不是字符串", func(t *testing.T) {
		// 前端读的是 body.partial === true；序列化成 "true" 字符串就恒不等。
		raw, err := json.Marshal(calendarFeedEnvelope(nil, 1, 2, true))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"partial":true`) {
			t.Errorf("partial 必须序列化为 JSON 布尔, got %s", string(raw))
		}
	})
}

// §118：降级信号必须真的从 handler **接到** envelope 上。
//
// ★ 为什么这条是源码自证而不是行为断言（这是本节最该被记住的一处）：
//
//	上一版只测了 calendarFeedEnvelope(…, true) 这个**构造函数**。
//	变异把调用点改成 calendarFeedEnvelope(entries, from, to, false) ——
//	**门全绿**。构造函数是对的，接线是错的，两者在文本与行为上都分不开。
//	（§105 的同款：行数断言验不出「真 upsert 还是盲覆盖」。
//	  §116 的同款：自检覆盖的是不是出事的那一处。）
//
//	行为端到端打 handler 需要一个 Feed 会部分失败的 calendar.Service，
//	而 calendar.NewService 只接受 *pgxpool.Pool；本仓的 go test 不带数据库
//	（57 个包约 20s 跑完）⇒ 这里拿不到。
//
//	⇒ 折中：envelope 的**形状**用行为断言（上一条测试，双向），
//	  handler 的**接线**用字面量自证，并给出它自己的自证判据。
//	  诚实的边界写在这里，别让后人以为这条测的是运行时行为。
func TestCalendarHandlerWiresPartialSignal(t *testing.T) {
	// go test 的工作目录是包目录（与同包的源码自证用同一个约定）。
	const src = "server_calendar.go"
	body, ok := goFuncBodyForTest(src, "handleCalendarEvents")
	if !ok {
		t.Fatalf("取不到 %s 里的 handleCalendarEvents 函数体 —— "+
			"函数被改名或搬走了，请更新这条自证的锚点（别直接删掉这条测试）", src)
	}
	const want = "calendarFeedEnvelope(entries, from, to, err != nil)"
	if !strings.Contains(body, want) {
		t.Errorf("handleCalendarEvents 没有把 err 接到 envelope 上。\n"+
			"找不到接线片段：%s\n"+
			"这正是 §118 修复前的形态：降级只进了服务端日志，客户端零信号。\n"+
			"若确属有意改动，请同步改这条断言并在文档里写明理由。", want)
	}

	// 自证：字面量在整个文件里**恰好出现一次**。
	//   出现 0 次 = 接线没了（上面已报）；出现 ≥2 次 = 有人复制了这条调用，
	//   或者 gofmt 改了空白导致上面那条其实匹配的是另一处 ⇒ 一律要求澄清。
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读 %s 失败（自证的前提没了）: %v", src, err)
	}
	if n := strings.Count(string(raw), want); n != 1 {
		t.Errorf("接线片段在 %s 里出现 %d 次，期望恰好 1 次 —— "+
			"重复出现说明这条断言可能匹配的不是你以为的那一处", src, n)
	}
	// 反向自证：函数体里必须**同时**还有那个降级日志 ——
	// 若有人把日志删了改走别处，上面那条自证就失去「同一个 if 块」的语境。
	if !strings.Contains(body, "[calendar] feed 部分来源失败") {
		t.Error("handleCalendarEvents 里的部分降级日志不见了 —— " +
			"请确认降级仍然被记录，否则操作员侧彻底看不到原因")
	}
}

// goFuncBodyForTest 按大括号配平截出函数体。
// ⚠ 与本文件其它断言同源纪律：截不到就报错，**绝不**返回空串继续跑
//
//	（空串会让所有 strings.Contains 断言变成「没找到」，看着像断线，
//	实际是量具坏了 —— 形状与真缺陷无法区分）。
func goFuncBodyForTest(path, name string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	src := string(raw)
	idx := strings.Index(src, "func (s *Server) "+name+"(")
	if idx < 0 {
		return "", false
	}
	start := strings.Index(src[idx:], "{")
	if start < 0 {
		return "", false
	}
	start += idx
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1], true
			}
		}
	}
	return "", false
}
