package server

// 审计式守卫：凡是服务端可能阻塞超过 http.Server.WriteTimeout（30s，见
// cmd/pocketd/main.go）的 handler，它对外的请求路径都必须在运行时真的被豁免。
//
// 为什么不能用一份手写清单来判：清单本身就是会烂的文档。2026-10-01 的
// STT 修复（2985cfef）加的是「STT 长路径全在白名单」这条结构断言，
// 它只覆盖 STT；于是同一个机制在别处又漏了两次：
//
//	/api/meetings/*        转写 120s / refine 90s / summary 45s
//	/api/emails/classify   单封 25s × 默认 20 封 = 最坏 500s
//	/api/email/backfill    按窗口回补历史邮件
//	/api/notes/{id}/summarize  60s
//
// 2026-10-02 实测 `/api/emails/classify {"limit":3}`：客户端拿到
// 「基础连接已经关闭：连接被意外关闭」，一个字节都没有——服务端还在算。
// 用户看到的就是"邮件没有自动归纳整理的能力"。
//
// 所以这里改成从源码反推：
//
//	1. AST 扫出所有 `context.WithTimeout(x, D)` 且 D > 30s 的 handler；
//	2. 从 `mux.HandleFunc("path", … s.H(…))` 建立 handler → 路由前缀；
//	3. 对没有直接注册的 handler（由 router 按 URL 后缀二次分发的），
//	   从 router 里的 `strings.HasSuffix(_, "LIT")` 分支反推它实际对应的
//	   请求路径后缀；
//	4. 要求该路径命中 longLivedPaths / longLivedSuffixes，否则报漏。
//
// 负控见文件末尾：合成一份「长 handler 挂在没豁免的路由上」的源码，
// 必须被抓出来，否则这道闸门只是一句「我加了白名单所以好了」。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeTimeoutBudget 必须与 cmd/pocketd/main.go 里的 WriteTimeout 保持一致。
// 用 const 是为了让它在编译期可比；两边不一致由下面那条对齐断言兜住。
const writeTimeoutBudget = 30 * time.Second

// routeAudit 是反推出来的路由信息。
type routeAudit struct {
	// routeOf: handler 名 → 注册的路由前缀（只对直接注册的有值）
	routeOf map[string]string
	// dispatchSuffixOf: handler 名 → 它在 router 里被分发的 URL 后缀
	dispatchSuffixOf map[string][]string
	// inheritedRouteOf: 没有直接注册的 handler → 它所属 router 的路由前缀
	inheritedRouteOf map[string]string
	// longBudgets: handler 名 → 它自己声明的**请求作用域**超时预算
	longBudgets map[string]time.Duration
	// calleesOf: 函数名 → 它调用了哪些 Server 方法（用于路由继承）
	calleesOf map[string][]string
	// callers: longBudgets 之外，单独标记「循环里逐条调用慢 helper」的 handler
	// (see perItemBudgetFloor)。
	looped map[string]time.Duration
	// loopCallsOf: 函数名 → 它在循环体里调用过的函数（判定要等 anyBudgetOf
	// 收齐之后再做，否则定义在后面的 helper 会被漏掉）
	loopCallsOf map[string][]string
	// anyBudgetOf: 函数名 → 它内部出现过的最大 WithTimeout 预算（不论挂在哪个 ctx 上）
	anyBudgetOf map[string]time.Duration
}

// isMethodCallOnServer 判定 `s.Name(...)` 这种 Server 方法调用。
func isMethodCallOnServer(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != "s" {
		return "", false
	}
	return sel.Sel.Name, true
}

// durationOfExpr 只认 `N * time.Second` / `N * time.Minute` 这类字面量乘法，
// 认不出就返回 ok=false —— 宁可漏判也不要用猜出来的值判合规。
func durationOfExpr(e ast.Expr) (time.Duration, bool) {
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != token.MUL {
		return 0, false
	}
	n, ok := bin.X.(*ast.BasicLit)
	if !ok || n.Kind != token.INT {
		return 0, false
	}
	unit, ok := bin.Y.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	pkg, ok := unit.X.(*ast.Ident)
	if !ok || pkg.Name != "time" {
		return 0, false
	}
	num, err := strconv.Atoi(n.Value)
	if err != nil {
		return 0, false
	}
	switch unit.Sel.Name {
	case "Second":
		return time.Duration(num) * time.Second, true
	case "Minute":
		return time.Duration(num) * time.Minute, true
	}
	return 0, false
}

// isContextWithTimeout 判定 `context.WithTimeout(...)`。
func isContextWithTimeout(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "WithTimeout" || len(call.Args) < 2 {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context"
}

// isRequestContextExpr 判定 `r.Context()`。
func isRequestContextExpr(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != "r" || sel.Sel.Name != "Context" {
		return false
	}
	return true
}

// requestScopedNames 收集「由 r.Context() 派生」的那些变量名。
//
// 为什么必须做这一步：`handleEmailSync` 里有两处 WithTimeout——
//
//	syncCtx, _ := context.WithTimeout(r.Context(),  30*time.Second)  ← 请求路径
//	ctx,     _ := context.WithTimeout(context.Background(), 2*time.Minute) ← 后台
//
// 只按"数值 > 30s"筛，会把后台那个 2 分钟算成 handler 的阻塞预算，
// 于是 `/api/emails/sync` 被误报成漏豁免——而它本来就不该豁免
// （longlived_paths_test.go 早就断言过它是短请求）。误报会把真漏项淹掉。
func requestScopedNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	// ctx := r.Context() / ctx, cancel := context.WithCancel(r.Context())
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) == 0 {
			return true
		}
		derived := false
		if rhs, ok := as.Rhs[0].(*ast.CallExpr); ok {
			if isRequestContextExpr(rhs.Fun) {
				derived = true
			} else if isContextWithTimeout(rhs) {
				derived = exprIsRequestScoped(rhs.Args[0], names)
			} else if sel, ok := rhs.Fun.(*ast.SelectorExpr); ok {
				// context.WithCancel / WithDeadline
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "context" &&
					(sel.Sel.Name == "WithCancel" || sel.Sel.Name == "WithDeadline") &&
					len(rhs.Args) > 0 {
					derived = exprIsRequestScoped(rhs.Args[0], names)
				}
			}
		}
		if !derived {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				names[id.Name] = true
			}
		}
		return true
	})
	return names
}

// exprIsRequestScoped 判断一个表达式是否引用了请求作用域的上下文。
func exprIsRequestScoped(e ast.Expr, names map[string]bool) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return names[v.Name]
	case *ast.CallExpr:
		return isRequestContextExpr(v.Fun)
	}
	return false
}

var _ = sort.Strings

// perItemBudgetFloor 是「单个条目也算长」的门槛。
//
// 为什么需要它：有一类 handler 的耗时不是自己写一个 context.WithTimeout
// 决定的，而是**在循环里逐条调用**一个自带秒级预算的 helper：
//
//	handleEmailClassify
//	  for … (limit 封，默认 20)
//	    classifyViaGateway → context.WithTimeout(ctx, 25*time.Second)
//
// 单看 handler 自身的字面超时，它是 0 —— 只扫字面量的判据会**完全看不见**
// 这条路由。2026-10-02 实测就是它：POST /api/emails/classify {"limit":3}
// 客户端拿到「连接被意外关闭」。负控把 /api/emails/classify 从白名单拿掉时
// 旧版判据一声不响——这正是它的盲点，不是它没用。
//
// 门槛取 20s：低于它逐条也说不清"慢"，高于 30s WriteTimeout 的一半再慢一点
// 就会在多封累加时越界。
const perItemBudgetFloor = 20 * time.Second

// analyzeRoutes 解析一组源码，返回路由反推结果。
func analyzeRoutes(fset *token.FileSet, files map[string]*ast.File) routeAudit {
	out := routeAudit{
		routeOf:          map[string]string{},
		dispatchSuffixOf: map[string][]string{},
		longBudgets:      map[string]time.Duration{},
		calleesOf:        map[string][]string{},
		looped:           map[string]time.Duration{},
		loopCallsOf:      map[string][]string{},
		anyBudgetOf:      map[string]time.Duration{},
	}
	out.inheritedRouteOf = map[string]string{}

	// 1) 路由注册：mux.HandleFunc("/path", … s.Handler(…))
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") || len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			path, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			ast.Inspect(call.Args[1], func(m ast.Node) bool {
				switch e := m.(type) {
				case *ast.CallExpr:
					// s.Handler(...) —— 直接注册
					if name, ok := isMethodCallOnServer(e); ok {
						if _, seen := out.routeOf[name]; !seen {
							out.routeOf[name] = path
						}
					}
				case *ast.SelectorExpr:
					// s.Handler —— **方法值**。本仓库的实际写法是
					// `mux.HandleFunc(p, s.requireAuth(s.handleX))`，
					// s.handleX 在这里是方法值而不是调用，只认 CallExpr
					// 会把整张路由表读成空表（每个 handler 都误判成
					// 「无路由注册」）。这个坑是反向负控逼出来的。
					if recv, ok := e.X.(*ast.Ident); ok && recv.Name == "s" {
						name := e.Sel.Name
						if _, seen := out.routeOf[name]; !seen {
							out.routeOf[name] = path
						}
					}
				}
				return true
			})
			return true
		})
	}

	// 1.5) 后台 goroutine 目标：这些函数不写 HTTP 响应，
	// WriteTimeout 与它们无关（runEmailPipeline 15 分钟、classifyEmailsAsync
	// 1 分钟都是 `go s.xxx(...)` 起的）。把它们算进来只会逼出一堆
	// 没有意义的"豁免"，最后反而让真漏项混在里面不被注意。
	goTargets := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			gs, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			if name, ok := isMethodCallOnServer(gs.Call); ok {
				goTargets[name] = true
			}
			return true
		})
	}

	// 2) 每个 FuncDecl：长预算 + 它分发的后缀 + 它调用了谁
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// 键统一用**裸方法名**：routeOf 由调用点 `s.X(...)` 抽取，
			// 早先这里给方法加了 "Server." 前缀，于是两张表对不上，
			// 每个 handler 都误判成"无路由注册"。
			self := fn.Name.Name

			// 2a) 自身的长超时预算（后台 goroutine 除外：它们不写响应）
			if goTargets[self] {
				continue
			}
			// 只认**请求作用域**的超时。挂在 context.Background() 上的
			// 长预算属于后台任务，不受 WriteTimeout 约束，算进来就是误报。
			reqNames := requestScopedNames(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isContextWithTimeout(call) {
					return true
				}
				d, ok := durationOfExpr(call.Args[1])
				if !ok {
					return true
				}
				// 不论挂在哪个 ctx 上，都记一笔：判断「循环里逐条调用慢函数」
				// 用的是这个，而不是请求作用域那个。
				if d > out.anyBudgetOf[self] {
					out.anyBudgetOf[self] = d
				}
				if !exprIsRequestScoped(call.Args[0], reqNames) {
					return true
				}
				if d > writeTimeoutBudget {
					out.longBudgets[self] = d
				}
				return true
			})

			// 2a2) 循环里逐条调用「自带 ≥20s 预算」的函数 → 整条路由都算长。
			//
			// 上面的 2a 只看 handler 自己写不写得出一个长超时，而
			// handleEmailClassify 一个都不写：它的 25s 藏在
			// classifyViaGateway 里，由外层循环调用 N 次。
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				var body *ast.BlockStmt
				switch loop := n.(type) {
				case *ast.ForStmt:
					body = loop.Body
				case *ast.RangeStmt:
					body = loop.Body
				default:
					return true
				}
				ast.Inspect(body, func(m ast.Node) bool {
					call, ok := m.(*ast.CallExpr)
					if !ok {
						return true
					}
					name := ""
					if n2, ok := isMethodCallOnServer(call); ok {
						name = n2
					} else if id, ok := call.Fun.(*ast.Ident); ok {
						name = id.Name
					}
					if name == "" {
						return true
					}
					out.loopCallsOf[self] = append(out.loopCallsOf[self], name)
					return true
				})
				return true
			})

			// 2c) 调用了谁 —— 用来把子 handler 的路由从 router 继承过来
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if name, ok := isMethodCallOnServer(call); ok {
					out.calleesOf[self] = append(out.calleesOf[self], name)
				}
				return true
			})

			// 2b2) 路径分段 switch：`switch action { case "summary": s.X(… ) }`
			// 会议 router 用的是这种（action := parts[1]），推不出
			// strings.HasSuffix 字面量，但 case 的字符串就是动作名，
			// 拼成 "/<case>" 仍然是有效的请求路径后缀。
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if !ok {
					return true
				}
				for _, stmt := range sw.Body.List {
					cc, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range cc.List {
						lit, ok := expr.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						action, err := strconv.Unquote(lit.Value)
						if err != nil || action == "" {
							continue
						}
						for _, inner := range cc.Body {
							ast.Inspect(inner, func(m ast.Node) bool {
								if call, ok := m.(*ast.CallExpr); ok {
									if name, ok := isMethodCallOnServer(call); ok {
										out.dispatchSuffixOf[name] = append(
											out.dispatchSuffixOf[name], "/"+action)
									}
								}
								return true
							})
						}
					}
				}
				return true
			})

			// 2b) strings.HasSuffix(x, "LIT") 分支里分发给了谁
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				ifs, ok := n.(*ast.IfStmt)
				if !ok {
					return true
				}
				lit := suffixLiteralOf(ifs.Cond)
				if lit == "" {
					return true
				}
				ast.Inspect(ifs.Body, func(m ast.Node) bool {
					if call, ok := m.(*ast.CallExpr); ok {
						if name, ok := isMethodCallOnServer(call); ok {
							out.dispatchSuffixOf[name] = append(out.dispatchSuffixOf[name], lit)
						}
					}
					return true
				})
				return true
			})
		}
	}

	// 2.5) 循环里逐条调用慢函数 → 整条路由都算长。
	// 必须在 anyBudgetOf 收齐之后再判，否则定义在后面的 helper 会被漏掉。
	//
	// 而且要**传递**地判：真实链路是三跳
	//	handleEmailClassify →(循环) classifyOneEmail → classifyViaGateway(25s)
	// 只看直接被调用的那一层，classifyOneEmail 自己没有超时就被放过了。
	slowReachable := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for caller, callees := range out.calleesOf {
			if slowReachable[caller] {
				continue
			}
			for _, c := range callees {
				if out.anyBudgetOf[c] >= perItemBudgetFloor || slowReachable[c] {
					slowReachable[caller] = true
					changed = true
					break
				}
			}
		}
	}
	for caller, callees := range out.loopCallsOf {
		for _, c := range callees {
			if !slowReachable[c] {
				continue
			}
			d := out.anyBudgetOf[c]
			if d < perItemBudgetFloor {
				d = perItemBudgetFloor
			}
			if d > out.looped[caller] {
				out.looped[caller] = d
			}
		}
	}

	// 3) 路由继承：子 handler 自己没有注册时，继承调用它的 router 的前缀。
	//
	//	/api/meetings/  → handleMeetingRouter → handleMeetingSummary / Refine /
	//	                                       TranscribeMeeting
	//	/api/emails/invoices/ → handleEmailInvoiceRouter → handleEmailInvoiceHarvest
	//	/api/email/pipeline/run → handleEmailPipelineRun → runEmailPipeline
	//
	// 这些子 handler 用 `switch parts[1] { case "summary": … }` 这种
	// **路径分段 switch** 分发，推不出 HasSuffix 字面量，只能靠继承。
	// 迭代到不动点：router 也可能套 router。
	for changed := true; changed; {
		changed = false
		for caller, callees := range out.calleesOf {
			route, ok := out.routeOf[caller]
			if !ok {
				if r, ok2 := out.inheritedRouteOf[caller]; ok2 {
					route = r
				} else {
					continue
				}
			}
			for _, c := range callees {
				if _, has := out.routeOf[c]; has {
					continue
				}
				if _, has := out.inheritedRouteOf[c]; has {
					continue
				}
				out.inheritedRouteOf[c] = route
				changed = true
			}
		}
	}
	return out
}

// suffixLiteralOf 从 `strings.HasSuffix(x, "/summarize")` 里取出字面量。
// 认不出就返回 ""，让调用方跳过——不猜。
func suffixLiteralOf(cond ast.Expr) string {
	call, ok := cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "HasSuffix" {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "strings" {
		return ""
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || s == "" || !strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}

// uncoveredLongHandlers 返回「阻塞超预算但运行时没被豁免」的 handler。
// 抽成纯函数是为了让负控能喂合成源码。
func uncoveredLongHandlers(a routeAudit, prefixes, suffixes, patterns []string) []string {
	covers := func(path string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		for _, s := range suffixes {
			if strings.HasSuffix(path, s) {
				return true
			}
		}
		return false
	}

	var bad []string

	// 逐段模式：handler 由 router 按路径分段分发（会议那组），且它所属的
	// 路由前缀 + 动作名命中 longLivedPatterns。
	// 拼一个样本路径（中间那段用 "x" 代替真实 id）去比对模式。
	actionCoveredByPattern := func(route, handler string) bool {
		actions, ok := a.dispatchSuffixOf[handler]
		if !ok || len(actions) == 0 {
			return false
		}
		base := strings.TrimSuffix(route, "/")
		for _, act := range actions {
			sample := base + "/x" + act
			hit := false
			for _, pat := range patterns {
				if matchPathPattern(pat, sample) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		return true
	}

	// 逐条累加型：handler 自己没有长超时，但循环里调用的 helper 有 ≥20s 预算。
	for handler, per := range a.looped {
		if _, own := a.longBudgets[handler]; own {
			continue // 已按字面超时判过
		}
		if allDispatchSuffixesCovered(a, handler, suffixes) {
			continue
		}
		if route, ok := a.routeOf[handler]; ok {
			if covers(route) || actionCoveredByPattern(route, handler) {
				continue
			}
			bad = append(bad, handler+" 在循环里逐条调用单次 "+per.String()+
				" 的操作（条数由请求参数决定），但路由 "+route+" 未豁免")
			continue
		}
		if route, ok := a.inheritedRouteOf[handler]; ok {
			if covers(route) || actionCoveredByPattern(route, handler) {
				continue
			}
		}
		bad = append(bad, handler+" 在循环里逐条调用单次 "+per.String()+
			" 的操作，但既无路由注册、也推不出分发后缀")
	}

	for handler, budget := range a.longBudgets {
		// 最窄的判据优先：能推导出分发后缀就用后缀（/api/notes/ + /summarize
		// 这种），推不出才退到路由前缀。
		if allDispatchSuffixesCovered(a, handler, suffixes) {
			continue
		}
		if route, ok := a.routeOf[handler]; ok {
			if covers(route) {
				continue
			}
			bad = append(bad, handler+" 预算 "+budget.String()+" 但路由 "+route+" 未豁免")
			continue
		}
		if route, ok := a.inheritedRouteOf[handler]; ok {
			if covers(route) {
				continue
			}
			if actionCoveredByPattern(route, handler) {
				continue
			}
			// 路由前缀没命中，但账本登记了一个精确到动作的豁免路径
			if ex, ok := longLivedHandlerExemptions[handler]; ok && covers(ex.Route) {
				continue
			}
			bad = append(bad, handler+" 预算 "+budget.String()+
				" 但其 router 的路由 "+route+" 未豁免")
			continue
		}
		// 连路由都推不出来 —— 报出来让人补，绝不默认放过
		bad = append(bad, handler+" 预算 "+budget.String()+
			" 但既无路由注册、也推不出分发后缀（无法证明它被豁免）")
	}
	sort.Strings(bad)
	return bad
}

func allDispatchSuffixesCovered(a routeAudit, handler string, suffixes []string) bool {
	ds, ok := a.dispatchSuffixOf[handler]
	if !ok || len(ds) == 0 {
		return false
	}
	// 该 handler 的**每一个**分发后缀都得被豁免。只要有一条漏了，
	// 就存在一条能被 30s 掐断的路径。
	for _, s := range ds {
		hit := false
		for _, suf := range suffixes {
			if strings.HasSuffix(s, suf) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func parsePackageSources(t *testing.T, dir string) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", dir, err)
	}
	out := map[string]*ast.File{}
	for _, p := range pkgs {
		for name, f := range p.Files {
			out[name] = f
		}
	}
	return fset, out
}

// TestLongRunningHandlersAreAllExempt 是这道守卫的主体。
func TestLongRunningHandlersAreAllExempt(t *testing.T) {
	_, files := parsePackageSources(t, ".")
	if len(files) == 0 {
		t.Fatal("没解析到任何源文件 —— 判据空跑，等于什么都没检查")
	}
	audit := analyzeRoutes(token.NewFileSet(), files)

	if len(audit.longBudgets) == 0 {
		t.Fatal("一个 >30s 的 handler 都没扫出来 —— 判据多半是空跑，" +
			"检查 durationOfExpr 是否还认得 `N * time.Second`")
	}

	bad := uncoveredLongHandlers(audit, longLivedPaths, longLivedSuffixes, longLivedPatterns)
	for _, b := range bad {
		t.Errorf("长耗时 handler 未被豁免，会在 %s 处被 WriteTimeout 掐断、客户端收到空响应：%s",
			writeTimeoutBudget, b)
	}

	// 账本不许养陈条目，也不许只是一段注释：每条都必须写得出真实路径，
	// 且该路径必须**真的**在白名单里被豁免。
	declared := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
	}
	coveredByList := func(path string) bool {
		for _, p := range longLivedPaths {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		for _, s := range longLivedSuffixes {
			if strings.HasSuffix(path, s) {
				return true
			}
		}
		return false
	}
	for name, ex := range longLivedHandlerExemptions {
		if !declared[name] {
			t.Errorf("longLivedHandlerExemptions 里的 %q 在本包里没有对应函数 —— "+
				"要么写错了名字，要么该条目已经过期（删除它，或修好名字）", name)
		}
		if strings.TrimSpace(ex.Reason) == "" {
			t.Errorf("longLivedHandlerExemptions[%q] 的 Reason 是空的。"+
				"豁免必须写清可核查的预算来源，不接受空口条", name)
		}
		if !coveredByList(ex.Route) {
			t.Errorf("longLivedHandlerExemptions[%q].Route = %q，但它并不在 "+
				"longLivedPaths / longLivedSuffixes 里 —— 账本说了不算，"+
				"运行时仍会被 30s 掐断", name, ex.Route)
		}
	}
}

// TestWriteTimeoutBudgetMatchesMain 钉住本文件顶部那个常量。
// 它必须等于 cmd/pocketd/main.go 里 http.Server.WriteTimeout 的值 ——
// 两边漂移的话，这道守卫会拿错误的标尺去量所有 handler。
func TestWriteTimeoutBudgetMatchesMain(t *testing.T) {
	mainSrc, err := filepath.Abs(filepath.Join("..", "..", "cmd", "pocketd", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mainSrc, nil, 0)
	if err != nil {
		t.Fatalf("解析 main.go 失败: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "WriteTimeout" {
			return true
		}
		if d, ok := durationOfExpr(kv.Value); ok {
			found = true
			if d != writeTimeoutBudget {
				t.Errorf("main.go 的 WriteTimeout = %s，但守卫按 %s 判断，标尺不一致",
					d, writeTimeoutBudget)
			}
		}
		return true
	})
	if !found {
		t.Fatal("main.go 里没找到形如 `WriteTimeout: 30 * time.Second` 的配置 —— " +
			"这条对齐断言已经失去意义，请同步更新守卫")
	}
}

// TestNewlyExemptedPathsSurviveWriteTimeout 是**运行时**证明，不只是查表。
//
// 前面那些都是「源码反推 + 白名单比对」，这里起一个真的带 WriteTimeout 的
// http.Server（不能用 httptest.NewServer——它默认没有 WriteTimeout，
// 测出来永远是「慢请求也能拿到响应」），让这些路径在远超 30s 的耗时下
// 必须真的拿得到响应。
func TestNewlyExemptedPathsSurviveWriteTimeout(t *testing.T) {
	base, stop := startSlowServer(t, 150*time.Millisecond)
	defer stop()

	// 本轮补进去的（2026-02 之前的漏网之鱼）
	for _, p := range []string{
		"/api/emails/classify", // 单封 25s × 最多 20 封
		"/api/meetings/m1/summary",
		"/api/meetings/m1/refine",
		"/api/meetings/m1/transcribe",
		"/api/email/backfill",
		"/api/notes/n1/summarize", // 后缀匹配
		"/api/emails/e1/summarize",
	} {
		t.Run("exempt:"+p, func(t *testing.T) {
			code, body, err := getStatus(base + p)
			if err != nil {
				t.Fatalf("%s 应当豁免 WriteTimeout，实际请求失败：%v", p, err)
			}
			if code != http.StatusOK {
				t.Errorf("%s 状态码 = %d，body=%q", p, code, body)
			}
		})
	}

	// 对照组：同一批路由里的**兄弟**端点必须仍然被 30s 掐断。
	// 没有它，上面那条可能只是「中间件坏了所以谁都不超时」——
	// 那时 err != nil 反而会 FAIL，所以这里必须确认它们确实拿不到响应。
	for _, p := range []string{
		"/api/emails/purge",
		"/api/meetings/m1",
		"/api/notes/n1",
	} {
		t.Run("still-cut:"+p, func(t *testing.T) {
			if code, body, err := getStatus(base + p); err == nil && code == http.StatusOK {
				t.Errorf("%s 不该被豁免，却拿到了 200 body=%q —— "+
					"说明豁免范围被放得过宽（整个 /api/meetings/ 或 /api/notes/ 被整段放开了）",
					p, body)
			}
		})
	}
}

// TestUncoveredLongHandlerIsReported 是**负控**。
//
// 没有它，上面那条用例可能因为「解析器什么都没扫到」而永远绿。
// 这里喂一份合成源码：把一个 120s 的 handler 注册在 /api/slow/whatever，
// 白名单里没有这条前缀 —— 必须被抓出来。
func TestUncoveredLongHandlerIsReported(t *testing.T) {
	const synthetic = `package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleSomethingSlow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	_ = ctx
}

func (s *Server) registerSynthetic(mux *http.ServeMux) {
	mux.HandleFunc("/api/slow/whatever", s.requireAuth(s.handleSomethingSlow))
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", synthetic, 0)
	if err != nil {
		t.Fatalf("合成源码解析失败: %v", err)
	}
	audit := analyzeRoutes(fset, map[string]*ast.File{"synthetic.go": f})

	if _, ok := audit.longBudgets["handleSomethingSlow"]; !ok {
		t.Fatalf("负控样本自身有问题：没识别出 120s 的 handler，扫到的是 %v",
			audit.longBudgets)
	}
	if route := audit.routeOf["handleSomethingSlow"]; route != "/api/slow/whatever" {
		t.Fatalf("负控样本自身有问题：没解析出路由注册，实际是 %q", route)
	}
	bad := uncoveredLongHandlers(audit, longLivedPaths, longLivedSuffixes, longLivedPatterns)
	if len(bad) == 0 {
		t.Fatal("负控失败：一个 120s 的 handler 挂在未豁免的路由上，守卫却没有报出来 —— " +
			"这道闸门是假的")
	}
	if !strings.Contains(bad[0], "handleSomethingSlow") {
		t.Errorf("报出的内容没点名 handler：%q", bad[0])
	}
}

// TestCoveredLongHandlerIsNotReported 是**反向负控**。
//
// 只测「会报错」不够：解析器若把一切都当成没豁免，上面那条也会绿。
// 同一个合成 handler，把前缀加进白名单后必须**不再**报。
func TestCoveredLongHandlerIsNotReported(t *testing.T) {
	const synthetic = `package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleSomethingSlow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	_ = ctx
}

func (s *Server) registerSynthetic(mux *http.ServeMux) {
	mux.HandleFunc("/api/slow/whatever", s.requireAuth(s.handleSomethingSlow))
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", synthetic, 0)
	if err != nil {
		t.Fatalf("合成源码解析失败: %v", err)
	}
	audit := analyzeRoutes(fset, map[string]*ast.File{"synthetic.go": f})

	withPrefix := uncoveredLongHandlers(audit, []string{"/api/slow/"}, longLivedSuffixes, longLivedPatterns)
	if len(withPrefix) != 0 {
		t.Errorf("前缀已在白名单里却仍然报错，说明判据不是看覆盖关系：%v", withPrefix)
	}
	withSuffix := uncoveredLongHandlers(audit, longLivedPaths, []string{"/whatever"}, longLivedPatterns)
	if len(withSuffix) != 0 {
		t.Errorf("后缀已在白名单里却仍然报错，说明后缀匹配没生效：%v", withSuffix)
	}
}
