package server

import (
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

func TestShouldAggregateSessions(t *testing.T) {
	cases := []struct {
		name string
		inst model.PocketInstance
		want bool
	}{
		{
			name: "disk origin always",
			inst: model.PocketInstance{ID: "disk-cursor", Origin: "disk", Health: "unknown"},
			want: true,
		},
		{
			name: "disk locator always",
			inst: model.PocketInstance{ID: "disk-zcode", APIBaseURL: "disk://zcode", Health: "offline"},
			want: true,
		},
		{
			name: "discovered unknown skipped",
			inst: model.PocketInstance{ID: "scan-1", Origin: "discovered", Health: "unknown", APIBaseURL: "http://172.18.0.1:8080"},
			want: false,
		},
		{
			name: "static offline skipped",
			inst: model.PocketInstance{ID: "local-opencode", Origin: "static", Health: "offline", APIBaseURL: "http://127.0.0.1:4096"},
			want: false,
		},
		{
			name: "healthy http kept",
			inst: model.PocketInstance{ID: "edge-1", Origin: "registered", Health: "healthy", APIBaseURL: "http://10.0.0.8:4096"},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAggregateSessions(tc.inst); got != tc.want {
				t.Fatalf("shouldAggregateSessions(%s) = %v, want %v", tc.inst.ID, got, tc.want)
			}
		})
	}
}

func TestListSessionsTimeout(t *testing.T) {
	if listSessionsTimeout("disk://cursor") != 15*time.Second {
		t.Fatalf("disk timeout")
	}
	if listSessionsTimeout("http://127.0.0.1:4096") != 3*time.Second {
		t.Fatalf("http timeout")
	}
}

func TestSortSessionsByUpdated(t *testing.T) {
	in := []adapter.OpenCodeSession{
		{ID: "old", TimeUpdated: 1},
		{ID: "new", TimeUpdated: 9},
		{ID: "mid", TimeUpdated: 5},
	}
	sortSessionsByUpdated(in)
	if in[0].ID != "new" || in[2].ID != "old" {
		t.Fatalf("order = %v", []string{in[0].ID, in[1].ID, in[2].ID})
	}
}

func TestPageSessions(t *testing.T) {
	items := []adapter.OpenCodeSession{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	got := pageSessions(items, 1, 2)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("got %+v", got)
	}
	if len(pageSessions(items, 10, 2)) != 0 {
		t.Fatalf("offset past end should be empty")
	}
}
