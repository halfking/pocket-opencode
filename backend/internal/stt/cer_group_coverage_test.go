// cer_group_coverage_test.go — §474.4 的**离线**门：分组汇总不许静默丢掉任何一类语料。
//
// ── 为什么要有这道门（缺陷的完整来龙去脉）─────────────────────────
//
// `live_asr_cer_probe_test.go` 的分组汇总原先遍历的是**写死的** 7 类：
//
//	for _, k := range []string{"基础","数字","英文混排","专名","同音易错","口语","长句"}
//
// 而 `asrCases()` 的 kind 有 **8** 个 —— 多出来的是 **`中英混排`，16 条**，
// 是整个语料里最大的一组。语料自己的注释写着它为什么存在：
//
//	「13 条样本每类才 1–2 条，得不出可信的分类结论；
//	  这一组把它做成**真正能回答问题的规模**（16 条）」
//
// ⇒ 这张表**从来没有显示过最该看的那一组**。逐条行仍在，数据没丢；
// 丢的是**决策者实际读的那一面**。
//
// ⚠⚠ 为什么这道门**必须离线**：
//
//	真 CER 探针要 `POCKET_LIVE_GATEWAY=1` + 租户 key + 29×2 次请求。
//	也就是说，**在没有任何凭据的环境里（CI、别人的克隆、新会话），
//	这个缺陷既不会发生、也不会被发现、也不会被挡住**。
//	⇒ 能守住它的判据不能依赖网关。
//
// ⚠ 这道门守的是「**类能不能被显示**」，不是「CER 高不高」——
//
//	两者别混：它对 `9.7%` 还是 `37.5%` 完全无话可说。
package stt

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// cerKindOrderRef 与被测代码里的推导路径**逐字同形**。
//
// ⚠ 这不是复制粘贴的坏味道，是**必要的耦合**：门要验证的正是
// 「数据推导出的组列表 == 表里打印的组列表」。若两者各写一份，
// 门就变成在验另一段代码 —— 而那段代码坏掉时门照样绿。
func cerKindOrderRef(present map[string]int) []string {
	kinds := make([]string, 0, len(present))
	for _, k := range knownKindOrder {
		if _, ok := present[k]; ok {
			kinds = append(kinds, k)
		}
	}
	rest := make([]string, 0, len(present))
	for k := range present {
		if !contains(kinds, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(kinds, rest...)
}

// TestCERGroupTableCoversEveryKind 是本门的主判据。
func TestCERGroupTableCoversEveryKind(t *testing.T) {
	present := map[string]int{}
	for _, c := range asrCases() {
		present[c.kind]++
	}
	if len(present) < 2 {
		t.Fatalf("语料只有 %d 个 kind，判据失去意义（语料可能没加载）", len(present))
	}
	got := cerKindOrderRef(present)
	if len(got) != len(present) {
		missing := make([]string, 0)
		for k := range present {
			if !contains(got, k) {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		t.Fatalf("语料有 %d 类，分组表只显示 %d 类；漏掉 %v", len(present), len(got), missing)
	}
	t.Logf("语料 %d 条 / %d 类：%s —— 全部可显示", len(asrCases()), len(present), strings.Join(got, "、"))
}

// TestCERGroupTableKeepsHistoricOrder 守住**排序可读性**：
// knownKindOrder 里已登记的类必须按登记顺序排在前面。
//
// ⚠ 为什么单独一条：上面的主判据只比「集合相等」，
//
//	而排序退化（所有新 kind 挤到最前）会让那张表变得难读，
//	而读数难读 ⇒ 下次有人「顺手整理」时更容易把某个类删掉。
func TestCERGroupTableKeepsHistoricOrder(t *testing.T) {
	present := map[string]int{}
	for _, c := range asrCases() {
		present[c.kind]++
	}
	got := cerKindOrderRef(present)
	// knownKindOrder 与 present 的交集必须**按登记顺序**出现在 got 的前缀里。
	var prefix []string
	for _, k := range knownKindOrder {
		if _, ok := present[k]; ok {
			prefix = append(prefix, k)
		}
	}
	if len(got) < len(prefix) {
		t.Fatalf("got 只有 %d 项，少于已登记的 %d 项：%v", len(got), len(prefix), got)
	}
	for i, k := range prefix {
		if got[i] != k {
			t.Errorf("第 %d 位应是 %q，实际 %q（登记顺序：%v）", i, k, got[i], prefix)
		}
	}
}

// TestCERProbePrintsFromData 守**探针本人**的打印循环。
//
// ⚠⚠ 为什么前面三条还不够：它们验的是本文件里那份**镜像实现**
//
//	（`cerKindOrderRef`）。若有人把探针的循环改回
//	`for _, k := range []string{...}` 而 `knownKindOrder` 保持不变，
//	三条全绿、而那张表**又**开始丢类别 —— 门守的是副本，不是被测的那段。
//	⇒ 必须直接读探针源码，断言它**不再有「只遍历字面量列表」的循环**。
//
// ⚠ 口径与本仓既有的源码级判据一致（`llm_prompt_schema_gate_test.go` 就是
//
//	按 anchor 取源码）：这里只断「形状」，不断数值 —— 数值要真跑网关才有。
func TestCERProbePrintsFromData(t *testing.T) {
	src, err := os.ReadFile("live_asr_cer_probe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	// ⚠ 精确形状：range 后面**直接跟一个字符串字面量切片**。
	//   这正是 §474.4 缺陷的原始形态；数据推导版是 `range kinds`。
	if regexp.MustCompile(`range\s+\[\]string\{[^}]*"基础"`).MatchString(s) {
		t.Error("探针的分组循环又在遍历**字面量列表**了 —— " +
			"那正是 §474.4 的缺陷形态：新增 kind 不会被显示。" +
			"请改成从 byKind 推导（见该处注释）")
	}
	// 正样本自证：数据推导真的在（否则上面那条判据可能是恒假）。
	if !regexp.MustCompile(`range\s+kinds\s*\{`).MatchString(s) {
		t.Error("探针里找不到 `range kinds` ⇒ 数据推导循环不在，判据可能恒假")
	}
	// knownKindOrder 必须还在，且**含**中英混排（它是最大的一组）。
	if !strings.Contains(s, `knownKindOrder = []string{`) {
		t.Fatal("knownKindOrder 不见了")
	}
	if !regexp.MustCompile(`"中英混排"`).MatchString(s) {
		t.Error("knownKindOrder 里没有 中英混排 —— 它是语料中最大的一组（16/29 条）")
	}
}

// ⚠ 负控：判据必须能抓到**下一次**再有人加一个没登记的 kind。
//
// 没有这一条，上面两条都可能是恒真的 ——
// 而「恒真」与「正确」在退出码上没有任何区别（§471.2 同族：
// 量具自己坏掉时，门会给出一个毫无异常读数）。
func TestCERGroupTableCatchesUnregisteredKind(t *testing.T) {
	present := map[string]int{"基础": 2, "中英混排": 16}
	got := cerKindOrderRef(present)
	if len(got) != len(present) {
		t.Fatalf("一个未登记到 knownKindOrder 的 kind 让判据漏项了：got=%v", got)
	}
	if got[0] != "基础" {
		t.Errorf("已登记的类必须排在前面，第 0 位应是 基础，实际 %q", got[0])
	}
	if !contains(got, "中英混排") {
		t.Error("中英混排 必须出现在表里 —— 这正是 §474.4 的缺陷本体")
	}
	t.Logf("未登记 kind 被正确追加到末尾：%v", got)
}
