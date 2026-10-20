package repohygiene

// §943 卡口：全仓「**裸 id 主键 + ON CONFLICT DO UPDATE**」的候选集棘轮。
//
// ⚠⚠ 为什么要这道卡口（本节查出来的**元问题**）：
//
//	`ON CONFLICT (id) DO UPDATE` 的冲突目标是**一列裸 id**（不是
//	(owner_id, workspace_id) 这样的复合键）⇒ ⇒ ⇒ **撞上别人的 id 就会
//	命中 DO UPDATE** ⇒ ⇒ ⇒ 它能不能越权，**完全取决于那条 DO UPDATE 有没有
//	归属守卫**（`WHERE <表>.owner_id = EXCLUDED.owner_id` 之类）。
//
//	⇒ ⇒ ⇒ 所以「哪些表用了裸 id + DO UPDATE」是一个**必须逐个定性**的清单，
//	而不是「grep 一下看起来有守卫就没事」。
//
//	⚠ §943 的教训（**而且是两次**）：
//	① **我自己的 grep 普查是错的**：它数出 6 处，而**按 AST 扫字面量**数出
//	   **12 处**（漏掉的是 `ON CONFLICT(id)` 无空格写法、跨字面量拼接的 SQL，
//	   以及 `INSERT INTO` 与 `ON CONFLICT` 不在同一个字面量里的情形）
//	   ⇒ ⇒⇒ ⇒ **「N 命中的第一嫌疑也是量具坏了」** —— 不只是 0 命中。
//	② 全仓 12 处里，当时**只有 1 处**（notes）带守卫；
//	   `meetings` 与 `meeting_tombstones`（**都在会议链上**）由本节补上；
//	   其余 9 处**不在会议/录音链上** ⇒ ⇒⇒ 本节**不代拍**，
//	   已在简报第 167 条 ② 登记为「待定性」。
//
// ⚠ 这道卡口量的是**候选集本身**（绝对条数 + 逐个身份），
//   **不是**「有没有守卫」——
//   ⇒ 因为守卫在本仓有**三种形态**，而**自动判「有没有 WHERE」会造假阴性**
//   （`task/store.go` 的守卫写在**写入前的 SELECT** 里 ⇒ SQL 里没有 WHERE
//   ⇒ 自动判会把它记成「没守卫」⇒ 台账被写歪 ⇒ 后来人以为那里有洞）。
//   ⇒ ⇒⇒⇒ **守卫的形态由人工定性（台账），自动判只做它能判的那一小部分**。
//   ⇒ 守卫「有没有真的生效」由各业务包的**行为**判据管
//   （§939 管 notes、§943 管 meetings 与 meeting_tombstones）。
//
// ⚠ 扫描方式：**只认 Go 源码里的字符串字面量**（用 go/parser 走 AST），
//   并按**文件**把字面量按源码顺序拼起来再找模式
//   ⇒ ⇒⇒ 因此 **① 注释里**出现的 `ON CONFLICT (id) DO UPDATE` 天然不算数
//   （★ 本仓 §917 记过：注释里的字样足以让一条门恒绿）
//   ⇒ ⇒⇒ **② 跨字面量拼接**的 SQL（`"INSERT INTO tasks (" + cols + ")" + values + "ON CONFLICT ..."`）
//   也能被完整看到 ⇒ ⇒⇒③ 不需要手写剥注释器（★ 手写剥注释器在 CRLF 文件上会静默失效）。
import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s943ConflictRE 匹配「裸 id + DO UPDATE」的冲突子句。
//
// ⚠ 允许 `ON CONFLICT(id)` 与 `ON CONFLICT (id)` 两种写法（仓里两种都有）。
var s943ConflictRE = regexp.MustCompile(`(?is)ON\s+CONFLICT\s*\(\s*id\s*\)`)

// s943DoUpdateRE 匹配 DO UPDATE。
var s943DoUpdateRE = regexp.MustCompile(`(?is)DO\s+UPDATE`)

// s943InsertRE 从同一条 SQL 里取目标表名。
var s943InsertRE = regexp.MustCompile(`(?is)INSERT\s+INTO\s+([A-Za-z_][A-Za-z0-9_]*)`)

// s943WhereRE 匹配 WHERE（用来判「这条 DO UPDATE 带不带 SQL 谓词守卫」）。
var s943WhereRE = regexp.MustCompile(`(?is)\bWHERE\b`)

// s943Guard 是「归属守卫」的**形态**（人工定性，不自动猜）。
//
// ⚠ 为什么是枚举而不是布尔：
//
//	「有 / 没有」会漏掉「守卫在别处」（task/store.go 的写入前 SELECT）
//	⇒ ⇒⇒ 登记表必须能表达**形态**，否则台账会把「有守卫」记成「没守卫」。
type s943Guard string

const (
	// s943GuardSQLWhere = 守卫写在 DO UPDATE 的 WHERE 谓词里。
	s943GuardSQLWhere s943Guard = "sql-where"
	// s943GuardPrecheck = 守卫写在**写入前的 SELECT** 作用域检查里。
	s943GuardPrecheck s943Guard = "precheck"
	// s943GuardNone = 没有守卫。
	s943GuardNone s943Guard = "none"
	// s943GuardUnqualified = ⛔ 未定性（待查）。
	s943GuardUnqualified s943Guard = "unqualified"
)

// s943Reach 是「撞号**可达性**」的口径（人工定性，§944 新增）。
//
// ⚠ 这一维回答的是「**id 能不能被外部指定**」：
//
//	它是 §944 收口 §943 那 9 处待定性项时的**关键**——
//	同样是「没有归属守卫」，**id 由客户端自选**（可达 ⇒ 必须修）
//	与**id 由服务端派生**（撞号要先知道对方的 id ⇒ 登记即可）是两件事。
type s943Reach string

const (
	// s943ReachClientChosen = id 由**客户端/请求体**提供 ⇒ 撞号可达。
	s943ReachClientChosen s943Reach = "client-chosen"
	// s943ReachServerDerived = id 由**服务端派生**（随机/哈希/验证器）
	// ⇒ 撞号需要先知道对方的 id。
	s943ReachServerDerived s943Reach = "server-derived"
	// s943ReachDead = 该写入路径在仓内**没有生产调用方**（死能力）⇒ 不可达。
	//
	// ⚠ 只认**仓内**的调用方；移动端在仓外 ⇒ 这一维**不等于**「线上没人用」
	//   ⇒ 但它足以说明「这条路径现在没人踩」。
	s943ReachDead s943Reach = "dead-capability"
)

// s943Fix 是「处置」的口径（人工定性，§944 新增）。
type s943Fix string

const (
	// s943FixFixed = 已补归属守卫（§939 / §943 / §944）。
	s943FixFixed s943Fix = "fixed"
	// s943FixQualified = 已定性为**不是洞**（写入前守卫 / id 服务端派生 /
	// 死能力）⇒ 登记，不代拍。
	s943FixQualified s943Fix = "qualified"
)

// s943Entry 是一处候选点的台账条目。
type s943Entry struct {
	// Table 是目标表名。
	Table string
	// Guard 是守卫形态（人工定性）。
	Guard s943Guard
	// Reach 是撞号可达性（人工定性，§944 新增）。
	Reach s943Reach
	// Fix 是处置口径（人工定性，§944 新增）。
	Fix s943Fix
	// Note 是定性的理由（一行）。
	Note string
}

// s943Registry 是候选集的**台账**（定性 + 绝对条数）。
//
// ⚠ 条数是**绝对值**（12）⇒ 任何增删都必须回来改这张表 ⇒ ⇒⇒⇒ 「新增一处」
// 必然触发一次重新定性。
//
// ⚠⚠ §944：这张表现在**把 §943 留下的 9 处待定性项全部收口**了
// （两维新口径：Reach = 撞号可达性、Fix = 处置），并因此多出一条
// **机器可判**的不变量：
//
//	「**id 由客户端自选** ⇒ 必须有归属守卫（sql-where 或 precheck）」
//
// ⇒ ⇒⇒ 将来有人新写一处「客户端自选 id + 裸 id DO UPDATE」⇒ 这道门立刻红
// ⇒ ⇒⇒⇒ 而不是因为「忘了登记」而溜过去。
//
// ⚠ 已修（带守卫）的那几处：
//
//	notes                §939 补的 `WHERE notes.user_id = $2`（QueryRow 读得到拒绝）
//	meetings             §943 补的 owner + workspace 守卫（同上）
//	meeting_tombstones   §943 补的 owner + workspace 守卫（同上）
//	biometric_credentials §944 D1 补的 user_id 守卫（后果最重：认证链接管）
//	asset_mirrors         §944 D2 补的 workspace_id 守卫（密文被搬走 + 覆盖）
//	devices               §944 D3 补的 user_id 守卫（设备行被搬走 + push_token 被换）
//	notification_rules    §944 D4 补的 workspace_id 守卫（通知规则被改写）
var s943Registry = map[string]s943Entry{
	"backend/internal/auth/biometric.go|biometric_credentials": {
		Table: "biometric_credentials", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§944 D1 补的 user_id 守卫（QueryRow 读得到拒绝）⇒ 撞 id 不再覆盖别人的**认证公钥**" +
			"（不覆盖 ⇒ 登录链按 id 取行时验的仍是原主人的公钥 ⇒ 不能顶替身份）；" +
			"id 来源：P0 stub 路径**完全由请求体给**、WebAuthn 路径由 attestation 派生",
	},
	"backend/internal/email/store.go|email_vacation_replies": {
		Table: "email_vacation_replies", Guard: s943GuardPrecheck,
		Reach: s943ReachServerDerived, Fix: s943FixQualified,
		Note: "守卫在**写入前的 SELECT**（校验已存在那行绑的 account 在 scope 内 ⇒ 不成立即 ErrNotFound）" +
			"⇒ SQL 里没有 WHERE；id 由服务端生成（randomID(\"vac\")）",
	},
	"backend/internal/email/store.go|emails": {
		Table: "emails", Guard: s943GuardNone,
		Reach: s943ReachServerDerived, Fix: s943FixQualified,
		Note: "id 服务端派生（em-pop3-<account>-<uidl>）⇒ 撞号要先知道对方 account + uidl；" +
			"SET 不含归属列 ⇒ 后果限于 snippet/importance/action_reason",
	},
	"backend/internal/identity/store.go|devices": {
		Table: "devices", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§944 D3 补的 user_id 守卫（QueryRow 读得到拒绝）⇒ 撞 id 不再把别人的设备行搬走；" +
			"★ 守卫**只**断言 user_id（不断言 workspace_id）⇒「设备跟随主人换租户」的既有语义不被误伤",
	},
	"backend/internal/lobster/store.go|asset_mirrors": {
		Table: "asset_mirrors", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§944 D2 补的 workspace_id 守卫（QueryRow 读得到拒绝）⇒ 撞 id 不再把别人的 cipher_blob " +
			"搬进自己的租户并覆盖；id 由请求体 pushes[].id 提供",
	},
	"backend/internal/meeting/pg_store.go|meeting_tombstones": {
		Table: "meeting_tombstones", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§943 D3 补的 owner + workspace 守卫（QueryRow 读得到拒绝）⇒ 不再搬走别人的墓碑",
	},
	"backend/internal/meeting/pg_store.go|meetings": {
		Table: "meetings", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§943 D1 补的 owner + workspace 守卫（QueryRow 读得到拒绝）⇒ 撞号换号，绝不覆盖",
	},
	"backend/internal/notes/store.go|notes": {
		Table: "notes", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§939 补的 `WHERE notes.user_id = $2`（QueryRow 读得到拒绝）",
	},
	"backend/internal/notifycenter/service.go|notification_rules": {
		Table: "notification_rules", Guard: s943GuardSQLWhere,
		Reach: s943ReachClientChosen, Fix: s943FixFixed,
		Note: "§944 D4 补的 workspace_id 守卫（QueryRow 读得到拒绝）⇒ 撞 id 不再改写别人的通知规则；" +
			"id 由请求体提供",
	},
	"backend/internal/opencode/store.go|opencode_sessions": {
		Table: "opencode_sessions", Guard: s943GuardNone,
		Reach: s943ReachDead, Fix: s943FixQualified,
		Note: "SaveSession 在仓内**没有生产调用方**（只有测试引用）⇒ 死能力；" +
			"且本表**没有归属列** ⇒ 将来接线时必须把归属一起设计" +
			"（⚠ 移动端在仓外 ⇒ 这一维不等于「线上没人用」）",
	},
	"backend/internal/scheduledtask/store.go|scheduled_task_tombstones": {
		Table: "scheduled_task_tombstones", Guard: s943GuardPrecheck,
		Reach: s943ReachServerDerived, Fix: s943FixQualified,
		Note: "写墓碑**之前**那句 DELETE 已按 user_id + workspace_id 限定（RowsAffected==0 即 ErrTaskNotFound " +
			"⇒ 走不到插入）⇒ 撞不到别人的行；SQL 里没有 WHERE",
	},
	"backend/internal/task/store.go|tasks": {
		Table: "tasks", Guard: s943GuardPrecheck,
		Reach: s943ReachClientChosen, Fix: s943FixQualified,
		Note: "守卫写在**写入前的 SELECT**（跨 workspace 同 ID ⇒ 拒绝写入并保留原行）⇒ SQL 里没有 WHERE",
	},
}

// s943Candidate 是一处「裸 id + ON CONFLICT DO UPDATE」。
type s943Candidate struct {
	// Key 是台账的键：<相对路径>|<表名>。
	Key string
	// File 是相对仓库根的路径。
	File string
	// Table 是目标表名。
	Table string
	// HasWhereClause 表示这条 DO UPDATE **后面带 WHERE**。
	//
	// ⚠ 它**只**说明「SQL 谓词守卫在不在这个位置」，
	//   **不**等于「这一处有没有守卫」（守卫可能是写入前的 SELECT）
	//   ⇒ ⇒⇒ 台账里那一维是**人工定性**的，见 s943Guard。
	HasWhereClause bool
}

// s943ScanGo 扫一个 Go 文件，返回它里面的候选点。
//
// ⚠ 只看**字符串字面量**（AST）⇒ 注释天然被排除。
// ⚠ 字面量按**源码顺序**拼起来再找模式 ⇒ 跨字面量拼接的 SQL 也能被看到。
func s943ScanGo(t *testing.T, root, rel string) []s943Candidate {
	t.Helper()
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, root+"/"+rel, nil, 0)
	if err != nil {
		// fail-closed：扫不到就不算通过。
		t.Fatalf("解析 %s 失败: %v", rel, err)
	}
	type lit struct {
		pos  token.Pos
		text string
	}
	var lits []lit
	ast.Inspect(src, func(n ast.Node) bool {
		litNode, ok := n.(*ast.BasicLit)
		if !ok || litNode.Kind != token.STRING {
			return true
		}
		sql, err := strconv.Unquote(litNode.Value)
		if err != nil {
			return true
		}
		lits = append(lits, lit{pos: litNode.Pos(), text: sql})
		return true
	})
	// 按源码顺序拼接（AST 的遍历顺序本身已是源码序，但显式排一次更稳）。
	sort.Slice(lits, func(i, j int) bool { return lits[i].pos < lits[j].pos })
	// 逐个字面量找候选点。
	//
	// ⚠⚠ **「带 WHERE」必须在【本字面量】里判定**（★ 本轮第一版就错在这）：
	//   它原本取的是「整份文件里 ON CONFLICT 之后的全部文本」
	//   ⇒ ⇒⇒ 那个文本里几乎总有别的语句带 WHERE ⇒ ⇒⇒ **恒真**
	//   ⇒ ⇒⇒⇒ 于是一道本该咬住「守卫被删」的棘轮**一直不咬**
	//   ⇒ ⇒⇒⇒ （★ 「恒绿」的第一嫌疑永远是量具坏了）。
	//
	// ⚠ 表名：优先取**本字面量**里的 INSERT INTO；
	//   本字面量里没有（SQL 是跨字面量拼出来的，例如 task/store.go 那样）
	//   ⇒ ⇒⇒ 回退到**源码序在前**的最近一个 INSERT INTO。
	var out []s943Candidate
	for i, l := range lits {
		m := s943ConflictRE.FindStringIndex(l.text)
		if m == nil {
			continue
		}
		rest := l.text[m[1]:]
		du := s943DoUpdateRE.FindStringIndex(rest)
		if du == nil {
			// 光有 ON CONFLICT (id) 而没有 DO UPDATE（例如 DO NOTHING）⇒ 不是候选。
			continue
		}
		table := "?"
		if tm := s943InsertRE.FindStringSubmatchIndex(l.text); tm != nil {
			table = l.text[tm[2]:tm[3]]
		} else {
			// 回退：源码序在前**最近一个** INSERT INTO。
			// ⚠ 不能取**第一个**（一个文件里常有好几条 INSERT）⇒ 否则张冠李戴。
			for j := i - 1; j >= 0; j-- {
				if tm := s943InsertRE.FindStringSubmatchIndex(lits[j].text); tm != nil {
					table = lits[j].text[tm[2]:tm[3]]
					break
				}
			}
		}
		out = append(out, s943Candidate{
			Key:            rel + "|" + table,
			File:           rel,
			Table:          table,
			HasWhereClause: s943WhereRE.MatchString(rest[du[1]:]),
		})
	}
	return out
}

// TestS943BareIDConflictCandidateSetIsPinned 全仓「裸 id + DO UPDATE」候选集棘轮。
//
// ⚠ 三件事缺一不可：
//
//	① 候选集的**绝对条数**（12）—— 少一处 ⇒ 有人删了它却没更新台账；
//	② 候选集的**逐个身份**（文件 + 表）—— 换了一处等价的写法也要知道；
//	③ 台账里声明为「SQL 谓词守卫」的条目 ⇒ 那一条**必须**真的有 WHERE
//	   （这一维是机械可判的；其余形态**不判** ⇒ 避免假阴性）。
//
// ⚠⚠ §944 新增第四件事：
//
//	④ 「**id 由客户端自选** ⇒ 必须有归属守卫」这条**蕴含**
//	   （Reach = client-chosen 的条目，Guard 必须是 sql-where 或 precheck）。
//	   ⇒ 这是 §943 那 9 处「待定性」的**收口**：定性结果落进台账两维，
//	   而「可达 + 无守卫」这个组合**再也过不去**。
//
// ⚠ 失败信息要说清「该怎么办」：这张表就是**待定性的清单**，
//
//	新增一处 ⇒ 请先定性（可达性 / 后果），再改这张表。
func TestS943BareIDConflictCandidateSetIsPinned(t *testing.T) {
	root := repoRoot(t)

	var found []s943Candidate
	for _, rel := range trackedFiles(t, root) {
		if !strings.HasPrefix(rel, "backend/") || !strings.HasSuffix(rel, ".go") {
			continue
		}
		// testdata 下的 .go 故意是坏代码 / 独立程序 ⇒ 不参与扫描。
		if strings.Contains(rel, "/testdata/") {
			continue
		}
		// ⚠ **测试文件里的 SQL 不是生产写路径** ⇒ 不计入候选集
		//   （那里出现的 ON CONFLICT 是夹具语句，例如
		//   `email/store_messageid_test.go` 里的 emails 夹具）。
		//   ⚠ 这条排除是**范围声明**，不是「它们没风险」——
		//   测试代码不写生产数据 ⇒ 撞 id 也改不到别人的行。
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		found = append(found, s943ScanGo(t, root, rel)...)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Key < found[j].Key })

	// ① 绝对条数。
	if len(found) != len(s943Registry) {
		var lines []string
		for _, c := range found {
			lines = append(lines, "  "+c.Key+"  带WHERE="+strconv.FormatBool(c.HasWhereClause))
		}
		t.Fatalf("裸 id + ON CONFLICT DO UPDATE 的候选集有 %d 处（台账登记 %d 处）\n%s\n"+
			"⇒ 新增/删除一处都要**重新定性**（可达性 / 后果），然后更新 s943Registry。",
			len(found), len(s943Registry), strings.Join(lines, "\n"))
	}

	// ②③ 逐个身份 + 机械可判的那一维。
	for _, c := range found {
		entry, ok := s943Registry[c.Key]
		if !ok {
			t.Fatalf("候选集里多出一处：%s（表 %s）—— 请先定性再更新 s943Registry",
				c.File, c.Table)
		}
		if entry.Table != c.Table {
			t.Errorf("%s 的表名解析成 %q，台账写的是 %q", c.Key, c.Table, entry.Table)
		}
		if entry.Guard == s943GuardSQLWhere && !c.HasWhereClause {
			t.Errorf("%s 在台账里声明为「SQL 谓词守卫」，但实际 DO UPDATE 后面**没有 WHERE** ⇒ "+
				"要么守卫被删了，要么台账过期（台账过期会误导后来人）", c.Key)
		}
	}

	// ④ §944 的**新不变量**：「id 由客户端自选 ⇒ 必须有归属守卫」。
	//
	//	★ 这条把 §943 留下的 9 处待定性项**收口**成一件**机器可判**的事：
	//	「撞号可达」（id 由请求体给）+「没有归属守卫」⇒ 只能是两种结果：
	//	**要么**补守卫，**要么**把定性改成「不可达」（id 服务端派生 / 死能力）
	//	⇒ ⇒⇒ 将来有人新写一处「客户端自选 id + 裸 id DO UPDATE」⇒ 这道门立刻红
	//	⇒ ⇒⇒⇒ 而不是因为「忘了登记」而溜过去。
	//
	//	⚠ 为什么不是「全部都要有守卫」：
	//	`emails`（id 服务端派生）与 `opencode_sessions`（死能力）确实没有守卫，
	//	但它们**撞不到** ⇒ ⇒⇒ 把它们也要求成「必须有守卫」只会逼着人写
	//	**没有意义的守卫**（那反而会让真守卫被淹没）。
	//	⇒ ⇒⇒ 所以判据断言的是「**可达 ⇒ 必须有守卫**」这条**蕴含**。
	for _, c := range found {
		entry := s943Registry[c.Key]
		if entry.Reach != s943ReachClientChosen {
			continue
		}
		if entry.Guard != s943GuardSQLWhere && entry.Guard != s943GuardPrecheck {
			t.Errorf("%s 的 id 由**客户端自选**（撞号可达）却没有归属守卫（台账写的是 %q）⇒ "+
				"要么补守卫，要么把 Reach 改成「不可达」并说清理由（§944 的通用口径）",
				c.Key, entry.Guard)
		}
	}
}
