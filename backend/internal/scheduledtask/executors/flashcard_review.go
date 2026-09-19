package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// NotificationClient is the subset of notifycenter.Service that the flashcard
// review executor needs. Mirrors the AgentBridgeClient / LLMBFFClient pattern
// in ai.go: keeping it as an interface lets unit tests inject a fake without
// spinning up a real PG-backed notify pipeline.
type NotificationClient interface {
	Dispatch(ctx context.Context, ev notifycenter.Event) (*notifycenter.DispatchResult, error)
}

// CountDueCardsClient is the subset of *flashcards.Store the executor needs.
// docs/flashcards-contract.md §5 names this method explicitly: the v1 daily
// review reminder is a single-store query, no JOINs, no projection.
type CountDueCardsClient interface {
	CountDueCards(ctx context.Context, userID string, nowSec int64) (int, error)
}

// FlashcardReviewExecutor is the v1 daily-review reminder. One task per
// user; on tick it asks the flashcards store for the due-card count and, if
// the count is positive, emits a single notification through notifycenter.
//
// The notification source/kind are stable identifiers that the frontend
// can map to localized text (see docs/flashcards-contract.md §5); the
// concrete English body is included as a Title fallback for any client
// that does not have a translation table.
type FlashcardReviewExecutor struct {
	store CountDueCardsClient
	notif NotificationClient
}

// NewFlashcardReviewExecutor wires the store + notification client. Match
// the ai.go pattern (non-pointer dependencies) — main.go is the sole
// caller and is responsible for supplying both. The notif argument may be
// nil for tests; production wiring in cmd/pocketd/main.go sets it via
// SetNotifier once the notifycenter service has been constructed (the
// scheduler is registered before the notify service is built).
func NewFlashcardReviewExecutor(store CountDueCardsClient, notif NotificationClient) *FlashcardReviewExecutor {
	return &FlashcardReviewExecutor{store: store, notif: notif}
}

// SetNotifier injects the notification client after construction. The
// notifycenter service in main.go is created after the scheduled task
// scheduler is registered, so this late-binding seam lets us follow the
// ai.go constructor shape while still respecting main.go's wiring order.
// Safe to call with nil to clear the binding.
func (e *FlashcardReviewExecutor) SetNotifier(n NotificationClient) {
	if e == nil {
		return
	}
	e.notif = n
}

func (*FlashcardReviewExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindFlashcardReview
}

// flashcardReviewPayload matches docs/flashcards-contract.md §5: the daily
// review executor accepts {user_id, due_window}. due_window is informational
// in v1 — only "today" is supported; other values fail loudly so a future
// "this_week" / "next_24h" can be added without silent fallback.
type flashcardReviewPayload struct {
	UserID    string `json:"user_id"`
	DueWindow string `json:"due_window,omitempty"`
}

// Execute queries the store and (when due cards exist) dispatches a single
// notification. Returns nil when there are no due cards — that's success,
// not an error (per contract: "nothing to nag about"). Infrastructure
// failures (DB error, notification send error) propagate so the scheduler
// records a failed run and retries on the next tick.
func (e *FlashcardReviewExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("flashcard review store is not configured")
	}
	if t == nil {
		return nil, fmt.Errorf("flashcard review task is nil")
	}
	var p flashcardReviewPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode flashcard review payload: %w", err)
	}
	userID := p.UserID
	if userID == "" {
		// Safety net for manually-defined tasks that omitted user_id.
		// The HTTP API normalises this; the scheduled-task UI also fills
		// it in from the task owner.
		userID = t.UserID
	}
	if userID == "" {
		return nil, fmt.Errorf("flashcard review payload requires user_id")
	}
	if p.DueWindow != "" && p.DueWindow != "today" {
		return nil, fmt.Errorf("flashcard review due_window %q is not supported in v1", p.DueWindow)
	}

	nowSec := time.Now().UTC().Unix()
	count, err := e.store.CountDueCards(ctx, userID, nowSec)
	if err != nil {
		return nil, fmt.Errorf("count due cards for %s: %w", userID, err)
	}
	if count <= 0 {
		out, _ := json.Marshal(map[string]any{
			"userId":   userID,
			"dueCount": 0,
			"notified": false,
		})
		return &scheduledtask.Result{Output: out}, nil
	}

	notified := false
	if e.notif != nil {
		title := fmt.Sprintf("You have %d cards due for review", count)
		payload, _ := json.Marshal(map[string]any{"dueCount": count})
		if _, err := e.notif.Dispatch(ctx, notifycenter.Event{
			WorkspaceID: t.WorkspaceID,
			UserID:      userID,
			Source:      "flashcards",
			Kind:        "flashcards.review.due",
			Title:       title,
			Body:        title,
			Payload:     payload,
			Priority:    "normal",
		}); err != nil {
			return nil, fmt.Errorf("dispatch due-review notification: %w", err)
		}
		notified = true
	}

	out, _ := json.Marshal(map[string]any{
		"userId":   userID,
		"dueCount": count,
		"notified": notified,
	})
	return &scheduledtask.Result{Output: out}, nil
}
