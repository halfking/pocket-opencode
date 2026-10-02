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

// TestBuildClassifyWritesCarriesReason 守住 RawClassifyResult.Reason 的透传。
//
// 2026-10-02：真库 122 封邮件的 action_reason **全为空**，而同一次分类响应里
// 的 ai_summary 122 封全有值。两者同源，排除了「分类器没返回」——
// 它返回了，被 classifyRun 构造 RawClassifyResult 时扔掉了。
//
// 这条断言只钉「透传」这一层（纯函数，不依赖 PG）：BuildClassifyWrites 若哪天
// 改成重建结构体而不是逐字段复制，Reason 会被静默丢掉。
func TestBuildClassifyWritesCarriesReason(t *testing.T) {
	got := BuildClassifyWrites([]RawClassifyResult{{
		EmailID: "em-reason", Category: "work", Importance: "high",
		Summary: "合同待签", Reason: "包含截止日期且需回复确认",
	}})
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].Reason != "包含截止日期且需回复确认" {
		t.Fatalf("Reason 被 BuildClassifyWrites 丢掉了：%q", got[0].Reason)
	}
}

// TestRawClassifyResultHasReasonField 是编译期护栏：Reason 字段必须存在。
//
// 它的意义在于「删掉字段」这个退化。若哪天把 Reason 删了而忘了改测试，
// 上面那条会先转红（编译失败）；这条把「为什么必须有它」钉在字段旁边。
func TestRawClassifyResultHasReasonField(t *testing.T) {
	var r RawClassifyResult
	r.Reason = "x"
	if r.Reason != "x" {
		t.Fatal("unreachable")
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
