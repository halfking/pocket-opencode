// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账（§467 纪律的续包，2026-10-17 补登）。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账，
// 但它的实现是 `filepath.Glob("*.go")`，只覆盖 stt 一个包；
// 2026-10-17 普查发现全仓 214 个跳过点里只有 12 个被逐条登记（5.6%）。
//
// 本仓的规矩：`docs/design/…-recording-quality-fixes.md` §467 / §775。
// 一道门必须**双向**精确匹配 —— 漏登记红、陈旧也红。
// 只增不减的台账会烂成一张永久通行证。
//
// 三条纪律与本文件的关系
// ----------------------
// ① 双向精确匹配        → TestSkipLedger_双向精确匹配
// ② 剥注释但保留字面量  → 由 internal/skipaudit 的 go/parser 实现：
//
//	AST 里没有注释，字符串字面量也不是 CallExpr，
//	所以 §467 抽取器的三个幽灵点（字面量里的假函数、
//	函数体里的 "t.Skip(" 字面量、只认 t.Skip( 漏掉 Skipf）一起消失。
//
// ③ 量具先自证          → TestSkipLedger_量具自证
//
// ⛔ 本文件**不含** `t.Skip(` 字面量，因此**不需要任何豁免**。
//
//	豁免本身是一种长期风险（§467 专门为它写过一条自证）；
//	能不用就不用，是最省事的防烂办法。
package flashcards

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**拥有跳过点的函数（owner）。⛔ 不在表内的一律红。
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
// 本包 2026-10-21 实测：3 个跳过点 / 3 个 owner / 7 个测试受影响。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("TestStore_CRUD_RoundTrip", skipaudit.CatHostCapability,
		"Store 的增删改查全链路需要真实 Postgres（回读、级联与唯一约束都只在真表上验证）。", 1,
		"POCKET_TEST_POSTGRES_DSN 指向一个可写的测试库"),

	skipaudit.EP("newPgStore", skipaudit.CatCredential,
		"flashcards 集成用例的 PG 夹具：没有 DSN 就整组跳过（5 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("newTestPGFlashcardStore", skipaudit.CatCredential,
		"flashcards 种子数据的 PG 集成夹具：没有 DSN 就跳过。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/flashcards",
	Entries:  knownSkips,
	MinFuncs: 3, // 实测 3 个 owner
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/flashcards")
	if err != nil {
		t.Fatalf("定位包目录失败: %v", err)
	}
	return d
}

// ③ 量具自证：先确认抽取器真的看得见东西，再谈台账对不对。
func TestSkipLedger_量具自证(t *testing.T) {
	res := scan(t)
	if err := skipaudit.SelfTestOK(ledger, res); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s：跳过测试 %d 个 / 跳过点 %d 个，台账登记 %d 条", ledger.Pkg, res.Found, res.Points, len(ledger.Entries))
}

// ① 双向精确匹配。
func TestSkipLedger_双向精确匹配(t *testing.T) {
	res := scan(t)
	if !res.OK() {
		t.Error(res.String())
	}
	// 台账登记的点数必须与实际点数一致 —— 这是台账与 census 的交叉口径。
	// ⚠ Points 刻意手写而非自动补：自动补会让这条校验恒真。
	if res.LedgeredPoints != res.Points {
		t.Errorf("台账点数 %d ≠ 实际点数 %d ⇒ Points 字段写错了",
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

func scan(t *testing.T) skipaudit.Result {
	t.Helper()
	res, err := skipaudit.Check(ledger, dir(t), nil)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	return res
}
