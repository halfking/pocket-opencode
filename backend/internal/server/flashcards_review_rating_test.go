package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/flashcards"
)

// BUG-M 回归锁（2026-09-30，由 scripts/probe-write-methods.mjs 的 method 级
// 探测发现，不是靠读代码看出来的）。
//
// 现象：对 POST /api/flashcards/cards/:id/review 发空 body，实测
//
//	500 {"error":"invalid rating 0 (must be 1..4)"}
//
// rating 越界是**客户端输入错误**，却走了 store 的 error 分支被归为 500。
// 危害有两处，都不是"返回码不好看"这种纯观感问题：
//   1. 前端 ApiError.retryable 依 5xx 判定可重试，会对一个永远不可能成功的
//      请求反复重试（空 body 的重试仍然是空 body）；
//   2. 任何按 5xx 计服务端故障率的看板/告警，都会把一次参数错误计进去。
//
// 判据：非法 rating 必须在**进 store 之前**被 400 挡住。
// 用零值 Store 断言 —— 若请求真的落到了 store，nil pool 会 panic 而非返回
// 干净的 400，这本身就是"拦截点正确"的额外证据。
func TestFlashcardsReviewRejectsInvalidRatingAsBadRequest(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.flashcardStore != nil {
		t.Skip("test server already has a flashcard store")
	}
	srv.SetFlashcardStore(&flashcards.Store{})
	token := tokens[""]

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost,
			"/api/flashcards/cards/card-1/review", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	t.Run("rating 0 (empty body) is 400 not 500", func(t *testing.T) {
		rr := post(`{}`)
		if rr.Code == http.StatusInternalServerError {
			t.Fatalf("POST review with empty body = 500: client input error misclassified as server fault (BUG-M regressed)")
		}
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("got %d %s, want 400", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "invalid rating") {
			t.Errorf("body %s should explain the rating range", rr.Body.String())
		}
	})

	t.Run("out-of-range ratings are 400", func(t *testing.T) {
		for _, rating := range []string{"-1", "5", "99"} {
			rr := post(`{"rating":` + rating + `}`)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("rating=%s got %d, want 400", rating, rr.Code)
			}
		}
	})

	t.Run("malformed json is still 400", func(t *testing.T) {
		rr := post(`{not json`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("malformed json got %d, want 400", rr.Code)
		}
	})

	// 合法 rating 不应被新校验误伤：它会走到 store，零值 Store 的 nil pool
	// 触发 panic/500 都属预期 —— 这里只断言"不是 400 校验文案"。
	t.Run("valid rating passes validation", func(t *testing.T) {
		rr := post(`{"rating":3}`)
		if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "invalid rating") {
			t.Fatalf("rating=3 rejected by the new range check: %s", rr.Body.String())
		}
	})
}
