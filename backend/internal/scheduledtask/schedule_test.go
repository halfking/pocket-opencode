package scheduledtask

import (
	"errors"
	"testing"
	"time"
)

func TestNewScheduleAndComputeNext(t *testing.T) {
	base := time.Date(2026, 8, 30, 8, 10, 0, 0, time.UTC).Unix()
	cases := []struct {
		name string
		kind ScheduleKind
		expr string
		want time.Time
	}{
		{"cron", ScheduleCron, "*/5 * * * *", time.Date(2026, 8, 30, 8, 15, 0, 0, time.UTC)},
		{"interval", ScheduleInterval, "30m", time.Date(2026, 8, 30, 8, 40, 0, 0, time.UTC)},
		{"at", ScheduleAt, "2026-08-30T09:00:00Z", time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSchedule(tc.kind, tc.expr, "UTC")
			if err != nil {
				t.Fatalf("NewSchedule: %v", err)
			}
			if got := s.ComputeNext(base); got != tc.want.Unix() {
				t.Fatalf("ComputeNext() = %s, want %s", time.Unix(got, 0), tc.want)
			}
		})
	}
}

func TestSchedulePreview(t *testing.T) {
	base := time.Date(2026, 8, 30, 8, 10, 0, 0, time.UTC).Unix()
	s, err := NewSchedule(ScheduleCron, "0 * * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	got := s.Preview(base)
	if len(got) != 5 {
		t.Fatalf("preview length = %d, want 5", len(got))
	}
	for i, ts := range got {
		want := time.Date(2026, 8, 30, 9+i, 0, 0, 0, time.UTC).Unix()
		if ts != want {
			t.Errorf("preview[%d] = %s, want %s", i, time.Unix(ts, 0), time.Unix(want, 0))
		}
	}
}

func TestScheduleValidation(t *testing.T) {
	cases := []struct {
		kind ScheduleKind
		expr string
	}{
		{ScheduleCron, "not cron"},
		{ScheduleInterval, "not duration"},
		{ScheduleInterval, "0s"},
		{ScheduleAt, "not timestamp"},
		{ScheduleKind("unknown"), "x"},
	}
	for _, tc := range cases {
		_, err := NewSchedule(tc.kind, tc.expr, "UTC")
		if !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("NewSchedule(%q, %q) error = %v, want ErrInvalidSchedule", tc.kind, tc.expr, err)
		}
	}
}

func TestAtSchedulePastReturnsZero(t *testing.T) {
	s, err := NewSchedule(ScheduleAt, "2026-08-30T08:00:00Z", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 30, 8, 10, 0, 0, time.UTC).Unix()
	if got := s.ComputeNext(base); got != 0 {
		t.Fatalf("past at schedule = %d, want 0", got)
	}
	if got := s.Preview(base); len(got) != 0 {
		t.Fatalf("past at preview = %v, want empty", got)
	}
}

func TestScheduleTimezone(t *testing.T) {
	base := time.Date(2026, 8, 30, 0, 10, 0, 0, time.UTC).Unix()
	s, err := NewSchedule(ScheduleCron, "0 9 * * *", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC).Unix()
	if got := s.ComputeNext(base); got != want {
		t.Fatalf("timezone next = %s, want %s", time.Unix(got, 0), time.Unix(want, 0))
	}
}

func TestDefaults(t *testing.T) {
	in := TaskInput{}
	in.Defaults()
	if in.Timezone != "Asia/Shanghai" || in.TimeoutSec != 120 || in.Enabled == nil || !*in.Enabled {
		t.Fatalf("unexpected defaults: %+v", in)
	}
}
