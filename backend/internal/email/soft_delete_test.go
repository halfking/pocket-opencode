package email

import "testing"

func TestBuildPurgeFieldsKeepsTitleAndSummary(t *testing.T) {
	got := BuildPurgeFields(PurgeSource{
		Subject:   "九月账单",
		Snippet:   "完整正文",
		AISummary: "账单提醒",
	}, 1700)
	if got.Subject != "九月账单" || got.AISummary != "账单提醒" {
		t.Fatalf("title/summary lost: %+v", got)
	}
	if got.Snippet != "" || !got.BodyPurged || got.DeletedAt != 1700 {
		t.Fatalf("body not purged: %+v", got)
	}
}

func TestBuildPurgeFieldsPromotesSnippet(t *testing.T) {
	got := BuildPurgeFields(PurgeSource{Subject: "广告", Snippet: "限时折扣"}, 9)
	if got.AISummary != "限时折扣" || got.Snippet != "" {
		t.Fatalf("snippet should become summary: %+v", got)
	}
}

func TestShouldHidePurged(t *testing.T) {
	if !ShouldHidePurged(1700, true) {
		t.Fatal("purged row must hide from inbox")
	}
	if ShouldHidePurged(0, false) {
		t.Fatal("active row must stay visible")
	}
}
