// skip_census_test.go —— 全仓「被跳过的测试」普查 + 逐条台账覆盖率棘轮。
//
// ── 这道门的历史（它自己被同一个形态击穿过两次）──────────────────────────
//
// §467 建了 `internal/stt/live_probe_skip_ledger_test.go`，开篇写的是
// 「让被跳过的测试**可见**」——**全仓级**的措辞，但实现是
// `filepath.Glob("*.go")`，**没有 `../`** ⇒ 实际只覆盖 `internal/stt`。
//
// 2026-10-17 普查轮把它扩到全仓（当时的读数：193 点 / 11 包，
// 有逐条台账的只有 stt，占 10.4%），并在补登过程中发现**普查本身有三处量具缺陷**
// —— 全部是「量具把自己的噪声当成读数」，而它一直稳稳输出一个漂亮的数字：
//
//	① **只认 `t.Skip(`**：全仓另有 57 处 `t.Skipf(` 被完全无视。
//	② **字面量里的假函数被当成真函数**：`internal/stt` 那份台账文件里的
//	   负控样本（原始字符串中的 `func TestReal(t *testing.T) { t.Skip(...) }`）
//	   被计入普查，该文件贡献 **9 个幽灵点**，stt 基线 20 里有 9 个是自指。
//	③ **函数体里的 `"t.Skip("` 字面量被当成调用**：同一文件写着
//	   `strings.Contains(string(raw), "t.Skip(")`，子串匹配把它算成一个跳过点。
//
// ⚠⚠ **2026-10-21：这道门被上游合并静默回退过一次 —— 而门禁当时是绿的。**
//
//	上游的 5 个提交（28c6acf4 / 99442d20 / 0fdda422 / 94f5fae0 / 4119c6d6）
//	把本文件整体覆盖回**手写扫描器**版（正则找 `func TestXxx` + 配平花括号 +
//	数文本里的 `t.Skip(`）。⇒ 那版扫描器把 `internal/skipaudit` 自己负控样本里的
//	**22 个字符串字面量**数成了跳过点（基线里 `./internal/skipaudit: 22` 就是它），
//	而 `./internal/email` 被数成 118（真实值 148）。
//	⇒ **门是绿的，读数是错的**：没有任何东西会告诉你「普查已退回到手写扫描器」。
//
//	⇒ 这就是本仓反复记录的那个形态第三次出现：
//	**失效的东西因为一直在输出而没人怀疑。**
//
// ⇒ 现在本门**完全委托** `internal/skipaudit`（go/parser 实现）：
//
//	Go 源码**保证能解析**（否则 `go build` 就红了）⇒ AST 天然满足全部要求：
//	注释不在 AST 里、字符串字面量不是 CallExpr、不需要花括号配平。
//
// ── 口径（2026-10-21 第三轮再改一次）────────────────────────────────────
//
// **跳过点**（site）的定义：一个 `t.Skip*` 调用。
//
// ⚠ 2026-10-21 起口径从「**测试函数体内**的跳过点」改成
// 「**包内全部**跳过点（含 helper 里的）」，因为实测发现：
//
//	运行时真的跳过的测试：**500** 个
//	只看测试函数体时静态看得见的：**157** 个
//	⇒ 缺口 **385** 个测试在运行时跳过，却对普查与台账**完全不可见**
//	（跳过发生在 **helper** 里，而 `t.Skip` 发生在**被调用方**）。
//
// ⇒ 现在本门的读数是「包内去重后的跳过点数」，
//
//	与 `internal/skipaudit` 的台账口径**逐字一致**（同��段代码、同一份豁免）。
//
// ⚠ 两条纪律（照抄 §467 踩过的坑）
//
//	① key 里**绝不能有行号**：`rel:line` 会在改一行代码后全线漂移
//	   （scripts/lib/baseline-ratchet.mjs 的文件头记了同型教训：
//	    z-index-ladder / bottom-chrome 的 ALLOWLIST 就因为带行号而漂）。
//	   ⇒ 本门以**包路径**为 key。
//	② **不跨嵌套 module**：`third_party/identity-go` 是独立 module
//	   （`github.com/kaixuan/identity-go`），主仓 `go test ./...` 根本不会跑它
//	   ⇒ 把它算进主仓分母是**虚增覆盖率**。
package testaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// ── 基线（2026-10-21 实测，语义见下方 census）────────────────────────────
// key = 包路径（相对 module root，'./' 开头）。⛔ 绝不写行号。
//
// ⚠ 口径：**包内去重后的跳过点数**（含 helper 里的）。
//
//	2026-10-17 的基线（211/194 那一版）数的是「测试函数体内的 t.Skip(」，
//	它把 helper 里的跳过点**整类漏掉** ⇒ 基线偏小 ⇒ 棘轮形同虚设：
//	在 helper 里加跳过点，门**不会红**。
//
//	⇒ 本表按新口径重算：全仓 **277** 个跳过点 / 30 个包。（2026-10-30：+1 —— 资源委托的 PG 夹具 authorityDelegationPG）
var SKIP_POINT_BASELINE = map[string]int{
	"./internal/adapter/disk":            2,
	"./internal/agent":                   3,
	"./internal/agentbridge":             1,
	"./internal/auth":                    1,
	"./internal/calendar":                1,
	"./internal/chatagent":               6,
	"./internal/config":                  3,
	"./internal/email":                   148,
	"./internal/finance":                 3,
	"./internal/flashcards":              3,
	"./internal/identity":                1,
	"./internal/learning":                1,
	"./internal/lobster":                 1,
	"./internal/marketplace":             1,
	"./internal/meeting":                 1,
	"./internal/notes":                   7,
	"./internal/notifycenter":            1,
	"./internal/outbound":                1,
	"./internal/pgmigrate":               1,
	"./internal/pgtime":                  1,
	"./internal/quota":                   1,
	"./internal/redclaw":                 1,
	"./internal/rss":                     1,
	"./internal/scheduledtask":           5,
	"./internal/scheduledtask/executors": 2,
	"./internal/server":                  58,
	"./internal/stt":                     15,
	"./internal/task":                    5,
	"./internal/vault":                   1,
	"./internal/wecom":                   1,
}

// censusFloor 是量具自证用的下限：抽取器若坏掉，读数会掉到接近 0。
// 取当前基线的约一半（277 → 130），既能抓住「突然扫不到」也能抓住大规模丢失。
const censusFloor = 130

// packagesWithDetailedLedger 记「哪些包有逐条台账」，值是台账文件名。
//
// ⛔ 这张表**必须**与「有跳过点的包集合」**双向**精确：
//
//	登记表里有而实物没有 ⇒ 登记在骗人；
//	实物有而登记表没有 ⇒ 那包的跳过点没人登记。
//	§467 的登记表是可以被清空而无人发现的 —— 本门两个方向都钉。
var packagesWithDetailedLedger = map[string]string{
	"./internal/adapter/disk":            "skip_ledger_test.go",
	"./internal/agent":                   "skip_ledger_test.go",
	"./internal/agentbridge":             "skip_ledger_test.go",
	"./internal/auth":                    "skip_ledger_test.go",
	"./internal/calendar":                "skip_ledger_test.go",
	"./internal/chatagent":               "skip_ledger_test.go",
	"./internal/config":                  "skip_ledger_test.go",
	"./internal/email":                   "skip_ledger_test.go",
	"./internal/finance":                 "skip_ledger_test.go",
	"./internal/flashcards":              "skip_ledger_test.go",
	"./internal/identity":                "skip_ledger_test.go",
	"./internal/learning":                "skip_ledger_test.go",
	"./internal/lobster":                 "skip_ledger_test.go",
	"./internal/marketplace":             "skip_ledger_test.go",
	"./internal/meeting":                 "skip_ledger_test.go",
	"./internal/notes":                   "skip_ledger_test.go",
	"./internal/notifycenter":            "skip_ledger_test.go",
	"./internal/outbound":                "skip_ledger_test.go",
	"./internal/pgmigrate":               "skip_ledger_test.go",
	"./internal/pgtime":                  "skip_ledger_test.go",
	"./internal/quota":                   "skip_ledger_test.go",
	"./internal/redclaw":                 "skip_ledger_test.go",
	"./internal/rss":                     "skip_ledger_test.go",
	"./internal/scheduledtask":           "skip_ledger_test.go",
	"./internal/scheduledtask/executors": "skip_ledger_test.go",
	"./internal/server":                  "skip_ledger_test.go",
	"./internal/stt":                     "live_probe_skip_ledger_test.go",
	"./internal/task":                    "skip_ledger_test.go",
	"./internal/vault":                   "skip_ledger_test.go",
	"./internal/wecom":                   "skip_ledger_test.go",
}

// ── 工具 ─────────────────────────────────────────────────────────────

// moduleRoot 向上找 go.mod。
// ⚠️ **不能**写死 '../..' —— capabilities-stub-gate 的文件头记了同型：
//
//	那道门先后两次算错级数（一次指向 frontend/src，一次 frontend/src/lib），
//	插件目录读成空 ⇒ 判据恒真通过。用 go.mod 锚定则移动文件也不会错。
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

// census 返回「包路径 → 去重后的跳过点数」与「包路径 → owner 数」。
//
// ⚠ 与 `internal/skipaudit` 的台账口径**同源**（同一份 ScanModule），
//
//	否则普查与台账会各说各话 —— 那正是「两把尺子量同一件事」的经典事故。
//
//	⚠ 而交叉门（scripts/audit-skip-runtime.mjs）也调同一份 ScanModule
//	⇒ 普查、逐条台账、交叉门**三处同源**。
func census(root string) (map[string]int, map[string]int, error) {
	scan, err := skipaudit.ScanModule(root)
	if err != nil {
		return nil, nil, err
	}
	return scan.Sites(), scan.OwnerCounts(), nil
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── 判据 ─────────────────────────────────────────────────────────────

// ① 量具自证：抽取器必须真的能看见东西。
// 这一条不是走过场——§467 的教训是「台账是空的」与「抽取器坏了」读数一样。
//
// ⚠ 2026-10-21 追加了两条**针对回退事故**的自证：
//
//	· 读数必须 ≥ censusFloor（回退成手写扫描器时读数会掉，但不一定掉到 floor 以下
//	  ⇒ 所以还要下一条「点总数对账」）；
//	· 普查到的包数必须 ≥ 基线里的包数（回退版会把 30 个包数成 11 个）。
func TestSkipCensus_量具自证(t *testing.T) {
	root := moduleRoot(t)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("moduleRoot 定位失败: %v", err)
	}
	perPkg, owners, err := census(root)
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}
	total := 0
	for _, n := range perPkg {
		total += n
	}
	if total < censusFloor {
		t.Fatalf("全仓跳过点只有 %d 个，低于量具下限 %d ⇒ 抽取器大概率坏了，"+
			"此时下面那道棘轮门会因为「读数为 0」而恒绿。实测曾有 %d 个。",
			total, censusFloor, sumBaseline())
	}
	if len(perPkg) < len(SKIP_POINT_BASELINE) {
		t.Fatalf("只扫到 %d 个有跳过点的包，基线里有 %d 个 ⇒ 遍历或抽取坏了\n"+
			"  ⚠ 2026-10-21 上游曾把这道门回退成手写扫描器：它只认测试函数体里的 t.Skip(，"+
			"于是 30 个包被数成 11 个，而门**照样是绿的**。",
			len(perPkg), len(SKIP_POINT_BASELINE))
	}
	// stt 是 §467 的第一份台账，必须被扫到（它是最容易因包名写错而消失的那个）
	if perPkg["./internal/stt"] == 0 {
		t.Fatalf("internal/stt 的跳过点读数为 0，但基线里它是 %d —— 遍历或抽取坏了",
			SKIP_POINT_BASELINE["./internal/stt"])
	}
	// email 是最大的一处（148 点 / 96 个 owner），它消失 ⇒ 抽取器一定坏了
	if perPkg["./internal/email"] != SKIP_POINT_BASELINE["./internal/email"] {
		t.Fatalf("internal/email 的跳过点读数是 %d，基线是 %d —— 抽取器与基线口径不一致",
			perPkg["./internal/email"], SKIP_POINT_BASELINE["./internal/email"])
	}
	// ⚠ 点总数对账：把各包读数加起来，必须与基线之和**逐字相等**。
	//   回退成手写扫描器时，email 会被数成 118（真实 148）⇒ 这一条立刻红。
	if total != sumBaseline() {
		t.Errorf("全仓跳过点合计 %d ≠ 基线合计 %d ⇒ 口径漂了\n"+
			"  ⚠ 2026-10-21 上游回退事件里，手写扫描器把 internal/skipaudit 自己的负控样本\n"+
			"    数成了 22 个跳过点、把 internal/email 数少了 30 个 —— 而门当时是绿的。",
			total, sumBaseline())
	}
	t.Logf("全仓跳过点：%d（基线 %d），分布在 %d 个包；owner 合计 %d",
		total, sumBaseline(), len(perPkg), sumOwners(owners))
}

func sumBaseline() int {
	n := 0
	for _, v := range SKIP_POINT_BASELINE {
		n += v
	}
	return n
}

func sumOwners(owners map[string]int) int {
	n := 0
	for _, v := range owners {
		n += v
	}
	return n
}

// ② ★ 棘轮：任何包的跳过点数都不得**超过**基线。
//
// ⚠ 口径变了以后（2026-10-21），这条门**比之前紧得多**：
//
//	旧口径（只看测试函数体）下，在 helper 里加一个 `t.Skip(` 是**不会**红的 ——
//	因为普查根本看不见 helper。新口径下它是**看得见的** ⇒ 棘轮真的开始拦了。
func TestSkipCensus_新增跳过点必须被登记(t *testing.T) {
	root := moduleRoot(t)
	perPkg, owners, err := census(root)
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}

	detailed := 0
	covered := 0
	for _, pkg := range sortedKeys(perPkg) {
		mark := "  "
		if _, ok := packagesWithDetailedLedger[pkg]; ok {
			mark = "✅"
			detailed++
			covered += perPkg[pkg]
		}
		t.Logf("  %s %-40s %3d 点 / %3d owner / 基线 %3d",
			mark, pkg, perPkg[pkg], owners[pkg], SKIP_POINT_BASELINE[pkg])
	}
	pct := 0
	total := sumBaseline()
	if total > 0 {
		pct = covered * 100 / total
	}
	t.Logf("有逐条台账的包：%d/%d，覆盖 %d/%d 个跳过点（%.1f%%）",
		detailed, len(perPkg), covered, total, float64(pct))

	var grew []string
	for _, pkg := range sortedKeys(perPkg) {
		base, known := SKIP_POINT_BASELINE[pkg]
		if perPkg[pkg] > base {
			if !known {
				grew = append(grew, fmt.Sprintf("%d %s（该包不在基线里，属新增）", perPkg[pkg], pkg))
			} else {
				grew = append(grew, fmt.Sprintf("%d/基线%d %s", perPkg[pkg], base, pkg))
			}
		}
	}
	// 反向：基线里有、现在整个包都没了 ⇒ 条目过期，台账会烂成永久通行证（§467 纪律①）
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

// ③ 台账登记表自证：**双向**精确匹配。
//
//	§467 的登记表是可以被清空而无人发现的（那正是「全仓级措辞、十分之一覆盖」的病）。
//	⇒ 这里两个方向都钉：
//	  · 登记表里有、实物里没有（登记在骗人，或台账过期）；
//	  · 实物里有、登记表里没有（那包的跳过点**没人登记**）。
func TestSkipCensus_台账登记表自证(t *testing.T) {
	root := moduleRoot(t)
	perPkg, _, err := census(root)
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}
	if len(packagesWithDetailedLedger) == 0 {
		t.Fatal("packagesWithDetailedLedger 被清空了 ⇒ 本门失去存在意义")
	}
	// 方向一：登记表 ⊆ 实物，且登记的文件必须真的存在
	for _, pkg := range sortedKeys(packagesWithDetailedLedger) {
		if perPkg[pkg] == 0 {
			t.Errorf("登记表声称 %s 有逐条台账，但普查到它的跳过点为 0", pkg)
		}
		if _, known := SKIP_POINT_BASELINE[pkg]; !known {
			t.Errorf("登记表列了 %s，但它不在 SKIP_POINT_BASELINE 里 ⇒ 两张表会互相打架", pkg)
		}
		rel := strings.TrimPrefix(pkg, "./")
		file := filepath.Join(root, filepath.FromSlash(rel), packagesWithDetailedLedger[pkg])
		if _, err := os.Stat(file); err != nil {
			t.Errorf("登记表声称 %s 的台账是 %s，但该文件不存在：%v", pkg, packagesWithDetailedLedger[pkg], err)
		}
	}
	// 方向二：实物 ⊆ 登记表（**新加的**）
	for _, pkg := range sortedKeys(perPkg) {
		if _, ok := packagesWithDetailedLedger[pkg]; !ok {
			t.Errorf("%s 有 %d 个跳过点，却没有逐条台账 ⇒ 这些跳过点没人登记\n"+
				"  ⇒ 请为该包建 skip_ledger_test.go（照 internal/skipaudit 的台账纪律）并登记到这里。",
				pkg, perPkg[pkg])
		}
	}
}

// ④ ⚠ 嵌套 module 的边界自证。
//
//	`third_party/identity-go` 是独立 module（`github.com/kaixuan/identity-go`），
//	主仓 `go test ./...` **不会**跑它 ⇒ 把它算进主仓分母是虚增覆盖率。
//
//	⚠ 它的已知缺口（**不在本门范围内**，登记在此）：那个 module **没有任何 CI 覆盖**
//	—— 它自己的 2 个跳过点至今没人看。
func TestSkipCensus_嵌套module不计入主仓分母(t *testing.T) {
	root := moduleRoot(t)
	perPkg, _, err := census(root)
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}
	for pkg := range perPkg {
		if strings.Contains(pkg, "third_party/") {
			t.Errorf("普查把嵌套 module 的包 %s 算进了主仓分母 ⇒ 覆盖率虚增", pkg)
		}
	}
	// 而它**确实存在**（否则这条断言会恒真）：那个 module 里确实有跳过点
	nested := filepath.Join(root, "third_party", "identity-go")
	if _, err := os.Stat(filepath.Join(nested, "go.mod")); err != nil {
		// ⚠ 这里**故意用 Fatal 而不是 Skip**：
		//   本文件一旦含跳过调用，普查就会把 testaudit 自己算成一个「有跳过点的包」
		//   ⇒ 而那正是「门禁文件自己制造噪声」的形态。
		//   ⇒ 同时：若哪天 third_party/identity-go 被移出仓里，这条断言就失去被检对象
		//   ⇒ 那时**必须**改这道门（而不是让它静默通过）。
		t.Fatalf("third_party/identity-go 已不在仓里（%v）⇒ 本门的前提不成立，请改这道门", err)
	}
	found := false
	_ = filepath.Walk(nested, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			raw, err := os.ReadFile(p)
			if err == nil && strings.Contains(string(raw), "t.Skip(") {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatal("third_party/identity-go 里已经找不到 t.Skip( ⇒ 本断言失去被检对象（恒真）")
	}
	t.Log("third_party/identity-go 是独立 module，不计入主仓分母；" +
		"⚠ 它的 2 个跳过点仍无 CI 覆盖（遗留，见 handoff）")
}
