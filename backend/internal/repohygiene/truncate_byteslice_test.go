package repohygiene

// 跨包源码普查：字符串上的字节切片 s[:N]。
//
// 背景见 §258：三处**落库**字段用 `message[:500]` 这种字节切片截断，
// 而按字节硬切会产出非法 UTF-8，PostgreSQL 直接拒绝整条写入
// （SQLSTATE 22021）——后果是这条记录根本写不进去，不是存成乱码。
// 三处所在的包（internal/email、internal/server）**各自早已有修好的
// truncateStr**，只是调用点绕过了它。
//
// ## 为什么这道门是「普查」而不是「几个断言」
//
// §253 那轮扫的是「按字节截断的 helper」，把「有没有 helper 忘了加回退」
// 当成了全部风险面。2026-10-08 按**行为**重扫（形态 `if len(x)>N { x = x[:N] }`）
// 一次命中 15 处 —— 全部是内联切片，不经过任何 helper，
// 所以「helper 都有回退」这个结论对它们**完全不适用**。
// ⇒ 判别口径必须是**形态**（这段代码干了什么），不是**名字**（它调用了谁）。
//
// ## §260 扩了覆盖面：从「字符串参数」到「字符串变量 + 字符串字段」
//
// §258.9 记下的盲区是局部变量与结构体字段。这一版补上：
//   - 局部变量：从 string 参数**传递赋值**得来的局部变量（做定点传播，
//     支持多跳 `a := p; b := a`），以及 `var x string`；
//   - 结构体字段：接收者类型**可解析**时（参数类型、`var x T`、`x := T{}`）
//     按包内结构体定义判断该字段是不是 string。
//
// ⚠ 覆盖范围的诚实边界（不要过度信任这道门）：
//   - **range 变量**（`for _, s := range []string`）不抓：元素类型要完整类型检查；
//   - **从函数返回值**来的字符串不抓（`t := strings.TrimSpace(s)`、`t := fmt.Sprintf(...)`）；
//   - **接收者类型推导不出**时不抓（`x := newFoo().Name`）；
//   - 跨包的结构体字段不抓（本门按**包**解析，看不到别的包的类型定义）；
//   - 上界是变量的 `s[:n]` 一律不抓——上界是变量时可能本来就是「字符上限」语义；
//   - **测试文件**一律不扫（测试里的切片常是故意的「造一个非法串」）。
//
// 豁免表必须写清理由，且理由要能被复核——「先放着」不算理由。
// 表里写了但代码已改/挪走的条目同样会红：否则它会变成一张永远没人看的废纸。

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
	"testing"
)

// byteSliceAllowlist 已知存在的「字符串上的字节切片」，逐条写明为什么留着。
// key 形如 "internal/email/fetcher.go:1191"（**切片那一行**，不是 if 那一行）。
var byteSliceAllowlist = map[string]string{
	// API key 按域一定是 ASCII（sk- 前缀 + base62），切点不会落在代理对中间。
	// 若将来允许非 ASCII key，这里必须同步改。
	"internal/server/llm_gateway_handler.go:663": "API key 是 ASCII 域，切点不可能落在代理对中间",
	// 外面套了 fmt.Sprintf(\"%q\", s)：2026-10-08 实测 %q 会把非法字节转义成
	// \\xNN，输出恒为合法 UTF-8（validUTF8=true，尾部 \"预预预\\xe9\"），
	// 所以落库的审计 detail 不会被 PG 拒。属于**意外**消毒，不是设计——
	// 谁把 %q 去掉就必须同步改这里。
	"internal/server/server_marketplace.go:87": "%q 把非法字节转义成 \\xNN，输出合法（2026-10-08 实测）",
}

type byteSliceHit struct {
	rel  string
	line int
	fn   string
	expr string
}

// pkgIndex 记录一个包内「哪些结构体的哪些字段是 string」。
type pkgIndex map[string]map[string]bool

// collectByteSlices 解析整个包（非测试文件）并返回该包内所有命中的字节切片。
func collectByteSlices(fset *token.FileSet, dir string) ([]byteSliceHit, error) {
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	idx := pkgIndex{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			collectStructStrings(f, idx)
		}
	}

	var hits []byteSliceHit
	for _, pkg := range pkgs {
		names := make([]string, 0, len(pkg.Files))
		for n := range pkg.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, decl := range pkg.Files[n].Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				hits = append(hits, analyzeFunc(fset, fn, idx)...)
			}
		}
	}
	return hits, nil
}

func collectStructStrings(f *ast.File, idx pkgIndex) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			set := idx[ts.Name.Name]
			if set == nil {
				set = map[string]bool{}
				idx[ts.Name.Name] = set
			}
			for _, fld := range st.Fields.List {
				if !isStringType(fld.Type) {
					continue
				}
				for _, nm := range fld.Names {
					set[nm.Name] = true
				}
			}
		}
	}
}

func isStringType(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "string"
}

// isKnownStringExpr 只认「已经是已知字符串变量」的表达式；不做类型推导。
func isKnownStringExpr(e ast.Expr, sc *funcScope) bool {
	id, ok := e.(*ast.Ident)
	return ok && sc.strings[id.Name]
}

// structTypeName 从类型表达式里取结构体名；取不到（非本包类型/推导不出）返回 ""。
func structTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return structTypeName(t.X)
	}
	return ""
}

// funcScope 记录一个函数里「哪些变量是 string」「哪些变量的类型是哪个结构体」。
type funcScope struct {
	strings map[string]bool
	types   map[string]string
}

func analyzeFunc(fset *token.FileSet, fn *ast.FuncDecl, idx pkgIndex) []byteSliceHit {
	sc := &funcScope{strings: map[string]bool{}, types: map[string]string{}}

	// 种子：参数 / 命名返回值
	seed := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			if isStringType(f.Type) {
				for _, n := range f.Names {
					sc.strings[n.Name] = true
				}
			}
			if tn := structTypeName(f.Type); tn != "" {
				for _, n := range f.Names {
					sc.types[n.Name] = tn
				}
			}
		}
	}
	seed(fn.Type.Params)
	seed(fn.Type.Results)

	// 定点传播：局部赋值 / var 声明。跑满直到不再有新增（有迭代上限防病态）。
	for pass := 0; pass < 8; pass++ {
		before := len(sc.strings) + len(sc.types)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				if len(node.Lhs) != len(node.Rhs) {
					return true
				}
				for i, rhs := range node.Rhs {
					lhs, ok := node.Lhs[i].(*ast.Ident)
					if !ok || lhs.Name == "_" {
						continue
					}
					if id, ok := rhs.(*ast.Ident); ok && sc.strings[id.Name] {
						sc.strings[lhs.Name] = true
					}
					// `t := s + "x"` / `t := "x" + s`：字符串参与的二元运算仍是字符串。
					// 不处理这一支的话，最常见的「给参数加个前后缀再截」会整个漏掉。
					if bin, ok := rhs.(*ast.BinaryExpr); ok {
						if isKnownStringExpr(bin.X, sc) || isKnownStringExpr(bin.Y, sc) {
							sc.strings[lhs.Name] = true
						}
					}
					if cl, ok := rhs.(*ast.CompositeLit); ok {
						if tn := structTypeName(cl.Type); tn != "" {
							sc.types[lhs.Name] = tn
						}
					}
				}
			case *ast.ValueSpec:
				tn := structTypeName(node.Type)
				for _, lhs := range node.Names {
					if isStringType(node.Type) {
						sc.strings[lhs.Name] = true
					}
					if tn != "" {
						sc.types[lhs.Name] = tn
					}
				}
			case *ast.ReturnStmt:
				// 命名返回值被赋值也算（少见，但零成本）
				_ = node
			}
			return true
		})
		if len(sc.strings)+len(sc.types) == before {
			break
		}
	}

	var hits []byteSliceHit
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		se, ok := n.(*ast.SliceExpr)
		if !ok || se.Low != nil || se.Max != nil {
			return true
		}
		lit, ok := se.High.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return true
		}
		if _, err := strconv.Atoi(lit.Value); err != nil {
			return true
		}
		switch x := se.X.(type) {
		case *ast.Ident:
			if !sc.strings[x.Name] {
				return true
			}
			hits = append(hits, byteSliceHit{
				rel: relToRepo(fset.Position(se.Pos()).Filename), line: fset.Position(se.Pos()).Line,
				fn: fn.Name.Name, expr: fmt.Sprintf("%s[:%s]", x.Name, lit.Value),
			})
		case *ast.SelectorExpr:
			// 接收者类型可解析、且该字段在本包里确实是 string，才算命中。
			recv, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			tn, ok := sc.types[recv.Name]
			if !ok {
				return true
			}
			if !idx[tn][x.Sel.Name] {
				return true
			}
			hits = append(hits, byteSliceHit{
				rel: relToRepo(fset.Position(se.Pos()).Filename), line: fset.Position(se.Pos()).Line,
				fn: fn.Name.Name, expr: fmt.Sprintf("%s.%s[:%s]", recv.Name, x.Sel.Name, lit.Value),
			})
		}
		return true
	})
	return hits
}

func relToRepo(path string) string {
	// 只需要稳定的后缀 key，裁到 internal/ 起即可。
	if i := strings.Index(path, string(filepath.Separator)+"internal"+string(filepath.Separator)); i >= 0 {
		return strings.TrimPrefix(path[i+1:], string(filepath.Separator))
	}
	return filepath.Base(path)
}

func TestNoByteSliceOnStringParam(t *testing.T) {
	root := repoRoot(t)
	backend := filepath.Join(root, "backend")
	if _, err := os.Stat(backend); err != nil {
		t.Fatalf("找不到 backend 目录：%v", err)
	}

	var hits []byteSliceHit
	fset := token.NewFileSet()
	err := filepath.Walk(backend, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if !info.IsDir() {
			return nil
		}
		base := info.Name()
		if base == ".git" || base == "node_modules" || base == "vendor" || base == "testdata" ||
			strings.HasPrefix(base, ".") && base != "." {
			return filepath.SkipDir
		}
		h, err := collectByteSlices(fset, path)
		if err != nil {
			// 解析失败不是「没命中」，必须炸出来，否则这道门会因一个语法错而全绿。
			return fmt.Errorf("解析 %s 失败：%w", path, err)
		}
		hits = append(hits, h...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk 失败：%v", err)
	}

	var bad []string
	seenAllow := map[string]bool{}
	for _, h := range hits {
		key := fmt.Sprintf("%s:%d", h.rel, h.line)
		if reason, ok := byteSliceAllowlist[key]; ok {
			seenAllow[key] = true
			if reason == "" {
				bad = append(bad, fmt.Sprintf("%s 豁免理由为空（写了豁免等于没审）", key))
			}
			continue
		}
		bad = append(bad, fmt.Sprintf("%s  %s() 里 %s —— 字符串按字节硬切会产出非法 UTF-8；落库字段会让 PostgreSQL 拒绝整条写入（SQLSTATE 22021）", key, h.fn, h.expr))
	}
	sort.Strings(bad)

	var stale []string
	for key := range byteSliceAllowlist {
		if !seenAllow[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		bad = append(bad, "以下豁免条目已失效（代码已改或行号已移），请删掉：\n    "+strings.Join(stale, "\n    "))
	}

	if len(bad) > 0 {
		t.Fatalf("发现 %d 条字符串上的字节切片：\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

// 判据自证：探测器必须能抓到它声称抓的那几种，且**不误报**另外几种。
// 没有这条，上面那道门可能因为探测器恒返回空而永远绿。
func TestByteSliceDetectorCatchesAndIgnores(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("写 %s 失败：%v", name, err)
		}
	}
	write("types.go", `package x

type Holder struct {
	Name string
	N    int
}

type Other struct {
	Name int
}
`)
	write("caught.go", `package x

func badParam(message string) string {
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func badNamedResult(errMsg string) (out string) {
	if len(errMsg) > 200 {
		errMsg = errMsg[:200]
	}
	return errMsg
}

// 局部变量：从 string 参数传递赋值而来（§260 新增覆盖）
func badLocal(s string) string {
	t := s + "x"
	if len(t) > 10 {
		t = t[:10]
	}
	return t
}

// 局部变量：多跳传播（§260 的定点传播要能穿透）
func badLocalMultiHop(s string) string {
	a := s
	b := a
	return b[:4]
}

// 局部 var 声明（§260 新增覆盖）
func badLocalVarDecl(s string) string {
	var t string = s
	return t[:4]
}

// 结构体字段：接收者类型可解析且该字段确实是 string（§260 新增覆盖）
func badField(h Holder) string {
	return h.Name[:8]
}

// 结构体字段：指针接收者
func badFieldPtr(h *Holder) string {
	return h.Name[:8]
}

// 局部结构体变量
func badFieldLocal() string {
	h := Holder{Name: "x"}
	return h.Name[:8]
}
`)
	write("ignored.go", `package x

// range 变量：元素类型要完整类型检查，本门不抓（已知盲区）
func rangeVar(ss []string) string {
	var last string
	for _, s := range ss {
		last = s[:3]
	}
	return last
}

// 同一个字段名在别的结构体里不是 string —— 不能凭名字判
func otherStructIsNotString(o Other) string {
	return o.Name[:3]
}

// []byte 按字节切是对的
func bytesAreFine(b []byte) []byte {
	if len(b) > 8 {
		b = b[:8]
	}
	return b
}

// 上界是变量，可能本来就是字符语义
func nonLiteral(s string) string {
	return s[:someLimit]
}

// Low 非 nil，不属于本门范围
func notFromZero(s string) string { return s[1:] }

// 接收者类型推导不出（本门不抓，已知盲区）
func unresolvableReceiver() string {
	x := newHolder().Name
	return x[:3]
}

var someLimit = 4

type holderType struct{ Name string }

func newHolder() *holderType { return &holderType{} }
`)

	fset := token.NewFileSet()
	hits, err := collectByteSlices(fset, dir)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	gotFn := map[string]int{}
	for _, h := range hits {
		gotFn[h.fn]++
	}
	wantFn := []string{
		"badParam", "badNamedResult", "badLocal", "badLocalMultiHop",
		"badLocalVarDecl", "badField", "badFieldPtr", "badFieldLocal",
	}
	for _, fn := range wantFn {
		if gotFn[fn] == 0 {
			t.Errorf("漏抓：%s（应至少 1 处，实际命中表 %v）", fn, gotFn)
		}
	}
	for _, fn := range []string{"rangeVar", "otherStructIsNotString", "bytesAreFine", "nonLiteral", "notFromZero", "unresolvableReceiver"} {
		if gotFn[fn] != 0 {
			t.Errorf("误报：%s 本应被忽略，实际命中 %d 处", fn, gotFn[fn])
		}
	}
	if len(hits) != len(wantFn) {
		t.Errorf("命中总数应为 %d（每个函数各 1 处），实际 %d：%+v", len(wantFn), len(hits), hits)
	}
}
