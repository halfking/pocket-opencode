package executors

// B-11, executor half: the do-not-disturb window belongs to the *owner* of the
// work item, resolved from their stored settings.
//
// The previous behaviour applied one server-wide window computed on the UTC day
// boundary, so a user eight hours off UTC got no deferral at all. These cases
// pin the wiring: the owner is asked, the answer is used, and a missing or
// broken preference never silences a reminder.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// fakeQuietPrefs answers per user and counts the lookups.
type fakeQuietPrefs struct {
	byUser map[string]task.QuietPreferences
	calls  map[string]int
}

func (f *fakeQuietPrefs) QuietPreferences(_ context.Context, _, userID string) (task.QuietPreferences, bool) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[userID]++
	p, ok := f.byUser[userID]
	return p, ok
}

// wallClockAt 返回「loc 时区今天 hh:mm」的 UNIX 秒；若那一刻**还没到**，退回昨天。
//
// ## 为什么不能钉死绝对日期
//
// executor 用的是墙上时钟（workitem_reminder.go:172 `now := time.Now().Unix()`），
// 且有 24 小时的陈旧上界（`:99` `DefaultStaleAfter`，判定在 `:197`，且**排在
// 免打扰之前**）。这里原来写死 `2026-09-30`：写完当天绿，跨过 24h 之后**永久红**。
//
// 2026-10-01 22:12 实测：`FallsBackWithoutPreferences` 报
// `a user with no stored preferences must still get their reminder, got 0 events` ——
// 夹具已 34.2 小时 > 24h，被判陈旧后直接 `ClearTaskRemindAt(..., 0)` 退役，
// 根本没走到免打扰逻辑。症状与「免打扰没生效」几乎一样，极易误判方向。
//
// 退回昨天而不是往后推，是因为 `DueTaskReminders(ctx, wsID, now, limit)` 只取
// `remind_at <= now`，往后的夹具**根本不会到期**。
func wallClockAt(t *testing.T, loc *time.Location, hour, minute int) int64 {
	t.Helper()
	now := time.Now()
	y, m, d := now.In(loc).Date()
	w := time.Date(y, m, d, hour, minute, 0, 0, loc)
	if w.After(now) {
		w = w.AddDate(0, 0, -1)
	}
	return w.Unix()
}

// freshExecutor 造一个 executor，并把陈旧上界关掉。
//
// **陈旧不是这些用例的主题** —— 它们测的是免打扰窗口属于 owner、四条分支各自
// 的判定。留着 24h 上界只会让夹具随日历腐烂（见 wallClockAt 的注释）。姊妹文件
// `workitem_reminder_test.go:468` 早就用 `SetStaleAfter(0)` 做了同样的事，这里对齐。
// 真要验陈旧行为，去 TestWorkItemReminderStaleBoundIsConfigurable。
func freshExecutor(store *fakeWorkItemStore, notif *fakeWorkNotifier) *WorkItemReminderExecutor {
	ex := NewWorkItemReminderExecutor(store, notif)
	ex.SetStaleAfter(0)
	return ex
}

// At 23:50 Shanghai time the reminder must be deferred; the same instant read
// on the old UTC day boundary looked like 15:50 and sailed through.
func TestWorkItemReminderDefersInTheOwnersTimezone(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 23:50 Shanghai time, on the day that wall time most recently came round.
	fireAt := wallClockAt(t, sh, 23, 50)

	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Late", OwnerID: "alice", WorkspaceID: "ws-1", RemindAt: fireAt}}
	prefs := &fakeQuietPrefs{byUser: map[string]task.QuietPreferences{
		"alice": {Timezone: "Asia/Shanghai"},
	}}
	notif := &fakeWorkNotifier{}
	ex := freshExecutor(store, notif)
	ex.SetQuietPreferences(prefs)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if num(t, decode(t, res), "deferred") != 1 {
		t.Fatalf("a 23:50 Shanghai reminder was not deferred: %+v", decode(t, res))
	}
	if len(notif.events) != 0 {
		t.Error("a deferred reminder must not notify")
	}
	moved, ok := store.cleared["t-1"]
	if !ok {
		t.Fatal("the deferral did not rewrite remind_at")
	}
	got := time.Unix(moved, 0).In(sh)
	if got.Hour() != 7 || got.Minute() != 30 {
		t.Errorf("deferred to %s, want 07:30 Asia/Shanghai", got.Format(time.RFC3339))
	}
	if prefs.calls["alice"] != 1 {
		t.Errorf("owner looked up %d times, want 1", prefs.calls["alice"])
	}
}

// Without stored preferences the executor must behave as before, not skip the
// reminder: the server default window applies.
func TestWorkItemReminderFallsBackWithoutPreferences(t *testing.T) {
	store := newFakeStore()
	// A time well outside any window, so the only question is whether the
	// reminder still fires.
	store.due = []task.Task{{ID: "t-1", Title: "Noon", OwnerID: "alice", WorkspaceID: "ws-1",
		RemindAt: wallClockAt(t, time.Local, 12, 0)}}
	ex := freshExecutor(store, &fakeWorkNotifier{})
	ex.SetQuietPreferences(&fakeQuietPrefs{}) // knows nobody

	if _, err := ex.Execute(context.Background(), schedTask()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(store.events) != 1 {
		t.Errorf("a user with no stored preferences must still get their reminder, got %d events", len(store.events))
	}
}

// A user who has switched do-not-disturb off must not be deferred, even though
// the default window would have caught them.
func TestWorkItemReminderHonoursDisabledQuietHours(t *testing.T) {
	store := newFakeStore()
	store.due = []task.Task{{ID: "t-1", Title: "Night owl", OwnerID: "alice", WorkspaceID: "ws-1",
		RemindAt: wallClockAt(t, time.UTC, 23, 50)}}
	prefs := &fakeQuietPrefs{byUser: map[string]task.QuietPreferences{
		"alice": {Timezone: "UTC", Disabled: true},
	}}
	notif := &fakeWorkNotifier{}
	ex := freshExecutor(store, notif)
	ex.SetQuietPreferences(prefs)

	res, err := ex.Execute(context.Background(), schedTask())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if num(t, decode(t, res), "deferred") != 0 {
		t.Error("do-not-disturb is off for this user; the reminder must fire at its own time")
	}
	if len(notif.events) == 0 {
		t.Error("the reminder should have been delivered")
	}
}

func mustResult(t *testing.T, ex *WorkItemReminderExecutor, st *scheduledtask.Task) *scheduledtask.Result {
	t.Helper()
	res, err := ex.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

// Two owners in one batch are each judged by their own clock; one deferred, one
// not, in the same tick.
func TestWorkItemReminderJudgesEachOwnerSeparately(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// One instant: 23:50 in Shanghai is 10:50 in New York.
	instant := wallClockAt(t, sh, 23, 50)

	store := newFakeStore()
	store.due = []task.Task{
		{ID: "cn", Title: "Shanghai", OwnerID: "alice", WorkspaceID: "ws-1", RemindAt: instant},
		{ID: "us", Title: "New York", OwnerID: "bob", WorkspaceID: "ws-1", RemindAt: instant},
	}
	prefs := &fakeQuietPrefs{byUser: map[string]task.QuietPreferences{
		"alice": {Timezone: "Asia/Shanghai"},
		"bob":   {Timezone: "America/New_York"},
	}}
	ex := freshExecutor(store, &fakeWorkNotifier{})
	ex.SetQuietPreferences(prefs)

	res := mustResult(t, ex, schedTask())
	if num(t, decode(t, res), "deferred") != 1 {
		t.Fatalf("exactly one of the two owners is inside their own quiet window: %+v", decode(t, res))
	}
	if num(t, decode(t, res), "fired") != 1 {
		t.Errorf("the other owner should have been reminded: %+v", decode(t, res))
	}
	if _, moved := store.cleared["cn"]; !moved {
		t.Error("the Shanghai owner's reminder was not deferred")
	}
	if len(store.events) != 1 || store.events[0].TaskID != "us" {
		t.Errorf("only the New York owner's reminder should have fired, got %+v", store.events)
	}
}

// fakeSettingsReader stands in for the per-user settings store, keyed the way
// usersetting.Store addresses a document.
type fakeSettingsReader struct {
	records map[string]string
	seen    []string
}

func settingsKey(userID, wsID, namespace, id string) string {
	return userID + "|" + wsID + "|" + namespace + "|" + id
}

func (f *fakeSettingsReader) Get(userID, workspaceID, namespace, id string) (*usersetting.Record, error) {
	key := settingsKey(userID, workspaceID, namespace, id)
	f.seen = append(f.seen, key)
	payload, ok := f.records[key]
	if !ok {
		// The real store answers (nil, nil) for a document that does not exist.
		return nil, nil
	}
	return &usersetting.Record{
		UserID: userID, WorkspaceID: workspaceID,
		Namespace: namespace, ID: id, Payload: json.RawMessage(payload),
	}, nil
}

// The settings adapter: one read per user per tick, and every failure mode
// falls back rather than erroring.
func TestSettingsQuietPreferences(t *testing.T) {
	t.Run("reads the stored document", func(t *testing.T) {
		reader := &fakeSettingsReader{records: map[string]string{
			"alice|ws-1|notifications|quiet-hours": `{"timezone":"Asia/Shanghai","startMin":1350,"endMin":450}`,
		}}
		lookup := NewSettingsQuietPreferences(reader)
		prefs, ok := lookup.QuietPreferences(context.Background(), "ws-1", "alice")
		if !ok {
			t.Fatal("the stored document was not found")
		}
		if prefs.Timezone != "Asia/Shanghai" || prefs.Window() != (task.QuietWindow{StartMin: 1350, EndMin: 450}) {
			t.Errorf("prefs = %+v", prefs)
		}
	})

	t.Run("a missing document is not an error", func(t *testing.T) {
		lookup := NewSettingsQuietPreferences(&fakeSettingsReader{})
		if _, ok := lookup.QuietPreferences(context.Background(), "ws-1", "nobody"); ok {
			t.Error("an absent preference must report ok=false so the caller uses its default")
		}
	})

	t.Run("a corrupt document is not an error", func(t *testing.T) {
		lookup := NewSettingsQuietPreferences(&fakeSettingsReader{records: map[string]string{
			"alice|ws-1|notifications|quiet-hours": `{"timezone":`,
		}})
		if _, ok := lookup.QuietPreferences(context.Background(), "ws-1", "alice"); ok {
			t.Error("a malformed document must fall back, not be honoured")
		}
	})

	t.Run("a missing store is not an error", func(t *testing.T) {
		lookup := NewSettingsQuietPreferences(nil)
		if _, ok := lookup.QuietPreferences(context.Background(), "ws-1", "alice"); ok {
			t.Error("with no settings store there are no preferences")
		}
	})

	t.Run("an empty user is never looked up", func(t *testing.T) {
		reader := &fakeSettingsReader{}
		lookup := NewSettingsQuietPreferences(reader)
		if _, ok := lookup.QuietPreferences(context.Background(), "ws-1", ""); ok {
			t.Error("an empty user id must not resolve to somebody's preferences")
		}
		if len(reader.seen) != 0 {
			t.Errorf("the store was queried for an empty user: %v", reader.seen)
		}
	})

	t.Run("the document round-trips through the real JSON shape", func(t *testing.T) {
		raw, err := json.Marshal(task.QuietPreferences{Timezone: "Europe/Berlin", StartMin: 60, EndMin: 300})
		if err != nil {
			t.Fatal(err)
		}
		prefs, ok := task.QuietPreferencesFromPayload(raw)
		if !ok || prefs.Location() == nil || prefs.Location().String() != "Europe/Berlin" {
			t.Errorf("round trip failed: %+v ok=%v", prefs, ok)
		}
	})
}

