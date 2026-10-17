package email

// store_select_scan_parity_test.go —— 静态护栏：包内每一条「读出一行
// Email」的路径，其 SELECT 投影列表与 Scan 目标列表必须**逐位对应**。
//
// ## 这条护栏补的是哪个洞
//
// store.go:2313 那段注释自己写着：
//
//	「Scan 的位置错位不会编译报错，只会让每封邮件的字段整体串位……
//	  **加列时两边必须同步改**」
//
// 也就是说，**投影列表与 Scan 目标列表是一对必须手工同步的表**，而这个不变式
// 当时只由注释守着。事实核对（2026-10-11 §423）：
//
//   - 同一对「表」在 email 包里存在**七份**，不是一个函数一对，而是七条读路径各一对：
//     ListEmails / GetEmailByID / ListEmailsByDay / ListEmailsScoped /
//     GetEmailByIDScoped / ListEmailsByDayScoped（这六条在 store.go），
//     以及 ListEmailsSince（列集是 store_pipeline.go 的常量 pipelineEmailCols）。
//   - 既有护栏 store_getbyid_columns_guard_test.go 只检查 GetEmailByID 与
//     GetEmailByIDScoped **是否包含** message_id / body_purged 两个列名，
//     它是「集合包含」判据，不是「逐位对应」判据，也不看另外五条。
//   - 既有护栏 store_action_reason_read_test.go 是端到端打真 PG 的用例，
//     它需要活的 PostgreSQL；没设 POCKET_TEST_POSTGRES_DSN 时**整个文件被 skip**
//     （该护栏的注释同样点明了这一点）⇒ 无库环境里这条回归无声通过。
//
// 缺口是用它自己注释里描述的那个错误证明的（§423）：在 SELECT 里把相邻两列
// 对调，跑**全量** `go test ./...`，FAIL 行数 **0**。
//
// ## 为什么「同类型相邻文本列互换」才是真正危险的那一种
//
// store.go:2316 那句「不产生任何错误信号」**举例举反了**：它举的是
// HasAttachments（bool）拿到 action_reason（text）的值。查 pgx v5.10.0 源码
// （不是凭记忆）得到的准确读数是——**错位是单向危险的**：
//
//	文本列 → *bool            TextCodec.PlanScan 不接受 *bool ⇒ scanPlanFail ⇒ 必报错
//	bool 列 → *string         scanPlanString.Scan 就是 *p = string(src) ⇒ 得到 "t"/"f"，静默
//	bool 列 → *sql.NullString NullString.Scan 走 convertAssign ⇒ 同样静默
//	同类型文本列互换           值类型合法、行数合法 ⇒ 静默
//
// ⇒ 注释点名的那个例子恰好是**安全的那一侧**（它会炸），而它宣称这不会报错；
// 真正无信号的是它没提的两类。本护栏按列名逐位比对，对三类一视同仁。
//
// ## 判据为什么必须锚到 AST 的字符串字面量
//
// 与 store_getbyid_columns_guard_test.go 同源：SQL 查询串一定在 token.STRING
// 节点里，只收集字符串字面量天然免疫注释——而本文件恰恰是注释密度最高的
// 文件之一（注释里逐字写着 `action_reason`、`suggested_action` 这些列名，
// 用 `strings.Contains(源码, 列名)` 做判据会被注释满足）。
//
// 本护栏比它多走一步：`SELECT ` + 常量 + ` FROM ...` 这种拼接会被还原成
// 一条完整语句（常量从包级字符串常量表解析）。只收集字面量会把列集丢掉，
// 于是 ListEmailsSince 会被读成「投影 0 列」——护栏静默退出它要护的东西。
//
// ## 覆盖边界（明确不覆盖什么）
//
// 只覆盖**产出 Email 行**的七条读路径。store.go 里还有 Account、每日摘要、
// 休假回复、OAuth 凭据等同款「SELECT ↔ Scan」结构，本护栏**不覆盖**，
// 属已登记缺口，见设计文档 §423.5，不要因为这里绿了就以为那些也对齐了。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// paritySite 描述一处「SELECT 投影 ↔ Scan 目标」必须逐位对应的读路径。
type paritySite struct {
	QueryFn string // 构造 SELECT 的函数
	ScanFn  string // 持有 Scan 调用的函数；空串表示与 QueryFn 同一个
	// FullRow=true 表示这条路径读的是「Email 整行」，参与 emailRowSharedColumns
	// 的交叉检查。显式最小列集的路径（如流水线扫描）设 false，理由写在该路径的
	// 常量注释里——不是「排除它」，是「它本来就不该有那些列」。
	FullRow bool
	// ExtraScanTargets 登记「不是 Email 字段」的 Scan 目标。真实存在的理由：
	// scanPipelineEmail 的第二个返回值 notifiedAt 对应 notified_at 列。
	// 登记了却没被用到必须判红（G173：登记了却无命中的条目必须删）。
	ExtraScanTargets map[string]string
	Note             string // 报错时给出的定位信息
}

// selectScanParitySites 是七条「读出一行 Email」的路径。全部登记，不是抽样：
// 少写一条，那条路径就静默退出护栏（护栏最坏的失败方式是不知道自己在护什么）。
//
// ⚠ 加新的 Email 读路径时**必须**在这里加一行，否则新路径不受任何列序保护。
var selectScanParitySites = []paritySite{
	{
		QueryFn: "ListEmails",
		FullRow: true,
		Note:    "无调用点的遗留读路径；它照样要保持两侧同步，否则一旦被接线就是每行必错",
	},
	{
		QueryFn: "GetEmailByID",
		FullRow: true,
		Note: "被 intent_executor / invoice_harvest / server_email_invoice 使用，" +
			"uid+message_id+body_purged 三列是发票自愈与 summarizeBody 守卫的前提",
	},
	{
		QueryFn: "ListEmailsByDay",
		FullRow: true,
		Note:    "无调用点的遗留读路径；与 ListEmailsByDayScoped 是同款两处历史（见 store.go:957）",
	},
	{
		QueryFn: "ListEmailsScoped",
		ScanFn:  "scanEmail",
		FullRow: true,
		Note: "Scan 被拆进独立的 scanEmail（签名是 interface{ Scan(...any) error }，" +
			"SQL 在调用方），所以两侧分属两个函数——这正是 readback_guard_test.go " +
			"对 scanEmail 记的那条豁免所说的「判据的固有盲区」，本护栏从另一侧补上",
	},
	{
		QueryFn: "GetEmailByIDScoped",
		FullRow: true,
		Note:    "详情页正文读取路径；比 GetEmailByID 多读 action_reason / body_path",
	},
	{
		QueryFn: "ListEmailsByDayScoped",
		FullRow: true,
		Note:    "scheduler.go 的每日摘要数据源，**在生产链路上**",
	},
	{
		QueryFn:          "ListEmailsSince",
		ScanFn:           "scanPipelineEmail",
		FullRow:          false,
		ExtraScanTargets: map[string]string{"notifiedAt": "notified_at"},
		Note: "pipelineEmailCols 是**独立手抄的第七份列集**，三个生产调用点" +
			"（垃圾清理 / 发票候选扫描 / 重要提醒）；列为「流水线扫描所需的最小列集」，" +
			"故不参与整行列的交叉检查，但列序必须逐位对齐",
	},
}

// minParityColumns 是扫描面下限：投影或 Scan 目标少于这个数，说明判据解析器
// 退化了（函数被改名、SELECT 被搬走、局部变量改名后无法解析），
// 而**不是**「代码变简单了」。低于它一律判红，不允许「删掉就通过」。
const minParityColumns = 8

// emailRowSharedColumns 是六条「整行读」路径**当前都有**的那些列。
// 它不是产品口径，是从现状取的事实交集，作用是堵住「两侧同时删掉同一列」
// 这个逐位对应判据看不见的洞：两侧一起错，对齐检查照样通过。
//
// 报红时的读法不是「不许删」，而是「另外 N 条路径还在读它，删之前先确认它们」。
var emailRowSharedColumns = []string{
	"id", "account_id", "from_address", "from_name", "subject", "snippet",
	"date", "is_read", "is_starred", "category", "importance",
	"ai_summary", "suggested_action", "has_attachments", "folder_name",
}

// TestStoreEmailSelectScanColumnParity 是主判据：七条读路径逐位比对。
func TestStoreEmailSelectScanColumnParity(t *testing.T) {
	fset, files, consts := loadEmailPackage(t)

	found := map[string]bool{}
	projectionsBySite := make([][]string, len(selectScanParitySites))

	for i, site := range selectScanParitySites {
		found[site.QueryFn] = true
		if site.ScanFn != "" {
			found[site.ScanFn] = true
		}

		queryFn := findFuncDecl(t, fset, files, site.QueryFn)
		projection, err := singleProjection(queryFn, consts)
		if err != nil {
			t.Errorf("%s: %v。%s", site.QueryFn, err, site.Note)
			continue
		}
		if len(projection) < minParityColumns {
			t.Errorf("%s: 解析出 %d 个投影列，少于扫描面下限 %d —— 判据解析器退化了。"+
				"这是「代码变简单了」还是「判据坏了」，必须先分清。%s",
				site.QueryFn, len(projection), minParityColumns, site.Note)
			continue
		}
		projectionsBySite[i] = projection

		scanFnName := site.ScanFn
		if scanFnName == "" {
			scanFnName = site.QueryFn
		}
		scanFn := findFuncDecl(t, fset, files, scanFnName)
		scan, err := scanRowTargets(scanFn, site.ExtraScanTargets)
		if err != nil {
			t.Errorf("%s（%s）: %v。%s", site.QueryFn, scanFnName, err, site.Note)
			continue
		}
		if len(scan) < minParityColumns {
			t.Errorf("%s: 解析出 %d 个 Scan 目标，少于扫描面下限 %d —— 判据解析器退化了。%s",
				site.QueryFn, len(scan), minParityColumns, site.Note)
			continue
		}

		if err := compareProjectionToScan(projection, scan); err != nil {
			t.Errorf("%s（%s）: %v。%s", site.QueryFn, scanFnName, err, site.Note)
		}
	}

	for _, site := range selectScanParitySites {
		if !found[site.QueryFn] {
			t.Errorf("护栏没匹配到目标函数 %s —— 函数被改名或移走了，护栏会静默放行。%s",
				site.QueryFn, site.Note)
		}
	}

	checkSharedColumnsPresent(t, projectionsBySite)
}

// TestSelectScanParityComparatorHasTeeth 是同形负控：拿判据自己的比对函数，
// 喂它一对**故意错位**的列表，必须报出位置与两侧列名。
//
// 没有这条，主判据就可能因为比对函数退化（永远返回 nil、或只比长度）
// 而全绿却毫无意义——「阴性对照报红时先怀疑对照本身」的同一件事，这里反过来：
// 对照必须先能红。
func TestSelectScanParityComparatorHasTeeth(t *testing.T) {
	aligned := []string{"id", "account_id", "from_address", "ai_summary", "suggested_action", "has_attachments"}
	if err := compareProjectionToScan(aligned, aligned); err != nil {
		t.Fatalf("同序列表被判为错位，比对函数坏了: %v", err)
	}

	// 形态一：同类型相邻文本列互换（真正静默的那一种）
	swapped := append([]string(nil), aligned...)
	swapped[3], swapped[4] = swapped[4], swapped[3]
	err := compareProjectionToScan(aligned, swapped)
	if err == nil {
		t.Fatal("相邻文本列 ai_summary ↔ suggested_action 对调后仍判为对齐 —— 比对函数没有牙")
	}
	if !strings.Contains(err.Error(), "ai_summary") || !strings.Contains(err.Error(), "suggested_action") {
		t.Errorf("报错没有指名道姓，两侧列名必须都出现在消息里。得到: %v", err)
	}

	// 形态二：长度不等（一侧加了列另一侧没加）
	if err := compareProjectionToScan(aligned, aligned[:len(aligned)-1]); err == nil {
		t.Fatal("列数不等仍判为对齐 —— 比对函数只比了内容没比长度（或反之）")
	}

	// 形态三：跨类型错位（store.go 注释举错的那一种，本护栏同样要抓住）
	crossType := append([]string(nil), aligned...)
	crossType[3], crossType[5] = crossType[5], crossType[3]
	if err := compareProjectionToScan(aligned, crossType); err == nil {
		t.Fatal("文本列 ↔ bool 列错位后仍判为对齐")
	}
}

// TestSelectScanParityResolvesConstConcatenation 单独锁住「SELECT + 常量 + FROM」
// 的还原能力。ListEmailsSince 的列集是包级常量，只收集字面量的解析器会把它
// 读成「投影 0 列」——而 0 列正好会被「恰好一处 SELECT」的检查放行之后
// 再被扫描面下限抓住，但读数会是「解析器坏了」而不是「列集变了」。
// 这条用例把「常量拼接能还原」本身变成可断言的事实。
func TestSelectScanParityResolvesConstConcatenation(t *testing.T) {
	fset, files, consts := loadEmailPackage(t)
	cols, ok := consts["pipelineEmailCols"]
	if !ok {
		t.Fatal("找不到常量 pipelineEmailCols —— 它被改名或搬走了，" +
			"ListEmailsSince 的列集解析会静默退化成「投影 0 列」")
	}
	// 走一遍真实形状：`SELECT ` + 常量 + ` FROM emails`
	sql := "SELECT " + cols + " FROM emails"
	proj, err := extractSelectProjections(sql)
	if err != nil {
		t.Fatalf("常量拼接的列集解析失败: %v", err)
	}
	if len(proj) != 1 {
		t.Fatalf("应恰好解析出 1 份投影，实际 %d 份", len(proj))
	}
	if len(proj[0]) < minParityColumns {
		t.Fatalf("pipelineEmailCols 只解析出 %d 列: %v", len(proj[0]), proj[0])
	}
	if got := proj[0][len(proj[0])-1]; got != "notified_at" {
		t.Errorf("pipelineEmailCols 末列应为 notified_at，实际 %q", got)
	}
	if got := proj[0][0]; got != "id" {
		t.Errorf("pipelineEmailCols 首列应为 id，实际 %q", got)
	}
	// 反向：只给列集、不给 SELECT…FROM 包裹，必须**失败**而不是返回空列表。
	// 「空投影」会让扫描面下限报成「判据坏了」，掩盖真正的原因。
	if _, err := extractSelectProjections(cols); err == nil {
		t.Error("裸列集（没有 SELECT…FROM）被解析成功 —— 这会让列集解析静默退化成空投影")
	}
	_ = fset
	_ = files
}

// TestSelectScanParityHelpersOnRealScanShape 用真实的 scanEmail 局部变量写法
// 验证「DB → 局部变量 → 结构体字段」这一跳能被解析，而不是靠裸名匹配。
// store_getbyid_columns_guard_test.go 的注释记录过：前两版就是找
// `&x.MessageID` 而一个都命中不了，因为真实代码经过局部变量中转。
func TestSelectScanParityHelpersOnRealScanShape(t *testing.T) {
	fset, files, _ := loadEmailPackage(t)
	scanFn := findFuncDecl(t, fset, files, "scanEmail")
	scan, err := scanRowTargets(scanFn, nil)
	if err != nil {
		t.Fatalf("scanEmail: %v", err)
	}
	if len(scan) < minParityColumns {
		t.Fatalf("scanEmail 只解析出 %d 个目标，判据退化了", len(scan))
	}
	// 这些列只有靠局部变量中转才拿得到，证明解析走的是 e.Field = local 这条链
	for _, want := range []string{"from_name", "subject", "snippet", "action_reason", "folder_name"} {
		if !containsString(scan, want) {
			t.Errorf("scanEmail 的 Scan 目标里解析不出列 %q —— 局部变量中转没被正确还原。得到: %v",
				want, scan)
		}
	}
	if scan[0] != "id" {
		t.Errorf("scanEmail 第一个 Scan 目标应解析为 id，实际 %q —— 目标顺序没保住", scan[0])
	}
}

// checkSharedColumnsPresent 报出「多条整行路径都该有、某条却没有」的列。
// 它防的是两侧同时删列——那种错，逐位对应判据看不见。
func checkSharedColumnsPresent(t *testing.T, projections [][]string) {
	t.Helper()
	for i, site := range selectScanParitySites {
		if !site.FullRow || projections[i] == nil {
			continue // 解析失败/最小列集路径已在主判据或 Note 里说明
		}
		present := make(map[string]bool, len(projections[i]))
		for _, c := range projections[i] {
			present[c] = true
		}
		for _, col := range emailRowSharedColumns {
			if present[col] {
				continue
			}
			var others []string
			for j := range selectScanParitySites {
				if j == i || !selectScanParitySites[j].FullRow || projections[j] == nil {
					continue
				}
				if containsString(projections[j], col) {
					others = append(others, selectScanParitySites[j].QueryFn)
				}
			}
			if len(others) == 0 {
				continue // 大家都读了，那是口径变更，不是本判据该拦的
			}
			t.Errorf("%s: 少了列 %q，而另外 %d 条路径仍在读它（%s）。"+
				"逐位对应判据看不见「两侧同时删同一列」，这条补上它。",
				site.QueryFn, col, len(others), strings.Join(others, ", "))
		}
	}
}

// compareProjectionToScan 是判据的核心：投影列表与 Scan 目标列表必须逐位相等。
// 返回的错误必须指名道姓——报「第 4 列不一致」而不说是哪两列，等于没报。
func compareProjectionToScan(projection, scan []string) error {
	if len(projection) != len(scan) {
		return fmt.Errorf("投影 %d 列、Scan %d 个目标，数量不等。"+
			"这正是 store.go:2314 注释说的「加列时两边必须同步改」被违反；"+
			"pgx 会在第一行报 expected %d destination arguments（**会**大声报错），"+
			"但两侧同时改错则无任何信号",
			len(projection), len(scan), len(scan))
	}
	for i := range projection {
		if projection[i] != scan[i] {
			return fmt.Errorf("第 %d 位错位：SELECT 第 %d 列是 %q，Scan 第 %d 个目标读到的是 %q。"+
				"列数相等 ⇒ 这不是少查一列，而是每行的这两个字段整体对调。"+
				"是否报错取决于方向（实测 pgx v5.10.0）：文本列→*bool 会报 scanPlanFail，"+
				"而 bool 列→*string/*sql.NullString 会静默变成 \"t\"/\"f\"；"+
				"同类型文本列互换则完全无信号",
				i+1, i+1, projection[i], i+1, scan[i])
		}
	}
	return nil
}

// singleProjection 从函数体里取出唯一一份 SELECT 投影列表。
// 候选来源有两条：(a) 含 select 的字符串字面量；(b) 能被完整求值的字符串拼接
// （`SELECT ` + 常量 + ` FROM ...`）。两条都试，因为同一函数里两种写法混用。
func singleProjection(fn *ast.FuncDecl, consts map[string]string) ([]string, error) {
	var seen [][]string
	add := func(cols []string) {
		key := strings.Join(cols, "\x00")
		for _, s := range seen {
			if strings.Join(s, "\x00") == key {
				return
			}
		}
		seen = append(seen, cols)
	}

	// (a) 字面量：只取自身就是完整 SELECT … FROM 的，避免把拼接片段算进来
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if proj, ok := oneProjection(s); ok {
			add(proj)
		}
		return true
	})
	// (b) 可求值的拼接
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || bin.Op != token.ADD {
			return true
		}
		s, ok := evalStringExpr(bin, consts)
		if !ok {
			return true
		}
		if proj, ok := oneProjection(s); ok {
			add(proj)
		}
		return false
	})

	switch len(seen) {
	case 0:
		return nil, fmt.Errorf("在函数体里找不到一条完整的 SELECT … FROM 语句" +
			"（字面量与可求值拼接都试过了）。判据无法定位投影列表；静默放行是最坏的失败方式")
	case 1:
		return seen[0], nil
	default:
		return nil, fmt.Errorf("在函数体里找到 %d 份互不相同的 SELECT 投影列表"+
			"（%v），判据无法确定哪一份与 Scan 对应", len(seen), seen)
	}
}

func oneProjection(sqlText string) ([]string, bool) {
	proj, err := extractSelectProjections(sqlText)
	if err != nil || len(proj) != 1 || len(proj[0]) == 0 {
		return nil, false
	}
	return proj[0], true
}

// scanRowTargets 解析一个函数里那一处「读整行」的 Scan 调用，返回按位置排列的
// 列名。找不到、或者找到多处，都必须显式报错而不是猜一处。
// extras 登记「不是 Email 字段」的 Scan 目标；登记了却没被用到必须判红。
func scanRowTargets(fn *ast.FuncDecl, extras map[string]string) ([]string, error) {
	var calls []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Scan" {
			return true
		}
		if len(call.Args) >= minParityColumns {
			calls = append(calls, call)
		}
		return true
	})
	switch len(calls) {
	case 0:
		return nil, fmt.Errorf("函数体内没有参数数 ≥ %d 的 Scan 调用", minParityColumns)
	case 1:
	default:
		return nil, fmt.Errorf("函数体内有 %d 处参数数 ≥ %d 的 Scan 调用，判据无法判定哪一处与 SELECT 对应",
			len(calls), minParityColumns)
	}

	// 先把「局部变量 → e.Field」还原表建出来：DB → 局部变量 → 结构体字段这一跳
	// 必须走真实赋值语句，不能靠裸名匹配（裸名匹配在有 import 的文件上恒真，
	// 也会让一次改名就误判）。
	localToField := map[string]string{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		lhs, lok := as.Lhs[0].(*ast.SelectorExpr)
		if !lok {
			return true
		}
		recv, rok := lhs.X.(*ast.Ident)
		if !rok || recv.Name != "e" {
			return true
		}
		// 右值有三种写法，都要认：`e.X = local`、`e.X = local.String`、
		// `e.X = *local`（可空列走 *string 中转，见 scanPipelineEmail）。
		switch rhs := as.Rhs[0].(type) {
		case *ast.Ident:
			localToField[rhs.Name] = lhs.Sel.Name
		case *ast.SelectorExpr:
			if inner, ok := rhs.X.(*ast.Ident); ok {
				localToField[inner.Name] = lhs.Sel.Name
			}
		case *ast.StarExpr:
			if inner, ok := rhs.X.(*ast.Ident); ok {
				localToField[inner.Name] = lhs.Sel.Name
			}
		}
		return true
	})

	used := map[string]bool{}
	out := make([]string, 0, len(calls[0].Args))
	for i, arg := range calls[0].Args {
		expr, ok := arg.(*ast.UnaryExpr)
		if !ok || expr.Op != token.AND {
			return nil, fmt.Errorf("第 %d 个 Scan 目标不是取地址（&x）", i+1)
		}
		switch target := expr.X.(type) {
		case *ast.SelectorExpr:
			recv, ok := target.X.(*ast.Ident)
			if !ok || recv.Name != "e" {
				return nil, fmt.Errorf("第 %d 个 Scan 目标不是 e.Field 形式: %#v", i+1, target)
			}
			out = append(out, snakeCaseColumn(target.Sel.Name))
		case *ast.Ident:
			if col, ok := extras[target.Name]; ok {
				used[target.Name] = true
				out = append(out, col)
				continue
			}
			field, ok := localToField[target.Name]
			if !ok {
				return nil, fmt.Errorf("第 %d 个 Scan 目标是局部变量 %q，"+
					"但本函数体内找不到 e.X = %s 的赋值，无法确定它读的是哪一列。"+
					"若它确实不是 Email 字段（例如函数的第二个返回值），"+
					"在 selectScanParitySites 的 ExtraScanTargets 里显式登记",
					i+1, target.Name, target.Name)
			}
			out = append(out, snakeCaseColumn(field))
		default:
			return nil, fmt.Errorf("第 %d 个 Scan 目标的形态不认识: %#v", i+1, arg)
		}
	}

	for name := range extras {
		if !used[name] {
			return nil, fmt.Errorf("ExtraScanTargets 登记了 %q，但它不再是本函数的 Scan 目标。"+
				"登记了却无命中的条目必须删掉（G173）", name)
		}
	}
	return out, nil
}

// extractSelectProjections 从（已剥注释的）SQL 文本里抽出所有 SELECT 投影列表。
// 括号感知的：COALESCE((SELECT COUNT(*) FROM ...), 0) 这类子查询里的
// SELECT / FROM 在深度 1，不能当成外层的 FROM 截断。
// 一条文本里若有多条 SELECT（例如 EXISTS 子查询），返回错误而不是猜。
func extractSelectProjections(sqlText string) ([][]string, error) {
	var out [][]string
	lower := strings.ToLower(sqlText)
	for i := 0; i < len(lower); {
		j := strings.Index(lower[i:], "select")
		if j < 0 {
			break
		}
		kwStart := i + j
		start := kwStart + len("select")
		i = start
		// 必须落在词边界上，避免把 selected_at 之类的标识符当关键字。
		// 注意两侧查的是**关键字之外**的字符：查 start-1 会拿到 't'（SELECT 的
		// 末字母），把每一处 SELECT 都判成「粘在标识符后面」。
		if start < len(lower) && isIdentByte(lower[start]) {
			continue
		}
		if kwStart > 0 && isIdentByte(lower[kwStart-1]) {
			continue
		}
		depth := 0
		end := -1
		for k := start; k < len(lower); k++ {
			switch lower[k] {
			case '(':
				depth++
			case ')':
				if depth > 0 {
					depth--
				}
			case '\'':
				for k++; k < len(lower); k++ {
					if lower[k] == '\'' {
						if k+1 < len(lower) && lower[k+1] == '\'' {
							k++
							continue
						}
						break
					}
				}
			case 'f':
				if depth != 0 || !strings.HasPrefix(lower[k:], "from") {
					continue
				}
				if isWordBoundaryAt(lower, k) {
					end = k
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("SELECT 之后没有顶层 FROM，无法确定投影列表的边界")
		}
		cols := splitTopLevelCommas(sqlText[start:end])
		for i := range cols {
			cols[i] = normalizeColumnRef(cols[i])
		}
		out = append(out, cols)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("文本里没有 SELECT 语句")
	}
	return out, nil
}

// splitTopLevelCommas 按括号深度 0 的逗号切分。空片段丢弃。
func splitTopLevelCommas(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '\'':
			for i++; i < len(s); i++ {
				if s[i] == '\'' {
					break
				}
			}
		case ',':
			if depth == 0 {
				if piece := strings.TrimSpace(s[start:i]); piece != "" {
					out = append(out, piece)
				}
				start = i + 1
			}
		}
	}
	if piece := strings.TrimSpace(s[start:]); piece != "" {
		out = append(out, piece)
	}
	return out
}

// normalizeColumnRef 把一个投影表达式归一成列名：
// `COALESCE(e.action_reason, '')` → action_reason；`e.id` → id；`e.id AS foo` → id。
func normalizeColumnRef(expr string) string {
	s := strings.TrimSpace(expr)
	if s == "" {
		return ""
	}
	if i := aliasSplit(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if inner, ok := unwrapFunc(s, "coalesce"); ok {
		args := splitTopLevelCommas(inner)
		if len(args) > 0 {
			return normalizeColumnRef(args[0])
		}
	}
	if i := strings.LastIndex(s, "."); i >= 0 && i < len(s)-1 && isIdentByte(s[i-1]) {
		// 点号左侧是合法标识符时才当表别名，否则 GREATEST(1.5, 2) 之类会被误切。
		s = s[i+1:]
	}
	s = strings.Trim(s, "`\"")
	return strings.ToLower(strings.TrimSpace(s))
}

// aliasSplit 找出投影别名 `X AS Y` 的切点；没有则返回 -1。
func aliasSplit(s string) int {
	lower := strings.ToLower(s)
	depth := 0
	for i := 0; i < len(lower)-3; i++ {
		switch s[i] {
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		case '\'':
			for i++; i < len(s); i++ {
				if s[i] == '\'' {
					break
				}
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if strings.HasPrefix(lower[i:], " as ") && (i == 0 || !isIdentByte(s[i-1])) {
			return i
		}
	}
	return -1
}

// unwrapFunc 判断 s 是否形如 fn(<inner>)，是则返回 inner。
func unwrapFunc(s, fn string) (string, bool) {
	if len(s) < len(fn)+2 {
		return "", false
	}
	if !strings.EqualFold(s[:len(fn)], fn) || s[len(fn)] != '(' || s[len(s)-1] != ')' {
		return "", false
	}
	return s[len(fn)+1 : len(s)-1], true
}

// snakeCaseColumn 把 Go 结构体字段名转成列名。处理首字母缩写与尾部缩写：
// HasAttachments→has_attachments、MessageID→message_id、UID→uid、
// AISummary→ai_summary（第二个大写字母跟着第三个大写字母时不插下划线）。
func snakeCaseColumn(field string) string {
	runes := []rune(field)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					b.WriteRune('_')
				} else if unicode.IsUpper(prev) && nextIsLower {
					b.WriteRune('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// evalStringExpr 求值一个纯字符串表达式：字面量、包级常量、括号、+ 拼接。
// 遇到函数调用（fmt.Sprintf 之类）就返回 false——宁可少一条候选，
// 也不要拼出半条 SQL 再当完整语句判。
func evalStringExpr(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.ParenExpr:
		return evalStringExpr(v.X, consts)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, ok1 := evalStringExpr(v.X, consts)
		r, ok2 := evalStringExpr(v.Y, consts)
		return l + r, ok1 && ok2
	}
	return "", false
}

// loadEmailPackage 解析本包全部**非测试** .go 文件，并抽出包级字符串常量表
// （`SELECT ` + 常量 + ` FROM ...` 的还原要用）。
func loadEmailPackage(t *testing.T) (*token.FileSet, map[string]*ast.File, map[string]string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve current file")
	}
	dir := filepath.Dir(thisFile)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go") && strings.HasSuffix(fi.Name(), ".go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 email 包: %v", err)
	}
	pkg, ok := pkgs["email"]
	if !ok {
		t.Fatalf("目录 %s 里没有 email 包", dir)
	}
	files := map[string]*ast.File{}
	for name, f := range pkg.Files {
		files[name] = f
	}

	consts := map[string]string{}
	names := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, decl := range pkg.Files[name].Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if s, err := strconv.Unquote(lit.Value); err == nil {
						consts[name.Name] = s
					}
				}
			}
		}
	}
	return fset, files, consts
}

func findFuncDecl(t *testing.T, fset *token.FileSet, files map[string]*ast.File, name string) *ast.FuncDecl {
	t.Helper()
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		for _, decl := range files[f].Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
				return fn
			}
		}
	}
	t.Fatalf("email 包里找不到函数 %s —— 护栏会静默放行。", name)
	return nil
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isWordBoundaryAt 判断 lower[k:] 处的四字母关键字是否落在词边界上。
func isWordBoundaryAt(lower string, k int) bool {
	if k > 0 && isIdentByte(lower[k-1]) {
		return false
	}
	end := k + 4
	return !(end < len(lower) && isIdentByte(lower[end]))
}
