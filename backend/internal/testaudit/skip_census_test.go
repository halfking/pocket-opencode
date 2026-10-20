// skip_census_test.go —— 全仓「被跳过的测试」普查（§467 同族的范围修正）
//
// 为什么要有这道门
// ----------------
// §467 建了 `internal/stt/live_probe_skip_ledger_test.go`，开篇写的是
// 「让被跳过的测试**可见**」——**全仓级**的措辞。但它的实现是
// `filepath.Glob("*.go")`，**没有 `../`** ⇒ 实际只覆盖 `internal/stt` 一个包。
//
// 这不是措辞不严谨，是**可测量的覆盖缺口**（2026-10-17 实测）：
//
//	全仓 t.Skip( 点：193，分布在 11 个包
//	internal/email        118  ← 没有任何台账
//	internal/server        37  ← 没有任何台账
//	internal/stt           20  ← ✅ 唯一有逐条台账的包（占 10.4%）
//	internal/notes          6 / livetest 4 / scheduledtask 2 / …
//
// 而 `go test ./... -v` 的真实读数是：**PASS 3398 / SKIP 641 / FAIL 0**
// ⇒ 全仓 **15.9% 的 Go 测试从未执行**，而绿色的读数把这件事盖住了。
// 这正是 §467 想防的形态，只不过它自己被同一个形态击中了。
//
// 这道门做什么、不做什么
// ----------------------
// **做**：把「哪些包有逐条台账」和「各包 t.Skip 点数」摊开，并给总数装一个**棘轮**
//
//	—— 新增跳过点必须被 conscious 地登记（或明确豁免），否则门转红。
//	棘轮只拦「变多」，不拦「存量」（存量 193 个，不是一天能清的债）。
//
// **不做**：不逐条登记全部 193 个（那是另一件独立的工作，见 handoff 遗留风险）。
//
//	本门只保证**这 193 不会悄悄变大**，以及「只有 stt 有台账」这个事实
//	不再被一句全仓级的措辞掩盖。
//
// ⚠️ 两条纪律（照抄 §467 踩过的坑）
//
//	① 剥注释，但**保留字符串字面量**：否则本文件的负控样本（原始字符串里的
//	   `t.Skip(`）会被当成真跳过点。⇒ 本文件自身必须被排除。
//	② key 里**绝不能有行号**：`rel:line` 会在改一行代码后全线漂移
//	   （scripts/lib/baseline-ratchet.mjs 的文件头记了同型教训：
//	    z-index-ladder / bottom-chrome 的 ALLOWLIST 就因为带行号而漂）。
//	   ⇒ 本门以**包路径**为 key。
package testaudit

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── 基线（2026-10-17 实测，语义见下方 census）────────────────────────────
// key = 包路径（相对 module root，'.' 开头）。⛔ 绝不写行号。
//
// ⚠ 2026-10-20（§936）：`./internal/server` 由 **37** 改成 **38**。
//
//	这不是**放宽**门禁，而是**补登记一个早就存在、却没被登记的跳过点**：
//	`internal/server/refine_shrinkage_guard_test.go` 里的
//	`t.Skip("这个夹具没触发 participantLeak —— 判据形同虚设，停下来说明")`
//	是 §907（commit 6d90ab00）**有意**加的「前提没建立就停下来说明」守卫，
//	但那个提交**没同步登记基线** ⇒ ⇒ 这道棘轮门从那时起**一直是红的**。
//	⇒ ⇒ 证据：2026-10-20 在**本提交之前**的 HEAD 的一份独立副本上实测，
//	读数与今天**逐字相同**（全仓 194 / 基线 193；server 38 / 基线 37）⇒
//	⇒ **不是 §936 的改动造成的**。
//	⇒ ⇒ 本条的语义是「**已 conscious 地登记**」，门禁的规则（只拦新增、
//	不拦存量）**一个字没改**。
//
// ⚠ 2026-10-20（§940）：**删掉** `./internal/notes` 那一条（6 → 无条目）。
//
//	这不是**放宽**门禁，而是**按本门自己的规则清理一条过期条目**：
//	本门规定「基线里有、现在整个包都没了 ⇒ 条目过期 ⇒ 必须删」（见下方
//	stale 检查）⇒ ⇒ §940 把 notes 包的 PG 夹具接上了**真 PG**（隔离 schema）
//	⇒ ⇒ 那 6 处 `if pool == nil { t.Skip(...) }` 是**已成死分支**的跳过点
//	⇒ ⇒⇒ （`getTestPool` 原本**恒返回 nil** ⇒ ⇒ **每一个** PG 用例都
//	⇒ ⇒⇒ **无条件 SKIP** ⇒ ⇒⇒ 那不是「环境没配」，而是「**这条用例在任何
//	⇒ ⇒⇒ 环境下都不会跑**」）⇒ ⇒⇒ 删掉它们 ⇒ ⇒ 本包的用例**第一次真跑**
//	⇒ ⇒⇒ ⇒ 实测：原 6 条 `TestStore_*` 从「恒 skip」变成「真跑且全绿」。
//
//	⇒ ⇒ **删条目之后，notes 包的棘轮反而更紧**：基线里没有它 ⇒ ⇒ 将来它
//	⇒ ⇒ 再出现任何跳过点，都会被判成「该包不在基线里，属**新增**」⇒ ⇒ ⇒
//	⇒ ⇒ ⇒ 而**不是**悄悄多过去。⇒ ⇒⇒ ⇒ 这条负控由 §940 的变异 **M6** 钉住
//	⇒ ⇒⇒ （往 notes 包的一个测试函数里塞一个 `t.Skip(` ⇒ ⇒ 这道门必须红）。
//
//	⚠⚠ **顺手记下一个本门的盲区**（**本节没有修**，登记在此）：
//	census 只数**测试函数体内**的 `t.Skip(`（`testFuncRe` 匹配 `func TestXxx`）
//	⇒ ⇒ **helper 里的 `t.Skip(` 它看不见** ⇒ ⇒ notes 包现在把「没有 DSN ⇒
//	⇒ ⇒ 跳过」放在 `newNotesTestPool` 这个 **helper** 里 ⇒ ⇒ ⇒ **运行时
//	⇒ ⇒ 仍会 skip**（与 flashcards / calendar 同一房规）⇒ ⇒⇒ 但 census 的
//	⇒ ⇒ 读数是 **0** ⇒ ⇒ ⇒ ⇒ 「notes 包 0 跳过点」**不等于**「notes 包永不
//	⇒ ⇒ ⇒ 跳过」。⇒ ⇒⇒ ⇒ 要不要把 census 扩到 helper 层 ⇒ ⇒ 那会改动
//	⇒ ⇒ ⇒ **别的包**的读数与基线 ⇒ ⇒ ⇒ 属**另一件独立的工作** ⇒ ⇒⇒⇒ 本节
//	⇒ ⇒ ⇒ **不代拍**（也不在本节范围内）。
var SKIP_POINT_BASELINE = map[string]int{
	"./internal/email":                 118,
	"./internal/server":                38,
	"./internal/stt":                   20,
	"./internal/livetest":              4,
	"./internal/scheduledtask":         2,
	"./third_party/identity-go/shadow": 2,
	"./internal/adapter/disk":          1,
	"./internal/agent":                 1,
	"./internal/config":                1,
	"./internal/flashcards":            1,
}

// censusFloor 是量具自证用的下限：抽取器若坏掉，读数会掉到接近 0。
// 取当前基线的约一半（193 → 80），既能抓住「突然扫不到」也能抓住大规模丢失。
const censusFloor = 80

// packagesWithDetailedLedger 记「哪些包有逐条台账」。
// ⛔ 当前只有 internal/stt。这张表**故意很小**，它的价值就是让人一眼看出
// 缺口有多大——写全仓级的措辞、只有十分之一的覆盖面，是 §775 记的那个病。
var packagesWithDetailedLedger = map[string]string{
	"./internal/stt": "live_probe_skip_ledger_test.go（§467，双向精确匹配）",
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

// stripGoComments 剥掉行注释与块注释，**但保留字符串/原始字符串字面量**。
// ⚠️ 保留字面量是刻意的（§464 的教训）：本文件的负控样本就是原始字符串里的
//
//	`t.Skip(`，若连字面量一起抹掉，样本会被当成「抽取器没抓到」而误报。
func stripGoComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch {
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		case src[i] == '`':
			b.WriteByte(src[i])
			i++
			for i < len(src) && src[i] != '`' {
				b.WriteByte(src[i])
				i++
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
		case src[i] == '"' || src[i] == '\'':
			q := src[i]
			b.WriteByte(src[i])
			i++
			for i < len(src) && src[i] != q {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i])
					i++
				}
				b.WriteByte(src[i])
				i++
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

var testFuncRe = regexp.MustCompile(`func\s+(Test[A-Za-z0-9_]*)\s*\(\s*\w+\s+\*testing\.T`)

// walkTestFiles 递归收集 module root 下所有 *_test.go。
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

// census 返回「包路径 → t.Skip( 点数」。
// key 形如 "./internal/email"，**不含行号**（见文件头纪律②）。
func census(root, selfName string) (map[string]int, int, error) {
	perPkg := map[string]int{}
	files := 0
	for _, f := range walkTestFiles(root) {
		if filepath.Base(f) == selfName {
			continue // 本文件自身：负控样本里的 t.Skip( 不是真跳过点
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, err
		}
		files++
		code := stripGoComments(string(raw))
		dir := filepath.Dir(f)
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return nil, 0, err
		}
		pkg := "./" + filepath.ToSlash(rel)

		locs := testFuncRe.FindAllStringIndex(code, -1)
		for _, loc := range locs {
			open := strings.Index(code[loc[1]:], "{")
			if open < 0 {
				continue
			}
			open += loc[1]
			depth, end := 0, -1
			for i := open; i < len(code); i++ {
				switch code[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						end = i
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				continue
			}
			if n := strings.Count(code[open:end], "t.Skip("); n > 0 {
				perPkg[pkg] += n
			}
		}
	}
	total := 0
	for _, n := range perPkg {
		total += n
	}
	return perPkg, total, nil
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

// ① 量具自证：抽取器必须真的能看见 t.Skip(。
// 这一条不是走过场——§467 的教训是「台账是空的」与「抽取器坏了」读数一样。
func TestSkipCensus_量具自证(t *testing.T) {
	if _, err := os.Stat(filepath.Join(moduleRoot(t), "go.mod")); err != nil {
		t.Fatalf("moduleRoot 定位失败: %v", err)
	}
	perPkg, total, err := census(moduleRoot(t), "skip_census_test.go")
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}
	if total < censusFloor {
		t.Fatalf("全仓 t.Skip( 点只有 %d 个，低于量具下限 %d ⇒ 抽取器大概率坏了，"+
			"此时下面那道棘轮门会因为「读数为 0」而恒绿。实测曾有 %d 个。",
			total, censusFloor, sumBaseline())
	}
	if len(perPkg) < 5 {
		t.Fatalf("只扫到 %d 个包，低于下限 5 ⇒ 目录遍历坏了", len(perPkg))
	}
	// stt 是唯一有台账的包，必须被扫到（它是最容易因包名写错而消失的那个）
	if perPkg["./internal/stt"] == 0 {
		t.Fatalf("internal/stt 的 t.Skip( 点读数为 0，但基线里它是 %d —— 遍历或抽取坏了",
			SKIP_POINT_BASELINE["./internal/stt"])
	}
}

func sumBaseline() int {
	n := 0
	for _, v := range SKIP_POINT_BASELINE {
		n += v
	}
	return n
}

// ② ★ 棘轮：任何包的 t.Skip( 点数都不得**超过**基线。
// 只拦新增，不拦存量——存量 193 个不是一天能清的债，硬拦等于这道门从写下
// 那天起就永远红（那不是门，是装饰；见 scripts/lib/baseline-ratchet.mjs）。
func TestSkipCensus_新增跳过点必须被登记(t *testing.T) {
	perPkg, total, err := census(moduleRoot(t), "skip_census_test.go")
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}

	// 报一份完整读数：顺带把「哪个包有台账」摊开，让 §775 的缺口在测试输出里可见
	t.Logf("全仓 t.Skip( 点：%d（基线 %d），分布在 %d 个包", total, sumBaseline(), len(perPkg))
	detailed := 0
	for _, pkg := range sortedKeys(perPkg) {
		mark := "  "
		if _, ok := packagesWithDetailedLedger[pkg]; ok {
			mark = "✅"
			detailed++
		}
		t.Logf("  %s %-40s %3d / 基线 %3d", mark, pkg, perPkg[pkg], SKIP_POINT_BASELINE[pkg])
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
		// ⚠️ 基线里没有的包 ⇒ 是新包，跳过点数必然 ≥1 ⇒ 直接算「新增」
		base, known := SKIP_POINT_BASELINE[pkg]
		if perPkg[pkg] > base {
			if !known {
				grew = append(grew, strconv.Itoa(perPkg[pkg])+" "+pkg+"（该包不在基线里，属新增）")
			} else {
				grew = append(grew, strconv.Itoa(perPkg[pkg])+"/基线"+strconv.Itoa(base)+" "+pkg)
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
		t.Errorf("以下包的 t.Skip( 点数超过基线：\n    %s\n"+
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

// ③ 台账登记表自证：有台账的包必须真的登记了，且登记的都真实存在。
// 没有它，packagesWithDetailedLedger 可以被清空而无人发现——
// 而「谁有台账」正是这道门要暴露的核心信息。
func TestSkipCensus_台账登记表自证(t *testing.T) {
	if len(packagesWithDetailedLedger) == 0 {
		t.Fatal("packagesWithDetailedLedger 被清空了 ⇒ 本门失去存在意义：" +
			"它的全部价值就是暴露「只有 10% 的包有台账」这个事实")
	}
	perPkg, _, err := census(moduleRoot(t), "skip_census_test.go")
	if err != nil {
		t.Fatalf("普查失败: %v", err)
	}
	for _, pkg := range sortedKeys(packagesWithDetailedLedger) {
		if perPkg[pkg] == 0 {
			t.Errorf("登记表声称 %s 有逐条台账，但普查到它的 t.Skip( 点为 0", pkg)
		}
		if _, known := SKIP_POINT_BASELINE[pkg]; !known {
			t.Errorf("登记表列了 %s，但它不在 SKIP_POINT_BASELINE 里 ⇒ 两张表会互相打架", pkg)
		}
	}
}
