// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账（§467 纪律的续包）。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。internal/testaudit 的普查门
// （TestSkipCensus_台账登记表自证）要求：每个拥有跳过点的包
// 必须有 skip_ledger_test.go 逐条登记，否则「绿色读数」会把
// 「这一段压根没跑」盖住。
//
// 本包只有一个跳过点（2026-11-27 审计登记）：
//
//	TestListRunEventsOverCapFailsExplicitly —— `-short` 模式下跳过，
//	因为它要构造 64MB 的 HTTP 载荷来钉「超上限响应必须显式失败、
//	不得静默截断」。这条锁在默认（非 -short）运行下会真实执行。
//
// ⛔ 本文件**不含**跳过调用字面量，因此**不需要任何豁免**
// （AST 抽取：字面量不是 CallExpr，见 internal/skipaudit/skipledger_test.go）。
package mcp

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**拥有跳过点的函数（owner）。
// ⛔ 不在表内的一律红（双向精确匹配：漏登记红、陈旧也红）。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("TestListRunEventsOverCapFailsExplicitly", skipaudit.CatShortMode,
		"钉「ListRunEvents 收到超过 64MB 上限的响应必须显式报错，不得静默截断」；"+
			"需构造 64MB 载荷，`-short` 模式下跳过以保住快速反馈环。",
		1,
		"不加 -short 运行（守卫是 testing.Short()）",
		"go test ./internal/mcp/ -run TestListRunEventsOverCapFailsExplicitly -v"),
}

// 三条纪律与本包的关系（照 internal/server/skip_ledger_test.go 的门形状）：
// ① 双向精确匹配（漏登记红、陈旧也红）→ TestSkipLedger_双向精确匹配
// ② 剥注释但保留字面量 → 由 internal/skipaudit 的 go/parser 实现
// ③ 量具先自证 → TestSkipLedger_量具自证
func TestSkipLedger_双向精确匹配(t *testing.T) {
	entries := map[string]skipaudit.Entry{}
	for _, e := range knownSkips {
		if _, dup := entries[e.Owner]; dup {
			t.Fatalf("owner %q 重复登记：每个跳过点有且只有一个 owner", e.Owner)
		}
		entries[e.Owner] = e
	}

	pkg, err := skipaudit.ScanPackage(".", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 漏登记 → 红
	for _, test := range pkg.Tests {
		if test.Points == 0 {
			continue
		}
		if _, ok := entries[test.Name]; !ok {
			t.Errorf("跳过点未登记：%s（%d 点）⇒ 请补台账或说明豁免理由", test.Name, test.Points)
		} else if got := entries[test.Name].Points; got != test.Points {
			t.Errorf("owner %s 台账点数 %d ≠ 实测 %d ⇒ 陈旧条目", test.Name, got, test.Points)
		}
	}

	// 陈旧 → 红
	for name := range entries {
		found := false
		for _, test := range pkg.Tests {
			if test.Name == name && test.Points > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("台账条目 %s 已陈旧：该 owner 已无跳过点 ⇒ 请删除条目", name)
		}
	}
}

func TestSkipLedger_量具自证(t *testing.T) {
	pkg, err := skipaudit.ScanPackage(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, test := range pkg.Tests {
		total += test.Points
	}
	// 本包当前实测 1 个跳过点。若这条红，说明扫描器行为变了或新增了跳过点，
	// 先更新 knownSkips 再更新这里 —— 两处都要 conscious 地改。
	if total != 1 {
		t.Fatalf("本包跳过点总数实测 %d ≠ 1 ⇒ 扫描器或用例变了，请同步更新 knownSkips", total)
	}
}
