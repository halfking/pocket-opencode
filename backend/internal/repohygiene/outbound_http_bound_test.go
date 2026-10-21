package repohygiene

// outbound_http_bound_test.go — §950 卡口：**出站 HTTP 必须有上界**。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，不许出现任何没有时间上界的出站 HTTP 请求。**
//
// # 为什么（§950 的实测依据，不是推理）
//
//	Go 的 net/http：「客户端没有 Timeout」+「请求没带带 deadline 的 ctx」
//	⇒ 这个请求**没有任何时间上界** ⇒ 上游不返回就**永久挂住**：
//
//	  - 对端黑洞（连不上也不拒绝）⇒ 卡在 dial；
//	  - 连上了但迟迟不回响应头　　⇒ 卡在读响应头；
//	  - 响应头到了但 body 读不完　⇒ 卡在读 body（trickle / 不发 Content-Length）。
//
//	⇒⇒⇒ 在一个**长跑**的 pocketd 里，这样的请求 = 那个 goroutine 永久泄漏；
//	⇒⇒⇒ §949 管的「panic 打死进程」**管不到这里** ——
//	⇒⇒⇒ 因为 **panic 能救，阻塞救不了**：recover 只在 panic 发生的那一刻有用，
//	　　 而「永远等不到响应」不是 panic，是**安静地停在那儿**。
//	⇒⇒⇒ 这也直接命中属主的核心诉求：「会议时长要能无限长，只取决于存储大小」
//	⇒⇒⇒ ⇒ 一个永远挂住的出站请求 = 那一路（会议上传 / 邮件同步 / 网关代理）
//	　　　 **永远走不完** ⇒ 长跑就会被拖死。
//
// # 实测数字（§950.0 的普查）
//
//	后端非测试代码的 `http.Client` 字面量：**33 处**
//	├ 有 Timeout：**29 处**（其中 2 处显式 `Timeout: 0` = SSE 形态）
//	├ 无 Timeout：**4 处**
//	│   ├ internal/acchttp/client.go　　⇒ 文档写了「HTTP 客户端超时为硬上限」，代码里**没有** Timeout
//	│   ├ internal/adapter/opencode_config.go / opencode_http.go ⇒ 靠 `context.WithTimeout` 兜
//	│   └ internal/facade/events.go　　 ⇒ SSE，靠 ctx 兜
//	├ `http.DefaultClient`：**2 处**（oauth_callback = **真缺陷**；audio_transform = 绕过了已构造的客户端）
//	└ 不带 ctx 的 `http.NewRequest`：**4 处**（其中 oauth_callback 那处 = **真缺陷**）
//
// # 口径
//
//	① 受检范围内（backend 非测试代码，**排除 internal/outbound 自身**）
//	   **不许有任何 `http.Client{…}` 字面量** ⇒ 客户端只能由 internal/outbound 构造
//	   ⇒⇒⇒ 上界由那**一个**构造口统一兜住（`NewClient` 保证 Timeout > 0）。
//	② `http.DefaultClient` **一律不许**（它没有 Timeout，且绕过本仓所有传输层配置）。
//	③ 包级便利函数 `http.Get/Post/Head/PostForm` **一律不许**
//	   （它们内部就是 `http.DefaultClient`）。
//	④ `http.Client` 的**值类型**（非指针）一律不许
//	   （值形态的 Timeout 恒为 0 ⇒ 天然无上界）。
//	⑤ 不带 ctx 的 `http.NewRequest` 必须**登记在册**并写清「上界由谁提供」。
//	⑥ 没有总超时的客户端（`outbound.NewStreamingClient`）必须**登记在册**并写清
//	   「上界由谁提供」（SSE 形态：上界来自请求 ctx）。
//
// # 为什么这样就足够了（不变式的完整推导）
//
//	(a) ① + ④ ⇒ 后端里**每一个**出站客户端都由 internal/outbound 构造
//	    ⇒ 由 outbound 包的单元测试保证 `Timeout > 0`（timeout <= 0 回落默认值）；
//	(b) ⑤ ⇒ 剩下的「不带 ctx 的请求」全部在台账里，台账写明「客户端 Timeout 兜底」；
//	(c) ⑥ ⇒ 剩下的「没有总超时的客户端」全部是 SSE 形态，上界来自请求 ctx。
//	⇒⇒⇒ 合起来：**每一个出站请求都至少有一个上界**。
//
// # 为什么用 go/parser 走 AST（而不是 grep）
//
//	★ §943/§945 的教训：「grep 只看到了我让它看的形态」。
//	★ 本仓的 `http.DefaultClient` 在**注释里**就有 4 处（§950 的修复说明）
//	⇒⇒⇒ grep 会把注释当成违规 ⇒ ⇒⇒ 必须走 AST ⇒ 注释天然不算数。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① `*_test.go` 与 `testdata/` 不是生产路径 ⇒ 排除。
//	② `backend/third_party/` 是上游 vendored ⇒ 排除。
//	③ 本卡口只看**后端**。
//
//	⚠ 前端那一族**不是本节的空白**，而是**已由既有卡口守住**：
//	  - `src/api/http.ts` 有统一的超时层（`DEFAULT_TIMEOUT_MS = 30s` +
//	    `AbortController` + `TimeoutError`；它的头注释写着「一个不响应的后端
//	    会让 await 永久挂起」——与本节要治的是**同一个病**）；
//	  - `src/api/__tests__/api-timeout-budget-table.test.mjs` 用一张**数据驱动**的
//	    预算表钉住「客户端超时 > 服务端执行预算」（它自己写着「相等即错」）。
//	⇒⇒⇒ ⇒ 本节只补**后端**这一侧。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s950OutboundRel 是 internal/outbound 包在仓内的路径前缀。
//
// ⚠ 它是**豁免**而不是「不管」：那个包是**唯一合法的客户端构造口**，
// 它自己只有 2 处 `http.Client{`（NewClient / NewStreamingClient），
// 由 TestS950OutboundIsTheOnlyConstructor 把住。
const s950OutboundRel = "backend/internal/outbound/"

// s950OutboundImport 是 internal/outbound 的导入路径。
const s950OutboundImport = "github.com/halfking/pocket-opencode/backend/internal/outbound"

// s950MinScannedFiles 是受检文件数的**下界**。
//
// ⚠ 为什么是下界而不是精确值：
//
//	精确值会在「新增一个后端文件」时飘红 ⇒⇒⇒ 那不是本卡口要管的事
//	⇒⇒⇒ 会逼着人习惯性改这个数字 ⇒⇒⇒ 改着改着就没人看了。
//	⇒⇒⇒ 真正的范围塌缩防护在**台账双向相等**：
//	　　 台账里登记的 7 个点一旦扫不到 ⇒ 台账立刻失配 ⇒ 门红。
const s950MinScannedFiles = 400

// s950LedgerRow 是台账的一行：**一个例外点 → 它为什么可以没有上界**。
//
// ⚠ 为什么台账要「双向相等」：
//
//	只钉「台账里的都还在」⇒ 有人**新增**一个不带 ctx 的请求不红；
//	只钉「源码里的都在台账」⇒ 有人**删掉**一个登记项不红。
//	⇒⇒⇒ 两个方向都要钉（§949 的同一条纪律）。
type s950LedgerRow struct {
	// Key 是「文件#函数」（函数名是**身份**，行号是**坐标** ⇒ 行号会漂）。
	Key string
	// Why 是「上界由谁提供」—— 不许为空。
	Why string
}

// s950CtxlessLedger 是「不带 ctx 的 http.NewRequest」登记表（口径 ⑤）。
//
// ⚠ 每一条的 Why 都必须写清**上界由谁提供**：本仓的答案是「客户端 Timeout 兜底」，
// 　 而客户端 Timeout > 0 由 internal/outbound 的单元测试保证（见 outbound_test.go）。
var s950CtxlessLedger = []s950LedgerRow{
	{
		Key: "backend/internal/redclaw/client.go#doRequest",
		Why: "客户端 Timeout 兜底（outbound.NewClient 保证 > 0）；调用链是 redclaw 的控制面短请求",
	},
	{
		Key: "backend/internal/server/companion_client.go#get",
		Why: "客户端 Timeout 兜底（outbound.NewClient 保证 > 0）；companion 是只读面",
	},
	{
		Key: "backend/internal/server/companion_client.go#post",
		Why: "客户端 Timeout 兜底（outbound.NewClient 保证 > 0）；companion 是只读面",
	},
}

// s950StreamingLedger 是「没有总超时的客户端（outbound.NewStreamingClient）」登记表（口径 ⑥）。
//
// ⚠ 每一条都是 **SSE 形态**：流的寿命由事件源决定，套上总超时会**掐断流**
// 　 ⇒⇒⇒ 上界改由**请求 ctx** 提供 ⇒ 每个点都要写清「ctx 从哪来」。
var s950StreamingLedger = []s950LedgerRow{
	{
		Key: "backend/internal/facade/events.go#consumeEventStream",
		Why: "facade 的 SSE 事件流：上界由请求 ctx 提供（取消即断流）",
	},
	{
		Key: "backend/internal/server/ssrf.go#newGuardedHTTPClient",
		Why: "网关 admin 的 SSE 长连接（timeout=0 是故意传的）：上界由请求 ctx 提供",
	},
	{
		Key: "backend/internal/agent/transport_http.go#Start",
		Why: "ACP 的 SSE 长连接：上界由请求 ctx 提供（Start 的 ctx）",
	},
	{
		Key: "backend/internal/adapter/opencode_http.go#SubscribeEvents",
		Why: "OpenCode 事件订阅 SSE：上界由请求 ctx 提供（调用方 cancel）",
	},
}

// s950Hit 是一次命中。
type s950Hit struct {
	File string
	Line int
	// Func 是所在的函数名（包级语句为空）。
	Func string
	// Kind 是命中形态的说明。
	Kind string
}

// s950ScanResult 是一次扫描的产物。
type s950ScanResult struct {
	// ScannedFiles 是受检文件数。
	ScannedFiles int
	// ClientLiterals 是 `http.Client{…}` 复合字面量。
	ClientLiterals []s950Hit
	// DefaultClients 是 `http.DefaultClient`。
	DefaultClients []s950Hit
	// PkgHelpers 是包级便利函数（http.Get / Post / Head / PostForm）。
	PkgHelpers []s950Hit
	// ClientValueTypes 是 `http.Client` 的**值类型**用法（非指针）。
	ClientValueTypes []s950Hit
	// NewHTTPClients 是 `new(http.Client)` —— 零值客户端（Timeout 恒为 0）。
	NewHTTPClients []s950Hit
	// DotImports 是点导入 net/http（会让本扫描器整族失明 ⇒ 直接判红）。
	DotImports []s950Hit
	// CtxlessRequests 是不带 ctx 的 `http.NewRequest`。
	CtxlessRequests []s950Hit
	// StreamingCalls 是 `outbound.NewStreamingClient` 调用点。
	StreamingCalls []s950Hit
	// OutboundLiterals 是 internal/outbound 内部的 `http.Client{…}` 字面量。
	OutboundLiterals []s950Hit
}

// s950ScanAll 扫**全部**受检文件。
func s950ScanAll(t *testing.T, root string) s950ScanResult {
	t.Helper()
	res := s950ScanResult{}
	for _, rel := range trackedFiles(t, root) {
		if !strings.HasPrefix(rel, "backend/") || !strings.HasSuffix(rel, ".go") {
			continue
		}
		if strings.Contains(rel, "/testdata/") {
			continue
		}
		// ⚠ `backend/third_party/` 是上游 vendored（identity-go）⇒ 排除。
		if strings.HasPrefix(rel, "backend/third_party/") {
			continue
		}
		// ⚠ 测试文件不是生产路径 ⇒ 排除。
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		path := root + "/" + rel
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", rel, err)
		}
		sub := s950ScanFile(fset, f, rel)
		res.ScannedFiles++
		res.ClientLiterals = append(res.ClientLiterals, sub.ClientLiterals...)
		res.DefaultClients = append(res.DefaultClients, sub.DefaultClients...)
		res.PkgHelpers = append(res.PkgHelpers, sub.PkgHelpers...)
		res.ClientValueTypes = append(res.ClientValueTypes, sub.ClientValueTypes...)
		res.NewHTTPClients = append(res.NewHTTPClients, sub.NewHTTPClients...)
		res.DotImports = append(res.DotImports, sub.DotImports...)
		res.CtxlessRequests = append(res.CtxlessRequests, sub.CtxlessRequests...)
		res.StreamingCalls = append(res.StreamingCalls, sub.StreamingCalls...)
		res.OutboundLiterals = append(res.OutboundLiterals, sub.OutboundLiterals...)
	}
	// ⚠ 排序 ⇒ 报错输出稳定（否则同一个问题每次顺序不同，人就没法 diff）。
	s950SortHits(res.ClientLiterals)
	s950SortHits(res.DefaultClients)
	s950SortHits(res.PkgHelpers)
	s950SortHits(res.ClientValueTypes)
	s950SortHits(res.NewHTTPClients)
	s950SortHits(res.DotImports)
	s950SortHits(res.CtxlessRequests)
	s950SortHits(res.StreamingCalls)
	s950SortHits(res.OutboundLiterals)
	return res
}

// s950ScanFile 在**已解析的 AST** 上找 §950 的各种形态。
//
// ⚠ 单独抽出来是为了让「自证臂」能拿**合成源码**喂进来（见 TestS950ScannerHasTeeth）。
func s950ScanFile(fset *token.FileSet, f *ast.File, rel string) s950ScanResult {
	res := s950ScanResult{}
	httpLocal, httpDot := s950NetHTTPLocalName(f)
	outLocal := s950OutboundLocalName(f)

	// allowed 记住「这个 http.Client 选择器是用在**指针**或**字面量**位置上的」
	// ⇒ 那两种是合法的，剩下的是「值类型」用法（判红）。
	allowed := map[*ast.SelectorExpr]bool{}

	visit := func(fnName string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			if n == nil {
				return true
			}
			switch v := n.(type) {
			case *ast.StarExpr:
				// `*http.Client`（指针类型/解引用）⇒ 合法用法。
				if sel, ok := s950HTTPClientSelector(v.X, httpLocal, httpDot); ok {
					allowed[sel] = true
				}
			case *ast.CompositeLit:
				if sel, ok := s950HTTPClientSelector(v.Type, httpLocal, httpDot); ok {
					allowed[sel] = true
					hit := s950Hit{File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
						Kind: "http.Client 字面量"}
					if strings.HasPrefix(rel, s950OutboundRel) {
						res.OutboundLiterals = append(res.OutboundLiterals, hit)
					} else {
						res.ClientLiterals = append(res.ClientLiterals, hit)
					}
				}
			case *ast.SelectorExpr:
				if sel, ok := s950HTTPClientSelector(v, httpLocal, httpDot); ok && !allowed[sel] {
					res.ClientValueTypes = append(res.ClientValueTypes, s950Hit{
						File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
						Kind: "http.Client 的值类型用法（非指针）",
					})
				}
				if s950IsNetHTTPSelector(v, httpLocal, httpDot, "DefaultClient") {
					res.DefaultClients = append(res.DefaultClients, s950Hit{
						File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
						Kind: "http.DefaultClient",
					})
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					if s950IsNetHTTPSelector(sel, httpLocal, httpDot, "Get", "Post", "Head", "PostForm") {
						res.PkgHelpers = append(res.PkgHelpers, s950Hit{
							File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
							Kind: "包级 http 便利函数（内部走 http.DefaultClient）",
						})
					}
					if s950IsNetHTTPSelector(sel, httpLocal, httpDot, "NewRequest") {
						res.CtxlessRequests = append(res.CtxlessRequests, s950Hit{
							File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
							Kind: "不带 ctx 的 http.NewRequest",
						})
					}
					if outLocal != "" && s950IsPlainSelector(sel, outLocal, "NewStreamingClient") {
						res.StreamingCalls = append(res.StreamingCalls, s950Hit{
							File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
							Kind: "outbound.NewStreamingClient（没有总超时的客户端）",
						})
					}
				}
				// ⚠ `new(http.Client)` 造出来的是**零值**客户端（Timeout 恒为 0）
				//　 ⇒⇒⇒ 它是「没有字面量」形态的无上界客户端 ⇒ 一样判红。
				if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
					if _, ok := s950HTTPClientSelector(v.Args[0], httpLocal, httpDot); ok {
						res.NewHTTPClients = append(res.NewHTTPClients, s950Hit{
							File: rel, Line: fset.Position(v.Pos()).Line, Func: fnName,
							Kind: "new(http.Client)",
						})
					}
				}
			}
			return true
		})
	}

	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			visit(v.Name.Name, v)
		case *ast.GenDecl:
			visit("", v)
		}
	}
	if httpDot {
		res.DotImports = append(res.DotImports, s950Hit{
			File: rel, Line: 1, Func: "",
			Kind: "点导入 net/http（会让本扫描器整族失明）",
		})
	}
	return res
}

// s950NetHTTPLocalName 找出本文件里 net/http 被 import 成的**本地名**。
//
// ⚠ 为什么要走 import 而不是直接认字面量 "http"：
//
//	import 别名是合法的（import h "net/http"）⇒ 只认字面量会让别名用法**整族漏检**
//	⇒⇒⇒ 那正是 §948/§949 记的「量具漏了一族形态」。
//
// ⚠ 第二个返回值是「是否点导入」：点导入会让 `http.Client` 变成 `Client`，
// 　 本扫描器的**所有**识别形态都失效 ⇒ ⇒⇒ 那种文件直接判红（见 TestS950NoDotImport）。
func s950NetHTTPLocalName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != "net/http" {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "." {
				return "", true
			}
			return imp.Name.Name, false
		}
		return "http", false
	}
	// ⚠ 本文件没 import net/http ⇒ 不许把任何 `xxx.Client` 当成 http.Client。
	return "", false
}

// s950OutboundLocalName 找出本文件里 internal/outbound 被 import 成的**本地名**。
func s950OutboundLocalName(f *ast.File) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != s950OutboundImport {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "outbound"
	}
	return ""
}

// s950HTTPClientSelector 判一个表达式是不是 `http.Client` 选择器。
func s950HTTPClientSelector(e ast.Expr, local string, dot bool) (*ast.SelectorExpr, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	if dot {
		return nil, false
	}
	if local == "" {
		return nil, false
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && sel.Sel.Name == "Client" {
		return sel, true
	}
	return nil, false
}

// s950IsNetHTTPSelector 判一个选择器是不是 `http.<名字>`（本地名匹配）。
func s950IsNetHTTPSelector(sel *ast.SelectorExpr, local string, dot bool, names ...string) bool {
	if dot || local == "" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != local {
		return false
	}
	for _, n := range names {
		if sel.Sel.Name == n {
			return true
		}
	}
	return false
}

// s950IsPlainSelector 判一个选择器是不是 `<本地名>.<名字>`（不看包路径）。
func s950IsPlainSelector(sel *ast.SelectorExpr, local string, name string) bool {
	if local == "" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == local && sel.Sel.Name == name
}

// s950KeyOf 造台账键：「文件#函数」。
func s950KeyOf(h s950Hit) string {
	return h.File + "#" + h.Func
}

// TestS950NoRawHTTPClientLiteral 是断言 ①：受检范围内不许有 `http.Client{…}` 字面量。
//
// ⚠ 这是本卡口的**牙**：光有「客户端都有 Timeout」不够 ——
//
//	否则「随手 `&http.Client{}`」这条路是通的 ⇒⇒⇒ 那个客户端 Timeout = 0
//	⇒⇒⇒ ⇒ 请求没有上界 ⇒ ⇒⇒ 长跑时会永久挂住。
func TestS950NoRawHTTPClientLiteral(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.ClientLiterals) == 0 {
		return
	}
	for _, h := range res.ClientLiterals {
		t.Errorf("%s:%d（%s）有一处 `http.Client{…}` 字面量\n"+
			"⇒ 它的 Timeout 恒为 0（除非你自己写）⇒ ⇒⇒ 这个出站请求**没有时间上界**\n"+
			"⇒ ⇒⇒ 上游不返回就永久挂住 ⇒ 长跑的 pocketd 里那是个永久泄漏的 goroutine\n"+
			"⇒ 改法：outbound.NewClient(超时)（timeout <= 0 会自动回落默认值，不会漏成无上界）。",
			h.File, h.Line, h.Func)
	}
}

// TestS950OutboundIsTheOnlyConstructor 是断言 ①b：internal/outbound 内部只允许 2 处字面量。
//
// ⚠ 钉住绝对条数（2）—— 防止有人在 outbound 包里又手写第三个裸客户端。
func TestS950OutboundIsTheOnlyConstructor(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if got, want := len(res.OutboundLiterals), 2; got != want {
		t.Errorf("internal/outbound 里的 `http.Client{…}` 字面量数 = %d（期望 %d）\n"+
			"⇒ outbound 是**唯一合法的客户端构造口** ⇒ 它内部只该有 NewClient / NewStreamingClient 两个\n"+
			"⇒ ⇒⇒ 多出来的每一个都是绕过兜底的口子", got, want)
	}
}

// TestS950NoDefaultClient 是断言 ②：不许用 `http.DefaultClient`。
//
// ⚠ 它没有 Timeout（= 没有上界），而且绕过本仓所有传输层配置。
func TestS950NoDefaultClient(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.DefaultClients) == 0 {
		return
	}
	for _, h := range res.DefaultClients {
		t.Errorf("%s:%d（%s）用了 `http.DefaultClient`\n"+
			"⇒ 它**没有 Timeout**（= 没有时间上界）⇒ 上游不返回就永久挂住\n"+
			"⇒ ⇒⇒ 而且它绕过了本仓构造的客户端（传输层配置、SSRF 防护一律不生效）\n"+
			"⇒ 改法：outbound.NewClient(超时)。", h.File, h.Line, h.Func)
	}
}

// TestS950NoPackageLevelHelpers 是断言 ③：不许用包级便利函数。
//
// ⚠ `http.Get` / `http.Post` / `http.Head` / `http.PostForm` 内部就是 `http.DefaultClient`
// 　 ⇒⇒⇒ 与断言 ② 同族，但它是**另一族形态** —— 只禁 DefaultClient 会漏掉它。
func TestS950NoPackageLevelHelpers(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.PkgHelpers) == 0 {
		return
	}
	for _, h := range res.PkgHelpers {
		t.Errorf("%s:%d（%s）用了包级 http 便利函数\n"+
			"⇒ 它们内部走的是 `http.DefaultClient` ⇒ **没有时间上界**\n"+
			"⇒ ⇒⇒ 上游不返回就永久挂住\n"+
			"⇒ 改法：outbound.NewClient(超时) + http.NewRequestWithContext(ctx, …)。", h.File, h.Line, h.Func)
	}
}

// TestS950NoHTTPClientValueType 是断言 ④：不许用 `http.Client` 的**值类型**。
//
// ⚠ 值形态的 Timeout 恒为 0 ⇒ 天然无上界；指针形态（`*http.Client`）只是「装客户端的槽位」，合法。
func TestS950NoHTTPClientValueType(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.ClientValueTypes) == 0 {
		return
	}
	for _, h := range res.ClientValueTypes {
		t.Errorf("%s:%d（%s）把 `http.Client` 当**值类型**用\n"+
			"⇒ 值形态的 Timeout 恒为 0 ⇒ **没有时间上界**\n"+
			"⇒ ⇒⇒ 用指针形态（`*http.Client`），并交给 outbound.NewClient 构造。", h.File, h.Line, h.Func)
	}
}

// TestS950NoNewHTTPClient 是断言 ④a：不许 `new(http.Client)`。
//
// ⚠ 为什么单独一条：断言 ① 只禁**字面量**（`http.Client{…}`），
// 　 而 `new(http.Client)` 造出来的是**零值**客户端（Timeout 恒为 0）
// 　 ⇒⇒⇒ 它是「没有字面量」形态的无上界客户端 ⇒ 一样判红。
//
// ⚠ 这条是**写变异时才发现的洞**（§949 的 M9 同族）：
// 　 原本以为「禁掉字面量就禁掉了所有造客户端的写法」，
// 　 写 N4 负控时才发现 `new(http.Client)` 这条路是通的 ⇒ 补上。
func TestS950NoNewHTTPClient(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.NewHTTPClients) == 0 {
		return
	}
	for _, h := range res.NewHTTPClients {
		t.Errorf("%s:%d（%s）用了 `new(http.Client)`\n"+
			"⇒ 它是**零值**客户端（Timeout 恒为 0）⇒ **没有时间上界**\n"+
			"⇒ ⇒⇒ 改法：outbound.NewClient(超时)。", h.File, h.Line, h.Func)
	}
}

// TestS950NoDotImport 是断言 ④b：不许点导入 net/http。
//
// ⚠ 为什么要单独禁：点导入会让 `http.Client` 变成 `Client`，
// 　 ⇒⇒⇒ 本扫描器的**所有**识别形态在那个文件里全部失效
// 　 ⇒⇒⇒ 那道门会**悄悄失明** —— 比没有门更糟。
// 　 ⇒⇒⇒ 所以这种文件直接判红，而不是「扫不到就当没有」。
func TestS950NoDotImport(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if len(res.DotImports) == 0 {
		return
	}
	for _, h := range res.DotImports {
		t.Errorf("%s 点导入了 net/http\n"+
			"⇒ ⇒⇒ 本卡口的扫描器在那个文件里会**整族失明**（`Client{}` / `DefaultClient` 都认不出）\n"+
			"⇒ ⇒⇒ 门会悄悄放过真正的无上界客户端 ⇒ 比没有门更糟\n"+
			"⇒ 改法：改成具名导入（import \"net/http\"）。", h.File)
	}
}

// TestS950CtxlessRequestLedger 是断言 ⑤：台账与源码**双向相等**。
func TestS950CtxlessRequestLedger(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)

	got := map[string]string{}
	for _, h := range res.CtxlessRequests {
		got[s950KeyOf(h)] = h.Kind
	}
	want := map[string]string{}
	for _, r := range s950CtxlessLedger {
		if _, dup := want[r.Key]; dup {
			t.Errorf("台账里 %q 出现两次", r.Key)
		}
		if strings.TrimSpace(r.Why) == "" {
			t.Errorf("台账项 %q 的理由是空的\n"+
				"⇒ ⇒⇒ 没写理由 = 没人知道「上界由谁提供」⇒ 这个例外就变成了「没人负责的例外」", r.Key)
		}
		want[r.Key] = r.Why
	}
	for key, kind := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s 有一处不带 ctx 的 http.NewRequest，但**台账里没有登记**\n"+
				"⇒ 要么在 s950CtxlessLedger 里登记（写清上界由谁提供），要么改用 http.NewRequestWithContext\n"+
				"⇒ 命中形态：%s", key, kind)
			continue
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("台账里的 %q 在源码里**不存在了**\n"+
				"⇒ 要么它被改好了（那就从台账里删掉），要么台账过期了", key)
		}
	}
	if got, want := len(s950CtxlessLedger), 3; got != want {
		t.Errorf("台账行数 = %d（期望 %d）⇒ 台账行数变了必须重新定性", got, want)
	}
	if got, want := len(res.CtxlessRequests), 3; got != want {
		t.Errorf("不带 ctx 的 http.NewRequest 总数 = %d（期望 %d）⇒ 数量变了必须重新定性", got, want)
	}
}

// TestS950StreamingClientLedger 是断言 ⑥：台账与源码**双向相等**。
func TestS950StreamingClientLedger(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)

	got := map[string]string{}
	for _, h := range res.StreamingCalls {
		got[s950KeyOf(h)] = h.Kind
	}
	want := map[string]string{}
	for _, r := range s950StreamingLedger {
		if _, dup := want[r.Key]; dup {
			t.Errorf("台账里 %q 出现两次", r.Key)
		}
		if strings.TrimSpace(r.Why) == "" {
			t.Errorf("台账项 %q 的理由是空的\n"+
				"⇒ ⇒⇒ 没有总超时的客户端，理由必须写清「上界由谁提供」（SSE 形态：请求 ctx）", r.Key)
		}
		want[r.Key] = r.Why
	}
	for key, kind := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s 有一处 outbound.NewStreamingClient，但**台账里没有登记**\n"+
				"⇒ 它是**没有总超时**的客户端 ⇒ ⇒⇒ 上界必须写清由谁提供（通常是请求 ctx）\n"+
				"⇒ ⇒⇒ 要么在 s950StreamingLedger 里登记，要么改用 outbound.NewClient（带总超时）\n"+
				"⇒ 命中形态：%s", key, kind)
			continue
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("台账里的 %q 在源码里**不存在了**\n"+
				"⇒ 要么它被改成了带总超时的客户端（那就从台账里删掉），要么台账过期了", key)
		}
	}
	if got, want := len(s950StreamingLedger), 4; got != want {
		t.Errorf("台账行数 = %d（期望 %d）⇒ 台账行数变了必须重新定性", got, want)
	}
	if got, want := len(res.StreamingCalls), 4; got != want {
		t.Errorf("outbound.NewStreamingClient 调用点总数 = %d（期望 %d）⇒ 数量变了必须重新定性", got, want)
	}
}

// TestS950ScanScopeNotCollapsed 是断言 ⑦：扫描范围没有塌缩。
//
// ⚠ 「门绿」有两种原因：判据没洞 / **根本没扫到东西**。
//
//	⇒⇒⇒ 所以要钉一个下界：受检文件数不能低于阈值。
func TestS950ScanScopeNotCollapsed(t *testing.T) {
	root := repoRoot(t)
	res := s950ScanAll(t, root)
	if res.ScannedFiles < s950MinScannedFiles {
		t.Errorf("受检文件数 = %d（期望 >= %d）\n"+
			"⇒ ⇒⇒ 扫描范围塌缩了（改了路径前缀？漏了目录？）⇒ 门会**绿得毫无意义**\n"+
			"⇒ ⚠ 真正的范围塌缩防护在台账双向相等（登记的 7 个点扫不到就会失配）",
			res.ScannedFiles, s950MinScannedFiles)
	}
}

// TestS950ScannerHasTeeth 是**自证臂**：证明扫描器真的能抓到那些形态。
//
// ⚠ 没有这一条，「门绿」就分不清是「仓里真的干净」还是「量具坏了」
// 　 ⇒⇒⇒ 那正是 §949 的 M9（量具整族漏检）同族的坑。
//
// ⚠ 这里同时是**负控**：合法形态（带 ctx 的请求、字段名叫 http 的 Post 调用）
// 　 ⇒⇒⇒ 必须**扫不到** —— 否则这道门会把正常代码判红（误报），那比漏报更招人烦。
func TestS950ScannerHasTeeth(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want func(res s950ScanResult) bool
	}{
		{
			name: "裸 http.Client 字面量",
			src: `package p

import "net/http"

func f() { c := &http.Client{}; _ = c }
`,
			want: func(r s950ScanResult) bool { return len(r.ClientLiterals) == 1 },
		},
		{
			name: "别名导入的 http.Client 字面量（不能只认字面量 http）",
			src: `package p

import h "net/http"

func f() { c := &h.Client{}; _ = c }
`,
			want: func(r s950ScanResult) bool { return len(r.ClientLiterals) == 1 },
		},
		{
			name: "http.DefaultClient",
			src: `package p

import "net/http"

func f() { resp, _ := http.DefaultClient.Get("http://x"); _ = resp }
`,
			want: func(r s950ScanResult) bool { return len(r.DefaultClients) == 1 },
		},
		{
			name: "包级便利函数 http.Get",
			src: `package p

import "net/http"

func f() { resp, _ := http.Get("http://x"); _ = resp }
`,
			want: func(r s950ScanResult) bool { return len(r.PkgHelpers) == 1 },
		},
		{
			name: "不带 ctx 的 http.NewRequest",
			src: `package p

import "net/http"

func f() { req, _ := http.NewRequest("GET", "http://x", nil); _ = req }
`,
			want: func(r s950ScanResult) bool { return len(r.CtxlessRequests) == 1 },
		},
		{
			name: "outbound.NewStreamingClient",
			src: `package p

import "github.com/halfking/pocket-opencode/backend/internal/outbound"

func f() { c := outbound.NewStreamingClient(); _ = c }
`,
			want: func(r s950ScanResult) bool { return len(r.StreamingCalls) == 1 },
		},
		{
			name: "http.Client 的值类型",
			src: `package p

import "net/http"

func f(c http.Client) { _ = c }
`,
			want: func(r s950ScanResult) bool { return len(r.ClientValueTypes) == 1 },
		},
		// ↓↓↓ 负控：以下形态**必须扫不到** ↓↓↓
		{
			name: "new(http.Client)（零值客户端，没有字面量形态）",
			src: `package p

import "net/http"

func f() { c := new(http.Client); _ = c }
`,
			want: func(r s950ScanResult) bool { return len(r.NewHTTPClients) == 1 },
		},
		{
			name: "负控：带 ctx 的 http.NewRequestWithContext",
			src: `package p

import (
	"context"
	"net/http"
)

func f() { req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://x", nil); _ = req }
`,
			want: func(r s950ScanResult) bool {
				return len(r.CtxlessRequests) == 0 && len(r.ClientLiterals) == 0 &&
					len(r.DefaultClients) == 0 && len(r.PkgHelpers) == 0
			},
		},
		{
			name: "负控：字段名叫 http 的 Post 调用（不是包级 http.Post）",
			src: `package p

import "net/http"

type s struct{ http *http.Client }

func f(x *s) { resp, _ := x.http.Post(nil); _ = resp }
`,
			want: func(r s950ScanResult) bool {
				return len(r.PkgHelpers) == 0 && len(r.ClientValueTypes) == 0
			},
		},
		{
			name: "负控：指针形态的 *http.Client（字段类型）",
			src: `package p

import "net/http"

type s struct{ c *http.Client }
`,
			want: func(r s950ScanResult) bool { return len(r.ClientValueTypes) == 0 },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "synthetic.go", c.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("合成源码解析失败：%v", err)
			}
			res := s950ScanFile(fset, f, "synthetic.go")
			if !c.want(res) {
				t.Errorf("扫描器的判断不对：\n"+
					"  ClientLiterals=%d DefaultClients=%d PkgHelpers=%d ClientValueTypes=%d CtxlessRequests=%d StreamingCalls=%d\n"+
					"⇒ ⇒⇒ 这条用例是本卡口的**自证臂** ⇒ 它不过 ⇒ 扫描器可能已经坏了",
					len(res.ClientLiterals), len(res.DefaultClients), len(res.PkgHelpers),
					len(res.ClientValueTypes), len(res.CtxlessRequests), len(res.StreamingCalls))
			}
		})
	}
}

// s950SortHits 排序（保证报错输出稳定）。
func s950SortHits(hits []s950Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Line < hits[j].Line
	})
}
