// skipledger_test.go —— 抽取器本身的判据自证。
//
// ⚠ 本文件含大量**故意构造**的假样本（字符串里的 `t.Skip(`、
//
//	原始字符串里的假函数签名……）。它们都不是真跳过点。
//	它们能安全地存在，靠的是 AST 抽取：字面量不是 CallExpr。
//	⇒ 若哪天有人把抽取器改回「正则 + 剥注释 + 配平花括号」，
//	  本文件会立刻变成**污染源**（不是失败，是污染——那更难发现）。
//
// ⚠ 而 2026-10-21 实测：**上游合并把普查门覆盖回手写扫描器之后**，
//
//	那版扫描器把本文件里的样本字面量**数成了 22 个跳过点**，
//	而门禁当时是绿的 ⇒ 「量具把自己的噪声算成读数」第三次发生。
//
// 负控，每组对应**一个实测过的真实缺陷**：
//
//	① 漏认 t.Skipf( / t.SkipNow( 以及未来的任何 Skip* 写法
//	② 字面量里的假函数签名被当成真函数
//	③ 函数体里的 "t.Skip(" 字面量被当成调用
//	④ ⭐ helper 里的跳过点必须归因到调用它的测试（2026-10-21 实测的 385 个缺口）
//	⑤ 归因必须**传递**（测试 → helper → helper）
//	⑥ ⭐ 登记单位是 **owner**（拥有跳过点的函数），helper 也是登记单位
//	⑦ ⭐ 方法 owner 必须带接收者类型前缀（否则同包同名方法被合成一个）
package skipaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ① 三种跳过写法都必须被认出来。
func TestScanPackage_认全三种写法(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "m_test.go", "package p\n"+
		"func TestMixed(t *testing.T) {\n"+
		"\tt.Skip(\"a\")\n"+
		"\tif x {\n\t\tt.Skipf(\"b %d\", 1)\n\t}\n"+
		"\tif y {\n\t\tt.SkipNow()\n\t}\n"+
		"\t_, _, _ = x, y, 0\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Points != 3 {
		t.Fatalf("期望 1 个能跳过的测试 / 3 个跳过点，实测 %+v\n"+
			"  ⚠ 只认 t.Skip( 就是 §467 的原样，而它漏掉了全仓 34 处 Skipf", pkg.Tests)
	}
}

// ①b ⭐ 未来的 Skip* 写法也不能漏（白名单是失明的来源）。
func TestScanPackage_认未来新增的Skip写法(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "f_test.go", "package p\n"+
		"func TestFuture(t *testing.T) {\n"+
		"\tt.SkipIf(true, \"未来可能新增的写法\")\n"+
		"\t_ = 0\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 {
		t.Fatalf("期望认出 TestFuture，实测 %+v ⇒ 白名单式的写法匹配会再次失明", pkg.Tests)
	}
}

// ①c 接收者写错的名字不该被算成跳过点（`myt.Skip(` 是子串误收）。
func TestScanPackage_不误收别的接收者(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "r_test.go", "package p\n"+
		"func TestR(t *testing.T) {\n"+
		"\tmyt.Skip(\"x\")\n"+
		"\ts.t.Skip(\"y\")\n"+
		"\t_ = 0\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 0 {
		t.Fatalf("期望 0 个能跳过的测试，实测 %+v ⇒ 子串匹配把别的接收者算进来了", pkg.Tests)
	}
}

// ①d ⚠ testing 参数不叫 t 时也必须认出来。
//
//	写死 "t" 会让这些文件的跳过点**整包消失**且门照样绿（恒假）。
func TestScanPackage_参数名不是t也要认(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tt_test.go", "package p\n"+
		"func TestOddName(tt *testing.T) {\n"+
		"\ttt.Skip(\"x\")\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestOddName" {
		t.Fatalf("期望认出 TestOddName，实测 %+v ⇒ 抽取器把接收者名写死了", pkg.Tests)
	}
}

// ② 字面量里的假函数签名不得被当成真函数。
func TestScanPackage_字面量里的函数签名不算真函数(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ghost_test.go", "package p\n"+
		"const sample = `package q\n"+
		"// func TestGhost(t *testing.T) { t.Skip(\"幽灵\") }\n"+
		"func TestGhost2(t *testing.T) { t.Skip(\"幽灵2\") }\n"+
		"`\n"+
		"func TestReal(t *testing.T) { t.Skip(\"真的\") }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestReal" {
		t.Fatalf("期望恰好 1 个真跳过测试（TestReal），实测 %+v\n"+
			"  ⚠ 多出来的就是字面量里的幽灵 ⇒ 量具把自己算进去了", pkg.Tests)
	}
}

// ③ 真实函数体里的 "t.Skip(" 字面量不得被当成跳过调用。
func TestScanPackage_函数体里的字面量不算跳过调用(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "selfproof_test.go", "package p\n"+
		"func TestSelfProof(t *testing.T) {\n"+
		"\traw := someFile()\n"+
		"\thas := strings.Contains(raw, \"t.Skip(\")\n"+
		"\tn := strings.Count(raw, \"t.Skip(\")\n"+
		"\t_, _, _ = has, n, raw\n}\n"+
		"func TestActuallySkips(t *testing.T) {\n"+
		"\tt.Skip(\"这个才是真的\")\n\t_ = 1\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestActuallySkips" || pkg.Tests[0].Points != 1 {
		t.Fatalf("期望只认出 TestActuallySkips/1 点，实测 %+v\n"+
			"  ⚠ TestSelfProof 里的两个 \"t.Skip(\" 字面量被当成了调用", pkg.Tests)
	}
}

// ④ ⭐⭐ helper 里的跳过点必须归因到调用它的测试。
//
// 这是 2026-10-21 实测到的**最大缺口**：运行时跳过 500 个测试，
// 静态只看测试函数体时只看得见 157 个 ⇒ 385 个完全不可见。
//
// 真实形态（全仓有 73 个这样的 helper）：
//
//	func newTestStore(t *testing.T) (*Store, func()) {
//	    if dsn == "" {
//	        t.Skip("POCKET_TEST_POSTGRES_DSN not set; …")
//	    }
//	    …
//	}
func TestScanPackage_helper里的跳过点归因到调用者(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "h_test.go", "package p\n"+
		"func newTestStore(t *testing.T) int {\n"+
		"\tif dsn == \"\" {\n\t\tt.Skip(\"POCKET_TEST_POSTGRES_DSN not set\")\n\t}\n"+
		"\treturn 1\n}\n"+
		"func TestUsesHelper(t *testing.T) {\n"+
		"\t_ = newTestStore(t)\n}\n"+
		"func TestDoesNot(t *testing.T) {\t_ = 1\n}\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestUsesHelper" {
		t.Fatalf("期望只认出 TestUsesHelper（helper 的跳过点应归因给它），实测 %+v\n"+
			"  ⚠ 这正是 2026-10-21 实测的 385 个缺口", pkg.Tests)
	}
	if pkg.Tests[0].Points != 1 {
		t.Fatalf("Points 应为 1，实测 %d", pkg.Tests[0].Points)
	}
	if len(pkg.Tests[0].Via) != 1 || pkg.Tests[0].Via[0] != "newTestStore" {
		t.Fatalf("Via 应指出 helper 名，实测 %v", pkg.Tests[0].Via)
	}
	if pkg.Sites != 1 {
		t.Fatalf("本包去重后的跳过点应为 1（helper 里那一个），实测 %d", pkg.Sites)
	}
}

// ④b ⭐ 归因必须**传递**（测试 → helper → helper）。
func TestScanPackage_归因是传递的(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "t2_test.go", "package p\n"+
		"func inner(t *testing.T) {\n\tt.Skip(\"最深处\")\n}\n"+
		"func outer(t *testing.T) { inner(t) }\n"+
		"func TestChain(t *testing.T) { outer(t) }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestChain" || pkg.Tests[0].Points != 1 {
		t.Fatalf("期望认出 TestChain 且 Points=1（经 outer→inner 传递），实测 %+v", pkg.Tests)
	}
}

// ④c ⭐ 递归调用不能把归因搞成死循环。
func TestScanPackage_递归调用不挂死(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "rec_test.go", "package p\n"+
		"func a(t *testing.T) { b(t) }\n"+
		"func b(t *testing.T) { a(t); t.Skip(\"环里的跳过点\") }\n"+
		"func TestRec(t *testing.T) { a(t) }\n")
	done := make(chan struct{})
	go func() {
		defer close(done)
		pkg, err := ScanPackage(dir, nil)
		if err != nil {
			t.Errorf("扫描失败: %v", err)
			return
		}
		if len(pkg.Tests) != 1 || pkg.Tests[0].Points != 1 {
			t.Errorf("期望认出 TestRec/1 点，实测 %+v", pkg.Tests)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("递归调用导致归因死循环（5 秒未返回）")
	}
}

// ⑤ 量具自证（纪律③）：合成样本必须被扫到，且不能凭空多出。
func TestScanPackage_量具自证(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "x_test.go", "package p\n"+
		"func TestNoSkip(t *testing.T) { _ = 1 }\n"+
		"func TestSkips(t *testing.T) { t.Skip(\"a\"); t.Skip(\"b\") }\n"+
		"func helperThatSkips(t *testing.T) { t.Skip(\"helper 里的\") }\n"+
		"func TestHelper(t *testing.T) { helperThatSkips(t) }\n")
	// 非测试文件里的 Test 函数**不会**被 go test 执行 ⇒ 不得计入台账
	write(t, dir, "notatest.go", "package p\n"+
		"func TestInNonTestFile(t *testing.T) { t.Skip(\"a\") }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Tests) != 2 {
		t.Fatalf("期望恰好 2 个能跳过的测试（TestSkips / TestHelper），实测 %+v\n"+
			"  ⚠ 非 _test.go 文件里的 Test 函数不该被计入（go test 不会执行它们）", pkg.Tests)
	}
	for _, f := range pkg.Tests {
		if f.Name == "TestInNonTestFile" {
			t.Fatal("非 _test.go 文件里的 Test 函数被计入了 ⇒ 台账会虚增")
		}
	}
	byName := map[string]FuncSkip{}
	for _, f := range pkg.Tests {
		byName[f.Name] = f
	}
	if byName["TestSkips"].Points != 2 {
		t.Fatalf("TestSkips 应有 2 个跳过点，实测 %d", byName["TestSkips"].Points)
	}
	if byName["TestHelper"].Points != 1 {
		t.Fatalf("TestHelper 应有 1 个跳过点，实测 %d", byName["TestHelper"].Points)
	}
}

// ⑥ 解析失败必须报错，不能静默跳过整个文件。
// 静默 continue ⇒ 一个文件的跳过点从普查里消失，而门照样绿。
func TestScanPackage_解析失败必须报错(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "broken_test.go", "package p\nfunc TestBroken(t *testing.T) { this is not go }\n")
	if _, err := ScanPackage(dir, nil); err == nil {
		t.Fatal("破损文件被静默跳过 ⇒ 它的跳过点会从普查里消失而门仍绿")
	}
}

// ⑦ 豁免精确到文件，且不外溢。
func TestScanPackage_豁免精确到文件(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\nfunc TestA(t *testing.T) { t.Skip(\"x\") }\n")
	write(t, dir, "b_test.go", "package p\nfunc TestB(t *testing.T) { t.Skip(\"y\") }\n")
	pkg, err := ScanPackage(dir, map[string]bool{"b_test.go": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range pkg.Tests {
		if f.Name == "TestB" {
			t.Fatal("豁免了 b_test.go 却仍读到了 TestB ⇒ 豁免参数没生效")
		}
	}
	if len(pkg.Tests) != 1 || pkg.Tests[0].Name != "TestA" {
		t.Fatalf("期望只剩 TestA，实测 %+v", pkg.Tests)
	}
}

// ⑧ ⭐⭐ owner 模型：helper 是**登记单位**，且一个 helper 只登记一次。
//
// 这一条钉住 2026-10-21 的核心结论：
// 同一个 helper 被 300 多个测试触达时，台账登记**一条**（那个 helper），
// 而不是 300 条内容相同的副本。
//
// ⚠ 若哪天模型退回「按测试登记」，本条必须失败 —— 因为那样
//
//	「新增一个调用该 helper 的测试」会让门转红（纯粹的无意义抖动），
//	而 300 条互相抄写的副本抄错一条没人能发现。
func TestScanPackage_owner是登记单位且helper只登记一次(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "own_test.go", "package p\n"+
		"func newTestStore(t *testing.T) int {\n"+
		"\tif dsn == \"\" {\n\t\tt.Skip(\"POCKET_TEST_POSTGRES_DSN not set\")\n\t}\n"+
		"\treturn 1\n}\n"+
		"func TestA(t *testing.T) { _ = newTestStore(t) }\n"+
		"func TestB(t *testing.T) { _ = newTestStore(t) }\n"+
		"func TestC(t *testing.T) { _ = newTestStore(t) }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Owners) != 1 || pkg.Owners[0].Name != "newTestStore" {
		t.Fatalf("期望恰好 1 个 owner（newTestStore），实测 %+v\n"+
			"  ⚠ owner 数暴涨 ⇒ 登记单位又退回「每个测试一条」了", pkg.Owners)
	}
	if len(pkg.Owners[0].OwnSites) != 1 {
		t.Fatalf("newTestStore 应拥有 1 个跳过点，实测 %d", len(pkg.Owners[0].OwnSites))
	}
	if len(pkg.Owners[0].AffectedTests) != 3 {
		t.Fatalf("newTestStore 应影响 3 个测试，实测 %v", pkg.Owners[0].AffectedTests)
	}
	// 三个测试各自都能跳过，但**只有一个 owner**
	if len(pkg.Tests) != 3 {
		t.Fatalf("期望 3 个能跳过的测试，实测 %d", len(pkg.Tests))
	}
	if pkg.Sites != 1 {
		t.Fatalf("本包跳过点总数应为 1（去重），实测 %d", pkg.Sites)
	}
}

// ⑧b ⭐ owner 的自身点数必须与「它自己的跳过点」相等（不是可达点数）。
//
// 台账的 Points 写的是「这个 owner 里有几个跳过点」，
// 而「它能触达几个」是另一回事 —— 混用会让「台账点数之和 == 本包点数」
// 这条对账恒假。
func TestScanPackage_owner自身点数与可达点数不是一回事(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pts_test.go", "package p\n"+
		"func inner(t *testing.T) { t.Skip(\"深处的点\") }\n"+
		"func outer(t *testing.T) { inner(t); t.Skip(\"自己的点\") }\n"+
		"func TestUses(t *testing.T) { outer(t) }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Owner{}
	for _, o := range pkg.Owners {
		byName[o.Name] = o
	}
	if got := len(byName["outer"].OwnSites); got != 1 {
		t.Fatalf("outer 自身只有 1 个跳过点（inner 那个不算它的），实测 %d ⇒ owner 自身点数被可达点数污染了", got)
	}
	if got := byName["outer"].Reachable; got != 2 {
		t.Fatalf("outer 可达点数应为 2（自身 + inner），实测 %d", got)
	}
	if got := len(byName["inner"].OwnSites); got != 1 {
		t.Fatalf("inner 自身应有 1 个跳过点，实测 %d", got)
	}
}

// ⑨ ⭐ owner 名冲突：同包两个不同类型的同名方法必须分开。
//
// 不加接收者类型前缀 ⇒ 两条台账条目会指向两个不同的东西，
// 而校验器看不出区别（它们只是被合并了）。
func TestScanPackage_方法owner带接收者前缀(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "m_test.go", "package p\n"+
		"type A struct{}\ntype B struct{}\n"+
		"func (a *A) setup(t *testing.T) { t.Skip(\"A 的\") }\n"+
		"func (b *B) setup(t *testing.T) { t.Skip(\"B 的\") }\n")
	pkg, err := ScanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Owners) != 2 {
		t.Fatalf("期望 2 个 owner（A.setup / B.setup），实测 %+v\n"+
			"  ⚠ 同名方法被合并了 ⇒ 一条台账条目会指向两个不同的东西", pkg.Owners)
	}
	names := map[string]bool{}
	for _, o := range pkg.Owners {
		names[o.Name] = true
	}
	if !names["A.setup"] || !names["B.setup"] {
		t.Fatalf("owner 名应带接收者类型前缀，实测 %v", names)
	}
}

// ⑩ Check 的双向性：漏登记与陈旧都必须被抓出来。
// 若这条挂了，「双向精确匹配」就退化成单向，台账会慢慢烂成通行证。
func TestCheck_双向都咬(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestRegistered(t *testing.T) { t.Skip(\"a\") }\n"+
		"func TestSurprise(t *testing.T) { t.Skip(\"b\") }\n"+
		"func TestGone(t *testing.T) { }\n")
	l := Ledger{
		Pkg: "./synthetic",
		Entries: []Entry{
			EP("TestRegistered", CatCredential, "原因", 1, "UNLOCK=1"),
			EP("TestGone", CatCredential, "原因", 1, "UNLOCK=1"),
			EP("TestEmptyUnlock", CatCredential, "原因", 1),
		},
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unregistered) != 1 || !strings.Contains(res.Unregistered[0], "TestSurprise") {
		t.Fatalf("漏登记方向没咬：%v", res.Unregistered)
	}
	if len(res.Stale) != 2 || res.Stale[0] != "TestEmptyUnlock" || res.Stale[1] != "TestGone" {
		t.Fatalf("陈旧方向没咬：%v", res.Stale)
	}
	if len(res.Empty) != 1 || res.Empty[0] != "TestEmptyUnlock" {
		t.Fatalf("空话方向没咬：%v", res.Empty)
	}
	if res.OK() {
		t.Fatal("Check 在多种缺陷下仍报 OK ⇒ 判据恒真")
	}
}

// ⑪ ⭐ helper owner 漏登记必须报红（2026-10-21 那个 385 缺口的正面钉子）。
//
// 若这条挂了，「helper 的跳过点」又变成不可见的东西 ——
// 而它恰恰是运行时跳过的主体。
func TestCheck_helper漏登记会咬(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "h_test.go", "package p\n"+
		"func newTestStore(t *testing.T) int {\n"+
		"\tt.Skip(\"POCKET_TEST_POSTGRES_DSN not set\")\n\treturn 1\n}\n"+
		"func TestUses(t *testing.T) { _ = newTestStore(t) }\n")

	// 情形一：只登记了「测试」—— 而它其实一个跳过点都没有（跳过点在 helper 里）
	l := Ledger{
		Pkg:      "./synthetic",
		Entries:  []Entry{EP("TestUses", CatCredential, "以为是自己跳过", 1, "UNLOCK=1")},
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unregistered) != 1 || !strings.Contains(res.Unregistered[0], "newTestStore") {
		t.Fatalf("helper owner 漏登记却没报红：%v\n"+
			"  ⚠ 这就是 2026-10-21 那 385 个缺口复活了", res.Unregistered)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "TestUses" {
		t.Fatalf("追不到已登记 owner 的测试应被点名，实测 %v", res.Uncovered)
	}

	// 情形二：登记**那个 helper**（而不是那个测试）⇒ 全绿
	l2 := Ledger{
		Pkg:      "./synthetic",
		Entries:  []Entry{EP("newTestStore", CatCredential, "没有 DSN 就跳过", 1, "POCKET_TEST_POSTGRES_DSN=<可写测试库 DSN>")},
		MinFuncs: 1,
	}
	res2, err := Check(l2, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.OK() {
		t.Fatalf("登记了 helper 却仍报红 ⇒ 新模型自相矛盾：%s", res2.String())
	}
}

// ⑫ 干净状态下 Check 必须全绿，否则这道门是恒红的。
func TestCheck_干净状态全绿(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestOnly(t *testing.T) { t.Skip(\"a\") }\n"+
		"func TestPlain(t *testing.T) { _ = 1 }\n")
	l := Ledger{
		Pkg:      "./synthetic",
		Entries:  []Entry{EP("TestOnly", CatCredential, "原因", 1, "UNLOCK=1")},
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("干净状态却报红 ⇒ 这道门是恒红的：%s", res.String())
	}
}

// ⑬ Points 交叉校验：台账手写的点数与实测不符必须报红。
// ⚠ 这条若被「自动补 Points」废掉，它就永远绿 —— 那正是 2026-10-17 踩过的坑。
func TestCheck_Points交叉校验会咬(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestOnly(t *testing.T) { t.Skip(\"a\"); t.Skip(\"b\") }\n")
	l := Ledger{
		Pkg:      "./synthetic",
		Entries:  []Entry{EP("TestOnly", CatCredential, "原因", 1, "UNLOCK=1")}, // 写 1，实测 2
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PointMismatches) == 0 {
		t.Fatal("Points 写错却没报红 ⇒ 交叉校验失效（多半是 Points 被自动补了）")
	}
}

// ⑭ ⚠ 台账点数之和必须等于本包跳过点总数（对账等式）。
//
// 每个跳过点有且只有一个 owner ⇒ 「Σowner 自身点数」恒等于本包点数。
// 这条等式一旦不成立，说明有条目整个漏了登记（逐条校验抓不到「整条漏掉」）。
func TestCheck_点数对账会咬(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestA(t *testing.T) { t.Skip(\"a\") }\n"+
		"func TestB(t *testing.T) { t.Skip(\"b\") }\n")
	l := Ledger{
		Pkg: "./synthetic",
		// 只登记 TestA，TestB 漏了 ⇒ 点数之和 1 ≠ 实测 2
		Entries:  []Entry{EP("TestA", CatCredential, "原因", 1, "UNLOCK=1")},
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range res.PointMismatches {
		if strings.Contains(m, "点数之和") {
			found = true
		}
	}
	if !found {
		t.Fatalf("点数对账没咬：%v", res.PointMismatches)
	}
}

// ⑮ SelfTestOK 的三条边界。
func TestSelfTestOK(t *testing.T) {
	ok := Ledger{MinFuncs: 3, Entries: []Entry{EP("X", CatCredential, "r", 1, "u")}}
	if err := SelfTestOK(ok, Result{Owners: 3}); err != nil {
		t.Fatalf("读数达标却报错：%v", err)
	}
	if err := SelfTestOK(ok, Result{Owners: 2}); err == nil {
		t.Fatal("读数不足却放行 ⇒ 量具自证形同虚设")
	}
	if err := SelfTestOK(Ledger{MinFuncs: 0}, Result{Owners: 0}); err == nil {
		t.Fatal("空台账 + 零读数被放行 ⇒ 这正是纪律③要防的")
	}
}

var _ = strings.TrimSpace
