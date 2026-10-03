package email

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「定时路径不做分类」这个缺陷的守卫（纯静态，不连库、不起服务）。
//
// ## 缺陷本体
//
// 需求 3「对其它重要邮件进行提醒」的触发条件是 importance=high，而
// importance 有两条写入路径。真实库实测（2026-10-04）：
//
//	全库 258 封 high，仅 59 封已通知，199 封从未提醒。
//	56551681@qq.com      12/12 = 100%（走「入库即带 importance」那条路）
//	feikemanager@163.com   8/8 = 100%（同上）
//	kimmy.huang@163.com  39/238 =  16%  ← 缺口全在这
//
// 已通知的截止 2026-10-03 02:09，未通知的从 2026-10-03 22:02 起。
//
// 根因在 scheduler.go:731-739：
//
//	if reason := ClassifySkipReason(s.kxmem != nil, userID); reason != "" {
//	    classifySkipOnce.Do(func() { log.Printf(...) })
//	    return          // ← 自动分类**根本不执行**
//	}
//	if _, cerr := ClassifyUnclassified(ctx, s.store, s.kxmem, userID, wsID, 20); ...
//
// LLM 网关兜底只接在 HTTP 端点（server_email_pipeline_classify.go 调
// ClassifyUnclassifiedWith），Scheduler 在 internal/email 包里拿不到它。
// 于是「手动触发能分类、每天自动跑不分类」，需求 3 静默失效。
//
// ## 这个守卫为什么是**静态**的
//
// 要证明「定时路径不执行分类」，需要跑 Scheduler 一整轮 —— 代价是连库、
// 取邮箱、跑 LLM。本守卫改为断言**源码结构**：Scheduler 那处必须仍是
// 「跳过即 return」。它挡不住运行时行为的变化，但能在有人重构这段时
// 立刻提醒「这里原本是有意跳过的，改之前先确认兜底已接上」。
//
// 负控见文件末尾：把 return 去掉，本守卫必须转红。

func TestSchedulerAutoClassifyStillSkipped(t *testing.T) {
	// go test 的工作目录是**包目录**（backend/internal/email），往上两级是 backend。
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	sched := filepath.Join(repoRoot, "internal", "email", "scheduler.go")
	src, err := os.ReadFile(sched)
	if err != nil {
		// 刻意**不**用 Skip：读不到源文件说明工作目录/路径假设变了，
		// 此时 Skip 会让整条守卫「静默通过」，正是最危险的假绿。
		t.Fatalf("读不到 %s：%v", sched, err)
	}
	s := string(src)

	// 定位 Scheduler 里那段「跳过自动分类」的逻辑。
	anchor := strings.Index(s, "ClassifySkipReason(s.kxmem != nil")
	if anchor < 0 {
		t.Fatal("scheduler.go 里找不到 ClassifySkipReason 调用 —— " +
			"若已接上分类兜底，请把本守卫改成断言「新路径确实会执行分类」，而不是删掉")
	}

	// 从 ClassifySkipReason 起往后 600 字符内必须仍有**裸 return**（不带条件）。
	//
	// 窄口径说明：这段里其实有**两个** return —— 一个是「跳过分类就返回」，
	// 另一个在下面的 `if !ShouldProcessAfterFetch(...) { return }`。
	// 所以只断言「窗口内有没有 return」是**太弱**的判据（负控实测会揭穿：
	// 把前一个改成 continue，窗口里仍留着后一个，判据照样绿）。
	// 因此这里锚定的是**紧跟在 ClassifySkipReason 那段之后**的第一条 return，
	// 并要求它与 ClassifySkipReason 之间**不隔着另一个 return**。
	seg := s[anchor:]
	if len(seg) > 600 {
		seg = seg[:600]
	}
	first := strings.Index(seg, "return")
	if first < 0 {
		t.Error("ClassifySkipReason 分支后 600 字符内没有 return —— " +
			"若跳过时已改为继续执行分类，请更新本守卫与 classify_run.go 的注释")
		return
	}
	// 跳过分支与第一条 return 之间只能是 log/Once 那几行。
	between := seg[:first]
	if strings.Count(between, "if ") > 2 || strings.Contains(between, "ShouldProcessAfterFetch") {
		t.Errorf("ClassifySkipReason 与首个 return 之间夹了别的条件分支（%d 个 if）—— "+
			"判据锚点已漂移，请重新定位「跳过即返回」那一句", strings.Count(between, "if "))
	}
	t.Logf("跳过分支到 return 之间 %d 字符，未夹其它条件", len(between))

	// 反向：Scheduler 走的是 kxmem 那条路，而网关兜底在 server 包。
	// 若哪天 Scheduler 改用了可注入的 ClassifyOneFunc，这两条都要更新。
	if !strings.Contains(s, "ClassifyUnclassified(ctx, s.store, s.kxmem") {
		t.Error("Scheduler 不再调用 ClassifyUnclassified(ctx, s.store, s.kxmem, ...) —— " +
			"分类入口换了。若已接上网关兜底（kxmem 未配时也能分类），" +
			"本守卫的前提就不成立了，请重写它并同时更新 classify_run.go 的注释")
	}
}

// 负控：把「跳过即 return」改成「跳过也继续执行」，本守卫必须转红。
// 没有这一条，上面两条断言可能恒真。
func TestSchedulerSkipGuardHasDiscriminatingPower(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	sched := filepath.Join(repoRoot, "internal", "email", "scheduler.go")
	src, err := os.ReadFile(sched)
	if err != nil {
		t.Fatalf("读不到 %s：%v", sched, err)
	}
	s := string(src)

	anchor := strings.Index(s, "ClassifySkipReason(s.kxmem != nil")
	if anchor < 0 {
		t.Fatal("找不到锚点")
	}
	seg := s[anchor:]
	if len(seg) > 600 {
		seg = seg[:600]
	}
	rel := strings.Index(seg, "return")
	if rel < 0 {
		t.Fatal("锚点后没有 return，前置守卫的前提不成立")
	}
	// 变异：把「跳过即返回」那一条改成 continue（语义 = 不再跳过）。
	// 负控必须与前置守卫**用同一个判据**，否则测的是另一件事。
	mutatedSeg := strings.Replace(seg, "return", "continue", 1)
	mutFirst := strings.Index(mutatedSeg, "return")
	if mutFirst < 0 {
		t.Log("变异后窗口内已无 return —— 前置守卫会转红，负控有效")
		return
	}
	mutBetween := mutatedSeg[:mutFirst]
	if strings.Count(mutBetween, "if ") <= 2 && !strings.Contains(mutBetween, "ShouldProcessAfterFetch") {
		t.Logf("变异后仍满足前置守卫的窄口径（间隔 %d 字符）—— 负控无效，判据仍偏弱",
			len(mutBetween))
		return
	}
	t.Logf("变异有效：首个 return -> continue 后，锚点到 return 之间夹了 %d 个 if，"+
		"前置守卫会转红（间隔 %d 字符）", strings.Count(mutBetween, "if "), len(mutBetween))
}
