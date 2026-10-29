// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账（§467 纪律的续包）。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账，
// 但它的实现是 `filepath.Glob("*.go")`，只覆盖 stt 一个包；
// 2026-10-17 普查发现全仓 214 个跳过点里只有 12 个被登记（5.6%）。
//
// 本仓的规矩（`docs/design/…-recording-quality-fixes.md` 的 §467/§775）：
//
//	一道门必须**双向**精确匹配 —— 漏登记红、陈旧也红。
//	只增不减的台账会烂成一张永久通行证。
//
// 三条纪律与本文件的关系
// ----------------------
// ① 双向精确匹配          → TestSkipLedger_双向精确匹配
// ② 剥注释但保留字面量    → 由 internal/skipaudit 的 go/parser 实现，
//
//	AST 里根本没有注释，也把字面量里的
//	「假 t.Skip」排除在外。
//
// ③ 量具先自证            → TestSkipLedger_量具自证
//
// ⛔ 本文件**不含** `t.Skip(` 字面量，因此**不需要任何豁免**。
//
//	§467 的 stt 台账必须豁免它自己（负控样本里有一段原始字符串写着
//	`func TestReal(t *testing.T) { t.Skip(...) }`）—— 而豁免本身是一种
//	长期风险（§467 为此专门写了一条自证）。改用 AST 后这一类豁免可以
//	整体消失：豁免没了，「豁免烂成通行证」的路也就没了。
package notes

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**拥有跳过点的函数（owner）。
// ⛔ 不在表内的一律红。
//
// ⚠ **登记单位是 owner（拥有跳过点的函数），不是「测试」** —— 2026-10-21 改的：
//
//	本包的跳过点有一部分在 **helper**（夹具函数）里，而 helper 被 N 个测试共用。
//	若登记单位是「测试」，那 N 个测试就要写 N 条内容相同的条目 ——
//	而「为什么没跑」「需要什么才能跑」**只取决于那个 helper**。
//	⇒ 台账会烂成 N 份互相抄写的副本，抄错一份没人发现；
//	  而新增一个调用该 helper 的测试就会让门转红（纯粹的无意义抖动）。
//
//	每个跳过点**有且只有一个 owner** ⇒ 「每个 owner 都登记了」
//	⇒「每个跳过点都被登记了」⇒「运行时跳过的每一条都有台账可查」。
//
// 本包 2026-10-21 实测：7 个跳过点 / 7 个 owner / 12 个测试受影响。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("TestStore_Upsert_Insert", skipaudit.CatHostCapability,
		"Store 的 CRUD 需要真实 Postgres；本机没有测试库时整组跳过。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库",
		"（不设它 ⇒ 6 个 Store 用例全部 SKIP，而 go test 仍报 ok）"),
	skipaudit.EP("TestStore_Upsert_Update", skipaudit.CatHostCapability,
		"同上：需要真实 Postgres 才能验证 upsert 的更新分支。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),
	skipaudit.EP("TestStore_Upsert_InvalidDomain", skipaudit.CatHostCapability,
		"同上：校验分支也要真的落库才知道回滚是否生效。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),
	skipaudit.EP("TestStore_List", skipaudit.CatHostCapability,
		"同上：列表查询要在真实表上跑。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),
	skipaudit.EP("TestStore_GetByID_NotFound", skipaudit.CatHostCapability,
		"同上：查不存在要真的发一次查询。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),
	skipaudit.EP("TestStore_Delete", skipaudit.CatHostCapability,
		"同上：删除要验证行真的消失。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),

	skipaudit.EP("newNotesTestPool", skipaudit.CatCredential,
		"notes 的 PG 集成夹具：没有 DSN 就整包跳过（12 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/notes",
	Entries:  knownSkips,
	MinFuncs: 7, // 实测 7 个 owner
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/notes")
	if err != nil {
		t.Fatalf("定位包目录失败: %v", err)
	}
	return d
}

// ③ 量具自证：先确认抽取器真的看得见东西，再谈台账对不对。
func TestSkipLedger_量具自证(t *testing.T) {
	res, err := scan(t)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if err := skipaudit.SelfTestOK(ledger, res); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s：跳过测试 %d 个 / 跳过点 %d 个，台账登记 %d 条", ledger.Pkg, res.Found, res.Points, len(ledger.Entries))
}

// ① 双向精确匹配。
func TestSkipLedger_双向精确匹配(t *testing.T) {
	res, err := scan(t)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if !res.OK() {
		t.Error(res.String())
	}
	// 台账登记的点数必须与实际点数一致 —— 这是台账与 census 的交叉口径
	if res.LedgeredPoints != res.Points {
		t.Errorf("台账点数 %d ≠ 实际点数 %d ⇒ Points 字段没跑 Register 补齐",
			res.LedgeredPoints, res.Points)
	}
}

// 类别分布要**显式**钉住：新增一个「哑弹守卫」时，
// 必须有人在这里改一个常量，而不是悄悄混进总量里。
//
// ⚠ 这一类的语义在 2026-10-21 被**实测推翻**过一次：
//
//	它曾经叫 CatPremiseFailed（「前提不成立 ⇒ 永远不会失败」）。
//	实测结果是：那批守卫**全部没有触发** ⇒ 那些测试今天**是在断言的**，
//	不是「永远失败」。名字写错 ⇒ 下一个会话会按错的前提去处理它
//	（去「修好那个永远失败的测试」，而它其实一直是绿的）。
//
// ⇒ 现在叫 CatDefensiveGuard（哑弹守卫），说的是真话：
//
//	守卫的前提**当前恒成立** ⇒ 这条测试今天在跑、在断言；
//	而一旦前提成立，它会**静默退出门外** ——
//	那时绿色读数与「跑过并通过」完全无法区分。
func TestSkipLedger_哑弹守卫计数被钉住(t *testing.T) {
	const wantDefensiveGuard = 0
	got := 0
	for _, e := range knownSkips {
		if e.Cat == skipaudit.CatDefensiveGuard {
			got++
		}
	}
	if got != wantDefensiveGuard {
		t.Errorf("哑弹守卫类实际 %d 条，钉住值是 %d。\n"+
			"  ⇒ 新增一条要**有意识地**改这个常量并说明理由：\n"+
			"    这类的代价不是「暂时没跑」，而是「**哑弹**」——\n"+
			"    守卫的前提当前恒成立 ⇒ 它永不触发 ⇒ 这条测试今天**是在断言的**；\n"+
			"    而一旦前提成立，它会静默退出门外，那时绿色读数与「跑过并通过」无法区分。",
			got, wantDefensiveGuard)
	}
}

func scan(t *testing.T) (skipaudit.Result, error) {
	t.Helper()
	return skipaudit.Check(ledger, dir(t), nil)
}
