// Package skipaudit —— 「被跳过的测试」逐条台账的**共享实现**。
//
// 为什么要有这个包（§467 的续集）
// ------------------------------
// §467 给 `internal/stt` 建了第一道逐条台账，抽取器内联在那个文件里。
// 2026-10-17 普查发现全仓 193 个跳过点里只有 20 个（10.4%）被登记，
// 而 `internal/email`（118）与 `internal/server`（37）一个都没有。
//
// 若照 §467 的形状**逐包复制**一份抽取器，就有 11 份几乎相同、且各自
// 独立演化的实现 —— 那正是 §775 记的病（同一件事写多���，迟早只有其中
// 几份还活着）。⇒ 抽取器收编到本包，各包只留**条目表 + 四条判据**。
//
// ── ⭐ 为什么用 go/parser，而不是手写扫描（这是本包最关键的设计）──
//
// 第一版手写了状态机（剥注释 → 涂白字面量 → 花括号配平 → 正则找 t.Skip(）。
// 它在这个仓上立刻暴露了**三个**互相独立的缺陷，全部是「量具把自己算进去」：
//
//	① **漏认 t.Skipf( / t.SkipNow(** —— 只匹配字面量 `t.Skip(`。
//	   实测全仓另有 **57 处 `t.Skipf(`** 被完全无视。
//	   census 报的「193 个跳过点」实际至少 250，低估 23%，
//	   而它一直**稳稳地输出一个数**，没人会怀疑那个数。
//
//	② **字面量里的函数签名被当成真函数** —— 用「剥完注释保留字面量」的串
//	   去找 `func TestXxx(t *testing.T)`，于是
//	   `internal/stt/live_probe_skip_ledger_test.go` 里那段负控样本
//	   （原始字符串里的 `func TestReal(t *testing.T) { t.Skip(...) }`）
//	   被计入普查。该文件实测贡献 **9 个幽灵点**，
//	   而 stt 的棘轮基线正是 20 ⇒ **45% 的额度凭空多出来且永远用不掉**。
//
//	③ **函数体里的 `"t.Skip("` 字面量被当成跳过调用** —— 同一文件写着
//	   `strings.Contains(string(raw), "t.Skip(")`，子串匹配把它算成一个调用。
//	   同样是幽灵点。
//
// 还差点踩第四个：手写状态机在 `internal/livetest/wiring_gate_test.go` 上
// 因为一处字符字面量 `'` 的判定失误，**把后面 4 行真实代码整段当成字面量
// 涂白**，于是该包 4 个跳过点被报成 **0**。
//
// ⚠ 这四个的共同形态，就是本轮审计的主病：
//
//	**失效的东西因为一直在输出而没人怀疑。**
//
//	它们不是「偶尔出错」，是**结构上注定的**——手写扫描器必须同时正确
//	处理注释、三种引号、转义、字面量里的花括号、跨行原始字符串……
//	每一条都得对，漏一条就静默错，而输出依然是个漂亮的数字。
//
// ⇒ 改用 `go/parser`。Go 源码**保证能解析**（否则 `go build` 就红了），
//
//	于是 AST 天然满足全部要求：
//	  - 注释不在 AST 里          ⇒ 纪律②免费成立
//	  - 字符串字面量不是 CallExpr ⇒ ①②③ 三个幽灵点一次性消失
//	  - 不需要花括号配平          ⇒ 第四个缺陷的根因被移除
//	  - 跳过调用按**接收者标识符**认 ⇒ `myt.Skip(` 天然不误收
//
// 代价是每包几毫秒的解析时间；换来的是**判据不再可能恒假**。
//
// 三条纪律的落点（照 §467，每条都来自被它抓到过的事）
// ----------------------------------------------------
//
// ① **双向精确匹配**：漏一条要红，多一条（陈旧）也要红。
//
//	只增不减的台账会烂成一张**永久通行证**。见 Check。
//
// ② **剥注释**：由 AST 免费提供，且有负控钉住（见 skipledger_test.go）。
// ③ **量具先自证**：先断言「确实扫到了 ≥ N 个可跳过函数」，
//
//	否则「台账是空的」与「抽取器坏了」读数一模一样。见 SelfTestOK。
package skipaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Category 是跳过点的类别。
//
// ⚠ **类别不是标签，是取舍**：CatPremiseFailed 那一类
//
//	（守卫是「我自己的前提不成立」而不是「缺外部条件」）
//
//	与其它几类有本质区别，见该常量的说明。
type Category string

const (
	// CatDiagProbe：显式 opt-in 的诊断探针，守卫是 `POCKET_DIAG_*=1` 这类开关。
	// 这类是**有意为之**：诊断工具默认不跑，跑起来可能写生产库。
	// 解锁 = 置上那个具体开关（+ 它点名要的 DSN / 语料目录）。
	CatDiagProbe Category = "diag-probe"

	// CatCredential：需要真实凭据或真实语料（DSN / IMAP 账密 / 真网关）。
	CatCredential Category = "credential"

	// CatHostCapability：需要宿主具备某种能力（中文���体 / 非 root / 测试库）。
	// 注意：这类的解锁条件**在本机通常就无法满足**（例如
	// `os.Geteuid() == 0` ⇒ 权限位不生效 ⇒ 该用例在容器里永远跳过）。
	CatHostCapability Category = "host-capability"

	// CatShortMode：`-short` 模式下跳过（`testing.Short()`）。
	CatShortMode Category = "short-mode"

	// CatFixtureRegen：需要显式开关才能**重新生成**夹具/基线文件。
	// 平时跑的是已提交的夹具，跳过的是「重新生成」这一步。
	CatFixtureRegen Category = "fixture-regen"

	// CatPremiseFailed：⚠ **守卫是「我自己的前提不成立」**。
	//
	// 例：`if !ok { t.Skip("fallback path needs the keyword gate to pass") }`、
	// `if err == nil { t.Skip("fixture happened to decode; nothing to assert") }`。
	//
	// 这类与其它所有类别**性质不同**，必须单独记数：
	//
	//	其它类别的跳过，代价是「这段断言暂时没跑」；
	//	这一类的跳过，代价是「**这段断言永远不会失败**」——
	//	前提一旦不成立，测试自己退出门外，绿色读数与「跑过并通过」
	//	完全无法区分。
	//
	// 有几条甚至在 skip 消息里自认了（"判据要重新设计"、
	// "语义已变，请更新本用例"）—— 那说明作者当时就知道它已经空转。
	//
	// ⚠ 本类**不删测试**（删了会丢掉意图），但它们在 Check() 的统计里
	//	被单独报出，避免淹没在一百多条台账的墙里。
	CatPremiseFailed Category = "premise-failed"
)

// Entry 是台账的一条。
//
// ⛔ Unlock **绝不能**写「配置一下」这类空话 —— §464 的教训：
// 写清**实测的 env 键名**与范围，否则下一个会话会把「跳过」
// 重新登记成「没查」，而校验器看不出区别。
type Entry struct {
	Test   string
	Cat    Category
	Reason string
	Unlock []string
	// Points 是该函数体内的跳过调用次数（由 Register 自动填）。
	// ⛔ 不要手写：它只用于与 census 的点数交叉校验，
	// 手写就会变成「我以为它是几次」。
	// 0 表示未知，pointsOf 会按 1 计。
	Points int
}

// E 是一条台账的简写构造器。
func E(test string, cat Category, reason string, unlock ...string) Entry {
	return Entry{Test: test, Cat: cat, Reason: reason, Unlock: unlock}
}

// Register 从扫描结果批量补上 Points。
//
// ⚠ 它**不**生成 Reason/Unlock —— 那两栏必须手写。
// 用途仅限：把 Points 这个机械字段补上，省得漏。
func Register(e []Entry, dir string, exempt map[string]bool) ([]Entry, error) {
	fs, err := ScanDir(dir, exempt)
	if err != nil {
		return nil, err
	}
	m := map[string]int{}
	for _, f := range fs {
		m[f.Name] = f.Points
	}
	out := make([]Entry, len(e))
	copy(out, e)
	for i := range out {
		out[i].Points = m[out[i].Test]
	}
	return out, nil
}

// Ledger 是「一个包的台账」。
type Ledger struct {
	// Pkg 是包路径（相对 module root），形如 "./internal/email"。
	// 只用于报错信息与自证，**不含行号**（行号会随改一行代码全线漂移）。
	Pkg string
	// Entries 是全部允许被跳过的测试。
	Entries []Entry
	// MinFuncs 是量具下限：抽取器若坏掉，读数会掉到接近 0。
	// 纪律③。取值应约为 Entries 长度的 2/3 ~ 1/1。
	MinFuncs int
}

// Result 是一次核对的结果。
type Result struct {
	// Unregistered：代码里在跳过、但台账里没有（漏登记 ⇒ 门转红）。
	Unregistered []string
	// Stale：台账里有、但代码里已经找不到它的跳过调用（陈旧 ⇒ 门转红）。
	Stale []string
	// Empty：reason 或 unlock 缺失/为空（空话解锁不了任何东西）。
	Empty []string
	// ByCat：台账登记的条目按类别计数。
	ByCat map[Category]int
	// Found：实际扫描到的跳过函数总数。
	Found int
	// Points：实际扫描到的跳过调用总次数（与 census 同口径）。
	Points int
	// LedgeredPoints：台账登记的函数体跳过调用次数之和。
	// 用于与 census 的点数交叉校验。
	LedgeredPoints int
}

// OK 表示全绿。
func (r Result) OK() bool {
	return len(r.Unregistered) == 0 && len(r.Stale) == 0 && len(r.Empty) == 0
}

// String 给失败信息用。
func (r Result) String() string {
	var b strings.Builder
	if len(r.Unregistered) > 0 {
		b.WriteString("\n【漏登记】以下测试在跳过，但不在台账里：\n")
		for _, n := range r.Unregistered {
			b.WriteString("    - " + n + "\n")
		}
		b.WriteString("  ⇒ 它需要什么才能跑？请补进 Entries（含 Cat/Reason/Unlock）。\n")
	}
	if len(r.Stale) > 0 {
		b.WriteString("\n【陈旧】以下台账条目在源码里已找不到对应的跳过调用：\n")
		for _, n := range r.Stale {
			b.WriteString("    - " + n + "\n")
		}
		b.WriteString("  ⇒ 要么测试被删/改名，要么它不再跳过 —— 两种都要更新台账。\n")
		b.WriteString("  ⚠ 台账只增不减就会烂成一张永久通行证（§467 纪律①）。\n")
	}
	if len(r.Empty) > 0 {
		b.WriteString("\n【空话】以下条目缺 reason 或 unlock：\n")
		for _, n := range r.Empty {
			b.WriteString("    - " + n + "\n")
		}
	}
	return b.String()
}

// ── 定位 ───────────────────────────────────────────────────────────────

// DirOf 从当前工作目录向上找 go.mod，再拼出子包目录，返回绝对路径。
//
// ⚠ 为什么**不能**写死 "../../internal/xxx"：
//
//	testaudit 的 capabilities-stub-gate 文件头记了同型事故 ——
//	那道门先后两次算错级数（一次指向 frontend/src，一次 frontend/src/lib），
//	读成空目录 ⇒ **判据恒真通过**。
//	用 go.mod 锚定则移动文件、改包名都不会错。
func DirOf(rel string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if rel == "" {
				return dir, nil
			}
			return filepath.Join(dir, rel), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("从工作目录向上找不到 go.mod，无法定位 %q", rel)
}

// ── 抽取器 ─────────────────────────────────────────────────────────────

// FuncSkip 描述一个「函数体内含跳过调用」的测试。
type FuncSkip struct {
	Name   string
	Points int
}

// ScanDir 扫描 dir 下的 *_test.go，返回「函数体内含跳过调用」的测试。
//
// exempt 是**文件名基名**集合，这些文件被跳过。
// ⛔ exempt 必须由调用方**自证**：豁免一旦能被悄悄扩大，台账就不再是台账。
func ScanDir(dir string, exempt map[string]bool) ([]FuncSkip, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []FuncSkip
	seen := map[string]bool{}
	for _, f := range files {
		if exempt[filepath.Base(f)] {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		// ⚠ 解析失败必须**报错**，不能跳过这个文件。
		//   静默 continue 的后果是：一整个文件的跳过点从普查里消失，
		//   而门照样绿 —— 这正是本轮反复抓到的那个形态。
		file, err := parser.ParseFile(fset, f, raw, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败（会让普查悄悄少算一整个文件）: %w", filepath.Base(f), err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			if !strings.HasPrefix(fd.Name.Name, "Test") {
				continue
			}
			recv := testingVarName(fd)
			if recv == "" {
				continue
			}
			n := countSkipCalls(fd.Body, recv)
			if n == 0 || seen[fd.Name.Name] {
				continue
			}
			seen[fd.Name.Name] = true
			out = append(out, FuncSkip{Name: fd.Name.Name, Points: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ScanDirPoints 是 census 口径：**跳过点数**（同一函数里 N 个跳过调用算 N）。
func ScanDirPoints(dir string, exempt map[string]bool) (int, error) {
	fs, err := ScanDir(dir, exempt)
	if err != nil {
		return 0, err
	}
	t := 0
	for _, f := range fs {
		t += f.Points
	}
	return t, nil
}

// testingVarName 返回测试函数的 testing 参数名。
//
// ⚠ 不写死 "t"：仓库里存在 `func TestXxx(tt *testing.T)` 这类写法，
//
//	写死会让那些文件的跳过点**全部消失**且无人察觉（恒假）。
func testingVarName(fd *ast.FuncDecl) string {
	if fd.Type.Params == nil {
		return ""
	}
	for _, p := range fd.Type.Params.List {
		if star, ok := p.Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "testing" && sel.Sel.Name == "T" {
					if len(p.Names) > 0 {
						return p.Names[0].Name
					}
				}
			}
		}
	}
	return ""
}

// isSkipCall 判断一个调用是不是「跳过测试」。
//
// ⚠ 认全三种写法：`t.Skip(` / `t.Skipf(` / `t.SkipNow(`。
//
//	§467 的 census 只认第一种，全仓另有 57 处 `t.Skipf(` 被无视，
//	「193 个跳过点」实际至少 250 个（低估 23%）。
//	而它一直稳稳输出那个数 —— 失效的东西因为一直在输出而没人怀疑。
//
// ⚠ 接收者按 testingVarName 给出的名字比对，天然排除 `myt.Skip(`
//
//	这类子串误收。
func isSkipCall(call *ast.CallExpr, recv string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Skip", "Skipf", "SkipNow":
	default:
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == recv
}

func countSkipCalls(body *ast.BlockStmt, recv string) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && isSkipCall(call, recv) {
			n++
		}
		return true
	})
	return n
}

// ── 核对 ───────────────────────────────────────────────────────────────

// Check 执行纪律①③：双向精确匹配 + 量具自证 + 空话检查。
//
// 注意：Check **不**自己 t.Errorf —— 它返回结构化结果，
// 让调用方按自己的措辞报红。这样本包不依赖 testing，
// 抽取器本身也能被单测覆盖（纪律③对抽取器同样适用）。
func Check(l Ledger, dir string, exempt map[string]bool) (Result, error) {
	fs, err := ScanDir(dir, exempt)
	if err != nil {
		return Result{}, err
	}
	res := Result{ByCat: map[Category]int{}, Found: len(fs)}
	actual := map[string]bool{}
	for _, f := range fs {
		actual[f.Name] = true
		res.Points += f.Points
	}

	idx := map[string]Entry{}
	for _, e := range l.Entries {
		idx[e.Test] = e
		res.LedgeredPoints += pointsOf(e)
		res.ByCat[e.Cat]++
	}

	for _, f := range fs {
		if _, ok := idx[f.Name]; !ok {
			res.Unregistered = append(res.Unregistered, f.Name)
		}
	}
	for _, e := range l.Entries {
		if !actual[e.Test] {
			res.Stale = append(res.Stale, e.Test)
		}
		if strings.TrimSpace(e.Reason) == "" || len(e.Unlock) == 0 {
			res.Empty = append(res.Empty, e.Test)
		}
	}
	sort.Strings(res.Unregistered)
	sort.Strings(res.Stale)
	sort.Strings(res.Empty)
	return res, nil
}

// pointsOf 从条目自身附带的 Points 字段取点数；未填按 1 计。
func pointsOf(e Entry) int {
	if e.Points > 0 {
		return e.Points
	}
	return 1
}

// SelfTestOK 是纪律③的判定：量具读数是否可信。
func SelfTestOK(l Ledger, res Result) error {
	if res.Found < l.MinFuncs {
		return fmt.Errorf("量具失效：%s 只扫到 %d 个可跳过的测试（台账登记 %d 条，下限 %d）\n"+
			"  ⚠ 这**不是**「现在没有东西被跳过」—— 先怀疑抽取器，再怀疑代码。",
			l.Pkg, res.Found, len(l.Entries), l.MinFuncs)
	}
	if len(l.Entries) == 0 {
		return fmt.Errorf("%s 的台账是空的", l.Pkg)
	}
	return nil
}
