package executors

// LearningDigestExecutor is the daily "come back and learn" nudge
// (docs/学习muse/03-架构方案.md §3.3).
//
// It deliberately sits next to — not instead of — FlashcardReviewExecutor:
//   - flashcard_review answers "how many cards are due" (card domain truth)
//   - learning_digest answers "what deserves my attention today at all",
//     spanning due cards + unprocessed captured material + due work items,
//     and also reschedules the learning reminders that just fired.
//
// The "nothing to say -> say nothing" rule is the important part: a digest that
// reports four zeros teaches the user to ignore notifications, so the executor
// returns a successful run with notified=false instead of dispatching.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/learning"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// LearningClient is the subset of learning.Service the executor needs.
type LearningClient interface {
	DueSummary(ctx context.Context, wsID, userID string) (*learning.DueSummary, error)
	Advance(ctx context.Context, wsID, userID string, r learning.Reminder, quietEndMinute int) (int64, error)
	Streak(ctx context.Context, wsID, userID string, tzOffsetSec int64) (*learning.StreakView, error)
	ClaimMilestoneAnnouncement(ctx context.Context, wsID, userID string, milestoneDays int) (bool, error)
	Store() *learning.Store
}

// NotificationClient is reused from the flashcard executor: both dispatch the
// same notifycenter surface, so one interface serves both and tests can inject
// one fake for either executor.

// LearningDigestExecutor implements scheduledtask.KindLearningDigest.
type LearningDigestExecutor struct {
	learning LearningClient
	notif    NotificationClient
	// quietEndMinute is the end of the user's quiet hours, in minutes from
	// local midnight. Reminders that fire inside the window are rescheduled
	// to the window end instead of being dropped.
	quietEndMinute int
}

// NewLearningDigestExecutor wires the learning client and the (possibly nil)
// notification client. Notification is late-bound the same way the flashcard
// executor is, because main.go constructs the scheduler before notifycenter.
func NewLearningDigestExecutor(lc LearningClient, notif NotificationClient) *LearningDigestExecutor {
	return &LearningDigestExecutor{learning: lc, notif: notif, quietEndMinute: learning.DefaultQuietEndMinute}
}

// SetNotifier injects the notification client after construction.
func (e *LearningDigestExecutor) SetNotifier(n NotificationClient) {
	if e == nil {
		return
	}
	e.notif = n
}

// SetQuietEndMinute overrides the quiet-hours end. 0 disables the deferral.
func (e *LearningDigestExecutor) SetQuietEndMinute(minute int) {
	if e == nil {
		return
	}
	e.quietEndMinute = minute
}

func (*LearningDigestExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindLearningDigest
}

// digestPayload is the scheduled-task payload:
//
//	{"user_id":"...", "workspace_id":"...", "reschedule_reminders":true}
type digestPayload struct {
	UserID              string `json:"user_id"`
	WorkspaceID         string `json:"workspace_id"`
	RescheduleReminders *bool  `json:"reschedule_reminders,omitempty"`
}

func (e *LearningDigestExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.learning == nil {
		return nil, fmt.Errorf("learning digest client is not configured")
	}
	if t == nil {
		return nil, fmt.Errorf("learning digest task is nil")
	}
	var p digestPayload
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode learning digest payload: %w", err)
		}
	}
	userID := p.UserID
	if userID == "" {
		userID = t.UserID
	}
	if userID == "" {
		return nil, fmt.Errorf("learning digest payload requires user_id")
	}
	wsID := p.WorkspaceID
	if wsID == "" {
		wsID = t.WorkspaceID
	}
	reschedule := true
	if p.RescheduleReminders != nil {
		reschedule = *p.RescheduleReminders
	}

	summary, err := e.learning.DueSummary(ctx, wsID, userID)
	if err != nil {
		return nil, fmt.Errorf("learning due summary for %s: %w", userID, err)
	}

	// Reschedule the reminders that came due, so a daily digest keeps firing
	// tomorrow and a one-shot reminder retires itself.
	rescheduled := 0
	if reschedule {
		store := e.learning.Store()
		if store == nil {
			return nil, fmt.Errorf("learning store is not configured")
		}
		due, err := store.DueReminders(ctx, wsID, userID, nowUnix(), 50)
		if err != nil {
			return nil, fmt.Errorf("list due learning reminders: %w", err)
		}
		for _, r := range due {
			if _, err := e.learning.Advance(ctx, wsID, userID, r, e.quietEndMinute); err != nil {
				// One bad reminder must not abort the whole digest: the run
				// still reports the summary it managed to build.
				log.Printf("[learning] reschedule reminder %s failed: %v", r.ID, err)
				continue
			}
			rescheduled++
		}
	}

	notified := false
	if !summary.Empty() && e.notif != nil {
		payload, _ := json.Marshal(summary)
		if _, err := e.notif.Dispatch(ctx, notifycenter.Event{
			WorkspaceID: wsID,
			UserID:      userID,
			Source:      "learning",
			Kind:        "learning.digest.daily",
			Title:       digestTitle(summary),
			Body:        digestTitle(summary),
			Payload:     payload,
			Priority:    "normal",
		}); err != nil {
			return nil, fmt.Errorf("dispatch learning digest notification: %w", err)
		}
		notified = true
	}

	// The milestone is announced independently of the summary: reaching a
	// 7-day streak is worth saying even on a day with nothing due.
	milestone, streakDays, milestoneSent := e.announceMilestone(ctx, wsID, userID)
	if milestoneSent == 1 {
		notified = true
	}

	out, _ := json.Marshal(map[string]any{
		"userId":      userID,
		"dueCards":    summary.DueCards,
		"inbox":       summary.Inbox,
		"reviewItems": summary.ReviewItems,
		"dueTasks":    summary.DueTasks,
		"rescheduled": rescheduled,
		"notified":    notified,
		"milestone":   milestone,
		"streakDays":  streakDays,
	})
	return &scheduledtask.Result{Output: out}, nil
}

// announceMilestone celebrates a streak milestone exactly once.
//
// The claim happens *before* the dispatch, which is the safe order: a failed
// push means the celebration is skipped rather than repeated, and a user who
// misses a congratulations is a smaller problem than one who gets it every
// morning until they mute the app. The claim is a row in learning_reminders
// whose unique index makes the check atomic, so two digest runs racing each
// other cannot both announce.
//
// A streak read failure is never fatal: the digest's job is the due summary,
// and a cosmetic milestone must not take it down.
func (e *LearningDigestExecutor) announceMilestone(ctx context.Context, wsID, userID string) (milestone, streakDays, sent int) {
	if e.notif == nil {
		return 0, 0, 0
	}
	view, err := e.learning.Streak(ctx, wsID, userID, 0)
	if err != nil || view == nil || view.Milestone <= 0 {
		return 0, 0, 0
	}
	claimed, err := e.learning.ClaimMilestoneAnnouncement(ctx, wsID, userID, view.Milestone)
	if err != nil {
		log.Printf("[learning] claim milestone %d for %s failed: %v", view.Milestone, userID, err)
		return view.Milestone, view.Streak.Current, 0
	}
	if !claimed {
		return view.Milestone, view.Streak.Current, 0
	}
	payload, _ := json.Marshal(map[string]any{
		"streakDays": view.Streak.Current,
		"milestone":  view.Milestone,
	})
	title := fmt.Sprintf("%d days of study", view.Streak.Current)
	if _, err := e.notif.Dispatch(ctx, notifycenter.Event{
		WorkspaceID: wsID,
		UserID:      userID,
		Source:      "learning",
		Kind:        "learning.streak.milestone",
		Title:       title,
		Body:        title,
		Payload:     payload,
		Priority:    "normal",
	}); err != nil {
		log.Printf("[learning] dispatch streak milestone for %s failed: %v", userID, err)
		return view.Milestone, view.Streak.Current, 0
	}
	return view.Milestone, view.Streak.Current, 1
}

// digestTitle renders the notification headline. The client localises it; this
// English string is the fallback for any client without a translation entry,
// matching the flashcard executor's contract. The order matters: the most
// actionable signal (cards actually due) wins over a backlog count.
func digestTitle(s *learning.DueSummary) string {
	switch {
	case s.DueCards > 0:
		return fmt.Sprintf("%d cards due for review", s.DueCards)
	case s.Inbox > 0:
		return fmt.Sprintf("%d items waiting to be processed", s.Inbox)
	case s.ReviewItems > 0:
		return fmt.Sprintf("%d items in review", s.ReviewItems)
	case s.DueTasks > 0:
		return fmt.Sprintf("%d work items due", s.DueTasks)
	default:
		return "Nothing due today"
	}
}

// nowUnix is the executor clock. Indirection keeps the reminder reschedule
// call readable and matches the "no clock inside the pure functions" rule of
// the learning package.
func nowUnix() int64 { return time.Now().UTC().Unix() }
