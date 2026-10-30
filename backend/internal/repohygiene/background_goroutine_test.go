package repohygiene

// background_goroutine_test.go — §949 卡口：后端**后台 goroutine 的 panic**不许打死整个进程。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，后台 goroutine 必须经由 internal/background 启动。**
//
// # 为什么（§949 的实测依据，不是推理）
//
//	Go 的 runtime 语义：**任何 goroutine 里未被 recover 的 panic 都会终止整个进程**
//	—— 不是只终止那个 goroutine。
//
//	本仓原本只有两处 panic 边界：
//
//	  - internal/server/middleware.go 的 recoveryMiddleware —— 只覆盖**请求 goroutine**；
//	  - internal/scheduledtask/scheduler.go 的 executeSafely —— 只覆盖**任务执行器**。
//
//	⇒⇒⇒ 从请求里 `go` 出去的后台 goroutine **在那个边界之外**：
//		panic 从另一个 goroutine 逃逸，请求中间件的 `defer recover()` 抓不到
//	⇒⇒⇒ 一次 panic = 整个 pocketd 进程死
//	⇒⇒⇒ ⇒ 正在录的会议、正在同步的邮件、全部 WebSocket 连接一起没。
//
//	★ 而「panic 会直接带走整个进程」这句话**本仓自己早就写下来了**
//	（internal/email/scheduler.go 那个 goroutine 的注释）⇒⇒⇒
//	⇒ 那不是新口径，是**已写明却只兑现了 2 处的约定** ⇒⇒⇒ 本卡口把它补齐。
//
// # 实测数字（§949.0 的普查）
//
//	后端非测试代码的 `go` 语句：**79 处**（§949 之前是 80：79 + 新增的 background 包自身那 1 处）
//	├ 已有顶层 recover：**2 处**（internal/email/scheduler.go、internal/server/server_auth_extended.go）
//	└ 裸奔：**77 处**，跨 **40** 个文件
//
//	⚠⚠ 普查量具踩了**两个**坑，都记在这里（否则下一个人会重犯）：
//
//	① **父链错位**：`ast.Inspect` 的 `f(nil)` 与 `ast.Walk` 对 nil 子节点
//	   （如 `if` 的 `Init`）多发的那一次 nil 访问不配对 ⇒ 用「栈」建父节点映射
//	   会整体错位 ⇒ ⇒⇒ 「有没有 Close」这种判据会**恒假**（读数 0 处已关闭）。
//	② **整族漏检**：`go recv.Method()`（选择器形态）一开始根本没进统计
//	   ⇒ 读数从 77 修正到 **77 处的完整集合**（方法形态 46 + 字面量 31 + 具名 1）。
//	   ⇒ 「量出来的数比该有的少 ⇒ 先查某一族形态整个没被认出来」。
//
// # 口径
//
//	① 受检范围内（backend/ 非测试代码，**排除 internal/background 自身**）
//	   **不许有任何 `go` 语句** ⇒ 后台 goroutine 一律走 `background.Go*`。
//	② 每个 `background.Go*` 必须有**非空**的**唯一**名字 ⇒ 事后能定位是哪个任务炸了。
//	③ `internal/background` 的兜底实现里必须**同时**有 `recover()` **和**日志
//	   ⇒ 只有 recover 没有日志 = 把「进程崩溃」换成「静默吞掉 panic」
//	   ⇒ ⇒⇒ 那正是 §945/§946/§948 这一串在消灭的东西：**失败不许无声**。
//
// # 为什么用 go/parser 走 AST（而不是 grep）
//
//	★ §943/§945 的教训：「grep 只看到了我让它看的形态」。
//	⇒ 这里走 AST ⇒ 注释天然不算数。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① `*_test.go` 与 `testdata/` 不是生产路径 ⇒ 排除。
//	② `backend/third_party/` 是上游 vendored ⇒ 排除。
//	③ 本卡口只看**后端**；前端的等价族见 §948（promise 形态的静默拒绝）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s949BackgroundRel 是 internal/background 包的相对路径。
//
// ⚠ 它是**豁免**而不是「不管」：那个包里唯一的 `go` 语句正是兜底实现自身
//
//	（它自己带顶层 defer recover）⇒⇒ 由 TestS949HelperRecoversAndLogs 单独把住。
const s949BackgroundRel = "backend/internal/background/background.go"

// s949LedgerRow 是台账的一行：**一个后台任务的名字 → 它在哪个文件**。
//
// ⚠ 为什么键是「任务名」而不是「文件:行号」：
//
//	行号是**坐标**不是**身份**，会随任何一次编辑漂移
//	⇒⇒⇒ 台账会在无关改动后飘红 ⇒⇒⇒ 人就开始忽略这道门 ⇒⇒⇒ ⇒ 门一旦被忽略就等于没有。
//	⇒⇒⇒ 任务名是**身份**：它出现在 panic 日志里，是运维真正用来定位的东西。
//
// ⚠ 为什么台账要「双向相等」：
//
//	只钉「台账里的都还在」⇒ 有人**新增**一个裸 goroutine 不红；
//	只钉「源码里的都在台账」⇒ 有人**删掉**一个后台任务不红。
//	⇒⇒⇒ 两个方向都要钉。
type s949LedgerRow struct {
	// Name 是 background.Go* 的第一个参数（任务名）。
	Name string
	// File 是任务所在的仓库相对路径。
	File string
}

// s949Ledger 是台账本体（§949 修过/登记过的全部后台任务）。
//
// ⚠ 条数由 TestS949LedgerMatchesSource 的绝对条数断言钉住。
var s949Ledger = []s949LedgerRow{
	{"agent/acp-stdio-events", "backend/internal/agent/adapter_acp_stdio.go"},
	{"agent/health-check", "backend/internal/agent/registry.go"},
	{"agent/http-sse-reader", "backend/internal/agent/transport_http.go"},
	{"agent/mock-events", "backend/internal/agent/adapter_mock.go"},
	{"agent/opencode-events", "backend/internal/agent/adapter_opencode.go"},
	{"agent/pi-events", "backend/internal/agent/adapter_pi.go"},
	{"agent/stdio-stderr", "backend/internal/agent/transport_stdio.go"},
	{"agent/stdio-stdout", "backend/internal/agent/transport_stdio.go"},
	{"agent/stdio-wait", "backend/internal/agent/transport_stdio.go"},
	{"agent/ws-reader", "backend/internal/agent/transport_ws.go"},
	{"assistant/classify-emails", "backend/internal/server/server_assistant.go"},
	{"assistant/email-sync", "backend/internal/server/server_assistant.go"},
	{"audit/exporter", "backend/cmd/pocketd/main.go"},
	{"auth/redclaw-mirror", "backend/internal/server/server_auth_extended.go"},
	{"auth/send-code-email", "backend/internal/server/server_auth_extended.go"},
	{"client-ws/read-pump", "backend/internal/server/server_plugin_ws.go"},
	{"client-ws/write-pump", "backend/internal/server/server_plugin_ws.go"},
	{"email-pending/gc-loop", "backend/cmd/pocketd/main.go"},
	{"email/account-sync", "backend/internal/email/pipeline.go"},
	{"email/account-sync-run", "backend/internal/email/pipeline.go"},
	{"email/body-fetch", "backend/internal/email/pipeline.go"},
	{"email/daily-summary-loop", "backend/internal/email/scheduler.go"},
	{"email/fetcher", "backend/internal/email/fetcher.go"},
	{"email/imap-deadline-watchdog", "backend/internal/email/mime.go"},
	{"email/intent-loop", "backend/internal/email/scheduler.go"},
	{"email/invoice-extract", "backend/internal/server/server_email_invoice.go"},
	{"email/pipeline-loop", "backend/internal/email/scheduler.go"},
	{"email/poll-loop", "backend/internal/email/scheduler.go"},
	{"email/refresh-loop", "backend/internal/email/scheduler.go"},
	{"email/scheduler-account-sync", "backend/internal/email/scheduler.go"},
	{"email/vacation-loop", "backend/internal/email/scheduler.go"},
	{"gateway/node-overview", "backend/internal/server/llm_gateway_nodes_proxy.go"},
	{"localagent/execute", "backend/internal/localagent/localagent.go"},
	{"manager-ws/read-pump", "backend/internal/server/server_plugin_ws.go"},
	{"manager-ws/write-pump", "backend/internal/server/server_plugin_ws.go"},
	{"mobile-ws/read-pump", "backend/internal/websocket/mobile_hub.go"},
	{"mobile-ws/write-pump", "backend/internal/websocket/mobile_hub.go"},
	{"note/classify", "backend/internal/server/server_note_ingest.go"},
	{"notify/smtp-send", "backend/internal/notify/smtp.go"},
	{"opencode-http/subscribe-events", "backend/internal/adapter/opencode_http.go"},
	{"opencode/approval-broadcaster", "backend/cmd/pocketd/main.go"},
	{"opencode/event-forward", "backend/cmd/pocketd/main.go"},
	{"opencode/event-pump", "backend/cmd/pocketd/main.go"},
	{"opencode/event-stream", "backend/internal/opencode/event_stream.go"},
	{"opencode/idle-refresh", "backend/cmd/pocketd/main.go"},
	{"opencode/list-sessions", "backend/internal/opencode/manager.go"},
	{"opencode/permission-events", "backend/internal/opencode/permission_manager.go"},
	{"opencode/permission-manager", "backend/cmd/pocketd/main.go"},
	{"opencode/permission-poll", "backend/internal/opencode/permission_manager.go"},
	{"opencode/question-events", "backend/internal/opencode/question_manager.go"},
	{"opencode/question-manager", "backend/cmd/pocketd/main.go"},
	{"opencode/question-poll", "backend/internal/opencode/question_manager.go"},
	{"opencode/session-event-broadcaster", "backend/cmd/pocketd/main.go"},
	{"opencode/session-event-subscribe", "backend/internal/opencode/session_event_broadcaster.go"},
	{"opencode/session-idle-check", "backend/internal/opencode/manager.go"},
	{"opencode/session-idle-recheck", "backend/internal/opencode/manager.go"},
	{"opencode/upstream-loop", "backend/internal/opencode/event_stream.go"},
	{"plugin-ws/read-pump", "backend/internal/server/server_plugin_ws.go"},
	{"resource-auth/revalidate", "backend/internal/server/resource_middleware.go"},
	{"plugin-ws/write-pump", "backend/internal/server/server_plugin_ws.go"},
	{"redclaw/health-loop", "backend/internal/redclaw/bridge.go"},
	{"registry/auto-discovery", "backend/cmd/pocketd/main.go"},
	{"registry/health-check", "backend/internal/registry/registry.go"},
	{"registry/probe-candidate", "backend/internal/registry/discovery.go"},
	{"rss/digest-loop", "backend/internal/rss/digest_service.go"},
	{"rss/scan-scope", "backend/internal/rss/scheduler.go"},
	{"rss/scheduler-loop", "backend/internal/rss/scheduler.go"},
	{"scheduledtask/loop", "backend/internal/scheduledtask/scheduler.go"},
	{"scheduledtask/scan", "backend/internal/scheduledtask/scheduler.go"},
	{"scheduledtask/trigger-now", "backend/internal/scheduledtask/scheduler.go"},
	{"session/list-collector", "backend/internal/server/session_aggregate.go"},
	{"session/list-instances", "backend/internal/server/session_aggregate.go"},
	{"tasksync/disk-session-loop", "backend/internal/tasksync/disksession_scheduler.go"},
	{"tasksync/loop", "backend/internal/tasksync/scheduler.go"},
	{"webauthn/challenge-cleanup", "backend/internal/auth/webauthn_verifier.go"},
	{"websocket/hub", "backend/internal/server/server.go"},
	{"websocket/plugin-hub", "backend/internal/server/server.go"},
	{"websocket/read-pump", "backend/internal/server/server.go"},
	{"websocket/write-pump", "backend/internal/server/server.go"},
	{"wecom/broadcast", "backend/internal/wecom/handler.go"},
}

// s949GoStmt 是一次 `go` 语句。
type s949GoStmt struct {
	File string
	Line int
	// Text 是源码行（只用于报错定位）。
	Text string
	// SelfProtected 判这个 goroutine 自己有没有顶层 defer 的 recover()。
	SelfProtected bool
}

// s949Call 是一个 background.Go* 调用点。
type s949Call struct {
	Name string
	File string
	Line int
	// Fn 是被调用的函数名（Go / Go1 … Go4）。
	Fn string
}

// s949ScanResult 是一次扫描的产物。
type s949ScanResult struct {
	// GoStmts 是受检范围内的**全部** `go` 语句。
	GoStmts []s949GoStmt
	// Calls 是 background.Go* 调用点（名字 → 位置）。
	Calls []s949Call
	// AllGoStmts 是**整个后端**（含 internal/background）的 `go` 语句总数。
	AllGoStmts int
}

// s949ScanAll 扫**全部**受检文件。
func s949ScanAll(t *testing.T, root string) s949ScanResult {
	t.Helper()
	res := s949ScanResult{}
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
		sub := s949ScanAST(fset, f, rel)
		res.Calls = append(res.Calls, sub.Calls...)
		res.AllGoStmts += len(sub.GoStmts)
		// ⚠ internal/background 自身的 `go` 语句是**兜底实现**⇒ 不算违规。
		if rel == s949BackgroundRel {
			continue
		}
		res.GoStmts = append(res.GoStmts, sub.GoStmts...)
	}
	sort.Slice(res.Calls, func(i, j int) bool {
		if res.Calls[i].File != res.Calls[j].File {
			return res.Calls[i].File < res.Calls[j].File
		}
		return res.Calls[i].Line < res.Calls[j].Line
	})
	return res
}

// s949ScanAST 在**已解析的 AST** 上找 `go` 语句与 background.Go* 调用点。
//
// ⚠ 单独抽出来是为了让「自证臂」能拿**合成源码**喂进来。
func s949ScanAST(fset *token.FileSet, f *ast.File, rel string) s949ScanResult {
	res := s949ScanResult{}
	bgLocal := s949BackgroundLocalName(f)
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.GoStmt:
			pos := fset.Position(v.Pos())
			stmt := s949GoStmt{File: rel, Line: pos.Line}
			if lit, ok := v.Call.Fun.(*ast.FuncLit); ok {
				stmt.SelfProtected = s949HasTopLevelDeferRecover(lit.Body)
			}
			res.GoStmts = append(res.GoStmts, stmt)
			return true
		case *ast.CallExpr:
			fn, ok := s949BackgroundCallee(v.Fun)
			if !ok || fn == "" {
				return true
			}
			// ⚠ 本文件没 import internal/background ⇒ 不该有它的调用。
			if bgLocal == "" {
				return true
			}
			// ⚠ 只认 background.Go / Go1 … Go4。
			//   ⚠⚠ 首版写得太宽（凡选择器调用都算）⇒ 把 log.Printf 之类
			//   全认成了「后台任务」⇒ 台账对不上 ⇒ 这是**量具过宽**的又一次实录。
			base := bgLocal + ".Go"
			switch fn {
			case base, base + "1", base + "2", base + "3", base + "4":
			default:
				return true
			}
			name := ""
			if len(v.Args) > 0 {
				if lit, ok := v.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					name, _ = strconv.Unquote(lit.Value)
				}
			}
			pos := fset.Position(v.Pos())
			res.Calls = append(res.Calls, s949Call{
				Name: name,
				File: rel,
				Line: pos.Line,
				Fn:   fn,
			})
			return true
		}
		return true
	})
	return res
}

// s949BackgroundLocalName 找出本文件里 internal/background 包被 import 成的**本地名**。
//
// ⚠ 之所以要走 import 而不是直接认字面量 "background"：
//
//	import 别名是合法的（import bg "…/internal/background"）⇒
//	只认字面量会让别名用法**整族漏检** ⇒⇒⇒ 那正是 §948 记的「量具漏了一族」。
func s949BackgroundLocalName(f *ast.File) string {
	const want = "github.com/halfking/pocket-opencode/backend/internal/background"
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != want {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "background"
	}
	return ""
}

// s949BackgroundCallee 判一个调用是不是 background.Go*，返回被调函数名。
//
// ⚠ 要同时认三种形态（§949 的普查量具漏过其中一族）：
//
//	① background.Go("x", fn)          —— 普通调用
//	② background.Go2[A, B]("x", fn, a, b) —— 泛型实例化（IndexListExpr）
//	③ background.Go2[A]("x", fn, a)   —— 单个类型实参（IndexExpr）
func s949BackgroundCallee(fn ast.Expr) (string, bool) {
	switch v := fn.(type) {
	case *ast.SelectorExpr:
		return s949ExprText(v.X) + "." + v.Sel.Name, true
	case *ast.IndexExpr:
		return s949BackgroundCallee(v.X)
	case *ast.IndexListExpr:
		return s949BackgroundCallee(v.X)
	}
	return "", false
}

func s949ExprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return s949ExprText(v.X) + "." + v.Sel.Name
	}
	return "?"
}

// s949HasTopLevelDeferRecover 判一个函数体里有没有**顶层**的 defer recover()。
//
// ⚠ 必须是**顶层**：recover() 只有在**同一个 goroutine** 的 defer 里才有效
//
//	⇒ 嵌在另一个 func literal 里的 recover 抓不到外层的 panic。
func s949HasTopLevelDeferRecover(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		ds, ok := stmt.(*ast.DeferStmt)
		if !ok {
			continue
		}
		switch f := ds.Call.Fun.(type) {
		case *ast.FuncLit:
			if s949ContainsRecover(f.Body) {
				return true
			}
		}
	}
	return false
}

// s949ContainsRecover 判一段代码里有没有调用 recover()。
func s949ContainsRecover(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "recover" {
				found = true
			}
		}
		return true
	})
	return found
}

// TestS949NoBareGoroutine 是断言 ①：受检范围内**不许有裸 `go` 语句**。
//
// ⚠ 这是本卡口的**牙**：光有「后台任务都在台账里」不够 ——
//
//	否则「直接写个 `go func(){}()`」这条路是通的 ⇒⇒⇒ panic 又能打死进程了。
func TestS949NoBareGoroutine(t *testing.T) {
	root := repoRoot(t)
	res := s949ScanAll(t, root)
	if len(res.GoStmts) == 0 {
		return
	}
	for _, g := range res.GoStmts {
		extra := ""
		if g.SelfProtected {
			extra = "（它自己带了顶层 recover，但**口径是统一走 background.Go***：\n" +
				"　　⇒ 统一才有唯一的任务名可定位 ⇒ 散落的 recover 各写各的日志格式 ⇒ 事后拼不出「是哪个任务」）"
		}
		t.Errorf("%s:%d 有一处 `go` 语句没有走 background.Go%s\n"+
			"⇒ Go 的 runtime 语义：未被 recover 的 panic 会**终止整个进程**\n"+
			"⇒ ⇒⇒ 一次 panic = 整个 pocketd 死 ⇒⇒⇒ 正在录的会议、正在同步的邮件、全部 WebSocket 连接一起没。\n"+
			"⇒ 改法：background.Go(\"任务名\", func() { … })（任务名要全仓唯一）。",
			g.File, g.Line, extra)
	}
}

// TestS949AbsoluteCounts 是断言 ②：**绝对条数**。
//
// ⚠ 为什么必须钉绝对条数：
//
//	「扫描范围没塌缩」和「数字对得上」是两件事。
//	⇒⇒⇒ 例如有人把 backend/ 换成 backend/i ⇒ 扫不到东西 ⇒ 门照样绿。
//	⇒⇒⇒ 所以要钉：整个后端（含 internal/background）的 `go` 语句总数 = 1，
//	　　　background.Go* 调用点总数 = 79。
func TestS949AbsoluteCounts(t *testing.T) {
	root := repoRoot(t)
	res := s949ScanAll(t, root)

	if got, want := res.AllGoStmts, 1; got != want {
		t.Errorf("整个后端非测试代码的 `go` 语句总数 = %d（期望 %d）\n"+
			"⇒ 期望的那 1 处是 internal/background 自己的兜底实现 ⇒⇒⇒ 其余任何新增的 `go` 都要改走 background.Go",
			got, want)
	}
	if got, want := len(res.Calls), 80; got != want {
		t.Errorf("background.Go* 调用点总数 = %d（期望 %d）⇒ 数量变了必须重新定性", got, want)
	}
}

// TestS949BackgroundNamesValid 是断言 ②b：每个 background.Go* 都有**非空**且**唯一**的名字。
//
// ⚠ 名字为空 = panic 日志里没有任务名 ⇒ 事后定位不到是哪个任务炸的。
// ⚠ 名字重复 = 同一份日志里两个任务同名 ⇒ 同样定位不到。
func TestS949BackgroundNamesValid(t *testing.T) {
	root := repoRoot(t)
	res := s949ScanAll(t, root)
	seen := map[string]s949Call{}
	for _, c := range res.Calls {
		if strings.TrimSpace(c.Name) == "" {
			t.Errorf("%s:%d 的 background.%s 第一个参数（任务名）是空的\n"+
				"⇒ panic 日志里会没有任务名 ⇒ 事后定位不到是哪个任务炸的", c.File, c.Line, c.Fn)
			continue
		}
		if prev, dup := seen[c.Name]; dup {
			t.Errorf("任务名 %q 重复：%s:%d 与 %s:%d\n"+
				"⇒ 两个后台任务同名 ⇒ panic 日志分不出是哪个", c.Name, prev.File, prev.Line, c.File, c.Line)
			continue
		}
		seen[c.Name] = c
	}
}

// TestS949LedgerMatchesSource 是断言 ③：台账与源码**双向相等**。
func TestS949LedgerMatchesSource(t *testing.T) {
	root := repoRoot(t)
	res := s949ScanAll(t, root)

	got := map[string]string{}
	for _, c := range res.Calls {
		got[c.Name] = c.File
	}
	want := map[string]string{}
	for _, r := range s949Ledger {
		if _, dup := want[r.Name]; dup {
			t.Errorf("台账里 %q 出现两次", r.Name)
		}
		want[r.Name] = r.File
	}
	for name, file := range got {
		wf, ok := want[name]
		if !ok {
			t.Errorf("源码里有后台任务 %q（%s）但**台账里没有登记**\n"+
				"⇒ 要么在 s949Ledger 里登记，要么改掉（裸 `go` 一律判红，见 TestS949NoBareGoroutine）",
				name, file)
			continue
		}
		if wf != file {
			t.Errorf("后台任务 %q 的文件不一致：台账=%s 源码=%s", name, wf, file)
		}
	}
	for name, file := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("台账里的后台任务 %q（%s）在源码里**不存在了**\n"+
				"⇒ 要么任务被删了（删了要重新定性），要么台账过期了", name, file)
		}
	}
	if got, want := len(s949Ledger), 80; got != want {
		t.Errorf("台账行数 = %d（期望 %d）⇒ 台账行数变了必须重新定性", got, want)
	}
}

// TestS949HelperRecoversAndLogs 是断言 ④：兜底实现必须**同时**有 recover 和日志。
//
// ⚠ 为什么「有日志」是硬要求：
//
//	只有 recover 没有日志 ⇒ panic 被**静默**吞掉 ⇒ 进程活着但那个后台任务死了
//	⇒⇒⇒ 「哪个任务不干活了」没人知道 ⇒⇒⇒ 那正是 §945/§946/§948
//	⇒⇒⇒ 这一串审计一直在消灭的东西：**失败不许无声**。
func TestS949HelperRecoversAndLogs(t *testing.T) {
	root := repoRoot(t)
	path := root + "/" + s949BackgroundRel
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", s949BackgroundRel, err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "Go" {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatalf("%s 里找不到 background.Go 的实现", s949BackgroundRel)
	}
	if !s949ContainsRecover(fn.Body) {
		t.Errorf("%s 的 background.Go 里**没有 recover()**\n"+
			"⇒ 它的存在意义就是兜住 panic ⇒ 没有 recover 的兜底 = 没有兜底", s949BackgroundRel)
	}
	if !s949HasLogCall(fn.Body) {
		t.Errorf("%s 的 background.Go 里 recover 之后**没有日志**\n"+
			"⇒ panic 被静默吞掉 ⇒「哪个后台任务死了」没人知道\n"+
			"⇒ ⇒⇒ 失败不许无声（§945/§946/§948 同一条口径）", s949BackgroundRel)
	}
}

// s949HasLogCall 判一段代码里有没有调用 log.* / slog.* 之类的日志函数。
func s949HasLogCall(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := ce.Fun.(type) {
		case *ast.SelectorExpr:
			switch f.Sel.Name {
			case "Printf", "Println", "Print", "Error", "Warn", "Info", "Errorf", "Warnf", "Infof":
				found = true
			}
		}
		return true
	})
	return found
}

// TestS949ScannerHasTeeth 是**自证臂**：证明扫描器不是恒空的。
//
// ⚠ 为什么必须单独写一条：★ 「门绿」≠「量具有牙」——
//
//	一条只断言「源码里没有裸 go」的测试，在**扫描器坏了**时同样会绿
//	（扫描器什么都不报 ⇒「没有」成立 ⇒ 绿）⇒⇒⇒ 所以必须拿
//	**已知答案的输入**喂给扫描器，证明它**该报的会报**。
//
// ⚠ 合成样本要覆盖 §949 踩过的**两个坑**：
//
//	① 裸 `go func(){}()`（无兜底）⇒ 必须被报出来。
//	② `go` 一个**具名**函数（`go f()`）⇒ 形态不同，也必须被报出来。
//	③ 自己带顶层 defer recover 的 `go` ⇒ 仍然**算一处 go 语句**
//	   （口径是「统一走 background.Go」，不是「有 recover 就行」）。
//	④ background.Go 调用点必须被认出来并取到任务名（含泛型实例化形态）。
func TestS949ScannerHasTeeth(t *testing.T) {
	src := `package p

import (
	"context"
	"io"
	"log"

	"github.com/halfking/pocket-opencode/backend/internal/background"
)

var other = struct{ Thing func(string) }{Thing: func(string) {}}

func work() {}

func workWithRecover() {
	defer func() {
		_ = recover()
	}()
}

func a() {
	go func() {
		work()
	}()
}

func b() {
	go work()
}

func c() {
	go func() {
		defer func() {
			_ = recover()
		}()
		work()
	}()
}

func d() {
	background.Go("synthetic/one", func() { work() })
	background.Go2[context.Context, io.Reader]("synthetic/two", func(context.Context, io.Reader) {}, nil, nil)
	// ⚠ 负控：这些**不是** background 调用（首版量具过宽 ⇒ 把它们全认成了后台任务）
	log.Printf("not a background call")
	other.Thing("also not")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析合成源码失败：%v", err)
	}
	res := s949ScanAST(fset, f, "synthetic.go")

	// ① 三个 `go` 语句必须都被找到（裸的、具名的、自带 recover 的）。
	if got, want := len(res.GoStmts), 3; got != want {
		t.Errorf("扫描器在合成源码上找到 %d 处 `go`（期望 %d）⇒ 扫描器可能坏了\n%+v",
			got, want, res.GoStmts)
	}
	// ② 两个裸的（a、b）必须 SelfProtected=false；自带 recover 的（c）必须=true。
	bare := 0
	protected := 0
	for _, g := range res.GoStmts {
		if g.SelfProtected {
			protected++
		} else {
			bare++
		}
	}
	if bare != 2 {
		t.Errorf("合成源码里裸 `go` 应为 2 处（a、b），实得 %d ⇒ 扫描器的兜底识别坏了", bare)
	}
	if protected != 1 {
		t.Errorf("合成源码里自带 recover 的 `go` 应为 1 处（c），实得 %d ⇒ 扫描器的兜底识别坏了", protected)
	}
	// ③ 两个 background.Go* 调用点必须都被认出来，且任务名取对。
	if got, want := len(res.Calls), 2; got != want {
		t.Errorf("扫描器认出 %d 个 background.Go* 调用（期望 %d）⇒ 扫描器坏了", got, want)
	} else {
		if res.Calls[0].Name != "synthetic/one" {
			t.Errorf("第一个 background 调用的任务名 = %q（期望 synthetic/one）", res.Calls[0].Name)
		}
		if res.Calls[1].Name != "synthetic/two" {
			t.Errorf("第二个 background 调用的任务名 = %q（期望 synthetic/two）⇒ **泛型实例化形态**（Go2[A,B]）没被认出来", res.Calls[1].Name)
		}
	}
}
