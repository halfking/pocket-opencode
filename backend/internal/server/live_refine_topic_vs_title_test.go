// live_refine_topic_vs_title_test.go — §143 留下的**最大未测项**的对照实验。
//
// ── 它要回答的问题 ──
//
// §143 我把会议主题接进了术语表，并刻意给它一个**与 Title 不同的标签**
// （「会议议题」而不是「会议主题」），理由写在代码注释里：
//
//	Title 已经占用「会议主题：」这几个字，两个字段共用同一个标签
//	会让模型以为是**同一条信息出现了两次**，从而降低它对其中一条的注意力。
//
// ⚠⚠ 但那条理由**只有推理，没有读数**。同一条产品名，放在 Title 里到底行不行？
// 没测过。而 §143.3 的配对实测只证明了「Topic 这一侧有益」，
// **没有排除「Title 这一侧同样有益」** —— 若是后者，
// 那么「必须新增一个字段」这个设计就是多余的（多一个字段 = 多一处接线 = 多一个能漏的地方）。
//
// ⇒ 所以这是一条 **A/B**，不是又一次「ON vs OFF」。
//
// ── 为什么每臂要跑 3 次 ──
//
// §138.3 实测过：同一份输入，模型会在**同一批错词之间抖动**（玄介 ↔ 玄戒）。
// 那就是底噪的量级。n=1 时「TITLE 臂修好了」和「这次运气好」**无法区分**。
// ⇒ 3 臂 × 3 次，并**交错执行**（不是把 A 臂跑完再跑 B 臂）——
// 网关抖动会均匀地砸在三臂上，而不是只砸中最后跑的那一臂。
// 这是本文件与 §138.3 那个探针唯一的结构性差别，也是它能得出结论的前提。
//
// ── 判据只报读数，不给通过阈值 ──
//
// 与 live_refine_topic_term_test.go 同理由：LLM 修得对不对不是二值问题，
// 且模型侧非确定已实测，写死断言只会让下一个人去调到过拟合。
// 本文件回答的是「Topic 是不是**必要**的」，答案可以是「不是」——
// 那也是有价值的读数（那我就该考虑把 topic 这个字段撤掉，而不是继续接线）。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveGatewayRefineTopicVsTitle -v -count=1
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

const (
	abTermRight = "悬界"
	abWrongA    = "玄介"
	abWrongB    = "玄戒"
	// abTitleTail 是 Title 臂独有的词：转写里**从来没出现过**。
	// §35 实测过名单类上下文会把词塞进压根没提它的转写（张伟→赵敏），
	// 而「把对的改成错的」比漏修危险得多 ⇒ 它是本探针的越界判据。
	abTitleTail = "客户评审会"
)

// abTranscript 与 §138.3 的 S1 逐字相同，便于和那轮读数对照。
const abTranscript = "下周三跟客户过一遍，要准备" + abWrongA + "芯片和机载传感器的对比表"

// abArm 是一臂：产品名放在哪个字段里。
type abArm struct {
	name string
	meta meetingMetaIn
}

func abArms() []abArm {
	return []abArm{
		{name: "OFF  无元数据", meta: meetingMetaIn{}},
		// 生产里 Title 装的是「客户评审会筹备」这类**不含专有名词**的短描述。
		// 这里把产品名塞进 Title，模拟「没有 topic 字段时的老做法」——
		// 也就是 §143 之前唯一可用的那条路。
		{name: "TITLE 产品名放 Title", meta: meetingMetaIn{Title: "悬界芯片" + abTitleTail + "筹备"}},
		{name: "TOPIC 产品名放 Topic", meta: meetingMetaIn{Topic: "悬界芯片" + abTitleTail}},
	}
}

// abVerdict 只判三态，且**越界优先于修好**。
//
// ⚠ 顺序有讲究：先判越界。若一段输出既修好了产品名又把 Title 里
// 独有的词塞了进来，那它是「部分有害」，报「✓ 修好」会把它粉饰过去。
func abVerdict(got string) string {
	overreach := strings.Contains(got, abTitleTail)
	fixed := strings.Contains(got, abTermRight)
	switch {
	case fixed && overreach:
		return "⚠ 部分越界：修对了，但塞进了原文没有的「" + abTitleTail + "」"
	case fixed:
		return "✓ 修好"
	case strings.Contains(got, abWrongA) || strings.Contains(got, abWrongB):
		return "— 原样（仍是错词）"
	default:
		return "? 输出不含任何目标词，需人工判读"
	}
}

// abFixedVerdict 是「修好」那一态的字面量。
// ⚠ 口径必须只有**一处**：汇总数的就是它，而它同时是 abVerdict 的返回值之一。
//
//	两处各写一遍时，改了返回值不改汇总（或反之），读数会静默偏移（§329 抄写漂移形态）。
const abFixedVerdict = "✓ 修好"

// abArmCount 是单臂的三桶读数。
//
// ⚠⚠ 为什么要有「应跑」这个概念：原来的汇总打印「修好 %d/%d」，
//
//	而那个分母是**有效样本数**（reps 减去三试都失败的次数）。
//	于是 B 臂跑 3 次、失败 2 次、剩 1 次修好了，打印出来是「修好 1/1」——
//	**完美**，而实际上一半以上的请求根本没拿到读数。
//	唯一的提示是全文末尾那句聚合的「有 N 次三试都失败」，
//	它不指名**哪一臂**，且排在「怎么读这组读数」那段结论**之后**。
//	⇒ 「一次「选择/缺陷」在读数里同形」的形态：分母对了，口径看不见。
type abArmCount struct {
	fixed int // 判成 abFixedVerdict 的次数
	ok    int // 有读数（没失败）的次数 —— 比率的分母
	fail  int // 三试都失败、本次无读数的次数
}

// abArmCountOf 数一臂的三桶，返回值与一句可打印的红因（空串 = 对上了）。
//
// 抽成纯函数是为了**让口径能离线判**：探针开头就 t.Skip，
// 而 §498 登记的残余正是「分母是有效样本，却看不出有效样本是怎么来的」，
// 它原先只存在于打印格式里，判据够不着。
func abArmCountOf(reps int, texts []string, failed []bool) (abArmCount, string) {
	var c abArmCount
	if len(texts) != reps || len(failed) != reps {
		return c, fmt.Sprintf("采样记录 %d 条 / 失败标记 %d 条，期望各 %d 条"+
			"（有一轮既没 append 文本、也没 append 标记）", len(texts), len(failed), reps)
	}
	for j := range texts {
		if failed[j] {
			c.fail++
			continue
		}
		c.ok++
		if abVerdict(texts[j]) == abFixedVerdict {
			c.fixed++
		}
	}
	// ⚠ 下面两条在**当前实现里恒真**（ok/fail 是互斥二分，fixed ≤ ok），
	//   所以它们不是判据、只是防未来有人改这个函数时打破它。
	//   真正挑得起毛病的是上面那句长度检查 —— 那是唯一的外部约束。
	//   （照 §494 的教训登记：恒真的断言不冒充判据。）
	if c.ok+c.fail != reps {
		return c, fmt.Sprintf("三桶合计 %d ≠ 应跑 %d", c.ok+c.fail, reps)
	}
	if c.fixed > c.ok {
		return c, fmt.Sprintf("修好 %d > 有效样本 %d —— 口径写反了", c.fixed, c.ok)
	}
	return c, ""
}

func TestLiveGatewayRefineTopicVsTitle(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("主题 vs 标题的 A/B 对照：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m1/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	const reps = 3
	arms := abArms()

	// 单臂跑一次，返回文本与「是否失败」。
	runOnce := func(meta meetingMetaIn) (string, bool) {
		const maxTry = 3
		var lastErr error
		for try := 1; try <= maxTry; try++ {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			res, err := s.llmMeetingRefine(ctx, r, []meetingSegmentIn{{
				Speaker: "发言人", Text: abTranscript, Lang: "zh",
			}}, nil, meta)
			cancel()
			if err != nil {
				lastErr = err
				t.Logf("     … 第 %d/%d 次尝试失败：%v", try, maxTry, err)
				continue
			}
			if rj, _ := res["refine_rejected"].(bool); rj {
				// 兜底拦下了什么：本探针没传参会人，出现它说明有别的东西在起作用，
				// 如实报出来而不是当成功。
				return "⟪被兜底拦下⟫ " + fmt.Sprint(res["refined_transcript"]), false
			}
			return fmt.Sprint(res["refined_transcript"]), false
		}
		t.Logf("     ✗ 该次三试都失败：%v", lastErr)
		return "", true
	}

	// ★ 交错执行：第 k 轮把三臂各打一次，而不是把一臂跑满再换下一臂。
	//   网关抖动/限流在这半小时里并不均匀，顺序执行会把漂移**记到某一臂头上**。
	texts := make([][]string, len(arms))
	failed := make([][]bool, len(arms))
	tries := 0
	// ⚠⚠ §500：接账本，total = reps × len(arms)（登记在缺口表里的原话）。
	//
	// 映射与 §496 的 NextMeetingRaw 同一口径：
	//   拿到读数 ⇒ Complete（含「被兜底拦下」那种非空但无效的输出，它仍是一次跑成）；
	//   三试都失败 ⇒ **故意不记**。它是真失败，不是「按设计不成立」——
	//   §495 的 Skip 豁免只给「输入不足」那类**先验**理由，
	//   把失败记成跳过比不记更糟。不记即被账本算进「没做完」⇒ Report() 会出声。
	ledger := livetest.New("主题 vs 标题 A/B（三臂交错）", reps*len(arms))
	for k := 0; k < reps; k++ {
		t.Logf("── 第 %d/%d 轮（三臂交错）", k+1, reps)
		for ai, a := range arms {
			got, bad := runOnce(a.meta)
			if bad {
				tries++
				// ⚠⚠ §494：三试都失败时 got 是空串，而 abVerdict("") 判成
				//　「? 输出不含任何目标词，需人工判读」——
				//　⇒ **失败在逐行日志里长得像一个「模型输出了无法判读的结果」**，
				//　读日志的人会以为模型返回了空文本、需要人工看看。
				//
				//　汇总那侧其实是对的：failed[] 把它排除了，分母没有把它算进去。
				//　失真的只有逐行这一层 —— 而**它才是多数人读的那一层**。
				//　⇒ 与上面「⟪被兜底拦下⟫」同一手法：状态必须打在行上，不能靠空串自明。
				t.Logf("   %-22s ✗ 三试都失败，本次无读数（不计入下方比率）", a.name)
				texts[ai] = append(texts[ai], got)
				failed[ai] = append(failed[ai], bad)
				continue
			}
			ledger.Complete(a.name + "|第" + strconv.Itoa(k+1) + "轮")
			t.Logf("   %-22s → %s   %s", a.name, got, abVerdict(got))
			texts[ai] = append(texts[ai], got)
			failed[ai] = append(failed[ai], bad)
		}
	}

	// ⚠⚠ 账本 Report 必须**先于**任何比率打印（§495 的次序纪律）：
	//   跑不满这件事若排在结论后面，就只是附录。
	if rep := ledger.Report(); rep != "" {
		t.Log(rep)
	}
	t.Logf("  账本：跑成 %d / 共 %d（= 每臂应跑 %d × %d 臂）· 三试都失败 %d（不计入跑成，由 Report 负责出声）",
		ledger.Done(), ledger.Total(), reps, len(arms), tries)

	t.Log("")
	t.Logf("════ 汇总（每臂应跑 %d 次）════", reps)
	counts := make([]abArmCount, len(arms))
	sumFail := 0
	for ai, a := range arms {
		c, bad := abArmCountOf(reps, texts[ai], failed[ai])
		if bad != "" {
			t.Errorf("臂 %s %s ⇒ 这一臂的比率不可信，先修再读", a.name, bad)
			continue
		}
		counts[ai] = c
		sumFail += c.fail
	}
	// ⚠⚠⚠ 三桶对账必须**先于**任何比率打印（§495 的次序纪律）：
	//   比率先印出来，读数的人已经带着一个还没被核对过的分母往下看了，
	//   后面再多的告警也只是「附录」。
	//
	// ⚠ 这一条是真的交叉核对：sumFail 来自各臂的桶，tries 来自**逐轮**累加，
	//   两处是不同的代码路径。对不上 ⇒ 有一批失败既没进桶、也没被总数看到。
	if sumFail != tries {
		t.Errorf("各臂无读数合计 %d ≠ 逐轮记下的 tries %d ⇒ 有一批失败既没进桶、也没被总数看到", sumFail, tries)
	}
	t.Logf("  ✓ 对账：应跑 %d×%d=%d，其中三试都失败 %d，无读数合计 %d", reps, len(arms), reps*len(arms), tries, sumFail)
	for ai, a := range arms {
		c := counts[ai]
		if c.ok+c.fail == 0 {
			continue // 对账已报红，这一臂的桶不可信，不打比率免得误导
		}
		if c.ok == 0 {
			t.Logf("  %-22s 应跑 %d，**无读数 %d**（全部三试失败）⇒ 本臂无读数", a.name, reps, c.fail)
			continue
		}
		// ⭐ 分母与应跑次数**并排印出**：只看「修好 1/1」会以为这臂没问题，
		// 而「无读数 2」才是它真正的样子（§498 登记的残余）。
		t.Logf("  %-22s 修好 %d/%d　（应跑 %d，无读数 %d ⇒ 分母只剩 %d）",
			a.name, c.fixed, c.ok, reps, c.fail, c.ok)
	}

	t.Log("")
	t.Log("═ 怎么读这组读数 ══")
	t.Log("  · TITLE 修好率 ≈ TOPIC 修好率 ⇒ 「必须新增 topic 字段」这条理由**不成立**，")
	t.Log("    我把产品名放 Title 也能修好 ⇒ §143 的接线是多余的一处复杂度。")
	t.Log("  · TITLE 修好率明显低于 TOPIC ⇒ 独立标签这条理由**拿到了读数支撑**。")
	t.Log("  · 两臂都接近 OFF ⇒ 上一轮「TOPIC 修好了」大概率是底噪，§143.3 的结论要打折。")
	t.Log("  · 任一臂出现「部分越界」⇒ 该臂的元数据有害，不能这么喂。")
	if tries > 0 {
		t.Logf("⚠⚠ 有 %d 次三试都失败 ⇒ 对应臂的读数**不完整**；逐臂的「无读数 N」已并排印在每行分母后面", tries)
	}
	t.Log("")
	t.Logf("⚠ 样本量只有 %d/臂，**不构成统计显著**；本探针只回答「方向」与「量级」，", reps)
	t.Log("  不回答「效果确切是多少」。§35 已记过用 n=2 下结论的教训，此处同理。")
}

// TestABVerdictClassifiesThreeStates — 判读逻辑**自己的**门，不需要真网关。
//
// ⚠ 为什么必须单独有一条：上面那个探针开头就 `t.Skip`（需 POCKET_LIVE_GATEWAY=1），
//
//	⇒ 在 CI 与任何普通 `go test` 里**它一次都不执行**。
//	⇒ 若哪天有人把 `abVerdict` 的分支改坏（最省事的一种：把 `case fixed:` 提前），
//	  探针照样「跑得很好」，而读数会**整体偏向「修好」** ——
//	  那正好是本探针唯一想避免的偏差。
//	⇒ 这不是给探针补覆盖率，是给**尺子**补自证。
func TestABVerdictClassifiesThreeStates(t *testing.T) {
	// 三态 + 一个「需人工判读」态，逐条钉住。
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"目标词出现 = 修好", "下周三要准备" + abTermRight + "芯片的对比表", "✓ 修好"},
		{"仍是错词 = 原样", abTranscript, "— 原样（仍是错词）"},
		{"另一个错词也算原样", "要准备" + abWrongB + "芯片", "— 原样（仍是错词）"},
		{"修好但塞进原文没有的词 = 部分越界",
			"这次客户评审会要准备" + abTermRight + "芯片", "⚠ 部分越界：修对了，但塞进了原文没有的「" + abTitleTail + "」"},
		{"两个目标词都没有 = 需人工判读", "今天讨论预算和排期", "? 输出不含任何目标词，需人工判读"},
	}
	for _, c := range cases {
		if got := abVerdict(c.got); got != c.want {
			t.Errorf("%s：abVerdict(%q) = %q，期望 %q", c.name, c.got, got, c.want)
		}
	}

	// ★ 负控：判据**不是常量**，且不会把「没修好」误记成「修好」。
	//   这是统计口径的地基：汇总里数的就是 abVerdict == "✓ 修好" 的次数。
	seen := map[string]bool{}
	for _, c := range cases {
		seen[abVerdict(c.got)] = true
	}
	if len(seen) < 4 {
		t.Errorf("abVerdict 只区分出 %d 种结论 ⇒ 汇总口径会失真：%v", len(seen), seen)
	}
	for _, c := range cases {
		if c.want != "✓ 修好" && abVerdict(c.got) == "✓ 修好" {
			t.Errorf("abVerdict 把「%s」误判成「修好」⇒ 修好率会被系统性抬高", c.name)
		}
	}

	// ★ 负控：三臂必须真的**不一样**，否则 A/B 退化成同一个实验的 n=9。
	//   （本会话反复栽在「A/B 两臂其实喂了同一份输入」。）
	metas := make([]string, len(abArms()))
	for i, a := range abArms() {
		metas[i] = fmt.Sprintf("T=%q/P=%q", a.meta.Title, a.meta.Topic)
	}
	for i := range metas {
		for j := i + 1; j < len(metas); j++ {
			if metas[i] == metas[j] {
				t.Errorf("第 %d 与第 %d 臂喂了同一份元数据（%s）⇒ 对照实验退化", i, j, metas[i])
			}
		}
	}
}

// TestABArmCountReconciliation — §498 登记的残余：「修好 N/M」的分母是有效样本，
// 而有效样本是怎么来的读不出来。这条判据钉住那个口径。
//
// ⚠ 本探针开头就 t.Skip ⇒ 这里数不了任何真实运行，
//
//	所以判据只保证**口径函数**是自洽的（分母确实随失败数缩小），
//	不保证「每轮都会 append 一条记录」这件事成立 —— 后者是探针里的代码路径，
//	靠 abArmCountOf 的长度检查在真跑时兜住（对不上就 t.Errorf）。
func TestABArmCountReconciliation(t *testing.T) {
	const fixedOut = "下周三要准备" + abTermRight + "芯片的对比表"

	// ① 一次失败必须把分母缩小 1 —— 这是整节存在的理由。
	c, bad := abArmCountOf(3,
		[]string{fixedOut, fixedOut, fixedOut},
		[]bool{false, true, false})
	if bad != "" {
		t.Fatalf("① 对不上账：%s", bad)
	}
	if c.ok != 2 || c.fail != 1 || c.fixed != 2 {
		t.Errorf("① 三桶 = fixed %d / ok %d / fail %d，期望 2/2/1", c.fixed, c.ok, c.fail)
	}
	if c.ok == 3 {
		t.Error("① 失败一次之后分母仍是 3 ⇒ 「有效样本」这个词是假的（§498 残余原样存在）")
	}

	// ② 全失败 ⇒ ok 为 0，探针据此打「本臂无读数」而不是「修好 0/0」。
	c, bad = abArmCountOf(3, []string{"", "", ""}, []bool{true, true, true})
	if bad != "" {
		t.Fatalf("② 对不上账：%s", bad)
	}
	if c.ok != 0 || c.fail != 3 {
		t.Errorf("② 三桶 = fixed %d / ok %d / fail %d，期望 0/0/3", c.fixed, c.ok, c.fail)
	}

	// ③ 长度对不上必须**报红因**，且红因要指名是长度问题。
	//    ⚠ 这是本函数**唯一**真正的外部约束：ok/fail 是互斥二分，
	//    所以「ok+fail == reps」在长度对上时恒真（已在 abArmCountOf 里如实标注）。
	if _, bad := abArmCountOf(3, []string{fixedOut}, []bool{false}); bad == "" {
		t.Error("③ 只给了 1 条记录却没报红因 ⇒ 有一轮被静默吞掉，分母在无声地变小")
	}
	if _, bad := abArmCountOf(3, []string{fixedOut, fixedOut, fixedOut}, []bool{false, true}); bad == "" {
		t.Error("③ 文本 3 条但失败标记 2 条却没报红因 ⇒ 文本与标记已经对不上")
	}

	// ④ 负控：判据不是常量 —— 三种失败形态必须给出三种不同的桶。
	seen := map[abArmCount]bool{}
	for _, f := range [][]bool{
		{false, false, false},
		{false, true, false},
		{true, true, true},
	} {
		got, bad := abArmCountOf(3, []string{fixedOut, fixedOut, fixedOut}, f)
		if bad != "" {
			t.Fatalf("④ 失败形态 %v 对不上账：%s", f, bad)
		}
		seen[got] = true
	}
	if len(seen) != 3 {
		t.Errorf("④ 三种失败形态只区分出 %d 种桶：%v ⇒ 失败数没真正进桶", len(seen), seen)
	}
}

// TestABRateIsPrintedAfterReconciliation — 次序纪律：比率不许抢在对账前面。
//
// ⚠ 复用 live_agent_refs_fabrication_probe_test.go 里的 goStripped（同一 package）。
//
//	⇒ 跨文件依赖：删掉那个文件会让本条**编译不过**，这是有意的
//	（工具函数只有一份实现，比抄两份强）。
func TestABRateIsPrintedAfterReconciliation(t *testing.T) {
	const self = "live_refine_topic_vs_title_test.go"
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("读不到 %s: %v", self, err)
	}
	// ⚠ 锚点必须落在**代码**里：中文格式串全在字符串字面量里，
	//   被 goStripped 剥掉后就找不到了（§499.5 当场栽过一次）。
	code := goStripped(string(raw))
	iRec := strings.Index(code, "sumFail != tries")
	iRate := strings.Index(code, "a.name, c.fixed, c.ok")
	if iRec < 0 || iRate < 0 {
		t.Fatalf("量具失效：对账锚点位置 %d、比率锚点位置 %d，必须都 ≥ 0", iRec, iRate)
	}
	if iRate < iRec {
		t.Errorf("比率打印（位置 %d）在三桶对账（位置 %d）之前 ⇒ 分母还没被核对就先印出去了",
			iRate, iRec)
	}
}
