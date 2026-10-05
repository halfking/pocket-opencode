package email

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// body_invoice_link_wiring_test.go — q3 接线护栏：判据存在 ≠ 判据被用上。
//
// ## 为什么纯函数测试不够
//
// body_invoice_link_test.go 覆盖的是 bodyHasInvoiceLink 本身。它转红能说明
// 判据写错了，**转绿不能说明调用点真的在用它**。2026-10-02 实测过一次：
// 负控把 fetcher.go 里的 bodyHasInvoiceLink(bs.Bytes) 改成
// bodyHasInvoiceLink([]byte(DeriveSnippet(bs.Bytes, 500))) —— 也就是
// 「改用 snippet 判链接」，这是本功能最核心的约束被破坏的形态 ——
// 结果 body_invoice_link_test.go **全绿**。
//
// 原因很简单：那份测试里没有任何一条断言会去看 fetcher.go。
// 判据在文件里、判据被测绿、判据没接线，三件事可以同时成立。
//
// 这正是本仓库反复栽的那一类（见 email-classify-loop-wiring.test.ts 的
// 文件头：「纯函数测绿不能证明 use-email-inbox.ts 真的在用它」）。所以这里
// 补一条源码级护栏。
//
// ## 判据为什么匹配「实参形态」而不是「出现过这个词」
//
// 必须要求 bodyHasInvoiceLink 的实参是 `bs.Bytes`（原始字节），而不是
// 任何含 DeriveSnippet 的表达式 —— 后者正是那个负控的形态。
// 匹配调用形态（`bodyHasInvoiceLink(<形如 bs.Bytes>)`）而不是「文件里出现过
// bodyHasInvoiceLink」，否则把整行注释掉也能满足。

// wireFuncBodies 解析一个 .go 文件并把所有函数体拼起来，注释已被 AST 剥掉，
// 避免「把接线注释掉也算通过」。
//
// 名字带 wire 前缀是因为同包已有 stripGoComments（pgisolation_guard_test.go，
// 签名是 ([]byte) string）——合并时两侧各自加的测试文件撞名过一次，这里
// 不复用那个名字以免再次撞车。
func wireFuncBodies(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var b strings.Builder
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			// 必须用 printer.Fprint 而不是 ast.Fprint：后者在第三个参数为 nil
			// 时输出的是**带点线的调试格式**（`foo . . . Name: "bar"`），
			// 函数调用被拆成节点而不是 `f(...)`，于是下面那些
			// strings.Contains(..., "bodyHasInvoiceLink(") 永远不成立 ——
			// 护栏会恒红，而报错信息（"没接线"）会指向完全错误的方向。
			// printer.Fprint 打印的是正常的 Go 语法。
			printer.Fprint(&b, fset, fn.Body)
		}
	}
	return b.String()
}

func TestBodyHasInvoiceLink_IsWiredOnRawMIME(t *testing.T) {
	// 1) fetcher.go 里必须有 bodyHasInvoiceLink 的**调用**（不是只有定义），
	//    并且逐个取出实参。
	//
	// 这里必须走 AST 而不是正则。2026-10-03 负控实测：先用
	// `bodyHasInvoiceLink\(([^()]*)\)` 收实参，把实参换成
	// `[]byte(DeriveSnippet(raw, 500))` —— **护栏全绿**。
	// 原因很直白：负控那个实参自带括号，`[^()]*` 匹配不上，坏的那个调用点
	// 被静默跳过，只剩下另一个合法调用点通过检查。
	// 「判据扫不到坏形态」和「判据没发现缺陷」在结果上完全一样。
	args := bodyHasInvoiceLinkArgs(t, "fetcher.go")
	if len(args) == 0 {
		t.Fatal("fetcher.go 里没有 bodyHasInvoiceLink 的调用 —— q3 的判据没接线")
	}
	// 调用点数钉死。少一个就是有人把某个调用点搬走了，而那时上面那些
	// 逐个实参的检查会「因为没东西可查」而全绿。
	if len(args) != 2 {
		t.Errorf("fetcher.go 里 bodyHasInvoiceLink 的调用点数 = %d，期望 2"+
			"（BODY[TEXT] 那次、补拉 BODY[] 那次）", len(args))
	}

	// 2) **每一个**调用点的实参都必须是原始 MIME 字节，不是 DeriveSnippet 的结果。
	//
	// 这条是承重的：负控把实参换成 snippet（= href 里的 URL 已被 htmlToText
	// 删掉）时，本条必须转红。
	//
	// 2026-10-03 改动：本条原来只查 fetcher.go 里的**第一个**调用点，且要求
	// 实参字面写成 `bs.Bytes`。补拉整封那次改动把字节变量改名成 raw/whole，
	// 于是它在报「实参不是原始 MIME 字节：bodyHasInvoiceLink(raw)」——
	// 报的是变量名，不是那个真正要守的性质。
	//
	// 改成「逐个调用点 + 实参必须是裸标识符」之后，判据盯的是**性质**
	// 而不是拼写，并且新加的那个调用点自动进入守护范围。
	// 裸标识符这个要求不是放松：bodyHasInvoiceLink 收 []byte，而摘要变量
	// 是 string，想把 snippet 传进来必须显式转换（`[]byte(snippet)`），
	// 那就不是裸标识符了。类型系统已经替我们挡住了那条路。
	for _, arg := range args {
		if strings.Contains(arg, "DeriveSnippet") {
			t.Errorf("bodyHasInvoiceLink 的实参用了 DeriveSnippet 的结果：%s —— "+
				"htmlToText 会把 href 里的 URL 整个删掉，链接判据将恒为 false", arg)
		}
		if !bareIdentRe.MatchString(arg) {
			t.Errorf("bodyHasInvoiceLink 的实参不是裸的原始字节变量：%s —— "+
				"必须是未经 DeriveSnippet 的 []byte", arg)
		}
	}
}

// bodyHasInvoiceLinkArgs 用 AST 取出文件里所有 bodyHasInvoiceLink 调用的实参源码。
//
// 注释已被剥掉（ParseComments 保留注释，但下面只取表达式节点，注释不会混进来）。
func bodyHasInvoiceLinkArgs(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "bodyHasInvoiceLink" {
			return true
		}
		for _, a := range call.Args {
			var b strings.Builder
			if err := printer.Fprint(&b, fset, a); err != nil {
				t.Fatalf("打印实参失败：%v", err)
			}
			out = append(out, b.String())
		}
		return true
	})
	return out
}

// bareIdentRe 匹配一个裸标识符（可选地取 & 后再一个标识符）。
var bareIdentRe = regexp.MustCompile(`^&?[A-Za-z_][A-Za-z0-9_]*$`)

// backfill.go 那条接线同样要守：HasAttachments 必须或上链接判据。
// 少了它，判据算出来了也传不到 Email 结构体上。
func TestInvoiceLinkIsOrEdIntoHasAttachments(t *testing.T) {
	src := wireFuncBodies(t, "backfill.go")
	i := strings.Index(src, "HasAttachments:")
	if i < 0 {
		t.Fatal("backfill.go 里找不到 HasAttachments 的赋值 —— 取件映射被改动了")
	}
	line := src[i:]
	if end := strings.Index(line, "\n"); end > 0 {
		line = line[:end]
	}
	if !strings.Contains(line, "hasInvoiceLink") {
		t.Errorf("HasAttachments 没有或上链接判据：%s —— "+
			"只有 MIME 附件的邮件，📎 仍然不亮", strings.TrimSpace(line))
	}
	if !strings.Contains(line, "bodyStructureHasAttachment") {
		t.Errorf("HasAttachments 丢了原有的 MIME 附件判定：%s", strings.TrimSpace(line))
	}
}
