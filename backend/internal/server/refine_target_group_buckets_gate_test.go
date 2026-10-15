// refine_target_group_buckets_gate_test.go — §483 的**离线**门：
// 精校探针的「目标组」必须**四档都计数**，且小结的分母必须是真实样本数。
//
// ── 为什么要有这道门（缺陷的完整来龙去脉）─────────────────────────
//
// §477 实测精校探针后记下一条「小结写着『【目标组】需元数据的样本 **0 条**』
// ⇒ 实验在结构上没有回答『元数据有没有用』」。本轮去读源码，发现缺陷比那句话更具体：
//
// `live_gateway_probe_test.go` 的目标组分支原先只有两个计数器：
//
//	onOnly, offOnly := 0, 0                 // 目标组：仅某一臂修好
//	…
//	case "目标":
//	    switch {
//	    case off.mark != "修好" && on.mark == "修好":  onOnly++;  …
//	    case off.mark == "修好" && on.mark != "修好":  offOnly++;  …
//	    default:
//	        verdict = "— 两臂同类（" + off.mark + "）"    // ⛔ 一条都没计数
//	    }
//	…
//	t.Logf("  【目标组】需元数据的样本 %d 条：…", onOnly+offOnly, onOnly, offOnly)
//
// ⛔ 两个洞：
//
//	① 「两臂都修好」与「两臂都没修好」两档落进 `default`，**一条都没进统计**。
//	② 小结把 `onOnly+offOnly`（**臂间有差异的条数**）当成「样本数 N」打印。
//
// ⇒ 读数长这样：
//
//	【目标组】需元数据的样本 0 条：仅 ON 修好 0 / 仅 OFF 修好 0
//	⇒ 净效应为零：加了元数据没换来任何修复。
//
// 而真实情况是 **2 条目标样本都跑过了**（`refineProbeCases()` 里 `kind: "目标"`）。
// 「0 条」**不是「没样本」，是「没有一条出现臂间差异」** —— 处置完全不同：
// 前者要重做实验，后者说明**目标对两臂都没产生区分度**。
//
// ★★ 更要紧的是被漏掉的那一档恰好是 #29 要的天花板：
//
//	「两臂都修好」= 模型自身就能修 ⇒ 这一类**加外部证据的边际收益归零**。
//	「两臂都没修」= 加了元数据也修不动 ⇒ §477 实测它还会被**改得更错**。
//
// —— 也就是说，#29 最需要的两个读数**原本根本没被统计**。
//
// ⚠ 这道门**必须离线**：真精校探针要 `POCKET_LIVE_GATEWAY=1` + 租户 key
// + 14×2 次调用（实测 560–1680s，§476）。能守住它的判据不能依赖网关。
//
// ⚠ 这道门守的是「**四档有没有被分类、小结分母对不对**」，
// 不是「精校准不准」。它对「修好 3 条」还是「修好 0 条」完全无话可说。
package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// refineTargetBuckets 把四档判定抽成**纯函数**，
// 让「四档都必须落到某一档」这条不变量**可被离线验证**。
//
// ⚠ 它与探针里的 `switch` 同形：计数必须与下面那条 `case "目标":` **逐档对应**。
// 若两者各写一份，门就变成在验另一段代码 —— 而那段坏掉时门照样绿
// （§479.2 同族：门守的是副本，不是被测的那段）。
//
// off/on 是两臂的判定，取值只关心「修好」与不是「修好」。
func refineTargetBuckets(off, on string) string {
	switch {
	case off != "修好" && on == "修好":
		return "onOnly"
	case off == "修好" && on != "修好":
		return "offOnly"
	case off == "修好" && on == "修好":
		return "bothFixed"
	default:
		return "neitherFixed"
	}
}

// TestRefineTargetBucketsCoverEveryCombination 是量具自证 + 完备性判据。
//
// ⚠ 为什么要穷举 2×2：四档的判定条件是**互斥且完备**的，
// 而 §483 的缺陷正是「有一档没人管」。穷举能把「漏一档」变成红灯，
// 不必等跑真网关才发现。
func TestRefineTargetBucketsCoverEveryCombination(t *testing.T) {
	marks := []string{"修好", "原样", "改错", "失败", "兜底拦下"}
	seen := map[string]bool{}
	for _, off := range marks {
		for _, on := range marks {
			b := refineTargetBuckets(off, on)
			switch b {
			case "onOnly", "offOnly", "bothFixed", "neitherFixed":
				seen[b] = true
			default:
				t.Fatalf("off=%q on=%q 返回了未知档位 %q", off, on, b)
			}
		}
	}
	for _, want := range []string{"onOnly", "offOnly", "bothFixed", "neitherFixed"} {
		if !seen[want] {
			t.Errorf("四档里的 %q 在 2×2 穷举下**一次都没出现** ⇒ 这一档的判定条件写错了", want)
		}
	}
	// 反向自证：「两臂都修好」绝不能落进 onOnly/offOnly ——
	// 那正是 §483 之前丢数据的样子。
	if got := refineTargetBuckets("修好", "修好"); got != "bothFixed" {
		t.Errorf("两臂都修好应判 bothFixed，实际 %q", got)
	}
	if got := refineTargetBuckets("改错", "原样"); got != "neitherFixed" {
		t.Errorf("两臂都没修好应判 neitherFixed，实际 %q", got)
	}
}

// TestRefineProbeCasesHaveTargetSamples 守住「目标组**非空**」。
//
// ⚠ 为什么这条不能省：`TestRefineTargetBucketsCoverEveryCombination`
//
//	验的是**判定函数**，与语料无关。若哪天有人把 `refineProbeCases()`
//	里两条 `kind: "目标"` 删了（它们很容易被当成「已过时」清掉），
//	上面那条照样全绿，而探针会打印「目标组 0 条」并自报错 ——
//	但那时它报的是**运行时**错，而这里是**零成本**的离线发现。
func TestRefineProbeCasesHaveTargetSamples(t *testing.T) {
	byKind := map[string]int{}
	for _, c := range refineProbeCases() {
		byKind[c.kind]++
	}
	if byKind["目标"] == 0 {
		t.Fatalf("refineProbeCases() 里没有 kind=\"目标\" 的样本（当前分布 %v）⇒ "+
			"§31 说的「这两条是 termHint 存在的全部理由」已不成立，"+
			"请先确认它是否被有意移除，再更新设计文档 §31/§483", byKind)
	}
	if byKind["负控"] == 0 {
		t.Fatalf("refineProbeCases() 里没有 kind=\"负控\" 的样本（当前分布 %v）⇒ "+
			"§31.2 的风险面（termHint 自己引入的误改）不再被测", byKind)
	}
	t.Logf("语料分布 %v —— 目标组与负控组都在", byKind)
}

// TestRefineTargetCasesHaveCorrectFormInMeta 守住目标样本的**成立条件**。
//
// ⭐ 这是 §486 加进来的新风险：目标组的含义是「**只有**拿到元数据才可能修对」，
// 而这件事**依赖于正确写法确实出现在元数据里**。
// 一旦有人改了 Title / Participants 的措辞（或把规则里的 right 改掉），
// 样本就会**静默地不再是目标样本** —— 而探针照样跑、照样报数，
// 只是那条数从「元数据的功劳」变成了「模型自己就会」。
//
// ⚠ 与负控的判据形状**恰好相反**，别混：
//
//	负控要求「名单词与转写**完全不相交**」（否则泄漏判据恒真）
//	目标要求「正确写法**必须**在元数据里」（否则它根本不是目标样本）
func TestRefineTargetCasesHaveCorrectFormInMeta(t *testing.T) {
	for _, c := range refineProbeCases() {
		if c.kind != "目标" {
			continue
		}
		meta := c.meta.Title
		for _, p := range c.meta.Participants {
			meta += " " + p
		}
		if len(c.rules) == 0 {
			t.Errorf("目标样本 %q 没有规则 ⇒ 探针无从判断它修没修好", c.name)
			continue
		}
		for _, r := range c.rules {
			if !strings.Contains(meta, r.right) {
				t.Errorf("目标样本 %q 的判据要求 %q→%q，但**正确写法 %q 不在元数据里**"+
					"（元数据=%q）⇒ 它已不是目标样本，这条数会被误读成「元数据的功劳」",
					c.name, r.wrong, r.right, r.right, meta)
			}
			if strings.Contains(c.asrOut, r.right) {
				t.Errorf("目标样本 %q 的转写里**本来就含**正确写法 %q ⇒ 它不需要元数据就会「修好」",
					c.name, r.right)
			}
		}
	}
}

// TestRefineTargetHasIdentifierCases 守住「目标组里真的有标识符」。
//
// ⚠⚠ 为什么是**显式 class**而不是字符串特征（§486.4 变异 S3 实测）：
//
//	第一版用「`right` 含 ASCII 字母/数字」当判据，
//	而把三条标识符样本**全部删掉**后门**照样全绿** ——
//	因为「音译专名」那条的 `right` 是 `Polaris`，**它也是 ASCII**。
//	⇒ 判据分不开「技术标识符」与「音译专名」，却假装能分。
//	⇒ **分不开的判据不该用来守东西** ⇒ 语料自己声明 class。
//
// ⚠ 为什么它重要（§481.3 + §484）：真实主导错误是 `createdAt`/`changelog`/
// `consumerlag` 这一类。若哪天它们被清掉，这里必须先红 ——
// 否则读数会安静地退化成「元数据对人名有没有用」，而没人发现问错了问题。
func TestRefineTargetHasIdentifierCases(t *testing.T) {
	known := map[string]bool{"人名": true, "音译专名": true, "标识符": true}
	byClass := map[string]int{}
	for _, c := range refineProbeCases() {
		if c.kind != "目标" {
			continue
		}
		if !known[c.class] {
			t.Errorf("目标样本 %q 的 class=%q 不在已登记取值内（人名/音译专名/标识符）", c.name, c.class)
		}
		byClass[c.class]++
	}
	if byClass["标识符"] == 0 {
		t.Errorf("目标组里没有 class=\"标识符\" 的样本（当前 %v）⇒ "+
			"#29 真正关心的「术语/标识符」类别不被测；"+
			"读数会安静地退化成「元数据对人名有没有用」（§481.3/§484）", byClass)
	}
	t.Logf("目标组分类 %v", byClass)
}

// TestRefineProbeCountsAllFourBuckets 守**探针本人**。
//
// ⚠⚠ 为什么前面两条还不够：它们验的是本文件里那份镜像实现与语料，
//
//	而「探针到底打不打算把四档都统计」是**另一件事** ——
//	有人把 §483 的修复回退成 `default: verdict = "—"` 的话，两条全绿，
//	而 §477 那条读数会原样复发。⇒ 必须读探针源码。
func TestRefineProbeCountsAllFourBuckets(t *testing.T) {
	src, err := os.ReadFile("live_gateway_probe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// ① 四个计数器都必须声明并自增（漏一个自增 = 那一档永远为 0）。
	for _, v := range []string{"targetN", "onOnly", "offOnly", "bothFixed", "neitherFixed"} {
		if !regexp.MustCompile(`(?m)^\s*` + v + `\+\+`).MatchString(s) {
			t.Errorf("探针里找不到 `%s++` ⇒ 这一档永远不会被计数", v)
		}
	}
	// ② 小结的分母必须是 targetN，**不许**再用 onOnly+offOnly。
	//
	// ⚠⚠ 判据必须**逐行且锚定到小结那行**。第一版写成全文裸正则
	// `onOnly\s*\+\s*offOnly\s*,`，结果被 §483 修复里自己新增的
	// 对账行 `bothFixed+neitherFixed+onOnly+offOnly, targetN)` 命中 ——
	// 那行**本来就该**在 `offOnly` 后面跟逗号。
	// ⇒ 形态与 §474.4/§481 的「判据抓错对象」完全同形：
	//   判据红了，但红的是别的东西。**眼睛扫一遍命中位置**救下来这一轮。
	offSum := regexp.MustCompile(`onOnly\s*\+\s*offOnly`)
	badLine := -1
	for i, ln := range strings.Split(s, "\n") {
		if !offSum.MatchString(ln) {
			continue
		}
		// 只在同一行确实在打印【目标组】小结时才算违规。
		if !strings.Contains(ln, "【目标组】") {
			continue
		}
		badLine = i + 1
		break
	}
	if badLine > 0 {
		t.Errorf("第 %d 行：小结里仍在用 `onOnly+offOnly` 当分母 —— "+
			"那是「臂间有差异的条数」，不是「样本数」；"+
			"目标样本都跑过但两臂一致时，它会打印「样本 0 条」，"+
			"而那意思其实是「样本都在、只是没区分度」（§483）", badLine)
	}
	if !strings.Contains(s, "【目标组】需元数据的样本 %d 条：\", targetN") {
		t.Error("小结里的目标组分母不是 targetN")
	}
	// ③ 目标组为空必须自己变红（与「没跑」同形，表照旧打印、退出码照旧 0）。
	if !strings.Contains(s, "if targetN == 0 {") {
		t.Error("探针没有「目标组为空就报错」的分支 ⇒ " +
			"这一档可以整张印出来而其实根本没跑到，与「没跑」完全同形")
	}
	// ④ 四档合计必须与样本数对账。
	if !strings.Contains(s, "bothFixed+neitherFixed+onOnly+offOnly != targetN") {
		t.Error("探针没有「四档合计 ≠ 样本数」的对账断言 ⇒ " +
			"再加一档漏计数时不会有任何红灯")
	}
}

// ⚠ 负控：判据必须能抓到「有人把修复回退」。
//
// 没有这一条，上面 ①~④ 都可能是恒真的 —— 而「恒真」与「正确」
// 在退出码上没有任何区别（§471.2 同族）。
func TestRefineProbeBucketGuardIsNotVacuous(t *testing.T) {
	// 造一份「回退到 §483 之前」的样子：只有两档 + 裸 default + 旧分母。
	regressed := `
	onOnly, offOnly := 0, 0
	case "目标":
		switch {
		case off.mark != "修好" && on.mark == "修好":
			onOnly++
		case off.mark == "修好" && on.mark != "修好":
			offOnly++
		default:
			verdict = "— 两臂同类"
		}
	t.Logf("【目标组】需元数据的样本 %d 条", onOnly+offOnly)
	`
	for _, pat := range []string{
		`(?m)^\s*targetN\+\+`,
		`onOnly\s*\+\s*offOnly\s*,\s*onOnly`,
	} {
		if regexp.MustCompile(pat).MatchString(regressed) {
			t.Fatalf("判据自检失效：回退版源码竟然通过了 %q", pat)
		}
	}
	// 反向自证：当前真源码必须通过同一批判据。
	src, err := os.ReadFile("live_gateway_probe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, pat := range []string{
		`(?m)^\s*targetN\+\+`,
		`(?m)^\s*bothFixed\+\+`,
		`(?m)^\s*neitherFixed\+\+`,
	} {
		if !regexp.MustCompile(pat).MatchString(s) {
			t.Fatalf("判据自检失效：当前探针源码反而不通过 %q ⇒ 锚点已漂移，先修锚点", pat)
		}
	}
	t.Log("负控通过：回退版被判为不通过，当前源码通过同一批判据")
}
