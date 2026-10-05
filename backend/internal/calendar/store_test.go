package calendar

import (
	"context"
	"errors"
	"testing"
)

// A nil store must fail loudly. An empty calendar and a broken one look
// identical to the user, so every method returns ErrUnavailable rather than an
// empty result — that is the difference between "no events" and "no backend".
func TestNilStoreIsUnavailable(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if s.Available() {
		t.Fatal("nil store should not be available")
	}
	if err := s.migrate(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("migrate = %v, want ErrUnavailable", err)
	}
	if err := s.Create(ctx, &Event{ID: "x"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Create = %v, want ErrUnavailable", err)
	}
	if _, err := s.Get(ctx, "x", "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
	if err := s.Update(ctx, &Event{ID: "x"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Update = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(ctx, "x", "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Delete = %v, want ErrUnavailable", err)
	}
	if _, err := s.ListRange(ctx, "w", "u", 0, 10, false); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ListRange = %v, want ErrUnavailable", err)
	}
}

func TestNilServiceIsUnavailable(t *testing.T) {
	var s *Service
	if s.Available() {
		t.Fatal("nil service should not be available")
	}
	ctx := context.Background()
	if _, err := s.CreateEvent(ctx, "w", "u", EventInput{Title: "t", StartAt: 1}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("CreateEvent = %v, want ErrUnavailable", err)
	}
	if _, err := s.GetEvent(ctx, "x", "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetEvent = %v, want ErrUnavailable", err)
	}
	if _, err := s.UpdateEvent(ctx, "x", "w", EventInput{Title: "t", StartAt: 1}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("UpdateEvent = %v, want ErrUnavailable", err)
	}
	if err := s.DeleteEvent(ctx, "x", "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("DeleteEvent = %v, want ErrUnavailable", err)
	}
	if _, err := s.Feed(ctx, "w", "u", 0, 10, false); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Feed = %v, want ErrUnavailable", err)
	}
}

func TestEventInputNormalize(t *testing.T) {
	t.Run("空白标题被拒", func(t *testing.T) {
		in := EventInput{Title: "   ", StartAt: 100}
		if err := in.normalize(); err == nil {
			t.Fatal("blank title should be rejected")
		}
	})
	t.Run("缺开始时间被拒", func(t *testing.T) {
		in := EventInput{Title: "x", StartAt: 0}
		if err := in.normalize(); err == nil {
			t.Fatal("missing startAt should be rejected")
		}
	})
	t.Run("结束早于开始被拒", func(t *testing.T) {
		in := EventInput{Title: "x", StartAt: 200, EndAt: 100}
		if err := in.normalize(); err == nil {
			t.Fatal("endAt before startAt should be rejected")
		}
	})
	t.Run("结束等于开始是合法的到期点", func(t *testing.T) {
		in := EventInput{Title: "x", StartAt: 200, EndAt: 200}
		if err := in.normalize(); err != nil {
			t.Fatalf("zero-length event should be legal: %v", err)
		}
	})
	t.Run("默认时区与可见性被填上", func(t *testing.T) {
		in := EventInput{Title: "x", StartAt: 200}
		if err := in.normalize(); err != nil {
			t.Fatal(err)
		}
		if in.Timezone != DefaultTimezone {
			t.Errorf("Timezone = %q, want %q", in.Timezone, DefaultTimezone)
		}
		if in.Visibility != VisibilityPrivate {
			t.Errorf("Visibility = %q, want %q", in.Visibility, VisibilityPrivate)
		}
	})
	t.Run("非法可见性被拒", func(t *testing.T) {
		in := EventInput{Title: "x", StartAt: 200, Visibility: "public"}
		if err := in.normalize(); err == nil {
			t.Fatal("invalid visibility should be rejected")
		}
	})
	t.Run("标题被去空白", func(t *testing.T) {
		in := EventInput{Title: "  会议  ", StartAt: 200}
		if err := in.normalize(); err != nil {
			t.Fatal(err)
		}
		if in.Title != "会议" {
			t.Errorf("Title = %q, want %q", in.Title, "会议")
		}
	})
}

func TestDurationSeconds(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		want       int64
	}{
		{"正常区间", 100, 160, 60},
		{"零长到期点", 100, 100, 0},
		{"结束早于开始不返回负数", 100, 50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Event{StartAt: tc.start, EndAt: tc.end}
			if got := e.DurationSeconds(); got != tc.want {
				t.Errorf("DurationSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsTaskDone(t *testing.T) {
	// "accepted" 必须是完成态：仓库在 task/hierarchy.go 里把它当终态，
	// 若这里当未完成，一条已验收的任务会永远带着逾期标记留在日历上。
	for _, status := range []string{"completed", "accepted"} {
		if !isTaskDone(status) {
			t.Errorf("isTaskDone(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"pending", "in_progress", "", "active"} {
		if isTaskDone(status) {
			t.Errorf("isTaskDone(%q) = true, want false", status)
		}
	}
}

func TestNormalizeWorkspace(t *testing.T) {
	if got := normalizeWorkspace(""); got != DefaultWorkspaceID {
		t.Errorf("normalizeWorkspace(\"\") = %q, want %q", got, DefaultWorkspaceID)
	}
	if got := normalizeWorkspace("ws-1"); got != "ws-1" {
		t.Errorf("normalizeWorkspace(ws-1) = %q", got)
	}
}

func TestTitleOf(t *testing.T) {
	if got := titleOf("  会议  "); got != "会议" {
		t.Errorf("titleOf trims: got %q", got)
	}
	// 空标题仍要产出可见文本，否则该条目在日历上是一枚看不见的色块。
	if got := titleOf("   "); got == "" {
		t.Error("titleOf(blank) must not return empty string")
	}
}

func TestValidVisibility(t *testing.T) {
	if !ValidVisibility(VisibilityPrivate) || !ValidVisibility(VisibilityShared) {
		t.Error("known visibilities should be valid")
	}
	for _, v := range []string{"", "public", "PRIVATE", "everyone"} {
		if ValidVisibility(v) {
			t.Errorf("ValidVisibility(%q) = true, want false", v)
		}
	}
}

func TestFeedRangeValidation(t *testing.T) {
	s := &Service{store: &Store{}, sources: StaticSources{}}
	ctx := context.Background()
	// store.Available() 为 false，Feed 先短路到 ErrUnavailable —— 因此这里只
	// 验证「不可用」不会被范围校验的其它错误掩盖。
	if _, err := s.Feed(ctx, "w", "u", 100, 50, false); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Feed on unavailable store = %v, want ErrUnavailable", err)
	}
}

func TestNewIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if id == "" {
			t.Fatal("NewID returned empty string")
		}
		if seen[id] {
			t.Fatalf("NewID collision: %s", id)
		}
		seen[id] = true
	}
}
