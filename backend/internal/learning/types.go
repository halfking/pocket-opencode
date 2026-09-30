// Package learning implements the Learning Core: the module that turns
// everything the user reads (notes, email, RSS, meeting notes) into something
// that comes back on a schedule, and the server-side spaced-repetition
// scheduler that decides when.
//
// Why this package exists (docs/学习muse/03-架构方案.md §2):
//
//   - The flashcard domain stores cards and a review log, but its scheduling
//     truth lives in the browser (`flashcards/cards.go` explicitly does NOT
//     recompute due/state; docs/flashcards-contract.md §4 freezes FSRS on the
//     client). That means the *due-card count* a reminder depends on is only
//     as good as whichever device last reviewed — and if no device has been
//     open, the reminder is wrong or missing.
//   - Notes / email / RSS have no path into the memory loop at all: grep for
//     fromNote|toCard|generateCard in the server returns nothing.
//
// So: scheduling moves server-side (pure functions, unit-tested, no IO), and
// every source funnels through one row shape (learning_items) that remembers
// *where* the material came from instead of copying its body (ADR-003).
package learning

import (
	"encoding/json"
	"time"
)

// DefaultWorkspaceID matches the task domain's default tenant so a single
// tenant deployment keeps working with no explicit workspace.
const DefaultWorkspaceID = "default"

// Card states, aligned with flashcards.Card.State so both schedulers agree on
// what 0..3 mean (docs/flashcards-contract.md §1.2).
const (
	StateNew        = 0
	StateLearning   = 1
	StateReview     = 2
	StateRelearning = 3
)

// Review ratings, aligned with flashcard_revlog.rating.
const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
	RatingEasy  = 4
)

// SourceKind is where a learning item was captured from. The learning row keeps
// a reference, not a copy of the content (ADR-003).
type SourceKind string

const (
	SourceNote    SourceKind = "note"
	SourceEmail   SourceKind = "email"
	SourceRSS     SourceKind = "rss"
	SourceMeeting SourceKind = "meeting"
	SourceChat    SourceKind = "chat"
	SourceManual  SourceKind = "manual"
)

var sourceKinds = map[SourceKind]bool{
	SourceNote: true, SourceEmail: true, SourceRSS: true,
	SourceMeeting: true, SourceChat: true, SourceManual: true,
}

// ValidSourceKind reports whether k is an accepted source kind.
func ValidSourceKind(k string) bool { return sourceKinds[SourceKind(k)] }

// Stage is where a learning item sits in the learning funnel.
type Stage string

const (
	StageInbox    Stage = "inbox"    // collected, not processed yet
	StageLearning Stage = "learning" // being studied
	StageReview   Stage = "review"   // in the spaced-repetition loop
	StageMastered Stage = "mastered"
	StageArchived Stage = "archived"
)

var stages = map[Stage]bool{
	StageInbox: true, StageLearning: true, StageReview: true,
	StageMastered: true, StageArchived: true,
}

// ValidStage reports whether s is an accepted stage.
func ValidStage(s string) bool { return stages[Stage(s)] }

// ReminderKind is the trigger class of a learning reminder.
type ReminderKind string

const (
	// ReminderDailyDigest is the once-a-day review: the "come back and study"
	// signal that keeps a material from decaying unread.
	ReminderDailyDigest ReminderKind = "daily_digest"
	// ReminderSpacedReview fires when a card reaches its FSRS due time.
	ReminderSpacedReview ReminderKind = "spaced_review"
	// ReminderDeadline is a hard deadline on a study plan item.
	ReminderDeadline ReminderKind = "deadline"
	// ReminderStreak protects a daily-study streak.
	ReminderStreak ReminderKind = "streak"
)

var reminderKinds = map[ReminderKind]bool{
	ReminderDailyDigest: true, ReminderSpacedReview: true,
	ReminderDeadline: true, ReminderStreak: true,
}

// ValidReminderKind reports whether k is an accepted reminder kind.
func ValidReminderKind(k string) bool { return reminderKinds[ReminderKind(k)] }

// ReminderRuleKind is how the recurrence of a reminder is expressed.
type ReminderRuleKind string

const (
	RuleDaily    ReminderRuleKind = "daily"    // rule_value = "HH:MM" in local time
	RuleInterval ReminderRuleKind = "interval" // rule_value = minutes
	RuleOnce     ReminderRuleKind = "once"     // rule_value = unix seconds
)

var ruleKinds = map[ReminderRuleKind]bool{RuleDaily: true, RuleInterval: true, RuleOnce: true}

// ValidRuleKind reports whether k is an accepted rule kind.
func ValidRuleKind(k string) bool { return ruleKinds[ReminderRuleKind(k)] }

// ReminderState is the lifecycle of one reminder row.
type ReminderState string

const (
	ReminderPending ReminderState = "pending"
	ReminderSent    ReminderState = "sent"
	ReminderAcked   ReminderState = "acked"
	ReminderSnoozed ReminderState = "snoozed"
	ReminderDone    ReminderState = "done"
)

var reminderStates = map[ReminderState]bool{
	ReminderPending: true, ReminderSent: true, ReminderAcked: true,
	ReminderSnoozed: true, ReminderDone: true,
}

// ValidReminderState reports whether s is an accepted reminder state.
func ValidReminderState(s string) bool { return reminderStates[ReminderState(s)] }

// LearningItem is one captured material. It stores a *reference* to the source
// domain row (source_kind + source_id) plus the small amount of data the
// learning loop needs: a title, an optional summary, a deck, and a stage.
type LearningItem struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspaceId"`
	UserID      string   `json:"userId"`
	SourceKind  string   `json:"sourceKind"`
	SourceID    string   `json:"sourceId"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	DeckID      string   `json:"deckId,omitempty"`
	Stage       string   `json:"stage"`
	Importance  int      `json:"importance"`
	Tags        []string `json:"tags"`
	CapturedAt  int64    `json:"capturedAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	DeletedAt   int64    `json:"deletedAt,omitempty"`
}

// CaptureRequest is the POST /api/learning/items body. SourceKind/SourceID
// form the logical idempotency key: capturing the same email twice must not
// create two items, so the store upserts on
// (workspace_id, user_id, source_kind, source_id).
//
// Title and Summary are OPTIONAL: the one-click entry points send only the
// source reference and let the server resolve them (see resolver.go). A client
// that does pass a title keeps it.
type CaptureRequest struct {
	SourceKind string   `json:"sourceKind"`
	SourceID   string   `json:"sourceId"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary,omitempty"`
	DeckID     string   `json:"deckId,omitempty"`
	Importance int      `json:"importance,omitempty"`
	Stage      string   `json:"stage,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// Validate returns a client-facing error message, or "" when the request is
// acceptable. Kept as a pure function so the HTTP layer and the service share
// exactly one rule set.
//
// Note that Title is NOT required here: it may be resolved from the source row
// by Service.captureTitleFromSource. Only a request with neither a title nor a
// resolvable source is rejected (see the service).
func (c CaptureRequest) Validate() string {
	if !ValidSourceKind(c.SourceKind) {
		return "sourceKind must be one of note|email|rss|meeting|chat|manual"
	}
	if c.SourceID == "" && c.SourceKind != string(SourceManual) {
		return "sourceId is required for this sourceKind"
	}
	if c.Stage != "" && !ValidStage(c.Stage) {
		return "stage must be one of inbox|learning|review|mastered|archived"
	}
	if c.Importance < 0 || c.Importance > 5 {
		return "importance must be between 0 and 5"
	}
	return ""
}

// Reminder is one scheduled nudge in the learning loop.
type Reminder struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	UserID       string `json:"userId"`
	Kind         string `json:"kind"`
	ItemID       string `json:"itemId,omitempty"`
	CardID       string `json:"cardId,omitempty"`
	RuleKind     string `json:"ruleKind"`
	RuleValue    string `json:"ruleValue,omitempty"`
	NextDueAt    int64  `json:"nextDueAt"`
	State        string `json:"state"`
	LastSentAt   int64  `json:"lastSentAt,omitempty"`
	SnoozedUntil int64  `json:"snoozedUntil,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// UpsertReminderRequest is the POST /api/learning/reminders body.
type UpsertReminderRequest struct {
	Kind      string `json:"kind"`
	ItemID    string `json:"itemId,omitempty"`
	CardID    string `json:"cardId,omitempty"`
	RuleKind  string `json:"ruleKind"`
	RuleValue string `json:"ruleValue,omitempty"`
	NextDueAt int64  `json:"nextDueAt"`
}

// Validate returns a client-facing error message, or "" when acceptable.
func (u UpsertReminderRequest) Validate() string {
	if !ValidReminderKind(u.Kind) {
		return "kind must be one of daily_digest|spaced_review|deadline|streak"
	}
	if !ValidRuleKind(u.RuleKind) {
		return "ruleKind must be one of daily|interval|once"
	}
	if u.NextDueAt <= 0 {
		return "nextDueAt is required (unix seconds)"
	}
	if u.RuleKind == string(RuleDaily) {
		// parseHHMM enforces the range too, so "29:30" is rejected here instead
		// of silently scheduling to an impossible time-of-day.
		if _, ok := parseHHMM(u.RuleValue); !ok {
			return "ruleValue for a daily rule must be HH:MM"
		}
	}
	return ""
}

// DueSummary is the "what should I do today" aggregate. It is the payload of
// GET /api/learning/items/due and the body of the daily digest notification —
// one shape, so the notification text and the screen can never disagree.
type DueSummary struct {
	UserID      string `json:"userId,omitempty"`
	DueCards    int    `json:"dueCards"`
	Inbox       int    `json:"inbox"`
	ReviewItems int    `json:"reviewItems"`
	DueTasks    int    `json:"dueTasks"`
	NextDueAt   int64  `json:"nextDueAt,omitempty"`
}

// Empty reports whether there is genuinely nothing to do. The digest executor
// treats Empty as "do not notify" — nagging with an all-zero summary is how
// users learn to ignore reminders.
func (d DueSummary) Empty() bool {
	return d.DueCards == 0 && d.Inbox == 0 && d.ReviewItems == 0 && d.DueTasks == 0
}

// normalizeWorkspace applies the default tenant, mirroring task.normalizeWorkspace
// so both domains behave the same for an unset workspace.
func normalizeWorkspace(wsID string) string {
	if wsID == "" {
		return DefaultWorkspaceID
	}
	return wsID
}

func nowUnix() int64 { return time.Now().UTC().Unix() }

// jsonPayload is a tiny helper for event/reminder payloads.
func jsonPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
