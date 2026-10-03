package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/flashcards"
)

// BUG-L 回归锁（2026-09-30 真机验收）。
//
// 现象：真机点「保存卡片」，Network 面板稳定出现
//
//	POST /api/flashcards/notes -> 405 Method Not Allowed
//
// 于是闪卡卡片永远存不进后端，列表回显恒空。
//
// 根因：契约 §2 与前端 services/flashcards.ts 的 createNote 都打
// POST /api/flashcards/notes，但后端 handleFlashcardsItem 把 len(parts)==1 &&
// parts[0]=="notes" 一律交给 flashcardsNotesCollection，而后者只允许 GET
// （405 "GET only"）。真正的创建实现 flashcardsCreateNote 只挂在**无尾斜杠**的
// /api/flashcards（handleFlashcardsCollection）。两条路径不等价。
//
// 为什么 BUG-K 的测试没抓到：那次断言的是 store==nil 时的 503，而 503 检查在
// handleFlashcardsItem 最开头，早于任何方法分派——所以 405 永远被 503 挡住。
// 本文件因此必须注入一个**非 nil** 的 store 才能观察到真实分派。
//
// 修法：handleFlashcardsItem 对 notes 集合补 POST，等价于 POST /api/flashcards。
func TestFlashcardsCreateNoteRouteOnNotesSubpath(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.flashcardStore != nil {
		t.Skip("test server already has a flashcard store; BUG-L assertion needs the nil-store default")
	}
	// 零值 Store：非 nil 即代表「store 已配置」。下面所有断言都在触碰
	// flashcardStore 之前就返回（参数校验 400 / 方法不支持 405），
	// 因此不会解引用内部为 nil 的 pgxpool。
	srv.SetFlashcardStore(&flashcards.Store{})
	token := tokens[""]

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req, _ := http.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	// 修复前这里是 405 "GET only"。
	t.Run("POST /api/flashcards/notes reaches flashcardsCreateNote", func(t *testing.T) {
		rr := do(http.MethodPost, "/api/flashcards/notes", `{"front":"F","back":"B"}`)
		if rr.Code == http.StatusMethodNotAllowed {
			t.Fatalf("POST /api/flashcards/notes = 405: still only GET is routed (BUG-L regressed)")
		}
		// 缺 deckId -> flashcardsCreateNote 内部的第一道校验。
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "deckId is required") {
			t.Fatalf("POST /api/flashcards/notes = %d %s, want 400 deckId is required",
				rr.Code, rr.Body.String())
		}
	})

	t.Run("body validation matches the collection alias", func(t *testing.T) {
		// 有 deckId 但缺 front/back -> 第二道校验。证明不只是「不再是 405」，
		// 而是确实进了同一个 handler 的完整校验链。
		rr := do(http.MethodPost, "/api/flashcards/notes", `{"deckId":"d1"}`)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "front and back are required") {
			t.Fatalf("POST /api/flashcards/notes = %d %s, want 400 front and back are required",
				rr.Code, rr.Body.String())
		}

		// 对照组：老路径 POST /api/flashcards 行为必须完全一致。
		alias := do(http.MethodPost, "/api/flashcards", `{"deckId":"d1"}`)
		if alias.Code != rr.Code || alias.Body.String() != rr.Body.String() {
			t.Fatalf("POST /api/flashcards = %d %s, want identical to /notes = %d %s",
				alias.Code, alias.Body.String(), rr.Code, rr.Body.String())
		}
	})

	// 反向判据：证明不是「把 notes 集合改成放行所有方法」。
	t.Run("unsupported methods on /notes still 405", func(t *testing.T) {
		for _, method := range []string{http.MethodPut} {
			rr := do(method, "/api/flashcards/notes", `{}`)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /api/flashcards/notes = %d, want 405", method, rr.Code)
			}
		}
	})
}
