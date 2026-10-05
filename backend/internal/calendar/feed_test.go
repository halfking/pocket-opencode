package calendar

import (
	"context"
	"errors"
	"testing"
)

// failingSources makes one domain blow up, to prove the feed degrades to a
// partial view instead of failing the whole calendar. An empty calendar is a
// worse user experience than one missing a category.
type failingSources struct {
	tasks     []TaskDUE
	runs      []ScheduledRun
	taskErr   error
	runErr    error
	taskCalls int
	runCalls  int
	lastFrom  int64
	lastTo    int64
}

func (f *failingSources) TaskDeadlines(_ context.Context, _ string, from, to int64) ([]TaskDUE, error) {
	f.taskCalls++
	f.lastFrom, f.lastTo = from, to
	if f.taskErr != nil {
		return nil, f.taskErr
	}
	return f.tasks, nil
}

func (f *failingSources) ScheduledRuns(_ context.Context, _ string, from, to int64) ([]ScheduledRun, error) {
	f.runCalls++
	if f.runErr != nil {
		return nil, f.runErr
	}
	return f.runs, nil
}

func TestSortFeedAllDayFirst(t *testing.T) {
	entries := []FeedEntry{
		{ID: "timed", Title: "会议", StartAt: 500},
		{ID: "allday", Title: "休假", StartAt: 100, AllDay: true},
		{ID: "timed2", Title: "站会", StartAt: 300},
	}
	sortFeed(entries)
	if entries[0].ID != "allday" {
		t.Errorf("all-day should sort first, got %q", entries[0].ID)
	}
	if entries[1].ID != "timed2" || entries[2].ID != "timed" {
		t.Errorf("timed entries should sort by start time, got %q then %q", entries[1].ID, entries[2].ID)
	}
}

func TestSortFeedStableForEqualTimes(t *testing.T) {
	// 同起始时刻必须按标题稳定排序，否则每次刷新网格顺序都在变。
	entries := []FeedEntry{
		{ID: "b", Title: "Beta", StartAt: 100},
		{ID: "a", Title: "Alpha", StartAt: 100},
	}
	sortFeed(entries)
	if entries[0].Title != "Alpha" || entries[1].Title != "Beta" {
		t.Errorf("equal timestamps should sort by title, got %q then %q", entries[0].Title, entries[1].Title)
	}
	again := []FeedEntry{entries[1], entries[0]}
	sortFeed(again)
	if again[0].Title != "Alpha" {
		t.Errorf("sort must be repeatable, got %q first", again[0].Title)
	}
}

func TestBuildFeedRequiresStore(t *testing.T) {
	// 事件表缺失时必须报错而不是返回空：空日历与正常日历长得一模一样。
	s := &Service{store: nil, sources: StaticSources{}}
	if _, err := s.BuildFeed(context.Background(), "w", "u", 0, 100, false); err == nil {
		t.Fatal("BuildFeed without a store should fail")
	}
}

func TestMergeEntriesCombinesAllSources(t *testing.T) {
	events := []Event{{ID: "cal_1", Title: "季度评审", StartAt: 300, EndAt: 3300, Timezone: "Asia/Shanghai"}}
	tasks := []TaskDUE{
		{ID: "t1", Title: "交材料", DueAt: 100, Status: "pending", Timezone: "Asia/Shanghai"},
		{ID: "t2", Title: "已验收", DueAt: 150, Status: "accepted"},
	}
	runs := []ScheduledRun{{ID: "s1", Name: "每日摘要", NextRun: 200, Timezone: "Asia/Shanghai"}}

	got := mergeEntries(events, tasks, runs)
	if len(got) != 4 {
		t.Fatalf("want 4 entries, got %d", len(got))
	}
	// 顺序只看 start_at 递增：100 任务 / 150 任务 / 200 定时 / 300 日程。
	if got[0].ID != "task:t1" || got[1].ID != "task:t2" || got[2].ID != "scheduled:s1" || got[3].ID != "event:cal_1" {
		t.Errorf("unexpected order: %v", []string{got[0].ID, got[1].ID, got[2].ID, got[3].ID})
	}
	if got[3].Source != SourceEvent || got[3].RefID != "cal_1" {
		t.Errorf("event should keep its ref id, got %+v", got[3])
	}
	// 每个来源前缀都必须存在，否则客户端索引里会互相覆盖。
	for _, e := range got {
		if e.Source == "" || e.RefID == "" {
			t.Errorf("entry missing source/ref: %+v", e)
		}
		if e.ID == e.RefID && e.Source != SourceEvent {
			t.Errorf("non-event entry id must be source-prefixed, got %q", e.ID)
		}
	}
}

func TestMergeEntriesCarriesDoneFlag(t *testing.T) {
	got := mergeEntries(nil, []TaskDUE{
		{ID: "open", Title: "未完成", DueAt: 10, Status: "pending"},
		{ID: "done", Title: "已完成", DueAt: 20, Status: "accepted"},
	}, nil)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Done {
		t.Error("pending task must not be done")
	}
	if !got[1].Done {
		t.Error("accepted task must be done (it is terminal in this repo)")
	}
}

func TestMergeEntriesSkipsUnplaceableRows(t *testing.T) {
	// 没有到期日的任务无法定位到某一天；没有 id 的行无法跳回详情。两者都跳过，
	// 但**空标题保留**（用占位符），否则该任务在日历上彻底消失且无从察觉。
	got := mergeEntries(nil, []TaskDUE{
		{ID: "no-due", Title: "没有到期日", DueAt: 0},
		{ID: "", Title: "没有 id", DueAt: 10},
		{ID: "ok", Title: "正常", DueAt: 20},
		{ID: "blank", Title: "  ", DueAt: 30},
	}, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 entries (ok + blank), got %d", len(got))
	}
	if got[0].RefID != "ok" {
		t.Errorf("expected the valid task first, got %q", got[0].RefID)
	}
	if got[1].Title == "" {
		t.Error("blank title must render as a placeholder, not an invisible chip")
	}
}

func TestMergeEntriesDeadlineIsZeroLength(t *testing.T) {
	// 任务到期是一个「时刻」而不是一段时间：零长是刻意的，客户端的重叠
	// 判定对它有专门处理（见 calendar-feed.hasConflict）。
	got := mergeEntries(nil, []TaskDUE{{ID: "t", Title: "到期", DueAt: 42}}, nil)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].StartAt != 42 || got[0].EndAt != 42 {
		t.Errorf("deadline should be zero length, got %d..%d", got[0].StartAt, got[0].EndAt)
	}
}

func TestMergeEntriesSameRefIDAcrossSources(t *testing.T) {
	// 任务与日程事件的 id 可能都是 "42"，没有来源前缀就会互相覆盖。
	got := mergeEntries(
		[]Event{{ID: "42", Title: "日程", StartAt: 10}},
		[]TaskDUE{{ID: "42", Title: "任务", DueAt: 20}},
		nil,
	)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].ID == got[1].ID {
		t.Fatalf("feed ids collided: %s", got[0].ID)
	}
	if got[0].ID != "event:42" || got[1].ID != "task:42" {
		t.Errorf("unexpected ids: %q %q", got[0].ID, got[1].ID)
	}
	// 两条都仍指向原始 refId，点击时能跳回各自的详情页。
	if got[0].RefID != "42" || got[1].RefID != "42" {
		t.Errorf("ref ids should be preserved, got %q %q", got[0].RefID, got[1].RefID)
	}
}

func TestMergeEntriesEmpty(t *testing.T) {
	got := mergeEntries(nil, nil, nil)
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestFailingSourceIsNotFatal(t *testing.T) {
	// 一个域挂掉时，merge 本身不 panic；BuildFeed 层用 err==nil 才收数据。
	src := &failingSources{
		tasks:   []TaskDUE{{ID: "t1", Title: "任务", DueAt: 10}},
		taskErr: errors.New("tasks table missing"),
	}
	got, err := src.TaskDeadlines(context.Background(), "w", 0, 100)
	if !errors.Is(err, src.taskErr) {
		t.Errorf("task error should surface to BuildFeed, got %v", err)
	}
	if got != nil {
		t.Errorf("no entries expected on error, got %v", got)
	}
}

func TestStaticSourcesPassThrough(t *testing.T) {
	want := []TaskDUE{{ID: "t1", Title: "x", DueAt: 1}}
	s := StaticSources{Tasks: want}
	got, err := s.TaskDeadlines(context.Background(), "w", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "t1" {
		t.Errorf("static sources should pass through, got %v", got)
	}
}

func TestMaxFeedRangeIsEnforced(t *testing.T) {
	if maxFeedRangeSeconds != 62*86400 {
		t.Errorf("maxFeedRangeSeconds = %d", maxFeedRangeSeconds)
	}
	if maxFeedRangeSeconds <= 0 {
		t.Error("max feed range must be positive")
	}
}
