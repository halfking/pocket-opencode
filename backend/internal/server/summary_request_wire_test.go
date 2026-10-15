// summary_request_wire_test.go — §92：**摘要端点**的跨语言请求字段对账。
//
// 背景。§90 给精校端点补了 `segments` / `target_langs` 的契约门（refine-meta-parity.test.mjs），
// §85 给精校补了 `meta` 三字段。**摘要端点一个都没有** —— §66.4 只钉了「两份摘要提示词不许漂」，
// 请求字段本身是裸的。而 §89 已经证过一遍这个形状：交接点上**没门**的字段，
// 换个写法两边都不会报错。
//
// ★ 本门与 §85 那道的关键区别：它不靠「两边各抄一份字段清单、靠注释提醒同步」，
//
//	而是**直接把前端源码当数据源** —— 读 frontend/src/api/meetings.ts，
//	抠出 `summarize()` / `refine()` 实际 JSON.stringify 出去的键、以及
//	`toApiSegments()` 实际吐的键，再与 Go 的 json tag 逐条对账。
//	于是「清单」只有一份（在代码里），抄写漂移这件事从结构上消失了。
//
// ⚠ 读前端源码必须**先剥注释**（§84 纪律）：meetings.ts 的说明性注释里
//
//	大量出现 `action_items[].due`、`target_langs` 这类字面量，不剥就会把
//	「Go 那份没有」这类判据喂绿。
//
// ⚠ 本文件只负责 **Go 这一半**。「前端真的发出去了吗」（而不是源码里写着）
//
//	由 frontend/src/api/__tests__/summary-request-parity.test.mjs 真跑一遍打桩 fetch 负责。
package server

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const frontendMeetingsAPI = "../../../frontend/src/api/meetings.ts"

// ── 前端源码解析工具 ────────────────────────────────────────────────

// blankOut 用**等长空白**替换命中片段 —— 不能删，删了会让行号与偏移错位，
// 而本门后面还要按 `{` 配平取函数体，偏移一错就取到别的函数。
func blankOut(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return '\n'
			}
			return ' '
		}, m)
	})
}

// stripTSComments 剥掉 TS/JS 注释。块注释先整段处理，行注释再逐行处理。
func stripTSComments(t *testing.T, s string) string {
	t.Helper()
	out := blankOut(regexp.MustCompile(`(?s)/\*.*?\*/`), s)
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "//"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// matchedBraces 从 open（指向 '{'）开始按括号配平取到配平的 '}'，含两端。
//
// ⚠ 必须按**括号配平**取，不能用正则 `\{[^}]*\}` —— 换行、尾逗号、
//
//	嵌套对象都能骗过正则（§84 纪律）。
func matchedBraces(t *testing.T, s string, open int) string {
	t.Helper()
	if open < 0 || open >= len(s) || s[open] != '{' {
		t.Fatalf("偏移 %d 处不是 '{'，无法取函数体", open)
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[open : i+1]
			}
		}
	}
	t.Fatal("括号不配平")
	return ""
}

// paramList 取锚点处那一对圆括号里的**形参列表原文**（不含圆括号本身）。
//
// ⚠ 单独存在是因为「签名」与「函数体」是两段文本：`meta?: { title?: … }` 在签名里，
//
//	而 `JSON.stringify({ … })` 在函数体里 —— 两个都要抠，就得能分别取到。
func paramList(t *testing.T, src, anchor string) string {
	t.Helper()
	i := strings.Index(src, anchor)
	if i < 0 {
		t.Fatalf("前端源码里找不到锚点 %q —— 判据的前提失效（改它之前先确认这里还成不成立）", anchor)
	}
	rest := src[i:]
	open := strings.Index(rest, "(")
	if open < 0 {
		t.Fatalf("锚点 %q 后面没有形参列表", anchor)
	}
	depth := 0
	for j := open; j < len(rest); j++ {
		switch rest[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[open+1 : j]
			}
		}
	}
	t.Fatalf("锚点 %q 的形参列表括号不配平", anchor)
	return ""
}

// functionBody 取 `function name(` 或 `async name(` 起、括号配平的那段**函数体**。
//
// ⚠ 这里踩了两次坑，两个坑都不是「源码变了」造成的，是解析器自己的偏移算错：
//
//	① 不能找「锚点之后的第一个 `{`」—— 形参里就有对象字面
//	   （`meta?: { title?: … }`），第一个 `{` 是**形参**的括号。
//	② 偏移必须是**相对切片**的。传绝对偏移在负控样本上恰好也对
//	   （那批样本锚点在第 0 个字符），于是这个 bug 在负控里完全隐形，
//	   直到拿真源码才炸。⇒ 负控必须覆盖**非零起点**。
func functionBody(t *testing.T, src, anchor string) string {
	t.Helper()
	i := strings.Index(src, anchor)
	rest := src[i:]
	// 形参列表在 rest 里从第一个 '(' 到配平的 ')'；函数体紧跟其后。
	tail := rest[strings.Index(rest, "("):]
	open := strings.Index(tail, "(")
	depth := 0
	for j := open; j < len(tail); j++ {
		if tail[j] == '(' {
			depth++
		} else if tail[j] == ')' {
			depth--
			if depth == 0 {
				k := strings.Index(tail[j:], "{")
				if k < 0 {
					t.Fatalf("锚点 %q 的形参列表之后没有函数体", anchor)
				}
				return matchedBraces(t, tail, j+k)
			}
		}
	}
	t.Fatalf("锚点 %q 的形参列表括号不配平", anchor)
	return ""
}

// 对象字面量里的两种键：显式（`k: v` / `k?: T`）与简写（`k,`）。
//
// ⚠ 三个坑，都是实测踩出来的：
//
//	① 不能只认冒号 —— 前端 `JSON.stringify({ …, meta })` 的第三个键是**简写**，
//	  没有冒号，漏了它就恰好放过 meta 这个最容易丢的字段。
//	② 不能把 `,` 直接当键终止符 —— `prev_summary: prevSummary,` 会把**值**
//	  `prevSummary` 也当成一个键，对账于是报一个根本不存在的字段。
//	  所以拆成两条：显式键要求冒号/问号后**紧跟一个标识符**；
//	  简写键要求左右两侧紧贴 `{`/`,` 与 `,`/`}`。
//	③ TypeScript 的可选属性是 `k?: T`，不是 `k? = v`。第一版把可选属性写成
//	  `\?=` —— 于是**带类型标注的形参一个键都抠不出来**
//	  （`meta?: { title?: string; participants?: string[] }` 全军覆没）。
//	  ⇒ 负控样本必须与真源码**同形**（含类型标注），否则解析器的形态错误在负控里隐形。
var (
	explicitKeyRe  = regexp.MustCompile(`(^|[^\w.$])([A-Za-z_][A-Za-z0-9_]*)(\s*:\s*[A-Za-z_]|\s*\?:\s*[A-Za-z_])`)
	shorthandKeyRe = regexp.MustCompile(`([{,])\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)
)

// objectKeys 抽一段对象字面量文本里的全部键，按出现顺序，重复即失败。
func objectKeys(t *testing.T, what, src string) []string {
	t.Helper()
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	for _, m := range explicitKeyRe.FindAllStringSubmatch(src, -1) {
		add(m[2])
	}
	for _, m := range shorthandKeyRe.FindAllStringSubmatch(src, -1) {
		add(m[2])
	}
	if len(keys) == 0 {
		t.Fatalf("%s 里一个键都没抠出来 —— 解析器坏了，不是字段没了（会假绿）", what)
	}
	return keys
}

func requireSameKeys(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := strings.Join(got, ","), strings.Join(want, ",")
	if g != w {
		t.Errorf("%s\n  前端发出：%s\n  Go 侧期望：%s\n  ⇒ 两端字段名已经漂了，网关会把值静默丢成零值", what, g, w)
	}
}

// jsonTags 按声明顺序返回结构体的 json tag 列表。
func jsonTags(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" {
			t.Fatalf("%s.%s 没有 json tag", rt.Name(), f.Name)
		}
		out = append(out, tag)
	}
	return out
}

func frontendSource(t *testing.T) string {
	t.Helper()
	return stripTSComments(t, readFrontend(t, frontendMeetingsAPI))
}

// ── 门 1：顶层请求字段 ─────────────────────────────────────────────

// TestMeetingRequestTopLevelFieldsMatchTheFrontend 把「前端 stringify 出去的键」
// 与「Go 解码的 tag」逐条钉在一起。
//
// 负控见 TestMeetingRequestFieldsHaveTeeth：任一侧改名、增删，本条立刻转红。
func TestMeetingRequestTopLevelFieldsMatchTheFrontend(t *testing.T) {
	src := frontendSource(t)

	// summarize() 的请求体：body: JSON.stringify({ … })
	summarizeBody := functionBody(t, src, "async summarize(")
	sumAt := strings.Index(summarizeBody, "JSON.stringify(")
	if sumAt < 0 {
		t.Fatal("summarize() 里找不到 JSON.stringify —— 前端改了发法，判据要跟着改")
	}
	sumKeys := objectKeys(t, "summarize() 请求体", matchedBraces(t, summarizeBody, sumAt+len("JSON.stringify(")))

	// refine() 的请求体
	refineBody := functionBody(t, src, "async refine(")
	refAt := strings.Index(refineBody, "JSON.stringify(")
	if refAt < 0 {
		t.Fatal("refine() 里找不到 JSON.stringify —— 前端改了发法，判据要跟着改")
	}
	refKeys := objectKeys(t, "refine() 请求体", matchedBraces(t, refineBody, refAt+len("JSON.stringify(")))

	requireSameKeys(t, "摘要端点顶层字段", sumKeys, jsonTags(t, meetingSummaryBody{}))
	requireSameKeys(t, "精校端点顶层字段", refKeys, jsonTags(t, meetingRefineBody{}))

	// ★ 两个 body 必须**不同**。它们曾经都是匿名结构体、字段长得几乎一样，
	//   一旦有人复制粘贴把 refine 写成 summary（或反过来），上面两条会同时绿 ——
	//   所以显式断言「摘要有 prev_summary、精校没有」这个区分点。
	if hasKey(sumKeys, "prev_summary") == hasKey(refKeys, "prev_summary") {
		t.Errorf("prev_summary 必须在摘要端点有、在精校端点没有；实际 摘要=%v 精校=%v", sumKeys, refKeys)
	}
	if !hasKey(sumKeys, "prev_summary") {
		t.Error("摘要端点没有 prev_summary ⇒ 滚动摘要会退化成首轮（§66.2 实测 action_items 归零）")
	}
	if !hasKey(refKeys, "target_langs") {
		t.Error("精校端点没有 target_langs ⇒ 翻译目标语言这条需求整条断掉")
	}
}

// ── 门 2：segment 与 meta 的子字段 ─────────────────────────────────

// TestMeetingSegmentFieldsMatchTheFrontend 钉 `toApiSegments()` 实际吐的五个键。
//
// ⚠ 这五个字段在**摘要链**上其实只有两个被用（segmentsToText 只读 Speaker/Text，
//
//	§91.5 有登记）。本门守的是**线上缆**那一层：前端改名或删字段时，
//	Go 的 meetingSegmentIn 会静默收到零值，而精校链的时间感知去重（§83）
//	依赖 start_ms/end_ms —— 那时是「重复内容盖不住」，不报任何错。
func TestMeetingSegmentFieldsMatchTheFrontend(t *testing.T) {
	src := frontendSource(t)
	fn := functionBody(t, src, "function toApiSegments(")
	got := objectKeys(t, "toApiSegments() 吐出的键", fn)
	want := jsonTags(t, meetingSegmentIn{})
	requireSameKeys(t, "segment 子字段", got, want)
}

// TestMeetingMetaFieldsMatchTheFrontend 钉 meta 的三个字段名。
//
// meta 在**精校链**上承重（术语表，§64/§80/§81 实测「悬借→悬界 12/12」靠它）；
// 在**摘要链**上目前只有 kxmemory 分支用得上（而 kxmemory 生产上 404）。
func TestMeetingMetaFieldsMatchTheFrontend(t *testing.T) {
	src := frontendSource(t)
	// meta 的形参声明在**签名**里，不在函数体里：
	//   meta?: { title?: string; participants?: string[]; location?: string }
	//
	// ⚠ summarize 与 refine **各声明了一个** meta?。而「它们各自声明了哪些字段」
	//   正是这道门要回答的问题 —— 只查其中一个就漏掉了另一半的漂移。
	for _, anchor := range []string{"async summarize(", "async refine("} {
		params := paramList(t, src, anchor)
		idx := strings.Index(params, "meta?:")
		if idx < 0 {
			t.Fatalf("%s 的签名里没有 meta 形参 —— 判据的前提失效（实际签名：%s）", anchor, strings.TrimSpace(params))
		}
		brace := strings.Index(params[idx:], "{")
		if brace < 0 {
			t.Fatalf("%s 的 meta? 后面没有类型标注，判据跟不动（实际：%s）", anchor, params[idx:])
		}
		got := objectKeys(t, anchor+" 的 meta 形参声明", matchedBraces(t, params, idx+brace))
		requireSameKeys(t, anchor+" 的 meta 子字段", got, jsonTags(t, meetingMetaIn{}))
	}
}

// ── 门 3：负控（证明上面两条有牙） ─────────────────────────────────

// TestMeetingRequestFieldsHaveTeeth 把同一套抠取器喂几份**故意违规**的前端源码。
//
// ⚠ 这些样本判的是**解析器**，不是生产代码：若哪天解析器改成认不出违规形态，
//
//	它们会红 —— 那是有意的信号（说明该跟进解析器），不是回归。
func TestMeetingRequestFieldsHaveTeeth(t *testing.T) {
	// ⚠ pad 是**故意加的**：锚点不落在文件开头。
	//   解析器第一版把绝对偏移传给了已被切片的字符串，而在 pad=0 的样本上
	//   绝对偏移与相对偏移**恰好相等** ⇒ 这个 bug 在负控里完全隐形，
	//   直到拿真源码（锚点在第 2600 个字符）才炸。
	//   「负控必须在与真实使用方式相同的条件下跑」，否则验的是另一个分支。
	const pad = `import x from 'y'
const unrelated = { a: 1, b: 2 }
`
	cases := []struct {
		name, src, anchor, want string
		keys                    []string
	}{
		{
			name:   "顶层键改名",
			anchor: "async summarize(",
			src:    `async summarize(id){ body: JSON.stringify({ segments: a, prevSummary: p, meta: m }) }`,
			want:   "摘要端点顶层字段",
		},
		{
			name:   "少一个字段",
			anchor: "async summarize(",
			src:    `async summarize(id){ body: JSON.stringify({ segments: a, meta: m }) }`,
			want:   "摘要端点顶层字段",
		},
		{
			name:   "简写 meta 改成别的名字",
			anchor: "async summarize(",
			src:    `async summarize(id){ body: JSON.stringify({ segments: a, prev_summary: p, meta2: m }) }`,
			want:   "摘要端点顶层字段",
		},
		{
			name:   "segment 字段改名",
			anchor: "function toApiSegments(",
			src:    `function toApiSegments(s){ return s.map(x => ({ speaker: x.a, text: x.t, language: x.l, start_ms: x.s, end_ms: x.e })) }`,
			want:   "segment 子字段",
		},
		{
			name:   "segment 少一个字段",
			anchor: "function toApiSegments(",
			src:    `function toApiSegments(s){ return s.map(x => ({ speaker: x.a, text: x.t, lang: x.l, start_ms: x.s })) }`,
			want:   "segment 子字段",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := functionBody(t, pad+c.src, c.anchor)
			var got []string
			if strings.Contains(body, "JSON.stringify(") {
				at := strings.Index(body, "JSON.stringify(")
				got = objectKeys(t, c.name, matchedBraces(t, body, at+len("JSON.stringify(")))
			} else {
				got = objectKeys(t, c.name, body)
			}
			// 判据真的该红：拿它去和「正确的 Go 侧清单」比，必须不等。
			want := jsonTags(t, meetingSegmentIn{})
			if c.want == "摘要端点顶层字段" {
				want = jsonTags(t, meetingSummaryBody{})
			}
			if strings.Join(got, ",") == strings.Join(want, ",") {
				t.Fatalf("样本本身是合规的，负控失效：%v", got)
			}
			t.Logf("违规样本抠出 %v，与合规清单 %v 不同 ⇒ 门 1/2 会转红", got, want)
		})
	}

	// ★ meta 走的是**另一条**解析路径（形参列表 + TS 可选属性 `?:`），
	//   与上面五条（函数体 / 对象字面）不同形 —— 所以必须单独喂，
	//   且样本必须**带类型标注**。第一版负控没覆盖这条路径，
	//   结果「可选属性写成 `\?=`」这个解析器 bug 在负控里完全隐形。
	t.Run("meta 字段改名（带类型标注，与真源码同形）", func(t *testing.T) {
		params := paramList(t, pad+`async summarize(
    id: string,
    meta?: { title?: string, participants?: string[], venue?: string },
    signal?: AbortSignal,
  ): Promise<X> {
    return meta
  }`, "async summarize(")
		idx := strings.Index(params, "meta?:")
		if idx < 0 {
			t.Fatalf("样本里没有 meta 形参，负控失效（实际：%s）", params)
		}
		brace := strings.Index(params[idx:], "{")
		got := objectKeys(t, "meta 样本", matchedBraces(t, params, idx+brace))
		want := jsonTags(t, meetingMetaIn{})
		if strings.Join(got, ",") == strings.Join(want, ",") {
			t.Fatalf("样本本身是合规的，负控失效：%v", got)
		}
		t.Logf("违规样本抠出 %v，与合规清单 %v 不同 ⇒ TestMeetingMetaFieldsMatchTheFrontend 会转红", got, want)
	})
}

// ── 现状登记（**不是门**） ──────────────────────────────────────────

// TestMeetingSummaryMetaConsumptionStatus 只做读数，不判定成败。
//
// ★ 为什么它不是门：摘要链要不要把 meta 当术语表喂给 LLM，是**产品取舍**
//
//	（会改变全部摘要输出、增加 prompt 长度与延迟，而 §66 已经发现现役 glm-5.2
//	光是空跑就跑出 52s/57.5s，预算只有 45s）。本仓无权替用户拍这个板，
//	所以**不**把它写成承重门 —— 那等于把一个未决决定固化成规范。
//	这里只把现状读出来，让缺口在 `go test -v` 里可见。
//
//	现状（§91.3 实测）：meta 在**两条 LLM 路径**上都没进模型输入 ——
//	  · agent 路径  meetingSummaryViaAgent(agentCtx, r, body.Segments, body.PrevSummary)
//	  · 一次性回落  llmMeetingSummary(ctx, r, body.Segments, body.PrevSummary)
//	只有 kxmemory 分支读了 Meta.Title / Meta.Participants，且**连 Location 都没传**；
//	而 kxmemory 在生产配置下恒 404 ⇒ 生产上 meta 是彻底的死参数。
//
//	⇒ 待拍板。拍板后请**删掉本函数**并改写为「meta 必须出现在摘要提示词里」的承重断言。
func TestMeetingSummaryMetaConsumptionStatus(t *testing.T) {
	raw := readFrontend(t, "server_meeting.go")
	body := goFuncBody(t, raw, "func (s *Server) handleMeetingSummary(")

	for _, call := range []string{
		"s.meetingSummaryViaAgent(",
		"s.llmMeetingSummary(",
	} {
		i := strings.Index(body, call)
		if i < 0 {
			t.Fatalf("handleMeetingSummary 里找不到 %s —— handler 被重写了，本登记失效，请重读现状", call)
		}
		args := callArgs(body[i:], call)
		if strings.Contains(args, "Meta") {
			t.Logf("★ %s 已经把 meta 传下去了（%s）—— 本登记已过期，请删掉它并改成承重断言", call, args)
		} else {
			t.Logf("现状：%s 的实参是 %s —— meta 没有进入摘要链的模型输入", call, strings.TrimSpace(args))
		}
	}

	kx := strings.Index(body, "kxmemory.MeetingMeta{")
	if kx < 0 {
		t.Log("kxmemory 分支不见了（部署里已不装配？）—— 本登记的现状描述需更新")
		return
	}
	meta := matchedBraces(t, body, kx+len("kxmemory.MeetingMeta"))
	if !strings.Contains(meta, "Location") {
		t.Logf("现状：kxmemory 分支只传 %s，Location 连它也没传", strings.TrimSpace(meta))
	}
}

// callArgs 取一次调用的实参列表（含右括号），按括号配平。
func callArgs(from, call string) string {
	open := strings.Index(from, call) + len(call) - 1
	depth := 0
	for i := open; i < len(from); i++ {
		switch from[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return from[open+1 : i]
			}
		}
	}
	return ""
}

func hasKey(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
