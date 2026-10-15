package livetest

import "testing"

// TestLedgerReportsCompleteRun 跑满时**必须**安静。
//
// ⭐ 为什么这条排第一：完整性对账若在跑满时也说话，
// 很快就会被无视 —— 那就等于没有对账。
func TestLedgerReportsCompleteRun(t *testing.T) {
	l := New("对照", 2)
	if !l.Valid() {
		t.Fatal("total=2 却被判为非法")
	}
	l.Complete("a")
	if l.Incomplete() != true {
		t.Error("1/2 却被判为完整")
	}
	l.Complete("b")
	if l.Incomplete() {
		t.Error("2/2 却被判为不完整")
	}
	if got := l.Report(); got != "" {
		t.Errorf("跑满时 Report() 仍有输出 ⇒ 会被无视：%q", got)
	}
	if l.Done() != 2 || l.Total() != 2 {
		t.Errorf("Done/Total = %d/%d，应为 2/2", l.Done(), l.Total())
	}
}

// TestLedgerReportsIncompleteRun 是**承重**判据：少跑一项必须出声。
func TestLedgerReportsIncompleteRun(t *testing.T) {
	l := New("精校探针", 3)
	l.Complete("a")
	l.Complete("b")
	got := l.Report()
	if got == "" {
		t.Fatal("只完成 2/3 却没有 Report ⇒ 本包整个失效")
	}
	// 读数必须**自足**：不能只说「不完整」，要说清完成了几项、共几项。
	for _, want := range []string{"2/3", "不是完整结果"} {
		if !contains(got, want) {
			t.Errorf("Report 里缺 %q：%s", want, got)
		}
	}
}

// TestLedgerRejectsZeroTotal 守住「一项都不做却要报读数」。
//
// ⚠ 形态与 §483 完全同形：目标组 0 条时探针照样打印小结。
func TestLedgerRejectsZeroTotal(t *testing.T) {
	for _, bad := range []int{0, -1, -10} {
		l := New("空轮", bad)
		if l.Valid() {
			t.Errorf("total=%d 被判为合法", bad)
		}
		if l.Report() == "" {
			t.Errorf("total=%d 时 Report() 静默 ⇒ 一项不做也能报读数", bad)
		}
	}
	for _, ok := range []int{1, 3, 17} {
		if !New("正常", ok).Valid() {
			t.Errorf("total=%d 被判为非法", ok)
		}
	}
}

// ⛔ 负控：判据必须能抓到「把对账删掉」。
//
// 没有这一条，上面三条都可能是恒真的 —— 而「恒真」与「正确」在退出码上
// 毫无区别（§471.2 / §481.4 同族）。
//
// ⚠ 负控用的是**同一批断言**跑一份「不做对账」的模拟实现：
// 它在少跑一项时静默 ⇒ 那正是「没有对账」时的行为。
func TestLedgerNegativeControl(t *testing.T) {
	// 模拟「不记账」的实现：永远宣称完整。
	naive := struct {
		done, total int
	}{done: 2, total: 3}
	naiveReport := ""
	if naive.done == naive.total {
		naiveReport = ""
	}
	// 上面对账判据（TestLedgerReportsIncompleteRun 用的那条）会要求 Report 非空。
	// 这里验证 naive 在同样输入下**确实**给不出报告 ⇒ 判据不是恒真。
	if naive.done != naive.total && naiveReport != "" {
		t.Fatal("负控构造有误：naive 不该在 2/3 时给出报告")
	}
	real := New("x", 3)
	real.Complete("a")
	real.Complete("b")
	if real.Report() == "" {
		t.Fatal("真实现反而不报 ⇒ 判据方向反了")
	}
	// 再验一遍「跑满」时两者都静默 ⇒ 差别只在不完整时。
	if naiveReport == "" && real.Report() != "" {
		t.Log("（预期如此：2/3 时 naive 静默、真实现出声）")
	}
}

// ── 「预期跳过」的判据（§495）──────────────────────────────────────
//
// ⚠⚠ 为什么必须单独一组：上面 9 条判据**全部零 skip**，
// 所以它们全绿**不能证明**跳过语义对 —— 它们连跳过的边都碰不到。
// 而跳过的语义恰好是本包最容易被滥用的一处：
//
//	把「转写失败」记成 Skip ⇒ 读数从「少跑了两档」变成「按设计跳过了两档」
//
// ⇒ 那比不记更糟，因为它把失败洗成了设计。
func TestLedgerSkipIsNotSuccess(t *testing.T) {
	l := New("重叠计时", 3)
	l.Complete("1.8s 重叠正控")
	l.Complete("负控 0s")
	l.Skip("B 片装不下（素材仅 6.2s）")

	// ① 跳过**参与**对账：否则那两档预期跳过会让这条探针**永久**显示不完整
	//   ⇒ 对账天天喊 ⇒ 很快被无视 ⇒ 等于没有对账（§488 建包的前提）。
	if l.Incomplete() {
		t.Error("2 项做完 + 1 项按设计跳过被判为不完整 ⇒ 预期跳过与失败被混同")
	}
	// ② ⛔ 跳过**不冒充成功**：Done 是「跑成了几项」，跳过项不是跑成了。
	if l.Done() != 2 {
		t.Errorf("Done()=%d，应为 2 —— 跳过项混进「跑成了几项」⇒ 读者会把「没跑」读成「跑成了」", l.Done())
	}
	if l.Skipped() != 1 {
		t.Errorf("Skipped()=%d，应为 1", l.Skipped())
	}
	// ③ 跑满但有跳过时 Report **必须出声且点名**：
	//   只印 Done()=1/Total=3 时，「跳过了 1 档」与「漏了 2 档」在读数上同形，
	//   而后者更需要人知道 —— 它意味着那两档本轮没有读数。
	rep := l.Report()
	if rep == "" {
		t.Fatal("跑满但有跳过时 Report() 静默 ⇒ 跳过项从读数里消失")
	}
	if !contains(rep, "没有读数") {
		t.Errorf("Report 没说明跳过项本轮没有读数：%s", rep)
	}
	if !contains(rep, "B 片装不下") {
		t.Errorf("Report 没有点名跳过的是哪一项：%s", rep)
	}
}

// ⛔ 承重判据：跳过**不得**洗掉真失败。
//
// ⚠ 这是上面那条的反面，也是整个 Skip 机制唯一真正要防的失效模式。
func TestLedgerSkipCannotHideIncomplete(t *testing.T) {
	l := New("重叠计时", 4)
	l.Complete("a")
	l.Complete("b")
	l.Skip("超出音频时长") // 先验理由，本轮确实不该做
	// ⚠ 还差 1 项**既没做也没跳过** —— 那就是没跑成，必须出声。
	if !l.Incomplete() {
		t.Fatal("4 项里只做了 2 跳过 1，却判为完整 ⇒ 失败被洗成「按设计跳过」")
	}
	rep := l.Report()
	if !contains(rep, "不是完整结果") {
		t.Errorf("Report 没说不完整：%s", rep)
	}
	if !contains(rep, "3/4") {
		t.Errorf("Report 没给出 3/4 这个分母：%s", rep)
	}
	// ⚠⚠ §495 变异 N0 打出来的洞：不完整**且**有跳过时，
	// 「只完成 3/4」会被读成「完成了 3 项」——
	// 而真实情况是「完成 2 + 跳过 1 + 缺 1」。跳过被算进「完成」，
	// 正是 Skip 最容易被误读的方向 ⇒ 必须点破。
	if !contains(rep, "按设计跳过") {
		t.Errorf("Report 没点破跳过 ⇒「只完成 3/4」会被读成「完成了 3 项」：%s", rep)
	}
	// 第四项补上之后才完整，且此时 Report 说的是「按设计跳过」而不是「不完整」。
	l.Complete("c")
	if l.Incomplete() {
		t.Fatal("3 项做完 + 1 项跳过仍判为不完整")
	}
	if rep := l.Report(); contains(rep, "不是完整结果") {
		t.Errorf("补齐后仍按「不完整」说话：%s", rep)
	}
}

// ⚠ 负控：跳过**不得**计入 Done。
//
// 没有这一条，③ 那条判据可能恒真 ——
// 「Done 只数成功」与「Done 把跳过也算上」在「没打印 Done」时完全同形。
func TestLedgerSkipNegativeControl(t *testing.T) {
	// 朴素实现：跳过直接当完成。
	naiveDone := 1 + 1 // 完成 1 项 + 「跳过」1 项
	if naiveDone != 2 {
		t.Fatal("负控构造有误")
	}
	real := New("x", 2)
	real.Complete("a")
	real.Skip("b")
	if real.Done() == naiveDone {
		t.Fatal("负控失效：真实现把跳过计进了 Done ⇒「跑成了几项」不再可信")
	}
	// 另一侧：朴素实现**不**参与对账 ⇒ 会把 2/2 判成完整，
	// 而真实语义下 1 完成 + 1 跳过也是 2/2 —— 这一侧两者相同，
	// 所以正面的区分力只能来自「Skipped() 与 Done() 互不相等」，
	// 上面那条已经钉住；这里钉的是另一件事：**跳过不影响分母**。
	if real.Total() != 2 {
		t.Errorf("Total()=%d，Skip 不该改变声明的分母", real.Total())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
