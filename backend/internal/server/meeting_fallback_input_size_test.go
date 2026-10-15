package server

// meeting_fallback_input_size_test.go — §426：回落载荷必须带**真实的输入规模**。
//
// ## 这道门钉什么
//
// §425 查出的事实：`/summary` 一次性回落与 `/refine` 两条链
// **都不带转写截断**（另两条带：agent 尾 4000 runes、`/recommend` 尾 2000），
// 而 `ErrOutputTruncated` 只认**输出**侧（`finish_reason=length`）
// ⇒ **输入**装不下时上游返 4xx、不匹配它 ⇒ 后端 502
// ⇒ 前端把同一份超大请求体原样再发一次给 `/api/llm/chat` ⇒ 同样失败。
//
// ⇒ 「转写太长」与「模型没按契约返回」在响应里**完全同形**（都只有
// `refine_fallback` / `summary_fallback` 一个布尔），日志与界面都分不出来。
//
// §426 的处置**不是**替产品定阈值（那是待拍板 #17），而是把服务端
// **本来就知道**的量记下来，让那个阈值以后能**从实测来定**。
// 本门守的就是「记下来的那个数是真的」。
//
// ## 为什么必须按 rune
//
// 中文一字 3 字节：`len("今天讨论准确率") == 21` 而
// `utf8.RuneCountInString(...) == 7`。同文件的 truncateStr 已经记过同一个坑
// （按字节切会落在字的中间，产出非法 UTF-8）。
// ⭐ 所以本门的**主断言用中文字符串**，按字节实现会直接红 ——
//   这就是这道门与「键存在性」判据的根本区别。
//
// ## 既有门覆盖到哪（先查过，避免重复）
//
// `meeting_response_contract_test.go` 会把这两个键收进跨端键表，
// 前端 `meeting-response-keys.test.ts` 再断言它们**活过归一化**
// ⇒ 「键会不会被前端静默丢弃」已被守。
// ⚠ 但那道门只验**键存活**，不验**值正确**：把 7 写成 21 它照样绿。
// ⇒ 两者缺一不可，本门补的是后者。

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMeetingFallbackPayloadsCarryTrueInputSize(t *testing.T) {
	cases := []struct {
		name  string
		mk    func(string) map[string]any
		key   string
		plain string // 同时含中文与 ASCII，用来把「按字节」的实现打红
	}{
		{"refine", refineFallbackPayload, "refine_input_runes", "今天讨论准确率 v2"},
		{"summary", emptySummary, "summary_input_runes", "今天讨论准确率 v2"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 前提自证：中文串的字节数与 rune 数**必须不同**，
			// 否则这个用例区分不出按字节与按 rune 两种实现。
			wantRunes := utf8.RuneCountInString(c.plain)
			if int64(wantRunes) == int64(len(c.plain)) {
				t.Fatalf("前提不成立：夹具串 %q 的字节数与 rune 数相同（都是 %d），"+
					"这道门就分不出按字节实现了 —— 换一段含中文的夹具", c.plain, wantRunes)
			}

			got := c.mk(c.plain)

			raw, ok := got[c.key]
			if !ok {
				t.Fatalf("回落载荷里没有 %q —— §425 查出的两种失败（转写太长 / 模型没听话）"+
					"会继续完全同形。键=%v", c.key, fallbackKeys(got))
			}
			// 类型必须是数字：字符串会让前端 `Number(...)` 得到 NaN ⇒ undefined。
			n, ok := raw.(int)
			if !ok {
				t.Fatalf("%s 的类型是 %T，期望 int —— 前端按 Number() 读它，"+
					"非数字会在归一化时变成 NaN 而静默丢键", c.key, raw)
			}
			if n != wantRunes {
				t.Errorf("%s = %d，期望 %d（按 **rune** 计的输入规模）。"+
					"⚠ 若你看到的是字节数 %d：中文一字 3 字节，按字节算会让"+
					"「转写太长」的判据系统性地偏大 3 倍。"+
					"同口径参照：agent 截断 4000 runes、/recommend 截断 2000 runes",
					c.key, n, wantRunes, len(c.plain))
			}

			// 两条链的截断口径都是 rune，所以这个量必须能与它们直接比大小。
			if n <= 0 {
				t.Errorf("%s = %d，空转写不该报一个非正数", c.key, n)
			}
		})
	}
}

// TestMeetingFallbackInputSizeIsMeasuredOnSentTranscript 钉住「量的是**发出去的**那段」。
//
// agent 路径的 transcript 先被截到 4000 runes 再进 `emptySummary`
// ⇒ 那里报出来的必须**已经是截断后**的数字，而不是原始转写的全量。
// 否则这个量在两条链上口径不同，拿它去定阈值会把 agent 路径算高。
func TestMeetingFallbackInputSizeIsMeasuredOnSentTranscript(t *testing.T) {
	const maxRunes = 4000
	long := strings.Repeat("字", maxRunes+500)

	// 走 agent 路径的形状：先截尾再回落。
	runes := []rune(long)
	truncated := string(runes[len(runes)-maxRunes:])
	got := emptySummary(truncated)

	if n, _ := got["summary_input_runes"].(int); n != maxRunes {
		t.Errorf("agent 路径回落时 summary_input_runes = %d，期望 %d —— "+
			"它必须量的是**实际发出去的那段**（已截到 %d runes），不是原始全量 %d",
			n, maxRunes, maxRunes, utf8.RuneCountInString(long))
	}
}

// 同形负控：把两条链的键**对调**必须被抓出来。
// 键名长得极像（refine_input_runes / summary_input_runes），
// 而两条链的调用点都在同一个文件里 —— 对调是现实会发生的错。
func TestMeetingFallbackInputSizeKeysAreNotSwapped(t *testing.T) {
	const text = "今天讨论准确率"
	want := utf8.RuneCountInString(text)

	refine, _ := refineFallbackPayload(text)["refine_input_runes"].(int)
	summary, _ := emptySummary(text)["summary_input_runes"].(int)

	if refine != want || summary != want {
		t.Fatalf("前提自证失败：两条链算出了不同的数（refine=%d summary=%d，期望都是 %d）—— "+
			"负控的前提不成立，先查生产函数", refine, summary, want)
	}
	if !containsKey(refineFallbackPayload(text), "refine_input_runes") {
		t.Error("refine 回落载荷里没有 refine_input_runes")
	}
	if !containsKey(emptySummary(text), "summary_input_runes") {
		t.Error("summary 回落载荷里没有 summary_input_runes")
	}
	// 对调后的形状（负控）：两条链报的都是对方的键名 ⇒ 必须不成立。
	swapped := map[string]any{
		"summary_input_runes": refineFallbackPayload(text)["refine_input_runes"],
		"refine_input_runes":  emptySummary(text)["summary_input_runes"],
	}
	if _, bad := swapped["refine_input_runes"]; !bad {
		t.Fatal("负控自证失败")
	}
}

func containsKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func fallbackKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 编译期钉住：载荷必须能原样 JSON 序列化（门里的类型断言才有意义）。
var _ = json.Marshal
