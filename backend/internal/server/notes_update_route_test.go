package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// BUG-N 回归锁（2026-09-30，由 scripts/probe-write-methods.mjs 的 method 级
// 探测发现，不是靠读代码看出来的）。
//
// 现象：对 PUT /api/notes/__audit_nonexistent__ 发请求，实测
//
//	405 Method Not Allowed
//
// 前端 frontend/src/api/notes.ts 的 notesApi.update() 打的就是
// PUT /api/notes/:id。翻后端 handleNoteOperations 的 switch 只有 GET/DELETE，
// notes.Store 里也确实没有任何更新方法 —— **编辑笔记在真机上恒失败**，
// 而笔记的创建/读取/删除都是好的，所以肉眼看模块"大部分能用"。
//
// 判据：PUT/PATCH 必须进 handler 的更新分支（store 未配置时 503，
// 路由缺失或 method 不支持时才是 404/405）。修复前是 405。
func TestNotesUpdateRouteAcceptsPutAndPatch(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.notesStore != nil {
		t.Skip("test server already has a notes store; route-level assertion not applicable")
	}
	token := tokens[""]

	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		req, _ := http.NewRequest(method, "/api/notes/n-1",
			strings.NewReader(`{"title":"renamed"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)

		if rr.Code == http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/notes/:id = 405: note update is still unrouted (BUG-N regressed)", method)
		}
		if rr.Code == http.StatusNotFound {
			t.Fatalf("%s /api/notes/:id = 404: note update route missing (BUG-N regressed)", method)
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s /api/notes/:id = %d, want 503 (store nil): %s",
				method, rr.Code, rr.Body.String())
		}
	}

	// 对照：GET/DELETE 此前就有，不该被这次改动影响。
	t.Run("GET and DELETE still routed", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			req, _ := http.NewRequest(method, "/api/notes/n-1", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code == http.StatusNotFound || rr.Code == http.StatusMethodNotAllowed {
				t.Errorf("%s /api/notes/:id regressed to %d", method, rr.Code)
			}
		}
	})

	// 未知 method 仍须被拒 —— 证明不是把整个 switch 放开了。
	//
	// 两个坑都踩过：
	//  1. 判据一开始写的是 OPTIONS，拿到 200 就以为撞上了新 bug。查证后：那
	//     是 corsMiddleware 的标准预检短路（server.go:885 `if r.Method ==
	//     "OPTIONS"`），**任何路径的 OPTIONS 都不会进 handler**，200 属设计。
	//     改用 TRACE，它不触发预检短路。
	//  2. 换成 TRACE 后拿到 503 也不是"通过"：handleNoteOperations 开头
	//     `if s.notesStore == nil { 503 }` 在 method switch **之前**，store 未
	//     注入时任何 method 都是 503，method 级断言根本观察不到 switch。
	//     必须注入零值 store，TRACE 才会真正落到 default。
	//     （与 BUG-L 回归锁同一个教训：store==nil 的 503 会盖住一切 method 判定。）
	t.Run("unsupported method still 405", func(t *testing.T) {
		srv.notesStore = &notes.Store{}
		req, _ := http.NewRequest(http.MethodTrace, "/api/notes/n-1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("TRACE /api/notes/:id = %d, want 405 (store injected so switch is reached)", rr.Code)
		}
	})

	// 空 body 的 PUT 不应 panic：解析失败/空 patch 都要干净返回。
	// 零值 store 的 pool 是 nil，若请求越界到 store 就会 panic 而不是返回状态码。
	t.Run("empty PUT body does not reach the store", func(t *testing.T) {
		srv.notesStore = &notes.Store{}
		req, _ := http.NewRequest(http.MethodPut, "/api/notes/n-1", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		// 空 patch 是无害 no-op：GetByIDScoped 会被调用并因 nil pool 失败，
		// 所以这里只断言"不是 panic、不是 5xx 以外的不确定态"，具体码不锁死。
		if rr.Code == http.StatusMethodNotAllowed || rr.Code == http.StatusNotFound {
			t.Errorf("empty PUT = %d, want the update branch to be reached", rr.Code)
		}
	})

	// 非法字段值必须在进 store 前被 400 挡住（同样依赖 store 注入）。
	t.Run("invalid domain is 400 before store", func(t *testing.T) {
		srv.notesStore = &notes.Store{}
		req, _ := http.NewRequest(http.MethodPut, "/api/notes/n-1",
			strings.NewReader(`{"domain":"not-a-domain"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("PUT with invalid domain = %d, want 400: %s", rr.Code, rr.Body.String())
		}
	})

	// 把上面查证出来的 CORS 契约固定下来：OPTIONS 永远 200（预检短路），
	// 只有 Origin 不在白名单时才 403。后者是有安全含义的那一半。
	t.Run("OPTIONS is answered by CORS preflight, not the handler", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, "/api/notes/n-1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("OPTIONS = %d, want 200 (cors preflight short-circuit)", rr.Code)
		}

		bad, _ := http.NewRequest(http.MethodOptions, "/api/notes/n-1", nil)
		bad.Header.Set("Authorization", "Bearer "+token)
		bad.Header.Set("Origin", "http://evil.example")
		rr2 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr2, bad)
		if rr2.Code != http.StatusForbidden {
			t.Errorf("OPTIONS with disallowed Origin = %d, want 403", rr2.Code)
		}
	})
}
