package email

import "testing"

func TestBuildClassifyWritesNormalizesAndDropsEmpty(t *testing.T) {
	got := BuildClassifyWrites([]RawClassifyResult{
		{EmailID: "a", Category: "ad", Importance: "high", Summary: "促销"},
		{EmailID: "b", Category: "", Summary: "无类"},
		{EmailID: "c", Category: "work", Importance: "low", Summary: "周报"},
	})
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Category != "marketing" || got[0].EmailID != "a" {
		t.Fatalf("ads alias: %+v", got[0])
	}
	if got[1].Category != "work" {
		t.Fatalf("work: %+v", got[1])
	}
}

func TestShouldProcessAfterFetch(t *testing.T) {
	if !ShouldProcessAfterFetch(1, 0) {
		t.Fatal("synced account must process even with 0 new")
	}
	if ShouldProcessAfterFetch(0, 3) {
		t.Fatal("no synced account must not process")
	}
}
