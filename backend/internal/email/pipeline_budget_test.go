package email

// pipeline_budget_test.go — 第 1.5 步拉原文的预算与优先级。
//
// 背景（真实邮箱实测）：24h 窗口内 151 封未建档邮件，其中一部分需要拉原文
// 做发票二次提取/补开票日期，而每封原文 = 一次完整 IMAP 会话。原实现是
// for 循环逐封串行拉、且没有上限，实测这一步跑了 6 分钟仍未完。
//
// 这里钉死「超预算时谁被挤掉」：date（已确定是发票、只差日期）永远优先，
// candidate（推测性扫描）顺延到下一轮，不能反过来。

import "testing"

func TestLimitInvoiceBodyJobs_KeepsDateBeforeCandidate(t *testing.T) {
	// 先塞满 candidate，再塞 2 个 date：naive 的「截前 N 个」会把 date 全挤掉。
	jobs := make([]bodyJob, 0, maxInvoiceBodyFetches+5)
	for i := 0; i < maxInvoiceBodyFetches+5; i++ {
		jobs = append(jobs, bodyJob{reason: "candidate"})
	}
	jobs = append(jobs, bodyJob{reason: "date"}, bodyJob{reason: "date"})

	kept := limitInvoiceBodyJobs(jobs)
	if len(kept) != maxInvoiceBodyFetches {
		t.Fatalf("kept %d jobs, want exactly the budget %d", len(kept), maxInvoiceBodyFetches)
	}

	keptReasons := map[string]int{}
	for _, idx := range kept {
		keptReasons[jobs[idx].reason]++
	}
	if keptReasons["date"] != 2 {
		t.Fatalf("kept %d date job(s), want 2 —— 已确定是发票的邮件不能被推测性扫描挤掉", keptReasons["date"])
	}
	if keptReasons["candidate"] != maxInvoiceBodyFetches-2 {
		t.Fatalf("kept %d candidate job(s), want %d", keptReasons["candidate"], maxInvoiceBodyFetches-2)
	}
}

func TestLimitInvoiceBodyJobs_UnderBudgetKeepsEverything(t *testing.T) {
	jobs := []bodyJob{{reason: "date"}, {reason: "candidate"}, {reason: "date"}}
	kept := limitInvoiceBodyJobs(jobs)
	if len(kept) != len(jobs) {
		t.Fatalf("kept %d of %d, want all (未超预算不应丢任何一封)", len(kept), len(jobs))
	}
	// kept 必须是原序（0,1,2），否则回填会错位。
	for i, idx := range kept {
		if idx != i {
			t.Fatalf("kept[%d] = %d, want %d（顺序必须与原切片一致）", i, idx, i)
		}
	}
}

func TestLimitInvoiceBodyJobs_Empty(t *testing.T) {
	if got := limitInvoiceBodyJobs(nil); len(got) != 0 {
		t.Fatalf("limitInvoiceBodyJobs(nil) = %v, want empty", got)
	}
}
