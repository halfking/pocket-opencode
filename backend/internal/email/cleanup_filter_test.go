package email

import "testing"

func TestCleanupFilterRequiresConstraint(t *testing.T) {
	empty := CleanupFilter{}
	if empty.HasConstraint() {
		t.Fatal("empty filter must not count as a constraint")
	}
	if err := empty.Validate(); err == nil {
		t.Fatal("empty filter must fail validation")
	}
	if !(CleanupFilter{Subject: "发票"}).HasConstraint() {
		t.Fatal("subject is a constraint")
	}
	if !(CleanupFilter{From: "promo@"}).HasConstraint() {
		t.Fatal("from is a constraint")
	}
	if !(CleanupFilter{Since: 100}).HasConstraint() {
		t.Fatal("since is a constraint")
	}
	if !(CleanupFilter{Until: 200}).HasConstraint() {
		t.Fatal("until is a constraint")
	}
	if (CleanupFilter{AccountID: "acct-1"}).HasConstraint() {
		t.Fatal("account alone is not enough to batch-delete")
	}
}

func TestMatchCleanupSubjectFromDate(t *testing.T) {
	e := Email{
		FromAddress: "promo@shop.com",
		FromName:    "Shop Promo",
		Subject:     "限时抢购 全场秒杀",
		Date:        1_725_000_000,
	}
	if !MatchCleanup(e, CleanupFilter{Subject: "抢购"}) {
		t.Fatal("subject contains should match")
	}
	if MatchCleanup(e, CleanupFilter{Subject: "会议纪要"}) {
		t.Fatal("unrelated subject must not match")
	}
	if !MatchCleanup(e, CleanupFilter{From: "shop.com"}) {
		t.Fatal("from address contains should match")
	}
	if !MatchCleanup(e, CleanupFilter{From: "promo"}) {
		t.Fatal("from name contains should match")
	}
	if !MatchCleanup(e, CleanupFilter{Since: 1_724_000_000, Until: 1_726_000_000}) {
		t.Fatal("date in range should match")
	}
	if MatchCleanup(e, CleanupFilter{Since: 1_726_000_001}) {
		t.Fatal("date before since must not match")
	}
	if MatchCleanup(e, CleanupFilter{Until: 1_724_000_000}) {
		t.Fatal("date after until must not match")
	}
}

func TestSelectDeletableOnlyMovedUIDs(t *testing.T) {
	items := []CleanupItem{
		{ID: "a", AccountID: "acct-1", UID: 11},
		{ID: "b", AccountID: "acct-1", UID: 12},
		{ID: "c", AccountID: "acct-2", UID: 21},
		{ID: "d", AccountID: "acct-1", UID: 0},
	}
	moved := map[string][]int64{"acct-1": {12}}
	got := SelectDeletable(items, moved)
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("deletable=%v, want [b]", got)
	}
}
