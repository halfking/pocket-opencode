package calendar

// # The unified feed
//
// The calendar's value is answering "what do I have to do on this day" in one
// place, so it must show work items, scheduled automations and user events
// together. This file builds that merged view.
//
// Two rules shape the design:
//
//  1. **The feed is derived, not stored.** Nothing here is a second copy of a
//     task. A task's deadline changes in `tasks`, and the next feed reflects it.
//     That avoids the classic "calendar shows a stale shadow of your task list"
//     bug, at the cost of reading more than one table per request.
//
//  2. **Every entry keeps its source id.** FeedEntry.RefID points back at the
//     originating row and Source names the domain, so tapping an entry can jump
//     to the real detail page instead of editing a disconnected copy. The
//     calendar reads those domains; it does not own them.

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// FeedEntry is one row of the calendar, whatever domain it came from.
type FeedEntry struct {
	// ID is "source:refId" so two domains with the same primary key (a task
	// and an event both numbered "42") cannot collide in the client index.
	ID          string `json:"id"`
	Source      string `json:"source"`
	RefID       string `json:"refId"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	StartAt     int64  `json:"startAt"`
	EndAt       int64  `json:"endAt"`
	AllDay      bool   `json:"allDay"`
	Timezone    string `json:"timezone"`
	Done        bool   `json:"done,omitempty"`
	RemindAt    int64  `json:"remindAt,omitempty"`
}

// Feed sources.
const (
	SourceEvent     = "event"
	SourceTask      = "task"
	SourceScheduled = "scheduled"
)

// TaskDUE is a work-item deadline projected into the feed. The calendar package
// does not import the task package on purpose: a shared struct keeps the two
// stores independently testable and stops a change in the task model from
// silently altering calendar behaviour.
type TaskDUE struct {
	ID        string
	Title     string
	Status    string
	DueAt     int64
	RemindAt  int64
	AllDay    bool
	Timezone  string
	UpdatedAt int64
}

// ScheduledRun is an automation's next fire time projected into the feed.
type ScheduledRun struct {
	ID       string
	Name     string
	NextRun  int64
	Enabled  bool
	Timezone string
}

// dueSources reads the two dependent domains. It is a field on Service so tests
// can substitute fakes without a database.
type dueSources interface {
	TaskDeadlines(ctx context.Context, wsID string, from, to int64) ([]TaskDUE, error)
	ScheduledRuns(ctx context.Context, wsID string, from, to int64) ([]ScheduledRun, error)
}

// BuildFeed merges user events with work-item deadlines and automation runs.
//
// A source that fails is **skipped, not fatal**: the store that owns it may be
// unconfigured (no scheduler in this deployment), and an empty calendar would be
// a far worse outcome than a calendar missing one category. The returned
// sources slice tells the caller what actually made it in, so the client can be
// honest about a partial view.
func (s *Service) BuildFeed(ctx context.Context, wsID, userID string, from, to int64, includeShared bool) ([]FeedEntry, error) {
	events, err := s.store.ListRange(ctx, wsID, userID, from, to, includeShared)
	if err != nil {
		// The events table is the one thing the calendar cannot do without.
		return nil, err
	}

	var tasks []TaskDUE
	var runs []ScheduledRun
	var tasksErr, runsErr error
	if s.sources != nil {
		// A failing source contributes nothing but must not fail the whole feed.
		// The error is still returned: a broken query here looks **exactly** like
		// "you have no tasks" to the user, and that silence is how a bad column
		// name shipped once already (tasks has no `timezone` column). The client
		// degrades, but the operator sees why.
		tasks, tasksErr = s.sources.TaskDeadlines(ctx, wsID, from, to)
		runs, runsErr = s.sources.ScheduledRuns(ctx, wsID, from, to)
	}
	merged := mergeEntries(events, tasks, runs)
	if tasksErr != nil && runsErr != nil {
		return merged, errors.Join(tasksErr, runsErr)
	}
	if tasksErr != nil {
		return merged, tasksErr
	}
	return merged, runsErr
}

// mergeEntries is the pure core of the feed: three already-fetched slices in,
// one ordered slice out. Keeping it free of I/O is what makes the aggregation
// rules (dedup, done-flags, ordering) testable without a database — the store
// path only has to prove it *calls* the right things with the right range.
func mergeEntries(events []Event, tasks []TaskDUE, runs []ScheduledRun) []FeedEntry {
	entries := make([]FeedEntry, 0, len(events)+len(tasks)+len(runs))

	for _, e := range events {
		entries = append(entries, FeedEntry{
			// 来源前缀与任务/定时任务保持一致：事件 id 与任务 id 可能撞车
			// （例如两边都叫 "42"），裸 id 会在客户端索引里互相覆盖。
			ID:          SourceEvent + ":" + e.ID,
			Source:      SourceEvent,
			RefID:       e.ID,
			Title:       titleOf(e.Title),
			Description: e.Description,
			Location:    e.Location,
			StartAt:     e.StartAt,
			EndAt:       e.EndAt,
			AllDay:      e.AllDay,
			Timezone:    e.Timezone,
			RemindAt:    e.RemindAt,
		})
	}

	for _, t := range tasks {
		// A row with no due date cannot be placed on a calendar, so it is
		// skipped; a row with no title is still shown (as a placeholder)
		// because silently dropping it would make the day's count disagree
		// with the task list, and the user could never tell it existed.
		if t.DueAt <= 0 || strings.TrimSpace(t.ID) == "" {
			continue
		}
		entries = append(entries, FeedEntry{
			ID:      SourceTask + ":" + t.ID,
			Source:  SourceTask,
			RefID:   t.ID,
			Title:   titleOf(t.Title),
			StartAt: t.DueAt,
			// A deadline is an instant, not a span: zero length is
			// deliberate and the client's overlap logic handles it.
			EndAt:    t.DueAt,
			AllDay:   t.AllDay,
			Timezone: t.Timezone,
			Done:     isTaskDone(t.Status),
			RemindAt: t.RemindAt,
		})
	}

	for _, r := range runs {
		if r.NextRun <= 0 || strings.TrimSpace(r.ID) == "" {
			continue
		}
		entries = append(entries, FeedEntry{
			ID:       SourceScheduled + ":" + r.ID,
			Source:   SourceScheduled,
			RefID:    r.ID,
			Title:    titleOf(r.Name),
			StartAt:  r.NextRun,
			EndAt:    r.NextRun,
			Timezone: r.Timezone,
		})
	}

	sortFeed(entries)
	return entries
}

// isTaskDone reports whether a task status counts as finished. "accepted" is
// included deliberately: the repo treats it as a terminal state
// (see task/hierarchy.go), and treating it as pending would keep a done task
// pinned on the calendar as overdue forever.
func isTaskDone(status string) bool {
	return status == "completed" || status == "accepted"
}

// sortFeed orders entries the way a person reads a day: by start time, with
// all-day items first, then by title so equal timestamps stay stable across
// reloads (an unstable order makes the grid appear to shuffle on refresh).
func sortFeed(entries []FeedEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.AllDay != b.AllDay {
			return a.AllDay
		}
		if a.StartAt != b.StartAt {
			return a.StartAt < b.StartAt
		}
		return a.Title < b.Title
	})
}
