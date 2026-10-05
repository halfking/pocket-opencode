package calendar

// Bridges from the calendar to the other time-bearing domains.
//
// Each bridge issues one narrow query against the owning store's table rather
// than loading whole task lists and filtering in Go: the feed is requested on
// every calendar page turn, and "list every task then discard most of them"
// turns a fast screen into a slow one.

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGTaskDeadlines reads work-item deadlines inside [from, to) directly from the
// tasks table.
type PGTaskDeadlines struct{ Pool *pgxpool.Pool }

func (q PGTaskDeadlines) TaskDeadlines(ctx context.Context, wsID string, from, to int64) ([]TaskDUE, error) {
	if q.Pool == nil {
		return nil, ErrUnavailable
	}
	// 注意：**不要**在这里 SELECT timezone。tasks 表没有这一列
	// （见 task/store.go 的 ALTER 列表：type/owner_id/due_at/remind_at…，
	// 没有 timezone）。写上 `COALESCE(timezone,'')` 会让整条 SQL 在运行时
	// 报「column does not exist」，任务截止就会**整类从日历里消失**。
	// 而这条错误曾被 BuildFeed 的「源失败就跳过」吞掉，日历只是少了任务，
	// 不报错 —— 这类静默降级最难查。
	//
	// 任务的截止时间是一个绝对时刻（due_at, unix 秒），本身不含时区信息，
	// 展示时按观察者本地时区渲染即可，因此这里填默认时区。
	rows, err := q.Pool.Query(ctx,
		`SELECT id, title, status, due_at, remind_at
		   FROM tasks
		  WHERE workspace_id = $1
		    AND due_at > 0
		    AND due_at >= $2
		    AND due_at < $3
		  ORDER BY due_at ASC
		  LIMIT 500`,
		normalizeWorkspace(wsID), from, to)
	if err != nil {
		return nil, fmt.Errorf("calendar task deadlines: %w", err)
	}
	defer rows.Close()
	out := []TaskDUE{}
	for rows.Next() {
		var t TaskDUE
		if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.DueAt, &t.RemindAt); err != nil {
			return nil, fmt.Errorf("calendar task deadlines scan: %w", err)
		}
		t.Timezone = DefaultTimezone
		out = append(out, t)
	}
	return out, rows.Err()
}

// PGScheduledRuns reads the next fire time of enabled automations.
type PGScheduledRuns struct{ Pool *pgxpool.Pool }

func (q PGScheduledRuns) ScheduledRuns(ctx context.Context, wsID string, from, to int64) ([]ScheduledRun, error) {
	if q.Pool == nil {
		return nil, ErrUnavailable
	}
	rows, err := q.Pool.Query(ctx,
		`SELECT id, name, next_run_at, timezone
		   FROM scheduled_tasks
		  WHERE workspace_id = $1
		    AND enabled = TRUE
		    AND next_run_at > 0
		    AND next_run_at >= $2
		    AND next_run_at < $3
		  ORDER BY next_run_at ASC
		  LIMIT 500`,
		normalizeWorkspace(wsID), from, to)
	if err != nil {
		return nil, fmt.Errorf("calendar scheduled runs: %w", err)
	}
	defer rows.Close()
	out := []ScheduledRun{}
	for rows.Next() {
		var r ScheduledRun
		if err := rows.Scan(&r.ID, &r.Name, &r.NextRun, &r.Timezone); err != nil {
			return nil, fmt.Errorf("calendar scheduled runs scan: %w", err)
		}
		r.Enabled = true
		if r.Timezone == "" {
			r.Timezone = DefaultTimezone
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PGBridges bundles the two PG-backed readers into one dueSources value.
//
// It exists so the call site cannot accidentally wire only one of them. The
// failure this guards against is quiet: a calendar fed by events alone still
// renders, still passes every smoke test, and simply never shows the task
// deadlines and automation runs that are the point of the feature.
type PGBridges struct{ Pool *pgxpool.Pool }

func (b PGBridges) TaskDeadlines(ctx context.Context, wsID string, from, to int64) ([]TaskDUE, error) {
	return PGTaskDeadlines{Pool: b.Pool}.TaskDeadlines(ctx, wsID, from, to)
}

func (b PGBridges) ScheduledRuns(ctx context.Context, wsID string, from, to int64) ([]ScheduledRun, error) {
	return PGScheduledRuns{Pool: b.Pool}.ScheduledRuns(ctx, wsID, from, to)
}

// StaticSources is a fixed list of entries, used by tests and by deployments
// that want the calendar to show only locally-created events.
type StaticSources struct {
	Tasks     []TaskDUE
	Scheduled []ScheduledRun
}

func (s StaticSources) TaskDeadlines(context.Context, string, int64, int64) ([]TaskDUE, error) {
	return s.Tasks, nil
}

func (s StaticSources) ScheduledRuns(context.Context, string, int64, int64) ([]ScheduledRun, error) {
	return s.Scheduled, nil
}

// titleOf trims a source row's title, returning a placeholder when it is blank.
// A calendar entry with an empty title renders as an invisible chip, so it is
// better to show *something* the user can recognise and go fix.
func titleOf(title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return "(untitled)"
	}
	return t
}
