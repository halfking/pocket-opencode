package scheduledtask

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule encapsulates the parsed form of a Task's schedule expression.
// It computes the next due time given a base. ComputeNext is the single
// source of truth — both the scheduler tick and the HTTP preview endpoint
// call it, so the preview matches what the scheduler actually does.
type Schedule struct {
	Kind    ScheduleKind
	Expr    string
	TZ      *time.Location // nil → UTC
	parser  cron.Parser    // 5-field (m h dom mon dow) by default
	as5     bool           // true → use 5-field parser (cron without seconds)
}

// NewSchedule parses a (kind, expr, tz) triple and returns a usable Schedule.
// tz defaults to UTC when empty. Unsupported kinds return ErrInvalidSchedule.
func NewSchedule(kind ScheduleKind, expr, tz string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("%w: empty expression", ErrInvalidSchedule)
	}
	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("%w: bad timezone %q: %v", ErrInvalidSchedule, tz, err)
		}
		loc = l
	}
	sc := &Schedule{Kind: kind, Expr: expr, TZ: loc}
	// Standard 5-field cron (m h dom mon dow). robfig/cron/v3 defaults to
	// 6-field (with seconds); we use the explicit 5-field descriptor.
	sc.parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sc.as5 = true
	switch kind {
	case ScheduleCron:
		if _, err := sc.parser.Parse(expr); err != nil {
			return nil, fmt.Errorf("%w: cron parse %q: %v", ErrInvalidSchedule, expr, err)
		}
	case ScheduleInterval:
		d, err := time.ParseDuration(expr)
		if err != nil || d <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
			return nil, fmt.Errorf("%w: interval parse %q: %v", ErrInvalidSchedule, expr, err)
		}
	case ScheduleAt:
		if _, err := time.Parse(time.RFC3339, expr); err != nil {
			return nil, fmt.Errorf("%w: at parse %q: %v", ErrInvalidSchedule, expr, err)
		}
	default:
		return nil, fmt.Errorf("%w: unknown schedule kind %q", ErrInvalidSchedule, kind)
	}
	return sc, nil
}

// ComputeNext returns the unix-second timestamp of the next due time strictly
// after `baseSec`, in the schedule's timezone. Returns 0 when the schedule
// has no future occurrence (e.g. a one-shot `at` whose time is already past).
func (s *Schedule) ComputeNext(baseSec int64) int64 {
	if s == nil {
		return 0
	}
	base := time.Unix(baseSec, 0).In(s.TZ)
	switch s.Kind {
	case ScheduleCron:
		sched, err := s.parser.Parse(s.Expr)
		if err != nil {
			return 0
		}
		next := sched.Next(base)
		return next.Unix()
	case ScheduleInterval:
		d, err := time.ParseDuration(s.Expr)
		if err != nil || d <= 0 {
			return 0
		}
		return base.Add(d).Unix()
	case ScheduleAt:
		t, err := time.Parse(time.RFC3339, s.Expr)
		if err != nil {
			return 0
		}
		t = t.In(s.TZ)
		if t.Unix() <= baseSec {
			return 0 // one-shot already elapsed → disable
		}
		return t.Unix()
	}
	return 0
}

// Preview returns the next up-to-5 due times after baseSec.
func (s *Schedule) Preview(baseSec int64) []int64 {
	if s == nil {
		return nil
	}
	out := make([]int64, 0, 5)
	base := baseSec
	for i := 0; i < 5; i++ {
		next := s.ComputeNext(base)
		if next == 0 {
			break
		}
		out = append(out, next)
		base = next
	}
	return out
}

// DefaultCooldownSec returns a sensible per-kind cooldown (used by the
// scheduler to back off after a failed run before re-trying).
func DefaultCooldownSec(kind ScheduleKind, expr string) int64 {
	switch kind {
	case ScheduleCron:
		// For cron we don't enforce a cooldown (the next cron tick already
		// spaces runs); the field exists for callers that explicitly opt in.
		return 0
	case ScheduleInterval:
		if d, err := time.ParseDuration(expr); err == nil {
			// 10% of the interval, with a 5-second floor.
			ms := d / 10
			if ms < 5*time.Second {
				ms = 5 * time.Second
			}
			return int64(ms / time.Second)
		}
	case ScheduleAt:
		// One-shot: no cooldown.
		return 0
	}
	return 0
}
