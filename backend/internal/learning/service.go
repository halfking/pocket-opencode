package learning

// Service is the learning-domain use cases on top of the store. Everything the
// HTTP layer and the scheduled executor need goes through here, so both see the
// same rules (especially the idempotency and the "no reminder when there is
// nothing to do" policy).

import (
	"context"
	"fmt"
	"log"
	"time"
)

// DueCardCounter counts cards that are due for review right now. It is an
// interface because the flashcards store is the production implementation
// (flashcards.Store.CountDueCards) while tests inject a fake — the same
// pattern the scheduledtask executors use for their notification client.
type DueCardCounter interface {
	CountDueCards(ctx context.Context, userID string, nowSec int64) (int, error)
}

// DueTaskCounter counts unfinished work items due today or already overdue.
// Implemented by task.Store.CountDueWorkItems.
type DueTaskCounter interface {
	CountDueWorkItems(ctx context.Context, wsID, userID string, nowSec int64) (int, error)
}

// ReviewDayCounter supplies the review half of the study streak.
//
// It is an interface rather than a direct dependency on the flashcards package
// so the learning domain stays free of it, and so tests can inject a fake.
// *flashcards.Store satisfies it structurally — the signature below is its
// method, not a wrapper.
type ReviewDayCounter interface {
	ReviewTimestampsSince(ctx context.Context, userID string, sinceUnix int64) ([]int64, error)
}

// Service wires the store to the optional domain counters.
type Service struct {
	store    *Store
	cards    DueCardCounter
	tasks    DueTaskCounter
	reviews  ReviewDayCounter
	resolver SourceResolver
	nowFunc  func() int64
}

// NewService builds the service. cards and tasks are optional: a deployment
// without flashcards (remote-only mode) still gets the learning hub, just
// without the review counters.
func NewService(store *Store, cards DueCardCounter, tasks DueTaskCounter) *Service {
	return &Service{store: store, cards: cards, tasks: tasks, nowFunc: time.Now().UTC().Unix}
}

// SetReviewDayCounter installs the flashcard review source for the streak.
// Nil leaves the streak counting captures only, which is a degraded but honest
// streak rather than a wrong one.
func (s *Service) SetReviewDayCounter(r ReviewDayCounter) {
	if s == nil {
		return
	}
	s.reviews = r
}

// SetResolver installs the source resolver that turns a one-click
// {sourceKind, sourceId} into a titled learning item. Nil disables it, in
// which case a capture request must carry its own title.
func (s *Service) SetResolver(r SourceResolver) {
	if s == nil {
		return
	}
	s.resolver = r
}

// SetNowFunc overrides the clock. Tests use it; production never calls it.
func (s *Service) SetNowFunc(f func() int64) {
	if s == nil || f == nil {
		return
	}
	s.nowFunc = f
}

// Store returns the underlying store (nil-safe). Handlers that need a single
// narrow write (a stage transition) use it instead of widening Service.
func (s *Service) Store() *Store {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *Service) now() int64 {
	if s == nil || s.nowFunc == nil {
		return time.Now().UTC().Unix()
	}
	return s.nowFunc()
}

// Capture records a material as a learning item. The second return value is
// true when the item already existed (the capture was a no-op), which the API
// surfaces as 200 instead of 201 so a client retry is not an error.
func (s *Service) Capture(ctx context.Context, wsID, userID string, req CaptureRequest, newID func() string) (*LearningItem, bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("learning service is not configured")
	}
	// Validation and title resolution run BEFORE the store check on purpose:
	// a bad request or a deleted source should be reported as such, not
	// masked by an infrastructure error ("store not configured"), and this
	// ordering is what makes the capture path testable without Postgres.
	if msg := req.Validate(); msg != "" {
		return nil, false, fmt.Errorf("%s", msg)
	}
	if newID == nil {
		return nil, false, fmt.Errorf("id generator is required")
	}
	if err := s.captureTitleFromSource(ctx, wsID, userID, &req); err != nil {
		return nil, false, err
	}
	if s.store == nil {
		return nil, false, fmt.Errorf("learning store is not configured")
	}
	item := &LearningItem{
		ID:          newID(),
		WorkspaceID: normalizeWorkspace(wsID),
		UserID:      userID,
		SourceKind:  req.SourceKind,
		SourceID:    req.SourceID,
		Title:       req.Title,
		Summary:     req.Summary,
		DeckID:      req.DeckID,
		Stage:       req.Stage,
		Importance:  req.Importance,
		Tags:        req.Tags,
	}
	existing, err := s.store.CaptureItem(ctx, item)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, true, nil
	}
	return item, false, nil
}

// List returns the caller's learning items.
func (s *Service) List(ctx context.Context, wsID, userID, stage, sourceKind string, limit int) ([]LearningItem, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("learning store is not configured")
	}
	if stage != "" && !ValidStage(stage) {
		return nil, fmt.Errorf("unknown stage %q", stage)
	}
	if sourceKind != "" && !ValidSourceKind(sourceKind) {
		return nil, fmt.Errorf("unknown sourceKind %q", sourceKind)
	}
	return s.store.ListItems(ctx, wsID, userID, stage, sourceKind, limit)
}

// DueSummary aggregates "what deserves attention now" across the learning and
// work domains. A counter that is not configured contributes 0 rather than
// failing the whole summary: a partial answer is more useful to a reminder
// than an error.
func (s *Service) DueSummary(ctx context.Context, wsID, userID string) (*DueSummary, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("learning store is not configured")
	}
	now := s.now()
	out := &DueSummary{UserID: userID}

	stages, err := s.store.CountByStage(ctx, wsID, userID)
	if err != nil {
		return nil, err
	}
	out.Inbox = stages[string(StageInbox)]
	out.ReviewItems = stages[string(StageReview)]

	if s.cards != nil {
		n, err := s.cards.CountDueCards(ctx, userID, now)
		if err != nil {
			return nil, fmt.Errorf("count due cards: %w", err)
		}
		out.DueCards = n
	}
	if s.tasks != nil {
		n, err := s.tasks.CountDueWorkItems(ctx, wsID, userID, now)
		if err != nil {
			return nil, fmt.Errorf("count due work items: %w", err)
		}
		out.DueTasks = n
	}

	reminders, err := s.store.DueReminders(ctx, wsID, userID, now, 20)
	if err != nil {
		return nil, err
	}
	for _, r := range reminders {
		if out.NextDueAt == 0 || r.NextDueAt < out.NextDueAt {
			out.NextDueAt = r.NextDueAt
		}
	}
	return out, nil
}

// UpsertReminder creates or updates one reminder.
func (s *Service) UpsertReminder(ctx context.Context, wsID, userID string, req UpsertReminderRequest, newID func() string) (*Reminder, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("learning store is not configured")
	}
	if msg := req.Validate(); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	if newID == nil {
		return nil, fmt.Errorf("id generator is required")
	}
	return s.store.UpsertReminder(ctx, &Reminder{
		ID:          newID(),
		WorkspaceID: normalizeWorkspace(wsID),
		UserID:      userID,
		Kind:        req.Kind,
		ItemID:      req.ItemID,
		CardID:      req.CardID,
		RuleKind:    req.RuleKind,
		RuleValue:   req.RuleValue,
		NextDueAt:   req.NextDueAt,
	})
}

// ListReminders returns the caller's reminders.
func (s *Service) ListReminders(ctx context.Context, wsID, userID, state string, limit int) ([]Reminder, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("learning store is not configured")
	}
	if state != "" && !ValidReminderState(state) {
		return nil, fmt.Errorf("unknown state %q", state)
	}
	return s.store.ListReminders(ctx, wsID, userID, state, limit)
}

// SnoozeReminder pushes one reminder into the future.
func (s *Service) SnoozeReminder(ctx context.Context, wsID, userID, id string, minutes int64) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("learning store is not configured")
	}
	return s.store.SnoozeReminder(ctx, wsID, userID, id, minutes)
}

// AckReminder permanently silences one reminder.
func (s *Service) AckReminder(ctx context.Context, wsID, userID, id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("learning store is not configured")
	}
	return s.store.AckReminder(ctx, wsID, userID, id)
}

// StreakView is the read model the UI renders and the digest uses.
type StreakView struct {
	Streak Streak `json:"streak"`
	// Milestone is the highest milestone the current streak has reached, 0 if
	// none. Next is the one being aimed at, 0 when the list is exhausted.
	Milestone int `json:"milestone"`
	Next      int `json:"next"`
	// Today is the day index the numbers were computed against, so a client
	// can tell whether its own clock agrees with the server's.
	Today int64 `json:"today"`
}

// streakWindowDays bounds the history the streak is computed over. Two years
// is far beyond the longest milestone (365) and keeps the query from scanning
// an unbounded table for a user who has been active since the beginning.
const streakWindowDays = 730

// Streak derives the caller's study streak.
//
// tzOffsetSec is the client's seconds-east-of-UTC offset. The learning tables
// store unix seconds with no timezone column, so the day boundary has to come
// from the caller; passing 0 yields UTC days.
//
// A read failure is reported as an error rather than as a zero streak: "you
// have no streak" and "we could not tell" must not look the same on screen.
func (s *Service) Streak(ctx context.Context, wsID, userID string, tzOffsetSec int64) (*StreakView, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("learning store is not configured")
	}
	now := s.now()
	today := DayIndex(now, tzOffsetSec)
	since := (today - streakWindowDays) * SecondsPerDay

	captures, err := s.store.ActiveDayTimestamps(ctx, wsID, userID, since)
	if err != nil {
		return nil, err
	}
	// Flashcard reviews count as study too. A user who studies exclusively
	// with cards has no learning_items rows at all, and without this their
	// streak would read 0 forever — which is the most likely way someone
	// concludes the feature is broken.
	var reviews []int64
	if s.reviews != nil {
		reviews, err = s.reviews.ReviewTimestampsSince(ctx, userID, since)
		if err != nil {
			// Captures still count; a failing review source degrades the streak
			// rather than zeroing it. The failure is logged, not swallowed.
			log.Printf("[learning] review timestamps for %s failed: %v", userID, err)
			reviews = nil
		}
	}
	st := MergeActivityDays(captures, reviews, tzOffsetSec, today)
	milestone, _ := Milestone(st.Current)
	next, _ := NextMilestone(st.Current)
	return &StreakView{Streak: st, Milestone: milestone, Next: next, Today: today}, nil
}

// ClaimMilestoneAnnouncement reports whether the caller should announce a
// milestone now. It is exactly-once per user per milestone: the claim lives in
// learning_reminders and its unique index decides, so two digest runs racing
// each other cannot both announce.
func (s *Service) ClaimMilestoneAnnouncement(ctx context.Context, wsID, userID string, milestoneDays int) (bool, error) {
	if s == nil || s.store == nil {
		return false, fmt.Errorf("learning store is not configured")
	}
	if milestoneDays <= 0 {
		return false, nil
	}
	return s.store.ClaimMilestone(ctx, wsID, userID, MilestoneKey(milestoneDays), s.now())
}

// Advance computes the next occurrence of a reminder after it has fired, and
// records the delivery. A reminder with no rule is a one-shot: it is marked
// done instead of looping forever.
//
// Quiet hours are handled here rather than in the notification layer so the
// reschedule is persisted: a reminder that fired at 23:50 must come back after
// the quiet window, not be dropped.
func (s *Service) Advance(ctx context.Context, wsID, userID string, r Reminder, quietEndMinute int) (nextDueAt int64, err error) {
	now := s.now()
	next, repeat := nextOccurrence(r, now, quietEndMinute)
	if !repeat {
		// One-shot: acknowledge so the digest stops seeing it.
		if err := s.store.AckReminder(ctx, wsID, userID, r.ID); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := s.store.MarkReminderSent(ctx, wsID, userID, r.ID, next); err != nil {
		return 0, err
	}
	return next, nil
}

// Quiet hours are expressed in minutes from local midnight.
const (
	DefaultQuietStartMinute = 22*60 + 30 // 22:30
	DefaultQuietEndMinute   = 7*60 + 30  // 07:30
)

// nextOccurrence returns the next fire time and whether the reminder repeats.
func nextOccurrence(r Reminder, now int64, quietEndMinute int) (int64, bool) {
	switch ReminderRuleKind(r.RuleKind) {
	case RuleDaily:
		at, ok := parseHHMM(r.RuleValue)
		if !ok {
			// A malformed daily rule must not spin: fall back to a day out.
			return now + 86400, true
		}
		return nextDailyOccurrence(now, at, quietEndMinute), true
	case RuleInterval:
		minutes, err := parsePositiveInt(r.RuleValue)
		if err != nil || minutes <= 0 {
			return now + 86400, true
		}
		return nextAfterQuietHours(now+int64(minutes)*60, now, quietEndMinute), true
	case RuleOnce:
		return r.NextDueAt, false
	default:
		return 0, false
	}
}

// nextDailyOccurrence returns today's occurrence if it is still in the future
// (after the quiet window), otherwise tomorrow's.
func nextDailyOccurrence(now int64, atMinute, quietEndMinute int) int64 {
	dayStart := now - now%86400
	candidate := dayStart + int64(atMinute)*60
	if candidate <= now {
		candidate += 86400
	}
	return nextAfterQuietHours(candidate, now, quietEndMinute)
}

// nextAfterQuietHours pushes a fire time out of the quiet window. Quiet hours
// are approximated with UTC minutes because the reminder row stores unix
// seconds only; the caller's local clock is applied at render time. A quiet end
// of 0 means "no quiet hours configured".
func nextAfterQuietHours(candidate, now int64, quietEndMinute int) int64 {
	if quietEndMinute <= 0 {
		return candidate
	}
	nowMinute := int((now / 60) % 1440)
	if nowMinute < quietEndMinute && candidate < now+int64(quietEndMinute-nowMinute)*60 {
		return now + int64(quietEndMinute-nowMinute)*60
	}
	return candidate
}

// parseHHMM parses "HH:MM" into minutes from midnight.
func parseHHMM(v string) (int, bool) {
	if len(v) != 5 || v[2] != ':' {
		return 0, false
	}
	h, err1 := parsePositiveInt(v[0:2])
	m, err2 := parsePositiveInt(v[3:5])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func parsePositiveInt(v string) (int, error) {
	if v == "" {
		return 0, fmt.Errorf("empty integer")
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", v)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
