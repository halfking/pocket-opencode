// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账；
// 2026-10-17 普查轮把抽取器收编到 internal/skipaudit（go/parser 实现）；
// 2026-10-21 第三轮把**登记单位**从「测试」改成「**拥有跳过点的函数（owner）**」。
//
// ⚠ 为什么是 owner 而不是测试：
//
//	本包的跳过点在 **helper** 里（夹具函数），而 helper 被 N 个测试共用。
//	若登记单位是「测试」，那 N 个测试就要写 N 条内容相同的条目 ——
//	而「为什么没跑」「需要什么才能跑」只取决于那个 helper。
//	⇒ 台账会烂成 N 份互相抄写的副本，抄错一份没人发现，
//	  而新增一个调用该 helper 的测试就会让门转红（纯粹的无意义抖动）。
//
// 本仓的规矩（docs/design/…-recording-quality-fixes.md 的 §467/§775）：
//
//	一道门必须**双向**精确匹配 —— 漏登记红、陈旧也红。
//	只增不减的台账会烂成一张永久通行证。
//
// 本包 2026-10-21 实测：1 个跳过点 / 1 个 owner / 11 个测试受影响。
//
// 三条纪律与本文件的关系
// ----------------------
// ① 双向精确匹配        → TestSkipLedger_双向精确匹配
// ② 剥注释但保留字面量  → 由 internal/skipaudit 的 go/parser 实现：
//
//	AST 里根本没有注释，也把字面量里的「假跳过调用」排除在外。
//
// ③ 量具先自证          → TestSkipLedger_量具自证
//
// ⛔ 本文件**不含**跳过调用字面量，因此**不需要任何豁免**。
//
//	豁免本身是一种长期风险（§467 专门为它写过一条自证）；能不用就不用。
package notifycenter

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**拥有跳过点的函数（owner）。
//
// ⛔ 不在表内的一律红。
//
// 分类的规矩（照 §467，按**守卫条件**分，不按感觉分）：
//   - CatDiagProbe：显式 opt-in 的诊断探针，跑起来可能写真实数据；
//   - CatCredential：需要真 DSN / 真凭据 / 真语料；
//   - CatHostCapability：需要宿主具备某种能力；
//   - CatFixtureRegen：守卫是「重新生成已提交夹具」的开关；
//   - CatShortMode：`-short` 模式下跳过；
//   - CatDefensiveGuard：守卫的前提**当前恒成立**（哑弹：前提一旦成立它会静默退出门外）。
//
// ⛔ Unlock 绝不含「配置一下」这类空话：每条都落到具体的 env 键名，
//
//	或具体到「要让哪个条件变成真」—— §464 的教训是下一个会话会把
//	「跳过」重新登记成「没查」，而校验器看不出这两者的区别。
//
// ⛔ Points 是**手写**的：它是「我以为它是几个」，与 AST 数出来的互相校验。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("newTestStore", skipaudit.CatCredential,
		"通知中心的集成用例要真连 Postgres（通知项落库与去重）；本机没有测试库 ⇒ 这 11 个测试跳过。",
		1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/notifycenter",
	Entries:  knownSkips,
	MinFuncs: 1, // 实测 1 个 owner
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/notifycenter")
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
	t.Logf("%s：owner %d 个 / 跳过点 %d 个 / 受影响测试 %d 个，台账登记 %d 条",
		ledger.Pkg, res.Owners, res.Points, res.Found, len(ledger.Entries))
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

// 类别分布要**显式**钉住：新增一条某一类的跳过点时，
// 必须有人在这里改一个常量，而不是悄悄混进总量里。
func TestSkipLedger_类别计数被钉住(t *testing.T) {
	want := map[skipaudit.Category]int{
		skipaudit.CatCredential: 1,
	}
	got := map[skipaudit.Category]int{}
	for _, e := range knownSkips {
		got[e.Cat]++
	}
	for cat, n := range want {
		if got[cat] != n {
			t.Errorf("类别 %s 实际 %d 条，钉住值是 %d。\n"+
				"  ⇒ 新增一条要**有意识地**改这个常量并说明理由。",
				cat, got[cat], n)
		}
	}
	for cat, n := range got {
		if _, ok := want[cat]; !ok && n > 0 {
			t.Errorf("出现了未登记的类别 %s（%d 条）⇒ 分类规矩变了就要改这里", cat, n)
		}
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
