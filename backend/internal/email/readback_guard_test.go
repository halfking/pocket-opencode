package email

// readback_guard_test.go — 防「字段一直写、从不读回」这一类**静默**缺陷复发。
//
// ## 要防的是什么
//
// 某列在 INSERT/UPDATE 里一直有值，但**没有任何读取路径**把它 Scan 进来，
// 于是结构体字段恒为零值，下游某个守卫永不触发。代码不报错、测试不红、日志无痕。
// 2026-10-02 在这个包里抓到两个真实例：
//
//   - `Email.MessageID`：发票自愈的「真实 Message-ID 强确认/强否定」永不执行，
//     只剩 subject+from+同日的弱判据（真实同名发票的头部完全一样）。
//   - `Email.BodyPurged`：`summarizeBody` 的「正文已清空且禁止回源」永不触发。
//
// ## 判据（与第一版不同，第一版是错的）
//
// 第一版想按「字段名在非测试代码里是否被 Scan 或赋值」来判断，**做不了**：
// 实测诊断（本文件下方注释记了证据）显示包内 `&x.MessageID` 形式的 Scan 目标
// **一个都没有**——因为 `GetEmailByID` 那几处是先把列 Scan 进一个
// `sql.NullString` 局部变量（`&messageID`），**再**赋给结构体
// （`e.MessageID = messageID.String`）。字段名级的 AST 分析根本看不到这一跳；
// 而「只要被赋值过就算读过」的退路又会被 `fetcher.go:959`、`mime.go:438`
// 这类**内存里构造**的 Email 赋值满足，于是负控不转红、护栏形同虚设。
//
// 所以现在的判据**把范围锚到单个函数内**：
//
//	若某函数体里出现 `X.Field = ...`（把值写进结构体），
//	则同一个函数的源码里必须出现该字段对应的 DB 列名。
//
// 这样「从库读出来」和「赋值给字段」被强制发生在**同一个函数**里，
// 而函数体同时含 SQL 字面量，所以判据是可查的。
//
// ## 为什么只守这几个字段（不追求覆盖全部）
//
// 全字段扫描会误报：有些字段是纯入参载体、有些函数的 SQL 在 helper 里。
// 广撒网会产生一堆需要豁免的噪声，最后没人看。这里只守**已经出过事的**字段，
// 新增字段时顺手加一行即可。宁可少守，不要一个天天误报的护栏。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// readbackGuardedFields 受护栏管的字段 -> 对应的 DB 列名。
//
// 只收已经出过事的字段。键是结构体字段名（跨类型同名时共用一个键；
// 下面的判据按函数内出现与否判定，不区分接收者类型）。
// ## 扩守 ActionReason 时的一条实测（2026-10-02，别把它当成有承重的护栏）
//
// 负控实测：把本条目连同下面那条 Sync 豁免一起去掉，护栏**仍然全绿**。
// 也就是说 ActionReason 这一条目前是**惰性的**——包里唯一给 `.ActionReason`
// 赋值的函数是 fetcher 的 Sync，而它已被豁免（值来自规则引擎，与 DB 无关）。
//
// 它仍然值得留着，理由是：豁免一旦被误删，条目会立刻生效并报出 Sync。
// 但**不要**把它当成「AI 判定依据的读路径已被守住」的证据——那一层由
// classification_reason_persist_test.go 的端到端用例负责（它打真 PG，
// 真从 kxmemory 响应一路验到 emails.action_reason）。
var readbackGuardedFields = map[string]string{
	"MessageID":    "message_id",    // 发票自愈的强身份判据（§7co）
	"BodyPurged":   "body_purged",   // 摘要的「禁止回源」守卫（§7cq）
	"ActionReason": "action_reason", // AI 判定依据（§7ec：真库 122/122 为空）
}

// readbackFnExempt 允许「从**非 SQL** 来源赋值」的函数，必须写明理由。
// 与本包既有的 pgDSNGuardExempt 是同一条规矩：豁免必须写理由。
var readbackFnExempt = map[string]string{
	"ParseMIMEMessage": "Message-ID 是从 MIME 头解析出来的，与 DB 无关；" +
		"这个函数里本来就不该出现 message_id 列（护栏首版就是被它逼出这条豁免的）",
	"syncPOP3Fallback": "赋的是**合成** Message-ID（\"pop3-\"+sanitize(uidl)），" +
		"不是从库里读出来的。真实 Message-ID 在 POP3 侧根本不存在——" +
		"这正是 sameEmailMessage 里用 strings.HasPrefix(msgID, \"pop3-\") " +
		"把它排除在强确认之外的原因。护栏第二版（只看字符串字面量）才把它抓出来，添为豁免",
	"applyInlineRules": "赋的是**规则引擎的命中依据**（在内存里把 reasons 拼成的 " +
		"\"action: reason\" 串），与 DB 无关。action_reason 确实是 DB 列、由 " +
		"InsertEmail 写入，只是那条 SQL 不在本函数体内，本判据看不见。" +
		"合并前这段代码在 Sync 的函数体里，那时靠下面的 \"Sync\" 条目豁免；" +
		"取件映射被重构成 emailFromMessage + applyInlineRules 之后，赋值点搬到了这里，" +
		"豁免也必须跟着搬 —— **豁免跟的是赋值所在的那个函数，不是调用方**。" +
		"AI 分类那条路径（ClassifyUnclassified → SetClassificationWithReasonScoped）" +
		"才是 §7ec 修的断点，它有自己的端到端用例盯着",
	"Sync": "合并后 Sync 已不再直接给这三个受管字段赋值（取件映射搬进了 " +
		"emailFromMessage，ActionReason 的实际赋值在 applyInlineRules），" +
		"所以这条豁免已无对应赋值点。保留是为了将来有人在这里新赋一个受管字段时" +
		"能立刻看到说明，而不是凭空多一条无理由的静默豁免。真实理由见上面那条",
	"scanEmail": "q2（2026-10-02）新增的赋值点：ActionReason。**它是一个纯扫描函数**——" +
		"签名是 `func scanEmail(row interface{ Scan(...any) error })`，SQL 在**调用方**" +
		"（ListEmailsScoped）的字符串里，本函数体内不可能出现列名。" +
		"这不是「漏查列」，而是判据的固有盲区：凡是把 row.Scan 拆出去的函数都看不见 SQL。" +
		"该列真的被查了吗？由 store_action_reason_read_test.go 的端到端用例负责——" +
		"它打真 PG 塞一个非空理由，再断言列表与详情都读得到；负控（把 SELECT 里的" +
		"COALESCE(e.action_reason,'') 换成 ''）实测转红。" +
		"**不要**把这条豁免当作「读路径已被本护栏守住」的证据，那是另一个文件的职责。",
}

// TestGuard_FieldAssignmentIsBackedByItsColumn 结构护栏。
func TestGuard_FieldAssignmentIsBackedByItsColumn(t *testing.T) {
	fset := token.NewFileSet()
	type offender struct {
		file, fn, field, column string
		pos                     token.Position
	}
	var offenders []offender
	funcs := 0

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := name
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcs++
			// 函数体里出现过的 `X.Field =` 赋值目标，
			// 以及函数体里所有**字符串字面量**（SQL 就在其中）。
			//
			// 为什么只看字面量、而不是整个函数源码：注释里也会出现列名。
			// 我第一版用 strings.Contains(bodySrc, column)，结果自己写在
			// GetEmailByID 上方那段「message_id 必须在这里读出来」的注释
			// **满足了对 message_id 的检查** —— 负控把 SELECT 里的列删掉，
			// 护栏照样全绿。注释能满足任何源码扫描（pgisolation_guard_test.go
			// 的文件头就写着这条规矩，我还是在同一个包里又犯了一次）。
			// SQL 一定在字符串字面量里，所以只认字面量既精确又能免疫注释。
			assigned := map[string]token.Pos{}
			var literals strings.Builder
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range v.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						if _, seen := assigned[sel.Sel.Name]; !seen {
							assigned[sel.Sel.Name] = sel.Pos()
						}
					}
				case *ast.BasicLit:
					if v.Kind == token.STRING {
						literals.WriteString(v.Value)
						literals.WriteByte('\n')
					}
				}
				return true
			})
			sqlSrc := literals.String()

			for field, pos := range assigned {
				column, guarded := readbackGuardedFields[field]
				if !guarded {
					continue
				}
				if reason, ok := readbackFnExempt[fn.Name.Name]; ok && strings.TrimSpace(reason) != "" {
					continue
				}
				if !strings.Contains(sqlSrc, column) {
					offenders = append(offenders, offender{
						file: path, fn: fn.Name.Name, field: field, column: column,
						pos: fset.Position(pos),
					})
				}
			}
		}
	}

	if funcs == 0 {
		t.Fatal("checked == 0：护栏静默失效（一个函数都没遍历到）")
	}
	t.Logf("护栏遍历 %d 个函数、%d 个受管字段", funcs, len(readbackGuardedFields))

	if len(offenders) > 0 {
		var b strings.Builder
		b.WriteString("以下函数把结构体字段赋了值，却在同一函数里没有出现对应的 DB 列名——\n")
		b.WriteString("这通常意味着该字段是**恒零值**，下游拿它当守卫会永不触发：\n")
		for _, o := range offenders {
			b.WriteString("  ")
			b.WriteString(o.file)
			b.WriteString(":")
			b.WriteString(o.pos.String())
			b.WriteString("  ")
			b.WriteString(o.fn)
			b.WriteString("() 赋值 .")
			b.WriteString(o.field)
			b.WriteString(" 但函数内无 SQL 列 '")
			b.WriteString(o.column)
			b.WriteString("'\n")
		}
		t.Error(b.String())
	}
}
