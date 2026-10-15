// worktype_coverage_test.go — §159：**work_type 实际覆盖**的登记式棘轮。
//
// ── 这道门回答的问题 ──────────────────────────────────────────────
//
// 「按链钉模型」在网关侧**已经实现**（前端 `replaceWorkTypeRoutes` 已接线到
// `/api/admin/work-types/{key}/routes`，网关按 work-type 选模型）。
// 但本仓真正把 work-type 发到网关的入口**只有一个**：
//
//	llmbff.ChatRequest.Kind → WorkTypeFromKind() → llmgateway.ChatRequest.WorkType
//	                        → HTTP 头 X-Gw-Work-Type
//
// 所以「哪些业务链钉得上模型」完全取决于**请求结构体里有没有写 Kind**。
//
// ★★★ 而 `Service.Chat(ctx, req, kind)` 的第 3 个位置参数**不是它**：
//
//	internal/llmbff/service.go:189
//	func (s *Service) Chat(ctx context.Context, req ChatRequest, kind string)
//	  └─ 这个 kind 只进 `recorder.RecordUsage(..., kind)`，即**按用途分账**，
//	     之后函数就返回了 —— 它**从不**回到 provider，**到不了网关**。
//
//	`ChatRequest.Kind` 的注释写着 `// quota + gateway work-type`，
//	把两个同名不同物的参数描述成一个，是本题最大的坑：
//	`server_assistant.go` 的 `llmChatOnce`（**会议精校主路径**）传了 `, "meeting")`，
//	读起来像「meeting 这条链已经标记了」，实际 work_type 是**空串**。
//
// ── 为什么是登记式棘轮而不是承重断言 ──────────────────────────────
//
// 覆盖缺口有两类，处置完全不同：
//
//	(a) 「同一条业务链在主路径与回落路径发出的 work_type 不一致」
//	    例：会议摘要主路径发 `meeting_summary_agent`（无映射），
//	        前端回落路径发 `meeting_summary`（有映射）。
//	    这是**缺陷**，但修法会改变主路径实际命中的模型 ⇒ 属产品决定。
//	(b) `meeting_refine` 与 `live_translate` 撞在同一个 `doc_translate`
//	    ⇒ 即便配了路由，精校与实时翻译**无法分开**。
//	    这是**产品事实**，改法要动网关侧配置。
//
// 拍板前把 (a)(b) 写成承重断言＝把未决取舍焊死。
// 本门只保证**缺口不扩大**：新增一条链却忘了写 Kind ⇒ 立刻红。
//
// ── 抽取器踩过的四个坑（都留在这里，别删） ────────────────────────
//
// 1. **函数签名的返回类型** `func f(req llmbff.ChatRequest) llmbff.ChatRequest {`
//    里第二个 `ChatRequest {` 的 `{` 是**函数体的开括号**，不是结构体字面量。
//    判别式：`ChatRequest` 紧前面若是 `)`，那是返回类型 ⇒ 跳过。
// 2. **同名的别的类型**：`llmgateway.ChatRequest{}`、`redclaw.ChatRequest{}`。
//    判别式：限定包名必须是 `llmbff` 或为空。
// 3. **非字面量的 Kind**：`Kind: body.Kind`（前端 HTTP 体透传）。
//    字面量正则看不见它，而它**正是前端 kind 到达 Go 侧的唯一入口**。
// 4. **位置参数的抓取**：`}, "meeting")` 这一行 trim 后以 `}` 开头，
//    用「遇到 } 就停」的判据会**立刻停**，读不到任何位置参数 ⇒ 静默为空。
//
// ── 判据为什么用括号配平而不是固定字符窗口 ────────────────────────
//
// 同 §157：`Kind:` 到结构体闭合 `}` 的距离取决于中间有多少注释，
// 固定窗口会在「只改注释长度」时误报。配平对注释长度不敏感。

package llmbff

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── 抽取器 ────────────────────────────────────────────────────────

// kindSite 是一条「可能把 work-type 送上网关」的请求构造点。
type kindSite struct {
	File string
	Line int
	// Kind 是结构体里写的**字面量**值。空串有两种含义，由 HasKindExpr 区分。
	Kind string
	// KindExpr 是非字面量表达式（如 `body.Kind`），空串表示没有写。
	KindExpr string
	// PositionalKind 是同一调用点第 3 个位置参数里的字面量。
	// ★ 它**不影响 work_type**，只登记下来 —— 「把它误当成 kind」
	//   正是本文件要防的那类误读。
	PositionalKind string
}

func (s kindSite) hasKind() bool { return s.Kind != "" || s.KindExpr != "" }

// chatRequestRe 抓 `ChatRequest{` / `llmbff.ChatRequest{` / `llmbff.ChatRequest {`。
// 捕获组 1 = 限定包名（可空）。
var chatRequestRe = regexp.MustCompile(`(?:(\w+)\.)?ChatRequest\s*\{`)

// kindFieldRe 抓 `Kind:` 后面到行尾/逗号的表达式。
var kindFieldRe = regexp.MustCompile(`\bKind\s*:\s*([^,\n]*)`)

// stringLitRe 从表达式里取 Go 字符串字面量的值。
var stringLitRe = regexp.MustCompile(`"([^"]*)"`)

func goLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s：%v", path, err)
	}
	return strings.Split(string(b), "\n")
}

// stripGoComment 剥 `//` 行注释，并跳过字符串字面量里的 `//`。
func stripGoComment(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			if i == 0 || line[i-1] != '\\' {
				inStr = !inStr
			}
		case '/':
			if !inStr && i+1 < len(line) && line[i+1] == '/' {
				return line[:i]
			}
		}
	}
	return line
}

// matchBrace 从 lines[start] 的第 startCol 列（必须是 '{'）开始做花括号配平，
// 返回闭合 '}' 所在行号；配不平返回 -1。
//
// ★ 为什么带列号而不是从行首数：`if x { _ = llmbff.ChatRequest{` 这种
//
//	一行两个花括号的写法会让「从行首数」把外层也吃进来，闭合位置提前。
func matchBrace(lines []string, start, startCol int) int {
	depth := 0
	for i := start; i < len(lines); i++ {
		l := stripGoComment(lines[i])
		from := 0
		if i == start {
			from = startCol
		}
		for j := from; j < len(l); j++ {
			switch l[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// isSignatureReturnType 判断 `ChatRequest` 前面紧邻的是不是 ')'。
// 是 ⇒ 它是函数签名的返回类型，后面那个 '{' 是函数体开括号（坑 1）。
func isSignatureReturnType(cleanLine string, start int) bool {
	for i := start - 1; i >= 0; i-- {
		c := cleanLine[i]
		if c == ' ' || c == '\t' {
			continue
		}
		return c == ')'
	}
	return false
}

// collectGoSites 扫 dir 下的**生产** Go 代码。
func collectGoSites(t *testing.T, dir string) []kindSite {
	t.Helper()
	var out []kindSite

	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		lines := goLines(t, p)
		for i, raw := range lines {
			clean := stripGoComment(raw)
			if strings.HasPrefix(strings.TrimSpace(clean), "//") {
				continue
			}
			loc := chatRequestRe.FindStringSubmatchIndex(clean)
			if loc == nil {
				continue
			}
			// 坑 2：限定包名必须是 llmbff 或空。
			if loc[2] >= 0 {
				if q := clean[loc[2]:loc[3]]; q != "llmbff" {
					continue
				}
			}
			// 坑 1：返回类型。
			if isSignatureReturnType(clean, loc[0]) {
				continue
			}
			braceCol := loc[1] - 1
			closeLine := matchBrace(lines, i, braceCol)
			if closeLine < 0 {
				t.Fatalf("%s:%d 的 ChatRequest{ 花括号没配平 —— 抽取器坏了，"+
					"这道门会给出不可信读数", p, i+1)
			}
			site := kindSite{File: p, Line: i + 1}
			// 坑 3：Kind 可能是字面量，也可能是表达式。
			for j := i; j <= closeLine; j++ {
				l := stripGoComment(lines[j])
				m := kindFieldRe.FindStringSubmatch(l)
				if m == nil {
					continue
				}
				expr := strings.TrimSpace(m[1])
				if lit := stringLitRe.FindStringSubmatch(expr); lit != nil {
					site.Kind = lit[1]
				} else {
					site.KindExpr = expr
				}
				break
			}
			// 坑 4：位置参数在**闭合花括号之后**，通常就在同一行
			// （`}, "meeting")`）。必须先切出「闭合 } 之后的那半行」再找字面量 ——
			// 早先用 `continue` 想「留在同一行」，可三段式 for 的 continue
			// 会执行 j++，于是**整行被跳过**，读数恒为空。
			for j := closeLine; j < len(lines) && j <= closeLine+4; j++ {
				l := stripGoComment(lines[j])
				seg := l
				if j == closeLine {
					// 找这一行上**最后一个** '}'（结构体闭合处），取其后的部分。
					if k := strings.LastIndex(l, "}"); k >= 0 {
						seg = l[k+1:]
					} else {
						seg = ""
					}
				}
				if pos := regexp.MustCompile(`,\s*"([^"]*)"`).FindStringSubmatch(seg); pos != nil {
					site.PositionalKind = pos[1]
					break
				}
				if j > closeLine && strings.HasSuffix(strings.TrimSpace(seg), ")") {
					break
				}
			}
			out = append(out, site)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫 %s 失败：%v", dir, err)
	}
	return out
}

func serverDir() string { return filepath.Join("..", "server") }

func siteKey(s kindSite) string {
	k := s.Kind
	if k == "" {
		k = "<无 Kind>"
	}
	return filepath.Base(s.File) + ":" + itoa(s.Line) + " kind=" + k
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ── 登记的缺口基线 ────────────────────────────────────────────────
//
// ⚠ 这些是**读数登记**，不是「这样就对了」。
//   拍板后请**改这张表**（连同下面的断言），不要放宽判据。

// goSitesWithoutKind 是「构造了 llmbff 请求、结构体里没写 Kind ⇒
// work_type 恒为空」的调用点。
//
// ★ 键必须是「文件 + 第 3 个位置参数」而不是文件名：
//
//	早先按文件名登记，把 `server_assistant.go` 里的三处糊成一条
//	「会议精校主路径」—— 而实际上那一处 llmChatOnce 是**三条**会议链
//	（recommend / summary / refine）共用的入口。
//	行号会随无关编辑漂移，文件名又不是唯一键；位置参数既是稳定标识，
//	又自带业务含义（「meeting」= 会议链，「note_summary」= 随手记）。
var goSitesWithoutKind = map[string]string{
	"server_assistant.go|meeting":      "会议摘要/精校/推荐**主路径**（llmChatOnce，三链共用）",
	"server_assistant.go|note_summary": "随手记总结主路径",
	"server_assistant.go|chat":         "对话（/api/llm/chat 的通用入口）",
}

// noKindKey 给出「文件 + 位置参数」的登记键。
func noKindKey(s kindSite) string {
	return filepath.Base(s.File) + "|" + s.PositionalKind
}

// unmappedKinds 是「仓里真的在发、但 WorkTypeFromKind 没有 case」的 kind。
var unmappedKinds = map[string]string{
	"meeting_summary_agent": "会议摘要**主路径**（meetingagent.Runner）",
	"email-classify":        "邮件分类",
	"email-summary":         "邮件摘要",
}

// ── 门 1：漏写 Kind 的调用点不许变多 ───────────────────────────────

// TestWorkTypeKindWrittenAtEveryCallSite 棘轮：不允许出现**新**的
// 「构造了 llmbff 请求却没写 ChatRequest.Kind」的调用点。
func TestWorkTypeKindWrittenAtEveryCallSite(t *testing.T) {
	sites := collectGoSites(t, serverDir())
	if len(sites) < 3 {
		t.Fatalf("只扫到 %d 个 llmbff 请求构造点（%v）—— 抽取器多半坏了，"+
			"「一处都没问题」会是个无意义的恒真结论", len(sites), siteKeys(sites))
	}

	var offenders []string
	for _, s := range sites {
		if !s.hasKind() {
			if _, registered := goSitesWithoutKind[noKindKey(s)]; !registered {
				offenders = append(offenders, siteKey(s)+"  位置参数="+strconv.Quote(s.PositionalKind))
			}
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("这些调用点构造了 llmbff 请求却没写 ChatRequest.Kind ⇒ "+
			"work_type 恒为空串，「按链钉模型」对它们无效：\n  %s\n"+
			"（若有意为之，把文件登记进 goSitesWithoutKind 并写清理由；"+
			"若是漏写，补上 Kind 即可）",
			strings.Join(offenders, "\n  "))
	}

	t.Logf("读数：%d 个 llmbff 请求构造点，无新增「漏写 Kind」", len(sites))
	for _, s := range sites {
		if !s.hasKind() {
			t.Logf("  已登记缺口 %s —— %s；第 3 个位置参数 %q 只进计费，"+
				"不影响 work_type", siteKey(s),
				goSitesWithoutKind[noKindKey(s)], s.PositionalKind)
		}
	}
}

func siteKeys(sites []kindSite) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, siteKey(s))
	}
	sort.Strings(out)
	return out
}

// frontendKinds 是**前端**通过 `/api/llm/chat` 请求体发出的 kind。
//
// ⚠ 它们与 Go 侧字面量走的是**不同的通道**：`Kind: body.Kind` 透传
//
//	（门 3 钉住那条链）。所以任何问「本仓哪些 kind 能到网关」的判据，
//	定义域都必须**同时**包含这两处 —— 只扫 Go 字面量会漏掉整个前端集合。
//	早先的门 2 只扫 Go 字面量，变异 M2（把 doc_translate 的映射改成空串）
//	因此全绿 ⇒ 定义域漏了一整个来源。
var frontendKinds = []string{
	"meeting_summary", // fallbackSummarize
	"meeting_refine",  // fallbackRefine
	"live_translate",  // 录音实时翻译
	"doc_translate",   // 文档翻译
}

// ── 门 2：Go 侧在发的 kind 必须在映射里有归宿 ──────────────────────

// TestWorkTypeCoversEveryGoKind 棘轮：不允许出现**新的**
// 「Go 侧在发、但 WorkTypeFromKind 查无此案」的 kind。
func TestWorkTypeCoversEveryGoKind(t *testing.T) {
	sites := collectGoSites(t, serverDir())
	seen := map[string]string{} // kind → 第一个来源
	for _, s := range sites {
		if s.Kind == "" {
			continue
		}
		if _, ok := seen[s.Kind]; !ok {
			seen[s.Kind] = filepath.Base(s.File) + ":" + itoa(s.Line)
		}
	}
	// ★ 前端那批也进定义域。门 3 已钉住 `Kind: body.Kind` 这条透传链，
	//   所以它们确实能到 WorkTypeFromKind。
	for _, k := range frontendKinds {
		if _, ok := seen[k]; !ok {
			seen[k] = "frontend/src/api/meetings.ts 等（经 server_llmbff.go 的 Kind: body.Kind 透传）"
		}
	}

	var unmapped []string
	for kind := range seen {
		if WorkTypeFromKind(kind) != "" {
			continue
		}
		if _, registered := unmappedKinds[kind]; !registered {
			unmapped = append(unmapped, kind+"（来源 "+seen[kind]+"）")
		}
	}
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		t.Fatalf("这些 kind Go 侧在发、但 WorkTypeFromKind 没有 case ⇒ "+
			"work_type 落空字符串，这些链**钉不上模型**：\n  %s\n"+
			"（若有意为之，登记进 unmappedKinds；否则给 WorkTypeFromKind 加 case）",
			strings.Join(unmapped, "\n  "))
	}

	t.Logf("读数：全仓在发 %d 个 kind（Go 字面量 ∪ 前端请求体）：", len(seen))
	for _, k := range sortedKeys(seen) {
		wt := WorkTypeFromKind(k)
		if wt == "" {
			t.Logf("  %-24s → \"\"  ⚠ 已登记：%s", k, unmappedKinds[k])
		} else {
			t.Logf("  %-24s → %s", k, wt)
		}
	}
	for _, s := range sites {
		if s.KindExpr != "" {
			t.Logf("  %s 的 Kind=%s（表达式，非字面量）", siteKey(s), s.KindExpr)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── 门 3：前端 kind 的透传链不许断 ─────────────────────────────────

// TestFrontendKindPassThroughStillWired 钉住
// `server_llmbff.go` 里 `Kind: body.Kind` 这一行 ——
// 它是**前端 kind 到达 WorkTypeFromKind 的唯一通路**。
//
// ⚠ 它是表达式不是字面量，抽取器单独锚在 `body.Kind` 这个**唯一标识**上。
//
//	锚在 `Kind:` 上会被同文件里其它形状喂饱（宽匹配恒真，见 §157.2）。
//
// ★ 断了会怎样：前端的 `meeting_summary` / `meeting_refine` 停在 HTTP 层，
//
//	网关收不到 work_type，而**所有门禁仍然全绿**（没有报错、没有失败，
//	只是模型悄悄换成了 preferred[0]）。
func TestFrontendKindPassThroughStillWired(t *testing.T) {
	p := filepath.Join(serverDir(), "server_llmbff.go")
	lines := goLines(t, p)

	found := false
	for i, raw := range lines {
		l := stripGoComment(raw)
		if strings.Contains(l, "Kind:") && strings.Contains(l, "body.Kind") {
			found = true
			t.Logf("透传在 %s:%d —— 前端 kind 能到达 WorkTypeFromKind", p, i+1)
			break
		}
	}
	if !found {
		t.Fatalf("%s 里找不到 `Kind: body.Kind` ⇒ 前端传来的 kind 不再进入 "+
			"llmbff.ChatRequest，meeting_summary / meeting_refine 的 work_type "+
			"静默失效（没有报错，网关只是收不到 X-Gw-Work-Type）", p)
	}
}

// TestFrontendFallbackKindsAreMapped 钉住前端回落路径发的两个 kind。
//
// ⚠ 锚点用**函数名**（fallbackSummarize / fallbackRefine）而不是全局
//
//	`kind: '...'` 正则：`client.ts:96` 有 `kind: 'permission' | 'question'`
//	那是 **TypeScript 类型联合**，不是发出去的请求体，宽匹配会把它算进来。
func TestFrontendFallbackKindsAreMapped(t *testing.T) {
	root := filepath.Join("..", "..", "..", "frontend", "src", "api", "meetings.ts")
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("读不到 %s：%v", root, err)
	}
	src := string(b)

	for _, want := range []struct{ fn, kind, why string }{
		{"fallbackSummarize", "meeting_summary", "摘要回落路径"},
		{"fallbackRefine", "meeting_refine", "精校回落路径"},
	} {
		fnIdx := strings.Index(src, "function "+want.fn+"(")
		if fnIdx < 0 {
			t.Fatalf("%s 里找不到 function %s( —— 回落路径可能被改名/删除，"+
				"这条门需要重读", root, want.fn)
		}
		// 函数体范围：从声明到下一个顶层 `}` 之前。用「下一个顶层函数声明」截断更稳。
		end := len(src)
		if next := strings.Index(src[fnIdx+1:], "\nasync function "); next >= 0 {
			end = fnIdx + 1 + next
		}
		body := src[fnIdx:end]
		if !strings.Contains(body, "kind: '"+want.kind+"'") {
			t.Fatalf("%s 的 %s（%s）里没有 `kind: '%s'` ⇒ "+
				"这条链的 work_type 会落空", root, want.fn, want.why, want.kind)
		}
		wt := WorkTypeFromKind(want.kind)
		if wt == "" {
			t.Fatalf("前端发了 kind=%q，但 WorkTypeFromKind 查无此案 ⇒ "+
				"work_type 落空，前端这条链钉不上模型", want.kind)
		}
		t.Logf("  %-18s kind=%-16s → work_type=%q ✓", want.fn, want.kind, wt)
	}
}

// ── 门 4：撞车的 work-type 登记表 ─────────────────────────────────

// sharedWorkTypes 是「两条及以上业务链撞在同一个 work-type」。
// 键 = work-type，值 = 映射到它的 kind。
//
// ★ 这条登记是 §159 拍板项 (b) 的载体：
//
//	`meeting_refine`（会议精校）与 `live_translate`（录音实时翻译）
//	都落在 `doc_translate` ⇒ 设置页给 doc_translate 配的模型路由
//	**会同时改掉精校和实时翻译**。
var sharedWorkTypes = map[string][]string{
	"doc_translate": {"live_translate", "doc_translate", "meeting_refine"},
}

func TestWorkTypeSharingRegistry(t *testing.T) {
	// 反查：每个 work-type 由哪些 kind 映射而来。
	// kind 的来源域 = Go 侧字面量 ∪ 前端回落路径（门 3 已钉住透传链）。
	byWorkType := map[string]map[string]bool{}
	add := func(kind string) {
		wt := WorkTypeFromKind(kind)
		if wt == "" {
			return
		}
		if byWorkType[wt] == nil {
			byWorkType[wt] = map[string]bool{}
		}
		byWorkType[wt][kind] = true
	}
	for _, s := range collectGoSites(t, serverDir()) {
		if s.Kind != "" {
			add(s.Kind)
		}
	}
	for _, k := range frontendKinds {
		add(k)
	}

	for wt, registered := range sharedWorkTypes {
		var actual []string
		for k := range byWorkType[wt] {
			actual = append(actual, k)
		}
		sort.Strings(actual)
		want := append([]string(nil), registered...)
		sort.Strings(want)
		if strings.Join(actual, ",") != strings.Join(want, ",") {
			t.Fatalf("work-type %q 的来源链变了：\n  实际 %v\n  登记 %v\n"+
				"（新增一条链挤进同一个 work-type 会让「按链钉模型」静默失效：\n"+
				"  改一条链的模型会连带改掉另一条，且没有任何报错）",
				wt, actual, want)
		}
	}
	t.Logf("读数：%d 个 work-type 承载非空 kind，撞车登记核对通过：", len(byWorkType))
	for wt, k := range byWorkType {
		var list []string
		for x := range k {
			list = append(list, x)
		}
		sort.Strings(list)
		mark := ""
		if _, shared := sharedWorkTypes[wt]; shared {
			mark = "  ⚠ 已登记撞车"
		}
		t.Logf("  %-18s ← %s%s", wt, strings.Join(list, ", "), mark)
	}
}

// ── 量具自证（负控） ──────────────────────────────────────────────

// TestWorkTypeScannerHasTeeth 往 t.TempDir() 注入夹具，抽取器必须：
// 抓到它们、正确区分字面量与表达式、**不把位置参数当 Kind**、
// 跳过 _test.go 与 testdata。
//
// ⚠ 为什么注入到临时目录而不是真包目录：往 `../server/` 落临时 .go
//
//	会让**并发** go build 把它算进编译单元，造成与本测试无关的编译失败。
func TestWorkTypeScannerHasTeeth(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("写不了负控夹具 %s：%v", name, err)
		}
	}

	write("a_literal.go", `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

// Kind: "in_leading_comment" —— 注释里的 Kind: 不该被统计
func a() {
	_ = llmbff.ChatRequest{
		// Kind: "in_inline_comment",
		Kind: "injected_kind",
	}
}
`)

	// ★ 本文件最核心的坑：位置参数只进计费，work_type 仍是空串。
	write("b_positional_only.go", `package server

import (
	"context"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

func b(s *llmbff.Service) {
	_, _ = s.Chat(context.Background(), llmbff.ChatRequest{
		Model: "x",
	}, "meeting")
}
`)

	// 坑 1：函数签名的返回类型。
	write("c_signature.go", `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

func cResolve(req llmbff.ChatRequest) llmbff.ChatRequest {
	_ = req
	return req
}
`)

	// 坑 2：别的包的同名类型。
	write("d_other_package.go", `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmgateway"

func d() {
	_ = llmgateway.ChatRequest{
		WorkType: "should_not_count",
	}
}
`)

	// 坑 3：非字面量表达式 + 跨行配平。
	write("e_expr_multiline.go", `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

func e(body struct{ Kind string }) {
	_ = llmbff.ChatRequest{
		// 这里故意放一长串注释，用来验证配平对注释长度不敏感。
		// 固定字符窗口的抽取器在这里会截断或溢出。
		Model: "y",
		Kind:  body.Kind,
	}
}
`)

	// 域外：_test.go 与 testdata/ 必须被跳过。
	write("f_test.go", `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

func f() {
	_ = llmbff.ChatRequest{Kind: "from_test_file"}
}
`)
	write(filepath.Join("testdata", "g.go"), `package server

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

func g() {
	_ = llmbff.ChatRequest{Kind: "from_testdata"}
}
`)

	sites := collectGoSites(t, dir)
	byBase := map[string]kindSite{}
	for _, s := range sites {
		byBase[filepath.Base(s.File)] = s
	}

	if len(sites) != 3 {
		var names []string
		for _, s := range sites {
			names = append(names, filepath.Base(s.File)+" kind="+s.Kind+" expr="+s.KindExpr)
		}
		sort.Strings(names)
		t.Fatalf("期望恰好 3 个构造点（c 的返回类型、d 的别的包、f 的 _test.go、"+
			"g 的 testdata 都要被排除），实际 %d 个：%v", len(sites), names)
	}

	if got := byBase["a_literal.go"].Kind; got != "injected_kind" {
		t.Fatalf("a 的 Kind=%q，期望 \"injected_kind\"", got)
	}
	if got := byBase["e_expr_multiline.go"].KindExpr; got != "body.Kind" {
		t.Fatalf("e 的 KindExpr=%q，期望 \"body.Kind\" —— 非字面量没被识别", got)
	}
	if got := byBase["e_expr_multiline.go"].Kind; got != "" {
		t.Fatalf("e 的 Kind=%q，期望 \"\"（body.Kind 是表达式不是字面量）", got)
	}

	// ★ 核心断言：位置参数不得被当成 Kind。
	pos, ok := byBase["b_positional_only.go"]
	if !ok {
		t.Fatal("夹具 b 没被扫到 ⇒ 抽取器恒返回空，上面各门的读数全部不可信")
	}
	if pos.Kind != "" {
		t.Fatalf("夹具 b 的 Kind=%q：抽取器把第 3 个位置参数当成了 ChatRequest.Kind。\n"+
			"那正是 Service.Chat(ctx, req, kind) 里那个**只进 RecordUsage** 的参数，"+
			"它到不了网关 —— 判据会因此恒真。", pos.Kind)
	}
	if pos.PositionalKind != "meeting" {
		t.Fatalf("夹具 b 的位置参数读到 %q，期望 \"meeting\"（坑 4：不能一见到 } 就停）",
			pos.PositionalKind)
	}

	// 门 1 的判据函数必须能把「没写 Kind」的点判成 offender。
	offenders := kindlessSites(sites)
	if !containsFile(offenders, "b_positional_only.go") {
		t.Fatalf("门 1 的 offenders=%v 里没有「没写 Kind」的夹具 ⇒ 该判据恒真", offenders)
	}
	if containsFile(offenders, "a_literal.go") || containsFile(offenders, "e_expr_multiline.go") {
		t.Fatalf("写了 Kind 的夹具却被算成漏写：%v", offenders)
	}

	t.Logf("量具自证通过：7 夹具中扫到 3 点（返回类型/别的包/_test.go/testdata 已排除）；"+
		"字面量 %q 与表达式 %q 正确区分；位置参数 %q 未被当作 Kind",
		byBase["a_literal.go"].Kind, byBase["e_expr_multiline.go"].KindExpr, pos.PositionalKind)
}

func kindlessSites(sites []kindSite) []string {
	var out []string
	for _, s := range sites {
		if !s.hasKind() {
			out = append(out, siteKey(s))
		}
	}
	sort.Strings(out)
	return out
}

func containsFile(keys []string, base string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, base) {
			return true
		}
	}
	return false
}
