package email

// reminder_unclassified_hint_test.go — 「importance 为空」诊断提示的措辞与接线。
//
// 为什么要单独钉措辞：这段提示是运维排查的入口，写错比不写更糟。
// 原实现只说「未被 AI 分类过（检查 POCKET_KXMEMORY_BASE_URL）」，但 importance
// 在生产里有两条写入路径，只提一条会把人带偏：
//
//	① 账户规则 mark-important（fetcher.go，与 AI 无关）
//	② AI 分类 SetClassificationScoped（kxmemory / LLM provider）
//
// 真实库实测（2026-10-02）：5 个账户 rules 全 NULL + kxmemory 未配 +
// llmbff no provider configured ⇒ 两条路都不通，importance 恒为空。
// 此时只提示配 kxmemory = 让人在一条不通的路上排查。
//
// 接线护栏用 go/ast 匹配 CallExpr 而不是扫源码文本：源码文本会被注释满足
// （这段注释里就写着 reminderUnclassifiedHint），扫文本的护栏永远绿。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestReminderUnclassifiedHint_NamesBothImportanceWriters(t *testing.T) {
	all, err := reminderHintStringLiterals(t)
	if err != nil {
		t.Fatalf("collect hint literals: %v", err)
	}
	hint := strings.Join(all, "")
	if strings.TrimSpace(hint) == "" {
		t.Fatal("hint string literals are empty; the guard below would pass vacuously")
	}

	// 路径①：账户规则。漏掉它 = 把人送去配 kxmemory，而真实故障是 rules 没配。
	for _, want := range []string{"rules", "mark-important"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint must name the account-rules writer %q; got:\n%s", want, hint)
		}
	}
	// 路径②：AI 分类依赖。
	for _, want := range []string{"POCKET_KXMEMORY_BASE_URL", "AI 分类"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint must name the AI-classification dependency %q; got:\n%s", want, hint)
		}
	}
}

// 提示首段必须带真实计数，否则日志里「多少封没分类」这个最关键的信息会丢。
func TestReminderUnclassifiedHint_LeadsWithCounts(t *testing.T) {
	hint := reminderUnclassifiedHint(47, 47)
	if !strings.HasPrefix(hint, "47/47") {
		t.Errorf("hint must lead with the unclassified/scanned counts, got:\n%s", hint)
	}
	if got := reminderUnclassifiedHint(3, 40); !strings.HasPrefix(got, "3/40") {
		t.Errorf("hint must render partial counts, got:\n%s", got)
	}
}

// 接线护栏：notifyImportant 必须真的调用它。抽成纯函数后最典型的失效是
// 「函数被测得很好、调用点却没接上」，于是提示根本没出现在日志里。
func TestReminderUnclassifiedHint_WiredIntoNotifyImportant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0) // 0 = 不解析注释
	if err != nil {
		t.Fatalf("parse pipeline.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "notifyImportant" {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("notifyImportant not found in pipeline.go")
	}
	calls := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "reminderUnclassifiedHint" {
				calls++
			}
		}
		return true
	})
	if calls == 0 {
		t.Error("notifyImportant never calls reminderUnclassifiedHint: the corrected " +
			"wording would never reach the log, and the pure function is only " +
			"tested in isolation")
	}
}

// reminderHintStringLiterals 收集 reminderUnclassifiedHint 函数体里的字符串字面量。
// 只认 token.STRING 是刻意的：注释不是 token.STRING，所以这段测试文件里
// 对提示文案的讨论不会被护栏当成「实现里写了」。
func reminderHintStringLiterals(t *testing.T) ([]string, error) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "reminderUnclassifiedHint" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(bl.Value)
			if err != nil {
				return true
			}
			out = append(out, s)
			return true
		})
	}
	if out == nil {
		return nil, errNotFound
	}
	return out, nil
}

var errNotFound = errHintNotFound("reminderUnclassifiedHint not found in pipeline.go")

type errHintNotFound string

func (e errHintNotFound) Error() string { return string(e) }
