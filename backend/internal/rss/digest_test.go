package rss

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDigestDayRangeIsHalfOpen(t *testing.T) {
	start, end := DigestDayRange(time.Date(2026, 10, 3, 23, 59, 0, 0, time.UTC))
	if got := start.Format(time.RFC3339); got != "2026-10-03T00:00:00Z" {
		t.Errorf("day start = %s", got)
	}
	if got := end.Format(time.RFC3339); got != "2026-10-04T00:00:00Z" {
		t.Errorf("day end = %s, must be exclusive next midnight", got)
	}
	if !end.After(start) || end.Sub(start) != 24*time.Hour {
		t.Errorf("day range must span exactly 24h, got %s", end.Sub(start))
	}
}

func TestBuildSectionsKeepsCategoryOrderAndCapsCount(t *testing.T) {
	items := func(n int, cat string) []DigestItem {
		out := make([]DigestItem, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, DigestItem{ID: cat + string(rune('a'+i)), Category: cat})
		}
		return out
	}
	byCategory := map[string][]DigestItem{
		CategoryNews:    items(3, CategoryNews),
		CategoryFinance: items(4, CategoryFinance),
		CategoryIT:      items(5, CategoryIT),
		CategoryOther:   items(1, CategoryOther),
	}
	secs := buildSections(byCategory, 2)
	wantOrder := []string{CategoryIT, CategoryFinance, CategoryNews, CategoryOther}
	if len(secs) != len(wantOrder) {
		t.Fatalf("got %d sections, want %d", len(secs), len(wantOrder))
	}
	for i, want := range wantOrder {
		if secs[i].Category != want {
			t.Errorf("section %d = %q, want %q", i, secs[i].Category, want)
		}
		if len(secs[i].Items) > 2 {
			t.Errorf("section %q kept %d items, cap is 2", want, len(secs[i].Items))
		}
	}
	if secs[0].Label != "IT 科技" {
		t.Errorf("section label = %q", secs[0].Label)
	}
}

func TestBuildSectionsSkipsEmptyCategories(t *testing.T) {
	secs := buildSections(map[string][]DigestItem{CategoryIT: {{ID: "x", Category: CategoryIT}}}, 8)
	if len(secs) != 1 {
		t.Fatalf("empty categories must be skipped, got %d sections", len(secs))
	}
}

func TestBuildHeadlineAndBody(t *testing.T) {
	d := &Digest{
		Date:      "2026-10-03",
		ItemCount: 5,
		Sections: []DigestSection{
			{Category: CategoryIT, Label: "IT 科技", Items: []DigestItem{{Title: "标题一", URL: "https://a.example/1", SourceTitle: "源一"}}},
			{Category: CategoryNews, Label: "时事", Items: []DigestItem{{Title: "标题二", URL: "https://a.example/2", SourceTitle: "源二"}}},
		},
	}
	d.Headline = buildHeadline(d, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(d.Headline, "5 条") || !strings.Contains(d.Headline, "IT 科技 1") {
		t.Errorf("headline missing counts: %q", d.Headline)
	}
	body := buildBody(d)
	for _, want := range []string{"标题一", "源一", "https://a.example/1", "【IT 科技】", "【时事】"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	empty := &Digest{ItemCount: 0, Sections: nil}
	empty.Headline = buildHeadline(empty, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(empty.Headline, "暂无新内容") {
		t.Errorf("empty digest headline = %q", empty.Headline)
	}
}

func TestSummarizeTruncatesByRune(t *testing.T) {
	if got := summarize("  a   b  c ", 10); got != "a b c" {
		t.Errorf("summarize collapsed = %q", got)
	}
	if got := summarize("中文中文中文", 3); got != "中文中…" {
		t.Errorf("summarize truncate = %q", got)
	}
	if got := summarize("", 5); got != "" {
		t.Errorf("summarize empty = %q", got)
	}
}

func TestNormalizeCategory(t *testing.T) {
	cases := map[string]string{
		"IT":    CategoryIT,
		"tech":  CategoryOther,
		"":      CategoryOther,
		"news":  CategoryNews,
		"money": CategoryOther,
	}
	for in, want := range cases {
		if got := normalizeCategory(in); got != want {
			t.Errorf("normalizeCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

// ===== DigestService（纯内存 fake） =====

type fakeDigestStore struct {
	scopes  []Scope
	builds  []string
	saved   []string
	failFor string
}

func (f *fakeDigestStore) ListActiveScopes(context.Context) ([]Scope, error) { return f.scopes, nil }

func (f *fakeDigestStore) BuildDigest(_ context.Context, sc Scope, day time.Time, _ DigestOptions) (*Digest, error) {
	if f.failFor != "" && sc.UserID == f.failFor {
		return nil, errors.New("boom")
	}
	d := &Digest{Date: DigestDateOf(day), ItemCount: 2, Headline: "h", Sections: []DigestSection{{Category: CategoryIT, Label: "IT 科技"}}}
	f.builds = append(f.builds, sc.UserID+"/"+sc.WorkspaceID)
	return d, nil
}

func (f *fakeDigestStore) SaveDigest(_ context.Context, sc Scope, d *Digest) (*Digest, error) {
	f.saved = append(f.saved, sc.UserID+"/"+sc.WorkspaceID+":"+d.Date)
	return d, nil
}

type recordingNotifier struct{ got []string }

func (n *recordingNotifier) NotifyDigest(_ context.Context, sc Scope, d *Digest) error {
	n.got = append(n.got, sc.UserID+"/"+sc.WorkspaceID+":"+d.Date)
	return nil
}

func TestDigestServiceRunOnceCoversEveryScope(t *testing.T) {
	store := &fakeDigestStore{scopes: []Scope{{UserID: "b", WorkspaceID: "default"}, {UserID: "a", WorkspaceID: "ws2"}}}
	notifier := &recordingNotifier{}
	svc := NewDigestService(store, notifier, Scope{}, DigestServiceOptions{
		AtHour: 8, AtMinute: 30, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 10, 3, 7, 0, 0, 0, time.UTC) },
	})
	n, err := svc.RunOnce(context.Background(), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 2 {
		t.Fatalf("generated %d digests, want 2", n)
	}
	// 作用域顺序稳定：先 a/ws2 再 b/default。
	if len(store.saved) != 2 || store.saved[0] != "a/ws2:2026-10-02" || store.saved[1] != "b/default:2026-10-02" {
		t.Errorf("saved = %v", store.saved)
	}
	if len(notifier.got) != 2 {
		t.Errorf("notifier got %d notifications, want 2", len(notifier.got))
	}
}

func TestDigestServiceFallsBackToPrimaryScope(t *testing.T) {
	store := &fakeDigestStore{} // 库里没有作用域
	notifier := &recordingNotifier{}
	svc := NewDigestService(store, notifier, Scope{UserID: "local", WorkspaceID: "default"}, DigestServiceOptions{Now: time.Now})
	n, err := svc.RunOnce(context.Background(), time.Now().UTC())
	if err != nil || n != 1 {
		t.Fatalf("fallback scope: n=%d err=%v", n, err)
	}
	if len(notifier.got) != 1 || notifier.got[0] != "local/default:"+DigestDateOf(time.Now().UTC()) {
		t.Errorf("notifier got = %v", notifier.got)
	}
}

// TestDigestServiceOneScopeFailureDoesNotBlockOthers 是"每天都要收到"的关键保证：
// 一个作用域炸了不能让别人也收不到。
func TestDigestServiceOneScopeFailureDoesNotBlockOthers(t *testing.T) {
	store := &fakeDigestStore{
		scopes:  []Scope{{UserID: "bad", WorkspaceID: "default"}, {UserID: "good", WorkspaceID: "default"}},
		failFor: "bad",
	}
	notifier := &recordingNotifier{}
	svc := NewDigestService(store, notifier, Scope{}, DigestServiceOptions{Now: time.Now})
	n, err := svc.RunOnce(context.Background(), time.Now().UTC())
	if err == nil {
		t.Error("expected an error for the failing scope")
	}
	if n != 1 {
		t.Errorf("generated %d, want 1 (the healthy scope)", n)
	}
	if len(notifier.got) != 1 || !strings.HasPrefix(notifier.got[0], "good/") {
		t.Errorf("notifier = %v, want only the healthy scope", notifier.got)
	}
}

func TestDigestServiceNotifyFailureDoesNotLoseTheDigest(t *testing.T) {
	store := &fakeDigestStore{scopes: []Scope{{UserID: "u", WorkspaceID: "w"}}}
	svc := NewDigestService(store, failingNotifier{}, Scope{}, DigestServiceOptions{Now: time.Now})
	n, err := svc.RunOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("notify failure must not surface as a run error: %v", err)
	}
	if n != 1 || len(store.saved) != 1 {
		t.Errorf("digest must still be saved: n=%d saved=%v", n, store.saved)
	}
}

type failingNotifier struct{}

func (failingNotifier) NotifyDigest(context.Context, Scope, *Digest) error {
	return errors.New("push channel down")
}

func TestDigestServiceNextRunRollsToTomorrow(t *testing.T) {
	svc := NewDigestService(&fakeDigestStore{}, nil, Scope{}, DigestServiceOptions{
		AtHour: 8, AtMinute: 30, Location: time.UTC,
	})
	cases := []struct{ from, want string }{
		{"2026-10-03T07:00:00Z", "2026-10-03T08:30:00Z"},
		{"2026-10-03T08:30:00Z", "2026-10-04T08:30:00Z"},
		{"2026-10-03T23:59:00Z", "2026-10-04T08:30:00Z"},
	}
	for _, c := range cases {
		from, _ := time.Parse(time.RFC3339, c.from)
		want, _ := time.Parse(time.RFC3339, c.want)
		if got := svc.nextRun(from); !got.Equal(want) {
			t.Errorf("nextRun(%s) = %s, want %s", c.from, got.Format(time.RFC3339), c.want)
		}
	}
}

// 越界的时刻必须被夹回合法范围，否则 nextRun 算出的时间永远到不了，日报静默不发。
func TestDigestServiceClampsOutOfRangeTime(t *testing.T) {
	svc := NewDigestService(&fakeDigestStore{}, nil, Scope{}, DigestServiceOptions{AtHour: 99, AtMinute: -3, Location: time.UTC})
	if svc.opt.AtHour != 8 || svc.opt.AtMinute != 30 {
		t.Errorf("clamped to %02d:%02d, want 08:30", svc.opt.AtHour, svc.opt.AtMinute)
	}
}
