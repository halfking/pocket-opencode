package repohygiene

// bounded_read_test.go — §952 卡口：**读进内存的字节数必须有上界**。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，不许把「不知道多大」的东西整个读进内存。**
//
// # 为什么（§952 的实测依据，不是推理）
//
//	§950 给每个出站 HTTP 请求加了**时间上界**（`http.Client.Timeout`）。
//	⇒⇒⇒ 但**时间上界不是大小上界**：30 秒内一台本机可以送出 30 GiB。
//	⇒⇒⇒ ⇒ 于是「上游返回一个超大响应体」这条路径，在 §950 之后**仍然能
//	　　　把 pocketd 的内存打穿** —— §950 治的是「等太久」，§952 治的是
//	　　　「读太多」，**两件事**。
//
//	★ 这也直接命中属主的核心诉求：「会议时长要能无限长，只取决于存储大小」
//	⇒⇒⇒ ⇒ 一个**没有大小上界**的读 = 会议越长、或上游越坏，
//	　　　 pocketd 的内存占用就越不受控 ⇒⇒⇒ ⇒ 「时长无限长」就变成了
//	　　　 「内存也无限长」⇒⇒⇒ ⇒ 必须是**两个独立的杠杆**，不是一个。
//
// # 普查的**形状**（§951 的教训：「普查必须认『绑定形态』」）
//
//	本卡口的普查**不是**「grep `io.ReadAll`」：
//
//	① **调用形态**：`io.ReadAll` / `json.NewDecoder` / `io.Copy` /
//	   `sizedread.All|Snippet|Limit` —— 五族。
//	② **绑定形态**（★ 这一族是本卡口**施工时自己**踩出来的）：
//	   - `limited := io.LimitReader(r.Body, N)`　　⇒ 已有
//	   - `limited := http.MaxBytesReader(w, r.Body, N)` ⇒ 已有
//	   - `a := io.LimitReader(...); b := a`　⇒ 迭代到不动点
//	③ **接收者形态**：`<recv>.Body` 的接收者静态类型决定它是
//	   `*http.Request`（入站）还是 `*http.Response`（出站）
//	   ⇒⇒⇒ ⇒ **两者同形**（字段类型都是 `io.ReadCloser`）
//	   ⇒⇒⇒ ⇒ 只能看**接收者变量**的类型。
//
// # 口径
//
//	① 受检范围：backend 非测试代码
//	   （排除 `third_party/`、`internal/sizedread/` 自身、见「范围声明」）。
//	② **出站响应体**（接收者是 `*http.Response`）的每一次消费必须是：
//	   - `io.ReadAll(io.LimitReader(body, N))`　　　（仓内已有的形态）
//	   - `sizedread.All(body, N)`　　　　　　　　　（**超限 ⇒ ErrTooLarge**）
//	   - `sizedread.Snippet(body, N)`　　　　　　　　（截断；只给错误文案用）
//	   - `json.NewDecoder(sizedread.Limit(body, N))`（**超限 ⇒ 解码器带出来**）
//	   ⇒⇒⇒ **不许**出现「直接读 body」的形态。
//	③ **入站请求体**（接收者是 `*http.Request`）：
//	   - 读取点有界（`io.LimitReader` / `http.MaxBytesReader` / `sizedread.*`）
//	     ⇒ 有界；
//	   - 或同函数内 `<recv>.Body = http.MaxBytesReader(...)` ⇒ 有界；
//	   - 或落在 `internal/server` 包内 ⇒ 由**全局限流中间件**兜住
//	     （`requestBodyLimitMiddleware`，由本卡口单独断言它存在且挂在 mux 上）。
//	④ **已在内存里的字节**（`bytes.NewReader` / `strings.NewReader`）
//	   **不判**：它的长度由**生产者**决定，而生产者的读取点**在册**。
//	⑤ **其它 reader**（文件 / MIME part / 解码器…）必须**逐条登记在册**，
//	   并写清「上界由谁提供」。
//	⑥ 每个上限 N 必须是**正常量**（`types` 里能取到常量值且 > 0）
//	   ⇒⇒⇒ 「上限写成 0」「上限来自某个可能为 0 的变量」都不算数。
//
// # 为什么用 go/types 走 AST（而不是 grep）
//
//	★ **`.Body` 这个字段名在请求与响应上完全同形**：
//	  `*http.Request.Body` 与 `*http.Response.Body` 的**字段类型都是
//	  `io.ReadCloser`** ⇒⇒⇒ **纯语法（甚至纯字段类型）都分不开**。
//	★ §943/§945/§947 的教训：「grep 只看到了我让它看的形态」。
//	★ §951 的教训：「普查必须认『绑定形态』」—— 这里认的是
//	  「**局部变量绑定的限流器**」与「**接收者变量**的类型」。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① `*_test.go` 不是生产路径 ⇒ 排除。
//	② `backend/third_party/` 是上游 vendored ⇒ 排除。
//	③ **`internal/sizedread/` 自身**：它**就是**那个限流器
//	  （`io.ReadAll(io.LimitReader(r, max))` 是它的实现，不是它的漏洞）
//	  ⇒⇒⇒ ⇒ 与 §950 排除 `internal/outbound` 同理 ⇒ **SELF 机制**。
//	④ **`cmd/_` 前缀的目录**：`go list ./...` **看不见**它们
//	  （go 工具链按约定忽略下划线开头的目录）⇒⇒⇒ ⇒ 它们**不在本卡口的
//	  普查范围**内 ⇒⇒⇒ 但 `cmd/_wecomprobe/main.go` 已在本节**顺手修好**
//	  （见主文档 §952.7 ⑨）。
//	⑤ **前端不在本卡口范围**：前端的读是 `res.arrayBuffer()` / `res.blob()`，
//	  它们的量纲是「用户点的那一个文件」，不是「对端给的任意流」；
//	  §951 的 `check-fetch-bounds.mjs` 已经把**时间**上界守住。
//	⑥ 本卡口只看**后端**。

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// =============================================================================
// 普查结果的数据结构
// =============================================================================

// s952Site 是「一处把字节读进内存的调用点」。
type s952Site struct {
	Rel     string // 仓库相对路径（backend/...）
	Func    string // 所属函数
	Line    int
	Form    string // 见 s952Form* 常量
	Src     string // 见 s952Src* 常量
	Arg     string // 实参的源码文本
	Cap     string // 上限的源码文本（没有则空）
	CapOK   bool   // 上限是**正常量**
	Bounded bool   // 判定结果：是否**已经**有大小上界
	Why     string // 依据 / 缺口
}

// s952Form* 是「读」的形态常量。
const (
	s952FormReadAll  = "readall"  // io.ReadAll(X)
	s952FormDecode   = "decode"   // json.NewDecoder(X)
	s952FormCopy     = "copy"     // io.Copy(dst, X)　—— dst 是内存时才算
	s952FormSizedAll = "sizedall" // sizedread.All(X, N)
	s952FormSizedSnp = "sizedsnip"
	s952FormSizedLim = "sizedlim" // sizedread.Limit(X, N)　—— **包装器**，不是读
)

// s952Src* 是「字节从哪来」的常量。
const (
	s952SrcResp  = "resp"  // 出站响应体
	s952SrcReq   = "req"   // 入站请求体
	s952SrcMem   = "mem"   // 已在内存里的字节
	s952SrcOther = "other" // 其它 reader
)

var (
	s952Once  sync.Once
	s952Sites []s952Site
	s952Err   error
)

// s952Census 跑一次普查（进程内缓存）。
//
// ⚠ 缓存**必须和错误一起存**（否则第一次失败后后续断言拿到「空普查」而假绿
// ⇒ 那就是「门绿 ≠ 量具有牙」）。
func s952Census(t *testing.T) []s952Site {
	t.Helper()
	s952Once.Do(func() { s952Sites, s952Err = s952CensusRun() })
	if s952Err != nil {
		t.Fatalf("§952 普查失败: %v", errText(s952Err))
	}
	return s952Sites
}

// =============================================================================
// 普查器
// =============================================================================

func s952CensusRun() ([]s952Site, error) {
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

	var sites []s952Site
	var typeErrs []string
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
		// ★ SELF 机制：限流器自身不在被检范围（§950 对 internal/outbound 同理）。
		if strings.HasSuffix(p.Dir, string(filepath.Separator)+"internal"+string(filepath.Separator)+"sizedread") {
			continue
		}
		if len(p.CgoFiles) > 0 {
			continue
		}
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

		sites = append(sites, s952ClassifyFiles(fset, info, parsed, rels)...)
	}
	if len(typeErrs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(typeErrs, "\n  - "))
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Rel != sites[j].Rel {
			return sites[i].Rel < sites[j].Rel
		}
		return sites[i].Line < sites[j].Line
	})
	return sites, nil
}

// s952ClassifyFiles 对一组已解析并已类型检查的文件做分类。
func s952ClassifyFiles(fset *token.FileSet, info *types.Info,
	parsed []*ast.File, rels []string) []s952Site {
	var out []s952Site
	for i, af := range parsed {
		out = append(out, s952WalkFile(fset, info, af, rels[i])...)
	}
	return out
}

// s952FuncCtx 是一个函数内的上下文。
type s952FuncCtx struct {
	fset *token.FileSet
	info *types.Info
	fd   *ast.FuncDecl
	rel  string
	// boundedBodies：`.Body` 被 `http.MaxBytesReader` 限流过的接收者变量名
	boundedBodies map[string]bool
	// bindings：局部变量 → 它绑定的「限流器」形态（迭代到不动点）
	bindings map[string]s952Binding
}

// s952Binding 是「一个局部变量绑定的限流器」。
type s952Binding struct {
	Cap   string
	CapOK bool
}

// s952WalkFile 走一个文件里的所有函数声明。
func s952WalkFile(fset *token.FileSet, info *types.Info, file *ast.File, rel string) []s952Site {
	var out []s952Site
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ctx := &s952FuncCtx{
			fset:          fset,
			info:          info,
			fd:            fd,
			rel:           rel,
			boundedBodies: s952BoundedBodyVars(fd.Body),
			bindings:      map[string]s952Binding{},
		}
		ctx.s952ResolveBindings()

		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// ★ 顺带收集「台账 helper 的调用点」（供台账核实用）。
			if id, ok := call.Fun.(*ast.Ident); ok && s952LedgerFuncs[id.Name] {
				s952LedgerCallSink = append(s952LedgerCallSink, s952LedgerCall{
					Rel:    rel,
					Line:   fset.Position(call.Pos()).Line,
					Callee: id.Name,
					CapOK:  len(call.Args) >= 2 && ctx.s952IsPositiveConst(call.Args[1]),
				})
			}
			form, ok := s952CallForm(call)
			if !ok {
				return true
			}
			out = append(out, ctx.s952SiteFor(call, form))
			return true
		})
	}
	return out
}

// s952CallForm 判一个调用属于哪一族「读」。
func s952CallForm(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch {
	case s952IsPkgSel(sel, "io", "ReadAll"):
		return s952FormReadAll, true
	case s952IsPkgSel(sel, "io", "Copy"):
		return s952FormCopy, true
	case s952IsPkgSel(sel, "json", "NewDecoder"):
		return s952FormDecode, true
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sizedread" {
		switch sel.Sel.Name {
		case "All":
			return s952FormSizedAll, true
		case "Snippet":
			return s952FormSizedSnp, true
		case "Limit":
			return s952FormSizedLim, true
		}
	}
	return "", false
}

// s952SiteFor 给一处读定类并判定是否有界。
func (c *s952FuncCtx) s952SiteFor(call *ast.CallExpr, form string) s952Site {
	site := s952Site{
		Rel:  c.rel,
		Func: c.fd.Name.Name,
		Line: c.fset.Position(call.Pos()).Line,
		Form: form,
	}
	var arg ast.Expr
	switch form {
	case s952FormCopy:
		// io.Copy(dst, src)：源是第 2 个实参。
		if len(call.Args) == 2 {
			arg = call.Args[1]
		}
	default:
		if len(call.Args) >= 1 {
			arg = call.Args[0]
		}
	}
	if arg == nil {
		site.Src = s952SrcOther
		site.Why = "实参缺失"
		return site
	}
	site.Arg = s947ExprText(arg)

	// 上限：sizedread.* 的第 2 个实参
	if form == s952FormSizedAll || form == s952FormSizedSnp || form == s952FormSizedLim {
		if len(call.Args) >= 2 {
			site.Cap = s947ExprText(call.Args[1])
			site.CapOK = c.s952IsPositiveConst(call.Args[1])
		}
	}

	// 读的是什么？
	src, cap, capOK, why := c.s952Resolve(arg, 0)
	site.Src = src
	if site.Cap == "" {
		site.Cap = cap
		site.CapOK = capOK
	}
	site.Why = why

	switch form {
	case s952FormCopy:
		// 只有「拷进内存」才是内存读；拷进文件 / io.Discard 不是。
		site.Bounded = true
		if !c.s952IsMemorySink(call) {
			site.Src = s952SrcMem
			site.Why = "拷进非内存目标（文件 / io.Discard）⇒ 不是内存读"
			return site
		}
		site.Bounded = capOK && cap != ""
	case s952FormReadAll, s952FormDecode:
		switch src {
		case s952SrcMem:
			// 已在内存里的字节 ⇒ 长度由**生产者**决定（生产者在册）。
			site.Bounded = true
			site.Why = "已在内存里的字节（长度由生产者决定，生产者在册）"
		case s952SrcReq:
			// 入站请求体：读取点有界（`io.LimitReader(r.Body, N)` 等）
			// ⇒ 或同函数的 MaxBytesReader ⇒ 或 internal/server 的全局中间件。
			site.Bounded = (capOK && cap != "") || c.s952ReqBodyBounded(arg)
		default:
			site.Bounded = capOK && cap != ""
		}
	case s952FormSizedAll, s952FormSizedSnp, s952FormSizedLim:
		// 这三族自己带上限 ⇒ 只需要上限是**正常量**。
		site.Bounded = site.CapOK
		if !site.CapOK {
			site.Why = "上限不是**正常量**（或没有上限）"
		}
	}
	return site
}

// s952IsMemorySink 判 io.Copy 的目标是**内存**（而不是文件 / io.Discard）。
//
// ⚠ `io.Discard` 的静态类型就是 `io.Writer` ⇒ **光看类型分不出来**
// ⇒⇒⇒ 必须先看**实参文本**是不是 `io.Discard`。
func (c *s952FuncCtx) s952IsMemorySink(call *ast.CallExpr) bool {
	if len(call.Args) < 1 {
		return false
	}
	if s947ExprText(call.Args[0]) == "io.Discard" {
		return false
	}
	t := c.s952TypeOf(call.Args[0])
	switch t {
	case "*bytes.Buffer", "*bytes.Reader", "*strings.Builder":
		return true
		// ⚠ `io.Discard` 的静态类型是 `io.Writer` ⇒ 它**不在**这个清单里
		//   ⇒ 天然被当成「非内存目标」（把 body 丢掉 ≠ 把 body 读进内存）。
		//   ⇒⇒⇒ 改这个清单时要记得它 —— §952 变异 M17 就是拿这一条做的。
	}
	// 类型不认识 ⇒ 保守当作**不是**内存（避免误报）。
	return false
}

// s952Resolve 判定一个表达式是不是「有界的 reader」，并给出它的来源与上限。
func (c *s952FuncCtx) s952Resolve(expr ast.Expr, depth int) (src string, cap string, capOK bool, why string) {
	if depth > 8 {
		return s952SrcOther, "", false, "表达式嵌套过深（判据自身放弃，保守判无界）"
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			// io.LimitReader(r, N)
			if s952IsPkgSel(sel, "io", "LimitReader") && len(e.Args) == 2 {
				innerSrc, _, _, _ := c.s952Resolve(e.Args[0], depth+1)
				return innerSrc, s947ExprText(e.Args[1]), c.s952IsPositiveConst(e.Args[1]),
					"io.LimitReader"
			}
			// http.MaxBytesReader(w, r, N)
			if s952IsPkgSel(sel, "http", "MaxBytesReader") && len(e.Args) == 3 {
				innerSrc, _, _, _ := c.s952Resolve(e.Args[1], depth+1)
				return innerSrc, s947ExprText(e.Args[2]), c.s952IsPositiveConst(e.Args[2]),
					"http.MaxBytesReader"
			}
			// sizedread.Limit(r, N)
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sizedread" && sel.Sel.Name == "Limit" && len(e.Args) == 2 {
				innerSrc, _, _, _ := c.s952Resolve(e.Args[0], depth+1)
				return innerSrc, s947ExprText(e.Args[1]), c.s952IsPositiveConst(e.Args[1]),
					"sizedread.Limit"
			}
			// bytes.NewReader / strings.NewReader ⇒ 已在内存里
			if s952IsPkgSel(sel, "bytes", "NewReader") || s952IsPkgSel(sel, "strings", "NewReader") {
				return s952SrcMem, "", false, "已在内存里的字节"
			}
		}
		return s952SrcOther, "", false, "未知 reader 形态：" + s947ExprText(expr)
	case *ast.Ident:
		// ★ 绑定形态：`limited := io.LimitReader(...)` / `http.MaxBytesReader(...)`
		if b, ok := c.bindings[e.Name]; ok {
			return s952SrcOther, b.Cap, b.CapOK, "局部绑定 " + e.Name
		}
		return s952SrcOther, "", false, "未受限流的标识符 " + e.Name
	case *ast.SelectorExpr:
		if e.Sel.Name == "Body" {
			t := c.s952TypeOf(e.X)
			switch t {
			case "*net/http.Response":
				return s952SrcResp, "", false, "出站响应体（**没有大小上界**）"
			case "*net/http.Request":
				return s952SrcReq, "", false, "入站请求体"
			default:
				return s952SrcOther, "", false, "`.Body` 但接收者类型是 " + t
			}
		}
		return s952SrcOther, "", false, "选择器 " + s947ExprText(expr)
	default:
		return s952SrcOther, "", false, "其它表达式 " + s947ExprText(expr)
	}
}

// s952ReqBodyBounded 判一个入站请求体的读取是否有界。
func (c *s952FuncCtx) s952ReqBodyBounded(arg ast.Expr) bool {
	sel, ok := arg.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Body" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if c.boundedBodies[id.Name] {
		return true
	}
	// `internal/server` 包由**全局限流中间件**兜住（由本卡口单独断言）。
	return strings.HasPrefix(c.rel, "backend/internal/server/")
}

// s952TypeOf 取一个表达式的静态类型字符串。
func (c *s952FuncCtx) s952TypeOf(e ast.Expr) string {
	if c.info == nil {
		return ""
	}
	if t := c.info.TypeOf(e); t != nil {
		return t.String()
	}
	return ""
}

// s952IsPositiveConst 判一个表达式是不是**正整数常量**。
//
// ⚠ 「上限是常量」是**硬要求**：上限来自一个可能为 0 的变量时，
//
//	「有上界」只是**约定**不是**保证**（§950 的教训）。
func (c *s952FuncCtx) s952IsPositiveConst(e ast.Expr) bool {
	if c.info == nil {
		return false
	}
	tv, ok := c.info.Types[e]
	if !ok || tv.Value == nil {
		return false
	}
	i, ok := constant.Int64Val(constant.ToInt(tv.Value))
	if !ok {
		return false
	}
	return i > 0
}

// s952BoundedBodyVars 找出本函数里「`.Body` 被装上限流器」的接收者变量名。
//
// ⚠ 只认 `http.MaxBytesReader` —— 因为它是 stdlib 里**唯一**能把限流后的
//
//	reader 装回 `.Body` 字段的构造口（`io.LimitReader` 返回 `io.Reader`，
//	类型装不回去）。
func s952BoundedBodyVars(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Body" {
				continue
			}
			if i >= len(as.Rhs) {
				continue
			}
			call, ok := as.Rhs[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			cs, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !s952IsPkgSel(cs, "http", "MaxBytesReader") {
				continue
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// s952ResolveBindings 解析「局部变量 ← 限流器」的绑定，**迭代到不动点**。
//
// ⚠ 迭代到不动点是必要的：`a := io.LimitReader(r.Body, N); b := a`
//
//	只解一层会漏掉 `b`（§951 的教训）。
func (c *s952FuncCtx) s952ResolveBindings() {
	for {
		changed := false
		ast.Inspect(c.fd.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || as.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range as.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(as.Rhs) {
					continue
				}
				if _, exists := c.bindings[id.Name]; exists {
					continue
				}
				call, ok := as.Rhs[i].(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				var capExpr ast.Expr
				switch {
				case s952IsPkgSel(sel, "io", "LimitReader") && len(call.Args) == 2:
					capExpr = call.Args[1]
				case s952IsPkgSel(sel, "http", "MaxBytesReader") && len(call.Args) == 3:
					capExpr = call.Args[2]
				case s952IsSizedreadLimit(call) && len(call.Args) == 2:
					capExpr = call.Args[1]
				default:
					continue
				}
				c.bindings[id.Name] = s952Binding{
					Cap:   s947ExprText(capExpr),
					CapOK: c.s952IsPositiveConst(capExpr),
				}
				changed = true
			}
			return true
		})
		if !changed {
			return
		}
	}
}

// s952IsSizedreadLimit 判一个调用是不是 `sizedread.Limit(...)`。
func s952IsSizedreadLimit(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Limit" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "sizedread"
}

// s952IsPkgSel 判「包名.函数名」这个形态。
func s952IsPkgSel(sel *ast.SelectorExpr, pkg, fn string) bool {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == pkg && sel.Sel.Name == fn
}

// s952ClassifySource 对一段**合成源码**做分类（自证臂用）。
//
// ⚠ 只用 stdlib 的 source importer ⇒ 自证臂不依赖仓内任何东西。
// ⚠ fset 与 importer **全局复用**：source importer 内部按 import path 缓存
//
//	已类型检查的包 ⇒⇒⇒ 复用才不至于把 net/http 的源码类型检查做 10 遍。
var (
	s952SynthOnce sync.Once
	s952SynthFset *token.FileSet
	s952SynthImp  types.Importer
	s952SynthSeq  int
)

func s952ClassifySource(src, rel string) ([]s952Site, error) {
	s952SynthOnce.Do(func() {
		s952SynthFset = token.NewFileSet()
		s952SynthImp = importer.ForCompiler(s952SynthFset, "source", nil)
	})
	fset := s952SynthFset
	s952SynthSeq++
	name := fmt.Sprintf("synthetic_%d.go", s952SynthSeq)
	af, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("解析合成源码失败: %w", err)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: s952SynthImp}
	if _, err := conf.Check("synthetic", fset, []*ast.File{af}, info); err != nil {
		return nil, fmt.Errorf("类型检查合成源码失败: %w", err)
	}
	return s952ClassifyFiles(fset, info, []*ast.File{af}, []string{rel}), nil
}

// =============================================================================
// 台账
// =============================================================================

// s952LedgerEntry 是台账的一条豁免。
type s952LedgerEntry struct {
	// Reason 是「上界由谁提供」的书面理由。
	Reason string
	// WantCap 是该处上限**必须长成的样子**（源码文本）。
	//
	// ⚠ **为什么必须钉住上限的样子**（§952 变异 M14 逼出来的）：
	//
	//	台账只记「哪个函数是豁免的」⇒⇒⇒ 则「那个函数把上限删掉了」
	//	**察觉不到** —— 它从「有界（上限由调用方给）」变成了
	//	「完全无界」，但两者的**分类**都是「src=other ⇒ 无界」
	//	⇒⇒⇒ ⇒ 台账必须把**上限的形态**也钉住。
	WantCap string
}

// s952Ledger 是「上限由**调用方**提供」的豁免清单。
//
// ⚠ 口径：这类 helper **不是**没有上界，而是**上界在参数上**。
//
//	本卡口不认「参数」当上界（§950 的教训：「调用方记得传一个正数」
//	是**约定**不是**保证**）⇒⇒⇒ ⇒ 它们必须逐条登记，
//	**并且**由 `TestS952LedgerCallersPassConstants` 逐个调用点核实
//	「每个调用方都给了正常量」⇒⇒⇒ ⇒ 台账的每一条都带**可执行的证明**。
var s952Ledger = map[string]s952LedgerEntry{
	"backend/internal/adapter/opencode_config.go::readTruncated": {
		Reason:  "上限由参数 n 给；调用点 opencode_config.go:126 传字面量 256（错误消息片段）",
		WantCap: "int64(n)",
	},
	"backend/internal/agent/helpers.go::readBody": {
		Reason: "上限由参数 maxLen 给；4 个调用点（transport_http.go:90/180/240、" +
			"transport_ws.go:82）全传字面量 200（错误消息片段）",
		WantCap: "int64(maxLen)",
	},
	"backend/internal/rss/url.go::readLimited": {
		Reason: "上限由参数 max 给；调用点 discovery.go:54 传常量 DefaultMaxBodyBytes、" +
			"fetcher.go:91 传可配置项 f.maxBodyBytes（**恒为正**：SetMaxBodyBytes 只接受 n > 0；" +
			"且上限 <= 0 时**失败关闭**）⇒ 行为证明见 internal/rss/read_limited_test.go",
		WantCap: "max + 1",
	},
}

// s952LedgerKeyForFunc 按 helper 名找它在台账里的键。
func s952LedgerKeyForFunc(fn string) string {
	for k := range s952Ledger {
		if strings.HasSuffix(k, "::"+fn) {
			return k
		}
	}
	return ""
}

// s952LedgerFuncs 是台账里那些 helper 的函数名（用于核实调用点）。
var s952LedgerFuncs = map[string]bool{
	"readTruncated": true,
	"readBody":      true,
	"readLimited":   true,
}

// s952LedgerCall 是一次「台账 helper 的调用」。
type s952LedgerCall struct {
	Rel    string
	Line   int
	Callee string
	CapOK  bool // 实参是不是**正常量**
}

var (
	s952LedgerOnce  sync.Once
	s952LedgerCalls []s952LedgerCall
	// s952LedgerCallSink 由普查顺带填充（普查跑完才有内容）。
	s952LedgerCallSink []s952LedgerCall
)

// =============================================================================
// 断言
// =============================================================================

// TestS952CensusCountsArePinned 钉住普查的**绝对条数**。
//
// ⚠ 为什么钉绝对条数（§943/§947 的纪律）：只钉「无界 = 0」的话，
//
//	量具**整个失效**（扫不出任何东西）时也会绿 ⇒⇒⇒ ⇒ 必须同时钉住
//	「一共扫到多少处」与「其中多少处有界」。
func TestS952CensusCountsArePinned(t *testing.T) {
	sites := s952Census(t)
	byForm := map[string]int{}
	bySrc := map[string]int{}
	bounded := 0
	for _, s := range sites {
		byForm[s.Form]++
		bySrc[s.Src]++
		if s.Bounded {
			bounded++
		}
	}
	// 2026-10-26：+1 —— ACC 审批应答的请求体解码（MaxBytesReader ⇒ 有界）。
	if len(sites) != 265 {
		t.Errorf("§952 普查到 %d 处「读进内存」的调用点（期望 265）\n"+
			"⇒ 绝对条数是**量具有牙**的证据：量具整个失效时这个数会变 ⇒⇒⇒ "+
			"⇒ 若这一条变了，请先确认是「代码真的多了/少了读点」还是「量具瞎了」",
			len(sites))
	}
	want := map[string]int{
		s952FormReadAll:  52,
		s952FormDecode:   142,
		s952FormCopy:     3,
		s952FormSizedAll: 16,
		s952FormSizedSnp: 17,
		s952FormSizedLim: 35,
	}
	for form, n := range want {
		if byForm[form] != n {
			t.Errorf("形态 %s 扫到 %d 处（期望 %d）", form, byForm[form], n)
		}
	}
	wantSrc := map[string]int{
		s952SrcResp:  122,
		s952SrcReq:   112,
		s952SrcMem:   8,
		s952SrcOther: 23,
	}
	for src, n := range wantSrc {
		if bySrc[src] != n {
			t.Errorf("来源 %s 扫到 %d 处（期望 %d）", src, bySrc[src], n)
		}
	}
	if bounded != 262 {
		t.Errorf("有界 %d 处（期望 262）⇒⇒⇒ 无界处应恰好是台账里的 3 条", bounded)
	}
}

// TestS952NoUnboundedReadsExceptLedger 钉住「无界的读点**恰好**是台账那几条」。
func TestS952NoUnboundedReadsExceptLedger(t *testing.T) {
	sites := s952Census(t)
	for _, s := range sites {
		if s.Bounded {
			continue
		}
		key := s.Rel + "::" + s.Func
		if _, ok := s952Ledger[key]; !ok {
			t.Errorf("★ 发现**没有登记**的无界读点：%s:%d %s\n"+
				"⇒ 形态 %s（实参 %s，上限 %q）\n"+
				"⇒ 依据：%s\n"+
				"⇒⇒⇒ 两种可能：(a) 真缺陷 ⇒ 修它；(b) 有正当理由 ⇒ 登记进 s952Ledger\n"+
				"⇒⇒⇒ ⚠ **不许**为了让门绿而把它塞进台账：台账每一条都要写清「上界由谁提供」",
				s.Rel, s.Line, s.Func, s.Form, s.Arg, s.Cap, s.Why)
		}
	}
	if len(s952Ledger) != 3 {
		t.Errorf("台账有 %d 条（期望 3）", len(s952Ledger))
	}
}

// TestS952LedgerIsNotStale 钉住「台账不许过期」。
//
// ⚠ §951 的教训：「改一本台账 ⇒ 别的批次的变异锚点会过期」。
//
//	⇒⇒⇒ 台账里每一条都必须对应一个**真实存在**的无界读点。
func TestS952LedgerIsNotStale(t *testing.T) {
	sites := s952Census(t)
	live := map[string]bool{}
	for _, s := range sites {
		if !s.Bounded {
			live[s.Rel+"::"+s.Func] = true
		}
	}
	for key, entry := range s952Ledger {
		if !live[key] {
			t.Errorf("台账条目 %q **已过期**（代码里已经没有这个无界读点了）\n"+
				"⇒ 理由：%s\n"+
				"⇒⇒⇒ 要么代码被修好了（删掉这条），要么台账键写错了（改键）",
				key, entry.Reason)
		}
	}
}

// TestS952LedgerCapFormIsPinned 钉住「台账里的上限**形态**没被改掉」。
//
// ⚠ **这条是 §952 变异 M14 逼出来的**（见 s952LedgerEntry.WantCap 的注释）：
//
//	台账原本只记「哪个函数豁免」⇒⇒⇒ 「那个函数把上限删了」察觉不到
//	⇒⇒⇒ ⇒ 现在每条台账都钉住上限的**源码形态**。
func TestS952LedgerCapFormIsPinned(t *testing.T) {
	sites := s952Census(t)
	seen := map[string]bool{}
	for _, s := range sites {
		key := s.Rel + "::" + s.Func
		entry, ok := s952Ledger[key]
		if !ok {
			continue
		}
		seen[key] = true
		if s.Cap != entry.WantCap {
			t.Errorf("台账条目 %q 的上限形态 = %q（期望 %q）\n"+
				"⇒ 理由：%s\n"+
				"⇒⇒⇒ ⚠ 这条台账的**前提**变了：「上限由调用方给」已经名存实亡\n"+
				"⇒⇒⇒ ⇒ 要么改台账（并重新核实调用点），要么把上限改回来",
				key, s.Cap, entry.WantCap, entry.Reason)
		}
	}
	for key := range s952Ledger {
		if !seen[key] {
			t.Errorf("台账条目 %q 在代码里**找不到对应的读点**", key)
		}
	}
}

// TestS952LedgerCallersPassConstants 钉住「台账里的每个调用点都给了正常量」。
//
// ⚠ 这是台账的**可执行证明**：台账说「上限由调用方给」，
//
//	那就必须逐个调用点核实「调用方真的给了正数」。
//	⇒⇒⇒ 否则「由调用方给」就退化成一句**没人核实的约定**。
func TestS952LedgerCallersPassConstants(t *testing.T) {
	// ⚠ 先跑普查：台账调用点是**普查顺带**收集的 ⇒ 顺序不能反。
	s952Census(t)
	s952LedgerOnce.Do(func() { s952LedgerCalls = s952LedgerCallSink })
	if len(s952LedgerCalls) == 0 {
		t.Fatalf("台账 helper 的调用点一个都没扫到 ⇒ 量具坏了（拒绝给结论）")
	}
	seen := map[string]int{}
	for _, c := range s952LedgerCalls {
		seen[c.Callee]++
		if c.CapOK {
			continue
		}
		// 上限不是常量 ⇒ 只在台账写明「**恒为正**」或「**失败关闭**」时放行。
		//
		// ⚠ 放行不是「相信台账」：每条这样的台账都必须**指名**它的行为证明
		//   （本仓里是 internal/rss/read_limited_test.go）。
		key := s952LedgerKeyForFunc(c.Callee)
		reason := s952Ledger[key].Reason
		if !strings.Contains(reason, "恒为正") && !strings.Contains(reason, "失败关闭") {
			t.Errorf("台账 helper %s 的调用点 %s:%d 传的**不是正常量**，"+
				"而台账没有写明「恒为正」或「失败关闭」\n"+
				"⇒ 「上限由调用方给」必须在台账里写清**为什么它仍然是有界的**\n"+
				"⇒ 台账原文：%s", c.Callee, c.Rel, c.Line, reason)
		}
	}
	// 逐个 helper 钉住调用点条数（防止有人新加一个调用点却不给上限）。
	want := map[string]int{
		"readTruncated": 1,
		"readBody":      4,
		"readLimited":   2,
	}
	for name, n := range want {
		if seen[name] != n {
			t.Errorf("台账 helper %s 的调用点扫到 %d 个（期望 %d）\n"+
				"⇒ 新增调用点必须重新核实「上限是不是正常量」", name, seen[name], n)
		}
	}
}

// TestS952AllCapsArePositiveConstants 钉住「每个上限都是正常量」。
//
// ⚠ 「上限是常量」是**硬要求**：上限来自一个可能为 0 的变量时，
//
//	「有上界」只是**约定**不是**保证**（§950 的教训）。
func TestS952AllCapsArePositiveConstants(t *testing.T) {
	sites := s952Census(t)
	for _, s := range sites {
		if s.Cap == "" {
			continue
		}
		// 台账那三条的「上限」是**形参** ⇒ 由另一条断言（逐个调用点核实）负责。
		if _, ok := s952Ledger[s.Rel+"::"+s.Func]; ok {
			continue
		}
		if !s.CapOK {
			t.Errorf("%s:%d %s 的上限 %q **不是正常量**\n"+
				"⇒ 上限必须是**编译期就能确定**的正整数常量\n"+
				"⇒⇒⇒ 「上限可能为 0」时，「有上界」只是约定不是保证", s.Rel, s.Line, s.Func, s.Cap)
		}
	}
}

func TestS952GlobalRequestBodyLimitMiddlewareExists(t *testing.T) {
	root, err := s947BackendRoot()
	if err != nil {
		t.Fatalf("取 backend 模块根失败: %v", err)
	}
	path := filepath.Join(root, "internal", "server", "server.go")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	found, applied, hasLimit := s952InspectMiddleware(string(b))
	if !found {
		t.Errorf("找不到 requestBodyLimitMiddleware ⇒ §952 口径 ③ 的「全局兜底」前提不成立")
	}
	if !hasLimit {
		t.Errorf("requestBodyLimitMiddleware 里**没有** http.MaxBytesReader ⇒ " +
			"「入站请求体有全局兜底」这个前提不成立")
	}
	if !applied {
		t.Errorf("requestBodyLimitMiddleware **没有被挂到任何 handler 链上** ⇒ " +
			"「internal/server 包的入站请求体有界」不成立")
	}
}

// s952InspectMiddleware 看一份源码里「全局限流中间件」的三件事：
// 有没有这个中间件 / 它有没有被挂上去 / 它里面有没有真的限流。
//
// ⚠ 做成**纯函数**（吃源码字符串）⇒ 自证臂可以用合成源码喂它
// ⇒⇒⇒ ⇒ 中间件被改坏这件事，判据自己就能发现，不用靠人盯。
func s952InspectMiddleware(src string) (found, applied, hasLimit bool) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "middleware_probe.go", src, 0)
	if err != nil {
		return false, false, false
	}
	for _, decl := range af.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Name.Name == "requestBodyLimitMiddleware" {
			found = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Body" || i >= len(as.Rhs) {
						continue
					}
					call, ok := as.Rhs[i].(*ast.CallExpr)
					if !ok {
						continue
					}
					cs, ok := call.Fun.(*ast.SelectorExpr)
					if ok && s952IsPkgSel(cs, "http", "MaxBytesReader") {
						hasLimit = true
					}
				}
				return true
			})
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if ok && id.Name == "requestBodyLimitMiddleware" {
				applied = true
			}
			return true
		})
	}
	return found, applied, hasLimit
}

// =============================================================================
// 自证臂（证明量具能认全形态、能抓到坏形态）
// =============================================================================

// TestS952SelfVerification 用**合成源码**证明判据的行为。
//
// ⚠ 为什么必须自证（§951 的教训）：「自证臂的价值不在『证明量具能抓』，
//
//	在『证明量具能认全形态』」—— 本节施工时量具自己漏认了三族形态
//	（`http.MaxBytesReader` 绑定 / `io.Discard` 目标 / 入站 body 的读取点限流），
//	都是**自证臂**该抓的。
//
// ⚠ 合成源码里用**局部变量** `sizedread` 代替那个包
//
//	（判据对它的识别是**纯语法**的：只看 `X.Limit` 里 X 是不是叫 sizedread）
//	⇒⇒⇒ ⇒ 自证臂不依赖仓内任何东西 ⇒ 不会因为仓变了而失去意义。
func TestS952SelfVerification(t *testing.T) {
	const prelude = `package synthetic

import (
	"encoding/json"
	"io"
	"net/http"
)

var _ = json.Valid

var sizedread = struct {
	Limit   func(io.Reader, int64) io.Reader
	All     func(io.Reader, int64) ([]byte, error)
	Snippet func(io.Reader, int64) string
}{
	Limit:   func(r io.Reader, n int64) io.Reader { return r },
	All:     func(r io.Reader, n int64) ([]byte, error) { return nil, nil },
	Snippet: func(r io.Reader, n int64) string { return "" },
}

func do(w http.ResponseWriter, r *http.Request, resp *http.Response, data []byte) {
	_ = w
	_ = r
	_ = resp
	_ = data
`

	cases := []struct {
		name      string
		body      string
		wantForm  string
		wantSrc   string
		wantBound bool
	}{
		{"坏①：直接读出站响应体", `
	_, _ = io.ReadAll(resp.Body)`, s952FormReadAll, s952SrcResp, false},
		{"坏②：直接用解码器读出站响应体", `
	_ = json.NewDecoder(resp.Body).Decode(&struct{}{})`, s952FormDecode, s952SrcResp, false},
		{"坏③：入站请求体（不在 internal/server 里 ⇒ 没有全局兜底）", `
	_, _ = io.ReadAll(r.Body)`, s952FormReadAll, s952SrcReq, false},
		{"坏④：上限写成 0", `
	_, _ = sizedread.All(resp.Body, 0)`, s952FormSizedAll, s952SrcResp, false},
		{"好①：仓内已有的形态", `
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))`, s952FormReadAll, s952SrcResp, true},
		{"好②：sizedread.Limit 包住解码器", `
	_ = json.NewDecoder(sizedread.Limit(resp.Body, 1<<20)).Decode(&struct{}{})`, s952FormDecode, s952SrcResp, true},
		{"好③：sizedread.All", `
	_, _ = sizedread.All(resp.Body, 1<<20)`, s952FormSizedAll, s952SrcResp, true},
		{"好④：sizedread.Snippet", `
	_ = sizedread.Snippet(resp.Body, 4096)`, s952FormSizedSnp, s952SrcResp, true},
		{"好⑤：绑定形态（io.LimitReader）", `
	limited := io.LimitReader(resp.Body, 1<<20)
	_, _ = io.ReadAll(limited)`, s952FormReadAll, s952SrcOther, true},
		{"好⑥：绑定形态（http.MaxBytesReader）", `
	limited := http.MaxBytesReader(w, r.Body, 1<<20)
	_, _ = io.ReadAll(limited)`, s952FormReadAll, s952SrcOther, true},
		{"好⑦：已在内存里的字节", `
	_, _ = io.ReadAll(bytes.NewReader(data))`, s952FormReadAll, s952SrcMem, true},
		{"好⑧：io.Discard 不是内存读", `
	_, _ = io.Copy(io.Discard, resp.Body)`, s952FormCopy, s952SrcMem, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := prelude + tc.body + "\n}\n"
			if tc.name == "好⑦：已在内存里的字节" {
				src = "package synthetic\n\nimport (\n\t\"bytes\"\n\t\"io\"\n)\n\nfunc do(data []byte) {\n" +
					"\t_, _ = io.ReadAll(bytes.NewReader(data))\n}\n"
			}
			sites, err := s952ClassifySource(src, "synthetic.go")
			if err != nil {
				t.Fatalf("合成源码分类失败: %v", err)
			}
			var hit *s952Site
			for i := range sites {
				if sites[i].Form == tc.wantForm {
					hit = &sites[i]
					break
				}
			}
			if hit == nil {
				t.Fatalf("合成源码里没扫到形态 %s 的读点（扫到 %d 处）", tc.wantForm, len(sites))
			}
			if hit.Src != tc.wantSrc {
				t.Errorf("来源 = %s（期望 %s）⇒ 判据认错了「字节从哪来」", hit.Src, tc.wantSrc)
			}
			if hit.Bounded != tc.wantBound {
				t.Errorf("有界 = %v（期望 %v）⇒ 判据认错了「有没有上界」\n"+
					"⇒ 依据：%s", hit.Bounded, tc.wantBound, hit.Why)
			}
		})
	}
}

// TestS952MiddlewareSelfVerification 用**合成源码**证明「全局中间件」那条检查有牙。
//
// ⚠ §952 口径 ③ 的依据是「中间件存在 + 被挂上 + 真的限流」三件事
//
//	⇒⇒⇒ 三件事里任何一件坏掉，判据都必须能发现。
func TestS952MiddlewareSelfVerification(t *testing.T) {
	const good = `package p

import "net/http"

func requestBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		next.ServeHTTP(w, r)
	})
}

func build(handler http.Handler) http.Handler {
	return requestBodyLimitMiddleware(handler)
}
`
	cases := []struct {
		name      string
		src       string
		wantFound bool
		wantApp   bool
		wantLimit bool
	}{
		{"好：中间件在、被挂上、真的限流", good, true, true, true},
		{"坏①：中间件没了", `package p

import "net/http"

func build(handler http.Handler) http.Handler {
	return handler
}
`, false, false, false},
		{"坏②：中间件在但没被挂上", `package p

import "net/http"

func requestBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		next.ServeHTTP(w, r)
	})
}
`, true, false, true},
		{"坏③：被挂上了但里面没限流", `package p

import "net/http"

func requestBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func build(handler http.Handler) http.Handler {
	return requestBodyLimitMiddleware(handler)
}
`, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found, applied, hasLimit := s952InspectMiddleware(tc.src)
			if found != tc.wantFound {
				t.Errorf("found = %v（期望 %v）", found, tc.wantFound)
			}
			if applied != tc.wantApp {
				t.Errorf("applied = %v（期望 %v）", applied, tc.wantApp)
			}
			if hasLimit != tc.wantLimit {
				t.Errorf("hasLimit = %v（期望 %v）", hasLimit, tc.wantLimit)
			}
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
