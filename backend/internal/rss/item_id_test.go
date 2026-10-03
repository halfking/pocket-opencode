package rss

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestItemIDIsPerSourceAndContent 说明 item 主键必须是 (source_id, 内容哈希)
// 的组合。这条锁的是一个真实事故：原来写的是 stableHash(source.ID, h, "", "")，
// 而 stableHash 是「取第一个非空参数」，于是 id 恒等于 sha256(source.ID) ——
// 同一个源的每一条 item 主键都一样，每个源最多存进一条，其余全部 duplicate key。
func TestItemIDIsPerSourceAndContent(t *testing.T) {
	hashA := stableHash("guid-a", "", "t", "c")
	hashB := stableHash("guid-b", "", "t", "c")
	if hashA == hashB {
		t.Fatal("test setup broken: the two content hashes must differ")
	}
	// 同源不同文 → 不同 id
	if itemID("src-1", hashA) == itemID("src-1", hashB) {
		t.Error("two different items of the same source must not share an id")
	}
	// 不同源同文（转载）→ 不同 id
	if itemID("src-1", hashA) == itemID("src-2", hashA) {
		t.Error("the same article in two sources must not share an id (syndication is common)")
	}
	// 稳定：同源同文重复抓取 → 同一个 id
	if itemID("src-1", hashA) != itemID("src-1", hashA) {
		t.Error("item id must be stable across fetches")
	}
}

// TestParserGivesEveryItemItsOwnID 是这件事最直接的判据：一个 feed 里的多条
// 条目必须拿到不同的 id。
func TestParserGivesEveryItemItsOwnID(t *testing.T) {
	feed := `<?xml version="1.0"?><rss version="2.0"><channel>
		<title>t</title><link>https://example.com</link><description>d</description>
		<item><title>One</title><link>https://example.com/1</link><guid>1</guid><description>c1</description></item>
		<item><title>Two</title><link>https://example.com/2</link><guid>2</guid><description>c2</description></item>
		<item><title>Three</title><link>https://example.com/3</link><guid>3</guid><description>c3</description></item>
	</channel></rss>`
	p := NewParser()
	items, err := p.Parse([]byte(feed), Source{ID: "src-1", UserID: "u", WorkspaceID: "w"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("parsed %d items, want 3", len(items))
	}
	seen := map[string]string{}
	for _, it := range items {
		if it.ID == "" {
			t.Fatalf("item %q has empty id", it.Title)
		}
		if prev, dup := seen[it.ID]; dup {
			t.Errorf("items %q and %q share id %q — one source can only store one item", prev, it.Title, it.ID)
		}
		seen[it.ID] = it.Title
	}
	// TestStoreInsertManyItemsFromOneSource 是那个事故在真实库里的形状。
	for _, it := range items {
		if it.ID == itemID("src-1", it.Hash) {
			continue
		}
		t.Errorf("item %q id is not derived from (source, hash)", it.Title)
	}
}

// TestUpsertItemRealWorldCollisions 必须在真实 PG 上跑：主键冲突是数据库行为，
// 内存假库测不出来。
func TestUpsertItemRealWorldCollisions(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

	srcA, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://syn/a", Title: "A", Category: CategoryIT, Enabled: true, FetchInterval: time.Hour}, sc)
	if err != nil {
		t.Fatalf("create source A: %v", err)
	}
	srcB, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://syn/b", Title: "B", Category: CategoryNews, Enabled: true, FetchInterval: time.Hour}, sc)
	if err != nil {
		t.Fatalf("create source B: %v", err)
	}

	// 1) 同一个源的 3 条不同内容：过去只有第 1 条能进去。
	for i := 0; i < 3; i++ {
		it := Item{SourceID: srcA.ID, Hash: stableHash(fmt.Sprintf("guid-%d", i), "", "", ""), URL: fmt.Sprintf("https://syn/a/%d", i), Title: fmt.Sprintf("A%d", i), PublishedAt: &now}
		if _, err := s.UpsertItemScoped(ctx, it, sc); err != nil {
			t.Fatalf("insert item %d of source A: %v", i, err)
		}
	}
	// 2) 同文转载到另一个源：过去会撞主键。
	syndicated := stableHash("guid-0", "", "", "")
	if _, err := s.UpsertItemScoped(ctx, Item{SourceID: srcB.ID, Hash: syndicated, URL: "https://syn/b/0", Title: "A0", PublishedAt: &now}, sc); err != nil {
		t.Fatalf("syndicated copy in source B: %v", err)
	}
	// 3) 重复抓取同一条：必须是「已存在」而不是报错、也不是新行。
	inserted, err := s.UpsertItemScoped(ctx, Item{SourceID: srcA.ID, Hash: stableHash("guid-0", "", "", ""), URL: "https://syn/a/0", Title: "A0", PublishedAt: &now}, sc)
	if err != nil {
		t.Fatalf("re-insert same item: %v", err)
	}
	if inserted {
		t.Error("re-fetching the same item must report it as not newly inserted")
	}
	all, err := s.ListItems(ctx, sc, ListItemsOptions{Limit: 50})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("stored items = %d, want 4 (3 from A + 1 syndicated in B)", len(all))
	}
	ids := map[string]bool{}
	for _, it := range all {
		if ids[it.ID] {
			t.Errorf("duplicate item id in store: %s", it.ID)
		}
		ids[it.ID] = true
	}
}

// 跨用户：同一个 feed 被两个用户订阅时，item 主键也不能互相打架。
func TestUpsertItemAcrossUsers(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	alice := Scope{UserID: "alice", WorkspaceID: "ws1"}
	bob := Scope{UserID: "bob", WorkspaceID: "ws1"}
	now := time.Now().UTC()

	srcA, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://shared.example/feed", Title: "shared", Enabled: true, FetchInterval: time.Hour}, alice)
	if err != nil {
		t.Fatalf("alice source: %v", err)
	}
	srcB, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://shared.example/feed", Title: "shared", Enabled: true, FetchInterval: time.Hour}, bob)
	if err != nil {
		t.Fatalf("bob source: %v", err)
	}
	h := stableHash("guid-same", "https://shared.example/1", "T", "C")
	if _, err := s.UpsertItemScoped(ctx, Item{SourceID: srcA.ID, Hash: h, URL: "https://shared.example/1", Title: "T", PublishedAt: &now}, alice); err != nil {
		t.Fatalf("alice insert: %v", err)
	}
	if _, err := s.UpsertItemScoped(ctx, Item{SourceID: srcB.ID, Hash: h, URL: "https://shared.example/1", Title: "T", PublishedAt: &now}, bob); err != nil {
		t.Fatalf("bob insert of the same article: %v", err)
	}
}
