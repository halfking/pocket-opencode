// live_note_ref_evidence_probe_test.go —
// 「search_notes 告诉模型『找到了』，但它给模型看的证据里没有那个词」。
//
// 这是 §330–§333 那条链的**第四层**，且与前三层**形态相反**：
// 前三层是「可检索面被截短」（关键词落在窗外 ⇒ 根本搜不到），
// 这一层是「**命中了，但证据不在场**」（关键词落在窗内 ⇒ 搜到了，
// 但 renderResults 回给模型的那 160 字里没有它）。
//
// 证据链（两处用了**两把不同的尺子**）
// ------------------------------------
//
//	tools.go:138  scoreNotes   body = BodyForLLM(0)   ⇒ 0 = **不截断**，用 Content 全文打分
//	tools.go:199  renderResults snip = BodyForLLM(0) ⇒ 取到全文，
//	              随后 tools.go:202 `if len([]rune(snip)) > 160` 截到 **160 rune**
//
// ⇒ 一个命中在第 161 字之后的关键词，会让笔记**排进结果**，
//
//	而模型看到的那 160 字里**根本没有那个词**。
//
// ⚠ 这一层对**随手记现在就成立**（notes-persist.ts:411-414 存 Content 全文），
//
//	对会议笔记在 §333.5 修法实施后才成立 ——
//	也就是说 §333.5 会**放大**它（会议笔记从 166 字扩到全文，
//	更多命中会落在 160 字之外）。
//
// 为什么值得单独测
// --------------
// §329 实测到 F 臂有 10/20 产出「捏造标题 + note_id 为空」的弱编造。
// 「工具说找到 3 条、但每条给模型的摘要都与查询词无关」
// 是**同一个失效形态**：模型拿不到依据，只能看着标题猜。
// 本探针把这个机制从模型行为里拆出来，直接在纯函数层判它。
//
// ★ 夹具纪律：本文件的两臂**同一条笔记、同一个词，唯一变量是词的位置**。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// evidenceKeyword 复用 §333 的尾部关键词（同词，两节可直接对照）。
const evidenceKeyword = tailKeyword

// evidenceFiller 不含任何查询词的填充文本。
const evidenceFiller = "会议首先回顾了上季度的工作进展与后续安排情况，" // 23 字

// evidenceFillerOfLength 造恰好 n 个汉字，且**不含**查询词。
func evidenceFillerOfLength(t *testing.T, n int) string {
	t.Helper()
	var sb []rune
	for len(sb) < n {
		sb = append(sb, []rune(evidenceFiller)...)
	}
	out := string(sb[:n])
	if strings.Contains(out, evidenceKeyword) {
		t.Fatalf("填充文本混进了查询词 %q ⇒ 夹具失效", evidenceKeyword)
	}
	return out
}

// storedQuickNote 复刻**随手记**路径的落库形状（notes-persist.ts:411-414）：
// Content 存全文、Snippet 存 200 rune 预览。
//
// ⚠ 与 §333 的 storedMeetingNote **正好相反** —— 那条只填 Snippet。
//
//	两条路径的切法不同正是 §333 的根因，所以这里必须造另一条。
func storedQuickNote(t *testing.T, content string) notes.Note {
	t.Helper()
	r := []rune(content)
	if len(r) > 200 {
		r = r[:200]
	}
	return notes.Note{
		ID:      "note_quick",
		Title:   "客户评审会",
		Content: content,
		Snippet: string(r),
		Domain:  "work",
	}
}

// evidenceLines 只取 renderResults 输出里**缩进的证据行**，不含标题行
// 「与「X」相关的笔记 N 条：」。
//
// ⚠⚠ 2026-10-09（§334.2）：第一版用 `strings.Contains(整段输出, 查询词)`
//
//	判「证据里有没有那个词」—— 那是**恒真**的，因为 renderResults 的
//	标题行 `fmt.Fprintf(&sb, "与「%s」相关的笔记 %d 条：\n", query, len(hits))`
//	**把查询词原样印在第一行**。
//	⇒ 臂 B 明明没复现，判据却报「含词」，测试转绿。
//	方向最贵：它会把这一层判成「不存在」，于是没人去修。
//	**阴性对照那一条也同样恒真**，所以它当时一点保证都没提供。
func evidenceLines(out string) string {
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "   ") { // 缩进行 = 每条结果的证据
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func searchOne(t *testing.T, n notes.Note) string {
	t.Helper()
	tool := &meetingagent.NoteSearcher{
		Src:    staticNoteSource{items: []notes.Note{n}},
		UserID: "u", WorkspaceID: "ws",
	}
	out, err := tool.Execute(context.Background(), []byte(`{"query":"`+evidenceKeyword+`"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}

// TestNoteRefEvidenceMayNotContainTheMatchedKeyword 钉住第四层：
// **命中位置与证据位置可以不一致**。
func TestNoteRefEvidenceMayNotContainTheMatchedKeyword(t *testing.T) {
	// ★ 唯一变量 = 查询词在正文里的位置。
	//   臂 A 在第 15 字（tailSentence 内），臂 B 在第 300 字之后。
	noteA := storedQuickNote(t, tailSentence+evidenceFillerOfLength(t, 400))
	noteB := storedQuickNote(t, evidenceFillerOfLength(t, 300)+tailSentence)

	// ── 前提自证：两臂都**必须命中**。
	//    否则下面「证据里没有词」可能只是「压根没搜到」，
	//    那是 §333 的第三层，不是这一层 —— 两者必须分开判。
	outA, outB := searchOne(t, noteA), searchOne(t, noteB)
	for _, arm := range []struct {
		name string
		out  string
	}{{"A-词在第15字", outA}, {"B-词在第315字", outB}} {
		if strings.Contains(arm.out, "没有找到") {
			t.Fatalf("臂 %s 没命中 ⇒ 本测试量的是 §333 第三层不是第四层：%s", arm.name, arm.out)
		}
		t.Logf("臂 %s：命中（前提成立）", arm.name)
	}

	// ── 前提自证：整段输出一定含查询词（标题行带的）。
	//    没有这条，读者无法判断下面「证据行不含词」是判别力还是巧合。
	if !strings.Contains(outB, evidenceKeyword) {
		t.Fatalf("整段输出都不含查询词 ⇒ renderResults 的标题行不再印查询词，"+
			"evidenceLines 的假设变了，须重新核对：%s", outB)
	}
	t.Logf("前提自证：全文 Contains 命中（恒真，标题行带的）⇒ 必须看证据行才有判别力")

	// ── 正题：臂 B 命中了，但证据里**没有那个词**。
	//
	// ⚠ 2026-10-09（§334.3）：这里原来写成「证据行含词 ⇒ Fatal」。
	//   方向反了 —— 证据窗口一旦被抬高（W1 变异 160→5000），
	//   第四层**真的消失了**，判据却报红。
	//   缺陷不复现 ≠ 判据失效；两者同形，但**判据的可信度由变异台保证**，
	//   不该由被测测试自己用 Fatal 兜。
	if strings.Contains(evidenceLines(outB), evidenceKeyword) {
		t.Logf("臂 B 证据行含词 ⇒ 第四层**已不成立**（证据窗口覆盖了命中位置）")
	} else {
		t.Errorf("臂 B 命中了「%s」，但回给模型的证据里**没有这个词**：%s\n"+
			"⇒ scoreNotes 用全文打分、renderResults 只回 160 rune，两把尺子不一致；\n"+
			"   模型被告知「找到 1 条相关笔记」，看到的却是一段与查询无关的文字",
			evidenceKeyword, evidenceLines(outB))
	}

	// ── 阴性对照：词在证据窗口内时，证据行里**必须**含这个词。
	//    没有它，上面那条就可能只是「证据一直是空的」这种平凡失效。
	if !strings.Contains(evidenceLines(outA), evidenceKeyword) {
		t.Fatalf("臂 A 证据窗口内的词也没出现 ⇒ 证据一直是空的，这条判据量的是别的东西：%s",
			evidenceLines(outA))
	}
	t.Logf("臂 A：命中且证据行含词（对照组成立）")
}
