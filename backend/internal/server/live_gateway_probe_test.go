// 真网关探针（默认 skip）。验的是**单测验不到的那一段**：真实网关
// 到底吐不吐 tool_calls、检索结果能不能真的回到模型手里、笔记总结
// 的 JSON 有没有被模型遵守。
//
// 默认 skip 的理由：会消耗真实额度、依赖外网。显式开关：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveGateway -v
//
// ⚠ 本文件**不是**门禁。它是取证工具：跑一次，把观察写进设计文档。
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// probeNotes 满足 meetingagent.NoteSource。
//
// ⚠ 刻意用固定夹具而不是真库：本次要量的是「真网关会不会吐 tool_calls、
// 工具输出能不能回到模型手里」，**不是**检索算法本身（那由 meetingagent
// 的单测与变异覆盖）。把笔记正文里放一个**转写里绝不出现**的标记串
// （"AX-7749"），于是「最终结论里有没有它」就成了一个可判定的量：
// 那个串除了经由 search_notes 之外没有任何途径能到达模型。
type probeNotes struct{ items []notes.Note }

func (p probeNotes) ListScoped(context.Context, string, string, string) ([]notes.Note, error) {
	return p.items, nil
}

const (
	// probeTranscript 是会议转写，形态对齐真实分段拼接结果。
	probeTranscript = "张伟说北极星项目的续期合同这个月要签，让林岚去跟进。\n" +
		"另外下周三下午三点跟客户开一次评审会，会议室已经定了。"
	// probeMarker 只存在于夹具笔记里，转写里没有。
	probeMarker = "AX-7749"
)

func liveGatewayBFF(t *testing.T) *llmbff.Service {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	if baseURL == "" || apiKey == "" {
		t.Fatal("POCKET_LIVE_GATEWAY=1 但缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	// 与生产同一条构造路径（main.go → NewDynamicLLMGatewayBFFProvider），
	// 而不是自己 new 一个 llmgateway.Client —— 否则验的就不是同一段代码。
	resolve := func(_, _ string) GatewayConfig {
		cfg := GatewayConfig{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Format:  strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_FORMAT")),
		}
		// anthropic 形态实测**不接受 "auto"**（No available provider for
		// model 'auto'），必须给具体模型名，否则探针会把「网关拒绝」误报
		// 成「agent 坏了」。
		if m := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_MODEL")); m != "" {
			cfg.PreferredModels = []string{m}
		}
		return cfg
	}
	p := NewDynamicLLMGatewayBFFProvider(resolve)
	if p == nil {
		t.Fatal("NewDynamicLLMGatewayBFFProvider 返回 nil")
	}
	return llmbff.NewService(p, llmbff.NoopRecorder{})
}

func runLiveAgent(t *testing.T, bff *llmbff.Service, tools []meetingagent.Tool) *meetingagent.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	r := &meetingagent.Runner{
		Chat:        bff.Stream,
		WorkspaceID: "ws_live_probe",
		UserID:      "probe",
		Kind:        "meeting_summary_agent",
	}
	res, err := r.Run(ctx, meetingAgentSystemPrompt("", tools), probeTranscript, tools)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	return res
}

func clipRune(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// TestLiveGatewayAgentToolCall 是**A/B 对拍**，不是打印。
//
// 只有把「给了工具」和「没给工具」两臂都跑一遍，「真网关支持 tool_calls」
// 才是可判定的：单跑有工具那臂，看到 toolCalls=0 时无法区分
// 「网关不支持」与「模型这轮不想查」。对照组把后者排除了。
func TestLiveGatewayAgentToolCall(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)

	withTools := runLiveAgent(t, bff, []meetingagent.Tool{
		&meetingagent.NoteSearcher{Src: probeNotes{items: []notes.Note{{
			ID:      "note-probe-001",
			Title:   "北极星项目续期讨论",
			Content: "北极星项目的续期合同要在 11 月底前签完，负责人是林岚，备案号 " + probeMarker + "，预算 42 万。",
		}}}},
	})
	noTools := runLiveAgent(t, bff, nil)

	t.Logf("A 带工具: turns=%d toolCalls=%v truncated=%v",
		withTools.Turns, withTools.ToolCalls, withTools.Truncated)
	t.Logf("A 结论（%d 字）: %s", len([]rune(withTools.Content)), clipRune(withTools.Content, 500))
	t.Logf("B 无工具: turns=%d toolCalls=%v truncated=%v",
		noTools.Turns, noTools.ToolCalls, noTools.Truncated)
	t.Logf("B 结论（%d 字）: %s", len([]rune(noTools.Content)), clipRune(noTools.Content, 500))

	// 硬断言只钉「一定会成立」的事；网关能力这类可变量写进日志当证据。
	if strings.TrimSpace(withTools.Content) == "" {
		t.Error("A 组最终结论为空：有界循环没能收口")
	}
	if strings.TrimSpace(noTools.Content) == "" {
		t.Error("B 组最终结论为空")
	}
	// 判据自证：不注册工具就不该有工具调用，否则上面的对比无意义。
	if len(noTools.ToolCalls) != 0 {
		t.Errorf("B 组未注册工具却出现 toolCalls=%v —— 判据自身有问题", noTools.ToolCalls)
	}

	if len(withTools.ToolCalls) > 0 {
		t.Logf("★ 真网关吐了 tool_calls：%v（模型主动检索）", withTools.ToolCalls)
	} else {
		t.Logf("⚠ 真网关这一轮没有产生 tool_calls：agent 降级为一次性总结（功能仍可用，references 为空）")
	}
	// 端到端闭环的判据：夹具里的标记串只有经由工具才能到达模型。
	if strings.Contains(withTools.Content, probeMarker) {
		t.Logf("★ 工具输出真的回到了模型手里：最终结论里出现了只有 search_notes 才能给出的 %q", probeMarker)
	} else {
		t.Logf("· 最终结论未出现 %q（可能模型未引用，也可能是工具没跑；上面 toolCalls=%v 可区分）",
			probeMarker, withTools.ToolCalls)
	}
}

// TestLiveGatewayNoteSummaryJSON 验笔记总结：真网关是否遵守「严格返回 JSON」，
// 以及 parseNoteSummaryPayload 能不能真的拆出 action_items。
func TestLiveGatewayNoteSummaryJSON(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)

	body := "今天和王总确认了口袋助手的续费方案。" +
		"他说下周三下午三点之前要把报价单发过去，另外让小林准备一份对比表。"
	n := notes.Note{ID: "n1", Content: body, Snippet: clipRune(body, 200)}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := bff.Chat(ctx, llmbff.ChatRequest{
		WorkspaceID: "ws_live_probe",
		Model:       "auto",
		Messages:    []llmbff.Message{{Role: "user", Content: buildNoteSummaryPrompt(n.BodyForLLM(6000))}},
		Temperature: 0.3,
		MaxTokens:   2048,
		User:        "probe",
	}, "note_summary")
	if err != nil {
		t.Fatalf("note summary chat: %v", err)
	}

	t.Logf("网关原文（%d 字）: %s", len([]rune(resp.Content)), clipRune(resp.Content, 600))
	summary, items := parseNoteSummaryPayload(resp.Content)
	t.Logf("解析 summary（%d 字）: %s", len([]rune(summary)), clipRune(summary, 300))
	for i, it := range items {
		t.Logf("  action_items[%d]: text=%q assignee=%q due=%q", i, it.Text, it.Assignee, it.Due)
	}

	if strings.TrimSpace(summary) == "" {
		t.Error("summary 为空")
	}
	// 后半段的「小林准备对比表」在前 200 字之外，正是 BodyForLLM 修的那个点。
	if !strings.Contains(summary, "小林") && !strings.Contains(summary, "对比表") {
		t.Errorf("总结里没有正文后半段的信息（小林/对比表）—— BodyForLLM 可能没生效或模型漏了；原文=%q",
			clipRune(resp.Content, 300))
	}
	if len(items) == 0 {
		t.Error("action_items 为空：真网关没遵守 JSON 契约，或解析回落了")
	}
}

// probeMeetingSegs 是一段**形态贴近真实**的切段转写：带说话人、有口语、
// 有重复与口头禅，篇幅中等（几百字）。用来验「精校」这条链。
var probeMeetingSegs = []meetingSegmentIn{
	{Speaker: "张伟", Text: "那个，我们这个季度的续费方案Basically已经定了啊"},
	{Speaker: "李娜", Text: "定了吗？我这边还看到有两个客户没签"},
	{Speaker: "张伟", Text: "对，没签的那两个走线下，就是那个王总和林岚那边"},
	{Speaker: "李娜", Text: "嗯，然后呢"},
	{Speaker: "张伟", Text: "下周三下午三点我们跟客户开一次评审会，把这个方案再过一遍"},
	{Speaker: "李娜", Text: "评审会我准备一下对比表，主要是跟旧的版本有什么差异"},
	{Speaker: "张伟", Text: "可以，另外续期合同要在十一月底之前签完，这个是硬指标"},
	{Speaker: "李娜", Text: "明白，我来跟"},
	{Speaker: "张伟", Text: "好，那今天就到这里"},
}

// TestLiveGatewayMeetingRefine 验「精校」在真实网路上真的产出精校结果。
//
// 为什么单独验：这是用户原话里「录音完成后，可能还需要一次精校」的落点，
// 而它此前只有单测。风险点很具体 —— llmChatOnce **不设 MaxTokens**，
// 而 refine 的提示词要求模型吐出**整篇** refined_transcript 加上
// structured_minutes。若预算不足导致 JSON 截断，parseRefineJSON 失败，
// 处理器会**静默回落到原始转写** ⇒ 用户看到「精校了」，实际什么都没做。
func TestLiveGatewayMeetingRefine(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m1/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	raw := segmentsToText(probeMeetingSegs)
	t.Logf("原始转写（%d 字）: %s", len([]rune(raw)), clipRune(strings.TrimSpace(raw), 300))

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	res, err := s.llmMeetingRefine(ctx, r, probeMeetingSegs, nil, meetingMetaIn{})
	if err != nil {
		t.Fatalf("llmMeetingRefine: %v", err)
	}

	refined, _ := res["refined_transcript"].(string)
	t.Logf("精校结果（%d 字）: %s", len([]rune(refined)), clipRune(refined, 400))
	if sm, ok := res["structured_minutes"]; ok {
		t.Logf("structured_minutes: %v", sm)
	}
	if todos, ok := res["todos"]; ok {
		t.Logf("todos: %v", todos)
	}

	// 硬断言：精校必须真的改变了内容。回落分支会原样返回 raw，
	// 那正是「看起来成功、实际没做」的形态。
	if strings.TrimSpace(refined) == "" {
		t.Fatal("refined_transcript 为空")
	}
	if strings.TrimSpace(refined) == strings.TrimSpace(raw) {
		t.Error("精校结果与原始转写逐字相同 ⇒ 走了静默回落分支，JSON 解析失败（预算不足/模型不守契约）")
	}
	if refined == raw {
		t.Logf("· 注意：refined_transcript 与 raw 完全一致（含换行），需人工确认是回落还是模型原样返回")
	}
}

// TestLiveGatewayMeetingRefineLongTranscript 探「精校」在**长转写**下的边界。
//
// 动机：llmChatOnce 不设 MaxTokens，而 refine 的输出里含**整篇**
// refined_transcript + structured_minutes。短输入（221 字）实测通过，
// 但长会议一旦让 JSON 截断，parseRefineJSON 就会失败，处理器**静默回落**
// 成原始转写 —— 用户看到「精校完成」，实际什么都没做。
// 这个形态靠单测看不出来（假件总是返回完整 JSON）。
func TestLiveGatewayMeetingRefineLongTranscript(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m2/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 把基准段扩到约 8 倍，模拟一场两小时会议的转写量。
	var segs []meetingSegmentIn
	for round := 0; round < 8; round++ {
		for i, base := range probeMeetingSegs {
			segs = append(segs, meetingSegmentIn{
				Speaker: base.Speaker,
				Text:    base.Text,
				Lang:    base.Lang,
				StartMs: (round*len(probeMeetingSegs) + i) * 30000,
			})
		}
	}
	raw := segmentsToText(segs)
	t.Logf("原始转写（%d 字）", len([]rune(raw)))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	res, err := s.llmMeetingRefine(ctx, r, segs, nil, meetingMetaIn{})
	if err != nil {
		t.Fatalf("llmMeetingRefine: %v", err)
	}

	refined, _ := res["refined_transcript"].(string)
	t.Logf("精校结果（%d 字）: %s", len([]rune(refined)), clipRune(refined, 300))
	if sm, ok := res["structured_minutes"]; ok {
		t.Logf("structured_minutes: %v", sm)
	}

	if strings.TrimSpace(refined) == "" {
		t.Fatal("refined_transcript 为空")
	}
	if strings.TrimSpace(refined) == strings.TrimSpace(raw) {
		t.Error("长转写下精校结果与原文逐字相同 ⇒ 静默回落（JSON 被截断或模型不守契约）")
	}
	// 长输入下最容易丢的是结构化字段：它们排在 refined_transcript 之后。
	if sm, ok := res["structured_minutes"].(map[string]any); ok {
		if ai, _ := sm["action_items"].([]any); len(ai) == 0 {
			t.Error("长转写下 action_items 为空：结构化纪要很可能被截断丢掉了")
		}
	} else {
		t.Error("structured_minights 不是对象，结构化纪要缺失")
	}
}

// TestLiveGatewayMeetingRecommend 验「相关推荐」在真实网路上真的产出条目。
//
// 这是需求「总结同时给出参考资料与建议」的后半段：agent 的 references 已验，
// 但 recommend（另一条独立链路，`llmMeetingRecommend`）此前只有假件。
// 它同样有「静默降级」风险：解析失败时返回空数组，而前端对空数组
// 不给任何提示（推荐是增强，不该让总结整块报错）。
func TestLiveGatewayMeetingRecommend(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m3/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	segs := []meetingSegmentIn{
		{Speaker: "张伟", Text: "我们这个季度的续费方案基本上已经定了"},
		{Speaker: "李娜", Text: "下周三下午三点跟客户开评审会，王总和林岚那边走线下签约"},
		{Speaker: "张伟", Text: "另外续期合同要在十一月底之前签完"},
	}
	summary := "季度续费方案确定；未签客户转线下；下周评审会；合同月底前签完"

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	items, err := s.llmMeetingRecommend(ctx, r, segs, summary)
	if err != nil {
		t.Fatalf("llmMeetingRecommend: %v", err)
	}
	for i, it := range items {
		t.Logf("items[%d]: %v", i, it)
	}
	t.Logf("条数=%d", len(items))

	if len(items) == 0 {
		t.Fatal("推荐为空：真网关没守 JSON 契约，或 parseRecommendJSON 把它吃空了（静默降级）")
	}
	// 逐条核**真实契约**。
	//
	// ⚠ 第一版断言查的是 `query` 字段，读数显示它不存在 —— 但那不是缺陷：
	// parseRecommendJSON 有意把 query 折进 url（前端 onOpenRelated 见到 url
	// 才新开窗口，没有 url 的 web 条目点击会静默无反应）。
	// ⇒ 判据的锚点必须对齐**契约**，不是对齐「我以为该有的字段」。
	for i, it := range items {
		title, _ := it["title"].(string)
		rawURL, _ := it["url"].(string)
		typ, _ := it["type"].(string)
		if strings.TrimSpace(title) == "" {
			t.Errorf("items[%d] 缺 title：%v", i, it)
		}
		if typ != "web" {
			t.Errorf("items[%d] type=%q，期望 web（前端按 type 分派点击行为）", i, typ)
		}
		const prefix = "https://www.bing.com/search?q="
		if !strings.HasPrefix(rawURL, prefix) {
			t.Errorf("items[%d] url=%q，不是可点的搜索链接（前端会静默无反应）", i, rawURL)
			continue
		}
		// 解回来核「关键词」这件事：必须是合法转义，且短（提示词要求中文关键词、非整句）。
		kw, err := url.QueryUnescape(strings.TrimPrefix(rawURL, prefix))
		if err != nil {
			t.Errorf("items[%d] url 的查询串没有正确转义：%v", i, err)
			continue
		}
		if strings.TrimSpace(kw) == "" {
			t.Errorf("items[%d] 搜索关键词为空：%v", i, it)
		}
		if r := []rune(kw); len(r) > 40 {
			t.Errorf("items[%d] 关键词长达 %d 字，像整句而不是关键词：%q", i, len(r), kw)
		}
	}
}

// probeRollingSegs 模拟一场会议被逐段转写出来的样子。
var probeRollingSegs = []meetingSegmentIn{
	{Speaker: "张伟", Text: "我们这个季度的续费方案基本上已经定了"},
	{Speaker: "李娜", Text: "没签的那两个走线下，王总和林岚那边"},
	{Speaker: "张伟", Text: "另外续期合同要在十一月底之前签完，这是硬指标"},
	{Speaker: "李娜", Text: "下周三下午三点跟客户开评审会，我准备对比表"},
	{Speaker: "张伟", Text: "预算这块按老方案走，不追加"},
}

// TestLiveGatewayRollingSummary 量「录音时的即时总结」在真实网关上的多轮行为。
//
// 这就是用户说的「在录音时需要进行即时总结」的真实形态：录音进行中每隔一段
// 调一次，并把上一轮的 summary 当作 prev 传回去。**多轮累积**是最容易出问题
// 的地方（行动项被后续轮次挤掉、要点漂移、JSON 契约在第二轮松掉），而此前
// 没有任何测试覆盖多轮。
//
// 特别关注一个具体隐患：llmMeetingSummary 在 prev != "" 时把提示词换成
// 「请更新摘要，严格返回**相同** JSON 格式」—— 这一条消息里从没给过格式，
// 「相同」无所指。真实模型会不会照旧守契约，只有跑了才知道。
func TestLiveGatewayRollingSummary(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m4/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	prev := ""
	var prevItems []string
	for round := 1; round <= len(probeRollingSegs); round++ {
		segs := probeRollingSegs[:round]
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		res, err := s.llmMeetingSummary(ctx, r, segs, prev)
		cancel()
		if err != nil {
			t.Fatalf("第 %d 轮总结失败：%v", round, err)
		}
		summary, _ := res["summary"].(string)
		var items []string
		// ⚠ 2026-10-07：必须把 assignee / due 一起打出来。
		//
		// 这个探针一直只打 `text`，于是「时间点到底进没进日程」这件事
		// **从来没被观测过** —— 而前端 resolveTodoDue 读的是 action item 的
		// `due` 字段，不是摘要正文。
		//
		// 实测（auto 臂第 4 轮）：摘要正文里明明白白写着「下周三下午三点」，
		// 而 action_items 的 text 是「与客户召开评审会」—— **时间不在 text 里**。
		// 那么它到底在不在 `due` 里，就是决定用户那条需求成败的最后一环。
		var dueSeen []string
		var pairs []string
		if raw, ok := res["action_items"].([]any); ok {
			for _, it := range raw {
				if m, ok := it.(map[string]any); ok {
					txt := fmt.Sprint(m["text"])
					due := fmt.Sprintf("%v", m["due"])
					items = append(items, txt)
					dueSeen = append(dueSeen, due)
					// ★ 2026-10-07：text 与 due **分开打印**没法回答「时间点
					//   是挂在哪条上、这一轮还在不在」—— 那正是本轮要定的论。
					pairs = append(pairs, fmt.Sprintf("%s ⇒ %s", clipRune(txt, 28), due))
				}
			}
		}
		kps, _ := res["key_points"].([]any)
		t.Logf("第 %d 轮（累计 %d 段）", round, round)
		t.Logf("   summary（%d 字）: %s", len([]rune(summary)), clipRune(summary, 200))
		t.Logf("   key_points=%d action_items=%d %v", len(kps), len(items), items)
		t.Logf("   action_items[].due = %v", dueSeen)
		t.Logf("   ★ text⇒due 配对 = %v", pairs)

		// 硬断言 1：每一轮都必须产出非空 summary（第 2 轮起走的是 prev 分支，
		// 格式指令缺失，最可能在这里空掉）。
		if strings.TrimSpace(summary) == "" {
			t.Fatalf("第 %d 轮 summary 为空", round)
		}
		// ⚠ 这里**不**断言「行动项必须逐轮变多」—— 那是我的假设，不是产品契约。
		//
		// 真实模型会**合并**语义重复的条目，合并后条数减少是合理的。
		//
		// ⚠⚠ 2026-10-07 订正：这段注释原本写着「产品侧 createMeetingTodos 是
		// **增量式**（每次 INSERT 新行 + 新建提醒），早先建好的提醒不会被删，
		// 所以『变少』不等于信息丢失」。**那句是错的**，而且错得让人放心：
		//   - createMeetingTodos 在会议侧是 `MeetingDetailView` 里的**手动**动作；
		//   - 自动那条路是 `ingestMeetingArtifacts`，它**整个会议只跑一次**
		//     （录音结束 → 精校 → 入库），读的是**最后一轮**的 actionItems；
		//   - 所以「早先建好的提醒」根本不存在——每轮滚动**没有**建过任何提醒。
		//
		// ⇒ 真正的丢失点在更前面：`useLiveSummary` 每轮
		//   `liveSummary.value = toLiveSummary(result)` **整体覆盖**并落库，
		//   历史轮次的行动项在前端就被抹除，活不到 ingest。
		//
		// ⇒ 而且「变少」未必等于时间点丢失：模型可能把 due 挂到**另一条**
		//   同义条目上（实测 auto 臂第 5 轮 `下周三评审会前` 仍在，只是宿主换了）。
		//   判断标准必须是 **text⇒due 配对**，不是条目数——所以本探针
		//   现在同时打印 `text ⇒ due`。
		if round > 1 && len(items) < len(prevItems) {
			t.Logf("· 第 %d 轮行动项由 %d  consolidates 为 %d 条：%v",
				round, len(prevItems), len(items), items)
		}
		// 硬断言 3：同一个行动项不该在同一轮里出现两次。
		seen := map[string]bool{}
		for _, it := range items {
			if seen[it] {
				t.Errorf("第 %d 轮行动项重复：%q", round, it)
			}
			seen[it] = true
		}
		prev = summary
		prevItems = items
	}
	t.Logf("最终 summary（%d 字）: %s", len([]rune(prev)), clipRune(prev, 300))
}

// TestLiveGatewayRefineFixesASRErrors 验「录音后精校」到底能不能修 ASR 的错。
//
// 动机（2026-10-07）：§27 用 TTS 造语料测出 `mimo-v2.5-asr` 的
// CER = 7.26%，且错误**高度集中在中英混排**：
//
//	CI          → 页百零一        （两个字母被读成中文谐音）
//	user_id     → user下划线ID     （下划线被当成一个词）
//	Kafka       → Tafad
//	changelog   → Challog
//	二〇二六年    → 二零二六年
//
// 用户需求第一条是「在录音完成后，可能还需要一次精校」。§17/§21 只验了
// 精校**链路通不通、失败会不会静默伪装成成功**，从没验过它**准不准** ——
// 而「准不准」才是这条需求存在的理由。
//
// ⇒ 本探针把 §27 的**真实错误样本**喂进生产同款路径
//
//	（`Server{llmBFF: bff}` → `llmMeetingRefine`，与
//	handleMeetingRefine 的 LLM 兜底分支完全同一条），
//	看精校能不能把英文标识符修回来。
//
// ⚠ 判据只报读数不判定对错：LLM 修得对不对不是二值问题，
//
//	逐条列出来由人读。写死一个「必须 100% 修好」的断言只会让下一个人
//	去调提示词调到过拟合。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveGatewayRefineFixesASRErrors -v -timeout 2400s
//
// ⚠⚠ `-timeout` **不是可选的**（2026-10-11 实测，§476）：
//
//	本探针是 14 条 × 2 臂 = **28 次**调用，实测 20–60s/条 ⇒ 560–1680s。
//	而 `go test` 的默认 `-timeout` 是 **10 分钟（600s）**
//	⇒ 照本注释原来那条命令跑，**必然中途被杀**，
//	而被杀之前打出来的逐条读数**看起来和完整结果一模一样**。
//	⚠ 本仓 **15 个真网关探针里有 14 个的用法注释都漏了这个 flag**。
func TestLiveGatewayRefineFixesASRErrors(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关精校质量探针：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m1/actions", nil)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ 这里**不再**建全局 ctx：第一版那个写死的 300s 预算
	// 就是「跑完 5/14 却照样报小结」的成因。超时下沉到每次调用。

	// ── 配对设计：每条样本都跑两臂（OFF=清空 meta，ON=带 meta）──
	//
	// 为什么不能「跑两趟再 diff」：只跑 ON 臂拿到的数字和 §30 的历史读数
	// **不可比** —— 模型版本会漂、样本会扩，那两个变量都会让差异失去归因。
	// 要切干净的只有「有没有元数据」这一个变量。
	//
	// ⚠ 第一版是「跑两趟、用脚本 diff」。第一版直接产出的读数是**不可用的**，
	// 两个原因都是量具的错，不是产品的错：
	//
	//	① ctx 超时**写死 300s**。14 条样本在 ~20-60s/条下只跑完 5 条，
	//	   剩下 3 条 `context deadline exceeded`，而末尾照样打印
	//	   「小结：修好 3 / 改错 0 / 原样 1」——
	//	   **截断的跑被当成完整结果上报**。这与 §11/§8/§17 是同一族：
	//	   失败被吞掉，读者拿到一个看起来干净的数。
	//	   ⇒ 改为**每次调用独立超时**，且失败必须计入分母并显式打印。
	//
	//	② 同一份输入在同一份代码上给出了不同结果：第一条「字母读成谐音」
	//	   在 OFF 臂被判「✅ 修好」、在 ON 臂被判「— 原样返回」，而这条样本
	//	   **两臂的提示词逐字相同**（它没有 meta）。⇒ 模型侧非确定。
	//	   在这个前提下，「OFF vs ON 的差异」里混着一份测不掉的模型噪声，
	//	   **不能归因到「加了元数据」这一个变量上**。
	//
	// 配对设计把这两条都解决掉：
	//
	//	- 11 条**无 meta** 的对照样本在两臂里输入逐字相同 ⇒ 它们两臂的差异
	//	  就是**噪声底噪**的直接测量值，而不是靠猜。目标组那 3 条的差异
	//	  必须**明显大于**底噪，才谈得上是元数据的功劳。
	//	- 每次调用独立 ctx ⇒ 单条慢不会饿死其余样本。
	//
	// 判定只对「修好 / 原样 / 改错」三态，**不给通过阈值** ——
	// n=14 配 28 次调用，这个样本量支撑不起显著性检验。
	// 这里要回答的是「方向对不对、代价有没有上升」，不是「提升了多少」。
	type verdict struct {
		mark   string // "修好" / "原样" / "改错" / "失败"
		got    string
		detail string
	}
	// retryCount 统计「因为网关抖动而重试」的次数。
	// 它要出现在小结里：重试次数高说明这轮读数是在抖动中取得的，
	// 而**抖动会同时抬高噪声底噪**（§35 实测：重试后的噪声率并不低）。
	retryCount := 0
	runArm := func(cIdx int, meta meetingMetaIn) verdict {
		c := refineProbeCases()[cIdx]
		// ⚠ 每次调用独立超时 + 有限重试。
		//
		// 第一版只有超时没有重试，而网关在实测时段抖动明显
		//（`http2: timeout awaiting response headers`）。单次网络抖动
		// 与「模型这么答」是两件事，把它记成前者会把分母啃掉一块 ——
		// 而啃掉的分母**不可见**，小结照样打印一个好看的数。
		//
		// 判别方式：重试后仍失败才算失败，且把尝试次数一并报出来。
		const maxTry = 3
		var lastErr error
		for try := 1; try <= maxTry; try++ {
			if try > 1 {
				retryCount++
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			res, err := s.llmMeetingRefine(ctx, r, []meetingSegmentIn{{
				Speaker: "发言人", Text: c.asrOut, Lang: "zh",
			}}, nil, meta)
			cancel()
			if err != nil {
				lastErr = err
				t.Logf("   … %s 第 %d/%d 次尝试失败：%v", c.name, try, maxTry, err)
				continue
			}
			// ⚠⚠ 必须先分清 refine_rejected 与 refine_fallback —— 两者都置
			// refine_fallback，但**原因完全不同**，桶名必须对上承诺：
			// 前者是兜底拦下了名单词污染（预期行为，是 §35 的成果），
			// 后者是模型没遵守 JSON 契约（§17 查的缺陷）。第一版只查后者，
			// 于是兜底生效时探针报「JSON 契约没遵守」——
			// **判据在量 A，标签在说 B**，读数会把成果报成缺陷。
			if rj, _ := res["refine_rejected"].(bool); rj {
				return verdict{"兜底拦下", fmt.Sprint(res["refined_transcript"]),
					"结果里混进了转写中不存在的参会人，已整体回落原文（预期行为）"}
			}
			if fb, _ := res["refine_fallback"].(bool); fb {
				// ⚠ 降级不是网络抖动，它本身就是被测对象（§17 查的那条），
				// 所以**不重试** —— 重试只会掩盖它。
				return verdict{"失败", "", fmt.Sprintf("refine_fallback=true（JSON 契约没遵守），第 %d 次尝试", try)}
			}
			got := fmt.Sprint(res["refined_transcript"])
			if try > 1 {
				t.Logf("   … %s 第 %d 次尝试才成功（说明前几次是网关抖动，不是结论）", c.name, try)
			}
			// 「修好」的判据：ASR 版本里的错误标识符不见了，且正确标识符出现了。
			//
			// ⚠ 大小写不敏感：第二版实测里模型输出的是 `Changelog`（大写 C），
			// 而判据查的是小写 `changelog` —— 一条**正确的修复**被判成「改错」。
			// 「修好 1 / 改错 2」那个读数就是这么来的：它是量具坏了，不是产品坏了。
			// （本会话第三次自己写坏判据：§17 演示 payload、§23 分句断言、本条。）
			low := strings.ToLower(got)
			for _, rule := range c.rules {
				if !strings.Contains(low, strings.ToLower(rule.wrong)) &&
					strings.Contains(low, strings.ToLower(rule.right)) {
					return verdict{"修好", got, ""}
				}
			}
			if normSpace(got) == normSpace(c.asrOut) {
				return verdict{"原样", got, ""}
			}
			if len([]rune(normSpace(got))) > len([]rune(normSpace(c.asrOut)))+2 {
				return verdict{"改错", got, "加了原文没有的内容"}
			}
			return verdict{"改错", got, "改了但没改对"}
		}
		return verdict{"失败", "", fmt.Sprintf("重试 %d 次仍失败：%v", maxTry, lastErr)}
	}

	noiseSame, noiseDiff, noiseN := 0, 0, 0 // 对照组：两臂输入逐字相同，差异即噪声
	onOnly, offOnly := 0, 0                 // 目标组：仅某一臂修好
	// ⛔ §483：目标组**必须四档都计数**。原先只有 onOnly/offOnly，
	// 而小结把 `onOnly+offOnly` 当成「需元数据的样本 N 条」打印 ——
	// 于是「两臂都修好」与「两臂都没修好」两档既没进统计、
	// 又让那个 N **小于真实样本数**。
	// 读数长这样：
	//
	//	【目标组】需元数据的样本 0 条：仅 ON 修好 0 / 仅 OFF 修好 0
	//	⇒ 净效应为零：加了元数据没换来任何修复。
	//
	// 而真实情况是 2 条目标样本**都跑过了**。「0 条」不是「没样本」，
	// 是「没有一条出现臂间差异」—— 这两件事的处置完全不同：
	// 前者说明实验量不到目标，后者说明**目标对两臂都没产生区分度**。
	targetN, bothFixed, neitherFixed := 0, 0, 0
	negCtrl, negLeak := 0, 0 // 负控：条数 / 名单词漏进输出的条数
	guardedN := 0            // 被代码兜底拦下的条数（预期行为，不是缺陷）
	failed := 0
	// ⭐ §488：本轮做了几项必须对账。
	//
	// §476.1 ① 的原始形态是「ctx 写死 300s ⇒ 14 条只跑完 5 条 ⇒
	// 末尾照样打印『修好 3 / 改错 0 / 原样 1』」——
	// **截断的跑被当成完整结果上报**。
	// §476 给本探针补了 `-timeout 2400s`，但那防不住**下一次**网关变慢。
	//
	// ⚠ 这里**不做耗时预估**：本探针耗时实测过（560–1680s），
	// 而其余 14 个探针**没测过** —— 填没测过的数字正是我反复拒绝做的事。
	// ⇒ 只记「做没做完」：**被砍在第几项都会出声**，与 `-timeout` 取值无关。
	ledger := livetest.New("精校探针", len(refineProbeCases()))
	for i, c := range refineProbeCases() {
		off := runArm(i, meetingMetaIn{})
		on := runArm(i, glossaryArm(c.meta))
		if off.mark == "失败" || on.mark == "失败" {
			failed++
		}
		hasMeta := c.meta.Title != "" || len(c.meta.Participants) > 0

		verdict := "—"
		switch c.kind {
		case "对照":
			noiseN++
			if off.mark == on.mark {
				noiseSame++
				verdict = "对照组·两臂一致"
			} else {
				noiseDiff++
				verdict = "⚠ **对照组·两臂不同 ⇒ 这是噪声不是元数据**"
			}
		case "目标":
			targetN++
			switch {
			case off.mark != "修好" && on.mark == "修好":
				onOnly++
				verdict = "✅ **仅含元数据时修好**（§31 生效）"
			case off.mark == "修好" && on.mark != "修好":
				offOnly++
				verdict = "⚠ 仅无元数据时修好（元数据反而干扰）"
			case off.mark == "修好" && on.mark == "修好":
				// ⚠⚠ §483：这一档原先落在 default 里、**一条都没被计数**，
				// 而它恰恰是「模型自己就能修」的那一档 ——
				// 对 #29（术语表扩源）来说，**这一档才是天花板**：
				// 说明这类错误不需要外部证据也能修，加证据的边际收益就此归零。
				bothFixed++
				verdict = "— 两臂都修好（模型自身即可，不构成元数据的功劳）"
			default:
				neitherFixed++
				verdict = "❌ 两臂都没修好（OFF=" + off.mark + " ON=" + on.mark + "）"
			}
		case "负控":
			// ⚠ 负控问的是**代价**，不是收益：名单给了模型之后，
			// 它会不会把名单里的词塞进转写、或把转写里的词「对齐」进名单。
			// 判据不看「读起来像不像」，直接量**名单词有没有出现在输出里**。
			negCtrl++
			leak := rosterLeak(on.got, c.meta, c.asrOut)
			if leak != "" {
				negLeak++
				verdict = "❌ **名单词 " + leak + " 漏进了输出**（termHint 引入的误改）"
			} else {
				if on.mark == "兜底拦下" {
					verdict = "✅ 被代码兜底拦下并回落原文（名单词没进最终输出）"
				} else {
					verdict = "✅ 名单词一个都没漏进输出"
				}
			}
		default:
			verdict = "⚠ 样本未标注 kind，不计入任何统计"
		}

		t.Logf("[%-12s] %s", c.name, verdict)
		t.Logf("   ASR     : %s", c.asrOut)
		t.Logf("   正确答案: %s", c.correct)
		if hasMeta {
			t.Logf("   元数据  : 主题=%q 参会人=%v（GLOSSARY=%s）", c.meta.Title, c.meta.Participants,
				map[bool]string{true: "all", false: strings.TrimSpace(os.Getenv("POCKET_REFINE_GLOSSARY"))}[strings.TrimSpace(os.Getenv("POCKET_REFINE_GLOSSARY")) == "" ||
					strings.TrimSpace(os.Getenv("POCKET_REFINE_GLOSSARY")) == "all"])
		} else {
			t.Logf("   元数据  : （无，两臂提示词逐字相同）")
		}
		t.Logf("   OFF(无元数据): [%s] %s", off.mark, off.got)
		if off.detail != "" {
			t.Logf("                  ↑ %s", off.detail)
		}
		if off.mark == "失败" {
			t.Logf("                  ↑ 失败原因：%s", off.detail)
		}
		t.Logf("   ON (含元数据): [%s] %s", on.mark, on.got)
		if on.detail != "" {
			t.Logf("                  ↑ %s", on.detail)
		}
		if on.mark == "失败" {
			t.Logf("                  ↑ 失败原因：%s", on.detail)
		}
		ledger.Complete(c.name)
	}

	// ⚠⚠ 必须**在打印汇总之前**对账 —— 否则就已经把不完整的分母印出去了，
	//   而那正是本条要防的那一形态（§476.1 ①）。
	if msg := ledger.Report(); msg != "" {
		t.Error(msg)
	}
	// ⚠⚠ §492：分母必须**来自账本**而不是另算一遍。
	//
	// 原先 `total := len(refineProbeCases())` 与账本是两个独立来源 ——
	// 删掉汇总那行的话，读者看不到「跑了几项/共几项」，
	// 而 `Report()` 只在不完整时报错，**跑满时一个字都不说**。
	// ⇒ 打印 `ledger.Done()/ledger.Total()`：它们在跑满时也说话。
	total := ledger.Total()
	t.Log("")
	t.Logf("═ 配对结果（账本 %d/%d 项 × 2 臂 = %d 次调用）",
		ledger.Done(), ledger.Total(), total*2)
	t.Logf("  失败调用：%d / %d（重试 %d 次）", failed, total*2, retryCount)
	if failed > 0 {
		t.Logf("  ⚠⚠ 有 %d 次调用失败 —— **上面的分母不完整**，", failed)
		t.Log("    失败样本不计入任何统计，但也不能当「没修好」：未知，不是零。")
	}
	t.Log("")
	t.Logf("  【噪声底噪】对照组 %d 条（两臂输入逐字相同）：", noiseN)
	t.Logf("    两臂一致 %d / 两臂不同 %d", noiseSame, noiseDiff)
	if noiseN > 0 {
		t.Logf("    ⇒ 噪声率 %d/%d（%.0f%%）。目标组的差异必须**大于**这个底噪，",
			noiseDiff, noiseN, 100*float64(noiseDiff)/float64(noiseN))
		t.Log("      否则就是噪声穿了个马甲。")
	}
	// ⚠⚠ §483：分母必须是**真实跑过的目标样本数**，不是「臂间有差异的条数」。
	//   这不是措辞问题：两者能同时成立（目标样本存在但两臂表现一致），
	//   而旧写法在那种情况下会打印「样本 0 条」——
	//   **读起来像「没样本」，实际是「样本都在、只是没区分度」**。
	t.Logf("  【目标组】需元数据的样本 %d 条：", targetN)
	t.Logf("    两臂都修好 %d（模型自身即可） / 仅 ON 修好 %d / 仅 OFF 修好 %d / 两臂都没修 %d",
		bothFixed, onOnly, offOnly, neitherFixed)
	if targetN == 0 {
		// 与 §474.4 的漏类别、§481 的空档同形：表照旧打印、退出码照旧 0，
		// 与「没跑」完全同形 ⇒ 必须自己出声。
		t.Errorf("目标组样本数为 0 ⇒ 本轮**没有量到 termHint 的目标**（§31）")
	} else if onOnly == 0 && offOnly == 0 {
		t.Logf("    ⇒ %d 条目标样本**两臂无差异**。这不是「净效应为零」，而是：", targetN)
		if bothFixed > 0 {
			t.Logf("      其中 %d 条模型**自己就修好了** ⇒ 这一类的修复不需要外部证据，", bothFixed)
			t.Log("      对 #29（术语表扩源）而言，这一档就是**收益天花板**。")
		}
		if neitherFixed > 0 {
			t.Logf("      其中 %d 条**两臂都没修好** ⇒ 加了元数据也确实修不动，", neitherFixed)
			t.Log("      这一档需要的是别的手段（§477 实测：LLM 精校会把它改得更错）。")
		}
		t.Log("      ⚠ 与「没跑目标组」是两回事：样本在这里，只是没区分度。")
	}
	if bothFixed+neitherFixed+onOnly+offOnly != targetN {
		t.Errorf("目标组四档合计 %d ≠ 样本数 %d ⇒ 有一档被漏计数了",
			bothFixed+neitherFixed+onOnly+offOnly, targetN)
	}
	t.Logf("  【负控】有名单但转写正确的样本 %d 条：名单词漏进输出 %d 条", negCtrl, negLeak)
	if guardedN > 0 {
		t.Logf("  【兜底】代码兜底拦下 %d 条（含元数据时模型引入名单词，已整体回落原文）", guardedN)
	}
	if negLeak == 0 {
		t.Log("    ⇒ 没有观察到 termHint 引入的**替换/新增**类误改。")
		t.Log("    ⚠ 但这不等于「没有任何代价」：负控里模型仍可能改写别的地方，")
		t.Log("      那类改动要看上面逐条的 OFF/ON 对照，判据只量名单词泄漏。")
	}
}

// rosterLeak 返回**第一个**「原本不在转写里、却出现在输出里」的名单词，没有则返回空串。
//
// ⚠ 为什么不直接用「非空相等」当安全判据：负控的失败形态是
//
//	① 替换：转写里的「张伟」被换成名单里的名字
//	② 新增：名单里的名字被塞进转写没出现的位置
//
// 两者在输出里都表现为「某个名单词冒出来了」。只查「名单词是否出现」
// 能同时抓住这两类 —— 它是这两类失败的**必要条件**。
//
// ⚠⚠ **orig 是必需的，不是可选的优化**。第一版没要 orig，
//
//	于是负控样本「张伟和李娜都会参加」配名单 `[李娜, 王强]` 报了一次
//	「名单词 李娜 漏进输出」—— 而模型返回的是**原样**，一个字没改。
//	「李娜」出现在输出里，是因为它**本来就在转写里**。
//
//	⇒ 这是一条**恒真**的判据踩中了一个恰好不该命中的样本：
//	「漏进输出」这个桶名当时对外承诺的是「模型自己加的」，
//	而它实际承诺的是「输出里出现了名单词」—— 两件事。
//	桶名与承诺不符，就是这个仓库最熟的那个缺陷家族（§35.6 之前的
//	compact-table-census 同型）。**判据红/绿都必须先问「它在量哪一件事」。**
//
// ⚠ 剩下的已知上限：标题按**整串**匹配，只插入标题某个片段时抓不到。
func rosterLeak(got string, meta meetingMetaIn, orig string) string {
	if got == "" {
		return ""
	}
	isNew := func(w string) bool {
		w = strings.TrimSpace(w)
		// 原文里本来就有 ⇒ 不算模型加的
		return w != "" && !strings.Contains(orig, w) && strings.Contains(got, w)
	}
	for _, p := range meta.Participants {
		if isNew(p) {
			return strings.TrimSpace(p)
		}
	}
	if isNew(meta.Title) {
		return strings.TrimSpace(meta.Title)
	}
	return ""
}

// refineProbeCase / refineProbeCases：精校探针的样本表。
//
// ⚠ 提到包级不是为了整洁，是因为**门禁必须引用真数据**。
//
// 之前这份样本表是探针函数里的局部变量，于是门禁只能自己抄一份 ——
// 结果变异 L3（把负控名单改成与转写重叠）和 L4（删掉一条负控）
// 都**改了真数据却门禁全绿**：门看的是它自己那份副本。
// 这就是「夹具太假」那一类：夹具与被测物各走各的，
// 于是门量的是「副本还对不对」，不是「产品还对不对」。
type refineProbeCase struct {
	name, correct, asrOut string
	rules                 []struct{ wrong, right string }
	meta                  meetingMetaIn
	kind                  string // "对照"（无 meta，量能力+噪声） / "目标"（有 meta，量收益） / "负控"（有 meta，量代价）
	// class 只对 kind=="目标" 有意义，取值 "人名" / "音译专名" / "标识符"。
	//
	// ⚠⚠ 为什么必须是**显式字段**而不能靠字符串特征推断（§486.4 实测）：
	//
	//	第一版的门用「`right` 里含 ASCII 字母/数字」当「有标识符样本」的判据，
	//	而删除全部三条标识符样本后门**照样全绿** ——
	//	因为「音译专名」那条的 `right` 是 `Polaris`，**也是 ASCII**。
	//	⇒ 判据分不开这两类，却假装能分。
	//	⇒ **分不开的判据不该用来守东西**；只能让语料自己声明。
	class string
}

func refineProbeCases() []refineProbeCase {
	return []refineProbeCase{
		// ── 对照组（无 meta）：termHint 为空，测的是纯语言模型能力 ──
		{name: "字母读成谐音", correct: "把 user_id 改成 uid，然后跑一遍 CI 看有没有回归。",
			asrOut: "把user下划线ID改成UID，然后跑一遍页百零一，看有没有回归。",
			rules:  []struct{ wrong, right string }{{"页百零一", "CI"}, {"下划线", "user_id"}}, kind: "对照"},
		{name: "术语听错", correct: "请检查一下 Redis 和 Kafka 的 consumer lag 是否正常。",
			asrOut: "请检查一下Redis和Tafad的consumerlag是否正常。",
			rules:  []struct{ wrong, right string }{{"Tafad", "Kafka"}}, kind: "对照"},
		{name: "术语+下划线", correct: "请把接口升级到 v2.5 版本，并同步更新 changelog。",
			asrOut: "请把接口升级到v2.5版本，并同步更新Challog。",
			rules:  []struct{ wrong, right string }{{"Challog", "changelog"}}, kind: "对照"},
		{name: "同音数字（应原样保留）", correct: "二〇二六年十月七号是周三，下周一就是十号。",
			asrOut: "二零二六年十月七号是周三，下周一就是十号。",
			rules:  nil, kind: "对照"},
		{name: "git→接单", correct: "记得先 git pull，然后 rebase 一下，别直接 force push。",
			asrOut: "接单先给pull,然后rebase一下,别直接forcepush。",
			rules:  []struct{ wrong, right string }{{"接单", "git"}, {"给pull", "git pull"}}, kind: "对照"},
		{name: "conf→康复", correct: "配置文件在 conf 目录下的 app.yaml 文件里。",
			asrOut: "配置文件在康复目录下的app文件里。",
			rules:  []struct{ wrong, right string }{{"康复", "conf"}, {"app文件", "app.yaml"}}, kind: "对照"},
		{name: "版本号读成中文", correct: "我们从 1.0 升级到 2.0，中间要兼容 1.5 的客户端。",
			asrOut: "我们从一点零升级到二点零,中间要兼容一点五的客户端。",
			rules:  []struct{ wrong, right string }{{"一点零", "1.0"}, {"二点零", "2.0"}}, kind: "对照"},
		{name: "下划线读成词", correct: "这个字段叫 createdAt，不是 created_at，两个地方不一致。",
			asrOut: "这个档段叫created,不是created下划线at,两个地方不一致。",
			rules:  []struct{ wrong, right string }{{"档段", "字段"}, {"下划线at", "_at"}}, kind: "对照"},
		{name: "驼峰被拆", correct: "把 calculateTotal 这个函数拆开，现在耦合太重了。",
			asrOut: "把`countedgettotal`这个函数拆开,现在耦合太重了。",
			rules:  []struct{ wrong, right string }{{"countedgettotal", "calculateTotal"}}, kind: "对照"},
		{name: "缩写+下划线混排", correct: "把 user_id 改成 uid，然后跑一遍 CI 看有没有回归。",
			asrOut: "把user下划线ID改成UID,然后跑一遍CI,看有没有回归。",
			rules:  []struct{ wrong, right string }{{"下划线", "_"}}, kind: "对照"},

		// ── 负控：termHint 在场，但转写是**对的**，必须一个字节都不动 ──
		//
		// ⚠ 这两条是 §31 自己的风险面，不是凑数。
		//
		// 提示词把参会人/主题当「可信上下文」喂进去，就等于给了模型一份
		// **词表**。词表会诱导两种新错误，而它们**只在这两条上出现**：
		//
		//	① 把转写里名单外的名字「对齐」进名单
		//	   （「张伟」不在参会人名单里 ⇒ 模型可能觉得它听错了）
		//	② 把名单里的名字塞进转写里没出现的位置（凭空增加内容）
		//
		// §30 定的原则是「误改比漏修危险」—— 而这两种误改**正是 termHint
		// 自己引入的**。不测它们，就等于只看收益不看代价。
		//
		// ⚠ 名单词必须与转写**完全不相交**，否则这条负控测不出替换风险：
		//
		//	转写里有「张伟」，名单写 `[李娜, 王强]` ⇒ 模型把「张伟」改成名单里
		//	任何一个都该被抓；但名单里的「李娜」「王强」**本来就在转写里**，
		//	任何「名单词是否出现在输出里」的朴素判据都会恒真（第一版就栽在这）。
		//
		//	⇒ 这里用 `[赵敏, 孙磊]`：转写里的三个人都不在名单里。
		//	  若模型把「张伟」对齐成名单中的某人，`rosterLeak` 抓得到；
		//	  若它不动，才是这条负控要的结论。
		{name: "负控-替换风险（名单与转写不相交）", correct: "张伟和李娜都会参加，王强负责做会议纪要。",
			asrOut: "张伟和李娜都会参加，王强负责做会议纪要。",
			rules:  nil, // 判据靠 rosterLeak，不靠「修好」
			meta:   meetingMetaIn{Title: "产品评审周会", Participants: []string{"赵敏", "孙磊", ""}},
			kind:   "负控"},
		{name: "负控-插入风险（名单词原文完全没有）", correct: "有问题发邮件到 support 那个邮箱，或者在群里 at 我。",
			asrOut: "有问题发邮件到support那个邮箱,或者在群里艾特我。",
			rules:  nil,
			meta:   meetingMetaIn{Title: "技术支持值班群同步", Participants: []string{"客服组", "值班经理"}},
			kind:   "负控"},

		// ── §31 目标组：只有拿到上下文才可能修好的错误 ──
		//
		// 这两条是 termHint 存在的**全部理由**。§30.2 测出的分界线是
		// 「拼写接近 vs 毫不相干」，而 `张伟`→`章伟`、`宝路里斯`→`Polaris`
		// 属于后者：没有任何语言模型邻域知识能推出正确答案，
		// **只有给了名单/主题才有依据**。
		{name: "人名同音（需元数据）", correct: "章伟和李娜都会参加今天的评审。",
			asrOut: "张伟和李娜都会参加今天的评审。",
			rules:  []struct{ wrong, right string }{{"张伟", "章伟"}},
			meta:   meetingMetaIn{Title: "第十五届供应商大会", Participants: []string{"章伟", "李娜", "王强"}}, kind: "目标", class: "人名"},
		{name: "音译专名（需元数据）", correct: "Polaris 那边说下周给结果。",
			asrOut: "宝路里斯那边说下周给结果。",
			rules:  []struct{ wrong, right string }{{"宝路里斯", "Polaris"}},
			meta:   meetingMetaIn{Title: "Polaris 供应商对接会", Participants: []string{"采购部"}}, kind: "目标", class: "音译专名"},

		// ── §486 新增：元数据里含**技术标识符**的目标组 ──
		//
		// 为什么必须加这一类（这是 §481.3 + §484 逼出来的）：
		//
		//	§481.3 实测：真实主导错误是 `createdAt`→「一个是顿,tried」、
		//	`changelog`→`Channelbug`、`consumerlag`→`consolelog` ——
		//	而它们**一条都不在**元数据里，所以探针把它们全放在对照组。
		//	⇒ 于是目标组里**一个标识符都没有**，而真实错误几乎全是标识符。
		//	⇒ #29 拿这套语料去问「元数据有没有用」，问的是**人名和音译专名**，
		//	  而 #29 真正关心的是**术语/标识符**。
		//
		// ⚠⚠ **场景是构造的，不是观测到的**：真实会议里「主题恰好写了 createdAt」
		// 这件事本轮**没有观测数据**。这里做的是「**给定**元数据里含正确写法时，
		// 精校能不能修」这一条件实验 —— 它回答的是**能力上限**，
		// 不是「真实会议里有多少机会用上」。⚠ 两者不可混。
		//
		// ✅ 但 `asrOut` **是真实 ASR 输出**：逐字取自 §481.3 回查的
		// `/tmp/cer_run2.txt` 原始读数（mimo-v2.5-asr，两次跑一致），
		// **不是编的** —— 只有 metadata 是构造的。
		{name: "标识符-驼峰被拆（需元数据）", correct: "这个字段叫 createdAt，不是 created_at。",
			asrOut: "一个是顿,tried,不是created下划线at。",
			rules:  []struct{ wrong, right string }{{"一个是顿", "createdAt"}},
			meta:   meetingMetaIn{Title: "createdAt 字段命名评审", Participants: []string{"后端组"}}, kind: "目标", class: "标识符"},
		{name: "标识符-低写连词被拆（需元数据）", correct: "请同步更新 changelog，并跑一遍回归。",
			asrOut: "请同步更新Channelbug,并跑一遍回归。",
			rules:  []struct{ wrong, right string }{{"Channelbug", "changelog"}},
			meta:   meetingMetaIn{Title: "changelog 与发布流程对齐", Participants: []string{"发布负责人"}}, kind: "目标", class: "标识符"},
		{name: "标识符-术语误听（需元数据）", correct: "请检查一下 Kafka 的 consumer lag 是否正常。",
			asrOut: "请检查一下Tafad的consolelog是否正常。",
			rules:  []struct{ wrong, right string }{{"Tafad", "Kafka"}, {"consolelog", "consumer lag"}},
			meta:   meetingMetaIn{Title: "Kafka consumer lag 消费延迟排查", Participants: []string{"SRE"}}, kind: "目标", class: "标识符"},
	}
}

// glossaryArm 按 POCKET_REFINE_GLOSSARY 裁剪术语表的来源。
//
//	all   （默认）主题 + 参会人
//	title 只用主题，不给参会人名单
//	none  什么都不给
//
// ⚠ 为什么加这个开关：实测（§35.6）量到 termHint **自己引入**了一类误改 ——
//
//	转写「张伟和李娜都会参加」（正确）配名单「赵敏、孙磊」
//	⇒ 有元数据时模型输出「**赵敏和孙磊**都会参加」
//
//	两次加禁令都没压住（第二次是「名单上没有的人名多半是正确的」，
//	复测仍复现）。继续堆措辞就是过拟合（§25 的教训）。
//	而这个开关测的是一个**结构变量**：伤害到底来自「参会人名单」
//	还是来自「主题」。两者可分 —— 「Polaris」那处修复来自 Title，
//	「章伟」那处来自 Participants。
func glossaryArm(meta meetingMetaIn) meetingMetaIn {
	switch strings.TrimSpace(os.Getenv("POCKET_REFINE_GLOSSARY")) {
	case "none":
		return meetingMetaIn{}
	case "title":
		return meetingMetaIn{Title: meta.Title}
	default:
		return meta
	}
}

// normSpace 去空白与说话人标记，用于「是否原样返回」的比较。
//
// ⚠ 说话人标记必须先剥掉：segmentsToText 会在每行前面加 "[发言人] "，
//
//	而 ASR 文本没有，模型又按提示词第 6 条把它保留了下来 —— 不剥的话
//	「原样返回」永远为假，每条都会被误报成「改了但没改对」。
func normSpace(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && r != ' ' && r != '\n' && r != '\r' && r != '\t' {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
