package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BUG-K 回归锁（2026-09-30 真机验收）。
//
// 现象：新用户 GET /api/flashcards 返回 decks=0，且**没有任何创建卡组的路径**——
//   - 后端没有 POST /api/flashcards/decks（实测 404）
//   - 前端列表页「新建卡组」按钮执行 router.push('/flashcards/new')，
//     跳到的是「新建卡片」页（FlashcardEditView）
//
// 于是 decks=0 → selectedDeckId 取不到值 → 保存按钮恒 disabled →
// 闪卡模块从零状态完全不可用。
//
// 本测试锁住「路由存在」这一环：store 未配置时必须返回 503
// （路由已注册，只是缺 store），而不是 404（路由不存在）。
// 修复前本用例拿到的是 404。
func TestFlashcardsCreateDeckRouteExists(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.flashcardStore != nil {
		t.Skip("test server has a real flashcards store; route-level assertion not applicable")
	}
	token := tokens[""]

	t.Run("POST /api/flashcards/decks is routed (503 not 404)", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/flashcards/decks",
			strings.NewReader(`{"name":"My Deck"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)

		if rr.Code == http.StatusNotFound {
			t.Fatalf("POST /api/flashcards/decks returned 404: the create-deck route is still missing (BUG-K regressed)")
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when flashcardStore is nil, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	// 对照：cards/notes 的 POST 路由本来就存在，同样应是 503 而非 404。
	// 这组对照证明判据是「路由注册」而不是碰巧返回了某个状态码。
	t.Run("sibling routes behave the same way", func(t *testing.T) {
		for _, path := range []string{"/api/flashcards/cards", "/api/flashcards/notes"} {
			req, _ := http.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("POST %s = %d, want 503 (store nil)", path, rr.Code)
			}
		}
	})
}
