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
package wecom

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**允许被跳过的测试。⛔ 不在表内的一律红。
var knownSkips = []skipaudit.Entry{
	skipaudit.E("TestDecryptFromNetFixture", skipaudit.CatFixtureRegen,
		"用 .NET 侧导出的真实密文夹具验解密对格式变更的兼容性。",
		"先跑 scripts/wecom-net-fixture.ps1 生成 .NET fixture 并提交")}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/wecom",
	Entries:  knownSkips,
	MinFuncs: 1, // 实测 1 个
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/wecom")
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

// 类别分布要**显式**钉住：新增一个「前提不成立就跳过」的测试时，
// 必须有人在这里改一个常量，而不是悄悄混进总量里。
func TestSkipLedger_前提不成立类计数被钉住(t *testing.T) {
	const wantPremiseFailed = 0
	got := 0
	for _, e := range knownSkips {
		if e.Cat == skipaudit.CatPremiseFailed {
			got++
		}
	}
	if got != wantPremiseFailed {
		t.Errorf("「前提不成立就跳过」类实际 %d 条，钉住值是 %d。\n"+
			"  ⇒ 新增一条要**有意识地**改这个常量并说明理由：\n"+
			"    这类的代价不是「暂时没跑」，而是「**永远不会失败**」——\n"+
			"    前提一旦不成立，测试自己退出门外，绿色读数与「跑过并通过」无法区分。",
			got, wantPremiseFailed)
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
