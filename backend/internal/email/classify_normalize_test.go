package email

// classify_normalize_test.go — AI 分类结果的取值归一化。
//
// 2026-10-01 实测发现的缺陷：`BuildClassifyWrites`（classify_run.go:18）
// 对 **Category** 做了归一化（NormalizeCategory），但 **Importance 完全没校验**
// —— kxmemory 返回什么就原样落库。
//
// 后果链（需求 4「对其它重要邮件进行提醒」）：
//   kxmemory 返回 "High" / "HIGH" / "高"（大小写/写法差异很常见）
//     -> emails.importance 落成 "High"
//     -> splitReminderCandidates 的 `case "high"` 匹配不上（pipeline.go:727）
//     -> 重要邮件**静默漏提醒**，报告里 remindersSent=0 也不报错
//
// 且 DB 层**没有 CHECK 约束**兜底（实测 pg_constraint 对 emails 返回 0 行），
// 所以脏值会一直留着。
//
// 真实数据现状：importance 分布 = (empty) 275 / medium 111 / high 56 / low 5，
// 全是小写规范值——所以这个缺陷**从未在真实数据上暴露**。
//
// 负控对照：把 NormalizeImportance 改成恒返回空
//          -> TestNormalizeImportance_CanonicalizesUpstreamValues 转红。

import "testing"

// NormalizeImportance 的行为契约（待实现后由本文件钉住）。
func TestNormalizeImportance_CanonicalizesUpstreamValues(t *testing.T) {
	cases := []struct{ in, want string }{
		// 规范值原样透传
		{"high", "high"}, {"medium", "medium"}, {"low", "low"},
		// 大小写差异 —— LLM 返回最常见的偏差
		{"High", "high"}, {"HIGH", "high"}, {"Medium", "medium"}, {"LOW", "low"},
		// 前后空白
		{" high ", "high"}, {"\tmedium\n", "medium"},
		// 中文/同义写法
		{"高", "high"}, {"中", "medium"}, {"低", "low"},
		{"重要", "high"}, {"普通", "medium"},
		// 数字档位
		{"1", "high"}, {"2", "medium"}, {"3", "low"},
		// 无法识别 -> 空（表示「未分类」，由 splitReminderCandidates 计入 unclassified）
		{"", ""}, {"urgent-ish", ""}, {"3.5", ""},
	}
	for _, tc := range cases {
		if got := NormalizeImportance(tc.in); got != tc.want {
			t.Errorf("normalizeImportanceForTest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// 端到端契约：脏的 importance 不得让重要邮件漏提醒。
// 这是需求 4 的核心——"High" 必须与 "high" 一样进 toNotify。
func TestSplitReminderCandidates_UpstreamCaseDoesNotLoseReminders(t *testing.T) {
	notified := []int64{0, 0, 0}
	emails := []Email{
		{ID: "e1", Importance: "high"},
		{ID: "e2", Importance: "High"}, // 上游大小写偏差
		{ID: "e3", Importance: "HIGH"}, // 上游全大写
	}
	// 归一化后应当三者都进 toNotify
	normalized := make([]Email, len(emails))
	for i, e := range emails {
		e.Importance = NormalizeImportance(e.Importance)
		normalized[i] = e
	}
	toNotify, unclassified := splitReminderCandidates(normalized, notified)
	if len(toNotify) != 3 {
		t.Fatalf("all three must be notified, got %d (unclassified=%d)", len(toNotify), unclassified)
	}
	for _, e := range toNotify {
		if e.Importance != "high" {
			t.Fatalf("toNotify entry %s has non-canonical importance %q", e.ID, e.Importance)
		}
	}
}

// BuildClassifyWrites 必须同时归一化 category 和 importance。
// 这条钉住「两个字段都要过一遍」——修复前只有 category 被处理。
func TestBuildClassifyWrites_NormalizesBothFields(t *testing.T) {
	in := []RawClassifyResult{
		{EmailID: "e1", Category: "WORK", Importance: "High"},
		{EmailID: "e2", Category: "bill", Importance: "MEDIUM"},
		{EmailID: "e3", Category: "spam", Importance: "Low"},
	}
	out := BuildClassifyWrites(in)
	if len(out) != 3 {
		t.Fatalf("all 3 rows must survive, got %d", len(out))
	}
	for _, r := range out {
		switch r.Importance {
		case "high", "medium", "low":
		default:
			t.Errorf("importance %q was not canonicalized (row %s)", r.Importance, r.EmailID)
		}
	}
	if out[0].Importance != "high" {
		t.Errorf("row e1 importance = %q, want high", out[0].Importance)
	}
	if out[1].Importance != "medium" {
		t.Errorf("row e2 importance = %q, want medium", out[1].Importance)
	}
	if out[2].Importance != "low" {
		t.Errorf("row e3 importance = %q, want low", out[2].Importance)
	}
}

// 无法识别的 importance 必须变成空（= 未分类），而不是原样落一个脏值。
// 空值会被 splitReminderCandidates 计入 unclassified，报告里看得见；
// 脏值则既不提醒也不计数——最坏情况。
func TestBuildClassifyWrites_UnknownImportanceBecomesEmpty(t *testing.T) {
	in := []RawClassifyResult{
		{EmailID: "e1", Category: "work", Importance: "critical-ish"},
	}
	out := BuildClassifyWrites(in)
	if len(out) != 1 {
		t.Fatalf("row must survive (category is valid), got %d", len(out))
	}
	if out[0].Importance != "" {
		t.Fatalf("unknown importance must become empty, got %q", out[0].Importance)
	}
	// 空值必须能被 splitReminderCandidates 识别为「未分类」
	_, unclassified := splitReminderCandidates([]Email{{ID: "e1", Importance: out[0].Importance}}, []int64{0})
	if unclassified != 1 {
		t.Fatalf("empty importance must count as unclassified, got %d", unclassified)
	}
}
