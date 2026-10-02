package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/flashcards"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newRSSRoutePG 起一个带**真实 PostgreSQL** 的 server，专供 RSS 新路由的端到端
// 验证。为什么要这么重：这次的三个端点（starter / import-starter / digest）
// 真正的风险不在 handler 分支，而在"路由没注册""作用域取错""JSON 形状对不上"
// 这些只有真跑一次才看得见。
func newRSSRoutePG(t *testing.T) (*Server, *pgxpool.Pool, string, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("POCKET_TEST_PG_DSN")
	}
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping rss route integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	// schema 名必须含 `*_test_`：pg_test_isolation_guard_test.go 的规则 2 就是按这个
	// 形状确认「测试没有连到生产 schema」，名字不匹配会被守卫判红。
	schema := "rss_route_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfgPool, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfgPool.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfgPool)
	if err != nil {
		rootPool.Close()
		t.Fatalf("pool: %v", err)
	}
	drop := func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}

	rssStore, err := rss.NewStore(ctx, pool)
	if err != nil {
		drop()
		t.Fatalf("rss.NewStore: %v", err)
	}
	fcStore, err := flashcards.NewStore(ctx, pool)
	if err != nil {
		drop()
		t.Fatalf("flashcards.NewStore: %v", err)
	}
	if err := fcStore.EnsureSchema(ctx); err != nil {
		drop()
		t.Fatalf("EnsureSchema: %v", err)
	}

	srv, _, _, tokens := newMobileRouteServer(t)
	srv.SetRSSStore(rssStore)
	srv.SetFlashcardStore(fcStore)
	return srv, pool, tokens[""], drop
}

func doJSON(t *testing.T, srv *Server, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r, _ = http.NewRequest(method, path, nil)
	} else {
		r, _ = http.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, r)
	out := map[string]any{}
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
	}
	return rr.Code, out
}

// TestRSSStarterRoutesEndToEnd 覆盖"用户从零开始：看推荐源 → 一键导入 → 看到订阅"。
func TestRSSStarterRoutesEndToEnd(t *testing.T) {
	srv, _, token, cleanup := newRSSRoutePG(t)
	defer cleanup()

	code, body := doJSON(t, srv, http.MethodGet, "/api/rss/sources/starter", token, "")
	if code != http.StatusOK {
		t.Fatalf("GET starter: %d %v", code, body)
	}
	feeds, _ := body["feeds"].([]any)
	if len(feeds) < 9 {
		t.Fatalf("starter catalog too small: %d", len(feeds))
	}
	cats := map[string]bool{}
	for _, f := range feeds {
		m := f.(map[string]any)
		cats[m["category"].(string)] = true
		if _, ok := m["categoryLabel"].(string); !ok {
			t.Errorf("starter feed missing categoryLabel: %v", m)
		}
	}
	for _, c := range []string{"it", "finance", "news"} {
		if !cats[c] {
			t.Errorf("starter catalog missing category %q", c)
		}
	}
	// seeds 是 starter 的兼容别名，两者必须一致，否则老入口和新品不一致。
	codeSeeds, seedBody := doJSON(t, srv, http.MethodGet, "/api/rss/sources/seeds", token, "")
	if codeSeeds != http.StatusOK {
		t.Fatalf("GET seeds: %d", codeSeeds)
	}
	if len(seedBody["seeds"].([]any)) != len(feeds) {
		t.Errorf("seeds(%d) and starter(%d) disagree", len(seedBody["seeds"].([]any)), len(feeds))
	}

	// 分类过滤
	code, body = doJSON(t, srv, http.MethodGet, "/api/rss/sources/starter?category=finance", token, "")
	if code != http.StatusOK {
		t.Fatalf("GET starter?category: %d", code)
	}
	for _, f := range body["feeds"].([]any) {
		if f.(map[string]any)["category"] != "finance" {
			t.Errorf("category filter leaked: %v", f)
		}
	}

	// 一键导入（幂等）
	code, body = doJSON(t, srv, http.MethodPost, "/api/rss/sources/import-starter", token, `{}`)
	if code != http.StatusOK {
		t.Fatalf("import-starter: %d %v", code, body)
	}
	created := int(body["created"].(float64))
	if created < 20 {
		t.Fatalf("import created %d, want >= 20", created)
	}
	code, body = doJSON(t, srv, http.MethodPost, "/api/rss/sources/import-starter", token, `{}`)
	if code != http.StatusOK {
		t.Fatalf("second import: %d", code)
	}
	if int(body["created"].(float64)) != 0 || int(body["skipped"].(float64)) != created {
		t.Errorf("re-import not idempotent: created=%v skipped=%v", body["created"], body["skipped"])
	}

	// 导入结果必须真的出现在订阅列表里
	code, body = doJSON(t, srv, http.MethodGet, "/api/rss/sources", token, "")
	if code != http.StatusOK {
		t.Fatalf("list sources: %d", code)
	}
	if got := len(body["sources"].([]any)); got != created {
		t.Errorf("sources = %d, want %d", got, created)
	}
}

// TestRSSDigestRoutesEndToEnd：空库也能拿到日报（"每天一份摘要"的第一天不能是 404/500）。
func TestRSSDigestRoutesEndToEnd(t *testing.T) {
	srv, pool, token, cleanup := newRSSRoutePG(t)
	defer cleanup()

	code, body := doJSON(t, srv, http.MethodGet, "/api/rss/digest", token, "")
	if code != http.StatusOK {
		t.Fatalf("GET digest on empty store: %d %v", code, body)
	}
	d, _ := body["digest"].(map[string]any)
	if d == nil {
		t.Fatalf("digest missing: %v", body)
	}
	if d["itemCount"].(float64) != 0 {
		t.Errorf("empty store digest itemCount = %v", d["itemCount"])
	}

	code, body = doJSON(t, srv, http.MethodGet, "/api/rss/digest?date=not-a-date", token, "")
	if code != http.StatusBadRequest {
		t.Errorf("bad date should be 400, got %d", code)
	}

	// 先导入推荐源（拿到真实作用域），再塞条目。
	if code, b := doJSON(t, srv, http.MethodPost, "/api/rss/sources/import-starter", token, `{"categories":["it"],"maxPerCategory":1}`); code != http.StatusOK {
		t.Fatalf("import-starter: %d %v", code, b)
	}
	// 塞两条今天的条目（直接写库：这里测的是聚合与 API，不是 feed 解析）。
	ctx := context.Background()
	var userID, workspaceID string
	row := pool.QueryRow(ctx, `SELECT user_id, workspace_id FROM rss_sources LIMIT 1`)
	if err := row.Scan(&userID, &workspaceID); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	now := time.Now().UTC()
	srcID := "src-e2e-digest"
	if _, err := pool.Exec(ctx, `
		INSERT INTO rss_sources(id,user_id,workspace_id,url,title,category,enabled,fetch_interval,next_fetch_at,created_at,updated_at)
		VALUES ($1,$2,$3,'https://e2e.example/feed','E2E 源','it',TRUE,3600,$4,$4,$4)`,
		srcID, userID, workspaceID, now); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO rss_items(id,source_id,user_id,workspace_id,hash,url,title,summary,published_at,status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'摘要',$8,'unread')`,
			fmt.Sprintf("it-e2e-%d", i), srcID, userID, workspaceID,
			fmt.Sprintf("h%d", i), fmt.Sprintf("https://e2e.example/%d", i),
			fmt.Sprintf("E2E 条目 %d", i), now.Add(time.Duration(-i)*time.Hour)); err != nil {
			t.Fatalf("insert item: %v", err)
		}
	}

	// 强制重新生成，才能看到刚写的条目。
	code, body = doJSON(t, srv, http.MethodPost, "/api/rss/digest/run", token, "")
	if code != http.StatusOK {
		t.Fatalf("digest run: %d %v", code, body)
	}
	d = body["digest"].(map[string]any)
	if int(d["itemCount"].(float64)) != 2 {
		t.Errorf("itemCount = %v, want 2", d["itemCount"])
	}
	sections, _ := d["sections"].([]any)
	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	sec := sections[0].(map[string]any)
	if sec["category"] != "it" || sec["label"] != "IT 科技" {
		t.Errorf("section = %v", sec)
	}
	bodyText, _ := d["body"].(string)
	if !strings.Contains(bodyText, "E2E 条目 0") || !strings.Contains(bodyText, "https://e2e.example/0") {
		t.Errorf("digest body is not shareable:\n%s", bodyText)
	}

	// 历史列表
	code, body = doJSON(t, srv, http.MethodGet, "/api/rss/digests", token, "")
	if code != http.StatusOK {
		t.Fatalf("digests list: %d", code)
	}
	if len(body["digests"].([]any)) != 1 {
		t.Errorf("digest history = %v", body["digests"])
	}
}

// TestFlashcardsStarterRoutesEndToEnd：内置学习库的目录与导入。
func TestFlashcardsStarterRoutesEndToEnd(t *testing.T) {
	srv, _, token, cleanup := newRSSRoutePG(t)
	defer cleanup()

	code, body := doJSON(t, srv, http.MethodGet, "/api/flashcards/starter", token, "")
	if code != http.StatusOK {
		t.Fatalf("GET starter: %d %v", code, body)
	}
	decks, _ := body["decks"].([]any)
	if len(decks) < 4 {
		t.Fatalf("starter decks = %d, want >= 4", len(decks))
	}
	if body["imported"] != false {
		t.Errorf("imported should be false for a fresh user, got %v", body["imported"])
	}
	total := 0
	for _, d := range decks {
		m := d.(map[string]any)
		total += int(m["cardCount"].(float64))
	}
	if total < 300 {
		t.Errorf("starter card total = %d, want >= 300", total)
	}

	code, body = doJSON(t, srv, http.MethodPost, "/api/flashcards/starter/import", token, `{}`)
	if code != http.StatusOK {
		t.Fatalf("import: %d %v", code, body)
	}
	if int(body["cardsCreated"].(float64)) != total {
		t.Errorf("cardsCreated = %v, want %d", body["cardsCreated"], total)
	}

	code, body = doJSON(t, srv, http.MethodPost, "/api/flashcards/starter/import", token, `{}`)
	if code != http.StatusOK {
		t.Fatalf("re-import: %d", code)
	}
	if int(body["cardsCreated"].(float64)) != 0 {
		t.Errorf("re-import created %v cards, want 0", body["cardsCreated"])
	}

	// 导入后卡组必须真的出现在 /api/flashcards 的 decks 里。
	code, body = doJSON(t, srv, http.MethodGet, "/api/flashcards", token, "")
	if code != http.StatusOK {
		t.Fatalf("GET flashcards: %d %v", code, body)
	}
	if got := len(body["decks"].([]any)); got < 4 {
		t.Errorf("decks after import = %d, want >= 4", got)
	}

	code, body = doJSON(t, srv, http.MethodGet, "/api/flashcards/starter", token, "")
	if code != http.StatusOK || body["imported"] != true {
		t.Errorf("imported flag after import = %v (code %d)", body["imported"], code)
	}

	// 只导入一套
	code, body = doJSON(t, srv, http.MethodPost, "/api/flashcards/starter/import", token, `{"deckIds":["starter-ai-basics"]}`)
	if code != http.StatusOK {
		t.Fatalf("single import: %d %v", code, body)
	}
	if int(body["decks"].(float64)) != 1 || int(body["cardsCreated"].(float64)) != 0 {
		t.Errorf("single-deck re-import = %v, want decks=1 cardsCreated=0", body)
	}
}
