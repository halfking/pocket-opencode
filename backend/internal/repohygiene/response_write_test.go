package repohygiene

// response_write_test.go — §946 卡口：HTTP 响应体写失败不许无声。
//
// # 这道卡口在量什么
//
//	一句话：**「把响应体写出去」这件事，失败必须可见。**
//
//	三族（都要求接收者**可证明**是响应体）：
//
//	① **A 族 · 编码**：`json.NewEncoder(w).Encode(v)` 的结果被丢弃
//	   ⇒ 旧写法**先提交了状态行** ⇒ ⇒⇒ 客户端拿到「200 OK + **空体**」
//	   （§946 实测：status=200 bodyLen=0，**不是**截断）
//	   ⇒ ⇒⇒ 而错误被 `_ =` 吞掉 ⇒ ⇒⇒⇒ 服务端**一个字都没留**
//	   ⇒ ⇒⇒⇒ 前端只看到「Unexpected end of JSON input」，排查时毫无线索。
//	② **B 族 · 写出**：`w.Write(...)` 的结果被丢弃
//	   ⇒ 写失败（客户端断开 / 连接重置）⇒ ⇒⇒ 客户端拿到**截断响应**
//	   ⇒ ⇒⇒ 而服务端**零记录**。
//	③ **C 族 · 格式化写出**：`fmt.Fprintf(w, ...)` 的结果被丢弃
//	   ⇒ SSE 流的写失败 ⇒ ⇒⇒ 流已死却还在继续写（每次都刷失败）。
//
// # 为什么 §945 的卡口管不到这一族（§946 的实测发现）
//
//	§945 的扫描器**只访问 `*ast.AssignStmt`** ⇒ ⇒⇒ **裸语句**形态
//	（`w.Write(...)` / `json.NewEncoder(w).Encode(...)`）**完全不在参考集里**
//	⇒ ⇒⇒ ⇒ §945 的「97 处」是**下界**；那张卡在这条形态上**没有牙**。
//
//	⚠ 但**不能**把 §945 的扫描器改成「所有裸语句都算丢弃」：
//	  裸语句里绝大多数是 **void setter**（`SetX(...)` / `Start(...)` 返回空）
//	  ⇒ ⇒⇒ 要区分「裸语句丢弃了返回值」**必须有类型信息**（go/types）
//	  ⇒ ⇒⇒ ⇒ 本卡口改用**可证明的接收者**收窄：
//	  · `http.ResponseWriter` **形参** ⇒ 它的 `Write` 签名是 `(int, error)`
//	  · `json.NewEncoder(w)` ⇒ `Encode` 签名是 `error`
//	  · `fmt.Fprintf(w, ...)` ⇒ 签名是 `(int, error)`
//	  ⇒ 这三类**不需要类型信息**也能证明「丢的是 error」。
//
// # ⛔ 明确**不在**本卡口范围的（是「不查」，不是「没风险」）
//
//	① `json.Marshal(v)` 结果被丢弃的那 68 处（§946 实测）——
//	   它们**绝大多数不在响应体上**（出站请求体 / DB 的 JSONB 列 / WebSocket
//	   载荷）⇒ ⇒⇒ 判「是不是响应体」需要**数据流**信息（AST 做不到）
//	   ⇒ ⇒⇒ ⇒ 已登记在 §946 文档的「非候选」清单里，**本卡口不覆盖**。
//	   ⇒ ⇒⇒ 其中**确属响应体**的两处（SSE 事件编码）**已修**。
//	② `backend/third_party/`（上游 vendored identity-go）不扫。
//	③ `*_test.go` 与 `testdata/` 不是生产路径，不扫。
//
// # 三层断言（缺一层就退化成「门绿 ≠ 量具有牙」）
//
//	① **三族归零**：全仓不许再有「被丢弃的响应体编码 / 写出 / 格式化写出」。
//	② **自证臂**：拿**已知答案的合成源码**喂给扫描器，证明它**该报的会报**
//	   ⇒ ★ 只断言「没有 X」的测试在扫描器坏掉时照样会绿（扫描器什么都不报
//	   ⇒ ⇒ ⇒ 「没有」就成立 ⇒ 绿）⇒ ⇒⇒ 必须证明它**有牙**。
//	③ **形态钉住**：共享 helper 必须**先编码、后写状态行**
//	   ⇒ ⇒⇒ 光钉「没有丢弃」不够 —— 只记日志而**仍然先写状态行**的话，
//	   ⇒ ⇒⇒ 症状一模一样（200 + 空体）⇒ ⇒⇒ ⇒ 必须钉住**顺序**。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s946ScopeFile 判一个仓库相对路径是否在受检范围内。
func s946ScopeFile(rel string) bool {
	if !strings.HasPrefix(rel, "backend/") || !strings.HasSuffix(rel, ".go") {
		return false
	}
	if strings.Contains(rel, "/testdata/") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	// ⚠ 上游 vendored 代码：本仓改它下次同步上游就会被覆盖 ⇒ 排除。
	if strings.HasPrefix(rel, "backend/third_party/") {
		return false
	}
	return true
}

// s946IsEncoderCall 判一个调用是不是「把值编码进**响应体**」的调用。
//
// ⚠ 只认 `json.NewEncoder(<ResponseWriter>).Encode(v)` 这一种形态：
//
//	· `json.Marshal(v)` 的结果被丢弃**不**算 —— 它的产物通常进的是
//	  请求体 / DB 列 / WS 载荷（§946 实测 68 处），判「是不是响应体」
//	  需要数据流信息 ⇒ ⇒⇒ ⇒ 本卡口不覆盖（已登记为非候选）。
//	· `json.NewEncoder(buf).Encode(v)`（buf 不是响应体）**不**算。
func s946IsEncoderCall(call *ast.CallExpr, rwNames map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Encode" {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	isel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok || isel.Sel.Name != "NewEncoder" {
		return false
	}
	ipkg, ok := isel.X.(*ast.Ident)
	if !ok || ipkg.Name != "json" {
		return false
	}
	// ⚠ 目标必须是**响应体**。
	target, ok := inner.Args[0].(*ast.Ident)
	if !ok || !rwNames[target.Name] {
		return false
	}
	return true
}

// s946IsFmtWriteCall 判一个调用是不是「格式化写进**响应体**」。
//
// ⚠ 覆盖 `fmt.Fprintf` / `fmt.Fprint` / `fmt.Fprintln`。
func s946IsFmtWriteCall(call *ast.CallExpr, rwNames map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return false
	}
	switch sel.Sel.Name {
	case "Fprintf", "Fprint", "Fprintln":
	default:
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	target, ok := call.Args[0].(*ast.Ident)
	return ok && rwNames[target.Name]
}

// s946IsSSEHelperCall 判一个调用是不是 §946 的 SSE 写 helper。
//
// ⚠ 为什么单列一族：SSE 的失败语义是「写失败 = 这条流已经死了」
// 　 ⇒ ⇒⇒ 调用方必须能**中断**它 ⇒ ⇒⇒ ⇒ 而这完全取决于 helper **返不返回**
// 　 ⇒ ⇒⇒ ⇒ 那个错误 ⇒ ⇒⇒ ⇒ 它的结果一旦被丢弃，死流就会一直写。
func s946IsSSEHelperCall(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "s946SSEWritef"
}

// s946ResponseWriterParams 收集一个函数的 `http.ResponseWriter` 形参名。
//
// ⚠ 为什么用**形参类型**而不是「receiver 叫 w」：本仓里 `s.Write(r, ...)` 是
// 　 **审计**日志写入（receiver 是 *Server）—— 用名字匹配会把它误当成
// 　 HTTP 响应体 ⇒ ⇒⇒ 所以必须按**类型**收窄。
func s946ResponseWriterParams(fd *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fd.Type == nil || fd.Type.Params == nil {
		return out
	}
	for _, p := range fd.Type.Params.List {
		sel, ok := p.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "http" || sel.Sel.Name != "ResponseWriter" {
			continue
		}
		for _, n := range p.Names {
			if n.Name != "_" {
				out[n.Name] = true
			}
		}
	}
	return out
}

// s946DiscardedCallsInStmt 取一条语句里「结果被丢弃」的调用。
//
// ⚠ 两种丢弃形态都要认：
//
//	`_ = f()` / `_, _ = f()` ⇒ 显式丢弃（§945 认得的那种）
//	`f()`（裸语句）        ⇒ 隐式丢弃（§945 **认不出**，§946 要认）
//
// ⚠ 判「最后一个左值是 `_`」而不是「有没有 `_`」——Go 的惯例是 error 在最后
// 　 一个返回值 ⇒ `_, err = f()` **不算**丢弃（§945 已经踩过这个坑）。
func s946DiscardedCallsInStmt(stmt ast.Stmt) []*ast.CallExpr {
	switch st := stmt.(type) {
	case *ast.ExprStmt:
		call, ok := st.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
		return []*ast.CallExpr{call}
	case *ast.AssignStmt:
		if len(st.Lhs) == 0 {
			return nil
		}
		last, ok := st.Lhs[len(st.Lhs)-1].(*ast.Ident)
		if !ok || last.Name != "_" {
			return nil
		}
		var out []*ast.CallExpr
		for _, rhs := range st.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				out = append(out, call)
			}
		}
		return out
	}
	return nil
}

// s946Finding 是一条「被丢弃的响应体写出」记录。
type s946Finding struct {
	File string
	Line int
	Text string
	Kind string // "encode" / "write" / "fmt"
}

func (f s946Finding) String() string {
	return f.File + ":" + strconv.Itoa(f.Line) + "  [" + f.Kind + "] " + f.Text
}

// s946ScanAll 扫全仓，返回三族发现。
func s946ScanAll(t *testing.T, root string) []s946Finding {
	t.Helper()
	var out []s946Finding
	for _, rel := range trackedFiles(t, root) {
		if !s946ScopeFile(rel) {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, root+"/"+rel, nil, 0)
		if err != nil {
			t.Errorf("解析 %s 失败：%v", rel, err)
			continue
		}
		out = append(out, s946ScanAST(t, fset, f, rel)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// s946ScanAST 在**已解析的 AST** 上扫（抽出来是为了让自证臂能喂合成源码）。
func s946ScanAST(t *testing.T, fset *token.FileSet, f *ast.File, rel string) []s946Finding {
	t.Helper()
	var out []s946Finding
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		rwNames := s946ResponseWriterParams(fd)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			stmt, ok := n.(ast.Stmt)
			if !ok {
				return true
			}
			for _, call := range s946DiscardedCallsInStmt(stmt) {
				switch {
				case s946IsEncoderCall(call, rwNames):
					out = append(out, s946Finding{
						File: rel, Line: fset.Position(call.Pos()).Line,
						Text: exprText(fset, call), Kind: "encode",
					})
				case s946IsFmtWriteCall(call, rwNames):
					out = append(out, s946Finding{
						File: rel, Line: fset.Position(call.Pos()).Line,
						Text: exprText(fset, call), Kind: "fmt",
					})
				case s946IsSSEHelperCall(call):
					// D 族：SSE 写 helper 的结果被丢弃 ⇒ 调用方**无法中断**死流。
					out = append(out, s946Finding{
						File: rel, Line: fset.Position(call.Pos()).Line,
						Text: exprText(fset, call), Kind: "sse",
					})
				default:
					// B 族：http.ResponseWriter 上的 Write 结果被丢弃。
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Write" {
						continue
					}
					recv, ok := sel.X.(*ast.Ident)
					if !ok || !rwNames[recv.Name] {
						continue
					}
					out = append(out, s946Finding{
						File: rel, Line: fset.Position(call.Pos()).Line,
						Text: exprText(fset, call), Kind: "write",
					})
				}
			}
			return true
		})
	}
	return out
}

// TestS946NoDiscardedResponseWrite 是断言 ①：三族都归零。
func TestS946NoDiscardedResponseWrite(t *testing.T) {
	root := repoRoot(t)
	findings := s946ScanAll(t, root)
	if len(findings) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("响应体写失败不许无声 —— 下列调用把错误**整个丢掉**了：\n")
	for _, f := range findings {
		sb.WriteString("  " + f.String() + "\n")
	}
	sb.WriteString("⇒ 编码失败 ⇒ 客户端拿到「200 + 空体」而服务端零记录；\n")
	sb.WriteString("⇒ 写失败 ⇒ 客户端拿到截断响应而服务端零记录；\n")
	sb.WriteString("⇒ SSE 写失败 ⇒ 流已死却还在继续写。\n")
	sb.WriteString("⇒ 修法：编码类走 writeJSON（先编码后写状态）；写出类检查 error 并留痕；\n")
	sb.WriteString("　　SSE 走 s946SSEWritef（写失败 ⇒ 中断这条流）。\n")
	t.Error(sb.String())
}

// TestS946ScannerHasTeeth 是**自证臂**：证明扫描器不是恒空的。
//
// ⚠ 正对照与负对照都必须有：
//
//	① 正对照：裸语句 + `_ =` 两种丢弃形态，A/B/C 三族**都要**扫出来。
//	② 负对照：① 被接住的**不能**扫出来；接收者**不是**响应体的**不能**扫出来
//	   （否则这道门会变成噪声门）。
func TestS946ScannerHasTeeth(t *testing.T) {
	src := `package p

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func a(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"x": 1}) // ← A 族，必须扫出来
}

func b(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]any{"x": 1}) // ← A 族（裸语句），必须扫出来
}

func c(w http.ResponseWriter) {
	_, _ = w.Write([]byte("x")) // ← B 族，必须扫出来
}

func d(w http.ResponseWriter) {
	w.Write([]byte("x")) // ← B 族（裸语句），必须扫出来
}

func e(w http.ResponseWriter) {
	fmt.Fprintf(w, "x") // ← C 族，必须扫出来
}

func f(w http.ResponseWriter) {
	fmt.Fprint(w, "x") // ← C 族（裸语句），必须扫出来
}

func g(w http.ResponseWriter) {
	if err := json.NewEncoder(w).Encode(map[string]any{"x": 1}); err != nil { // ← 不能扫出来
		_ = err
	}
}

func h(w http.ResponseWriter) {
	if _, err := w.Write([]byte("x")); err != nil { // ← 不能扫出来
		_ = err
	}
}

func i(w http.ResponseWriter) {
	if _, err := fmt.Fprintf(w, "x"); err != nil { // ← 不能扫出来
		_ = err
	}
}

func j(buf *bytes.Buffer) {
	fmt.Fprintf(buf, "x") // ← 不能扫出来（接收者不是响应体）
}

func l(w http.ResponseWriter) {
	s946SSEWritef(w, "x") // ← D 族，必须扫出来（结果被丢弃 ⇒ 无法中断死流）
}

func m(w http.ResponseWriter) {
	if err := s946SSEWritef(w, "x"); err != nil { // ← 不能扫出来
		_ = err
	}
}

func k(w http.ResponseWriter, buf *bytes.Buffer) {
	_ = json.NewEncoder(buf).Encode(map[string]any{"x": 1}) // ← 不能扫出来（编码目标不是响应体）
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("解析合成源码失败：%v", err)
	}
	got := s946ScanAST(t, fset, f, "synthetic.go")

	// 正对照：三族各 2 处 + D 族 1 处。
	if len(got) != 7 {
		t.Errorf("扫描器在合成源码上报出 %d 处（期望 7 处）⇒ 扫描器可能坏了\n  %s",
			len(got), s946FormatFindings(got))
		return
	}
	kinds := map[string]int{}
	for _, f := range got {
		kinds[f.Kind]++
	}
	for kind, want := range map[string]int{"encode": 2, "write": 2, "fmt": 2, "sse": 1} {
		if kinds[kind] != want {
			t.Errorf("「%s」族扫出 %d 处，期望 %d 处\n  %s", kind, kinds[kind], want, s946FormatFindings(got))
		}
	}
}

// s946FormatFindings 把发现渲染成可读文本（报错用）。
func s946FormatFindings(fs []s946Finding) string {
	var sb strings.Builder
	for _, f := range fs {
		sb.WriteString("  " + f.String() + "\n")
	}
	return sb.String()
}

// ---- 断言 ③：形态钉住（helper 必须「先编码、后写状态」）----

// s946HelperSite 钉住一个 helper 的位置与形态。
type s946HelperSite struct {
	File string
	Func string
	// Why 是「这一处为什么值得单列」的说明（必填）。
	Why string
}

// s946EncodeFirstHelpers 是「必须**先编码、后写状态行**」的 helper。
//
// ⚠ 为什么是**名单**而不是「全仓所有写 JSON 的函数」：
//
//	名单是**穷举**出来的（这三个就是本仓所有 JSON 应答入口）⇒
//	新增一个 JSON 入口的人**必须**同时把它加进这张名单
//	⇒ ⇒⇒ 而加进来就意味着它**立刻**要满足这条规则
//	⇒ ⇒⇒ ⇒ 「新增入口 ⇒ 自动受约束」，而不是「新增入口 ⇒ 忘了就漏」。
var s946EncodeFirstHelpers = []s946HelperSite{
	{File: "backend/internal/server/server_assistant.go", Func: "writeJSON",
		Why: "全仓 JSON 响应的主入口（294 处调用；writeError 933 处也走它）⇒ 它的顺序决定 1200+ 个响应的行为。"},
	{File: "backend/internal/feishu/handler.go", Func: "writeJSON",
		Why: "飞书 webhook 应答入口（12 处调用）—— 同形 ⇒ 同一条规则。"},
	{File: "backend/internal/updates/updates.go", Func: "writeJSON",
		Why: "版本检查应答入口 —— 同形 ⇒ 同一条规则。"},
}

// s946DelegateHelpers 是「必须**委托给 writeJSON**」的 helper。
//
// ⚠ 为什么必须委托：这些 helper 以前**各自**写了一遍「先 WriteHeader 再 Encode」
// 　⇒ ⇒⇒ 委托之后只有**一处**定义「先编码后写状态」⇒ ⇒⇒ 修一处就够。
var s946DelegateHelpers = []s946HelperSite{
	{File: "backend/internal/server/server_assistant.go", Func: "writeKxmemoryError",
		Why: "kxmemory 错误应答 —— 自己再写一遍就会回到「先写状态后编码」。"},
	{File: "backend/internal/server/server_migration.go", Func: "writeMigrationJSON",
		Why: "迁移应答 —— 同上。"},
	{File: "backend/internal/server/session_transcript.go", Func: "writeSessionJSON",
		Why: "会话转写应答 —— 同上。"},
}

// s946ParseFunc 解析一个文件并找到指定名字的函数。
func s946ParseFunc(t *testing.T, root, rel, name string) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, root+"/"+rel, nil, 0)
	if err != nil {
		t.Errorf("解析 %s 失败：%v", rel, err)
		return nil, nil
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name.Name == name {
			return fset, fd
		}
	}
	return fset, nil
}

// s946FirstEncodeAndCommit 返回函数体里两个位置：
//
//	encPos    —— 第一个「编码」调用的位置（json.Marshal / json.NewEncoder(...).Encode）
//	commitPos —— 第一个「提交响应」的位置（w.WriteHeader / w.Write / fmt.Fprintf(w,...)）
//
// ⚠ 两者都取 **NoPos** 表示「函数体里没有」。
func s946FirstEncodeAndCommit(fd *ast.FuncDecl) (encPos, commitPos token.Pos) {
	encPos = token.NoPos
	commitPos = token.NoPos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if s946IsAnyEncoderCall(call) {
			if encPos == token.NoPos || call.Pos() < encPos {
				encPos = call.Pos()
			}
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "WriteHeader", "Write", "Fprintf", "Fprint", "Fprintln":
		default:
			return true
		}
		if commitPos == token.NoPos || call.Pos() < commitPos {
			commitPos = call.Pos()
		}
		return true
	})
	return encPos, commitPos
}

// s946IsAnyEncoderCall 判一个调用是不是「编码」（不要求目标是响应体）。
func s946IsAnyEncoderCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "json" {
		return sel.Sel.Name == "Marshal" || sel.Sel.Name == "MarshalIndent"
	}
	if sel.Sel.Name != "Encode" {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	isel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok || isel.Sel.Name != "NewEncoder" {
		return false
	}
	ipkg, ok := isel.X.(*ast.Ident)
	return ok && ipkg.Name == "json"
}

// s946CallsFunc 判函数体里有没有调用某个包级函数。
func s946CallsFunc(fd *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return true
	})
	return found
}

// TestS946HelperEncodeBeforeStatus 是断言 ③a：helper 必须先编码、后提交响应。
//
// ⚠ 为什么这条不能省：只钉「没有丢弃的错误」是不够的 ——
//
//	如果 helper 只是把错误**记了日志**、却**仍然先 WriteHeader**，
//	那客户端拿到的还是「200 OK + 空体」⇒ ⇒⇒ 症状一模一样
//	⇒ ⇒⇒ ⇒ 必须钉住**顺序**，而不是「有没有检查错误」。
func TestS946HelperEncodeBeforeStatus(t *testing.T) {
	root := repoRoot(t)
	for _, h := range s946EncodeFirstHelpers {
		if strings.TrimSpace(h.Why) == "" {
			t.Errorf("%s ::%s 没有写 Why（理由必填）", h.File, h.Func)
		}
		fset, fd := s946ParseFunc(t, root, h.File, h.Func)
		if fd == nil {
			t.Errorf("%s ::%s **找不到这个函数**（被删了或改名了 ⇒ 这道门形同虚设）", h.File, h.Func)
			continue
		}
		encPos, commitPos := s946FirstEncodeAndCommit(fd)
		if encPos == token.NoPos {
			t.Errorf("%s ::%s 的函数体里**没有编码调用**（json.Marshal / json.NewEncoder(...).Encode）\n"+
				"⇒ 它不满足「先编码后写状态」⇒ 编码失败时客户端仍会拿到「200 + 空体」", h.File, h.Func)
			continue
		}
		if commitPos == token.NoPos {
			t.Errorf("%s ::%s 的函数体里**没有写响应**（既没 WriteHeader 也没 Write）⇒ 这道门量不到它", h.File, h.Func)
			continue
		}
		if encPos > commitPos {
			t.Errorf("%s ::%s：提交响应（%s）发生在编码（%s）**之前**\n"+
				"⇒ 编码失败时状态行已经提交 ⇒ 客户端拿到「200 OK + 空体」而服务端零记录\n"+
				"⇒ 修法：先 json.Marshal，再 WriteHeader，最后 Write。",
				h.File, h.Func,
				fset.Position(commitPos).String(), fset.Position(encPos).String())
		}
	}
}

// TestS946HelpersDelegateToWriteJSON 是断言 ③b：同形 helper 必须委托。
func TestS946HelpersDelegateToWriteJSON(t *testing.T) {
	root := repoRoot(t)
	for _, h := range s946DelegateHelpers {
		if strings.TrimSpace(h.Why) == "" {
			t.Errorf("%s ::%s 没有写 Why（理由必填）", h.File, h.Func)
		}
		fset, fd := s946ParseFunc(t, root, h.File, h.Func)
		if fd == nil {
			t.Errorf("%s ::%s **找不到这个函数**（被删了或改名了 ⇒ 这道门形同虚设）", h.File, h.Func)
			continue
		}
		if !s946CallsFunc(fd, "writeJSON") {
			t.Errorf("%s ::%s 没有委托给 writeJSON ⇒ 它自己写了一遍 ⇒ 「先编码后写状态」这条规则没有覆盖它", h.File, h.Func)
			continue
		}
		// ⚠ 委托了却**自己**还提交响应 ⇒ 它绕过了 writeJSON 的顺序保证。
		if _, commitPos := s946FirstEncodeAndCommit(fd); commitPos != token.NoPos {
			t.Errorf("%s ::%s 委托了 writeJSON，却**自己**还提交响应（%s）⇒ 顺序保证被绕过",
				h.File, h.Func, fset.Position(commitPos).String())
		}
	}
}

// TestS946FallbackBodyIsConstant 是断言 ③c：编码失败的兜底体必须是**常量字节**。
//
// ⚠ 为什么：编码失败的原因很可能就是「值里有 NaN / 循环引用」⇒
// 　 ⇒⇒ 兜底体如果**再走一次编码**，它自己也可能失败 ⇒ ⇒⇒ ⇒ 兜底必须写常量。
func TestS946FallbackBodyIsConstant(t *testing.T) {
	root := repoRoot(t)
	fset, fd := s946ParseFunc(t, root, "backend/internal/server/server_assistant.go", "writeJSONEncodeFailure")
	if fd == nil {
		t.Fatalf("找不到 writeJSONEncodeFailure（被删了 ⇒ 编码失败时没有兜底）")
	}
	encPos, _ := s946FirstEncodeAndCommit(fd)
	if encPos != token.NoPos {
		t.Errorf("writeJSONEncodeFailure 里**又用了编码器**（%s）⇒ 编码失败时兜底体可能**自己也失败** ⇒ 必须写常量字节",
			fset.Position(encPos).String())
	}
}
