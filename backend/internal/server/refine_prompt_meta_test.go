// refine_prompt_meta_test.go — 门禁：会议元数据必须**真的**拼进精校提示词。
//
// 2026-10-07（§31）。要修的是 §30.2 测出的分界线：
//
//	能修：拼写与正确词**接近**的（Tafad→Kafka、Challog→Changelog、档段→字段）
//	修不了：**毫不相干**的（页百零一→CI、接单→git、countedgettotal→calculateTotal）
//
// 后者没有任何语言模型邻域知识可用，**只有拿到名单/主题才有依据**。
// 而 meta.Title / meta.Participants 一直在请求体里（也转给了 kxmemory），
// 只是 LLM 兜底分支一个都没用 ⇒ 这是接线，不是新功能。
//
// ⚠ 这道门是**行为断言**，不是源码扫描。
//
//	判据从「源码里有没有这几个字」升级成「**拼出来的提示词里有没有这几项**」：
//	调用 buildRefinePrompt 拿返回值，直接在里面找。
//	§31 最危险的失败形态恰恰是源码扫描抓不到的那种 ——
//	变量名对、字面量在，但实参传错了（比如调用点传了 meetingMetaIn{}），
//	源码里「参会人：」四个字好好地在那儿，提示词里却一个字都没有。
//
// ⚠ 它抓不住模型**是否遵守**术语表（那是 env 门控的真网关探针
//
//	TestLiveGatewayRefineFixesASRErrors 的职责，含 §31 的两条负控）。
//	两层缺一不可。
package server

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 负控样本：名单里**没有**「张伟」，主题是产品评审周会。
// 若模型拿名单当词表去「对齐」，就会把正确的「张伟」当成听错。
var metaNegativeControl = meetingMetaIn{
	Title:        "产品评审周会",
	Participants: []string{"李娜", "王强", ""},
}

func TestRefinePromptCarriesMeetingMeta(t *testing.T) {
	const transcript = "[发言人] 张伟和李娜都会参加今天的评审。"
	p := buildRefinePrompt(transcript, "", metaNegativeControl)

	// 1) 主题必须进提示词，且带标签（不是散落的一个词）。
	if !strings.Contains(p, "产品评审周会") {
		t.Error("会议主题没进提示词 —— §31 的术语表少了一半")
	}
	if !strings.Contains(p, "会议主题：") {
		t.Error("主题没有标签，模型无从判断这一行是「可信上下文」而不是转写内容")
	}

	// 2) 每个参会人都在。
	for _, want := range []string{"李娜", "王强"} {
		if !strings.Contains(p, want) {
			t.Errorf("参会人 %q 没进提示词", want)
		}
	}
	if !strings.Contains(p, "参会人：") {
		t.Error("参会人没有标签")
	}

	// 3) ⚠ 名单里的空串不得漏进提示词。
	//
	// 用户手填的参会人列表里混空串是常态（前端新增参会人、清空某一项都会留一个）。
	// 直接 join 会拼出「李娜、王强、。」—— 那份「可信上下文」自己变脏，
	// 模型顺着脏上下文猜，正是 §31 注释里点名的那个风险。
	if strings.Contains(p, "、。") || strings.Contains(p, "、、") {
		t.Errorf("参会人列表把空项拼进去了：%q", termHintSnippet(p))
	}

	// 4) 转写本身必须在，且术语表在它**之前**（先读规则再读文本）。
	ti, mi := strings.Index(p, transcript), strings.Index(p, "会议主题：")
	if ti < 0 {
		t.Fatalf("转写没进提示词：%q", p)
	}
	if mi < 0 || mi > ti {
		t.Error("术语表应排在转写之前 —— 放在之后模型会当成「转写的一部分」")
	}

	// 5) ⚠ 反向禁令必须字面在提示词里。
	//
	// 只给词表不给禁令，等于**主动邀请**模型拿名单去替换原文里的词。
	// 负控样本测的就是这个：名单里有「李娜」没有「张伟」，
	// 模型若照着名单把「张伟」改掉，那就是 termHint 自己引入的误改 ——
	// 而 §30 定的原则是误改比漏修危险得多（用户发现不了）。
	//
	// ⚠ 锚点用「明显对应」「并不对应」这两个**别处不会出现的片段**，
	// 不能用「不要」：提示词规则 1 本来就写着「不要为了通顺而调整」，
	// 用「不要」当锚点，这道门在禁令被整段删掉之后**照样绿**
	//（这正是 §31 之前那版的洞，只是方向相反）。
	for _, guard := range []string{"明显对应", "并不对应"} {
		if !strings.Contains(p, guard) {
			t.Errorf("提示词缺少反向禁令片段 %q —— 只给词表不给禁令，"+
				"会诱导模型拿名单去改写正确的转写", guard)
		}
	}

	// ⚠ 第三条禁令：**实测逼出来的**，不是预防性设计。
	//
	// 配对实验量到：转写「张伟和李娜都会参加」（正确）配名单「赵敏、孙磊」，
	// 有元数据时模型输出「**赵敏和孙磊**都会参加」——
	// 它把转写里的人名**对齐**进了名单。
	//
	// 前两条禁令拦不住这个：模型认为「张伟→赵敏」是「对应的」
	//（赵敏确实在名单里），于是第一条禁令反而给了它依据。
	// ⇒ 禁令必须**点名人名**这个具体误解，不能只堵通例。
	for _, guard := range []string{"名单上没有的人名", "多半是正确的", "不要**把它换成名单里的另一个人名"} {
		if !strings.Contains(p, guard) {
			t.Errorf("提示词缺少「人名对齐」禁令片段 %q —— 实测会把正确的人名"+
				"换成名单里的另一个人名，而用户看不出来", guard)
		}
	}
}

// TestRefinePromptCarriesMeetingTopic —— §143：主题必须真的进提示词。
//
// 为什么单独一条而不并入 TestRefinePromptCarriesMeetingMeta：
// 那条断言的是「title/participants/location 在场」，而**主题被加进来时
// 最自然的写法就是忘了读它** —— meta 里有字段、提示词里没它，
// 跨语言契约门（refine_meta_wire_test.go / refine-meta-parity）
// **两侧照样全绿**，因为它们只验「线缆通不通」，不验「线缆接到了什么」。
//
// 本条是那一段的**终点断言**：从 meta 到提示词。
func TestRefinePromptCarriesMeetingTopic(t *testing.T) {
	const topic = "悬界芯片客户评审会"
	p := buildRefinePrompt("下周三跟客户过一遍。", "", meetingMetaIn{Topic: topic})
	if !strings.Contains(p, topic) {
		t.Errorf("提示词里没有主题 %q ⇒ 主题里的专有名词到不了精校\n%s", topic, termHintSnippet(p))
	}
	// ⚠ 这条钉的是一条**约定**，不是一条实测结论。
	//   原始理由写的是「两者共用『会议主题：』会让模型以为是同一条信息出现两次」——
	//   §146（2026-10-07）测过相邻的问题并**证伪了它更强的那个版本**
	//   （「产品名只能放主题」：Title 放产品名同样 3/3 修好），
	//   但**同标签是否有害从未被测过**。
	//   ⇒ 这里当约定钉住（防误改、防两侧漂），不当因果陈述。
	//   ⇒ 若哪天要动这条约定，先补一个「Topic 也用『会议主题：』」的对照臂。
	if strings.Contains(termHintSnippet(p), "会议主题："+topic) {
		t.Error("主题用了与 Title 相同的标签「会议主题：」⇒ 两个字段在模型眼里是同一条信息（约定，见上方更正）")
	}

	// 负控：空主题不得拼出空标签。
	if got := metaTermHint(meetingMetaIn{Topic: "   "}); strings.Contains(got, "会议议题") {
		t.Errorf("空白主题仍拼进了提示词：%q", got)
	}
	// 负控：与 Title 同值时也必须是两条独立条目。
	both := metaTermHint(meetingMetaIn{Title: topic, Topic: topic})
	if strings.Count(both, topic) != 2 {
		t.Errorf("title 与 topic 同值时只出现了一次（%q）⇒ 两个字段被合并成一条了", both)
	}
}

func TestRefinePromptOmitsMetaFrameWhenMetaEmpty(t *testing.T) {
	const transcript = "[发言人] 会议从三点开始。"
	p := buildRefinePrompt(transcript, "", meetingMetaIn{})

	// 探针的对照组（POCKET_REFINE_ARM=off）正是这条路径。
	// 若空元数据时还留着「以下信息是可信的上下文」的空框架，
	// 对照组就不是真对照 —— 它仍带着一句「这里本来该有上下文」的话。
	for _, leak := range []string{"可信的上下文", "会议主题：", "参会人："} {
		if strings.Contains(p, leak) {
			t.Errorf("无元数据时提示词里仍出现 %q —— 空框架会让对照组不干净", leak)
		}
	}
	if !strings.Contains(p, transcript) {
		t.Fatal("转写丢了")
	}
	// 空框架不得留下多余空行把提示词开头撑变形。
	if strings.HasPrefix(p, "\n") {
		t.Error("提示词以空行开头 —— langHint 与 termHint 都为空时应紧接正文")
	}
}

func TestRefinePromptKeepsLangHintAndMeta(t *testing.T) {
	p := buildRefinePrompt("[发言人] hello", "翻译目标语言：en。", meetingMetaIn{
		Title: "Overseas Sync", Participants: []string{"Amy"},
	})
	for _, want := range []string{"翻译目标语言：en。", "Overseas Sync", "Amy", "[发言人] hello"} {
		if !strings.Contains(p, want) {
			t.Errorf("提示词缺 %q —— 多语种会议也带元数据时四项必须同时在场", want)
		}
	}
	// ⚠ 顺序：langHint → termHint → 正文。
	// 两条 hint 都是「前缀说明」，正文必须在最后，否则模型会把 hint
	// 当成转写的一部分（termHint 自带换行尾，拼在正文后面不会分隔）。
	li, ti, bi := strings.Index(p, "翻译目标语言"), strings.Index(p, "会议主题："), strings.Index(p, "以下是会议录音")
	if !(li < ti && ti < bi) {
		t.Errorf("提示词三段顺序不对：langHint=%d termHint=%d 正文=%d（应为 0<1<2）", li, ti, bi)
	}
}

func TestNonEmptyFiltersBlankParticipants(t *testing.T) {
	got := nonEmpty([]string{"李娜", "", "   ", "王强"})
	if len(got) != 2 || got[0] != "李娜" || got[1] != "王强" {
		t.Errorf("nonEmpty 过滤结果 = %v，期望 [李娜 王强]", got)
	}
	if got := nonEmpty([]string{"", "  "}); len(got) != 0 {
		t.Errorf("全空输入应返回空切片，实际 %v", got)
	}
	// ⚠ 不得原地改调用方的切片：meta 是请求体里的结构，
	// 就地截断会让同一个 meta 在后续日志/落库处看到被改过的内容。
	in := []string{"李娜", "", "王强"}
	_ = nonEmpty(in)
	if len(in) != 3 {
		t.Errorf("nonEmpty 改动了调用方的切片，长度从 3 变成 %d", len(in))
	}
}

// TestHandleMeetingRefinePassesMetaThrough 钉住**调用点**。
//
// ⚠ 这道门是补洞补出来的，不是想出来的。
//
// 上面前三道门都直接调 buildRefinePrompt，于是它们**全都绕过了调用点** ——
// 而「§31 的上下文能不能到提示词」这件事真正会坏的地方就在那儿：
// 有人把 handleMeetingRefine 里的 body.Meta 改成 meetingMetaIn{}（或干脆删掉参数），
// 三道门**照样全绿**：字面量在、纯函数正确、termHint 拼得对，只是**没人把数据传进去**。
//
// 锚点取**实参**而不是整行源码：gofmt 可能把这条调用折行，
// 整行匹配一折行就假红，而假红比无门更坏（会让人去改对的地方）。
func TestHandleMeetingRefinePassesMetaThrough(t *testing.T) {
	raw, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatalf("读 server_meeting.go: %v", err)
	}
	src := stripGoComments(string(raw))

	call := callArgTail(src, "llmMeetingRefine(")
	if call == "" {
		t.Fatal("找不到 llmMeetingRefine 的调用点（只有函数声明？§31 的接线整段丢了）")
	}
	if !strings.HasSuffix(call, "body.Meta") {
		t.Errorf("llmMeetingRefine 的最后一个实参是 %q，期望 body.Meta —— "+
			"元数据到不了提示词，§31 的术语表等于没接", call)
	}
}

// callArgTail 找出 src 中 func 调用（不是声明）的实参列表，并去掉首尾括号。
func callArgTail(src, fn string) string {
	for i := 0; ; {
		j := strings.Index(src[i:], fn)
		if j < 0 {
			return ""
		}
		at := i + j
		// 跳过函数声明（声明左边是 "func "）与注释（已剥，仍可能有零星残留）。
		prefix := strings.TrimRight(src[:at], " \t")
		if strings.HasSuffix(prefix, "func") || strings.HasPrefix(prefix, "//") {
			i = at + len(fn)
			continue
		}
		depth, k := 1, at+len(fn)
		for ; k < len(src) && depth > 0; k++ {
			switch src[k] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			return "" // 括号不配对，源码有别的问题，让别的门去报
		}
		return strings.TrimSpace(src[at+len(fn) : k-1])
	}
}

// termHintSnippet 截出术语表那一段，只为报错信息可读。
func termHintSnippet(p string) string {
	i := strings.Index(p, "参会人：")
	if i < 0 {
		return p
	}
	if i+60 > len(p) {
		return p[i:]
	}
	return p[i : i+60]
}

// rosterLeak 的门。它是**负控的唯一判据**，判据自己有牙才谈得上结论。
func TestRosterLeakDetectsSubstitutionAndInsertion(t *testing.T) {
	meta := meetingMetaIn{Title: "Polaris 供应商对接会", Participants: []string{"客服组", "  ", "值班经理"}}

	cases := []struct {
		name, got, orig, want string
		m                     *meetingMetaIn // 非 nil 则覆盖上面的 meta
	}{
		{"干净输出", "[发言人] 张伟和李娜都会参加。", "张伟和李娜都会参加。", "", nil},
		{"插入：名单词冒进原文没有的位置", "[发言人] 问题请找值班经理。", "问题请找对应同事。", "值班经理", nil},
		{"替换：转写里的名字被换成名单里的人", "[发言人] 客服组负责发邮件。", "张伟负责发邮件。", "客服组", nil},
		// ⚠ 标题按**整串**匹配是刻意的保守设计：标题里那个有辨识度的词
		// （如「Polaris」）出现在输出里，通常是**正确的修复**而不是泄漏 ——
		// 目标组「音译专名」那条正是靠它修好的。若按词元匹配，
		// 那条成功会被自己的安全判据判成失败。
		// ⇒ 代价：只插入了标题某个片段的情况**抓不到**。这是已知上限。
		{"标题只出现片段词（不判泄漏，避免误伤正确修复）", "[发言人] Polaris 那边下周给结果。", "宝路里斯那边下周给结果。", "", nil},
		{"标题整串被塞进输出", "[发言人] 见 Polaris 供应商对接会 纪要。", "见会议纪要。", "Polaris 供应商对接会", nil},
		{"空输出（调用失败）不算泄漏", "", "任意原文", "", nil},
		// ⚠⚠ 这一条是本门存在的**主要理由**（§35.6 的实测教训）：
		// 名单词本来就写在转写里时，它出现在输出里**不构成泄漏**。
		// 第一版没有 orig 参数，于是负控样本「张伟和李娜都会参加」配名单
		// `[李娜, 王强]` 报了一次假泄漏，而模型返回的是**原样**。
		//
		// ⚠ 用例本身也踩过一次：它当时配的 meta 是 客服组/值班经理/Polaris，
		// 而这四个字**一个都不在 got 里** ⇒ 把 `!strings.Contains(orig, w)`
		// 整段删掉，它照样绿。**测「恒真复发」的那条，必须让被排除的条件
		// 真的成立**，否则它测的是别的东西。
		{"名单词原文本来就有 ⇒ 不得判成泄漏",
			"[发言人] 张伟和李娜都会参加。", "张伟和李娜都会参加。", "",
			&meetingMetaIn{Participants: []string{"李娜"}}},
	}
	for _, c := range cases {
		m := meta
		if c.m != nil {
			m = *c.m
		}
		if got := rosterLeak(c.got, m, c.orig); got != c.want {
			t.Errorf("%s：rosterLeak = %q，期望 %q", c.name, got, c.want)
		}
	}

	// ⚠ 负控样本本身的性质，**直接引用探针的真数据**。
	//
	// 第一版把样本表硬编码在门里，于是变异 L3（把负控名单改成与转写重叠）
	// 和 L4（删掉一条负控）**改了真数据却门禁全绿** —— 门看的是副本。
	// ⇒ 现在直接遍历 refineProbeCases()。
	neg, ctrl, tgt := 0, 0, 0
	for _, c := range refineProbeCases() {
		switch c.kind {
		case "负控":
			neg++
			for _, p := range c.meta.Participants {
				if w := strings.TrimSpace(p); w != "" && strings.Contains(c.asrOut, w) {
					t.Errorf("负控 %q：参会人 %q 与转写重叠 —— 这条负控会被 "+
						"rosterLeak 的 orig 参数整个跳过，看着绿其实没测", c.name, w)
				}
			}
			if w := strings.TrimSpace(c.meta.Title); w != "" && strings.Contains(c.asrOut, w) {
				t.Errorf("负控 %q：主题与转写重叠 —— 同上", c.name)
			}
			// 原样返回必须恒为「不泄漏」
			if got := rosterLeak(c.asrOut, c.meta, c.asrOut); got != "" {
				t.Errorf("负控 %q：把转写原文原样喂回 rosterLeak 应为空，实得 %q", c.name, got)
			}
			if len(c.rules) != 0 {
				t.Errorf("负控 %q：rules 必须为空 —— 负控问的是代价不是收益，"+
					"给它 rules 会让「修好」判据在负控上产生无意义的判定", c.name)
			}
		case "对照":
			ctrl++
			if c.meta.Title != "" || len(c.meta.Participants) > 0 {
				t.Errorf("对照 %q：带了 meta —— 对照组两臂输入必须逐字相同，"+
					"否则它的两臂差异就不能算作噪声", c.name)
			}
		case "目标":
			tgt++
			if len(c.rules) == 0 {
				t.Errorf("目标 %q：rules 为空 —— 目标组是量收益的，没有修好判据它测不出任何东西", c.name)
			}
		default:
			t.Errorf("样本 %q 的 kind=%q 未标注（对照/目标/负控）", c.name, c.kind)
		}
	}
	if neg < 2 {
		t.Errorf("负控样本只剩 %d 条 —— 只测一类失败形态（替换/插入）不够", neg)
	}
	if tgt < 2 {
		t.Errorf("目标样本只剩 %d 条 —— §31 的结论「加了元数据才修得好」样本量不足", tgt)
	}
	if ctrl < 5 {
		t.Errorf("对照样本只剩 %d 条 —— 噪声底噪量不出来", ctrl)
	}
}

// participantLeak / refineFallbackPayload 的门。
//
// ⚠ 这是本轮**唯一不依赖模型听话**的保障：
// 实测两次往提示词里加禁令都没压住「把转写里的人名换成名单里的人」，
// 所以必须由代码兜底。代码兜底自己就得有门。
func TestParticipantLeakRejectsRosterNamesNotInSource(t *testing.T) {
	const src = "[发言人] 张伟和李娜都会参加，王强负责做会议纪要。"
	meta := meetingMetaIn{Title: "产品评审周会", Participants: []string{"赵敏", "孙磊", ""}}

	cases := []struct {
		name, refined, want string
	}{
		{"原样返回（正确）", src, ""},
		{"模型把转写里的人名换成名单里的人（实测复现）",
			"[发言人] 赵敏和孙磊都会参加，王强负责做会议纪要。", "赵敏"},
		{"只换一个", "[发言人] 赵敏和李娜都会参加，王强负责做会议纪要。", "赵敏"},
		{"干净输出（纯纠错）", "[发言人] 章伟和李娜都会参加今天的评审。", ""},
		{"空结果", "", ""},
		// ⚠ 主题**不在**硬拦范围内：目标组「音译专名」那条
		// 正是靠主题里的 Polaris 修好的，若一并拦掉，
		// 那条**正确**的修复会被自己的守卫判成失败。
		{"主题词出现（刻意不拦）", "[发言人] Polaris 供应商对接会下周给结果。", ""},
	}
	for _, c := range cases {
		if got := participantLeak(c.refined, meta, src); got != c.want {
			t.Errorf("%s：participantLeak = %q，期望 %q", c.name, got, c.want)
		}
	}

	// ⚠ 参会人名**本来就在转写里**时，不得判成泄漏 ——
	// 否则会议里真实到场的参会人会被当成模型加的，一次白拦。
	leakMeta := meetingMetaIn{Participants: []string{"李娜", "王强"}}
	if got := participantLeak(src, leakMeta, src); got != "" {
		t.Errorf("参会人本来就在转写里却报了泄漏 %q —— 这会让真实参会人被误拦", got)
	}
}

// TestParticipantLeakSeparatesHomophoneFixFromRosterAlignment（§55）
//
// §54 真网关实测出的两难：守卫分不开「模型把同音错改对」与「模型引入外人」，
// 而误拦的代价是**整份精校 + 全部行动项被丢弃**（实测 3 条，含带期限那条）。
// §55 的修法是引入「姓氏位是否保留」这一判别信号。
//
// ⚠ 这道门必须**同时**钉住两件事，只钉一头等于把门拆了：
//
//	① 放行同音改对（林兰→林岚）
//	② **仍拦** roster 对齐（张伟/李娜→赵敏/孙磊）—— §30 的原始危害
//
// 只加①不加②，是本门自己要防的那种「扩门把安全网拆了」。
func TestParticipantLeakSeparatesHomophoneFixFromRosterAlignment(t *testing.T) {
	// 真网关实测的原始转写（mimo-v2.5-asr 原文）。
	const realSrc = "[张伟] 下周三下午三点，跟王总还有林兰开个客户评审会，" +
		"要准备悬界芯片和机载传感器的对比表。张伟负责工头安装的预算确认，" +
		"所有续期合同必须在十一月底之前签完。"
	const realFixed = "[张伟] 下周三下午三点，跟王总还有林岚开个客户评审会，" +
		"要准备悬界芯片和机载传感器的对比表。张伟负责工头安装的预算确认，" +
		"所有续期合同必须在十一月底之前签完。"

	homophoneMeta := meetingMetaIn{Participants: []string{"张伟", "林岚"}}
	if got := participantLeak(realFixed, homophoneMeta, realSrc); got != "" {
		t.Errorf("同音改对（林兰→林岚，姓氏位保留）被判成泄漏 %q —— "+
			"这条误拦会让实测的 3 条行动项 100%% 丢失", got)
	}

	cases := []struct {
		name, src, refined, want string
		roster                   []string
	}{
		{
			"同姓等长 + 源名被消耗 ⇒ 放行（§54 实测形态）",
			realSrc, realFixed, "",
			[]string{"赵敏", "孙磊", "张", "林岚"},
		},
		{
			"★ 换姓氏 ⇒ 仍拦（§35 实测的真实危害）",
			"[发言人] 张伟和李娜都会参加，王强负责做会议纪要。",
			"[发言人] 赵敏和孙磊都会参加，王强负责做会议纪要。", "赵敏",
			[]string{"赵敏", "孙磊"},
		},
		{
			"只换一个且换了姓 ⇒ 仍拦",
			"[发言人] 张伟和李娜都会参加，王强负责做会议纪要。",
			"[发言人] 赵敏和李娜都会参加，王强负责做会议纪要。", "赵敏",
			[]string{"赵敏", "孙磊"},
		},
		{
			"★ 只是**追加**、源名还在 ⇒ 仍拦（不是替换是引入）",
			realSrc, realSrc + "另外林岚也会到场。", "林岚",
			[]string{"林岚"},
		},
		{
			"★ 同姓但**长度不同** ⇒ 仍拦（§55 规则的边界）",
			"[发言人] 林兰会代表技术部参加。",
			"[发言人] 林岚华会代表技术部参加。", "林岚华",
			[]string{"林岚华"},
		},
		{
			"单字名单项 ⇒ 仍拦（不可能是同音替换）",
			"[发言人] 王总和林兰都会参加。",
			"[发言人] 张总和林岚都会参加。", "张",
			[]string{"张"},
		},
		{
			// ★ 这条专门钉住「姓氏位保留」这个条件本身。
			//   张三 → 李三 **只差一个字**（hamming=1），只有姓氏位能把它们分开。
			//   少这一条，删掉姓氏判定后整张表仍然全绿（变异 N1 曾如此）。
			"★ 同长度只差一字但**换了姓** ⇒ 仍拦（张三→李三）",
			"[发言人] 张三负责接口联调，李四参加评审。",
			"[发言人] 李三负责接口联调，李四参加评审。", "李三",
			[]string{"李三"},
		},
		{
			"转写里根本没有可对应的同姓名 ⇒ 仍拦",
			"[发言人] 今天的议题是预算和排期。",
			"[发言人] 今天的议题是预算和排期，赵敏参加。", "赵敏",
			[]string{"赵敏"},
		},
	}
	for _, c := range cases {
		got := participantLeak(c.refined, meetingMetaIn{Participants: c.roster}, c.src)
		if got != c.want {
			t.Errorf("%s：participantLeak = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestGuardKeepsActionItemsWhenHomophoneFixed（§55）
//
// ★ 这条量的是**整条守卫的后果**，不是 participantLeak 的返回值。
//
// 只测纯函数会漏掉一件事：守卫命中后 `guardRefineResult` 返回的是
// `refineFallbackPayload`，那里 `structured_minutes.action_items` 与 `todos`
// 都是空数组 —— 也就是说守卫一旦误拦，**行动项跟着精校文本一起没了**。
// 「精校文本被还原」用户还能看出来，「日程里什么都没有」他看不出来。
func TestGuardKeepsActionItemsWhenHomophoneFixed(t *testing.T) {
	parsed := map[string]any{
		"refined_transcript": realFixedForGuardTest,
		"structured_minutes": map[string]any{
			"action_items": []any{
				map[string]any{"text": "所有续期合同签完", "assignee": "", "due": "十一月底之前"},
			},
			"agenda": []any{}, "decisions": []any{}, "next_meeting": nil,
		},
		"todos": []any{
			map[string]any{"text": "所有续期合同签完", "assignee": "", "due": "十一月底之前"},
		},
	}
	meta := meetingMetaIn{Participants: []string{"张伟", "林岚"}}

	got, rejected := guardRefineResult(parsed, realSrcForGuardTest, meta)
	if rejected {
		t.Fatalf("同音改对被判成 refine_rejected —— 实测代价是 100%% 产出丢失")
	}
	if got["refined_transcript"] != realFixedForGuardTest {
		t.Errorf("未被拒时必须原样返回精校结果，实际 %v", got["refined_transcript"])
	}
	sm, _ := got["structured_minutes"].(map[string]any)
	items, _ := sm["action_items"].([]any)
	if len(items) == 0 {
		t.Error("未被拒却丢了 action_items —— 「时间点自动进日程」没有输入")
	}
	todos, _ := got["todos"].([]any)
	if len(todos) == 0 {
		t.Error("未被拒却丢了 todos")
	}
}

const (
	realSrcForGuardTest = "[张伟] 下周三下午三点，跟王总还有林兰开个客户评审会，" +
		"所有续期合同必须在十一月底之前签完。"
	realFixedForGuardTest = "[张伟] 下周三下午三点，跟王总还有林岚开个客户评审会，" +
		"所有续期合同必须在十一月底之前签完。"
)

// §23 的教训：提醒建成了吗从来没被上报过。用户看到「精校完成」却拿到
// 未精校的原文，而接口 200、日志一行 —— 与 §17 同一族。
func TestRefineFallbackPayloadMarksDegradation(t *testing.T) {
	const src = "[发言人] 原文在这里。"
	fb := refineFallbackPayload(src)

	if fb["refined_transcript"] != src {
		t.Errorf("降级载荷必须带原始转写，实际 %v", fb["refined_transcript"])
	}
	if ok, _ := fb["refine_fallback"].(bool); !ok {
		t.Error("降级载荷必须带 refine_fallback=true —— 否则调用方无法区分「精校过了」和「没精校」")
	}
	// 结构化字段必须是合法空值，不能缺键：前端按固定结构渲染。
	for _, k := range []string{"translations", "structured_minutes", "todos"} {
		if _, ok := fb[k]; !ok {
			t.Errorf("降级载荷缺字段 %q —— 合法但空的返回体也必须与成功时同构（§21）", k)
		}
	}
	if sm, ok := fb["structured_minutes"].(map[string]any); ok {
		for _, k := range []string{"agenda", "decisions", "action_items"} {
			if _, ok := sm[k]; !ok {
				t.Errorf("structured_minutes 缺字段 %q", k)
			}
		}
	}
}

// guardRefineResult 的门：兜底必须真的**改掉返回体**，而不只是算个布尔。
func TestGuardRefineResultRejectsRosterSubstitution(t *testing.T) {
	const src = "[发言人] 张伟和李娜都会参加，王强负责做会议纪要。"
	meta := meetingMetaIn{Title: "产品评审周会", Participants: []string{"赵敏", "孙磊"}}

	// 1) 实测复现的那种误改 ⇒ 必须回落原文 + 打标记
	bad := map[string]any{
		"refined_transcript": "[发言人] 赵敏和孙磊都会参加，王强负责做会议纪要。",
	}
	got, rejected := guardRefineResult(bad, src, meta)
	if !rejected {
		t.Fatal("把参会人换成名单里的人却没有被拒 —— 这正是实测复现的误改")
	}
	if got["refined_transcript"] != src {
		t.Errorf("被拒时必须回落原始转写，实际 %v", got["refined_transcript"])
	}
	if ok, _ := got["refine_rejected"].(bool); !ok {
		t.Error("被拒时必须打 refine_rejected 标记 —— 否则调用方分不清「精校过了」和「被拦下了」")
	}

	// 2) 干净的纠错结果 ⇒ 原样放行，**不得**打标记
	okIn := map[string]any{
		"refined_transcript": "[发言人] 章伟和李娜都会参加今天的评审。",
	}
	got2, rejected2 := guardRefineResult(okIn, src, meta)
	if rejected2 {
		t.Error("正常纠错被误拦 —— 兜底会毁掉 §31 的收益")
	}
	if got2["refined_transcript"] != okIn["refined_transcript"] {
		t.Error("放行时不得改动结果")
	}
	if _, has := got2["refine_rejected"]; has {
		t.Error("放行时不得带 refine_rejected 标记")
	}

	// 3) 空 refined_transcript（§17 的降级载荷已经填了原文）⇒ 不重复兜底
	fb := refineFallbackPayload(src)
	if _, r := guardRefineResult(fb, src, meta); r {
		t.Error("降级载荷不该被兜底再拒一次")
	}
}

// TestRefineGuardIsWiredUpAtCallSite 钉住**接线**本身。
//
// ⚠ 这道门是补洞补出来的：变异 G1（把兜底整段删掉）、
//
//	G2（只打日志不回落）、G3（回落但不打标记）全都在 llmMeetingRefine 里，
//	而当时门只测纯函数 guardRefineResult 的**行为** —— 三处**全部仍绿**。
//	这就是「判据放错层」：函数是对的，可它没人调。
func TestRefineGuardIsWiredUpAtCallSite(t *testing.T) {
	raw, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatalf("读 server_meeting.go: %v", err)
	}
	src := stripGoComments(string(raw))
	body := goFuncBody(t, src, "func (s *Server) llmMeetingRefine(")

	if !strings.Contains(body, "guardRefineResult(") {
		t.Fatal("llmMeetingRefine 没有调用 guardRefineResult —— 实测复现的误改无人兜底")
	}
	// 必须把兜底**产出的**结果返回出去，而不是调完就扔。
	if !strings.Contains(body, "return guarded, nil") {
		t.Error("llmMeetingRefine 调了 guardRefineResult 却没有返回它的结果 —— 兜底等于没接")
	}
	// 且必须按 rejected 分支走（回落发生在 rejected 时）。
	if !strings.Contains(body, "rejected") {
		t.Error("llmMeetingRefine 没有依据 rejected 分支 —— 兜底的结果与不兜底无差别")
	}

	// ⚠⚠ 这一条是补第二洞补出来的：只查「`rejected` 出现过」是**不够**的。
	//
	// 变异 G3 把分支**倒置**成 `if !rejected { return guarded } return parsed` ——
	// 被拒时反而把有问题的结果返回出去，而上面三条断言**全部照样绿**
	// （`guardRefineResult(` 在、`return guarded, nil` 在、`rejected` 在）。
	//
	// ⇒ 语义锚点不能是「某段文本出现过」，要是「**分支方向对**」。
	//   这里选一个不依赖缩进/格式的等价物：
	//   兜底调用之后**只准**返回兜底产出的结果。
	guardAt := strings.Index(body, "guardRefineResult(")
	if guardAt >= 0 {
		after := body[guardAt:]
		if strings.Contains(after, "return parsed, nil") {
			t.Error("兜底调用之后又 return parsed —— 倒置分支会让被拒的结果原样返回，" +
				"兜底等于白写")
		}
	}
}

// TestMeetingSummaryFallbackGetsItsOwnBudget 钉住「agent 与回落不共享已耗尽的 ctx」。
//
// ⚠ 这是 2026-10-07 抓到的**结构性**缺陷，也是本会话最隐蔽的一个：
//
// 总结链路 kxmemory → agent → 一次性 chat 回落，三段**共用一个 ctx**。
// 而 agent 是 `DefaultMaxTurns = 3` 的有界循环（每次 step 一次串行 LLM 往返），
// 网关 auto 当前解析到推理模型 deepseek-v4-flash（实测首字节 24~142s）。
//
// ⇒ agent 只要跑满一轮，外层 ctx 就过期了；
//
//	**回落拿到的是一个已经死掉的 context，必然立刻失败**，端点返回 502。
//
// ⇒ 「agent 失败时必须回落到一次性 chat」这个设计（§11/§14）
//
//	在生产上**从来没有真正生效过** —— 不是回落效果差，是回落根本没跑成。
//
// ⚠ 为什么是源码断言而不是行为断言：ctx 的过期时序没法用单测复现
//
//	（那需要一个真的卡 45s 的上游）。能验的是**结构**：
//	agent 那一段不得直接复用外层 ctx。
func TestMeetingSummaryFallbackGetsItsOwnBudget(t *testing.T) {
	raw, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatalf("读 server_meeting.go: %v", err)
	}
	src := stripGoComments(string(raw))
	body := goFuncBody(t, src, "func (s *Server) handleMeetingSummary(")

	// 1) agent 必须有自己的子预算 ctx。
	if !strings.Contains(body, "agentBudget") {
		t.Error("handleMeetingSummary 里没有 agentBudget —— " +
			"agent 与一次性回落共用同一个 ctx，回落拿到的是已过期的 context，必然失败")
	}
	// 2) agent 那次调用必须用 agentCtx，而不是外层 ctx。
	agentCall := "s.meetingSummaryViaAgent("
	i := strings.Index(body, agentCall)
	if i < 0 {
		t.Fatal("handleMeetingSummary 里找不到 meetingSummaryViaAgent 调用")
	}
	callArg := callArgTail(body[max(0, i-200):], agentCall)
	if callArg == "" {
		t.Fatal("meetingSummaryViaAgent 的实参取不到")
	}
	if !strings.HasPrefix(callArg, "agentCtx") {
		t.Errorf("meetingSummaryViaAgent 的第一个实参是 %q，期望 agentCtx —— "+
			"直接传外层 ctx 就是这个缺陷本身", callArg)
	}
	// ⚠⚠ 必须断言 agentCtx 的**派生来源**。
	//   只看实参名是不够的：变异 B2 把
	//   `context.WithTimeout(r.Context(), agentBudget)` 改成
	//   `context.WithTimeout(ctx, agentBudget)` —— 实参名照样叫 agentCtx，
	//   但它派生自那个**会被耗尽**的 ctx，缺陷原封不动地回来了。
	if !strings.Contains(body, "context.WithTimeout(r.Context(), agentBudget)") {
		t.Error("agentCtx 不是从 r.Context() 派生的 —— 派生自已耗尽的 ctx 等于没分预算")
	}
	// 3) 回落必须仍从 r.Context() 派生（客户端断开要能取消），
	//    而不是从 agent 那个 ctx 派生。
	if !strings.Contains(body, "s.llmMeetingSummary(ctx,") {
		t.Error("一次性回落没有拿到独立预算的 ctx —— 回落仍会继承 agent 已耗尽的预算")
	}
	if !strings.Contains(body, "context.WithTimeout(r.Context(), summaryBudget)") {
		t.Error("外层预算没有显式命名 summaryBudget —— 读数无法核对，" +
			"下一个人很容易把它和 agentBudget 调反")
	}
	// 4) agentBudget 必须小于 summaryBudget，否则「分段」是假的。
	var ab, sb time.Duration
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "agentBudget =") {
			ab = parseGoDurationAfter(line, "agentBudget =")
		}
		if strings.Contains(line, "summaryBudget =") {
			sb = parseGoDurationAfter(line, "summaryBudget =")
		}
	}
	if ab == 0 || sb == 0 {
		t.Fatalf("没解析出预算：agentBudget=%v summaryBudget=%v", ab, sb)
	}
	if ab >= sb {
		t.Errorf("agentBudget(%v) 必须小于 summaryBudget(%v)，否则 agent 会把回落的预算吃光",
			ab, sb)
	}
}

// parseGoDurationAfter 从形如 `agentBudget = 30 * time.Second` 的行里取时长。
//
// ⚠ 只认「数字 * time.<单位>」这一种形态。写成通用 Go 表达式求值器
//
//	是过度工程，而且那种东西很容易**静默**解析失败返回 0 ——
//	而 0 会被本门的 `ab >= sb` 判成「通过」，等于门自己放水。
//	解析不了就返回 0，让上面的 Fatalf 直接报出来。
func parseGoDurationAfter(line, marker string) time.Duration {
	i := strings.Index(line, marker)
	if i < 0 {
		return 0
	}
	expr := strings.TrimSpace(strings.TrimSuffix(line[i+len(marker):], ","))
	fields := strings.Fields(strings.ReplaceAll(expr, "*", " "))
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	unit, ok := map[string]time.Duration{
		"Second": time.Second, "Seconds": time.Second,
		"Millisecond": time.Millisecond, "Milliseconds": time.Millisecond,
		"Minute": time.Minute, "Minutes": time.Minute,
	}[strings.TrimPrefix(fields[1], "time.")]
	if !ok {
		return 0
	}
	return time.Duration(n * float64(unit))
}
