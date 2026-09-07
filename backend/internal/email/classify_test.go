package email

import "testing"

func TestNormalizeCategory(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"work":    "work",
		"ad":      "marketing",
		"ADS":     "marketing",
		"mystery": "personal",
		"spam":    "spam",
	}
	for in, want := range cases {
		if got := NormalizeCategory(in); got != want {
			t.Fatalf("NormalizeCategory(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNeedsClassification(t *testing.T) {
	if !NeedsClassification("") {
		t.Fatal("empty category needs classification")
	}
	if NeedsClassification("work") {
		t.Fatal("categorized mail must be skipped")
	}
}

func TestNextClassifyLimit(t *testing.T) {
	ids := []string{"a", "b", "c"}
	got := CapClassifyIDs(ids, 2)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("CapClassifyIDs=%v", got)
	}
	if CapClassifyIDs(nil, 20) != nil && len(CapClassifyIDs(nil, 20)) != 0 {
		t.Fatal("empty input must stay empty")
	}
}
