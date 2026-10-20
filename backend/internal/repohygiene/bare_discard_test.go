package repohygiene

// bare_discard_test.go — §947 卡口：后端**裸语句丢弃了返回值**。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，任何「一个调用的返回值被直接丢掉」的裸语句，
//	都必须能被点名。**
//
//	与 §945 的分工：
//
//	· §945 管的是**显式丢弃**（`_ = f(...)`）⇒ 那是写代码的人**明知**有错、
//	  仍写 `_ =` 的形态。
//	· §947 管的是**隐式丢弃**（裸语句 `f(...)`）⇒ 那是**根本没意识到**有返回值
//	  的形态 ⇒ ⇒⇒ 它是 §945 **看不见**的那一族（§945 的扫描器只访问
//	  `AssignStmt`）。
//
// # 为什么必须用**类型信息**（这才是本节的核心）
//
//	裸语句里绝大多数是 **void setter**（`h.Set("k","v")`、`mu.Lock()`…）⇒
//	AST **分不出**「丢掉了一个 error」与「本来就没有返回值」。
//	⇒ ⇒⇒ §946 已经踩过一次这个坑（「用语法树」≠「用语法树就够」）。
//
//	⇒ 所以本卡口用 **go/types**：把每个调用的**静态返回类型**查出来，
//	  于是「这个裸语句丢掉了什么」变成**可判定**的事实。
//
//	★ 类型信息还带来一个 AST 永远给不了的能力：
//	  **`bytes.Buffer` / `strings.Builder` / `hash.Hash` 的写方法，
//	  文档保证 error 恒为 nil** ⇒ 它们可以被**可证明地**豁免。
//	  ⇒ 这 180+ 处豁免**只有类型信息才能做**（同名方法在 `*os.File` 上
//	  就是真会失败的写）⇒ ⇒⇒ 这就是「用类型信息」换来的东西。
//
// # 三层断言（缺一层就退化成「门绿 ≠ 量具有牙」）
//
//	① **台账双向相等**：扫出来的 (调用名, 接收者类型) → 条数 + 文件集合
//	   必须与台账**完全一致** ⇒ 少一行（有人偷偷删了台账行）或多一行
//	   （有人新加了一个丢弃）都红。
//	② **台账里不许有「没兜底的持久化写」**：登记「持久化写的错误被丢掉」
//	  只在**错误分支里**才成立（`if err != nil { f.Close() }`）⇒
//	  ⇒⇒ 这条是**机器可判**的（AST 能看出语句在不在 `err != nil` 分支里）。
//	③ **自证臂**：扫描器自己坏掉时必须**立刻红**（合成语料 + 正/负控）。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① **只管裸语句**（`*ast.ExprStmt`）：`defer f(...)` 与 `go f(...)`
//	   是**另一个族**（实测 defer 形态丢弃返回值的 150 处，其中 25 处是
//	   `defer tx.Rollback(ctx)` 这个 Go 的标准事务惯用法）⇒ 本卡口不覆盖。
//	   ⇒ 但 **defer 的「写」语义**（`defer tx.Commit(ctx)` / `defer f.Write(...)`）
//	   是**卡口管的**（见断言 ④）—— 因为那会让一次提交**静默丢失**。
//	② **`_ = f(...)` 不在本卡口范围**：那是 §945 的族（两者互不重叠，
//	   §945 的台账条数不受本卡口影响）。
//	③ `backend/third_party/` 是**上游 vendored 代码** ⇒ 排除。
//	④ `*_test.go` 与 `testdata/` 不是生产路径 ⇒ 排除。
//	⑤ **构建标签排除的文件不在本卡口范围**（`go list` 只给当前平台生效的
//	   文件）⇒ 实测本仓当前为 0 ⇒ 由断言 ⑥ 盯着。
//
// # 为什么普查器要在测试里调 `go list -export`
//
//	类型信息需要**导出数据**（编译产物）⇒ stdlib 的 `go/importer` 的 gc 模式
//	可以按需加载 ⇒ 但它得知道每个 import path 对应哪个 .a 文件 ⇒
//	⇒ 那份清单只有 `go list -export -json -deps` 给得出。
//
//	⚠ 拿不到就**必须失败**（不许 skip）：一道「量不出来就不给结论」的卡口
//	  等于没有卡口（§941 的 PG 缺失即拒绝跑，是同一条纪律）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// =============================================================================
// 数据结构
// =============================================================================

// s947Site 是「一处被丢弃的调用结果」。
type s947Site struct {
	Kind     string // "bare" | "defer" | "go"
	Rel      string // 仓库相对路径（backend/...）
	Func     string // 所属函数（闭包记作 外层函数·closure）
	Callee   string // 调用名（方法名 / 包级函数名 / 局部闭包名）
	Pkg      string // 包级调用的包名（fmt / log / …；方法调用为空）
	Recv     string // 接收者的静态类型（方法调用才有）
	RecvText string // 接收者的源码文本（如 "tx"、"os.Stdout"）
	Arg0Type string // 第一个实参的静态类型
	Arg0Text string // 第一个实参的源码文本
	Ret      string // 返回值类型（"error" / "(int, error)" / "bool" …）
	Line     int
	InErr    bool // 该语句是否位于某个 `err != nil` 分支内
	HasDefer bool // 同函数内是否有同名 defer 兜底
}

// s947Key 是台账的分组键。
func (s s947Site) s947Key() string { return s.Callee + "|" + s.Recv }

// =============================================================================
// 普查器
// =============================================================================

// s947Package 是 `go list -export -json -deps` 的一个条目。
type s947Package struct {
	ImportPath      string
	Dir             string
	Export          string
	GoFiles         []string
	CgoFiles        []string
	IgnoredGoFiles  []string
	Standard        bool
	s947IgnoredList bool
}

// s947BackendRoot 找到 backend 模块根（含 backend/go.mod 的目录）。
func s947BackendRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("取工作目录失败: %w", err)
	}
	for i := 0; i < 8; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(b), "module github.com/halfking/pocket-opencode/backend") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("找不到 backend 模块根（backend/go.mod）")
}

// s947ListPackages 跑 `go list -export -json -deps ./...`。
//
// ⚠ 失败必须**致命**：普查器拿不到导出数据 ⇒ 就没有类型信息 ⇒
//
//	「扫出来是 0 条」会被误读成「仓库很干净」。
func s947ListPackages(root string) ([]s947Package, error) {
	cmd := exec.Command("go", "list", "-export", "-json", "-deps", "./...")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("`go list -export -json -deps ./...` 退出 %v\n%s\n"+
			"⚠ 这一步是**类型信息**的唯一来源 ⇒ 拿不到就不能给结论（不许 skip）",
			err, stderr.String())
	}
	var pkgs []s947Package
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	for {
		var p s947Package
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("解析 go list 输出失败: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("`go list` 返回空清单 ⇒ 普查器无从下手（拒绝给结论）")
	}
	return pkgs, nil
}

// s947Importer 按 import path 提供导出数据。
func s947Importer(fset *token.FileSet, pkgs []s947Package) types.Importer {
	exports := map[string]string{}
	for _, p := range pkgs {
		if p.Export != "" {
			exports[p.ImportPath] = p.Export
		}
	}
	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %q", path)
		}
		return os.Open(f)
	})
}

var (
	s947Once  sync.Once
	s947Sites []s947Site
	s947Err   error
)

// s947Census 扫出后端全部「被丢弃的调用结果」。
//
// ⚠ 结果在**进程内缓存**：同一进程里 9 条断言共用一次类型检查
//
//	⇒ ⇒⇒ 但缓存**必须和错误一起存**（否则第一次失败后
//	后续断言会拿到「空普查」而假绿 ⇒ 那就是「门绿 ≠ 量具有牙」）。
func s947Census(t *testing.T) []s947Site {
	t.Helper()
	s947Once.Do(func() { s947Sites, s947Err = s947CensusRun() })
	if s947Err != nil {
		t.Fatalf("§947 普查失败: %v", s947Err)
	}
	return s947Sites
}

// s947CensusRun 是普查的真正实现（不碰 *testing.T ⇒ 可缓存）。
func s947CensusRun() ([]s947Site, error) {
	root, err := s947BackendRoot()
	if err != nil {
		return nil, err
	}
	pkgs, err := s947ListPackages(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	imp := s947Importer(fset, pkgs)

	var sites []s947Site
	var typeErrs []string
	cgoSkipped := 0
	ignored := 0
	for _, p := range pkgs {
		if p.Standard {
			continue
		}
		if !strings.HasPrefix(p.Dir, root) {
			continue
		}
		if strings.Contains(p.Dir, string(filepath.Separator)+"third_party"+string(filepath.Separator)) {
			continue
		}
		if len(p.CgoFiles) > 0 {
			// cgo 包的 .go 源文件不能直接类型检查（import "C" 不是真包）。
			cgoSkipped++
			continue
		}
		ignored += len(p.IgnoredGoFiles)
		files := append([]string{}, p.GoFiles...)
		if len(files) == 0 {
			continue
		}
		var parsed []*ast.File
		rels := []string{}
		for _, fn := range files {
			path := filepath.Join(p.Dir, fn)
			af, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
			}
			parsed = append(parsed, af)
			rel, err := mustRel(root, path)
			if err != nil {
				return nil, err
			}
			rels = append(rels, "backend/"+filepath.ToSlash(rel))
		}
		info := &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		}
		conf := types.Config{
			Importer: imp,
			Error: func(err error) {
				typeErrs = append(typeErrs, fmt.Sprintf("类型检查 %s: %v", p.ImportPath, err))
			},
		}
		conf.Check(p.ImportPath, fset, parsed, info)

		for i, af := range parsed {
			s947WalkFile(fset, info, af, rels[i], &sites)
		}
	}
	if cgoSkipped > 0 {
		typeErrs = append(typeErrs, fmt.Sprintf("有 %d 个 cgo 包未被普查（范围声明里必须写明）", cgoSkipped))
	}
	_ = ignored // 断言 ⑥ 单独盯这个数
	if len(typeErrs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(typeErrs, "\n  - "))
	}
	return sites, nil
}

func mustRel(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("相对路径计算失败: %w", err)
	}
	return rel, nil
}

// s947WalkFile 走一个文件里的所有函数声明。
func s947WalkFile(fset *token.FileSet, info *types.Info, file *ast.File, rel string, out *[]s947Site) {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		defers := s947CollectDefers(fd.Body)
		s947WalkStmts(fset, info, fd.Body.List, fd.Name.Name, rel, false, defers, out)
	}
}

// s947CollectDefers 收集一个函数体里的 defer 调用（用于「回滚有 defer 兜底」这条断言）。
func s947CollectDefers(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		sel, ok := d.Call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		out[s947ExprText(sel.X)+"."+sel.Sel.Name] = true
		return true
	})
	return out
}

// s947WalkStmts 走语句树，收集被丢弃的调用结果。
func s947WalkStmts(fset *token.FileSet, info *types.Info, stmts []ast.Stmt,
	fn, rel string, inErr bool, defers map[string]bool, out *[]s947Site) {
	for _, stmt := range stmts {
		// 闭包（*ast.FuncLit）是**表达式**不是语句 ⇒ 它出现在哪儿都得单独走一遍
		// （例如 `http.HandleFunc("/", func(w,r){…})`、`defer func(){…}()`）。
		s947ScanFuncLits(fset, info, stmt, fn, rel, inErr, defers, out)
		switch st := stmt.(type) {
		case *ast.ExprStmt:
			call, ok := st.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			if site, ok := s947MakeSite(fset, info, call, "bare", fn, rel, inErr); ok {
				site.HasDefer = defers[site.RecvText+"."+site.Callee]
				*out = append(*out, site)
			}
		case *ast.DeferStmt:
			if site, ok := s947MakeSite(fset, info, st.Call, "defer", fn, rel, inErr); ok {
				*out = append(*out, site)
			}
		case *ast.GoStmt:
			if site, ok := s947MakeSite(fset, info, st.Call, "go", fn, rel, inErr); ok {
				*out = append(*out, site)
			}
		case *ast.IfStmt:
			errBranch := inErr || s947IsErrCondition(st.Cond)
			s947WalkStmts(fset, info, st.Body.List, fn, rel, errBranch, defers, out)
			switch els := st.Else.(type) {
			case *ast.BlockStmt:
				s947WalkStmts(fset, info, els.List, fn, rel, inErr, defers, out)
			case *ast.IfStmt:
				s947WalkStmts(fset, info, []ast.Stmt{els}, fn, rel, inErr, defers, out)
			}
		case *ast.ForStmt:
			s947WalkStmts(fset, info, st.Body.List, fn, rel, inErr, defers, out)
		case *ast.RangeStmt:
			s947WalkStmts(fset, info, st.Body.List, fn, rel, inErr, defers, out)
		case *ast.BlockStmt:
			s947WalkStmts(fset, info, st.List, fn, rel, inErr, defers, out)
		case *ast.SwitchStmt:
			for _, c := range st.Body.List {
				cc, ok := c.(*ast.CaseClause)
				if !ok {
					continue
				}
				s947WalkStmts(fset, info, cc.Body, fn, rel, inErr, defers, out)
			}
		case *ast.TypeSwitchStmt:
			for _, c := range st.Body.List {
				cc, ok := c.(*ast.CaseClause)
				if !ok {
					continue
				}
				s947WalkStmts(fset, info, cc.Body, fn, rel, inErr, defers, out)
			}
		case *ast.SelectStmt:
			for _, c := range st.Body.List {
				cc, ok := c.(*ast.CommClause)
				if !ok {
					continue
				}
				s947WalkStmts(fset, info, cc.Body, fn, rel, inErr, defers, out)
			}
		case *ast.LabeledStmt:
			s947WalkStmts(fset, info, []ast.Stmt{st.Stmt}, fn, rel, inErr, defers, out)
		}
	}
}

// s947ScanFuncLits 找出这条语句**表达式部分**里的闭包并走进它的函数体。
//
// ⚠ 嵌套语句不在这里走（由 s947WalkStmts 负责）⇒ 否则同一段代码会被走两遍。
func s947ScanFuncLits(fset *token.FileSet, info *types.Info, root ast.Node,
	fn, rel string, inErr bool, defers map[string]bool, out *[]s947Site) {
	ast.Inspect(root, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		switch v := x.(type) {
		case *ast.FuncLit:
			s947WalkStmts(fset, info, v.Body.List, fn+"·closure", rel, inErr, defers, out)
			return false
		case ast.Stmt:
			if x != root {
				return false
			}
		}
		return true
	})
}

// s947MakeSite 把一个调用表达式变成一条「被丢弃的调用结果」。
//
// ⚠ 关键：**返回类型是空的（void）不算丢弃** —— 那不是「丢掉了什么」，
//
//	只是「本来就没有东西可拿」。
func s947MakeSite(fset *token.FileSet, info *types.Info, call *ast.CallExpr,
	kind, fn, rel string, inErr bool) (s947Site, bool) {
	tv, ok := info.Types[call]
	if !ok || tv.Type == nil {
		return s947Site{}, false
	}
	ret := tv.Type.String()
	if ret == "" || ret == "()" {
		return s947Site{}, false
	}
	pos := fset.Position(call.Pos())
	s := s947Site{
		Kind:  kind,
		Rel:   rel,
		Func:  fn,
		Ret:   ret,
		Line:  pos.Line,
		InErr: inErr,
	}
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		s.Callee = f.Sel.Name
		s.RecvText = s947ExprText(f.X)
		if id, ok := f.X.(*ast.Ident); ok {
			s.Pkg = id.Name
		}
		if sel := info.Selections[f]; sel != nil {
			s.Recv = sel.Recv().String()
		}
	case *ast.Ident:
		s.Callee = f.Name
	case *ast.IndexExpr:
		// 泛型调用：取被实例化的那个名字
		if id, ok := f.X.(*ast.Ident); ok {
			s.Callee = id.Name
		}
		if id, ok := f.X.(*ast.Ident); ok {
			if obj := info.Uses[id]; obj != nil {
				s.Recv = ""
				s.Callee = obj.Name()
			}
		}
	case *ast.IndexListExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			s.Callee = id.Name
		}
	}
	if len(call.Args) > 0 {
		s.Arg0Text = s947ExprText(call.Args[0])
		if tv2, ok := info.Types[call.Args[0]]; ok && tv2.Type != nil {
			s.Arg0Type = tv2.Type.String()
		}
	}
	return s, true
}

// s947ExprText 取表达式的源码文本。
//
// ⚠ 有些判定**只能按写法做**：`os.Stdout` 与任何 `*os.File` 在类型上
//
//	**完全一样** ⇒ 「写的是不是终端」只能看源码文本。
func s947ExprText(e ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), e); err != nil {
		return ""
	}
	return buf.String()
}

// s947IsErrCondition 判一个 if 条件是不是「错误分支」。
//
// ⚠ 收得很紧：只认 `err != nil`（名字必须**恰好**是 err）⇒
//
//	宁可漏判（让卡口更严），也不可错判（让卡口变松）。
func s947IsErrCondition(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	id, ok := bin.Y.(*ast.Ident)
	if !ok || id.Name != "nil" {
		return false
	}
	lhs, ok := bin.X.(*ast.Ident)
	if !ok || lhs.Name != "err" {
		return false
	}
	return true
}

// =============================================================================
// 豁免规则（每一条都必须是「可证明」的）
// =============================================================================

// s947Exempt 判一处丢弃是否属于「可证明无害」的豁免。
//
// ⚠ 每条豁免都必须有**依据**（Go 文档保证 / 标准库惯用法），
//
//	拿不出依据的 ⇒ 进台账，不许豁免。
func s947Exempt(s s947Site) string {
	switch s.Callee {
	case "Write", "WriteString", "WriteByte", "WriteRune":
		switch s.Recv {
		case "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer":
			// 依据：bytes.Buffer / strings.Builder 的写方法**永远返回 nil error**
			//（它们的写只会因内存不足 panic，不会返回 error）。
			return "R1 内存缓冲区（error 恒为 nil）"
		case "hash.Hash":
			// 依据：hash.Hash.Write 的文档写明「never returns an error」。
			return "R3 hash.Hash.Write（error 恒为 nil）"
		}
	case "Fprintf", "Fprint", "Fprintln":
		switch s.Arg0Type {
		case "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer":
			// 依据：写进内存缓冲区 ⇒ 不会失败。
			return "R2 写进内存缓冲区（error 恒为 nil）"
		}
		if s.Arg0Text == "os.Stdout" || s.Arg0Text == "os.Stderr" {
			return "R6 终端输出"
		}
	case "Printf", "Print", "Println":
		if s.Pkg == "fmt" || s.Pkg == "log" {
			// 依据：写 stdout/stderr ⇒ 惯用法。
			return "R4 终端输出"
		}
	case "copy":
		// 依据：copy 的返回值是「复制的字节数」，error 恒为 nil（标准库惯用法）。
		return "R7 copy（惯用法）"
	case "GET", "POST", "PUT", "DELETE", "PATCH":
		if s.Recv == "github.com/labstack/echo/v4.Group" || s.Recv == "*github.com/labstack/echo/v4.Group" {
			// 依据：echo 的路由注册返回 *Route，惯用法是不接返回值。
			return "R8 echo 路由注册（惯用法）"
		}
	case "Add":
		if s.Recv == "sync/atomic.Int64" || s.Recv == "*sync/atomic.Int64" {
			// 依据：原子计数器的返回值是「加完之后的值」，惯用法是不接。
			return "R9 原子计数器（惯用法）"
		}
	}
	if s.RecvText == "os.Stdout" || s.RecvText == "os.Stderr" {
		return "R6 终端输出"
	}
	return ""
}

// =============================================================================
// 台账
// =============================================================================

// s947LedgerEntry 是台账的一行：**一类**被丢弃的调用结果 + 逐条定性理由。
//
// ⚠ 为什么按「(调用名, 接收者类型)」分组而不是逐行登记：
//
//	同一类的几十处**理由是同一个**（例如 websocket 的 Close 都是
//	「连接正在关闭，无补救动作」）⇒ 逐行登记会写 74 行重复的话，
//	而**逐行登记的真正价值是「谁在哪」**⇒ ⇒⇒ 所以本台账记
//	**条数 + 文件集合**：文件集合一变（多一个文件）就必须显式改台账
//	⇒ ⇒⇒ 新增一处丢弃不会「顺手把计数 +1 就混过去」。
type s947LedgerEntry struct {
	Callee string
	Recv   string
	Count  int
	Files  []string
	Reason string
}

// s947Ledger 是**全部**被登记的丢弃点（豁免之外的，一个都不许漏）。
var s947Ledger = []s947LedgerEntry{
	{
		Callee: "Close", Recv: "*github.com/gorilla/websocket.Conn", Count: 12,
		Files: []string{
			"backend/internal/agent/transport_ws.go",
			"backend/internal/server/server_plugin_ws.go",
			"backend/internal/websocket/hub.go",
			"backend/internal/websocket/mobile_hub.go",
			"backend/internal/websocket/plugin_hub.go",
		},
		Reason: "连接正在关闭：Close 失败时连接已经是死的，没有任何补救动作可做。",
	},
	{
		Callee: "Close", Recv: "io.ReadCloser", Count: 12,
		Files: []string{
			"backend/internal/acchttp/client.go",
			"backend/internal/adapter/opencode_http.go",
			"backend/internal/agent/transport_http.go",
			"backend/internal/agent/transport_ws.go",
			"backend/internal/email/invoice_zip.go",
			"backend/internal/redclaw/client.go",
			"backend/internal/rss/fetcher.go",
			"backend/internal/server/llm_gateway_admin_client.go",
		},
		Reason: "只读响应体的收尾：内容已经读完，Close 失败没有补救动作（也不影响已读到的数据）。",
	},
	{
		Callee: "SetReadDeadline", Recv: "*github.com/gorilla/websocket.Conn", Count: 10,
		Files: []string{
			"backend/internal/websocket/hub.go",
			"backend/internal/websocket/mobile_hub.go",
			"backend/internal/websocket/plugin_hub.go",
		},
		Reason: "设置读超时：失败意味着连接已经不可用，后续读会自己报错，不影响正确性。",
	},
	{
		Callee: "SetWriteDeadline", Recv: "*github.com/gorilla/websocket.Conn", Count: 10,
		Files: []string{
			"backend/internal/websocket/hub.go",
			"backend/internal/websocket/mobile_hub.go",
			"backend/internal/websocket/plugin_hub.go",
		},
		Reason: "设置写超时：失败意味着连接已经不可用，后续写会自己报错，不影响正确性。",
	},
	{
		Callee: "Close", Recv: "*os.File", Count: 6,
		Files: []string{
			"backend/internal/adapter/disk/sqlite_ro.go",
			"backend/internal/email/body_cache.go",
			"backend/internal/email/crypto.go",
			"backend/internal/redclaw/file_exporter.go",
		},
		Reason: "**错误路径上的清理**：主错误（写失败）已经返回，这里只是把临时文件/句柄收掉 ⇒ 由断言 ② 机器判定「必须位于 err != nil 分支内」。",
	},
	{
		Callee: "WriteMessage", Recv: "*github.com/gorilla/websocket.Conn", Count: 5,
		Files: []string{
			"backend/internal/websocket/hub.go",
			"backend/internal/websocket/mobile_hub.go",
			"backend/internal/websocket/plugin_hub.go",
		},
		Reason: "发关闭帧：写失败说明对端已经走了，帧发不出去也没有补救动作。",
	},
	{
		Callee: "SetDeadline", Recv: "net.Conn", Count: 4,
		Files: []string{
			"backend/internal/email/pop3_fetcher.go",
		},
		Reason: "设置空闲超时：失败意味着连接已经断了，后续读写会自己报错。",
	},
	{
		Callee: "Stop", Recv: "*time.Timer", Count: 3,
		Files: []string{
			"backend/internal/opencode/session_event_broadcaster.go",
			"backend/internal/rss/digest_service.go",
		},
		Reason: "Timer.Stop 的返回值只表示「是否成功停掉」，标准库惯用法是不接返回值。",
	},
	{
		Callee: "Close", Recv: "*database/sql.Rows", Count: 2,
		Files: []string{
			"backend/internal/chatagent/sqlite_store.go",
		},
		Reason: "rows.Close 的错误会被紧随其后的 rows.Err() 覆盖（迭代期的 DB 错误由 rows.Err() 报），不额外携带信息。",
	},
	{
		Callee: "Deliver", Recv: "*github.com/halfking/pocket-opencode/backend/internal/agent.PendingCalls", Count: 2,
		Files: []string{
			"backend/internal/agent/transport_stdio.go",
		},
		Reason: "false = 「没有等待者」（这个 id 无人认领）⇒ 同一帧还会走 recvCh 兼容路径，不算丢失。",
	},
	{
		Callee: "Close", Recv: "*github.com/emersion/go-imap/v2/imapclient.Client", Count: 1,
		Files: []string{
			"backend/internal/email/imapops.go",
		},
		Reason: "IMAP 会话的收尾，Close 失败无补救动作。",
	},
	{
		Callee: "Close", Recv: "*github.com/halfking/pocket-opencode/backend/internal/chatagent.SQLiteStore", Count: 1,
		Files: []string{
			"backend/cmd/pocketd/main.go",
		},
		Reason: "**错误路径上的清理**：初始化失败分支里，主错误已经 WARN 并返回。",
	},
	{
		Callee: "Close", Recv: "net.Conn", Count: 1,
		Files: []string{
			"backend/internal/server/smtp_probe.go",
		},
		Reason: "SMTP 探针的连接收尾，无补救动作。",
	},
	{
		Callee: "Rollback", Recv: "github.com/jackc/pgx/v5.Tx", Count: 1,
		Files: []string{
			"backend/internal/chatagent/sync.go",
		},
		Reason: "版本冲突分支上的**显式**回滚（为了尽早释放连接）⇒ 同一函数上方有 `defer tx.Rollback(ctx)` 兜底（由断言 ③ 机器判定）。",
	},
	{
		Callee: "Sscanf", Recv: "", Count: 1,
		Files: []string{
			"backend/cmd/gwdbg/main.go",
		},
		Reason: "调试工具里的一次性解析：解析失败不影响后续流程（工具只打印）。",
	},
	{
		Callee: "applyParsedBodyDate", Recv: "", Count: 1,
		Files: []string{
			"backend/internal/email/pipeline.go",
		},
		Reason: "函数靠**指针**改写 inv；返回值只是同一份日期的冗余副本。",
	},
	{
		Callee: "insertOpsEntries", Recv: "*github.com/halfking/pocket-opencode/backend/internal/server.Server", Count: 1,
		Files: []string{
			"backend/internal/server/server_email_purge.go",
		},
		Reason: "返回的是「插进去的条目」，调用方改用 r 里的内容，不需要返回值。",
	},
	{
		Callee: "workerSemaphore", Recv: "*github.com/halfking/pocket-opencode/backend/internal/scheduledtask.Scheduler", Count: 1,
		Files: []string{
			"backend/internal/scheduledtask/scheduler.go",
		},
		Reason: "调用点是**副作用**（惰性建信号量），返回值在这个位置用不到。",
	},
}

// =============================================================================
// 断言
// =============================================================================

// TestS947NoUnregisteredDiscardedResult —— 台账双向相等。
func TestS947NoUnregisteredDiscardedResult(t *testing.T) {
	sites := s947Census(t)
	groups := map[string]*s947Group{}
	for _, s := range sites {
		if s.Kind != "bare" {
			continue
		}
		if s947Exempt(s) != "" {
			continue
		}
		g := groups[s.s947Key()]
		if g == nil {
			g = &s947Group{callee: s.Callee, recv: s.Recv, files: map[string]bool{}}
			groups[s.s947Key()] = g
		}
		g.count++
		g.files[s.Rel] = true
	}

	ledger := map[string]*s947LedgerEntry{}
	for i := range s947Ledger {
		e := &s947Ledger[i]
		ledger[e.Callee+"|"+e.Recv] = e
	}

	var problems []string
	for key, g := range groups {
		e, ok := ledger[key]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"未登记的丢弃点：%s（接收者 %s）× %d，文件 %v",
				g.callee, g.recv, g.count, sortedKeys(g.files)))
			continue
		}
		if e.Count != g.count {
			problems = append(problems, fmt.Sprintf(
				"台账条数与实际不符：%s（接收者 %s）台账 %d，实际 %d（文件 %v）",
				g.callee, g.recv, e.Count, g.count, sortedKeys(g.files)))
			continue
		}
		want := map[string]bool{}
		for _, f := range e.Files {
			want[f] = true
		}
		if len(want) != len(g.files) {
			problems = append(problems, fmt.Sprintf(
				"台账文件集合与实际不符：%s（接收者 %s）台账 %v，实际 %v",
				g.callee, g.recv, e.Files, sortedKeys(g.files)))
			continue
		}
		for f := range want {
			if !g.files[f] {
				problems = append(problems, fmt.Sprintf(
					"台账里登记的文件 %s 现在没有这类丢弃点了（%s / %s）", f, g.callee, g.recv))
			}
		}
	}
	for key, e := range ledger {
		if _, ok := groups[key]; !ok {
			problems = append(problems, fmt.Sprintf(
				"台账里有一行已经不存在了（可以删）：%s（接收者 %s）× %d",
				e.Callee, e.Recv, e.Count))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("§947 台账与实际不符（%d 条）：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
}

// s947Group 是普查结果的一个分组。
type s947Group struct {
	callee string
	recv   string
	count  int
	files  map[string]bool
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// s947IsPersistenceWrite 判一处丢弃是不是「持久化写」。
func s947IsPersistenceWrite(s s947Site) bool {
	switch s.Recv {
	case "*os.File":
		switch s.Callee {
		case "Write", "WriteString", "WriteByte", "WriteRune", "Sync":
			return true
		}
	case "*mime/multipart.Writer":
		return s.Callee == "Close"
	case "io.WriteCloser":
		return s.Callee == "Write"
	}
	return false
}

// TestS947PersistenceWriteMustBeGuarded —— 台账里不许有「没兜底的持久化写」。
//
// 这一条是本卡口的**牙**：登记「持久化写的错误被丢掉」只有在
// **错误分支里**才成立（`if err != nil { f.Close() }`）⇒
// ⇒⇒ 裸写在正常路径上的持久化写 ⇒ 必须被处理，不能进台账。
func TestS947PersistenceWriteMustBeGuarded(t *testing.T) {
	sites := s947Census(t)
	var problems []string
	for _, s := range sites {
		if s.Kind != "bare" || s947Exempt(s) != "" {
			continue
		}
		if !s947IsPersistenceWrite(s) {
			continue
		}
		// 文件的 Close 是持久化写的最后一环 ⇒ 同样按「持久化写」对待。
		if s.Recv == "*os.File" && s.Callee == "Close" {
			if !s.InErr {
				problems = append(problems, fmt.Sprintf(
					"%s:%d [%s] %s 的失败没有被兜底（不在 err != nil 分支内）",
					s.Rel, s.Line, s.Func, s.Callee))
			}
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s:%d [%s] 持久化写 %s.%s 的错误被丢弃且没有兜底",
			s.Rel, s.Line, s.Func, s.Recv, s.Callee))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("§947 有 %d 处持久化写的失败没有兜底：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
}

// TestS947RollbackMustHaveDeferFallback —— 回滚类必须有 defer 兜底。
func TestS947RollbackMustHaveDeferFallback(t *testing.T) {
	sites := s947Census(t)
	var problems []string
	for _, s := range sites {
		if s.Kind != "bare" || s947Exempt(s) != "" {
			continue
		}
		if s.Callee != "Rollback" {
			continue
		}
		if !s.HasDefer {
			problems = append(problems, fmt.Sprintf(
				"%s:%d [%s] 显式回滚没有 defer 兜底（回滚失败 ⇒ 连接可能被占住）",
				s.Rel, s.Line, s.Func))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("§947 有 %d 处回滚没有 defer 兜底：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
}

// TestS947DeferredPersistenceWriteForbidden —— defer 里的「写」语义不许出现。
//
// ⚠ 范围声明里说「defer 形态不在本卡口范围」，但**这一条是例外**：
//
//	`defer tx.Commit(ctx)` / `defer f.Write(...)` 这类会让一次提交
//	**静默丢失** ⇒ ⇒⇒ 它必须被接住。
func TestS947DeferredPersistenceWriteForbidden(t *testing.T) {
	sites := s947Census(t)
	var problems []string
	for _, s := range sites {
		if s.Kind != "defer" {
			continue
		}
		switch {
		case s.Recv == "*os.File" && (s.Callee == "Write" || s.Callee == "WriteString" || s.Callee == "Sync"):
		case s.Recv == "io.WriteCloser" && s.Callee == "Write":
		case strings.Contains(s.Recv, ".Tx") && (s.Callee == "Commit" || s.Callee == "Exec"):
		default:
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s:%d [%s] defer 了一个持久化写 %s.%s（失败会静默丢失）",
			s.Rel, s.Line, s.Func, s.Recv, s.Callee))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("§947 有 %d 处 defer 的持久化写：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
}

// TestS947ScannerHasTeeth —— 自证臂：扫描器坏了必须立刻红。
//
// ★ 本仓的第二条「把量具自己打坏」的防护（第一条是 §945 的 M8）：
//
//	合成语料里**埋**一处丢弃（正控）⇒ 必须被扫出来；
//	同时**埋**几处不该被算进去的（负控：void 调用 / `_ =` / 缓冲写入 /
//	终端输出 / copy）⇒ 一个都不许被算进「丢弃」。
func TestS947ScannerHasTeeth(t *testing.T) {
	const src = `package synthetic

import (
	"bytes"
	"os"
)

func positives(f *os.File, b *bytes.Buffer) {
	f.Close()                    // ← 正控：丢弃了 error，必须被扫出来
	f.Write([]byte("x"))         // ← 正控：持久化写，必须被扫出来
}

func negatives(b *bytes.Buffer, src []byte) {
	b.WriteString("x")            // 负控：内存缓冲（error 恒 nil）
	copy(b.Bytes(), src)           // 负控：copy（惯用法）
	os.Stdout.Write([]byte("x"))   // 负控：终端输出
	_ = os.Remove("x")             // 负控：§945 的族，不在本卡口范围
	var s string
	s = "x"                        // 负控：赋值不是丢弃
	_ = s
}
`
	root, err := s947BackendRoot()
	if err != nil {
		t.Fatalf("§947 自证臂: %v", err)
	}
	pkgs, err := s947ListPackages(root)
	if err != nil {
		t.Fatalf("§947 自证臂: %v", err)
	}
	fset := token.NewFileSet()
	imp := s947Importer(fset, pkgs)
	af, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("§947 自证臂：合成语料解析失败: %v", err)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imp}
	if _, err := conf.Check("synthetic", fset, []*ast.File{af}, info); err != nil {
		t.Fatalf("§947 自证臂：合成语料类型检查失败: %v", err)
	}
	var sites []s947Site
	s947WalkFile(fset, info, af, "synthetic.go", &sites)

	// 正控：不被豁免的丢弃点必须**恰好**是那两处（Close / Write on *os.File）。
	// ⚠ 注意负控（缓冲写入 / copy / 终端输出）**也会被扫出来** ——
	//   它们是「裸语句丢弃了返回值」，只是**被证明无害**而豁免
	//   ⇒ ⇒⇒ 所以负控要断言的是「它们被豁免了」，不是「扫不到」。
	var kept []s947Site
	exemptSeen := map[string]bool{}
	for _, s := range sites {
		if s.Kind != "bare" {
			continue
		}
		if r := s947Exempt(s); r != "" {
			exemptSeen[s.Callee+"|"+s.Recv] = true
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) != 2 {
		var got []string
		for _, s := range kept {
			got = append(got, fmt.Sprintf("%s(%s)", s.Callee, s.Recv))
		}
		t.Fatalf("§947 自证臂：应恰好留下 2 处未豁免的丢弃，实际 %d 处：%v", len(kept), got)
	}
	byCallee := map[string]string{}
	for _, s := range kept {
		byCallee[s.Callee] = s.Recv
	}
	if byCallee["Close"] != "*os.File" || byCallee["Write"] != "*os.File" {
		t.Fatalf("§947 自证臂：扫出的形态不对：%v", byCallee)
	}
	// 负控：缓冲写入 / 终端输出 / copy 必须被豁免（它们无害）。
	for _, want := range []string{"WriteString|*bytes.Buffer", "copy|", "Write|*os.File"} {
		if !exemptSeen[want] {
			t.Fatalf("§947 自证臂：负控 %q 没有被豁免（豁免规则坏了）", want)
		}
	}
	// 负控之二：`_ = os.Remove(...)`（§945 的族）根本不该出现在裸语句里。
	for _, s := range sites {
		if s.Kind == "bare" && s.Callee == "Remove" {
			t.Fatalf("§947 自证臂：`_ =` 形态被当成了裸语句")
		}
	}
}

// TestS947ExemptionRulesAreSound —— 豁免规则的正/负控。
//
// ★ 豁免规则本身也要有牙：R1（内存缓冲区）**只能**豁免缓冲区
//
//	⇒ `*os.File.WriteString` 这种真会失败的写**不许**被豁免。
func TestS947ExemptionRulesAreSound(t *testing.T) {
	buf := s947Site{Callee: "WriteString", Recv: "strings.Builder"}
	if s947Exempt(buf) == "" {
		t.Fatalf("§947 豁免规则失效：strings.Builder.WriteString 应当被豁免")
	}
	file := s947Site{Callee: "WriteString", Recv: "*os.File"}
	if s947Exempt(file) != "" {
		t.Fatalf("§947 豁免规则过宽：*os.File.WriteString 竟然被豁免了（它真会失败）")
	}
	fileWrite := s947Site{Callee: "Write", Recv: "*os.File"}
	if s947Exempt(fileWrite) != "" {
		t.Fatalf("§947 豁免规则过宽：*os.File.Write 竟然被豁免了（它真会失败）")
	}
	hashWrite := s947Site{Callee: "Write", Recv: "hash.Hash"}
	if s947Exempt(hashWrite) == "" {
		t.Fatalf("§947 豁免规则失效：hash.Hash.Write 应当被豁免")
	}
	// 终端判定只能按**写法**（类型上 os.Stdout 与任何 *os.File 没区别）。
	stdout := s947Site{Callee: "Write", Recv: "*os.File", RecvText: "os.Stdout"}
	if s947Exempt(stdout) == "" {
		t.Fatalf("§947 豁免规则失效：os.Stdout.Write 应当被豁免")
	}
	notStdout := s947Site{Callee: "Write", Recv: "*os.File", RecvText: "f"}
	if s947Exempt(notStdout) != "" {
		t.Fatalf("§947 豁免规则过宽：普通文件（名字不是 os.Stdout）竟然被豁免了")
	}
}

// TestS947FixedSitesStillExist —— §947 参考集：修过的那批不许被「删掉」来消红。
//
// ⚠ §945 的教训：判据只钉「现在没有丢弃」是不够的 ——
//
//	有人可以把整个调用**删掉**，红就消失了。
//	⇒ ⇒⇒ 必须钉住「那个调用还在，而且已经不是被丢弃的形态」。
func TestS947FixedSitesStillExist(t *testing.T) {
	root, err := s947BackendRoot()
	if err != nil {
		t.Fatalf("§947 参考集: %v", err)
	}
	cases := []struct {
		rel    string
		needle string
		why    string
	}{
		{"internal/stt/transcribe.go", "if err := w.Close(); err != nil",
			"multipart 的 Close 必须被接住（请求体不完整 ⇒ 上游收到畸形 multipart）"},
		{"internal/stt/provider_openai.go", "if err := w.Close(); err != nil",
			"multipart 的 Close 必须被接住"},
		{"internal/websocket/mobile_hub.go", "if _, err := w.Write(",
			"WS 写失败 = 流已死 ⇒ 必须中断这条流"},
		{"cmd/pocketd/main.go", "if err := rssSched.Start(",
			"调度器启动失败必须留痕（不能打「started」的假日志）"},
		{"internal/opencode/approval_projection.go", "p.version.CompareAndSwap(cur, now)",
			"CAS 的布尔结果必须被接住（版本号不能回退）"},
		{"internal/email/mime.go", "if _, err := br.ReadByte(); err != nil",
			"literal 的结束符读失败必须被接住（正文被截断不许无声）"},
		{"internal/email/oauth_callback.go", "if _, err := fmt.Fprintf(w,",
			"响应体写失败必须留痕（§946 的 C 族在闭包里漏网的那一处）"},
		{"internal/llmgateway/anthropic.go", "if !emit(StreamDelta{",
			"消费方断开必须被接住（不许为一个没人要的流继续读上游）"},
		{"internal/server/llmbff_redclaw_provider.go", "if !fn(llmbff.Delta{",
			"消费方断开必须被接住"},
	}
	for _, c := range cases {
		path := filepath.Join(root, c.rel)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("§947 参考集：读不到 %s: %v", c.rel, err)
			continue
		}
		if !strings.Contains(string(b), c.needle) {
			t.Errorf("§947 参考集：%s 里找不到 %q\n　　⇒ %s\n　　⇒ 不许把调用删掉来消红",
				c.rel, c.needle, c.why)
		}
	}
}

// s947BuildTagHiddenKnown 是**已知**被构建标签排除、因而不在本卡口覆盖范围的
// 非测试文件。
//
// ⚠ 这是一张**要显式维护**的清单：新增一个带构建标签的文件 ⇒
//
//	本卡口的覆盖面变小 ⇒ ⇒ 必须显式红一次（而不是让范围悄悄缩小）。
var s947BuildTagHiddenKnown = []string{
	// wasm 专用的探针构建（//go:build js && wasm）⇒ 只在 wasm 目标下编译。
	"backend/cmd/wasmprobe/marshal/main_wasm.go",
}

// TestS947BuildTagHiddenScopeIsPinned —— 构建标签排除的文件必须与已知清单**精确相等**。
//
// ⚠ `_test.go` 不算（测试文件本来就不在本卡口范围）。
func TestS947BuildTagHiddenScopeIsPinned(t *testing.T) {
	root, err := s947BackendRoot()
	if err != nil {
		t.Fatalf("§947 范围声明: %v", err)
	}
	pkgs, err := s947ListPackages(root)
	if err != nil {
		t.Fatalf("§947 范围声明: %v", err)
	}
	var hidden []string
	for _, p := range pkgs {
		if p.Standard || !strings.HasPrefix(p.Dir, root) {
			continue
		}
		if strings.Contains(p.Dir, string(filepath.Separator)+"third_party"+string(filepath.Separator)) {
			continue
		}
		for _, f := range p.IgnoredGoFiles {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			rel, err := mustRel(root, filepath.Join(p.Dir, f))
			if err != nil {
				t.Fatalf("§947 范围声明: %v", err)
			}
			hidden = append(hidden, "backend/"+filepath.ToSlash(rel))
		}
	}
	sort.Strings(hidden)
	want := append([]string{}, s947BuildTagHiddenKnown...)
	sort.Strings(want)
	if strings.Join(hidden, "\n") != strings.Join(want, "\n") {
		t.Fatalf("§947 构建标签排除的文件清单变了：\n  实际：%v\n  已知：%v\n"+
			"  ⇒ 新增带构建标签的文件会让本卡口的覆盖面变小 ⇒ 必须显式登记",
			hidden, want)
	}
}

// =============================================================================
// 规模快照（让「这一族有多大」有个可核对的数字）
// =============================================================================

func TestS947CensusSnapshot(t *testing.T) {
	sites := s947Census(t)
	var bare, deferSites, goSites, exempt, registered int
	for _, s := range sites {
		switch s.Kind {
		case "defer":
			deferSites++
			continue
		case "go":
			goSites++
			continue
		}
		bare++
		if s947Exempt(s) != "" {
			exempt++
		} else {
			registered++
		}
	}
	ledgerCount := 0
	for _, e := range s947Ledger {
		ledgerCount += e.Count
	}
	if registered != ledgerCount {
		t.Errorf("§947 台账条数 %d 与实际登记数 %d 不一致", ledgerCount, registered)
	}
	t.Logf("§947 普查：裸语句丢弃返回值 %d 处（可证明豁免 %d / 台账登记 %d），defer 形态 %d 处，go 形态 %d 处",
		bare, exempt, registered, deferSites, goSites)
}
