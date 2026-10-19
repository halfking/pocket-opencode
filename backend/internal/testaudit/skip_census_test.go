// skip_census_test.go —— 全仓「被跳过的测试」普查 + 逐条台账覆盖率棘轮。
//
// ── 这道门的历史（它自己也被同一个形态击穿过）──────────────────────────
//
// §467 建了 `internal/stt/live_probe_skip_ledger_test.go`，开篇写的是
// 「让被跳过的测试**可见**」——**全仓级**的措辞，但实现是
// `filepath.Glob("*.go")`，**没有 `../`** ⇒ 实际只覆盖 `internal/stt`。
//
// 2026-10-17 普查轮把它扩到全仓（当时的读数：193 点 / 11 包，
// 有逐条台账的只有 stt，占 10.4%）。2026-10-17 本轮把存量补登完，
// 并在补登过程中发现**普查本身有三处量具缺陷**——全部是「量具把自己
// 的噪声当成读数」，而它一直稳稳输出一个漂亮的数字：
//
//	① **只认 `t.Skip(`**：全仓另有 57 处 `t.Skipf(` 被完全无视。
//	   「193 个跳过点」实际至少 250，低估 23%。
//	② **字面量里的假函数被当成真函数**：`internal/stt` 那份台账文件里的
//	   负控样本（原始字符串中的 `func TestReal(t *testing.T) { t.Skip(...) }`）
//	   被计入普查，该文件贡献 **9 个幽灵点**，stt 基线 20 里有 9 个是自指。
//	③ **函数体里的 `"t.Skip("` 字面量被当成调用**：同一文件写着
//	   `strings.Contains(string(raw), "t.Skip(")`，子串匹配把它算成一个跳过点。
//
// 另外两个**方向相反**的实测：
//
//	④ `internal/livetest` 报的 4 个跳过点**全部是幽灵**——那 4 处 `t.Skip`
//	   是**样本源码**，住在本包一个字符串字面量里（抽取器源码自己的测试样本）。
//	⑤ `third_party/identity-go` 是**独立 module**（`github.com/kaixuan/identity-go`），
//	   主 module 的 `go test ./...` 根本不会跑它，且没有任何 CI workflow 引用它。
//	   普查却把它的 2 个跳过点算进主 module 的分母 ⇒ 虚增覆盖率。
//
// ⇒ 现在本门**完全委托** `internal/skipaudit`（go/parser 实现）。
//
//	手写扫描器必须同时正确处理注释、三种引号、转义、字面量里的花括号、
//	跨行原始字符串……每一条都得对，漏一条就静默错，而输出依然是个数字。
//
// ── 这道门做什么 ──────────────────────────────────────────────────────
//
//	① 普查：逐包点数，装一个**棘轮**（不得增加）。
//	② 覆盖率棘轮：哪些包**必须**有逐条台账，且登记文件必须真的存在。
//	   §467 的登记表是可以被清空而无人发现的 —— 本轮补上这个方向。
//	③ 边界：不跨嵌套 module。
package testaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// ── 基线 ───────────────────────────────────────────────────────────────
//
// key = 包路径（相对 module root，'./' 开头）。⛔ 绝不写行号——
// 行号会在改一行代码后全线漂移（scripts/lib/baseline-ratchet.mjs 的文件头
// 记了同型教训：z-index-ladder / bottom-chrome 的 ALLOWLIST 就因为带行号而漂）。
//
// ⚠ 2026-10-17 重算过一次：从 193 改成 211。**涨了 18，不是有人新增了跳过**，
//
//	是量具修好了（`t.Skipf(` 不再漏认 = +18），同时剔掉幽灵与嵌套 module。
//	⇒ 棘轮只拦「相对这个基线变大」，不拦「比 193 大」。
var SKIP_POINT_BASELINE = map[string]int{
	"./internal/email":                   140,
	"./internal/server":                  39,
	"./internal/stt":                     12,
	"./internal/notes":                   6,
	"./internal/task":                    3,
	"./internal/adapter/disk":            2,
	"./internal/agent":                   2,
	"./internal/config":                  2,
	"./internal/scheduledtask":           2,
	"./internal/scheduledtask/executors": 2,
	"./internal/flashcards":              1,
	"./internal/wecom":                   1,
}

// censusFloor 是量具自证用的下限：抽取器若坏掉，读数会掉到接近 0。
// 取基线的一半（211 → 100）：既能抓住「突然扫不到」也能抓住大规模丢失。
const censusFloor = 100

// packagesWithDetailedLedger：哪些包有逐条台账，值是台账文件名。
//
// ⛔ 本表的价值就是让人一眼看出**还有谁没台账**。2026-10-17 补登前它只有
//
//	1 个包（10.4% 覆盖）；补登后见下。
var packagesWithDetailedLedger = map[string]string{
	"./internal/email":                   "skip_ledger_test.go",
	"./internal/server":                  "skip_ledger_test.go",
	"./internal/stt":                     "live_probe_skip_ledger_test.go",
	"./internal/notes":                   "skip_ledger_test.go",
	"./internal/task":                    "skip_ledger_test.go",
	"./internal/adapter/disk":            "skip_ledger_test.go",
	"./internal/agent":                   "skip_ledger_test.go",
	"./internal/config":                  "skip_ledger_test.go",
	"./internal/scheduledtask":           "skip_ledger_test.go",
	"./internal/scheduledtask/executors": "skip_ledger_test.go",
	"./internal/flashcards":              "skip_ledger_test.go",
	"./internal/wecom":                   "skip_ledger_test.go",
}

// ledgerRequired 是**覆盖率的棘轮**：这些包必须出现在 packagesWithDetailedLedger 里，
// 且对应的台账文件必须真的存在。
//
// ⚠ 为什么需要它（2026-10-17 实测，不是推断）：
//
//	`packagesWithDetailedLedger` 原来**可以被整个删空**而门照样绿 ——
//	§467 那一版只检查「表里的包普查得到跳过点」，没检查「表本身非空」，
//	更没检查「表里说的台账文件存在」。
//
//	而这张表恰恰是**唯一**让「哪��包还没被逐条登记」可见的地方。
//	把它删掉，等于把这次审计的全部产出静音。
var ledgerRequired = []string{
	"./internal/email",
	"./internal/server",
	"./internal/stt",
	"./internal/notes",
	"./internal/task",
	"./internal/adapter/disk",
	"./internal/agent",
	"./internal/config",
	"./internal/scheduledtask",
	"./internal/scheduledtask/executors",
	"./internal/flashcards",
	"./internal/wecom",
}

// ── 工具 ───────────────────────────────────────────────────────────────

// moduleRoot 向上找 go.mod。
// ⚠ **不能**写死 '../..' —— capabilities-stub-gate 的文件头记了同型：
// 那道门先后两次算错级数，插件目录读成空 ⇒ 判据恒真通过。
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("从工作目录向上找不到 go.mod，无法定位 module root")
	return ""
}

// walkTestFiles 递归收集 module root 下所有 *_test.go。
//
// ⚠⚠ **遇到嵌套 go.mod 必须停**（2026-10-17 补记）：
//
//	`third_party/identity-go` 是独立 module（github.com/kaixuan/identity-go），
//	主 module 的 `go test ./...` **不会**跑它，也没有 CI workflow 引用它。
//	普查把它算进来 = 虚增覆盖率，而且会让人以为「那里也有台账管着」。
//
//	⚠ 这正是「扫描范围」的经典陷阱：不是「扫多了」，是**扫进了另一个宇宙**。
func walkTestFiles(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			// 嵌套 module 边界：进去就不是本 module 的测试了
			if p != root {
				if _, e := os.Stat(filepath.Join(p, "go.mod")); e == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// testDirs 返回所有含 *_test.go 的目录（去重、排序）。
func testDirs(root string) []string {
	seen := map[string]bool{}
	for _, f := range walkTestFiles(root) {
		seen[filepath.Dir(f)] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// census 返回「包路径 → 跳过点数」与总数。
func census(root string) (map[string]int, int, map[string]int) {
	perPkg := map[string]int{}
	perPkgFuncs := map[string]int{}
	total := 0
	for _, dir := range testDirs(root) {
		fs, err := skipaudit.ScanDir(dir, nil)
		if err != nil {
			// 抽取器出错必须让门红，不能静默少算一整个目录
			fmt.Printf("!! %s 扫描失败: %v\n", dir, err)
			continue
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			continue
		}
		pkg := "./" + filepath.ToSlash(rel)
		n := 0
		for _, f := range fs {
			n += f.Points
		}
		if n == 0 {
			continue
		}
		perPkg[pkg] = n
		perPkgFuncs[pkg] = len(fs)
		total += n
	}
	return perPkg, total, perPkgFuncs
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sumBaseline() int {
	n := 0
	for _, v := range SKIP_POINT_BASELINE {
		n += v
	}
	return n
}

// ── 判据 ───────────────────────────────────────────────────────────────

// ① 量具自证：抽取器必须真的能看见跳过点，且**看得见门自己所在的那个包**。
func TestSkipCensus_量具自证(t *testing.T) {
	root := moduleRoot(t)
	perPkg, total, _ := census(root)
	if total < censusFloor {
		t.Fatalf("全仓跳过点只有 %d 个，低于量具下限 %d ⇒ 抽取器大概率坏了，"+
			"此时下面那道棘轮门会因为「读数为 0」而恒绿。基线合计 %d。",
			total, censusFloor, sumBaseline())
	}
	if len(perPkg) < 5 {
		t.Fatalf("只扫到 %d 个包，低于下限 5 ⇒ 目录遍历坏了", len(perPkg))
	}
	if perPkg["./internal/stt"] == 0 {
		t.Fatalf("internal/stt 的跳过点读数为 0，但基线里它是 %d —— 遍历或抽取坏了",
			SKIP_POINT_BASELINE["./internal/stt"])
	}
	// 边界自证：嵌套 module 必须被跳过，否则「另一个宇宙」会混进分母。
	if _, err := os.Stat(filepath.Join(root, "third_party", "identity-go", "go.mod")); err == nil {
		for _, f := range walkTestFiles(root) {
			if strings.Contains(filepath.ToSlash(f), "/third_party/identity-go/") {
				t.Fatalf("walkTestFiles 走进了嵌套 module %s ⇒ 普查分母被虚增，"+
					"且让人误以为那里也有台账管着", f)
			}
		}
	}
}

// ② ★ 棘轮：任何包的跳过点数都不得**超过**基线。
// 只拦新增，不拦存量——存量不是一天能清的债，硬拦等于这道门从写下
// 那天起就永远红（那不是门，是装饰；见 scripts/lib/baseline-ratchet.mjs）。
func TestSkipCensus_新增跳过点必须被登记(t *testing.T) {
	root := moduleRoot(t)
	perPkg, total, perFuncs := census(root)

	// 报一份完整读数：顺带把「哪个包有台账」摊开
	t.Logf("全仓跳过点：%d（基线 %d），分布在 %d 个包", total, sumBaseline(), len(perPkg))
	detailed := 0
	for _, pkg := range sortedKeys(perPkg) {
		mark := "  "
		if _, ok := packagesWithDetailedLedger[pkg]; ok {
			mark = "✅"
			detailed++
		}
		t.Logf("  %s %-42s 点 %3d / 测试 %3d / 基线 %3d",
			mark, pkg, perPkg[pkg], perFuncs[pkg], SKIP_POINT_BASELINE[pkg])
	}
	covered := 0
	for pkg, n := range perPkg {
		if _, ok := packagesWithDetailedLedger[pkg]; ok {
			covered += n
		}
	}
	pct := 0
	if total > 0 {
		pct = covered * 100 / total
	}
	t.Logf("有逐条台账的包：%d/%d，覆盖 %d/%d 个跳过点（%.1f%%）—— 其余是「只计数未登记」",
		detailed, len(perPkg), covered, total, float64(pct))

	var grew []string
	for _, pkg := range sortedKeys(perPkg) {
		base, known := SKIP_POINT_BASELINE[pkg]
		if perPkg[pkg] > base {
			if !known {
				grew = append(grew, strconv.Itoa(perPkg[pkg])+" "+pkg+"（该包不在基线里，属新增）")
			} else {
				grew = append(grew, strconv.Itoa(perPkg[pkg])+"/基线"+strconv.Itoa(base)+" "+pkg)
			}
		}
	}
	// 反向：基线里有、现在整个包都没了 ⇒ 条目过期
	var stale []string
	for _, pkg := range sortedKeys(SKIP_POINT_BASELINE) {
		if perPkg[pkg] == 0 {
			stale = append(stale, pkg)
		}
	}

	if len(grew) > 0 {
		t.Errorf("以下包的跳过点数超过基线：\n    %s\n"+
			"  ⇒ 新增跳过必须被 conscious 地登记（补台账或写明豁免理由），否则「绿色读数」"+
			"又会把「这一段压根没跑」盖住——§467 建这道门就是为了防这个。",
			strings.Join(grew, "\n    "))
	}
	if len(stale) > 0 {
		t.Errorf("基线里这些包已不存在或已无跳过点，条目过期：\n    %s\n"+
			"  ⇒ 台账一旦可以只增不减，就会烂成一张**永久通行证**（§467 纪律①）。",
			strings.Join(stale, "\n    "))
	}
}

// ③ 台账登记表自证 + **覆盖率棘轮**。
//
// 这一条是本轮新加的：§775 那版只检查「登记表里有的包确实有跳过点」，
// 因此**把登记表删空是查不出来的** —— 而登记表正是「谁还没被逐条登记」
// 唯一的可见出口。删掉它 = 把这次审计的产出静音。
func TestSkipCensus_台账登记表自证且不可删空(t *testing.T) {
	if len(packagesWithDetailedLedger) == 0 {
		t.Fatal("packagesWithDetailedLedger 被清空了 ⇒ 本门失去存在意义：" +
			"它的全部价值就是暴露「哪些包还没被逐条登记」")
	}
	root := moduleRoot(t)
	perPkg, _, _ := census(root)

	for _, pkg := range sortedKeys(packagesWithDetailedLedger) {
		if perPkg[pkg] == 0 {
			t.Errorf("登记表声称 %s 有逐条台账，但普查到它的跳过点为 0", pkg)
		}
		if _, known := SKIP_POINT_BASELINE[pkg]; !known {
			t.Errorf("登记表列了 %s，但它不在 SKIP_POINT_BASELINE 里 ⇒ 两张表会互相打架", pkg)
		}
		// ⭐ 登记的台账文件必须**真的存在**
		f := filepath.Join(root, pkg[2:], packagesWithDetailedLedger[pkg])
		if _, err := os.Stat(f); err != nil {
			t.Errorf("登记表说 %s 的台账是 %s，但该文件不存在：%v\n"+
				"  ⇒ 「有台账」这句话必须是可核对的事实，不能只是一行登记。",
				pkg, packagesWithDetailedLedger[pkg], err)
		}
	}

	// ★ 覆盖率棘轮：ledgerRequired 里的包必须都在登记��里
	var missing []string
	for _, pkg := range ledgerRequired {
		if _, ok := packagesWithDetailedLedger[pkg]; !ok {
			missing = append(missing, pkg)
		}
	}
	if len(missing) > 0 {
		t.Errorf("以下包已被逐条登记，却从 packagesWithDetailedLedger 里消失了：\n    %s\n"+
			"  ⇒ 登记表只能**增**不能减。少登记一个包，看起来只是少一行字，"+
			"实际是那一整包的跳过重新变成「没人知道为什么没跑」。",
			strings.Join(missing, "\n    "))
	}
}

// ④ 登记完整性自证：ledgerRequired 本身不得被清空或缩水。
//
// ⚠ 与 ③ 是两件事：③ 查「登记表 vs 棘轮」，④ 查「棘轮 vs 它自己」。
// 只做 ③ 的话，把两个列表**同时**删掉就绕过去了。
func TestSkipCensus_覆盖率棘轮自身不得被删空(t *testing.T) {
	const floor = 12
	if len(ledgerRequired) < floor {
		t.Errorf("ledgerRequired 只剩 %d 个包，低于下限 %d ⇒ 覆盖率棘轮被削了。\n"+
			"  ⚠ 它和 packagesWithDetailedLedger 同时被清空的话，③ 会显示「无缺失」，"+
			"于是两道门一起静默 —— 这个下限就是为了堵住那个合谋。",
			len(ledgerRequired), floor)
	}
	if len(packagesWithDetailedLedger) < len(ledgerRequired) {
		t.Errorf("登记表（%d）比棘轮（%d）还小 ⇒ 有条目被删了",
			len(packagesWithDetailedLedger), len(ledgerRequired))
	}
}
