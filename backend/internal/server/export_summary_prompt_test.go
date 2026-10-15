// export_summary_prompt_test.go — 把**滚动摘要**提示词导出成文件，供延迟测量使用。
//
// ⚠ 为什么单独一个导出器：滚动摘要是**另一条链**（§66），它的
//   - 提示词不同（`summaryJSONSchema`，不是 `buildRefinePrompt`）
//   - 预算不同（后端 45s + agent 30s，不是 90s）
//   - 形态是**滚动**的：第 N 次调用带「已有摘要 + 新增转写」
//
// 而 `llmMeetingSummary` 本身是发网络请求的，node 里跑不了 ——
// 所以拼装必须先被提成**纯函数** `buildSummaryPrompt`，导出器再调它本人。
//
// ⚠ 「跑不了就手抄」是 §66.2 修掉的错误做法：手抄出来的提示词与生产可能不一致，
// 而没有断言的话谁也发现不了。要么共用纯函数，要么就别导出。
//
// 纪律同 export_refine_prompt_test.go：**直接调被测函数本人**，
// 不去正则抽源码字面量（那式量具一改格式就失明，且是静默失明）。
//
// 用法：
//
//	POCKET_EXPORT_SUMMARY_PROMPT=/tmp/x.txt \
//	POCKET_EXPORT_SUMMARY_TRANSCRIPT=/tmp/y.txt \
//	POCKET_EXPORT_SUMMARY_PREV_FILE=/tmp/prev.txt \
//	go test ./internal/server -run TestExportSummaryPrompt -v
package server

import (
	"os"
	"strings"
	"testing"
)

func TestExportSummaryPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("导出用工具测试")
	}
	out := os.Getenv("POCKET_EXPORT_SUMMARY_PROMPT")
	if out == "" {
		t.Skip("未设 POCKET_EXPORT_SUMMARY_PROMPT")
	}
	transcript := "张伟：我们先过一下接口联调的进度。"
	if p := os.Getenv("POCKET_EXPORT_SUMMARY_TRANSCRIPT"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s: %v", p, err)
		}
		transcript = strings.TrimSpace(string(raw))
	}
	prev := ""
	if p := os.Getenv("POCKET_EXPORT_SUMMARY_PREV_FILE"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s: %v", p, err)
		}
		prev = strings.TrimSpace(string(raw))
	}

	// 提示词拼装已提成纯函数 buildSummaryPrompt，
	// 这里**直接调它本人** —— 生产与导出共用同一个字符串，
	// 漂移在结构上就不可能发生。
	//
	// ⚠ 2026-10-07（§66.2）这一格原来是**手抄** llmMeetingSummary 的两行
	//   fmt.Sprintf，理由是「那个函数会发网络请求，node 里跑不了」，
	//   注释里还写着「下面断言两条分支的字面量与源码完全一致（拼不上就立刻红）」——
	//   **那条断言从来没有写出来**，整份文件里没有。
	//   注释在替一个不存在的守卫背书，而 §66 的摘要链延迟测量走的正是它。
	prompt := buildSummaryPrompt(transcript, prev)

	if err := os.WriteFile(out, []byte(prompt), 0o600); err != nil {
		t.Fatalf("写 %s: %v", out, err)
	}
	t.Logf("已导出 %d 字符（转写 %d / 已有摘要 %d）到 %s", len(prompt), len(transcript), len(prev), out)
}
