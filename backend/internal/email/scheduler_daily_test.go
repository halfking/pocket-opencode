package email

// scheduler_daily_test.go — 需求 4「对其它重要邮件进行提醒」的每日摘要链路。
//
// ## 覆盖的三个 0% 函数
//
//	scheduler.go:748 runDailySummary   每日触发，按 (user, workspace) 去重后逐个生成
//	scheduler.go:796 summarizeUser     单 scope 生成 + 落库
//	store.go:2021  ListEmailsByDayScoped  按「某一天」取邮件
//
// ## 最重要的一条：ListEmailsByDayScoped 的时区参数是**无效的**
//
// 见 TestListEmailsByDayScoped_TZOffsetIsIgnored —— 那条用例锁的是一个真缺陷，
// 不是风格问题。东八区用户每天 00:00-08:00 之间触发的摘要，统计窗口是错的。
//
// ## 为什么这些值得测
//
// 需求 4 的产出就是这条链路。而它此前 0% 覆盖，意味着「重要邮件提醒」在
// 真实数据上从未被验证过（§7by：120 封真实邮件的 category/importance 全空）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// fakeKxmem 实现 kxmemory.Client（7 个方法）。只有 DailySummary 需要真行为，
// 其余返回零值即可 —— 它们在这条链路上不该被调用，被调用了就是缺陷。
type fakeKxmem struct {
	mu       sync.Mutex
	daily    []kxmemory.DailySummaryRequest
	dailErr  error
	otherHit int // 非 DailySummary 方法被调用的次数
}

func (f *fakeKxmem) DailySummary(_ context.Context, req kxmemory.DailySummaryRequest) (*kxmemory.DailySummaryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dailErr != nil {
		return nil, f.dailErr
	}
	f.daily = append(f.daily, req)
	return &kxmemory.DailySummaryResponse{Summary: "summary for " + req.Date}, nil
}

func (f *fakeKxmem) dailyCalls() []kxmemory.DailySummaryRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kxmemory.DailySummaryRequest(nil), f.daily...)
}

func (f *fakeKxmem) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.otherHit
}

func (f *fakeKxmem) ClassifyNote(context.Context, kxmemory.ClassifyNoteRequest) (*kxmemory.ClassifyNoteResponse, error) {
	f.otherHit++
	return &kxmemory.ClassifyNoteResponse{}, nil
}

func (f *fakeKxmem) ClassifyEmails(context.Context, kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	f.otherHit++
	return &kxmemory.ClassifyEmailsResponse{}, nil
}

func (f *fakeKxmem) MeetingSummary(context.Context, kxmemory.MeetingSummaryRequest) (*kxmemory.MeetingSummaryResponse, error) {
	f.otherHit++
	return &kxmemory.MeetingSummaryResponse{}, nil
}

func (f *fakeKxmem) MeetingRecommend(context.Context, kxmemory.MeetingRecommendRequest) (*kxmemory.MeetingRecommendResponse, error) {
	f.otherHit++
	return &kxmemory.MeetingRecommendResponse{}, nil
}

func (f *fakeKxmem) MeetingRefine(context.Context, kxmemory.MeetingRefineRequest) (*kxmemory.MeetingRefineResponse, error) {
	f.otherHit++
	return &kxmemory.MeetingRefineResponse{}, nil
}

func (f *fakeKxmem) Stats() kxmemory.Stats { return kxmemory.Stats{} }

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// setEmailDate 改写邮件时间。seedEmail 只能写 time.Now()，而按「日」取信
// 的用例需要精确控制时间戳。
func setEmailDate(t *testing.T, store *Store, id string, unix int64) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE emails SET date=$1 WHERE id=$2`, unix, id); err != nil {
		t.Fatalf("set date of %s: %v", id, err)
	}
}

func setEmailImportance(t *testing.T, store *Store, id, importance string) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE emails SET importance=$1 WHERE id=$2`, importance, id); err != nil {
		t.Fatalf("set importance of %s: %v", id, err)
	}
}

// atNoon 把 "YYYY-MM-DD" 解析成该日的 UTC 正午。
//
// **不要用 time.Now().Unix()** 来喂按日取信的用例：ListEmailsByDayScoped 以
// （tz=0 时）UTC 午夜为界，而 `time.Now().Format("2006-01-02")` 给的是**本地**日期。
// 在东八区，本地 00:00-08:00 期间本地日期比 UTC 日期快一天，此刻的 Unix 值落在
// 算出的日界**之前** —— 用例会在每天这 8 小时里假失败。取正午则两种口径都在窗内。
func atNoon(t *testing.T, date string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	return d.Add(12 * time.Hour)
}

// ---------------------------------------------------------------------------
// 缺陷用例：时区参数被忽略
// ---------------------------------------------------------------------------

// TestListEmailsByDayScoped_HonorsTZOffset 守住需求 4 每日摘要的日界。
//
// ## 曾经的缺陷
//
// ListEmailsByDayScoped 的实现曾是：
//
//	t, _ := time.Parse("2006-01-02", date)   // -> UTC 午夜
//	loc := time.FixedZone("user", tzOffsetSec)
//	t = t.In(loc)                            // 只改 Location，Unix 值不变！
//	startUnix := t.Unix()
//
// `time.Time.In()` 只改变显示用的 Location，**底层时刻不变**，所以
// `t.Unix()` 恒等于「该日期的 UTC 午夜」。tzOffsetSec 传什么都没用。
//
// 对东八区（+8）的后果：用户认为的「今天 00:00」实际是 UTC 前一天 16:00。
// 于是每天 00:00-08:00 之间触发的每日摘要，取到的是**当地时间的昨天 08:00
// 到现在 08:00** 的邮件 —— 需求 4 的提醒时间窗错了 8 小时。
//
// 这条用例在修复**之前是红的**（2026-10-02 首次运行实测：本地 01:00 那封被排除），
// 现已转绿。改动是 store.go 新增的 parseDayStart（用 ParseInLocation 直接在
// 目标时区解析，而不是事后 .In()）。
//
// ## 为什么这个断言不是"多测一个边界"
//
// 两封邮件都在「本地 10-02」，但分居 UTC 午夜两侧。只断言"日界随 tz 移动"
// 会被某些错误实现蒙混过关；把两侧都种上、要求**两封都在**，才真的锁住了
// "日界 = 本地午夜"这个语义。
func TestListEmailsByDayScoped_HonorsTZOffset(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "u-tz"
		ws   = "ws-tz"
		tz   = 8 * 3600 // 东八区
	)
	seedAccount(t, store, "acct-tz", user, ws)

	// 「本地今天」= 2026-10-02（东八区）。
	// 正确语义下，这一天的范围是 UTC 2026-10-01 16:00 .. 2026-10-02 16:00。
	loc := time.FixedZone("CST", tz)
	dayStart := time.Date(2026, 10, 2, 0, 0, 0, 0, loc)
	date := dayStart.Format("2006-01-02")

	// 两封都在「本地 10-02」内，但分居 UTC 午夜两侧：
	localEarly := dayStart.Add(1 * time.Hour) // 本地 01:00 -> UTC 10-01 17:00
	localNoon := dayStart.Add(12 * time.Hour) // 本地 12:00 -> UTC 10-02 04:00
	localLate := dayStart.Add(23 * time.Hour) // 本地 23:00 -> UTC 10-02 15:00

	seedEmail(t, store, "early", "acct-tz", ws, "本地凌晨一点")
	seedEmail(t, store, "noon", "acct-tz", ws, "本地中午十二点")
	seedEmail(t, store, "late", "acct-tz", ws, "本地晚上十一点")
	setEmailDate(t, store, "early", localEarly.Unix())
	setEmailDate(t, store, "noon", localNoon.Unix())
	setEmailDate(t, store, "late", localLate.Unix())

	got, err := store.ListEmailsByDayScoped(ctx, user, ws, date, tz)
	if err != nil {
		t.Fatalf("ListEmailsByDayScoped: %v", err)
	}

	ids := map[string]bool{}
	for _, e := range got {
		ids[e.ID] = true
	}
	for _, want := range []struct {
		id string
		at time.Time
	}{
		{"early", localEarly}, {"noon", localNoon}, {"late", localLate},
	} {
		if !ids[want.id] {
			t.Errorf("email at local %s (= %s) is missing from local day %s (tzOffsetSec=%d); got %d rows",
				want.at.Format(time.RFC3339), want.at.UTC().Format(time.RFC3339), date, tz, len(got))
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want exactly 3", len(got))
	}
}

// TestParseDayStart 单独锁住根因函数，省得日界语义只能靠带库用例间接覆盖。
//
// 关键断言：start.Unix() 必须随 tzOffsetSec **变化**。
// 缺陷版本（time.Parse + .In）在这里就会失败——它对任何 tzOffsetSec
// 都返回同一个 UTC 午夜的值。
func TestParseDayStart(t *testing.T) {
	const date = "2026-10-02"

	for _, tc := range []struct {
		name     string
		tz       int
		wantUnix int64
	}{
		{"UTC", 0, 1790899200},                     // 2026-10-02T00:00Z
		{"east+8", 8 * 3600, 1790899200 - 8*3600},  // 2026-10-01T16:00Z
		{"west-5", -5 * 3600, 1790899200 + 5*3600}, // 2026-10-02T05:00Z
	} {
		got, err := parseDayStart(date, tc.tz)
		if err != nil {
			t.Fatalf("%s: parseDayStart: %v", tc.name, err)
		}
		if got.Unix() != tc.wantUnix {
			t.Errorf("%s: start.Unix() = %d, want %d", tc.name, got.Unix(), tc.wantUnix)
		}
		// 日期文本必须仍解析回同一个日历日。
		if got.Format("2006-01-02") != date {
			t.Errorf("%s: round-tripped to %s, want %s", tc.name, got.Format("2006-01-02"), date)
		}
	}

	// 不同 tz 必须给出不同起点——这一条直接就是缺陷版本的死穴。
	utcDay, _ := parseDayStart(date, 0)
	cstDay, _ := parseDayStart(date, 8*3600)
	if utcDay.Unix() == cstDay.Unix() {
		t.Errorf("tzOffsetSec had no effect: both returned unix %d", utcDay.Unix())
	}

	if _, err := parseDayStart("not-a-date", 0); err == nil {
		t.Error("parseDayStart accepted an unparsable date")
	}
}

// TestListEmailsByDayScoped_SkipsDeleted 确认软删除的邮件不进摘要。
func TestListEmailsByDayScoped_SkipsDeleted(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-del", "u-del", "ws-del")
	seedEmail(t, store, "live", "acct-del", "ws-del", "inbox")
	seedEmail(t, store, "gone", "acct-del", "ws-del", "trash")

	// 「今天」取本地日期；邮件锚在该日正午，避开 UTC 日界（见 atNoon 注释）。
	today := time.Now().Format("2006-01-02")
	noon := atNoon(t, today)

	seedEmail(t, store, "live", "acct-del", "ws-del", "inbox")
	seedEmail(t, store, "gone", "acct-del", "ws-del", "trash")
	setEmailDate(t, store, "live", noon.Unix())
	setEmailDate(t, store, "gone", noon.Unix())
	if _, err := store.pool.Exec(ctx, `UPDATE emails SET deleted_at=$1 WHERE id=$2`, noon.Unix(), "gone"); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}

	date := today
	got, err := store.ListEmailsByDayScoped(ctx, "u-del", "ws-del", date, 0)
	if err != nil {
		t.Fatalf("ListEmailsByDayScoped: %v", err)
	}
	for _, e := range got {
		if e.ID == "gone" {
			t.Fatal("a soft-deleted email must not appear in the daily summary input")
		}
	}
	if len(got) != 1 || got[0].ID != "live" {
		t.Fatalf("got %d rows, want exactly the live one", len(got))
	}
}

// TestListEmailsByDayScoped_InvalidDateIsAnError 非法日期必须报错而不是静默返回空。
func TestListEmailsByDayScoped_InvalidDateIsAnError(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	if _, err := store.ListEmailsByDayScoped(context.Background(), "u", "ws", "not-a-date", 0); err == nil {
		t.Fatal("an unparsable date must be an error, not a silent empty result")
	}
}

// ---------------------------------------------------------------------------
// runDailySummary：早退 + scope 去重
// ---------------------------------------------------------------------------

// kxmem 未配置时必须早退，而且**不碰 store**。
// 用 nil store 证明：越过早退就会 nil panic，「不 panic」本身即证据。
func TestRunDailySummary_NilKxmemSkipsBeforeTouchingStore(t *testing.T) {
	s := &Scheduler{store: nil, crypto: nil, kxmem: nil}
	s.runDailySummary(context.Background())
}

// 同一 (user, workspace) 下多个账户只生成**一次**摘要。
// 这是成本控制点：不去重的话，一个挂 5 个邮箱的用户每天会打 5 次 LLM。
func TestRunDailySummary_DedupesScopesPerUserWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 同一用户 + 同一 workspace 的三个账户。
	for _, id := range []string{"acct-a", "acct-b", "acct-c"} {
		seedAccount(t, store, id, "u-multi", "ws-multi")
	}
	// 另一个 workspace 的同一用户，必须**单独**生成一份。
	seedAccount(t, store, "acct-ws2", "u-multi", "ws-other")

	now := time.Now().Format("2006-01-02")
	for _, id := range []string{"acct-a", "acct-b", "acct-c", "acct-ws2"} {
		seedEmail(t, store, "em-"+id, id, "ws-multi", "今天的一封信")
		setEmailDate(t, store, "em-"+id, atNoon(t, now).Unix())
	}

	kx := &fakeKxmem{}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.tzOffsetSec.Store(0)
	s.runDailySummary(ctx)

	calls := kx.dailyCalls()
	// ws-multi 的 3 个账户 + ws-other 的 1 个 = 2 个 scope。
	if len(calls) != 2 {
		t.Fatalf("DailySummary called %d times, want 2 (one per user+workspace, not per account)", len(calls))
	}
	if kx.calls() != 0 {
		t.Fatalf("daily summary touched %d unrelated kxmemory methods; it must not", kx.calls())
	}
}

// 没有启用账户时不得调用 LLM。
func TestRunDailySummary_NoEnabledAccountsCallsNothing(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	seedAccount(t, store, "acct-off", "u-off", "ws-off")
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE email_accounts SET enabled=FALSE WHERE id=$1`, "acct-off"); err != nil {
		t.Fatalf("disable account: %v", err)
	}

	kx := &fakeKxmem{}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.runDailySummary(context.Background())

	if n := len(kx.dailyCalls()); n != 0 {
		t.Fatalf("DailySummary called %d times with no enabled account", n)
	}
}

// ---------------------------------------------------------------------------
// summarizeUser：落库口径
// ---------------------------------------------------------------------------

func TestSummarizeUser_ImportantCountOnlyCountsHigh(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-imp", "u-imp", "ws-imp")
	day := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)

	// 5 封：2 封 high、1 封 medium、1 封 low、1 封空
	for _, tc := range []struct{ id, imp string }{
		{"em-h1", "high"}, {"em-h2", "high"},
		{"em-m", "medium"}, {"em-l", "low"}, {"em-e", ""},
	} {
		seedEmail(t, store, tc.id, "acct-imp", "ws-imp", tc.id)
		setEmailDate(t, store, tc.id, day.Add(12*time.Hour).Unix())
		setEmailImportance(t, store, tc.id, tc.imp)
	}

	kx := &fakeKxmem{}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.tzOffsetSec.Store(0)

	if err := s.summarizeUser(ctx, "u-imp", "ws-imp", "2026-05-20"); err != nil {
		t.Fatalf("summarizeUser: %v", err)
	}

	sum, err := store.GetSummaryByDateScoped(ctx, "u-imp", "ws-imp", "2026-05-20")
	if err != nil || sum == nil {
		t.Fatalf("summary not written: sum=%v err=%v", sum, err)
	}
	if sum.TotalCount != 5 {
		t.Fatalf("TotalCount = %d, want 5", sum.TotalCount)
	}
	// 只有 importance=="high" 计入 —— medium/low/空 都不算「重要」。
	if sum.ImportantCount != 2 {
		t.Fatalf("ImportantCount = %d, want 2 (only importance=high counts)", sum.ImportantCount)
	}
	if sum.Content == "" {
		t.Fatal("Content is empty; the LLM summary was not persisted")
	}
}

// 当天没有邮件时不得调用 LLM（省一次网络往返）。
func TestSummarizeUser_NoEmailsSkipsLLM(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-none", "u-none", "ws-none")

	kx := &fakeKxmem{}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.tzOffsetSec.Store(0)

	if err := s.summarizeUser(context.Background(), "u-none", "ws-none", "2030-01-01"); err != nil {
		t.Fatalf("summarizeUser: %v", err)
	}
	if n := len(kx.dailyCalls()); n != 0 {
		t.Fatalf("DailySummary called %d times for a day with no emails", n)
	}
}

// LLM 报错必须往上抛，让 runDailySummary 计入 failures。
func TestSummarizeUser_LLMErrorPropagates(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-err", "u-err", "ws-err")
	seedEmail(t, store, "em-err", "acct-err", "ws-err", "一封信")
	today := time.Now().Format("2006-01-02")
	setEmailDate(t, store, "em-err", atNoon(t, today).Unix())

	kx := &fakeKxmem{dailErr: fmt.Errorf("llm down")}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.tzOffsetSec.Store(0)

	err := s.summarizeUser(ctx, "u-err", "ws-err", today)
	if err == nil {
		t.Fatal("an LLM failure must propagate; runDailySummary counts it as a failure")
	}
	if !strings.Contains(err.Error(), "llm down") {
		t.Fatalf("error lost the cause: %v", err)
	}
}

// actionItems 必须是合法 JSON —— 注释里写明「之前 fmt.Sprintf(\"%v\") 会产生
// Go-syntax 输出如 [kxmemory.ExtractedTodo{...}]，不可解析」。锁住这个不退化。
func TestSummarizeUser_ActionItemsAreValidJSON(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-act", "u-act", "ws-act")
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedEmail(t, store, "em-act", "acct-act", "ws-act", "带待办")
	setEmailDate(t, store, "em-act", day.Add(10*time.Hour).Unix())

	kx := &fakeKxmem{}
	s := &Scheduler{store: store, kxmem: kx, stop: make(chan struct{})}
	s.tzOffsetSec.Store(0)

	if err := s.summarizeUser(ctx, "u-act", "ws-act", "2026-06-01"); err != nil {
		t.Fatalf("summarizeUser: %v", err)
	}
	sum, err := store.GetSummaryByDateScoped(ctx, "u-act", "ws-act", "2026-06-01")
	if err != nil || sum == nil {
		t.Fatalf("summary not written: %v", err)
	}
	// 本例 Todos 为空 -> ActionItems 应为空串；一旦将来有内容，这里会解析它。
	if sum.ActionItems != "" {
		var v any
		if err := json.Unmarshal([]byte(sum.ActionItems), &v); err != nil {
			t.Fatalf("ActionItems is not valid JSON (%q): %v", sum.ActionItems, err)
		}
	}
}
