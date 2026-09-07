package server

import "testing"

func TestNativeAgentKind(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"cursor":       "cursor",
		"disk-cursor":  "cursor",
		"disk-zcode":   "zcode",
		"  disk-opencode ": "opencode",
	}
	for in, want := range cases {
		if got := nativeAgentKind(in); got != want {
			t.Fatalf("nativeAgentKind(%q)=%q want %q", in, got, want)
		}
	}
}
