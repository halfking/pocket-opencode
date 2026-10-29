// Package skipaudit —— 「被跳过的测试」逐条台账的**共享实现**。
//
// ── 这个包解决的是什么 ────────────────────────────────────────────────
//
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账，
// 2026-10-17 的普查轮把抽取器收编到本包并把存量补登到 100%。
//
// 但**那一轮的抽取器只看测试函数自己的函数体**。于是 2026-10-21 实测发现：
//
//	运行时真的跳过的测试：**500** 个
//	静态普查看得见的：**157** 个
//	⇒ 缺口 **385** 个测试在运行时跳过，却对普查与台账**完全不可见**。
//
// ⚠ 缺口的原因不是「算错了」，是**扫描范围把被检对象排除在外**：
//
//	func newTestStore(t *testing.T) (*Store, func()) {
//	    if dsn == "" {
//	        t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping …")   ← 跳过点在这里
//	    }
//	    …
//	}
//
// 这个 `t.Skip` 在一个 **helper** 里，不在任何 Test 函数体里。
// 而 `t.Skip` 发生在**被调用方** —— 真正跳过的是调用 `newTestStore(t)` 的
// 那 300 多个测试。
//
// ⇒ 本包现在做**传递归因**：一个测试「能不能跳过」= 它的函数体里
//
//	有跳过调用，**或**它（直接或间接）调用了带跳过调用的函数。
//
// ⚠ 方向很重要：**归因是保守的（宁可多算，不可漏算）**。
//
//	「静态说能跳过、这次没跳」无害（可能只是这次环境齐了）；
//	「静态说不能跳过、这次跳了」才是危险的（没人登记 ⇒ 没人知道为什么没跑）。
//	`internal/testaudit` 的交叉门正是按这个不对称方向设计的。
//
// ── 登记单位：owner（拥有跳过点的函数），不是「测试」 ──────────────────
//
// 2026-10-21 第三轮实测发现：现有 12 份台账登记的条目数**恰好**等于
// 各包「测试函数自身拥有跳过点」的个数（email 65/65、server 34/34…），
// 而**helper 拥有的跳过点一个都没登记**。
//
// ⇒ 这不是「台账漏了几条」，是**登记单位选错了**：
//
//	同一个 helper 被 300 多个测试共同触达。若登记单位是「测试」，
//	那 300 个测试就得写 300 条内容相同的条目 —— 而它们的内容
//	（「为什么没跑」「需要什么才能跑」）**只取决于那个 helper**。
//	⇒ 台账会烂成 300 份互相抄写的副本，抄错一份没人发现，
//	而新增一个调用该 helper 的测试就会让门转红（纯粹的无意义抖动）。
//
// ⇒ 本包改用 **owner**：**拥有跳过点的那个函数**。
//
//	· owner 是测试  → 条目名就是测试名（与 §467 的旧台账同形，内容可继承）
//	· owner 是 helper → 条目名就是 helper 名，**一次登记覆盖它触达的所有测试**
//
// 覆盖面的等价性：每个跳过点**有且只有一个 owner**，
//
//	所以「每个 owner 都登记了」⇒「每个跳过点都被登记了」
//	⇒「运行时跳过的每一条都有台账可查」。
//	Check 里另有一道显式的「能跳过的测试必须追得到已登记的 owner」断言，
//	把这条推理钉在代码里，而不是留在注释里。
//
// ── 为什么用 go/parser，而不是手写扫描（这是本包最关键的设计）──
//
// 第一版手写了状态机（剥注释 → 涂白字面量 → 配平花括号 → 正则找 t.Skip(）。
// 它在这个仓上立刻暴露了**三个**互相独立的缺陷，全部是「量具把自己算进去」：
//
//	① **漏认 t.Skipf( / t.SkipNow(** —— 只匹配字面量 `t.Skip(`。
//	② **字面量里的假函数被当成真函数** —— 原始字符串里的 `func TestReal(…) { t.Skip(…) }`。
//	③ **函数体里的 `"t.Skip("` 字面量被当成跳过调用**。
//
// 还差点踩第四个：手写状态机在 `internal/livetest/wiring_gate_test.go` 上
// 因为一处字符字面量 `'` 的判定失误，**把后面 4 行真实代码整段当成字面量涂白**。
//
// ⚠ 这四个的共同形态，就是本仓反复记录的那个病：
//
//	**失效的东西因为一直在输出而没人怀疑。**
//
// 它们不是「偶尔出错」，是**结构上注定的**——手写扫描器必须同时正确
// 处理注释、三种引号、转义、字面量里的花括号、跨行原始字符串……
// 每一条都得对，漏一条就静默错，而输出依然是个漂亮的数字。
//
// ⇒ 改用 `go/parser`。Go 源码**保证能解析**（否则 `go build` 就红了），
//
//	于是 AST 天然满足全部要求：
//	  - 注释不在 AST 里          ⇒ 纪律②免费成立
//	  - 字符串字面量不是 CallExpr ⇒ ①②③ 三个幽灵点一次性消失
//	  - 不需要花括号配平          ⇒ 第四个缺陷的根因被移除
//
// 代价是每包几毫秒的解析时间；换来的是**判据不再可能恒假**。
//
// ⚠️ 2026-10-21 又一次实证：上游合并把 `internal/testaudit` 的普查门
// 整体覆盖回手写扫描器版，而**门禁当时是绿的** —— 因为它数的是
// 「文本里的 t.Skip(」而不是「真的跳过点」，把 `internal/skipaudit`
// 自己负控样本里的 22 个字符串字面量也数成了跳过点。
// ⇒ 同一个形态，第三次。
//
// ── 三条纪律的落点（照 §467，每条都来自被它抓到过的事）─────────────────
//
// ① **双向精确匹配**：漏一条要红，多一条（陈旧）也要红。
//
//	只增不减的台账会烂成一张**永久通行证**。见 Check。
//
// ② **剥注释**：由 AST 免费提供，且有负控钉住（见 skipledger_test.go）。
// ③ **量具先自证**：先断言「确实扫到了 ≥ N 个 owner」，
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
	"strconv"
	"strings"
)

// Category 是跳过点的类别。
//
// ⚠ **类别不是标签，是取舍**。每个类别回答的是不同的问题：
// 「这次为什么没跑」和「它永远会不会失败」是两件事。
type Category string

const (
	// CatDiagProbe：显式 opt-in 的诊断探针，守卫是 `POCKET_DIAG_*=1` 这类开关。
	// 这类是**有意为之**：诊断工具默认不跑，跑起来可能写生产库。
	// 解锁 = 置上那个具体开关（+ 它点名要的 DSN / 语料目录）。
	CatDiagProbe Category = "diag-probe"

	// CatCredential：需要真实凭据或真实语料（DSN / IMAP 账密 / 真网关 / 真测试库）。
	// ⚠ 这一类**必须**区分两种：
	//   - 本机没配 → 跳过（正常，等环境）
	//   - **本机配了** → 说明环境是坏的，那是另一个问题，不该藏在这一类里。
	CatCredential Category = "credential"

	// CatHostCapability：需要宿主具备某种能力（中文字体 / 非 root / tzdata）。
	// 注意：这类的解锁条件**在本机通常就无法满足**（例如
	// `os.Geteuid() == 0` ⇒ 权限位不生效 ⇒ 该用例在容器里永远跳过）。
	CatHostCapability Category = "host-capability"

	// CatShortMode：`-short` 模式下跳过（`testing.Short()`）。
	CatShortMode Category = "short-mode"

	// CatFixtureRegen：需要显式开关才能**重新生成**夹具/基线文件。
	// 平时跑的是已提交的夹具，跳过的是「重新生成」这一步。
	CatFixtureRegen Category = "fixture-regen"

	// CatDefensiveGuard：⭐ 守卫是「前提不成立就跳过」，但**该前提当前恒成立**，
	// 所以这条测试**今天是在断言的**，不是空转。
	//
	// 例：`if srv.taskStore != nil { t.Skip("…route-level assertion not applicable") }`
	// —— 而测试服务器构造时传的正是 nil ⇒ 守卫永不触发。
	//
	// ⚠ 它与「真的会跳过」必须分开记，因为两者的风险完全不同：
	//   - 真跳过 ⇒ 这段断言今天没跑（等环境）
	//   - 死守卫 ⇒ 这段断言今天在跑，但**一旦前提变了，它会静默退出门外**，
	//     而绿色读数与「跑过并通过」无法区分。
	//
	// ⇒ 记成这一类，是提醒下一个会话：**这行守卫不是保险，是一颗哑弹**，
	//	前提一旦成立它就会生效，而那时没人会注意到测试不再断言。
	//
	// ⚠ 2026-10-21 实测更正：这个类别**曾经**叫 `CatPremiseFailed`
	//（「前提不成立 ⇒ 永远不会失败」）。那个名字被实测推翻了：
	//	27 条这类守卫**全部没有触发** ⇒ 那些测试今天**是在断言的**，
	//	不是「永远失败」。名字写错 ⇒ 下一个会话会按错的前提去处理它
	//	（去「修好那个永远失败的测试」，而它其实一直是绿的）。
	CatDefensiveGuard Category = "defensive-guard"
)

// Entry 是台账的一条。
//
// ⚠ 登记单位是 **owner**（拥有跳过点的函数），不是「测试」。
//
//	见包注释「登记单位」一节。
//
// ⛔ Unlock **绝不能**写「配置一下」这类空话 —— §464 的教训：
// 写清**实测的 env 键名**与范围，否则下一个会话会把「跳过」
// 重新登记成「没查」，而校验器看不出区别。
type Entry struct {
	// Owner 是拥有跳过点的函数名。
	// 测试 → 测试名；helper → helper 名（方法带接收者类型前缀，如 `Store.setup`）。
	Owner string
	Cat   Category
	// Reason 是「为什么这次没跑」的第一手说明。
	Reason string
	// Unlock 是「要什么才能跑」——必须是**实测的 env 键名**或
	// 「要让哪个条件变成真」的具体描述。
	Unlock []string
	// Points 是该 owner **自身直接拥有**的跳过点数量。
	//
	// ⛔ 必须**手写**，且**没有默认值**：
	//	自动补会让「台账点数 == 实测点数」恒真（2026-10-17 实测踩过）。
	//	因此本包只导出构造器 EP（强制传点数），不导出留空的 E。
	//
	// 本包内各 owner 的跳过点**互不重叠**（每个点有且只有一个 owner），
	// 所以「台账点数之和 == 本包跳过点总数」是一条可对账的等式。
	Points int
}

// EP 是台账条目的**唯一**构造器。
//
// ⚠ Points 必须**手写**：它是「我以为它是几个」，与实测互为交叉校验。
// 自动补会让「台账点数 == 实测点数」恒真（2026-10-17 实测踩过）。
//
// ⚠ 之所以**不**提供留空点数的重载：留一个「可以不写」的构造函数，
// 等于给「忘了写点数」开一个静默的口子 —— 而少了点数，
// 「台账点数之和 == 实测点数」这条对账就少了一块。
func EP(owner string, cat Category, reason string, points int, unlock ...string) Entry {
	return Entry{Owner: owner, Cat: cat, Reason: reason, Unlock: unlock, Points: points}
}

// Ledger 是「一个包的台账」。
type Ledger struct {
	// Pkg 是包路径（相对 module root），形如 "./internal/email"。
	// 只用于报错信息与自证，**不含行号**（行号会随改一行代码全线漂移）。
	Pkg string
	// Entries 是全部允许存在的跳过点 owner。
	Entries []Entry
	// MinFuncs 是量具下限：抽取器若坏掉，读数会掉到接近 0。
	// 纪律③。取值应约为 Entries 长度的 2/3 ~ 1/1。
	MinFuncs int
}

// Result 是一次核对的结果。
type Result struct {
	// Unregistered：owner 有跳过点，但台账里没有（漏登记 ⇒ 门转红）。
	Unregistered []string
	// Stale：台账里有、但那个 owner 已经不再拥有跳过点（陈旧 ⇒ 门转红）。
	Stale []string
	// Empty：reason 或 unlock 缺失/为空（空话解锁不了任何东西）。
	Empty []string
	// PointMismatches：台账手写的 Points 与实测不一致的条目，
	// 以及「台账点数之和 ≠ 本包跳过点总数」的对账失败。
	PointMismatches []string
	// Uncovered：能跳过的测试里，有谁追不到任何已登记的 owner。
	// 按当前模型它恒为空（每个跳过点必属某个 owner）—— 留着它是为了
	// 「模型哪天变了」时立刻出声，而不是安静地少覆盖。
	Uncovered []string
	// ByCat：台账登记的条目按类别计数。
	ByCat map[Category]int
	// Found：实际扫描到的「能跳过」的测试函数总数。
	Found int
	// Owners：实际扫描到的 owner 数（拥有跳过点的函数数）。
	Owners int
	// Points：本包**去重后**的跳过点总数（每个跳过点只有一个 owner ⇒ 不重复）。
	Points int
	// LedgeredPoints：台账登记的 Points 之和。
	LedgeredPoints int
}

// OK 表示全绿。
func (r Result) OK() bool {
	return len(r.Unregistered) == 0 && len(r.Stale) == 0 &&
		len(r.Empty) == 0 && len(r.PointMismatches) == 0 && len(r.Uncovered) == 0
}

// String 给失败信息用。
func (r Result) String() string {
	var b strings.Builder
	if len(r.Unregistered) > 0 {
		b.WriteString("\n【漏登记】以下 owner 有跳过点，但不在台账里：\n")
		for _, n := range r.Unregistered {
			b.WriteString("    - " + n + "\n")
		}
		b.WriteString("  ⇒ 它需要什么才能跑？请补进 Entries（含 Cat/Reason/Points/Unlock）。\n")
	}
	if len(r.Stale) > 0 {
		b.WriteString("\n【陈旧】以下台账条目对应的 owner 已不再拥有跳过点：\n")
		for _, n := range r.Stale {
			b.WriteString("    - " + n + "\n")
		}
		b.WriteString("  ⇒ 要么跳过点被删/挪走，要么它挪进了别的 owner —— 两种都要更新台账。\n")
		b.WriteString("  ⚠ 台账只增不减就会烂成一张永久通行证（§467 纪律①）。\n")
	}
	if len(r.Empty) > 0 {
		b.WriteString("\n【空话】以下条目缺 reason 或 unlock：\n")
		for _, n := range r.Empty {
			b.WriteString("    - " + n + "\n")
		}
	}
	if len(r.PointMismatches) > 0 {
		b.WriteString("\n【点数不符】以下条目手写的 Points 与实测不一致：\n")
		for _, n := range r.PointMismatches {
			b.WriteString("    - " + n + "\n")
		}
		b.WriteString("  ⇒ Points 必须手写：自动补会让这条交叉校验恒真。\n")
	}
	if len(r.Uncovered) > 0 {
		b.WriteString("\n【无主】以下测试能跳过，却追不到任何已登记的 owner：\n")
		for _, n := range r.Uncovered {
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
//	那道门先后两次算错级数，读成空目录 ⇒ **判据恒真通过**。
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

// FuncSkip 描述一个「能跳过」的测试。
type FuncSkip struct {
	Name string
	// Points 是该测试能触达的跳过点数量。
	Points int
	// Via 是它触达跳过点的 owner 名（用于人读：「它为什么能跳过」）。
	Via []string
	// Sites 是它能触达的跳过点 id 列表。
	Sites []int
}

// Owner 是一个「拥有跳过点的函数」。
type Owner struct {
	// Name 是 owner 名：测试名 / helper 名（方法带接收者类型前缀）。
	Name string
	// OwnSites 是它**自身直接拥有**的跳过点 id 列表。
	OwnSites []int
	// Reachable 是它自身 + 经调用链能触达的跳过点总数。
	Reachable int
	// IsTest 表示它是 `_test.go` 里的 Test 函数（go test 会跑的那些）。
	IsTest bool
	// AffectedTests 是能触达它的测试名（仅用于报告与补登参考）。
	AffectedTests []string
}

// funcInfo 是一个包级函数的静态信息。
type funcInfo struct {
	name  string
	sites []int    // 直接的跳过点 id
	calls []string // 它调用的函数名（包级函数或方法名）
}

// Package 是一个包的扫描结果。
type Package struct {
	// Tests 是能跳过的测试。
	Tests []FuncSkip
	// Owners 是拥有跳过点的函数（登记单位）。
	Owners []Owner
	// Sites 是本包去重后的跳过点总数。
	Sites int
	// SiteOwner 把跳过点 id 映射到「直接拥有它的 owner 名」。
	SiteOwner map[int]string
	// SiteMessage 把跳过点 id 映射到它的跳过消息（跳过原因的第一手证据）。
	// ⚠ 写台账的 unlock 时**必须**用这里的真实消息，而不是猜。
	SiteMessage map[int]string
}

// ScanPackage 扫描一个包目录，做**传递归因**。
//
// exempt 是**文件名基名**集合，这些文件被跳过。
// ⛔ exempt 必须由调用方**自证**：豁免一旦能被悄悄扩大，台账就不再是台账。
func ScanPackage(dir string, exempt map[string]bool) (*Package, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	infos := map[string]*funcInfo{}
	order := []string{}
	siteOwner := map[int]string{}
	siteMessage := map[int]string{}
	siteCount := 0
	testNames := map[string]bool{}

	for _, f := range files {
		base := filepath.Base(f)
		if exempt[base] {
			continue
		}
		isTestFile := strings.HasSuffix(base, "_test.go")
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		// ⚠ 解析失败必须**报错**，不能跳过这个文件。
		//   静默 continue 的后果是：一整个文件的跳过点从普查里消失，
		//   而门照样绿 —— 这正是本仓反复抓到的那个形态。
		file, err := parser.ParseFile(fset, f, raw, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败（会让普查悄悄少算一整个文件）: %w", base, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := ownerName(fd)
			// ⚠ 只有 **_test.go 文件里**的 Test 函数才是 go test 会跑的测试。
			//	非测试文件里定义一个 `func TestXxx` 是**不会**被 go test 执行的，
			//	把它算成「能跳过的测试」会虚增台账。
			isTest := isTestFile && fd.Recv == nil && strings.HasPrefix(name, "Test") &&
				testingVarName(fd) != ""
			recv := testingVarName(fd)
			info, ok := infos[name]
			if !ok {
				info = &funcInfo{name: name}
				infos[name] = info
				order = append(order, name)
			}
			// 跳过点
			ast.Inspect(fd.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || recv == "" {
					return true
				}
				if isSkipCall(call, recv) {
					info.sites = append(info.sites, siteCount)
					siteOwner[siteCount] = name
					siteMessage[siteCount] = skipMessage(call)
					siteCount++
				}
				return true
			})
			// 调用的函数名
			ast.Inspect(fd.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					info.calls = append(info.calls, fn.Name)
				case *ast.SelectorExpr:
					// 方法调用 / 跨包调用：记下方法名。
					// ⚠ 跨包调用（别的 package 的 helper）**解析不了**，
					//	那是本抽取器已知的边界，由 testaudit 的交叉门兜底。
					info.calls = append(info.calls, fn.Sel.Name)
				}
				return true
			})
			if isTest {
				testNames[name] = true
			}
		}
	}

	// ── 传递闭包：reachableSites(fn) = 自身 sites ∪ 它调用到的函数的 sites ──
	memo := map[string][]int{}
	var reachable func(name string, seen map[string]bool) []int
	reachable = func(name string, seen map[string]bool) []int {
		if v, ok := memo[name]; ok {
			return v
		}
		if seen[name] {
			return nil // 递归保护
		}
		seen[name] = true
		info := infos[name]
		if info == nil {
			return nil
		}
		set := map[int]bool{}
		for _, s := range info.sites {
			set[s] = true
		}
		for _, c := range info.calls {
			for _, s := range reachable(c, seen) {
				set[s] = true
			}
		}
		out := make([]int, 0, len(set))
		for s := range set {
			out = append(out, s)
		}
		sort.Ints(out)
		memo[name] = out
		return out
	}

	pkg := &Package{SiteOwner: siteOwner, SiteMessage: siteMessage, Sites: siteCount}
	// owner 索引：name → owner（自身点 + 可达点 + 受影响测试）
	ownerIdx := map[string]*Owner{}
	for _, name := range order {
		info := infos[name]
		if len(info.sites) == 0 {
			continue
		}
		own := append([]int(nil), info.sites...)
		sort.Ints(own)
		ownerIdx[name] = &Owner{
			Name:     name,
			OwnSites: own,
			IsTest:   testNames[name],
		}
	}
	for _, name := range order {
		if !testNames[name] {
			continue
		}
		sites := reachable(name, map[string]bool{})
		if len(sites) == 0 {
			continue
		}
		via := map[string]bool{}
		for _, s := range sites {
			owner := siteOwner[s]
			if owner != name && owner != "" {
				via[owner] = true
			}
		}
		// 记录「哪些测试触达了哪些 owner」
		// ⚠ 一个 owner 拥有多个点时，同一个测试会经过多个点被撞见多次 ⇒ 必须去重。
		for _, s := range sites {
			if o, ok := ownerIdx[siteOwner[s]]; ok {
				o.AffectedTests = append(o.AffectedTests, name)
			}
		}
		viaNames := make([]string, 0, len(via))
		for v := range via {
			viaNames = append(viaNames, v)
		}
		sort.Strings(viaNames)
		pkg.Tests = append(pkg.Tests, FuncSkip{Name: name, Points: len(sites), Via: viaNames, Sites: sites})
	}
	// owner 的可达点数
	for name, o := range ownerIdx {
		o.Reachable = len(reachable(name, map[string]bool{}))
	}
	for _, name := range order {
		if o, ok := ownerIdx[name]; ok {
			seen := map[string]bool{}
			uniq := []string{}
			for _, t := range o.AffectedTests {
				if !seen[t] {
					seen[t] = true
					uniq = append(uniq, t)
				}
			}
			sort.Strings(uniq)
			o.AffectedTests = uniq
			pkg.Owners = append(pkg.Owners, *o)
		}
	}
	sort.Slice(pkg.Tests, func(i, j int) bool { return pkg.Tests[i].Name < pkg.Tests[j].Name })
	sort.Slice(pkg.Owners, func(i, j int) bool { return pkg.Owners[i].Name < pkg.Owners[j].Name })
	return pkg, nil
}

// ScanDir 是 ScanPackage 的便捷封装：只返回「能跳过」的测试。
func ScanDir(dir string, exempt map[string]bool) ([]FuncSkip, error) {
	pkg, err := ScanPackage(dir, exempt)
	if err != nil {
		return nil, err
	}
	return pkg.Tests, nil
}

// ScanOwners 是 ScanPackage 的便捷封装：只返回「拥有跳过点的函数」。
func ScanOwners(dir string, exempt map[string]bool) ([]Owner, error) {
	pkg, err := ScanPackage(dir, exempt)
	if err != nil {
		return nil, err
	}
	return pkg.Owners, nil
}

// ── 全仓扫描 ───────────────────────────────────────────────────────────

// ModuleScan 是**整个 module** 的扫描结果。
//
// ⚠ 为什么要收进本包（而不是让普查门与交叉门各写一份）：
//
//	普查门（internal/testaudit）与交叉门（scripts/audit-skip-runtime.mjs）
//	必须量**同一件事**。两把尺子量同一件事 ⇒ 迟早分叉 ⇒
//	「普查说 276、交叉门说 271」时，谁也不知道该信谁。
//	⇒ 单一来源。
type ModuleScan struct {
	// Packages 的 key 是包路径（相对 module root，'./internal/email'）。
	Packages map[string]*Package
	// Root 是 module root。
	Root string
}

// Sites 返回「包路径 → 去重后的跳过点数」。
func (m *ModuleScan) Sites() map[string]int {
	out := map[string]int{}
	for k, p := range m.Packages {
		out[k] = p.Sites
	}
	return out
}

// OwnerCounts 返回「包路径 → owner 数」。
func (m *ModuleScan) OwnerCounts() map[string]int {
	out := map[string]int{}
	for k, p := range m.Packages {
		out[k] = len(p.Owners)
	}
	return out
}

// ScanModule 扫描整个 module，返回「有跳过点的包」。
//
// ⚠ **不跨嵌套 module**：遇到子目录里有 go.mod 就整棵子树跳过。
//
//	`third_party/identity-go` 是独立 module（`github.com/kaixuan/identity-go`），
//	主仓 `go test ./...` **不会**跑它 ⇒ 把它算进主仓分母是**虚增覆盖率**。
//
// ⚠ 而**不是**靠「文件名前缀」过滤：前缀过滤在有人改目录名时会静默失配。
func ScanModule(root string) (*ModuleScan, error) {
	dirs, err := modulePackageDirs(root)
	if err != nil {
		return nil, err
	}
	out := &ModuleScan{Packages: map[string]*Package{}, Root: root}
	for _, d := range dirs {
		pkg, err := ScanPackage(d, nil)
		if err != nil {
			return nil, err
		}
		if len(pkg.Owners) == 0 {
			continue
		}
		rel, err := filepath.Rel(root, d)
		if err != nil {
			return nil, err
		}
		out.Packages["./"+filepath.ToSlash(rel)] = pkg
	}
	return out, nil
}

// modulePackageDirs 收集 module root 下所有含 .go 文件的目录（跳过嵌套 module）。
func modulePackageDirs(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			if p != root {
				if _, e := os.Stat(filepath.Join(p, "go.mod")); e == nil {
					return filepath.SkipDir // 嵌套 module：整棵子树不属于本仓
				}
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	seen := map[string]bool{}
	uniq := []string{}
	for _, d := range out {
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	return uniq, nil
}

// ScanDirPoints 是 census 口径：本包**去重后**的跳过点总数。
//
// ⚠ 与「各测试 Points 之和」**不是**同一个数：
// 一个 helper 里的跳过点会被 300 多个测试共同触达，
// 求和会把同一个点算 300 多次 ⇒ 普查的总数会爆炸。
//
// ⚠ 与「各 owner 自身点数之和」**是**同一个数：
// 每个跳过点有且只有一个 owner ⇒ 两者恒等（台账的点数对账就靠这条等式）。
func ScanDirPoints(dir string, exempt map[string]bool) (int, error) {
	pkg, err := ScanPackage(dir, exempt)
	if err != nil {
		return 0, err
	}
	return pkg.Sites, nil
}

// ownerName 返回 owner 名。
//
// ⚠ 方法要带接收者类型前缀（`Store.setup`），否则同包两个不同类型的
// 同名方法会被合成一个 owner —— 台账条目会指向两个不同的东西，
// 而校验器看不出区别。
func ownerName(fd *ast.FuncDecl) string {
	if fd.Recv == nil {
		return fd.Name.Name
	}
	if len(fd.Recv.List) > 0 {
		t := fd.Recv.List[0].Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return id.Name + "." + fd.Name.Name
		}
	}
	return fd.Name.Name
}

// testingVarName 返回函数的 testing 参数名。
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
// ⚠ 按**前缀**匹配（`Skip` / `Skipf` / `SkipNow` / 未来的 `SkipXxx`），
//
//	而不是白名单。白名单是 §467 那版的做法，而后来的实测证明：
//	**任何新增的写法都会让抽取器再次失明，而它照样稳稳输出一个数。**
//	（§467 只认 `t.Skip(` ⇒ 全仓 34 处 `t.Skipf(` 被无视；
//	  「193 个跳过点」实际至少 227 个。）
//
//	stdlib 的 testing.T 上以 Skip 开头的方法只有 Skip / Skipf / SkipNow，
//	前缀匹配不会误收；而仓内若出现 `t.SkipIf(...)` 这类包装，它**应该**被看见。
//
// ⚠ 接收者按 testingVarName 给出的名字比对，天然排除 `myt.Skip(`
//
//	这类子串误收。
func isSkipCall(call *ast.CallExpr, recv string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if !strings.HasPrefix(sel.Sel.Name, "Skip") {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == recv
}

// skipMessage 取跳过调用的第一条字符串实参（跳过原因的第一手证据）。
//
// ⚠ 取不到就返回空串 —— **不猜**。台账的 reason 写错了不会被校验器发现，
//
//	但那正是「无人知道为什么没跑」的开始。
func skipMessage(call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return v
}

// ── 核对 ───────────────────────────────────────────────────────────────

// Check 执行纪律①③：双向精确匹配 + 量具自证 + 空话检查 + 点数交叉校验。
//
// 注意：Check **不**自己 t.Errorf —— 它返回结构化结果，
// 让调用方按自己的措辞报红。这样本包不依赖 testing，
// 抽取器本身也能被单测覆盖（纪律③对抽取器同样适用）。
func Check(l Ledger, dir string, exempt map[string]bool) (Result, error) {
	pkg, err := ScanPackage(dir, exempt)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		ByCat:  map[Category]int{},
		Found:  len(pkg.Tests),
		Owners: len(pkg.Owners),
		Points: pkg.Sites,
	}
	actual := map[string]int{}
	for _, o := range pkg.Owners {
		actual[o.Name] = len(o.OwnSites)
	}

	idx := map[string]Entry{}
	for _, e := range l.Entries {
		idx[e.Owner] = e
		res.LedgeredPoints += e.Points
		res.ByCat[e.Cat]++
	}

	// 漏登记：有跳过点却没进台账
	for _, o := range pkg.Owners {
		if _, ok := idx[o.Name]; ok {
			continue
		}
		msgs := []string{}
		for _, s := range o.OwnSites {
			msgs = append(msgs, fmt.Sprintf("%q", pkg.SiteMessage[s]))
		}
		res.Unregistered = append(res.Unregistered, fmt.Sprintf(
			"%s（%d 个跳过点，消息：%s）", o.Name, len(o.OwnSites), strings.Join(msgs, " / ")))
	}
	// 陈旧 / 点数不符 / 空话
	for _, e := range l.Entries {
		measured, ok := actual[e.Owner]
		if !ok {
			res.Stale = append(res.Stale, e.Owner)
		} else if e.Points != measured {
			res.PointMismatches = append(res.PointMismatches,
				fmt.Sprintf("%s：台账写 %d，实测 %d", e.Owner, e.Points, measured))
		}
		if strings.TrimSpace(e.Reason) == "" || len(e.Unlock) == 0 {
			res.Empty = append(res.Empty, e.Owner)
		}
	}
	// 对账：台账点数之和 == 本包跳过点总数
	// ⚠ 这一条是**逐条校验的补充**，不是替代：逐条能抓「哪一条错了」，
	//	这一条能抓「有条目整个漏了登记」。
	if res.LedgeredPoints != res.Points {
		res.PointMismatches = append(res.PointMismatches,
			fmt.Sprintf("%s：台账点数之和 %d ≠ 本包跳过点总数 %d", l.Pkg, res.LedgeredPoints, res.Points))
	}
	// 覆盖：每个能跳过的测试都要追得到至少一个已登记的 owner
	for _, t := range pkg.Tests {
		covered := false
		for _, s := range t.Sites {
			if _, ok := idx[pkg.SiteOwner[s]]; ok {
				covered = true
				break
			}
		}
		if !covered {
			res.Uncovered = append(res.Uncovered, t.Name)
		}
	}
	sort.Strings(res.Unregistered)
	sort.Strings(res.Stale)
	sort.Strings(res.Empty)
	sort.Strings(res.PointMismatches)
	sort.Strings(res.Uncovered)
	return res, nil
}

// SelfTestOK 是纪律③的判定：量具读数是否可信。
func SelfTestOK(l Ledger, res Result) error {
	if res.Owners < l.MinFuncs {
		return fmt.Errorf("量具失效：%s 只扫到 %d 个 owner（台账登记 %d 条，下限 %d）\n"+
			"  ⚠ 这**不是**「现在没有东西被跳过」—— 先怀疑抽取器，再怀疑代码。",
			l.Pkg, res.Owners, len(l.Entries), l.MinFuncs)
	}
	if len(l.Entries) == 0 {
		return fmt.Errorf("%s 的台账是空的", l.Pkg)
	}
	return nil
}
