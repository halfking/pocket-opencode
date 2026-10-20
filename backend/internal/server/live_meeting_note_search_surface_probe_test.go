// live_meeting_note_search_surface_probe_test.go —
// 「会议内容能不能被 search_notes 搜到」的**可判定**边界。
//
// 缺陷/限制
// ----------
// §939 起 ingestMeetingNote 在 `server_meeting_note_ingest.go`（原先在
// `server_meeting_ingest.go`）里只填 **Snippet**：
//
//	Snippet: truncateStr(content, 500),        // content 是精校稿全文
//
// 没有填 `Content`。而 `notes.Store.Upsert`（store.go:108-110）对 content 列是
// NOT NULL，回落 `content = n.Snippet` ⇒ **落库的 content 只有精校稿的前 500 字**。
// `NoteSearcher.Execute` 又用 `BodyForLLM(0)` 取正文（note.go:57-61，Content 优先、
// 空则回落 Snippet）⇒ 检索匹配的**只有那 500 字**。
//
// ⚠ 这是 §332.3 那条规律的第三层：
//
//	第 1 层（作用域恒空，§330 已修）会恒返回零行，把这一层**完全挡住**；
//	第 2 层（只搜最近 200 条，§332.1）会再挡一层。
//	三层叠起来，「跨会议检索」这件事**从来没有真正生效过**。
//
// 为什么这个夹具不是又一个「忽略条件的替身」（§330.3 的教训）
// ----------------------------------------------------------
// 夹具里的 `Snippet` 值**由生产用的 truncateStr 现算**，不是手抄的 500。
// 这样「上限改了」时本测试会跟着变，而不是钉死一个数字。
package server

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// meetingSnippetBudgetFiles 是「尺子可能在哪个文件里」的候选表。
//
// ⚠ §939：ingestMeetingNote 已搬到 server_meeting_note_ingest.go ⇒
// 尺子那一行也跟着搬了 ⇒ ⇒ ⇒ **指向**必须跟着改。
// ⚠ 只改指向，**不动语义**：门禁量的仍然是「落库尺子之外的正文不可检索」
// 这个**机制**，不是「500」这个数字（§333.7 的原意）。
//
// ⚠ 为什么是「候选表 + 恰好命中一个」而不是「写死一个文件名」：
// 尺子的**单一真源**必须是生产那一行。若那一行被复制到第二个文件，
// 两份会各自漂移 ⇒ 判据就钉不住「唯一的真源」⇒ 必须当场 Fatal。
var meetingSnippetBudgetFiles = []string{
	"server_meeting_note_ingest.go",
	"server_meeting_ingest.go",
}

// meetingSnippetBudget 从**生产源码**读出 ingestMeetingNote 传给 truncateStr 的上限。
//
// ⚠ 2026-10-09（§333.7）：第一版把 500 **在夹具里另写了一遍**
// （`storeRulerRunes` 量尺子一处、`storedMeetingNote` 造笔记一处）。
// 结果变异台把上限从 500 改成 900 时**只改了一处**，
// 量具与被测对象当场自相矛盾（尺子仍量出 166，实际落库已变成 296）⇒
// 「上限改大」这条变异报了红，而它本该绿 ——
// **读出来的是量具的错，不是判据的错**（§「两条读数打架先怀疑量具」）。
//
// ⇒ 尺子的**单一真源**必须是生产那一行。抽不出来就 Fatal，
//
//	绝不静默退回一个写死的 500（那等于把漂移重新引入）。
func meetingSnippetBudget(t *testing.T) int {
	t.Helper()
	re := regexp.MustCompile(`Snippet:\s*truncateStr\(content,\s*(\d+)\)`)
	var found []string
	budget := 0
	for _, name := range meetingSnippetBudgetFiles {
		src, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		m := re.FindSubmatch(src)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(string(m[1]))
		if err != nil || n <= 0 {
			t.Fatalf("%s 里读出的上限 %q 不是正整数", name, m[1])
		}
		found = append(found, name)
		budget = n
	}
	switch len(found) {
	case 1:
		return budget
	case 0:
		t.Fatalf("在 %v 里都找不到 `Snippet: truncateStr(content, N)` —— "+
			"ingestMeetingNote 的落库形状变了，本夹具复刻的是旧行为，须同步改写",
			meetingSnippetBudgetFiles)
	default:
		t.Fatalf("尺子同时出现在 %v ⇒ 有两份会各自漂移的真源（须只留一份）", found)
	}
	// ⚠ 上面每个分支都以 Fatal 结束（Fatal 会 runtime.Goexit），
	//   但编译器不认识 Fatal ⇒ 必须补一条兜底返回。
	return 0
}

// storedMeetingNote 复刻 ingestMeetingNote 落库后的形状：
// 只有 Snippet，Snippet = 生产同一把尺子量出来的前 N 字。
//
// ⚠ 不设 Content —— 这正是生产里发生的事（Upsert 回落之后 DB 里 content 才等于它）。
func storedMeetingNote(t *testing.T, full string) notes.Note {
	t.Helper()
	return notes.Note{
		ID:      "note_from_meeting",
		Title:   "客户评审会",
		Snippet: truncateStr(full, meetingSnippetBudget(t)),
		Domain:  "work",
	}
}

// 固定夹具：关键词句与填充句。
const (
	headFiller   = "会议首先回顾了上季度的工作进展与后续安排情况，" // 23 字
	tailKeyword  = "预算八十万"
	tailSentence = "张伟在最后补充，本次评审会的预算八十万由他直接向财务申请。"
	headKeyword  = "排期"
)

// tailKeywordOffset = tailSentence 里关键词**前面**有多少字。
//
// ⚠ 2026-10-09（§333.7）：第一版把关键词当成在 tailSentence 开头，
// 于是「放在第 N 字」实际落在第 N+14 字 —— 前提自证当场打回
// （「关键词落在第 97 字，声称第 83 字」）。这与实现无关，是夹具自己编了位置。
var tailKeywordOffset = len([]rune(tailSentence[:strings.Index(tailSentence, tailKeyword)]))

// fillerOfLength 造恰好 n 个汉字的填充文本。
func fillerOfLength(t *testing.T, n int) string {
	t.Helper()
	var sb []rune
	for len(sb) < n {
		sb = append(sb, []rune(headFiller)...)
	}
	out := string(sb[:n])
	// ★ 前提自证：填充文本里不能混进任何关键词，
	//   否则「关键词落在第 N 字」这句话本身就是假的。
	for _, kw := range []string{tailKeyword, headKeyword} {
		if strings.Contains(out, kw) {
			t.Fatalf("填充文本混进了关键词 %q（n=%d）⇒ 夹具失效", kw, n)
		}
	}
	return out
}

// storeRulerRunes 用**生产同一把尺子**量「落库后正文还剩多少字」。
//
// ⚠ 2026-10-09（§333.7）：原来的夹具把关键词手写在「第 900 字」，
// 于是判据实际钉死的是**「上限恰好是 500」这个数字** —— 上限一改，
// 夹具与真实上限就对不上，测试要么假红要么假绿。
// 改成由尺子现算后：上限是几，关键词就落在尺子外几字，
// **判据钉的是「尺子之外不可检索」这个机制，不是某个数字**。
func storeRulerRunes(t *testing.T) int {
	t.Helper()
	return len([]rune(truncateStr(fillerOfLength(t, 4000), meetingSnippetBudget(t))))
}

// transcriptWithKeywordAt 造一条精校稿，把关键词放在**第 atRunes 字**处。
func transcriptWithKeywordAt(t *testing.T, atRunes int) string {
	t.Helper()
	fill := atRunes - tailKeywordOffset
	if fill < 0 {
		t.Fatalf("关键词位置 %d 小于它在句子里的偏移 %d，造不出这种稿子", atRunes, tailKeywordOffset)
	}
	full := fillerOfLength(t, fill) + tailSentence
	// ★ 前提自证：关键词真的落在第 atRunes 字处 —— 不断言的话，
	//   下面的判据可能在量一个根本没造出来的场景。
	//   （注意：strings.Index 返回**字节**下标，要换算成字下标再比。）
	if got := len([]rune(full[:strings.Index(full, tailKeyword)])); got != atRunes {
		t.Fatalf("夹具内部矛盾：关键词落在第 %d 字，声称第 %d 字", got, atRunes)
	}
	return full
}

// TestMeetingNoteSearchSurfaceIsTruncatedToSnippet 钉住 §333 的结论：
// 会议笔记落库只留 Snippet ⇒ **落库尺子之外的正文不可检索**。
//
// ⚠ 2026-10-09（§333.7）本测试被重写过一次，两个洞：
//
//	① 尾部那条只有 t.Logf ⇒ 本文件唯一会失败的断言是头部对照，
//	   把 `truncateStr(content, 500)` 改成 5000 照样全绿；
//	② 关键词手写在「第 900 字」⇒ 判据钉的是**数字 500**而不是机制。
//
// 现在：两条都硬断言，且关键词位置由**生产尺子现算**（ruler±偏移）。
// ⇒ 唯一变量是「关键词在尺子内还是尺子外」。
func TestMeetingNoteSearchSurfaceIsTruncatedToSnippet(t *testing.T) {
	ruler := storeRulerRunes(t)
	// ★ 前提自证：尺子必须真的存在、且真的在截断。
	//   若生产不再截断，ruler 会 == 4000，下面两条断言都没有判别力。
	if ruler <= 0 || ruler >= 4000 {
		t.Fatalf("落库尺子不存在或已失效（ruler=%d）⇒ 本测试失去判别力", ruler)
	}

	search := func(note notes.Note, q string) string {
		tool := &meetingagent.NoteSearcher{
			Src:    staticNoteSource{items: []notes.Note{note}},
			UserID: "u", WorkspaceID: "ws",
		}
		out, err := tool.Execute(context.Background(), []byte(`{"query":"`+q+`"}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return out
	}

	// 臂 A：关键词落在尺子**之内**（第 ruler/2 字）⇒ 必须搜得到。
	//   它同时是阴性对照：证明「臂 B 搜不到」不是因为工具坏了或作用域错了。
	noteA := storedMeetingNote(t, transcriptWithKeywordAt(t, ruler/2))
	if out := search(noteA, tailKeyword); strings.Contains(out, "没有找到") {
		t.Fatalf("尺子内的关键词（落库 %d 字，第 %d 字处）都搜不到 ⇒ 这条测试量的是别的东西：%s",
			len([]rune(noteA.Snippet)), ruler/2, out)
	} else {
		t.Logf("臂 A 尺子内（第 %d 字 / 落库 %d 字）：命中（对照组成立）",
			ruler/2, len([]rune(noteA.Snippet)))
	}

	// 臂 B：关键词落在尺子**之外**（第 ruler+20 字）⇒ 必须搜不到。
	noteB := storedMeetingNote(t, transcriptWithKeywordAt(t, ruler+20))
	if strings.Contains(noteB.Snippet, tailKeyword) {
		t.Fatalf("夹具没造出截断场景：%q 已在 snippet（%d 字）里，本测试失去判别力",
			tailKeyword, len([]rune(noteB.Snippet)))
	}
	out := search(noteB, tailKeyword)
	if !strings.Contains(out, "没有找到") {
		t.Errorf("臂 B 尺子外（第 %d 字 / 落库 %d 字）的关键词竟然搜到了：%s\n"+
			"⇒ 可检索面已不再截断在 snippet；本判据钉的是旧行为，须随 §333.5 修法改写",
			ruler+20, len([]rune(noteB.Snippet)), out)
		return
	}
	t.Logf("臂 B 尺子外（第 %d 字 / 落库 %d 字）：搜不到 ⇒ 会议笔记可检索面 = %d 字",
		ruler+20, len([]rune(noteB.Snippet)), len([]rune(noteB.Snippet)))
}

// staticNoteSource 按作用域返回固定集合。
type staticNoteSource struct{ items []notes.Note }

func (s staticNoteSource) ListScoped(_ context.Context, userID, workspaceID, _ string) ([]notes.Note, error) {
	if userID != "u" || workspaceID != "ws" {
		return nil, nil
	}
	return s.items, nil
}
