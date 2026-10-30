package server

import (
	"os"
	"strings"
	"testing"
)

// TestSessionLinksHasNoNPlusOne 是 N+1 优化的**形状护栏**（2026-10-28 §976）。
//
// ⚠⚠ 为什么**行为判据抓不到**这个回归：
//
//	「批量版」与「逐任务版」产出的**行集合完全相同**
//	（那是 internal/task/store_session_links_batch_test.go 钉住的东西）
//	⇒ 谁把实现退回 N+1，行为判据**照样绿** ⇒ 只有**形状**变了：
//	  查询数从 1 变回 1+N，而 N 随任务数增长。
//	⇒ 所以必须有一条**形状**判据：SessionLinks 的函数体里
//	  不许再出现 ListSessionsForTask（那正是 N+1 的那一次调用），
//	  且必须出现 ListAllSessionLinks（批量那一次）。
//
// ⚠ 判据本身也要能被测：把断言反过来（去找 ListSessionsForTask）⇒ 必红；
//
//	把 ListAllSessionLinks 改名 ⇒ 必红。
//
// ⚠ 为什么值得用**源码级**判据（而不是数查询次数）：
//
//	数查询次数需要 instrument pgxpool ⇒ 要改产品代码的观测面，
//	代价大于收益；且判据会随重构失效（改成别的写法就数不到了）。
//	源码级判据盯的是「**不许再按任务逐个查**」这条**形状约束**本身。
func TestSessionLinksHasNoNPlusOne(t *testing.T) {
	src, err := os.ReadFile("mobile_events_handler.go")
	if err != nil {
		t.Fatalf("read mobile_events_handler.go: %v", err)
	}
	text := string(src)

	const marker = "func (i taskStoreSessionIndex) SessionLinks("
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatal("找不到 taskStoreSessionIndex.SessionLinks —— 形状判据失明了（函数被改名/挪走）")
	}
	// 函数体：从 marker 到下一个顶格的 '}'。
	rest := text[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatal("找不到 SessionLinks 的函数体结尾 —— 形状判据失明了")
	}
	body := rest[:end]

	if strings.Contains(body, "ListSessionsForTask(") {
		t.Errorf("SessionLinks 里又出现了 ListSessionsForTask( ⇒ N+1 回来了\n%s\n"+
			"⇒ 这条路径**每 5 秒**被调一次（广播器 taskHealthTick），\n"+
			"  查询数会随任务数线性增长（实测 12 个任务 ⇒ 每 5 秒 13 条查询）。\n"+
			"⇒ 要一次查完，请用 task.Store.ListAllSessionLinks。", body)
	}
	if !strings.Contains(body, "ListAllSessionLinks(") {
		t.Errorf("SessionLinks 里没有 ListAllSessionLinks( ⇒ 形状判据失明或实现被改坏\n%s", body)
	}
}
