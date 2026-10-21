package repohygiene

// smtp_deadline_test.go — §951 卡口：**等一个不会自己结束的东西，必须自带 deadline**。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，每一个出站 TCP 拨号都必须有上界，
//	而每一个 SMTP 会话都必须有一个 socket deadline。**
//
// # 为什么（§951 的实测依据，不是推理）
//
//	§950 管的是「出站 HTTP」；它**没管**出站 TCP 本身，也没管长连接协议。
//	⇒⇒⇒ 而 SMTP 恰好两样都占：它先要**建连**，建连之后还要**说话**
//　　（banner / EHLO / STARTTLS / AUTH / MAIL / RCPT / DATA / QUIT）。
//
//	**变更前**（backend/internal/notify/smtp.go）这一族是**完全裸的**：
//	  · `tls.Dial("tcp", addr, tlsCfg)`　⇒ 内部是 `net.Dial`，**没有 Timeout**
//	    ⇒ 一个被黑洞掉的 SYN 就能把建连**永久**钉住；
//	  · `smtp.Dial(addr)`（两处）　　　⇒ 同上；
//	  · 之后的**每一个**阶段**全程没有 deadline**
//	    ⇒ 一台「accept 了 TCP 之后就不说话」的 SMTP 服务器能把 Send **永久**钉住。
//
//	★ 而 `net/smtp` **没有 context 支持** ⇒ 上界**只能**落在 socket deadline 上
//　　（ctx 取消管不到已经阻塞在 `Read` 上的那一次读）。
//　　⇒⇒⇒ 所以「有 ctx」**不等于**「有上界」—— 这与 §950 的「panic 能救，阻塞救不了」同族。
//
//	⇒⇒⇒ 这一条路径是**邮箱验证码**（internal/server/server_auth_extended.go 的
//　　sendCodeEmail ⇒ 10s ctx）与后台通知共用的
//　　⇒⇒⇒ ⇒ 挂住 = 邮件通知**永久失效且无声**（验证码发不出去、用户永远收不到）。
//
// # 口径
//
//	① 受检范围内（backend 非测试代码）**不许**出现
//	   `smtp.Dial(` / `tls.Dial(` / 裸 `net.Dial(`
//	   —— 这三个是 stdlib 提供的**无拨号超时**的口子。
//	② 每个 `net.Dialer{…}` 字面量**必须**带 `Timeout:` 字段
//	   —— `net.Dialer.Timeout` 只管**建连**，所以它管不到协议阶段（那是 ③ 的事）。
//	   少数例外（拨号由 ctx + 客户端 Timeout 兜底）必须**登记在册**并写清上界来源。
//	③ 每个 `smtp.NewClient(` 调用点**所在函数**必须有一个 `SetDeadline(`
//	   —— ⚠ 没有它，SMTP 会话阶段就是**无上界等待**（这正是 §951 修掉的那处）。
//
// # 为什么用 go/parser 走 AST（而不是 grep）
//
//	★ §943/§945/§950 的反复结论：「grep 只看到了我让它看的形态」。
//	★ 本次实测：`net.Dialer{Timeout}` 这个**字符串**在 backend/internal/notify/smtp.go
//　 的**注释**里就有一处 ⇒ grep 会把注释算成违规（实测 15 vs 真值 14）。
//	★ 同一个坑的另一个面：`internal/acchttp/client.go` 的 `Timeout:` 在**下一行**
//　 ⇒ 逐行 grep 也会看漏。
//	⇒⇒⇒ 必须走 AST ⇒ 注释天然不算数，多行字面量也看得全。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① `*_test.go` 与 `testdata/` 不是生产路径 ⇒ 排除。
//	② `backend/third_party/` 是上游 vendored ⇒ 排除。
//	③ 本卡口只看**后端**。前端那一族见 §951 的前端卡口。
//	④ ★ **已知的量具边界**（如实登记，不当「没风险」）：
//	   - ③ 的判据是「**同一个函数**里有 SetDeadline」⇒ 它**不区分**这个 deadline
//	     是设在**哪条**连接上的。三个 SMTP 入口（notify / smtp_probe / smtp_send）
//	     各自只有一条连接 ⇒ 当前等价；但**将来**一个函数里若有两条连接，
//	     这条判据会**变宽**（盖不住「只给其中一条设了 deadline」）。
//	   - ② 只认 `net.Dialer{…}` **字面量**；如果有人用 `var d net.Dialer` 再赋值，
//	     本卡口看不见它（⇒ 那条路本来就没有拨号超时 ⇒ 属真实盲区，已登记）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s951MinScannedFiles 是受检文件数的**下界**。
//
// ⚠ 为什么是下界而不是精确值：精确值会在「新增一个后端文件」时飘红
// ⇒⇒⇒ 那不是本卡口要管的事 ⇒⇒⇒ 会逼着人习惯性改这个数字 ⇒⇒⇒ 改着改着就没人看了。
// ⇒⇒⇒ 真正的范围塌缩防护在**台账双向相等**。
const s951MinScannedFiles = 400

// s951LedgerRow 是台账的一行：**一个「拨号没有 Dialer.Timeout」的例外 → 上界由谁提供**。
type s951LedgerRow struct {
	// Key 是「文件#函数」（函数名是**身份**，行号是**坐标** ⇒ 行号会漂）。
	Key string
	// Why 是「上界由谁提供」—— 不许为空。
	Why string
}

// s951NoDialTimeoutLedger 是**唯一**允许「`net.Dialer` 不带 Timeout」的台账。
//
// ⚠ 为什么台账要「双向相等」：
//
//	只钉「台账里的都还在」⇒ 有人**新增**一个无超时的拨号不红；
//	只钉「源码里的都在台账」⇒ 有人**删掉**一个登记项不红。
//	⇒⇒⇒ 两个方向都要钉（§949 / §950 的同一条纪律）。
var s951NoDialTimeoutLedger = []s951LedgerRow{
	{
		Key: "backend/internal/server/ssrf.go#safeOutboundHTTPClient",
		Why: "拨号本身没有 Dialer.Timeout；上界由 outbound.NewClient(10s) 的**客户端 Timeout** 提供 —— " +
			"http.Client.Timeout 会给请求 ctx 设一个 deadline，而 Transport 的 DialContext 拿到的就是那个 ctx " +
			"⇒ 拨号被同一个上界掐断。⚠ 这也解释了为什么 §950 给 outbound 补上客户端级硬上界是必要的。",
	},
}

// s951Hit 是一处命中。
type s951Hit struct {
	File string
	Line int
	// Func 是所在的**具名**函数（匿名闭包归属于它外层的具名函数）。
	Func string
	// Kind 是命中形态的说明。
	Kind string
}

// s951ScanResult 是一次扫描的产物。
type s951ScanResult struct {
	// ScannedFiles 是受检文件数。
	ScannedFiles int
	// Dialers 是全部 `net.Dialer{…}` 字面量。
	Dialers []s951Hit
	// DialersNoTimeout 是其中**没有** `Timeout:` 字段的那些。
	DialersNoTimeout []s951Hit
	// SMTPClients 是全部 `smtp.NewClient(` 调用点。
	SMTPClients []s951Hit
	// SMTPClientsNoDeadline 是其中**所在函数没有 SetDeadline(** 的那些。
	SMTPClientsNoDeadline []s951Hit
	// BannedDialForms 是被禁的拨号形态（smtp.Dial / tls.Dial / 裸 net.Dial）。
	BannedDialForms []s951Hit
	// DotImports 是点导入（会让本扫描器整族失明 ⇒ 直接判红）。
	DotImports []s951Hit
}

// s951ImportLocalName 取某个导入路径的**本地名**。
//
// ⚠ 返回 "." 表示点导入 ⇒ 本扫描器对该包**整族失明** ⇒ 必须判红。
// ⚠ 默认名取路径最后一段（`net` / `smtp` / `tls`），与 Go 的规则一致。
func s951ImportLocalName(f *ast.File, path string) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		parts := strings.Split(p, "/")
		return parts[len(parts)-1], true
	}
	return "", false
}

// s951SelectorIs 判断 e 是不是「本地名 local 的包里的 name」。
func s951SelectorIs(e ast.Expr, local, name string) bool {
	if local == "" {
		return false
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == local
}

// s951DialerHasTimeout 判断一个 `net.Dialer{…}` 字面量有没有 `Timeout:` 字段。
//
// ⚠ 返回值有三态：
//   - true  ⇒ 有 Timeout 字段（**非零**）⇒ 拨号自带上界；
//   - false ⇒ 没有 Timeout 字段 ⇒ 必须登记在台账里；
//   - 另有一个「Timeout 恒为 0」的子形态（`Timeout: 0`）⇒ 直接算违规
//     （零超时的 Dialer = 没有任何拨号上界）。
func s951DialerHasTimeout(cl *ast.CompositeLit) (has bool, zero bool) {
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Timeout" {
			continue
		}
		if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.INT {
			if v, err := strconv.Atoi(lit.Value); err == nil && v == 0 {
				return true, true
			}
		}
		return true, false
	}
	return false, false
}

// s951ScanAll 扫**全部**受检文件。
func s951ScanAll(t *testing.T, root string) s951ScanResult {
	t.Helper()
	res := s951ScanResult{}
	for _, rel := range trackedFiles(t, root) {
		if !strings.HasPrefix(rel, "backend/") || !strings.HasSuffix(rel, ".go") {
			continue
		}
		if strings.Contains(rel, "/testdata/") {
			continue
		}
		if strings.HasPrefix(rel, "backend/third_party/") {
			continue
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		path := root + "/" + rel
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", rel, err)
		}
		sub := s951ScanFile(fset, f, rel)
		res.ScannedFiles++
		res.Dialers = append(res.Dialers, sub.Dialers...)
		res.DialersNoTimeout = append(res.DialersNoTimeout, sub.DialersNoTimeout...)
		res.SMTPClients = append(res.SMTPClients, sub.SMTPClients...)
		res.SMTPClientsNoDeadline = append(res.SMTPClientsNoDeadline, sub.SMTPClientsNoDeadline...)
		res.BannedDialForms = append(res.BannedDialForms, sub.BannedDialForms...)
		res.DotImports = append(res.DotImports, sub.DotImports...)
	}
	s951SortHits(res.Dialers)
	s951SortHits(res.DialersNoTimeout)
	s951SortHits(res.SMTPClients)
	s951SortHits(res.SMTPClientsNoDeadline)
	s951SortHits(res.BannedDialForms)
	s951SortHits(res.DotImports)
	return res
}

// s951ScanFile 在**已解析的 AST** 上找 §951 的各种形态。
//
// ⚠ 单独抽出来是为了让「自证臂」能拿**合成源码**喂进来（见 TestS951ScannerHasTeeth）。
func s951ScanFile(fset *token.FileSet, f *ast.File, rel string) s951ScanResult {
	res := s951ScanResult{}
	netLocal, _ := s951ImportLocalName(f, "net")
	smtpLocal, _ := s951ImportLocalName(f, "net/smtp")
	tlsLocal, _ := s951ImportLocalName(f, "crypto/tls")
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if imp.Name != nil && imp.Name.Name == "." &&
			(p == "net" || p == "net/smtp" || p == "crypto/tls") {
			res.DotImports = append(res.DotImports, s951Hit{File: rel, Line: fset.Position(imp.Pos()).Line, Kind: "点导入 " + p})
		}
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		s951ScanFuncBody(fset, fn, rel, netLocal, smtpLocal, tlsLocal, &res)
	}
	return res
}

// s951ScanFuncBody 扫一个具名函数的函数体。
//
// ⚠ 匿名闭包（FuncLit）里的命中**归属到外层具名函数**
// ⇒ 键是「文件#具名函数」⇒ 闭包挪了位置也不影响台账对账。
func s951ScanFuncBody(fset *token.FileSet, fn *ast.FuncDecl, rel string, netLocal, smtpLocal, tlsLocal string, res *s951ScanResult) {
	funcName := fn.Name.Name
	hasSetDeadline := false
	var newClients []s951Hit

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.CallExpr:
			// ③ 的一半：找 SetDeadline( 与 smtp.NewClient(
			if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetDeadline" {
				hasSetDeadline = true
			}
			if s951SelectorIs(e.Fun, smtpLocal, "NewClient") {
				newClients = append(newClients, s951Hit{
					File: rel, Line: fset.Position(e.Pos()).Line, Func: funcName, Kind: "smtp.NewClient",
				})
			}
			// ① 被禁的拨号形态
			for _, banned := range []struct{ local, name string }{
				{smtpLocal, "Dial"},
				{tlsLocal, "Dial"},
				{netLocal, "Dial"},
			} {
				if s951SelectorIs(e.Fun, banned.local, banned.name) {
					res.BannedDialForms = append(res.BannedDialForms, s951Hit{
						File: rel, Line: fset.Position(e.Pos()).Line, Func: funcName,
						Kind: banned.local + "." + banned.name + "（无拨号超时的 stdlib 口子）",
					})
				}
			}
		case *ast.CompositeLit:
			// ② net.Dialer 字面量
			if s951SelectorIs(e.Type, netLocal, "Dialer") {
				hit := s951Hit{File: rel, Line: fset.Position(e.Pos()).Line, Func: funcName, Kind: "net.Dialer{}"}
				res.Dialers = append(res.Dialers, hit)
				has, zero := s951DialerHasTimeout(e)
				switch {
				case zero:
					res.DialersNoTimeout = append(res.DialersNoTimeout, s951Hit{
						File: rel, Line: fset.Position(e.Pos()).Line, Func: funcName,
						Kind: "net.Dialer{Timeout: 0}（零超时 = 没有拨号上界）",
					})
				case !has:
					res.DialersNoTimeout = append(res.DialersNoTimeout, hit)
				}
			}
		}
		return true
	})

	for _, hit := range newClients {
		res.SMTPClients = append(res.SMTPClients, hit)
		if !hasSetDeadline {
			res.SMTPClientsNoDeadline = append(res.SMTPClientsNoDeadline, s951Hit{
				File: hit.File, Line: hit.Line, Func: hit.Func,
				Kind: "smtp.NewClient 所在函数没有 SetDeadline( ⇒ SMTP 会话阶段无上界",
			})
		}
	}
}

// s951SortHits 排序（保证报错输出稳定）。
func s951SortHits(hits []s951Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].Kind < hits[j].Kind
	})
}

// s951KeyOf 是台账的键：文件#函数。
func s951KeyOf(h s951Hit) string { return h.File + "#" + h.Func }

// TestS951NoBannedDialForms 是断言 ①：三个无拨号超时的 stdlib 口子一律不许。
func TestS951NoBannedDialForms(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)
	if len(res.BannedDialForms) == 0 {
		return
	}
	for _, h := range res.BannedDialForms {
		t.Errorf("%s:%d（%s）有一处 `%s`\n"+
			"⇒ 这三个是 stdlib 提供的**无拨号超时**的口子（内部就是 `net.Dial`）\n"+
			"⇒ ⇒⇒ 一个被黑洞掉的 SYN 就能把建连**永久**钉住\n"+
			"⇒ ⇒⇒ 而且 net/smtp 没有 ctx 支持 ⇒ 之后每一个协议阶段也会跟着挂死\n"+
			"⇒ 改法：自己拨号（`net.Dialer{Timeout: …}`）+ `conn.SetDeadline(绝对上界)`。",
			h.File, h.Line, h.Func, h.Kind)
	}
}

// TestS951NoDotImport 是断言 ① 的前置：点导入会让本扫描器整族失明 ⇒ 直接判红。
func TestS951NoDotImport(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)
	for _, h := range res.DotImports {
		t.Errorf("%s:%d 有一处 %s\n"+
			"⇒ 点导入会让本卡口的扫描器对**整族**形态失明（它认的是「本地名.成员」）\n"+
			"⇒ ⇒⇒ 卡口会安静地少看一整个包 ⇒ 必须判红", h.File, h.Line, h.Kind)
	}
}

// TestS951EveryDialerHasTimeout 是断言 ②：每个 `net.Dialer{…}` 都要有 Timeout。
func TestS951EveryDialerHasTimeout(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)
	registered := map[string]bool{}
	for _, r := range s951NoDialTimeoutLedger {
		registered[r.Key] = true
	}
	for _, h := range res.DialersNoTimeout {
		key := s951KeyOf(h)
		if registered[key] {
			continue
		}
		t.Errorf("%s:%d（%s）有一处 `%s`\n"+
			"⇒ `net.Dialer.Timeout` 是**唯一**能给建连加上界的字段\n"+
			"⇒ ⇒⇒ 没有它 ⇒ 一个不响应的对端能把建连永久钉住\n"+
			"⇒ ⇒⇒ 若上界由别处提供（如 ctx + 客户端 Timeout），请登记进 s951NoDialTimeoutLedger。",
			h.File, h.Line, h.Func, h.Kind)
	}
}

// TestS951DialerExceptionLedger 是断言 ② 的台账双向相等 + 绝对条数。
func TestS951DialerExceptionLedger(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)

	found := map[string]bool{}
	for _, h := range res.DialersNoTimeout {
		found[s951KeyOf(h)] = true
	}
	registered := map[string]bool{}
	for _, r := range s951NoDialTimeoutLedger {
		if registered[r.Key] {
			t.Errorf("台账里有重复条目：%s", r.Key)
		}
		registered[r.Key] = true
		if strings.TrimSpace(r.Why) == "" {
			t.Errorf("台账条目 %s 没有写「上界由谁提供」", r.Key)
		}
	}
	for key := range found {
		if !registered[key] {
			t.Errorf("源码里有一处无 Timeout 的拨号（%s），但它**不在**台账里\n"+
				"⇒ 请登记进 s951NoDialTimeoutLedger 并写清上界来源", key)
		}
	}
	for key := range registered {
		if !found[key] {
			t.Errorf("台账里登记的 %s 在源码里**已经没有了**\n"+
				"⇒ 请删掉这条登记（台账只许变少）", key)
		}
	}
	if got, want := len(s951NoDialTimeoutLedger), 1; got != want {
		t.Errorf("台账条数 = %d（期望 %d）\n"+
			"⇒ 无拨号上界的例外点数量变了 ⇒ 扫描口径变了，或者有人增删了登记",
			got, want)
	}
}

// TestS951EverySMTPClientHasDeadline 是断言 ③：
// 每个 `smtp.NewClient(` 所在函数必须有一个 `SetDeadline(`。
//
// ⚠ 为什么这一条不能省：`net.Dialer.Timeout` **只管建连**；
// 建连之后的 banner / EHLO / STARTTLS / AUTH / MAIL / RCPT / DATA / QUIT
// 全都阻塞在**同一条连接**的读上 ⇒ 没有 socket deadline 就是**无上界等待**。
func TestS951EverySMTPClientHasDeadline(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)
	if len(res.SMTPClients) == 0 {
		t.Fatal("扫不到任何 smtp.NewClient( ⇒ 扫描口径塌缩了（不是「没有 SMTP」）")
	}
	for _, h := range res.SMTPClientsNoDeadline {
		t.Errorf("%s:%d（%s）%s\n"+
			"⇒ net/smtp 没有 ctx 支持 ⇒ 上界**只能**落在 socket deadline 上\n"+
			"⇒ ⇒⇒ 没有 SetDeadline ⇒ 一台「accept 了 TCP 之后就不说话」的服务器能把发送**永久**钉住\n"+
			"⇒ ⇒⇒ 改法：拨号后立刻 `conn.SetDeadline(绝对上界)`（一个 deadline 盖住每一个阶段，含 QUIT）。",
			h.File, h.Line, h.Func, h.Kind)
	}
}

// TestS951ScanScopeNotCollapsed 是范围下限哨兵。
func TestS951ScanScopeNotCollapsed(t *testing.T) {
	root := repoRoot(t)
	res := s951ScanAll(t, root)
	if res.ScannedFiles < s951MinScannedFiles {
		t.Fatalf("受检文件数 = %d，低于下界 %d\n"+
			"⇒ 扫描范围塌缩了（不是「后端变干净了」）", res.ScannedFiles, s951MinScannedFiles)
	}
	if len(res.Dialers) < 10 {
		t.Fatalf("扫到的 `net.Dialer{…}` 字面量只有 %d 个\n"+
			"⇒ 扫描口径塌缩了（真值是 14 个）", len(res.Dialers))
	}
	if len(res.SMTPClients) < 5 {
		t.Fatalf("扫到的 `smtp.NewClient(` 只有 %d 个\n"+
			"⇒ 扫描口径塌缩了（真值是 7 个）", len(res.SMTPClients))
	}
}

// TestS951ScannerHasTeeth 是**自证臂**：用合成源码证明扫描器真的能分辨。
//
// ⚠ 没有这一组，扫描器可能恒绿（"门绿有两种原因：判据无牙 / 变异没做到它声称的事"）。
func TestS951ScannerHasTeeth(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantNoTimeoutDialer 是期望被判为「无 Timeout 的拨号」的数量。
		wantNoTimeoutDialer int
		// wantNoDeadlineClient 是期望被判为「SMTP 客户端缺 deadline」的数量。
		wantNoDeadlineClient int
		// wantBanned 是期望被判为「禁用拨号形态」的数量。
		wantBanned int
	}{
		{
			name: "正控：Dialer 带 Timeout + 同函数有 SetDeadline ⇒ 都不该报",
			src: `package p
import ("net"; "net/smtp"; "time")
func f() error {
	d := &net.Dialer{Timeout: 5}
	conn, err := d.Dial("tcp", "h:1")
	if err != nil { return err }
	if err := conn.SetDeadline(time.Now()); err != nil { return err }
	_, err = smtp.NewClient(conn, "h")
	return err
}
`,
			wantNoTimeoutDialer:  0,
			wantNoDeadlineClient: 0,
			wantBanned:           0,
		},
		{
			name: "负控①：Dialer 不带 Timeout ⇒ 必须被抓",
			src: `package p
import "net"
func f() {
	d := &net.Dialer{}
	_ = d
}
`,
			wantNoTimeoutDialer: 1,
		},
		{
			name: "负控②：Timeout 恒为 0 ⇒ 必须被抓（零超时 = 没有上界）",
			src: `package p
import "net"
func f() {
	d := &net.Dialer{Timeout: 0}
	_ = d
}
`,
			wantNoTimeoutDialer: 1,
		},
		{
			name: "负控③：smtp.NewClient 所在函数没有 SetDeadline ⇒ 必须被抓",
			src: `package p
import ("net"; "net/smtp")
func f() error {
	conn, err := net.Dial("tcp", "h:1")
	if err != nil { return err }
	_, err = smtp.NewClient(conn, "h")
	return err
}
`,
			wantNoDeadlineClient: 1,
			wantBanned:           1,
		},
		{
			name: "负控④：smtp.Dial / tls.Dial / 裸 net.Dial ⇒ 必须被抓",
			src: `package p
import ("net"; "net/smtp"; "crypto/tls")
func f() error {
	_, err := smtp.Dial("h:1")
	if err != nil { return err }
	_, err = tls.Dial("tcp", "h:1", nil)
	if err != nil { return err }
	_, err = net.Dial("tcp", "h:1")
	return err
}
`,
			wantBanned: 3,
		},
		{
			name: "负控⑤：注释里写 net.Dialer{Timeout} **不算**违规（★ grep 的老坑）",
			src: `package p
// 这里提到 net.Dialer{Timeout: 10s} 只是注释。
func f() {}
`,
			wantNoTimeoutDialer: 0,
		},
		{
			name: "正控②：Timeout 写在**下一行**照样认得（★ 逐行 grep 的老坑）",
			src: `package p
import "net"
func f() {
	d := &net.Dialer{
		Timeout:   10,
		KeepAlive: 30,
	}
	_ = d
}
`,
			wantNoTimeoutDialer: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "synthetic.go", tc.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("合成源码解析失败：%v", err)
			}
			res := s951ScanFile(fset, f, "synthetic.go")
			if got := len(res.DialersNoTimeout); got != tc.wantNoTimeoutDialer {
				t.Errorf("「无 Timeout 的拨号」判出 %d 个，期望 %d 个", got, tc.wantNoTimeoutDialer)
			}
			if got := len(res.SMTPClientsNoDeadline); got != tc.wantNoDeadlineClient {
				t.Errorf("「SMTP 客户端缺 deadline」判出 %d 个，期望 %d 个", got, tc.wantNoDeadlineClient)
			}
			if got := len(res.BannedDialForms); got != tc.wantBanned {
				t.Errorf("「禁用拨号形态」判出 %d 个，期望 %d 个", got, tc.wantBanned)
			}
		})
	}
}
