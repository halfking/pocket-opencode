package email

// backfill_guard_test.go — BackfillHistory 的源码级护栏。
//
// 为什么是「扫源码」而不是跑行为：BackfillHistory 的关键不变式
// 「绝不推进 LastSyncedUID」只有连上真 IMAP 才能观察到，而这台机器上
// 没有 IMAP 环境（Greenmail 那套要 -tags=greenmail + docker + PG_DSN）。
// 一条没人跑的行为测试等于没有护栏，所以这里沿用 snippet_test.go 的做法：
// 直接断言源码里不允许出现的写法。
//
// 不变式为什么重要：历史回补按**日期**搜索 IMAP，捞回来的邮件 UID 必然
// 小于当前 LastSyncedUID。若回补顺手把 LastSyncedUID 推到「本批最大 UID」，
// 下一轮增量同步就从这个 UID+1 开始搜 —— 位于它与原游标之间的**新邮件
// （UID 更小、但日期更新）会被永久跳过**。这是把「补历史」变成「丢新邮件」
// 的最隐蔽的一种写法。

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 剥掉注释后扫描：护栏自身的说明文字里就写着这些禁用写法，不剥离会自我误判。
func codeOnly(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s：%v", name, err)
	}
	var b strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestBackfillNeverAdvancesSyncCursor(t *testing.T) {
	code := codeOnly(t, "backfill.go")

	forbidden := []struct {
		desc string
		re   string
	}{
		{
			desc: "写了 LastSyncedUID（推进游标会永久跳过新邮件）",
			re:   `LastSyncedUID\s*[:=]`,
		},
		{
			desc: "调用了 UpdateSyncState（增量同步的落库路径，不该被历史回补复用）",
			re:   `UpdateSyncState`,
		},
		{
			desc: "按 UID 区间搜（历史回补必须按日期窗口，否则又退回增量语义）",
			re:   `SearchCriteria\{[^}]*UID`,
		},
	}
	// 必须真的按正则编译后 MatchString。用 strings.Contains 去比对一个正则
	// 字面量（"LastSyncedUID\\s*[:=]"）永远为 false —— 这条护栏会一直绿，
	// 却不拦任何东西。负控：往 backfill.go 注入一行
	//   `if acc.LastSyncedUID < em.UID { acc.LastSyncedUID = em.UID }`
	// 本测试必须转红；注入前它是绿的。
	for _, f := range forbidden {
		re, err := regexp.Compile(f.re)
		if err != nil {
			t.Fatalf("护栏自身的正则 %q 编译失败：%v", f.re, err)
		}
		if re.MatchString(code) {
			t.Errorf("backfill.go 仍存在%s", f.desc)
		}
	}
}

// 回补必须真的走日期窗口，且必须复用 Sync 的取件映射。
//
// 这两条是「补回来的邮件与增量同步产出的行兼容」的前提：映射一旦分叉
// （messageID 兜底、缺 Date 头、snippet 派生、规则语义任一处不同），
// 重跑时两边写同一封邮件会互相覆盖字段。
func TestBackfillUsesDateWindowAndSharedMapping(t *testing.T) {
	code := codeOnly(t, "backfill.go")

	if !strings.Contains(code, "Since:") {
		t.Error("backfill.go 没有按 Since 日期窗口搜索 IMAP")
	}
	if !strings.Contains(code, "f.emailFromMessage(") {
		t.Error("backfill.go 没有复用 Sync 的 emailFromMessage，取件映射会分叉")
	}
	if !strings.Contains(code, "f.recordActionIntent(") {
		t.Error("backfill.go 没有落 email_action_intents：这批邮件的自动回复/归档将永不执行")
	}
	if !strings.Contains(code, "DefaultBackfillDays") || !strings.Contains(code, "DefaultBackfillMax") {
		t.Error("backfill.go 缺少 DefaultBackfillDays / DefaultBackfillMax 常量，调用方无法安全地沿用默认深度")
	}
}

// Sync 与 Backfill 必须走同一份取件映射与规则语义。
//
// 这里刻意断言的是**包级**不变量而不是「fetcher.go 里不能出现某个词」：
// applyInlineRules 内部本来就该调 rules.Evaluate，Sync 又是经 emailFromMessage
// 间接走到它的。写成「某个文件里不许出现 X」会在函数搬家时假失败，久了就没人
// 认真维护这条护栏。
func TestSyncAndBackfillShareInlineRuleEvaluation(t *testing.T) {
	// 1) rules.Evaluate 在整个包里只能被调用一次，且那一次必须在
	//    applyInlineRules 内部。两处各写一份的话，任一处改了规则语义，
	//    补回来的邮件和增量同步的邮件就会被写成两套结果，重跑时互相覆盖。
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录：%v", err)
	}
	total, inShared := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		code := codeOnly(t, name)
		n := strings.Count(code, "rules.Evaluate(")
		if n == 0 {
			continue
		}
		total += n
		if strings.Contains(code, "func applyInlineRules(") {
			inShared += n
		} else {
			t.Errorf("%s 直接调了 rules.Evaluate，绕过了共享的 applyInlineRules", name)
		}
	}
	if total != 1 {
		t.Errorf("包内 rules.Evaluate( 出现 %d 次，期望恰好 1 次（只在 applyInlineRules 里）", total)
	}
	if inShared != 1 {
		t.Errorf("applyInlineRules 里的 rules.Evaluate 出现 %d 次，期望 1 次", inShared)
	}

	// 2) 两个入口都必须经由共享映射 emailFromMessage。
	if !strings.Contains(codeOnly(t, "fetcher.go"), "f.emailFromMessage(") {
		t.Error("fetcher.go 的 Sync 没有走 emailFromMessage：Sync 与 Backfill 的取件映射会分叉")
	}
	if !strings.Contains(codeOnly(t, "backfill.go"), "f.emailFromMessage(") {
		t.Error("backfill.go 的 BackfillHistory 没有走 emailFromMessage")
	}
}
