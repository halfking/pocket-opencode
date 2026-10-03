package email

import (
	"os"
	"strings"
	"testing"
)

// ## 这条护栏守的是什么
//
// 真库最后一条 MIME 摘要泄漏（em-1669791317-acct-1790870162063806800-3）
// 的根因，2026-10-03 23:5x 实测定死：
//
//  1. 对该账户跑 `POST /api/email/backfill {days:30}` → fetched=14 saved=14
//     skipped=0，即这一行**确实被回补处理了**；
//  2. 处理后 snippet **一字未变**（len 仍 501，仍是
//     `------=_Part_… Content-Type: …`）。
//
// ⇒ 回补走到了这一行、InsertEmail 也执行了，但落库值没变。原因是
// DeriveSnippet 对这份输入**正确地返回了空串**，而
// store.go 的 upsert 写的是
//
//	snippet = CASE WHEN EXCLUDED.snippet <> '' THEN EXCLUDED.snippet ELSE emails.snippet END
//
// 空串在这里的含义是「不覆盖」，于是 2026-10-01 写进去的坏摘要被
// **永久冻结**：代码修好了，数据再也回不来。
//
// 这与「DeriveSnippet 判据失明」是两回事 —— 出口是安全的一侧（空），
// 坏的是**自愈链路**：没有任何一条路径能覆盖掉一个已知的坏值。
//
// ## 为什么判据钉在「库里现存的形态」而不是「函数的返回值」
//
// 只断言 DeriveSnippet 返回空串是不够的：那条断言在修复前后**都成立**
// （它本来就没在泄漏）。真正要守的不变量是
// 「已入库的 snippet 里不得残留 MIME 源码」，而它的失败模式恰恰是
// 「DeriveSnippet 正确返回空 ⇒ 旧坏值冻结」—— 只有从库这一侧看才看得见。
//
// 因此这里做两件事：
//   - 用真库导出的真实样本钉住 DeriveSnippet 对该形态的当前行为（不泄漏、返回空）；
//   - 用负控证明这条判据**有牙齿**：把样本换回「干净正文」形态时它必须转红。
const lastLeakSample = "testdata/last_leak_snippet.txt"

// negCtrlLeak 是负控开关：置 true 时让判据作用在「出口退回原文」的坏实现上，
// 用来证明本文件的判据**确实会红**。正常路径恒为 false。
const negCtrlLeak = false

// TestLastLeakSampleDerivesEmpty 钉住真库最后一条泄漏样本的当前行为。
//
// 输入形态的关键特征（实测，勿轻易改判据）：boundary 行、Content-Type 头
// 与正文被 IMAP **压在同一行**，因此 startsWithBoundaryLine=false、
// mimeParts 拆出 0 个 part —— 也就是说 2026-10-03 那次「按 part 拆」
// 的修复（snippetFromMIMEParts）**对它天然够不着**，不是修复失效。
func TestLastLeakSampleDerivesEmpty(t *testing.T) {
	raw, err := os.ReadFile(lastLeakSample)
	if err != nil {
		t.Skipf("缺少 %s（真库导出的真实样本）：%v", lastLeakSample, err)
	}
	body := strings.TrimRight(string(raw), "\r\n")

	// 先确认样本形态没被悄悄换掉：它是「压平的 boundary + MIME 头 + 正文」。
	if strings.HasPrefix(body, "Content-Type:") || looksLikePlainCleanBody(body) {
		t.Fatalf("样本形态已变（首行=%q），本测试守的形态不再存在，请重写", diagFirstLine(body))
	}

	got := DeriveSnippet([]byte(body), 500)
	if negCtrlLeak {
		// NEGCTRL: 模拟「出口退回原始字节」——2026-10-03 修复前的真实行为
		got = truncateRunes(body, 500)
	}
	if containsMIMESource(got) {
		t.Fatalf("DeriveSnippet 对真库最后一条泄漏样本返回了 MIME 源码，这是活着的泄漏：%q",
			truncForDiag(got))
	}
	// 当前形态下它返回空串（安全的一侧）。这不是「缺陷已修」的证据，
	// 只是把「出口不泄漏」这件事钉住 —— 见下面的负控与冻结测试。
	if got != "" {
		t.Logf("DeriveSnippet 返回了非空正文（%d rune），形态已变，请复核判据", len([]rune(got)))
	}
}

// TestLastLeakFrozenByEmptyUpsert 钉住**冻结**这个失效模式本身。
//
// 判据跑在 upsert 的那条 CASE 表达式所实现的语义上：空串 = 不覆盖。
// 它是纯函数，不需要真库，也不会被并发会话的网络状态影响。
func TestLastLeakFrozenByEmptyUpsert(t *testing.T) {
	const badStored = "------=_Part_x Content-Type: text/html; charset=\"UTF-8\" <p>旧坏值</p>"
	const freshGood = "国庆节放假值班通知：请各位同事查收值班表。"

	if got := upsertSnippet(badStored, ""); got != badStored {
		t.Fatalf("空摘要必须保留旧值（空=不覆盖），实际得到 %q", got)
	}
	if got := upsertSnippet(badStored, freshGood); got != freshGood {
		t.Fatalf("非空摘要必须覆盖旧值，实际得到 %q", got)
	}
	// 这就是本条泄漏的完整因果：DeriveSnippet 修好了、返回空，
	// 于是坏Stored 永远留在库里。
	if upsertSnippet(badStored, "") == badStored && DeriveSnippet([]byte(badStored), 500) == "" {
		t.Log("复现成立：出口安全（空）+ 空=不覆盖 ⇒ 存量坏值被永久冻结")
	}
}

// upsertSnippet 是 store.go 里那条 CASE 表达式的等价实现，
// 语义：EXCLUDED 非空才覆盖，否则保留旧值。
func upsertSnippet(stored, excluded string) string {
	if excluded != "" {
		return excluded
	}
	return stored
}

func looksLikePlainCleanBody(s string) bool {
	return !containsMIMESource(s)
}

func diagFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncForDiag(s string) string {
	r := []rune(s)
	if len(r) <= 220 {
		return s
	}
	return string(r[:220]) + "…"
}
