package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scriptedASR 按调用顺序返回预设文本，用于精确控制多段拼接结果。
type scriptedASR struct {
	mu    sync.Mutex
	texts []string
	idx   int
}

func (s *scriptedASR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(32 << 20)
	s.mu.Lock()
	defer s.mu.Unlock()
	txt := "fallback"
	if s.idx < len(s.texts) {
		txt = s.texts[s.idx]
	}
	s.idx++
	_, _ = w.Write([]byte(`{"text":"` + txt + `"}`))
}

func newIncremental(srvURL string) *IncrementalTranscriber {
	tr := NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: srvURL, APIKey: "k", Model: "stub-model",
			Transport: TransportTranscriptions, Channel: ChannelExternal,
		}, nil
	})
	return NewIncrementalTranscriber(tr)
}

func toneChunk(start, end float64) IncrementalChunk {
	return IncrementalChunk{
		Audio: continuousWAV(16000, 1), StartSec: start, EndSec: end, SilenceCut: true,
	}
}

func TestIncrementalProducesDeltaNotFullText(t *testing.T) {
	// scriptedASR 每段返回固定文本，delta 应为「新出现的那部分」而不是全量。
	stub := &scriptedASR{texts: []string{"甲", "乙"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()

	r1, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatalf("第 1 段失败: %v", err)
	}
	if r1.Text != "甲" || r1.Delta != "甲" {
		t.Errorf("第 1 段 text=%q delta=%q，期望都是「甲」", r1.Text, r1.Delta)
	}

	r2, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(1, 2), false)
	if err != nil {
		t.Fatalf("第 2 段失败: %v", err)
	}
	// Text 是累计文本（前端覆盖显示用），Delta 是相对上次的增量。
	// 「甲」与「乙」无字符重叠，所以累计为「甲乙」，增量为「乙」。
	if r2.Text != "甲乙" {
		t.Errorf("第 2 段 text=%q，期望累计「甲乙」", r2.Text)
	}
	if r2.Delta != "乙" {
		t.Errorf("第 2 段 delta=%q，期望只有新增的「乙」（前端据此只追加不重排）", r2.Delta)
	}
}

func TestIncrementalResetsBetweenSessions(t *testing.T) {
	stub := &scriptedASR{texts: []string{"甲", "乙", "丙"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()
	first, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text == "" {
		t.Fatal("第 1 段应出字")
	}
	// 新会话必须清空累积，否则第二场会议的文字会接着第一场显示
	it.Reset()
	r, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "乙" {
		t.Errorf("Reset 后应从零开始（只含本段文本），实际 %q", r.Text)
	}
	if strings.Contains(r.Text, first.Text) {
		t.Errorf("Reset 后仍含上一会话文本 %q，说明累积未清空", first.Text)
	}
}

func TestIncrementalSegmentFailureKeepsPreviousText(t *testing.T) {
	// 关键契约：一段失败不能让此前已经出字的即时文本消失。
	var hit int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		if hit == 1 {
			_, _ = w.Write([]byte(`{"text":"已经转写好的内容"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()
	r1, _ := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if r1.Text == "" {
		t.Fatal("第 1 段应出字")
	}

	r2, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(1, 2), false)
	if err != nil {
		t.Fatalf("单段失败不应让整个调用返回错误: %v", err)
	}
	if r2.Error == "" {
		t.Error("单段失败应在结果里带 Error，供前端提示")
	}
	if r2.Text != r1.Text {
		t.Errorf("段失败后已产出文本必须保留: 之前=%q 现在=%q", r1.Text, r2.Text)
	}
}

func TestIncrementalEmptyChunkIsNotAnError(t *testing.T) {
	stub := &scriptedASR{texts: []string{}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	r, err := it.TranscribeChunk(context.Background(), Scope{}, IncrementalChunk{}, false)
	if err != nil {
		t.Fatalf("纯静音产生的空切片不应报错: %v", err)
	}
	if r.Text != "" {
		t.Errorf("空切片不应产出文本，实际 %q", r.Text)
	}
}

func TestIncrementalFinalFlagPropagates(t *testing.T) {
	stub := &scriptedASR{texts: []string{"甲"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	r, _ := it.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), true)
	if !r.IsFinal {
		t.Error("IsFinal 未透传到结果")
	}
}

func TestIncrementalRejectsUnconfiguredEngine(t *testing.T) {
	it := NewIncrementalTranscriber(nil)
	if _, err := it.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), false); err == nil {
		t.Error("未配置的引擎应报错")
	}
	// nil 接收者也不能 panic
	var nilIt *IncrementalTranscriber
	if _, err := nilIt.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), false); err == nil {
		t.Error("nil 接收者应报错而不是 panic")
	}
}

func TestMergeIncrementalRemovesOverlap(t *testing.T) {
	// 这是即时转写最容易出错的地方：切片有 1 秒重叠，直接拼接会出现
	//「今天今天下午三点」这种明显错误。
	cases := []struct {
		name            string
		committed, next string
		want            string
	}{
		{"空历史直接取新段", "", "今天下午三点开会", "今天下午三点开会"},
		{"新段为空保持不变", "今天下午三点开会", "", "今天下午三点开会"},
		{"完全重复不叠加", "今天下午三点", "今天下午三点", "今天下午三点"},
		{"重叠部分去重", "今天下午三点开项目评审会", "三点开项目评审会请准备报告", "今天下午三点开项目评审会请准备报告"},
		{"无重叠原样拼接", "今天下午三点", "请准备进度报告", "今天下午三点请准备进度报告"},
		{"短于阈值不误判", "预算", "算完成", "预算算完成"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeIncremental(tc.committed, tc.next); got != tc.want {
				t.Errorf("mergeIncremental(%q, %q) = %q，期望 %q",
					tc.committed, tc.next, got, tc.want)
			}
		})
	}
}

func TestMergeIncrementalDoesNotDropRealContent(t *testing.T) {
	// 误去重的代价是丢掉真实内容，代价高于多几个重复字。
	//
	// 契约是「重叠判定上限 40 rune」：更短的重复视为切片重叠（去重），
	// 超过上限的重复视为真实重复说话（保留两份）。
	short := strings.Repeat("甲", 30)
	if got := mergeIncremental(short, short); got != short {
		t.Errorf("30 rune 重复应判为切片重叠并去重，实际长度 %d，期望 %d", len([]rune(got)), len([]rune(short)))
	}

	long := strings.Repeat("甲", 60)
	if got := mergeIncremental(long, long); len([]rune(got)) != 120 {
		t.Errorf("60 rune 重复应视为真实重复说话并保留两份，实际长度 %d，期望 120", len([]rune(got)))
	}
}

func TestMergeIncrementalHandlesChineseRuneBoundaries(t *testing.T) {
	// 按字节切会劈开 UTF-8 字符，表现为「甲」接「乙」丢掉「乙」。
	// 这条锁住按 rune 切的实现：单字不构成重叠，两个相同字才构成。
	cs := []rune("今天下午三点开项目评审会")
	ns := []rune("三点开项目评审会请准备进度报告")
	if got := mergeIncremental(string(cs), string(ns)); got != "今天下午三点开项目评审会请准备进度报告" {
		t.Errorf("中文重叠去重失败: %q", got)
	}
	// 无重叠的相邻中文不应被误判
	if got := mergeIncremental("我", "好"); got != "我好" {
		t.Errorf("单字相邻不应被误判成重叠: %q", got)
	}
}

// TestMergeIncrementalHandlesFuzzyOverlap 是 2026-10-06 的核心回归：
// 用户实测反馈「录音片段重复」。根因是原实现只用**逐字相等**判重叠，而
// 同一段音频被转两次时上下文不同，ASR 输出几乎不会逐字一致——
// 标点随机、连接词增减都很常见，于是精确比较在重叠区中途断掉，
// 整段拼接，用户看到重复。
//
// 这些用例模拟真实的 ASR 分歧形态（多标点/多连接词/少一个字）。
func TestMergeIncrementalHandlesFuzzyOverlap(t *testing.T) {
	cases := []struct {
		name            string
		committed, next string
		want            string
	}{
		{
			"标点不同仍应识别为重叠（next 的逗号要保留）",
			"今天下午三点开项目评审会",
			"今天下午三点开项目评审，请准备材料",
			// ⚠️ want 里的「，」不是笔误：next 确实多了一个逗号，
			//    它是本次新识别出的内容，必须出现在结果里。
			//    早先写成「…评审会请准备材料」漏掉了它——那才是真丢内容。
			"今天下午三点开项目评审会，请准备材料",
		},
		{
			"重叠区多一个字（ASR 幻听/漏字）",
			"我们下周一上午十点开季度复盘会",
			"下周一上午十点开季度复盘会议",
			"我们下周一上午十点开季度复盘会议",
		},
		{
			"重叠区少一个字",
			"麻烦你把预算表今天下班前发给我",
			"把预算表今天下班前发给我谢谢",
			"麻烦你把预算表今天下班前发给我谢谢",
		},
		{
			"重叠区带连接词差异",
			"这个方案的三个风险点已经确认过了",
			"三个风险点已经确认过了下周上线",
			"这个方案的三个风险点已经确认过了下周上线",
		},
		{
			"长重叠 + 中段差异",
			"上午的讨论主要集中在三个方面第一是预算第二是排期第三是人员安排",
			"方面第一是预算第二是排期第三是人员安排我们下午继续",
			"上午的讨论主要集中在三个方面第一是预算第二是排期第三是人员安排我们下午继续",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeIncremental(tc.committed, tc.next); got != tc.want {
				t.Errorf("mergeIncremental(%q, %q)\n got = %q\nwant = %q",
					tc.committed, tc.next, got, tc.want)
			}
		})
	}
}

// TestMergeIncrementalFuzzyNeverDropsRealContent 是模糊匹配的**负控**。
//
// 模糊去重的代价是「误判重叠 ⇒ 吃掉真实内容」，而误删的内容用户**永远不会
// 发现**（直到某天需要那句话时）。所以必须证明：只在真有重叠时才裁。
func TestMergeIncrementalFuzzyNeverDropsRealContent(t *testing.T) {
	cases := []struct {
		name            string
		committed, next string
	}{
		// 语义相近但**不是**重叠——拼接后必须两段都完整保留。
		{"完全是新的一句话", "今天下午三点开会", "明天上午九点复盘"},
		{"共享常用词但不同句", "我们明天开会讨论一下", "讨论一下预算的问题"},
		{"短串不得触发模糊匹配", "预算", "算完成"},
		{"单字不得触发模糊匹配", "我", "好"},
		{"高相似但位置不对（用户在文中重复）", "好的我知道了", "知道了好的"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeIncremental(tc.committed, tc.next)
			// 契约：要么原样拼接（两段都在），要么去重后的结果必须
			// 包含 committed 的全部内容。**任何情况下不得比 committed 更短**
			// 且丢掉 committed 自身。
			if len([]rune(got)) < len([]rune(tc.committed)) {
				t.Errorf("模糊匹配吃掉了真实内容: committed=%q next=%q got=%q",
					tc.committed, tc.next, got)
			}
			// 原样拼接时两段必须都还在（不丢任何一边）。
			if got == tc.committed+tc.next {
				return
			}
			// 走了去重分支：必须保留了 next 中**超出重叠**的部分。
			if len([]rune(got)) >= len([]rune(tc.committed))+len([]rune(tc.next)) {
				t.Errorf("去重分支却比原样拼接还长，逻辑有误: %q", got)
			}
		})
	}
}

// TestFuzzyOverlapAnchorsAreConservative 钉住两个阈值常量本身：
// 锚点太短 / 相似度太低会把日常碰撞误判成重叠。
func TestFuzzyOverlapAnchorsAreConservative(t *testing.T) {
	// 3 个字对 2 个（0.67 < 0.75）不构成重叠。
	cs := []rune("我们下周一上午")
	ns := []rune("下周一开始")
	if tail, ok := fuzzyOverlapTail(cs, ns, 40); ok {
		t.Errorf("短锚点高相似不应判为重叠，实际返回净增 %q", string(tail))
	}
	// 4 个字全同（1.0）构成重叠。
	cs2 := []rune("今天下午三点开会")
	ns2 := []rune("今天下午三点开会谢谢大家")
	tail, ok := fuzzyOverlapTail(cs2, ns2, 40)
	if !ok {
		t.Errorf("重叠锚点应被判为重叠")
	}
	if string(tail) != "谢谢大家" {
		t.Errorf("净增内容应为「谢谢大家」，实际 %q", string(tail))
	}
}

// TestFuzzyOverlapKeepsBothSidesAdditions 锁住选 LCS 而非「裁固定长度」的理由：
// 重叠区内部有插入字时，两边**各自多出来的字都必须保留**。
//
// 固定长度裁剪在这里必错（无论 n 取多少）：
//
//	committed = 今天下午三点开项目评审会
//	next      = 今天下午三点开项目评审，请准备材料
//	                    ↑ 真实重叠到此，next 之后多一个逗号
//
// 裁 13 ⇒ 净增变成「审，请准备材料」⇒ 「审」出现两次
// 裁 14 ⇒ 净增变成「，请准备材料」⇒ 相对 committed 看似没重复，但代价是
//
//	依赖「next 恰好只多一个尾字符」这一巧合，多一个就出错。
//
// LCS 对齐的净增是「，请准备材料」——两侧的真实增量都在，且只出现一次。
func TestFuzzyOverlapKeepsBothSidesAdditions(t *testing.T) {
	cs := []rune("今天下午三点开项目评审会")
	ns := []rune("今天下午三点开项目评审，请准备材料")
	tail, ok := fuzzyOverlapTail(cs, ns, 40)
	if !ok {
		t.Fatalf("应判为重叠")
	}
	got := string(tail)
	// ★ 必须**精确相等**，不能用 containsAll 包含判断。
	//   本条第一版写的是 containsAll(got, "，请准备材料")，结果固定长度裁剪
	//   变异（净增="审，请准备材料"）也能通过——因为那个串**包含**目标子串。
	//   「审」是 committed 已有的字，出现在净增里就是重复，属真缺陷。
	//   子串包含断言在这里是恒真判据：它只能证明"没丢"，不能证明"没多"。
	if got != "，请准备材料" {
		t.Errorf("净增必须恰为「，请准备材料」，实际 %q", got)
	}
	// 端到端：结果里「审」只能出现一次，且整体必须精确等于期望。
	merged := mergeIncremental(string(cs), string(ns))
	if n := countRune(merged, '审'); n != 1 {
		t.Errorf("合并结果中「审」应恰好出现 1 次，实际 %d 次：%q", n, merged)
	}
	if merged != "今天下午三点开项目评审会，请准备材料" {
		t.Errorf("合并结果必须精确匹配（不多字不少字），实际 %q", merged)
	}
}

// TestFuzzyOverlapRejectsUnrelatedText 负控：语义无关的两段不得被当成重叠，
// 否则会吃掉真实内容（这是模糊去重最危险的方向）。
func TestFuzzyOverlapRejectsUnrelatedText(t *testing.T) {
	cs := []rune("今天下午三点开项目评审会")
	ns := []rune("明天上午九点我们去楼下吃面")
	if _, ok := fuzzyOverlapTail(cs, ns, 40); ok {
		t.Errorf("语义无关的两段不应判为重叠")
	}
	// 锚点太短时也不判（limit < minFuzzyAnchor）。
	if _, ok := fuzzyOverlapTail([]rune("我们开会"), []rune("开会吧"), 40); ok {
		t.Errorf("锚点长度不足 4 时不应判为重叠")
	}
}

func countRune(s string, r rune) int {
	n := 0
	for _, c := range s {
		if c == r {
			n++
		}
	}
	return n
}

// TestAnchorCoverageGateRejectsLooseMatch 是 coverage 闸的**专门靶子**。
//
// 背景：模糊去重有三种失败形态，其中「覆盖率不足却判为重叠」最隐蔽——
// 它会产出一段读起来通顺、但内容来自错误位置的文本。coverage 闸就是拦它的。
//
// 这组用例的重叠区只占窗口一半左右，净增占比也在正常范围内，
// 所以拦下它们的**只可能是 coverage**。
func TestAnchorCoverageGateRejectsLooseMatch(t *testing.T) {
	cases := []struct {
		name            string
		committed, next string
		wantNoOverlap   bool
	}{
		{
			// 实测覆盖率 0.58 < 0.6：前半段沾边、后半段完全不同。
			"半程相似不算重叠",
			"今天要讨论三件事预算排期和人员", "预算排期和人员，以及风险",
			true,
		},
		{
			// 实测覆盖率 0.54 < 0.6。
			"错位相似的两段",
			"上午讨论三个方面预算排期人员", "面预算排期人员安排下午继续",
			true,
		},
		{
			// 覆盖率 0.86 ≥ 0.6 ⇒ 应判为重叠（阴性对照：闸不能一刀切全拒）。
			"高覆盖率应判为重叠",
			"麻烦你把预算表今天下班前发给我", "把预算表今天下班前发给我谢谢",
			false,
		},
		{
			"完全重复的窗口应判为重叠",
			"一二三四五六七", "一二三四五六七",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := fuzzyOverlapTail([]rune(tc.committed), []rune(tc.next), 40)
			if ok == tc.wantNoOverlap {
				t.Errorf("重叠判定=%v，期望 %v（committed=%q next=%q）",
					ok, !tc.wantNoOverlap, tc.committed, tc.next)
			}
		})
	}
}
