// skipledger_test.go —— 抽取器本身的判据自证。
//
// ⚠ 本文件含大量**故意构造**的假样本（字符串里的 `t.Skip(`、
//
//	原始字符串里的假函数签名……）。它们都不是真跳过点。
//	它们能安全地存在，靠的是 AST 抽取：字面量不是 CallExpr。
//	⇒ 若哪天有人把抽取器改回「正则 + 剥注释 + 配平花括号」，
//	  本文件会立刻变成**污染源**（不是失败，是污染——那更难发现）。
//	  这正是 §467 那版抽取器的死法，本文件就是给那条路留的绊马索。
//
// 四组负控，每组对应**一个实测过的真实缺陷**（详见 skipledger.go 的文件头）：
//
//	① 漏认 t.Skipf( / t.SkipNow(      —— 全仓 57 处被无视，低估 23%
//	② 字面量里的假函数签名被当成真函数 —— stt 9 个幽灵点
//	③ 函数体里的 "t.Skip(" 字面量被当成调用 —— 同一文件的 9 个幽灵点
//	④ 字面量里的花括号截断配平 / 状态机失灵 —— livetest 4 个点被报成 0
package skipaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ① 三种跳过写法都必须被认出来。
func TestScanDir_认全三种写法(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "m_test.go", "package p\n"+
		"func TestMixed(t *testing.T) {\n"+
		"\tt.Skip(\"a\")\n"+
		"\tif x {\n\t\tt.Skipf(\"b %d\", 1)\n\t}\n"+
		"\tif y {\n\t\tt.SkipNow()\n\t}\n"+
		"\t_, _, _ = x, y, 0\n}\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Points != 3 {
		t.Fatalf("期望 1 个函数 / 3 个跳过点（Skip+Skipf+SkipNow），实测 %+v\n"+
			"  ⚠ 只认 t.Skip( 就是 §467 的原样，而它漏掉了全仓 57 处 Skipf", fs)
	}
}

// ①b 接收者写错的名字不该被算成跳过点（`myt.Skip(` 是子串误收）。
func TestScanDir_不误收别的接收者(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "r_test.go", "package p\n"+
		"func TestR(t *testing.T) {\n"+
		"\tmyt.Skip(\"x\")\n"+
		"\ts.t.Skip(\"y\")\n"+
		"\t_ = 0\n}\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 0 {
		t.Fatalf("期望 0 个跳过点，实测 %+v ⇒ 子串匹配把别的接收者算进来了", fs)
	}
}

// ①c ⚠ testing 参数不叫 t 时也必须认出来。
//
//	写死 "t" 会让这些文件的跳过点**整包消失**且门照样绿（恒假）。
func TestScanDir_参数名不是t也要认(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tt_test.go", "package p\n"+
		"func TestOddName(tt *testing.T) {\n"+
		"\ttt.Skip(\"x\")\n}\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "TestOddName" {
		t.Fatalf("期望认出 TestOddName，实测 %+v ⇒ 抽取器把接收者名写死了", fs)
	}
}

// ② 字面量里的假函数签名不得被当成真函数。
func TestScanDir_字面量里的函数签名不算真函数(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ghost_test.go", "package p\n"+
		"const sample = `package q\n"+
		"// func TestGhost(t *testing.T) { t.Skip(\"幽灵\") }\n"+
		"func TestGhost2(t *testing.T) { t.Skip(\"幽灵2\") }\n"+
		"`\n"+
		"func TestReal(t *testing.T) { t.Skip(\"真的\") }\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "TestReal" {
		t.Fatalf("期望恰好 1 个真跳过测试（TestReal），实测 %+v\n"+
			"  ⚠ 多出来的就是字面量里的幽灵 ⇒ 量具把自己算进去了（stt 实测 9 个）", fs)
	}
}

// ③ 真实函数体里的 "t.Skip(" 字面量不得被当成跳过调用。
func TestScanDir_函数体里的字面量不算跳过调用(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "selfproof_test.go", "package p\n"+
		"func TestSelfProof(t *testing.T) {\n"+
		"\traw := someFile()\n"+
		"\thas := strings.Contains(raw, \"t.Skip(\")\n"+
		"\tn := strings.Count(raw, \"t.Skip(\")\n"+
		"\t_, _, _ = has, n, raw\n}\n"+
		"func TestActuallySkips(t *testing.T) {\n"+
		"\tt.Skip(\"这个才是真的\")\n\t_ = 1\n}\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "TestActuallySkips" || fs[0].Points != 1 {
		t.Fatalf("期望只认出 TestActuallySkips/1 点，实测 %+v\n"+
			"  ⚠ TestSelfProof 里的两个 \"t.Skip(\" 字面量被当成了调用", fs)
	}
}

// ④ 字面量里的花括号不得截断函数体（§467 的配平 bug 的回归测试）。
func TestScanDir_字面量里的花括号不截断函数体(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "braces_test.go", "package p\n"+
		"func TestBraceInString(t *testing.T) {\n"+
		"\tif x == \"}\" {\n\t\tt.Skip(\"看到 } 就收工\")\n\t}\n"+
		"\tt.Skip(\"第二条，必须也能被看见\")\n\t_ = x\n}\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Points != 2 {
		t.Fatalf("期望 1 个函数 / 2 点，实测 %+v ⇒ 字面量里的 } 截断了配平", fs)
	}
}

// ④b ⭐ 注释里的 t.Skip 不算（§464/§467 纪律②）。
func TestScanDir_注释里的跳过不算(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cmt_test.go", "package p\n"+
		"// func TestInComment(t *testing.T) { t.Skip(\"注释里的不算\") }\n"+
		"/* func TestInBlock(t *testing.T) { t.Skip(\"块注释里的也不算\") } */\n"+
		"func TestReal(t *testing.T) { t.Skip(\"这个才算\") }\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "TestReal" || fs[0].Points != 1 {
		t.Fatalf("期望只认出 TestReal/1 点，实测 %+v ⇒ 注释没被排除", fs)
	}
}

// ⑤ 量具自证（纪律③）：合成样本必须被扫到，且不能凭空多出。
func TestScanDir_量具自证(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "x_test.go", "package p\n"+
		"func TestNoSkip(t *testing.T) { _ = 1 }\n"+
		"func TestSkips(t *testing.T) { t.Skip(\"a\"); t.Skip(\"b\") }\n"+
		"func TestHelper(t *testing.T) { helperThatSkips() }\n")
	write(t, dir, "notatest.go", "package p\n"+
		"func TestSkips(t *testing.T) { t.Skip(\"a\") }\n")
	fs, err := ScanDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "TestSkips" || fs[0].Points != 2 {
		t.Fatalf("期望恰好 1 个可跳过测试 / 2 点，实测 %+v\n"+
			"  ⚠ 非 _test.go 文件被计入，或 helper 函数被误认", fs)
	}
}

// ⑥ 解析失败必须报错，不能静默跳过整个文件。
// 静默 continue ⇒ 一个文件的跳过点从普查里消失，而门照样绿。
func TestScanDir_解析失败必须报错(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "broken_test.go", "package p\nfunc TestBroken(t *testing.T) { this is not go }\n")
	if _, err := ScanDir(dir, nil); err == nil {
		t.Fatal("破损文件被静默跳过 ⇒ 它的跳过点会从普查里消失而门仍绿")
	}
}

// ⑦ 豁免精确到文件，且不外溢。
func TestScanDir_豁免精确到文件(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\nfunc TestA(t *testing.T) { t.Skip(\"x\") }\n")
	write(t, dir, "b_test.go", "package p\nfunc TestB(t *testing.T) { t.Skip(\"y\") }\n")
	fs, err := ScanDir(dir, map[string]bool{"b_test.go": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Name == "TestB" {
			t.Fatal("豁免了 b_test.go 却仍读到了 TestB ⇒ 豁免参数没生效")
		}
	}
	if len(fs) != 1 || fs[0].Name != "TestA" {
		t.Fatalf("期望只剩 TestA，实测 %+v", fs)
	}
}

// ⑧ Check 的双向性：漏登记与陈旧都必须被抓出来。
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
			E("TestRegistered", CatCredential, "原因", "UNLOCK=1"),
			E("TestGone", CatCredential, "原因", "UNLOCK=1"),
			E("TestEmptyUnlock", CatCredential, "原因"),
		},
		MinFuncs: 1,
	}
	res, err := Check(l, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unregistered) != 1 || res.Unregistered[0] != "TestSurprise" {
		t.Fatalf("漏登记方向没咬：%v", res.Unregistered)
	}
	if len(res.Stale) != 2 || res.Stale[0] != "TestEmptyUnlock" || res.Stale[1] != "TestGone" {
		t.Fatalf("陈旧方向没咬：%v", res.Stale)
	}
	if res.OK() {
		t.Fatal("Check 在三种缺陷下仍报 OK ⇒ 判据恒真")
	}
}

// ⑨ 干净状态下 Check 必须全绿，否则这道门是恒红的。
func TestCheck_干净状态全绿(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestOnly(t *testing.T) { t.Skip(\"a\") }\n"+
		"func TestPlain(t *testing.T) { _ = 1 }\n")
	l := Ledger{
		Pkg:      "./synthetic",
		Entries:  []Entry{E("TestOnly", CatCredential, "原因", "UNLOCK=1")},
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

// ⑩ SelfTestOK 的三条边界。
func TestSelfTestOK(t *testing.T) {
	ok := Ledger{MinFuncs: 3, Entries: []Entry{E("X", CatCredential, "r", "u")}}
	if err := SelfTestOK(ok, Result{Found: 3}); err != nil {
		t.Fatalf("读数达标却报错：%v", err)
	}
	if err := SelfTestOK(ok, Result{Found: 2}); err == nil {
		t.Fatal("读数不足却放行 ⇒ 量具自证形同虚设")
	}
	if err := SelfTestOK(Ledger{MinFuncs: 0}, Result{Found: 0}); err == nil {
		t.Fatal("空台账 + 零读数被放行 ⇒ 这正是纪律③要防的")
	}
}

// ⑪ Register 只补 Points，不许编造 Reason/Unlock。
func TestRegister_只补Points(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a_test.go", "package p\n"+
		"func TestA(t *testing.T) { t.Skip(\"a\"); t.Skipf(\"b\", 1) }\n")
	in := []Entry{E("TestA", CatDiagProbe, "原因", "UNLOCK=1")}
	out, err := Register(in, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Points != 2 {
		t.Fatalf("Points 应为 2，实测 %d", out[0].Points)
	}
	if out[0].Reason != "原因" || len(out[0].Unlock) != 1 {
		t.Fatalf("Register 动了手写栏位：%+v", out[0])
	}
	if in[0].Points != 0 {
		t.Fatal("Register 改了入参切片")
	}
	_ = strings.TrimSpace
}
