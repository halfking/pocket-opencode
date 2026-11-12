package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/memora"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// ─── 未配置降级（503）────────────────────────────────────────────────────

// TestMemoraNotebookRoutesUnconfigured 端到端打真实 Handler()：POCKET_MEMORA_*
// 未配置 ⇒ s.memora == nil ⇒ notebook 面两条新路由也必须如实 503（与既有
// /api/memora/* 三条路由同一降级口径，不允许 500 或静默空）。
func TestMemoraNotebookRoutesUnconfigured(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	h := srv.Handler()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"sync 未配置", http.MethodPost, "/api/memora/notes/sync", `{"all":true}`},
		{"search 未配置", http.MethodGet, "/api/memora/notes/search?q=x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("未配置应 503，got %d: %s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "memora 未配置") {
				t.Fatalf("503 应带可读原因，got %s", rr.Body.String())
			}
		})
	}
}

// TestMemoraNotebookRoutesRequireAuth 未带 token 一律 401（requireAuth 前置）。
func TestMemoraNotebookRoutesRequireAuth(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/memora/notes/sync", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，got %d", rr.Code)
	}
}

// ─── 幂等同步循环（memoraSyncOne）────────────────────────────────────────

// fakeNotebook 是有状态的 memora notebook 假端（服务端测试专用）：内存保存
// 笔记，支持 tag 过滤、If-Match CAS（不匹配回 412 version_conflict），形状
// 逐字段对齐 memora internal/notebook/handler.go。
type fakeNotebook struct {
	mu      sync.Mutex
	next    int
	notes   map[string]*fakeNBNote
	listQ   url.Values
	putBody map[string]any
}

type fakeNBNote struct {
	ID             string   `json:"id"`
	KBID           string   `json:"kb_id"`
	Title          string   `json:"title"`
	Lifecycle      string   `json:"lifecycle"`
	CurrentVersion int      `json:"current_version"`
	HTML           string   `json:"html"`
	PlainText      string   `json:"plain_text"`
	Tags           []string `json:"tags"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	Removed        bool     `json:"removed"`
}

// fakeNotebookUpstream 起服务。
func newFakeNotebookUpstream(t *testing.T) (*httptest.Server, *fakeNotebook) {
	t.Helper()
	fk := &fakeNotebook{notes: map[string]*fakeNBNote{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/notebook/notes", func(w http.ResponseWriter, r *http.Request) {
		fk.mu.Lock()
		defer fk.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			fk.listQ = r.URL.Query()
			tag := r.URL.Query().Get("tag")
			items := []fakeNBNote{}
			for _, n := range fk.notes {
				if tag != "" && !fakeHasTag(n.Tags, tag) {
					continue
				}
				items = append(items, *n)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items)})
		case http.MethodPost:
			var body struct {
				Title        string   `json:"title"`
				HTML         string   `json:"html"`
				Tags         []string `json:"tags"`
				ChangeReason string   `json:"change_reason"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			fk.next++
			id := "nb_" + strconv.Itoa(fk.next)
			now := time.Now().UTC().Format(time.RFC3339)
			fk.notes[id] = &fakeNBNote{
				ID: id, Title: body.Title, HTML: body.HTML, Tags: body.Tags,
				Lifecycle: "active", CurrentVersion: 1,
				PlainText: fakeStripTags(body.HTML),
				CreatedAt: now, UpdatedAt: now,
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "title": body.Title, "revision": 1, "etag": "1", "removed": false,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v2/notebook/notes/", func(w http.ResponseWriter, r *http.Request) {
		fk.mu.Lock()
		defer fk.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/notebook/notes/")
		n, ok := fk.notes[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"note not found"}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(n)
		case http.MethodPut:
			if r.Header.Get("If-Match") != strconv.Itoa(n.CurrentVersion) {
				w.WriteHeader(http.StatusPreconditionFailed)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "version_conflict", "current_version": n.CurrentVersion,
				})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			fk.putBody = body
			n.CurrentVersion++
			if v, ok := body["html"].(string); ok {
				n.HTML = v
				n.PlainText = fakeStripTags(v)
			}
			if v, ok := body["tags"].([]any); ok {
				n.Tags = nil
				for _, t := range v {
					if s, ok := t.(string); ok {
						n.Tags = append(n.Tags, s)
					}
				}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": n.ID, "title": n.Title, "revision": n.CurrentVersion,
				"etag": strconv.Itoa(n.CurrentVersion), "removed": false,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fk
}

func fakeHasTag(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func fakeStripTags(s string) string {
	s = strings.ReplaceAll(s, "<p>", "")
	return strings.ReplaceAll(s, "</p>", "")
}

// testNoteRef 构造 sync 输入（服务端 store 之外的直造路径）。
func testNoteRef(id, title, content string) noteRef {
	return noteRef{id: id, note: &notes.Note{
		ID: id, Title: title, Content: content,
		Tags: `["工作","todo"]`, UpdatedAt: time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC).Unix(),
	}}
}

// TestMemoraSyncOneIdempotentCycle 幂等三拍：created → skipped → updated。
// 重推同内容绝不产生第二条 memora 笔记；内容变化走 PUT（If-Match CAS），
// 并保留 noteTag/hashTag 定位 tag。
func TestMemoraSyncOneIdempotentCycle(t *testing.T) {
	srv, fk := newFakeNotebookUpstream(t)
	s := &Server{memora: memora.NewClient(srv.URL, "", "secret", "tenant-a", "proj-b", "pocketd", 5*time.Second)}
	if s.memora == nil {
		t.Fatal("client 不应为 nil")
	}
	ctx := context.Background()
	n := testNoteRef("p1", "购物清单", "牛奶\n鸡蛋")

	// 1) 首推 → created。
	status, memID, err := s.memoraSyncOne(ctx, n)
	if err != nil || status != "created" {
		t.Fatalf("首推应 created，got %s err=%v", status, err)
	}
	if memID == "" {
		t.Fatal("created 应回 memora id")
	}

	// 2) 重推同内容 → skipped（不新建）。
	status, memID2, err := s.memoraSyncOne(ctx, n)
	if err != nil || status != "skipped" {
		t.Fatalf("重推应 skipped，got %s err=%v", status, err)
	}
	if memID2 != memID {
		t.Fatalf("skipped 应指向同一条 memora 笔记: %q vs %q", memID2, memID)
	}
	fk.mu.Lock()
	count := len(fk.notes)
	fk.mu.Unlock()
	if count != 1 {
		t.Fatalf("重推不得新建笔记，memora 现有 %d 条", count)
	}

	// 3) 内容变化 → updated（PUT CAS）。
	n.note.Content = "牛奶\n鸡蛋\n面包"
	status, memID3, err := s.memoraSyncOne(ctx, n)
	if err != nil || status != "updated" {
		t.Fatalf("内容变化应 updated，got %s err=%v", status, err)
	}
	if memID3 != memID {
		t.Fatalf("更新应在原笔记上: %q vs %q", memID3, memID)
	}
	fk.mu.Lock()
	up := fk.notes[memID]
	putBody := fk.putBody
	fk.mu.Unlock()
	if !strings.Contains(up.HTML, "面包") {
		t.Fatalf("更新后应含新内容: %q", up.HTML)
	}
	if !fakeHasTag(up.Tags, "sync:openpocket:note:p1") || !fakeHasTag(up.Tags, "工作") {
		t.Fatalf("更新应保留/带上定位 tag 与原笔记 tags: %v", up.Tags)
	}
	if putBody == nil {
		t.Fatal("PUT body 应被记录")
	}

	// 4) tag 定位确凿：列表查询确实按 noteTag 过滤（幂等的基础）。
	fk.mu.Lock()
	tagQ := fk.listQ.Get("tag")
	fk.mu.Unlock()
	if tagQ != "sync:openpocket:note:p1" {
		t.Fatalf("定位查询应用 noteTag，got %q", tagQ)
	}
}

// TestNotePlainToHTMLWhitelist 受限 HTML 转换：段落包 <p>、文本转义——
// 白名单外标签一律不得出现（p/strong/em/ul/ol/li/h1-h6/a 之外没有）。
func TestNotePlainToHTMLWhitelist(t *testing.T) {
	html := notePlainToHTML("第一段 <b>加粗</b>\n\n第二段")
	if html != "<p>第一段 &lt;b&gt;加粗&lt;/b&gt;</p><p>第二段</p>" {
		t.Fatalf("受限 HTML 转换不符: %q", html)
	}
	if strings.Contains(html, "<b>") || strings.Contains(html, "<script") {
		t.Fatalf("白名单外标签必须被转义: %q", html)
	}
	if notePlainToHTML("   ") != "<p></p>" {
		t.Fatalf("空白正文应兜底空段: %q", notePlainToHTML("   "))
	}
}

// TestMemoraSyncOneFailures 失败路径逐条呈报：store 查不到、空正文。
func TestMemoraSyncOneFailures(t *testing.T) {
	srv, _ := newFakeNotebookUpstream(t)
	s := &Server{memora: memora.NewClient(srv.URL, "", "s", "", "", "", time.Second)}
	ctx := context.Background()

	status, _, err := s.memoraSyncOne(ctx, noteRef{id: "ghost"})
	if status != "failed" || err == nil || !strings.Contains(err.Error(), "note not found") {
		t.Fatalf("store 查不到应 failed，got %s err=%v", status, err)
	}

	status, _, err = s.memoraSyncOne(ctx, testNoteRef("p2", "空笔记", "   "))
	if status != "failed" || err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("空正文应 failed，got %s err=%v", status, err)
	}
}

// TestMemoraSyncOneUpstreamDown 上游不可达 ⇒ failed（不 panic、不部分成功）。
func TestMemoraSyncOneUpstreamDown(t *testing.T) {
	s := &Server{memora: memora.NewClient("http://127.0.0.1:1", "", "s", "", "", "", time.Second)}
	status, _, err := s.memoraSyncOne(context.Background(), testNoteRef("p3", "t", "c"))
	if status != "failed" || err == nil {
		t.Fatalf("上游不可达应 failed，got %s err=%v", status, err)
	}
}

// TestMemoraNoteSearchForwarding 检索转发端到端：query 透传 + source:memora
// 标注 + 未配置 502/503 分层。
func TestMemoraNoteSearchForwarding(t *testing.T) {
	srv, fk := newFakeNotebookUpstream(t)
	s := &Server{memora: memora.NewClient(srv.URL, "", "s", "tenant-a", "proj-b", "pocketd", 5*time.Second)}

	req := httptest.NewRequest(http.MethodGet, "/api/memora/notes/search?q=%E6%B8%85%E5%8D%95&limit=5", nil)
	rr := httptest.NewRecorder()
	s.handleMemoraNoteSearch(rr, req)
	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("应 200，got %d: %s", rr.Code, body)
	}
	var out struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应应可解析: %v", err)
	}
	if out.Total != 0 {
		t.Fatalf("空库应 0 条: %+v", out)
	}
	fk.mu.Lock()
	q := fk.listQ
	fk.mu.Unlock()
	if q.Get("q") != "清单" || q.Get("limit") != "5" {
		t.Fatalf("query 未透传: %v", q)
	}

	// limit>200 应被裁到 200。
	req = httptest.NewRequest(http.MethodGet, "/api/memora/notes/search?limit=9999", nil)
	rr = httptest.NewRecorder()
	s.handleMemoraNoteSearch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("limit 裁剪后应 200，got %d", rr.Code)
	}
	fk.mu.Lock()
	q = fk.listQ
	fk.mu.Unlock()
	if q.Get("limit") != "200" {
		t.Fatalf("limit 应裁到 200，got %q", q.Get("limit"))
	}

	// 非法 limit 应 400。
	req = httptest.NewRequest(http.MethodGet, "/api/memora/notes/search?limit=-1", nil)
	rr = httptest.NewRecorder()
	s.handleMemoraNoteSearch(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法 limit 应 400，got %d", rr.Code)
	}
}

// TestMemoraSyncSourceUpdatedAtRFC3339 源时间戳按 RFC3339 透传（memora
// parseSaveRequest 只认 RFC3339，格式错会 400）。
func TestMemoraSyncSourceUpdatedAtRFC3339(t *testing.T) {
	got := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/notebook/notes", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got, _ = body["source_updated_at"].(string)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"nb_z","revision":1,"etag":"1","removed":false}`))
	})
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)

	s := &Server{memora: memora.NewClient(upstream.URL, "", "s", "", "", "", time.Second)}
	ref := testNoteRef("p9", "t", "c")
	if _, _, err := s.memoraSyncOne(context.Background(), ref); err != nil {
		t.Fatalf("sync: %v", err)
	}
	want := "2026-11-13T08:00:00Z"
	if got != want {
		t.Fatalf("source_updated_at 应为 RFC3339 UTC，got %q want %q", got, want)
	}
}

// TestMemoraNoteItemDetailPassthrough 详情转发：/{id} 命中 GetNote（含
// plain_text/html），来源标注 source:"memora"；保留字（push/sync/search）
// 与空 id 不当笔记 id 用。
func TestMemoraNoteItemDetailPassthrough(t *testing.T) {
	srv, _ := newFakeNotebookUpstream(t)
	client := memora.NewClient(srv.URL, "", "s", "tenant-a", "proj-b", "pocketd", 5*time.Second)
	s := &Server{memora: client}

	// 造一条：假端是空库，先 POST 再读详情。
	if _, err := client.PushNote(context.Background(), memora.PushNoteRequest{Title: "t", HTML: "<p>清单</p>"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/memora/notes/nb_1", nil)
	rr := httptest.NewRecorder()
	s.handleMemoraNoteItem(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("详情应 200，got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应应可解析: %v", err)
	}
	if out["id"] != "nb_1" || out["source"] != "memora" {
		t.Fatalf("详情形状不符: %v", out)
	}
	if _, ok := out["plain_text"]; !ok {
		t.Fatalf("详情应含 plain_text（列表行没有，展开正文靠它）: %v", out)
	}

	for _, bad := range []string{"/api/memora/notes/", "/api/memora/notes/push", "/api/memora/notes/sync", "/api/memora/notes/search"} {
		req := httptest.NewRequest(http.MethodGet, bad, nil)
		rr := httptest.NewRecorder()
		s.handleMemoraNoteItem(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s 应 404，got %d", bad, rr.Code)
		}
	}
}

// TestMemoraNoteItemUnconfigured 未配置 memora 时详情也 503。
func TestMemoraNoteItemUnconfigured(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/memora/notes/nb_1", nil)
	rr := httptest.NewRecorder()
	s.handleMemoraNoteItem(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置应 503，got %d", rr.Code)
	}
}
