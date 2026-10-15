// meeting_response_contract_test.go — §109.6：会议响应的**键守恒**契约。
//
// §109 修掉的那个缺陷，形状是：
//
//	server_meeting.go  result["agent"] = true        ← 服务端标了「这轮是 agent 出的」
//	api/meetings.ts    normalizeSummary()            ← 前端四个字段全接住，agent 没接
//	MeetingInsightPanel v-if="references.length"     ← 整块参考资料静默消失，200 + 无报错
//
// 那不是「漏了一个字段」，是**降级信号在跨语言边界上蒸发**。
// 手工补好之后，如果不留常驻检查，下一个人加一个 `result["xxx"]` 就会原样重演。
//
// ★ 为什么这里用「真行为取键」而不是扫源码：
//
//	纯文本扫描会把**嵌套键**（structured_minutes.action_items）和
//	**ws 广播载荷**（{meetingId, items}，那不是 HTTP 响应体）
//	一并当成顶层响应字段 —— §109.6 实测正是这样，第一版抽取出 15 个 refine 键，
//	其中 agenda/decisions/action_items 三个根本不是顶层。
//	⇒ 判据量的是「响应体里有什么」，必须对着**真实构造出来的 map** 取。
//
// ★ 分两类来源，都必须机器可查，不留「我记得有这些」：
//
//  1. 纯构造函数（emptySummary / parseSummaryJSON / refineFallbackPayload /
//     parseRefineJSON / parseRecommendJSON）—— 直接调，取 map 的真实顶层键。
//  2. 处理器级键（agent / agent_turns / agent_tools / refine_rejected /
//     note_id / tasks_created）—— 它们只在带 I/O 的 handler 里被写进去，
//     调不动。改为**源码自证**：断言那一行赋值字面量确实存在于源文件里。
//     `["agent"] = true` 被删掉 ⇒ 本门转红。
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// contractFixtureRelPath 从 backend/internal/server 走到仓库根下的前端 fixtures。
const contractFixtureRelPath = "../../../frontend/src/api/__tests__/fixtures/meeting-response-keys.json"

// ── LLM 产物的真实形状 ────────────────────────────────────────────────
//
// 抄自生产提示词里的 JSON 契约（server_meeting.go 的 meetingAgentSystemPrompt
// 与 buildRefinePrompt），不是随手编的字段名。
// ⚠ 夹具必须在场：若某样本不含本该存在的键，门会因为「那个键没被消费」
// 而误报 ⇒ 键集本身就失去意义。所以下面每个 payload 都要覆盖其 schema 全部字段。

const contractSummaryLLMOutput = `{
  "summary":"本季度把录制链的准确率问题定位到切段边界",
  "key_points":["切段导致跨段专名丢失"],
  "action_items":[{"text":"把术语表接进摘要链","assignee":"我","due":"2026-10-09"}],
  "decisions":["精校放在录音结束之后"],
  "open_questions":["整段解码的内存阈值定多少"],
  "references":[{"title":"我上次的会议纪要","note_id":"note-1","why":"同一议题"}]
}`

const contractRefineLLMOutput = `{
  "refined_transcript":"我们把准确率问题定位到切段边界，先统一术语表。",
  "translations":{"en":"We located the accuracy issue at the segment boundary."},
  "structured_minutes":{
    "agenda":["准确率定位"],
    "decisions":["精校放在录音结束之后"],
    "action_items":[{"text":"把术语表接进摘要链","priority":"high"}],
    "next_meeting":"2026-10-10 10:00"
  },
  "todos":[{"text":"把术语表接进摘要链","priority":"medium","due":"2026-10-09"}]
}`

const contractRecommendLLMOutput = `{
  "items":[
    {"title":"切段边界对 ASR 的影响","snippet":"跨段专名最容易丢","query":"ASR 切段 边界 专名"},
    {"title":"术语表热词提升","snippet":"给 LLM 一份术语表","query":"LLM 热词表 提升准确率"}
  ]
}`

func contractKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contractUnion(groups ...[]string) []string {
	seen := map[string]bool{}
	for _, g := range groups {
		for _, k := range g {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contractContains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// contractHandlerKeys 是「只在带 I/O 的 handler 里被写进响应」的键。
//
// 每条带一个 sourceFile + 一个必须真实存在的赋值片段。
// ⚠ 这是**手工登记**，所以登记本身必须被机器检查 ——
//
//	断言那一行字面量确实在源文件里。删掉 `result["agent"] = true`
//	⇒ 本门转红，而不是让前端门继续以为 agent 存在。
var contractHandlerKeys = []struct {
	kind   string
	key    string
	source string
	anchor string // 必须在源文件里出现的字面量片段
}{
	{"summary", "agent", "server_meeting.go", `["agent"] = true`},
	{"summary", "agent_turns", "server_meeting.go", `["agent_turns"] = `},
	{"summary", "agent_tools", "server_meeting.go", `["agent_tools"] = `},

	{"refine", "refine_rejected", "server_meeting.go", `["refine_rejected"] = true`},
	{"refine", "note_id", "server_meeting_ingest.go", `["note_id"] = `},
	{"refine", "tasks_created", "server_meeting_ingest.go", `["tasks_created"] = `},
}

// mustParseRefineForContract 调生产解析器，失败即测试失败 ——
// 夹具不在场时门必须转红，不能悄悄退化成空键集。
func mustParseRefineForContract(t *testing.T) map[string]any {
	t.Helper()
	m, err := parseRefineJSON(contractRefineLLMOutput)
	if err != nil {
		t.Fatalf("parseRefineJSON 对契约夹具解析失败（夹具形状与服务端 schema 漂了？）: %v", err)
	}
	return m
}

func TestMeetingResponseKeyContract(t *testing.T) {
	// ── 1. 纯构造函数：真行为取顶层键 ──────────────────────────────
	empty, err := parseSummaryJSON("这不是 JSON", "原始转写兜底")
	if err != nil {
		t.Fatalf("parseSummaryJSON 回落分支不应返回 error: %v", err)
	}
	parsedSummary, err := parseSummaryJSON(contractSummaryLLMOutput, "原始转写兜底")
	if err != nil {
		t.Fatalf("parseSummaryJSON 解析分支返回 error: %v", err)
	}
	parsedRefine := mustParseRefineForContract(t)
	recItems := parseRecommendJSON(contractRecommendLLMOutput)
	if len(recItems) != 2 {
		t.Fatalf("契约夹具应解析出 2 条推荐，实得 %d 条（夹具漂了，门会误报）", len(recItems))
	}

	behavior := map[string][]string{
		"summary": contractUnion(
			contractKeys(empty),         // 回落形状
			contractKeys(parsedSummary), // agent/chat 正常形状
		),
		"refine": contractUnion(
			contractKeys(refineFallbackPayload("原始转写")),
			contractKeys(parsedRefine),
		),
		"recommendItem": contractKeys(recItems[0]),
	}

	// ── 2. 量具自证：抽取器本身坏掉时必须转红，不能返回空集 ──────────
	//
	// 这一段是本门最容易被无视的地方：如果 parse* 系列哪天改了签名或
	// 恒返回空 map，键集会静默变成空 ⇒ 前端门「零个键被丢」永远绿。
	for kind, minKeys := range map[string]int{"summary": 6, "refine": 5, "recommendItem": 6} {
		if len(behavior[kind]) < minKeys {
			t.Fatalf("抽取器自证失败：%s 只取到 %d 个顶层键（期望 ≥%d）—— "+
				"要么生产构造函数的形状变了，要么 parse* 系列不再返回真实 map。"+
				"先查这里，不要直接改前端门的期望值。kind=%s keys=%v",
				kind, len(behavior[kind]), minKeys, kind, behavior[kind])
		}
	}
	// 哨兵：这几键一旦从契约里消失，前端门的覆盖范围就悄悄变小了。
	for kind, want := range map[string]string{
		"summary":       "key_points",
		"refine":        "refine_fallback",
		"recommendItem": "url",
	} {
		if !contractContains(behavior[kind], want) {
			t.Fatalf("量具自证失败：%s 的键集里没有哨兵键 %q —— "+
				"说明取值路径已变（keys=%v）", kind, want, behavior[kind])
		}
	}

	// ── 3. 处理器级键：源码自证 ────────────────────────────────────
	handlerSources := map[string]string{}
	for _, hk := range contractHandlerKeys {
		src, ok := handlerSources[hk.source]
		if !ok {
			b, err := os.ReadFile(hk.source)
			if err != nil {
				t.Fatalf("读不到 %s（源码自证的前提没了）: %v", hk.source, err)
			}
			if len(b) < 1000 {
				t.Fatalf("量具自证失败：%s 只有 %d 字节，明显没读到完整源码", hk.source, len(b))
			}
			src = string(b)
			handlerSources[hk.source] = src
		}
		if !strings.Contains(src, hk.anchor) {
			t.Errorf("源码自证失败：%s 的响应键 %q 找不到赋值片段 %q —— "+
				"要么它被删了（前端门会继续假设它存在），要么被改名/搬家了。"+
				"两种情况都要先确认前端还认不认这个键。",
				hk.source, hk.key, hk.anchor)
		}
	}
	// 只匹配**赋值**，不匹配读取：`["agent"]` 出现在读取处不算证据。
	readOnlyRe := regexp.MustCompile(`\[\s*"agent"\s*\]\s*[,\)]`)
	if readOnlyRe.MatchString(handlerSources["server_meeting.go"]) {
		t.Logf("注：server_meeting.go 里出现了 agent 的读取形态，确认它同时也有写入形态即可")
	}

	contract := map[string]any{}
	for kind, keys := range behavior {
		contract[kind] = keys
	}
	// handler 键并进对应 kind
	merged := map[string][]string{}
	for k, v := range behavior {
		merged[k] = append([]string{}, v...)
	}
	for _, hk := range contractHandlerKeys {
		if contractContains(merged[hk.kind], hk.key) {
			continue
		}
		merged[hk.kind] = append(merged[hk.kind], hk.key)
		sort.Strings(merged[hk.kind])
	}
	contract = map[string]any{}
	for k, v := range merged {
		contract[k] = v
	}

	// 落盘契约：UPDATE_MEETING_KEY_CONTRACT=1 时重写，否则比对。
	path := filepath.Join("..", "..", "..", "frontend", "src", "api", "__tests__", "fixtures", "meeting-response-keys.json")
	blob, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatalf("序列化契约失败: %v", err)
	}
	blob = append(blob, '\n')

	if os.Getenv("UPDATE_MEETING_KEY_CONTRACT") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("建 fixtures 目录失败: %v", err)
		}
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			t.Fatalf("写契约失败: %v", err)
		}
		t.Logf("已重写 %s", path)
		return
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读契约失败 %s（首次生成请跑 UPDATE_MEETING_KEY_CONTRACT=1）: %v", path, err)
	}
	if string(onDisk) != string(blob) {
		t.Fatalf("会议响应键契约已过期：服务端发出的顶层键与 fixtures 不一致。\n"+
			"这不是「顺手更新一下」—— 先问新键是干什么的、前端要不要接，\n"+
			"有意忽略的写进前端门的 allowlist 并附理由，然后再跑 "+
			"UPDATE_MEETING_KEY_CONTRACT=1。\n磁盘:\n%s\n实测:\n%s",
			string(onDisk), string(blob))
	}
}

// ── /recommend 的承重断言（§109.5 否证的可执行版本）─────────────────
//
// 我在普查里一度怀疑「LLM 兜底推荐的 item 没有 url/type ⇒ 前端点击静默无反应」。
// parseRecommendJSON 其实把六个字段全补齐了 —— 那个疑似是假的。
//
// 但**「它补齐了」目前没有任何测试钉住**：删掉 url 那一行，
// 现有门全绿，而 MeetingDetailView.onOpenRelated 立刻变成哑的。
// 把它钉成行为断言，附一条负控（去掉 url 必须转红）。
func TestParseRecommendJSONItemsAreClickable(t *testing.T) {
	items := parseRecommendJSON(contractRecommendLLMOutput)
	if len(items) == 0 {
		t.Fatalf("契约夹具解析出 0 条推荐（夹具漂了）")
	}
	idx := strings.Index(contractRecommendLLMOutput, "ASR 切段 边界 专名")
	if idx < 0 {
		t.Fatalf("契约夹具里没有预期的 query 文本")
	}
	for i, it := range items {
		url, _ := it["url"].(string)
		if strings.TrimSpace(url) == "" {
			t.Errorf("item[%d] 没有 url —— MeetingDetailView.onOpenRelated "+
				"见到 url 就新开窗口，缺了它会掉进 type 分支而哪个都不匹配，"+
				"点击静默无反应。item=%v", i, it)
		}
		if typ, _ := it["type"].(string); strings.TrimSpace(typ) == "" {
			t.Errorf("item[%d] 没有 type —— 前端据此决定行为（web/note/email）。item=%v", i, it)
		}
		if id, _ := it["id"].(string); strings.TrimSpace(id) == "" {
			t.Errorf("item[%d] 没有 id —— 列表 key 与跳转都要用。item=%v", i, it)
		}
	}
	// 负控自证：url 必须真的是搜索链接，而不是随便一个串。
	first, _ := items[0]["url"].(string)
	if !strings.Contains(first, "?q=") {
		t.Errorf("item[0].url = %q，不是搜索兜底链接 —— 净化的 query 没拼进去", first)
	}
}

// 负控：把 parseRecommendJSON 的 url 赋值删掉，本测试必须转红。
// 由 /tmp 脚本 mutate 在验证阶段执行，不常驻。
var _ = regexp.MustCompile

// §119：UI 的「术语表」说明必须**只**提到 metaTermHint 真正读到的字段。
//
// 为什么这道门是行为派生而不是手抄清单：手抄的「用的是这三个字段」
// 一定会腐烂 —— 将来有人给 metaTermHint 加了 Topic、或给 meetingMetaIn
// 加了一个新字段，那张清单不会跟着变，而界面上的说明会继续漏说
// （或者更糟：开始说一个根本没接上的字段）。
//
// ⇒ 这里用**行为 + 反射**把「哪些字段真的被读了」测出来：
//
//	逐个字段单独喂进 metaTermHint，看输出是否为空。空 = 没被读。
//
// ★ 附带一条更锋利的断言（unused 必须为空）：
//
//	meetingMetaIn 里**声明**了却没人读的字段，就是 §64.6 那个形状 ——
//	「Location 一直在请求体里，而这段只读 Title 与 Participants」。
//	那次的修复是加进 metaTermHint；这条断言防的是它再发生。
func metaWithOnlyField(i int) meetingMetaIn {
	m := meetingMetaIn{}
	f := reflect.ValueOf(&m).Elem().Field(i)
	switch f.Kind() {
	case reflect.String:
		f.SetString("星海科技总部三楼")
	case reflect.Slice:
		f.Set(reflect.ValueOf([]string{"张伟", "李娜"}))
	default:
		panic("meetingMetaIn 出现了新的字段类型，请给这条测试补一个探针值：" + f.Kind().String())
	}
	return m
}

func TestMetaTermHintFieldContract(t *testing.T) {
	typ := reflect.TypeOf(meetingMetaIn{})
	// ⚠ 一律用空切片而非 nil 初始化：Go 的 nil slice 序列化成 **null**，
	// 而 JS 的 Array.isArray(null) === false ⇒ 契约落到前端就成了
	// 「不是数组」，下游要么崩要么静默跳过。
	// 与 calendarFeedEnvelope 把 nil entries 归一成 [] 同一课。
	declared := []string{}
	used := []string{}
	unused := []string{}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		declared = append(declared, name)
		if metaTermHint(metaWithOnlyField(i)) != "" {
			used = append(used, name)
		} else {
			unused = append(unused, name)
		}
	}
	sort.Strings(declared)
	sort.Strings(used)
	sort.Strings(unused)

	// 量具自证：若 metaTermHint 什么都不读，used 为空 ⇒
	// 下面「说明必须提到全部 used」会永远绿，而「unused 必须为空」会永远红 ——
	// 后者仍能报出真问题，但前者会失明。两者缺一不可。
	if len(used) < 2 {
		t.Fatalf("抽取器自证失败：只有 %d 个字段被判为「被使用」(%v) —— "+
			"先查 metaTermHint 是否还读这些字段，别直接改前端门", len(used), used)
	}
	if len(unused) != 0 {
		t.Errorf("meetingMetaIn 声明了这些字段但 metaTermHint **一个都不读**：%v —— "+
			"它们会随请求发上来又静静丢掉（§64.6 的形状：Location 曾经就是这样）。"+
			"要么接进术语表，要么从请求体里去掉；两者都比现在诚实。", unused)
	}

	contract := map[string]any{"declared": declared, "used": used, "unused": unused}
	blob, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	blob = append(blob, '\n')

	path := filepath.Join("..", "..", "..", "frontend", "src", "api", "__tests__", "fixtures", "meeting-meta-glossary-fields.json")
	if os.Getenv("UPDATE_MEETING_KEY_CONTRACT") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("已重写 %s：declared=%v used=%v unused=%v", path, declared, used, unused)
		return
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读契约失败 %s（首次生成请跑 UPDATE_MEETING_KEY_CONTRACT=1）: %v", path, err)
	}
	if string(onDisk) != string(blob) {
		t.Fatalf("metaTermHint 的字段集合已变（declared=%v used=%v unused=%v），但前端 fixtures 没跟着更新。\n"+
			"先决定界面上的术语表说明要不要跟着改 —— 它承诺的字段集合必须与后端真正读的**一致**，\n"+
			"否则说明就在撒谎。再跑 UPDATE_MEETING_KEY_CONTRACT=1。\n磁盘:\n%s\n实测:\n%s",
			declared, used, unused, string(onDisk), string(blob))
	}
}
