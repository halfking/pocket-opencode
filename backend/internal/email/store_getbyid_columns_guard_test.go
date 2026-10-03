package email

// store_getbyid_columns_guard_test.go —— 静态护栏：GetEmailByID 与
// GetEmailByIDScoped 的 SQL 必须包含 message_id 与 body_purged。
//
// ## 为什么必须有这条护栏（而 PG 测试不够）
//
// store_getbyid_columns_test.go 打真实 PG store 验证字段真能读回，但它需要
// 活的 PostgreSQL；没设 POCKET_TEST_POSTGRES_DSN 时**整个文件被 skip**。
// 也就是说在无库环境（CI、别人的开发机）里这个回归是**无声通过**的。
// 静态护栏不依赖任何外部服务，是最后一道网。
//
// ## 判据为什么必须锚到 AST 的字符串字面量
//
// 前两版都废了：
//   1. 找 `&x.MessageID` —— 一个都命中不了，因为真实代码是
//      `var messageID sql.NullString; ...Scan(&messageID); e.MessageID = ...`，
//      「DB → 结构体」这一跳经过**局部变量**，字段名级匹配原理上做不到。
//   2. 锚到函数体做 `strings.Contains(源码, "message_id")` —— **被注释骗过**：
//      自己在函数上方写的解释性注释里就含 `message_id`，
//      于是「把列删掉」这个负控仍然全绿。注释满足了对 bug 的检查。
//
// SQL 查询串一定在 `token.STRING` 节点里，所以只收集字符串字面量再找列名，
// 天然免疫注释。列名在 SQL 里是字面文本，不受局部变量中转影响。
//
// 另有一处易错：两处查询都含 `body_path`，不能把「函数体内出现某列名」
// 当判据——跨函数的文本匹配会互相顶包。这里按**函数边界**切分后再查。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// guardRequiredColumns 是这些查询必须带上的列。漏掉任何一个都会让下游拿到的
// 结构体字段恒为零值（守卫分支从不执行，且不产生任何错误信号）。
var guardRequiredColumns = []string{"message_id", "body_purged"}

// guardTargetFuncs 只锁这两个方法。它们的返回值直接喂给消费者：
// GetEmailByID → invoice_harvest.harvestOne → sameEmailMessage（用 MessageID）
// GetEmailByIDScoped → server_email_summary.summarizeBody（用 BodyPurged）
var guardTargetFuncs = []string{"GetEmailByID", "GetEmailByIDScoped"}

func TestGuardGetEmailByIDQueriesSelectRequiredColumns(t *testing.T) {
	src := readStoreSource(t)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse store.go: %v", err)
	}

	found := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !containsString(guardTargetFuncs, fn.Name.Name) {
			continue
		}
		found++
		lit := collectStringLiteralsInBody(fn.Body)
		if strings.TrimSpace(lit) == "" {
			t.Errorf("%s: 函数体内没有任何字符串字面量，判据解析器坏了（静默放行是最坏的失败方式）", fn.Name.Name)
			continue
		}
		for _, col := range guardRequiredColumns {
			if !strings.Contains(lit, col) {
				t.Errorf("%s: SQL 里没有列 %q —— 该字段在结构体里会恒为零值，"+
					"下游守卫分支在生产中从不执行。见 store_getbyid_columns_test.go 的后果说明",
					fn.Name.Name, col)
			}
		}
	}

	if found == 0 {
		t.Fatalf("护栏没匹配到任何目标函数（找到 0 个，应为 %d 个）："+
			"可能函数被改名或移走，护栏会静默放行", len(guardTargetFuncs))
	}
	if found != len(guardTargetFuncs) {
		t.Errorf("只匹配到 %d/%d 个目标函数，护栏覆盖不完整", found, len(guardTargetFuncs))
	}
}

func readStoreSource(t *testing.T) []byte {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve current file")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "store.go"))
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	return b
}

// collectStringLiteralsInBody 收集函数体（含嵌套调用）里所有字符串字面量的
// 拼接结果。刻意**不**收集注释内容——这是本护栏与被注释骗过的前两版的
// 唯一区别。
func collectStringLiteralsInBody(body *ast.BlockStmt) string {
	var out strings.Builder
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		out.WriteString(v)
		out.WriteByte('\n')
		return true
	})
	return out.String()
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
