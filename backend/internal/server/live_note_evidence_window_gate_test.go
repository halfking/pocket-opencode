// live_note_evidence_window_gate_test.go — §339「围绕命中位置取窗口」的契约门。
//
// 为什么这条门与 §334 的探针分开
// ------------------------------
// §334 的探针是**缺陷判据**：它回答「证据里有没有那个词」，
// 修法实施后它转绿 —— 这是**预期行为**，不是它的判别力。
// 本文件钉的是**契约**：无论关键词落在正文的哪个位置，
// 证据窗口都**完整**包含它。§334 的两条臂只测了第 15 字与第 314 字，
// 中间与**边界**（窗口切口）都没覆盖。
//
// ⚠ 边界是这里唯一真正的风险：
//
//	窗口右端是**开区间**（r[from:to]），而关键词可能正好骑在切口上 ——
//	一半在窗口内、一半在外。那时 `Contains` 仍可能为真（匹配到半截），
//	所以本文件用**单字关键词**做扫掠，把跨边界情形暴露成确定的红。
//
// ⚠ 本文件与探针都只是**契约门**：判据的可信度由变异台 /tmp/opstt/339 给。
package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// singleRuneKeyword 是扫掠用的**单字**查询词。
//
// ⚠ 必须单字：多字词骑在窗口切口上时，前半截仍可能被匹配到，
//
//	于是「关键词被切掉了」这件事会被 `Contains` 漏过去。
//
// 用生僻字是为了不与填充文本撞词。
const sweepKeyword = "龘"

func sweepNote(t *testing.T, keywordAt int) notes.Note {
	t.Helper()
	// 前缀用 §334 探针的填充句（不含 sweepKeyword），
	// 关键词单字放在第 keywordAt 字处，后面再垫足够长度。
	body := evidenceFillerOfLength(t, keywordAt) + sweepKeyword + evidenceFillerOfLength(t, 400)
	// 前提自证：关键词真的在第 keywordAt 字，且填充里没有它。
	idx := strings.Index(body, sweepKeyword)
	if idx < 0 {
		t.Fatalf("关键词没进正文")
	}
	if got := len([]rune(body[:idx])); got != keywordAt {
		t.Fatalf("夹具内部矛盾：关键词落在第 %d 字，声称第 %d 字", got, keywordAt)
	}
	if strings.Count(body, sweepKeyword) != 1 {
		t.Fatalf("正文里出现了 %d 个查询词 ⇒ 命中位置不唯一", strings.Count(body, sweepKeyword))
	}
	return storedQuickNote(t, body)
}

func sweepSearch(t *testing.T, n notes.Note, query string) string {
	t.Helper()
	tool := &meetingagent.NoteSearcher{
		Src:    staticNoteSource{items: []notes.Note{n}},
		UserID: "u", WorkspaceID: "ws",
	}
	out, err := tool.Execute(context.Background(), []byte(`{"query":"`+query+`"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}

// TestEvidenceWindowAlwaysContainsTheKeyword 是扫掠门：
// 关键词放在 0..360 的**每一个**字位，证据行都必须完整含它。
//
// ⚠ 起点是 0 而不是 1：第 0 字必须也含（窗口从 from=max(0, hitAt-lead) 起）。
func TestEvidenceWindowAlwaysContainsTheKeyword(t *testing.T) {
	// ⚠⚠ 哨兵必须是 -1，不能用整数的默认值 0。
	//   第一版写的是 `var firstBad int`（= 0），而 at 也从 0 开始 ——
	//   于是**即使 361 个位置全部通过**，末尾的 `firstBad >= 0` 也照样成立，
	//   测试报「第 0 字位失败」并打印第 0 字位的**正常输出**。
	//   我被那段输出带偏去查字符编码（hexdump 显示常量是 U+9F98），
	//   而真正的线索是「报错指向第 0 位」—— 第 0 位正是初始值。
	var firstBad = -1
	for at := 0; at <= 360; at++ {
		out := sweepSearch(t, sweepNote(t, at), sweepKeyword)
		// 前提：必须命中。不命中的话「证据里没词」就变成了 §333 第三层。
		if strings.Contains(out, "没有找到") {
			t.Fatalf("第 %d 字位没命中 ⇒ 扫掠量的不是窗口契约而是检索命中", at)
		}
		if !strings.Contains(evidenceLines(out), sweepKeyword) {
			firstBad = at
			break
		}
	}
	if firstBad >= 0 {
		out := sweepSearch(t, sweepNote(t, firstBad), sweepKeyword)
		t.Fatalf("关键词在第 %d 字位时**没有**出现在证据行里：\n%s",
			firstBad, evidenceLines(out))
	}
	t.Logf("0..360 每个字位都通过（共 361 个位置）")
}

// TestEvidenceWindowIsBounded 钉住「token 零增长」这条承诺：
// 无论正文多长，回给模型的证据都不得超过 evidenceLead+evidenceTrail。
//
// ⚠ 这条是 §339 选 B 案而不是 A 案的核心理由，必须钉住 ——
//
//	将来有人「顺手」把窗口改成 800 时，它必须打红。
func TestEvidenceWindowIsBounded(t *testing.T) {
	// 正文 3000 字，关键词埋在第 2500 字（远超任何固定窗口）。
	long := evidenceFillerOfLength(t, 2500) + sweepKeyword + evidenceFillerOfLength(t, 500)
	note := storedQuickNote(t, long)
	out := sweepSearch(t, note, sweepKeyword)
	ev := evidenceLines(out)
	if strings.Contains(out, "没有找到") {
		t.Fatalf("长正文没命中")
	}
	if !strings.Contains(ev, sweepKeyword) {
		t.Fatalf("第 2500 字的关键词没进证据窗口：\n%s", ev)
	}
	// 窗口上限：证据行加上前缀/后缀标记的「…」，主体不得超过 160+2。
	r := len([]rune(strings.TrimSpace(ev)))
	if r > 162 {
		t.Errorf("证据行 %d 字，超过约定的 160（+2 个省略号）⇒ token 代价不再是 0：\n%s", r, ev)
	}
	t.Logf("3000 字正文 + 第 2500 字关键词 ⇒ 证据行 %d 字", r)
}

// TestEvidenceWindowFallsBackWhenNoBodyHit 钉住回退分支：
// 只命中**标题**、正文里没有查询词时，不得返回空证据。
func TestEvidenceWindowFallsBackWhenNoBodyHit(t *testing.T) {
	note := storedQuickNote(t, evidenceFillerOfLength(t, 400))
	note.Title = "标题里有龘这个词"
	out := sweepSearch(t, note, sweepKeyword)
	if strings.Contains(out, "没有找到") {
		t.Fatalf("标题命中却没进结果")
	}
	if strings.TrimSpace(evidenceLines(out)) == "" {
		t.Fatalf("正文没有命中词时证据行是空的 ⇒ 回退分支坏了：\n%s", out)
	}
}

// TestEvidenceWindowTruncatesShortBodies 边界：正文比窗口短时给全文，不加省略号。
func TestEvidenceWindowTruncatesShortBodies(t *testing.T) {
	body := "龘" + evidenceFillerOfLength(t, 40)
	note := storedQuickNote(t, body)
	out := sweepSearch(t, note, sweepKeyword)
	ev := strings.TrimSpace(evidenceLines(out))
	if strings.Contains(ev, "…") {
		t.Errorf("正文只有 %d 字（短于窗口）却被标成截断：%q", len([]rune(body)), ev)
	}
	if !strings.Contains(ev, sweepKeyword) {
		t.Errorf("短正文里连关键词都丢了：%q", ev)
	}
	t.Logf("短正文（%d 字）原样给出", len([]rune(body)))
}

var _ = fmt.Sprintf // 保留 fmt 引用以免 goimports 在重排 import 时误删
