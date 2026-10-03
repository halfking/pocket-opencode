package executors

// Tests for LearningDigestExecutor.
//
// This executor had no tests at all before the streak milestone was added to
// it (the P0 work verified it only through the build and the route layer), so
// these cover the digest's contract as a whole rather than just the new branch:
// "nothing to say -> say nothing", the daily summary push, quiet-hours
// rescheduling, and the exactly-once milestone announcement.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/learning"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// fakeLearningClient records what the digest asked for.
type fakeLearningClient struct {
	summary    *learning.DueSummary
	summaryErr error
	store      *learning.Store

	// streak / milestone
	streak    *learning.StreakView
	streakErr error
	claimed   bool
	claimErr  error
	claimDays []int

	advanced int
}

func (f *fakeLearningClient) DueSummary(context.Context, string, string) (*learning.DueSummary, error) {
	if f.summaryErr != nil {
		return nil, f.summaryErr
	}
	return f.summary, nil
}

func (f *fakeLearningClient) Advance(context.Context, string, string, learning.Reminder, int) (int64, error) {
	f.advanced++
	return 0, nil
}

func (f *fakeLearningClient) Store() *learning.Store { return f.store }

func (f *fakeLearningClient) Streak(context.Context, string, string, int64) (*learning.StreakView, error) {
	if f.streakErr != nil {
		return nil, f.streakErr
	}
	return f.streak, nil
}

func (f *fakeLearningClient) ClaimMilestoneAnnouncement(_ context.Context, _ string, _ string, milestoneDays int) (bool, error) {
	f.claimDays = append(f.claimDays, milestoneDays)
	if f.claimErr != nil {
		return false, f.claimErr
	}
	return f.claimed, nil
}

func digestTask(payload string) *scheduledtask.Task {
	return &scheduledtask.Task{
		ID:          "sched-1",
		WorkspaceID: "ws-1",
		UserID:      "alice",
		Kind:        scheduledtask.KindLearningDigest,
		Payload:     json.RawMessage(payload),
	}
}

func TestLearningDigestSilentWhenNothingIsDue(t *testing.T) {
	c := &fakeLearningClient{summary: &learning.DueSummary{}}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	res, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// A digest of four zeros teaches the user to ignore notifications.
	if len(notif.events) != 0 {
		t.Errorf("an empty digest must stay silent, got %+v", notif.events)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Output, &out)
	if out["notified"] != false {
		t.Errorf("result notified = %v, want false", out["notified"])
	}
}

func TestLearningDigestNotifiesWhenSomethingIsDue(t *testing.T) {
	c := &fakeLearningClient{summary: &learning.DueSummary{DueCards: 3}}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(notif.events) != 1 {
		t.Fatalf("expected 1 digest dispatch, got %d", len(notif.events))
	}
	if notif.events[0].Kind != "learning.digest.daily" {
		t.Errorf("kind = %q, want learning.digest.daily", notif.events[0].Kind)
	}
}

func TestLearningDigestRequiresUserID(t *testing.T) {
	c := &fakeLearningClient{summary: &learning.DueSummary{}}
	ex := NewLearningDigestExecutor(c, &fakeWorkNotifier{})
	if _, err := ex.Execute(context.Background(), &scheduledtask.Task{Kind: scheduledtask.KindLearningDigest}); err == nil {
		t.Error("a digest with no user identity must fail loudly")
	}
}

func TestLearningDigestRejectsBadPayload(t *testing.T) {
	c := &fakeLearningClient{summary: &learning.DueSummary{}}
	ex := NewLearningDigestExecutor(c, &fakeWorkNotifier{})
	if _, err := ex.Execute(context.Background(), digestTask(`not-json`)); err == nil {
		t.Error("a malformed payload must fail loudly")
	}
}

// reschedule_reminders defaults to true, which needs a real store to list due
// reminders. With no store the executor must say so instead of silently
// skipping the reschedule.
func TestLearningDigestRescheduleNeedsStore(t *testing.T) {
	c := &fakeLearningClient{summary: &learning.DueSummary{}}
	ex := NewLearningDigestExecutor(c, &fakeWorkNotifier{})
	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice"}`)); err == nil {
		t.Error("rescheduling without a store must fail rather than silently skip")
	}
}

// --- streak milestone ---

func TestLearningDigestAnnouncesMilestoneOnce(t *testing.T) {
	c := &fakeLearningClient{
		summary: &learning.DueSummary{},
		streak:  &learning.StreakView{Streak: learning.Streak{Current: 7, ActiveToday: true}, Milestone: 7},
		claimed: true,
	}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	res, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(c.claimDays) != 1 || c.claimDays[0] != 7 {
		t.Fatalf("expected one claim for milestone 7, got %v", c.claimDays)
	}
	if len(notif.events) != 1 {
		t.Fatalf("expected one milestone dispatch, got %+v", notif.events)
	}
	ev := notif.events[0]
	if ev.Kind != "learning.streak.milestone" {
		t.Errorf("kind = %q, want learning.streak.milestone", ev.Kind)
	}
	// A milestone is worth saying even when nothing is due.
	var out map[string]any
	_ = json.Unmarshal(res.Output, &out)
	if out["notified"] != true {
		t.Errorf("notified = %v, want true", out["notified"])
	}
	if out["milestone"] != float64(7) {
		t.Errorf("milestone = %v, want 7", out["milestone"])
	}
}

// The claim is what makes this exactly-once: a second run finds the claim
// already taken and stays silent.
func TestLearningDigestMilestoneIsNotRepeated(t *testing.T) {
	c := &fakeLearningClient{
		summary: &learning.DueSummary{},
		streak:  &learning.StreakView{Streak: learning.Streak{Current: 30}, Milestone: 30},
		claimed: false, // the claim is already held
	}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(c.claimDays) != 1 {
		t.Errorf("the claim should still be attempted, got %v", c.claimDays)
	}
	if len(notif.events) != 0 {
		t.Errorf("an already-claimed milestone must not be announced again, got %+v", notif.events)
	}
}

func TestLearningDigestNoMilestoneBelowThree(t *testing.T) {
	c := &fakeLearningClient{
		summary: &learning.DueSummary{},
		streak:  &learning.StreakView{Streak: learning.Streak{Current: 2}},
		claimed: true,
	}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(c.claimDays) != 0 {
		t.Errorf("a 2-day streak must not claim a milestone, got %v", c.claimDays)
	}
	if len(notif.events) != 0 {
		t.Errorf("a 2-day streak must not notify, got %+v", notif.events)
	}
}

// A cosmetic feature must never take down the digest.
func TestLearningDigestSurvivesStreakFailure(t *testing.T) {
	c := &fakeLearningClient{
		summary:   &learning.DueSummary{DueCards: 2},
		streakErr: errors.New("db down"),
	}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("a streak read failure must not fail the digest: %v", err)
	}
	// The summary it did manage to build is still delivered.
	if len(notif.events) != 1 || notif.events[0].Kind != "learning.digest.daily" {
		t.Errorf("expected the daily summary to still go out, got %+v", notif.events)
	}
}

func TestLearningDigestSurvivesClaimFailure(t *testing.T) {
	c := &fakeLearningClient{
		summary:  &learning.DueSummary{},
		streak:   &learning.StreakView{Streak: learning.Streak{Current: 14}, Milestone: 14},
		claimErr: errors.New("conflict"),
	}
	notif := &fakeWorkNotifier{}
	ex := NewLearningDigestExecutor(c, notif)

	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("a claim failure must not fail the digest: %v", err)
	}
	if len(notif.events) != 0 {
		t.Errorf("without a claim nothing may be announced, got %+v", notif.events)
	}
}

// A nil notifier (remote-only / notifycenter not ready yet) must not panic on
// the milestone path either.
func TestLearningDigestMilestoneToleratesNilNotifier(t *testing.T) {
	c := &fakeLearningClient{
		summary: &learning.DueSummary{},
		streak:  &learning.StreakView{Streak: learning.Streak{Current: 7}, Milestone: 7},
		claimed: true,
	}
	ex := NewLearningDigestExecutor(c, nil)
	if _, err := ex.Execute(context.Background(), digestTask(`{"user_id":"alice","reschedule_reminders":false}`)); err != nil {
		t.Fatalf("Execute with a nil notifier: %v", err)
	}
	if len(c.claimDays) != 0 {
		t.Error("with no notifier the milestone must not even be claimed; a later run should announce it")
	}
}

func TestLearningDigestKindIsRegistered(t *testing.T) {
	found := false
	for _, k := range scheduledtask.AllKinds() {
		if k == scheduledtask.KindLearningDigest {
			found = true
		}
	}
	if !found {
		t.Errorf("%q is missing from AllKinds()", scheduledtask.KindLearningDigest)
	}
	if got := (*LearningDigestExecutor)(nil).Kind(); got != scheduledtask.KindLearningDigest {
		t.Errorf("Kind() = %q", got)
	}
}

var _ = notifycenter.Event{}
