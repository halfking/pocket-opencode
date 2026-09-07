package llmbff

import "testing"

func TestWorkTypeFromKind(t *testing.T) {
	cases := map[string]string{
		"live_translate":  "doc_translate",
		"doc_translate":   "doc_translate",
		"meeting_summary": "meeting_summary",
		"meeting_refine":  "doc_translate",
		"chat":            "",
		"":                "",
	}
	for in, want := range cases {
		if got := WorkTypeFromKind(in); got != want {
			t.Fatalf("WorkTypeFromKind(%q)=%q want %q", in, got, want)
		}
	}
}
