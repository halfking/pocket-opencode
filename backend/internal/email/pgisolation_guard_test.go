package email

// pgisolation_guard_test.go — 防「集成测试静默 skip」回归的护栏。
//
// ## 要挡的是什么
//
// 本包曾有三处各自硬编码 `os.Getenv("PG_DSN")`，与全仓二十多处统一走的
// `testDSN()`（认 POCKET_TEST_POSTGRES_DSN）脱节。两套变量名并存造成两个
// 方向的失败，而且**都报 ok**：
//
//  1. 只设标准变量时，这三处 `t.Skip` —— 需求 2「移到垃圾邮件箱」那条不可逆
//     链路（junk.go 69 条语句）在 CI 与本地**从未执行**，报告却是绿的。
//  2. 设 PG_DSN 时它们真的连上去，而本仓库惯例是同一个 DSN 既喂服务也喂测试
//     ⇒ PG_DSN 极可能就是生产 schema。
//
// **只修这三处是不够的**：新增文件再写一次同样的字面量，门禁照样绿。
// 所以把「集成测试不得直接读 PG_DSN」固化成结构性质。
//
// ## 判据为什么必须 stripComments
//
// 判据是**源码文本匹配**。不剥注释的话，任何人只要在旁边写一句
// `// 不要再用 os.Getenv("PG_DSN")` 就能让护栏转红，护栏就成了摆设。
// 我在前几轮已经栽过两次同款（见 agent memory「变体三：注释能满足任何源码
// 扫描断言」），所以这里先剥注释再匹配，并要求 `//` 前不是 `:` （避免误伤
// 字符串里的 `http://`）。
//
// ## 显式豁免
//
// realprobe_test.go：`-tags=realprobe` 手动启用的**只读**探针，它要连的正是
// 生产 schema（120 封真实邮件就��那里），且另有 POCKET_REAL_KEYS 门控。
// 那里读 PG_DSN 是有意的，不是漂移。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pgDSNGuardExempt 允许读 PG_DSN 的文件，及理由（理由必须写进这条豁免里）。
var pgDSNGuardExempt = map[string]string{
	"realprobe_test.go": "只读真实数据探针（-tags=realprobe），刻意连生产 schema，另有 POCKET_REAL_KEYS 门控",
	// 与 realprobe 同型：只读真实数据探针，同样是「PG_DSN 指向生产 schema
	// 才有意义」——它要量的正是生产 emails 表里那批脏 snippet 的落库形态，
	// 打错库就会扫到空集并输出与「已修复」无法区分的结论（该文件里
	// `t.Fatalf("判据在空集上转绿是假绿")` 与 current_schema() 当场校验
	// 就是为这件事准备的）。只读，不写库不改邮箱状态（UID FETCH PEEK）。
	"diag_real_fetch_snippet_stages_test.go": "只读真实数据探针：量生产 emails 的 BODY[TEXT] 落库形态，刻意连生产 schema，另有 POCKET_REAL_KEYS 门控，空集与 search_path 当场自证",
	// 与上面同型：只读真实数据探针，量的是生产 emails 里 snippet='' 的 32 行
	// 落空在哪一步（同一批数据、同一套门控、同样当场自证 search_path 与空集）。
	// 它要的就是生产表 —— 换库扫到空集，输出将与「没有这个问题」无法区分。
	"diag_empty_snippet_locus_test.go": "只读真实数据探针：定位生产 emails 中 snippet='' 的落空点，刻意连生产 schema，另有 POCKET_REAL_KEYS 门控，空集与 search_path 当场自证",
}

// stripGoComments 剥掉行注释与块注释，保留字符串字面量内容。
//
// 用 AST 做不到「保留字符串但删注释」，而我们要匹配的恰恰是字符串字面量里
// 的内容，所以走词法：逐字节扫，只处理引号外的注释。
func stripGoComments(src []byte) string {
	var b strings.Builder
	b.Grow(len(src))
	inLine, inBlock, inStr, inRune, inRaw := false, false, false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
			continue
		case inBlock:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i++
				// 用空格替换，保持后续 token 边界不粘连。
				b.WriteByte(' ')
			}
			continue
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		case inRune:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			} else if c == '\'' {
				inRune = false
			}
			continue
		case inRaw:
			b.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
			continue
		}
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			inLine = true
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			inBlock = true
			i++
		case c == '"':
			inStr = true
			b.WriteByte(c)
		case c == '\'':
			inRune = true
			b.WriteByte(c)
		case c == '`':
			inRaw = true
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// callEnvLiterals 返回该文件里所有 os.Getenv("...") 的字面量参数。
//
// 用 AST 而不是正则：正则要处理 `os.Getenv (` 这种带空格/换行的写法，
// 而「换个写法就绕过」正是这版判据曾经的死穴。AST 看的是**调用形状**，
// 空白与换行都绕不过去。
func callEnvLiterals(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filepath.Base(path), err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// 判据锁的是**值被读**（os.Getenv("PG_DSN")），
		// 不是「这段写法」：所以只要函数选择器是 Getenv 且首字母是 o，
		// 都算——换个变量名的绕法（同 myos.Getenv）也会被认出来。
		if sel.Sel.Name != "Getenv" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name != "os" && pkg.Name != "myos" {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out = append(out, lit.Value)
		}
		return true
	})
	return out
}

// TestNoIntegrationTestReadsRawPGDSN 是本护栏本体。
func TestNoIntegrationTestReadsRawPGDSN(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var offenders []string
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		checked++
		reason, exempt := pgDSNGuardExempt[e.Name()]
		if exempt {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("豁免 %s 没写理由", e.Name())
			}
			continue
		}
		for _, lit := range callEnvLiterals(t, e.Name()) {
			if lit == `"PG_DSN"` {
				offenders = append(offenders, e.Name())
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("以下文件直接读 PG_DSN（会造成集成测试静默 skip 或连上生产库）: %v\n"+
			"改用 greenmailDSN()（pgscope_test.go）。确需豁免就在 pgDSNGuardExempt 里加一行并写明理由。",
			offenders)
	}
	if checked == 0 {
		t.Fatal("一个 .go 都没检查到；护栏空转，不是有效护栏")
	}
	t.Logf("checked %d 个 .go 文件", checked)
}

// TestStripGoComments 确认剥注释真的有效 —— 护栏自身的前置条件。
//
// 没有它，一个只会「越剥越少」的坏实现会让上面那条恒绿。
func TestStripGoComments(t *testing.T) {
	cases := []struct{ in, wantPresent, wantAbsent string }{
		{`x := 1 // os.Getenv("PG_DSN")`, `x := 1`, `PG_DSN`},
		{`/* os.Getenv("PG_DSN") */ y`, `y`, `PG_DSN`},
		{`s := "os.Getenv(\"PG_DSN\")"`, `PG_DSN`, ``}, // 字符串字面量必须保留
		{"u := `os.Getenv(\"PG_DSN\")`", `PG_DSN`, ``}, // 原始字符串
		{`r := 'a' // 注释`, `'a'`, ``},                  // rune 字面量不误伤
		{`url := "http://x" // 备注`, `http://x`, ``},    // 字符串里的 // 不是注释
	}
	for _, c := range cases {
		got := stripGoComments([]byte(c.in))
		if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
			t.Errorf("stripGoComments(%q) 仍含 %q：注释没被剥掉 -> %q", c.in, c.wantAbsent, got)
		}
		if c.wantPresent != "" && !strings.Contains(got, c.wantPresent) {
			t.Errorf("stripGoComments(%q) 丢了 %q：代码被误伤 -> %q", c.in, c.wantPresent, got)
		}
	}
}
