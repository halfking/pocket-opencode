// wiring_gate_test.go — §489：真网关探针**按函数**接完整性对账。
//
// ⚠⚠ 本文件**收回**了 §488 的 `internal/server/live_ledger_wiring_gate_test.go`。
// 那不是推翻，是它**判据粒度比风险粒度粗一整级**：
//
// 　§488 的门断的是**文件**：liveProbeFiles 里写着 "live_gateway_probe_test.go"，
// 　而那个文件里有 **7 个**真网关探针 ——
// 　**只有 1 个**（RefineFixesASRErrors）接了账本，而门**全绿**。
//
// 　⇒ 「这个文件接了账本」与「这个文件里的探针接了账本」是两件事，
// 　而在退出码上**毫无区别**。
//
// ★ 这就是 §488 自己在文件头写的那个家族，落在自己身上：
// 　　「完整性对账存在、却保护不了任何东西」。
// 　判据只要**抓得比风险粗一级**，它保护的就是自己的覆盖率报告，而不是探针。
//
// ── 粒度必须对齐：风险在函数级，门就得在函数级 ──────────────────
//
// 　一个文件里可以有 N 个探针，其中任意子集有账本。
// 　⇒ 判据必须逐个 `func TestXxx(t *testing.T)` 地断。
//
// ── 判据本身不能是「猜」出来的 ────────────────────────────────────
//
// 　⚠ 这里刻意**不**去推断「哪个探针有危险形态」：
// 　「循环 + continue + 小结」这类字符串特征组合，我已在 §486 吃过一次亏
// 　（用「含 ASCII」当「标识符」类的判据，结果 `Polaris` 让它恒真）。
// 　⇒ **特征推断出来的判据，一旦两类共享同一表面特征，就必然恒真或恒假。**
//
// 　所以这里反过来做：**登记全部 24 个网关门控探针**，逐个写明它属于哪一类，
// 　门做的是**双向精确匹配**（漏登记红 / 登记陈旧红 / 两表重复红）。
//
// ── 四张表（§490：第三、四张是为「让账闭合」而必需的）──────────────
//
// 　只登记「危险的那些」的话，另外 13 个会永远对不上任何一张表，
// 　而下一个人看到「门红着、缺口表里却没有」，第一反应是「门坏了」。
// 　⇒ 四张表**恰好等于**扫描出的 24 个，且门断等式。
package livetest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// probePkgRoots 是被盘点的包（相对本包目录）。
//
// ⚠ 这些门是**源码级**判据，跑在 `go test` 里，
//
//	工作目录恒为该包目录 ⇒ `../server` / `../stt` 稳定成立。
var probePkgRoots = []string{"../server", "../stt"}

var testFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]*)\s*\(\s*t\s+\*testing\.T\s*\)`)

// gatewayGateRe 判「这个测试函数体里出现了真网关 env 键」。
//
// ⚠⚠ 这必须剥离注释与字符串字面量后再判，否则：
// 　① 注释里写的 `POCKET_LIVE_GATEWAY=1`（运行说明）会把**每个**文件都算进来；
// 　② 反引号原始字符串里的负控样本（§467 踩过）会造成同样的假阳性。
var gatewayGateRe = regexp.MustCompile(`POCKET_LIVE_GATEWAY|STT_LIVE_MINIMAX_KEY|POCKET_PROBE_SCOPE`)

// stripSrc 去掉 // 与 /* */ 注释，但**保留字符串字面量**。
//
// ⚠⚠ 为什么保留字符串：本仓两道门都栽在「掩码抹掉了我要找的东西」
// （§464/§467）。但这里有个反向风险 —— 负控样本就住在字符串里。
// ⇒ 结论：**剥注释、不剥字符串**，
// 　而负控样本用 `gatewayGateRe` 匹配不到（它写的是 `t.Skip` 而不是 env 键）。
func stripSrc(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			if i > len(src) {
				i = len(src)
			}
		case c == '"' || c == '\'' || c == '`':
			q := c
			j := i + 1
			for j < len(src) {
				if q != '`' && src[j] == '\\' {
					j += 2
					continue
				}
				if src[j] == q {
					j++
					break
				}
				j++
			}
			b.WriteString(src[i:j])
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// skipStringLit 返回该字符串字面量**结束之后**的位置；未闭合则返回 len(src)。
//
// ⚠⚠⚠ §501 新增。这是本文件三个配平器共用的底座，缺了它就会出现
//
//	「配平跑飞」这一整类静默失效：源码里**字符串字面量内部**的括号
//	被当成了结构，于是深度永久失衡、函数体/调用跨度一路吞到文件末尾。
//
// 　实测（`../server` + `../stt` 全部 *_test.go）：901 个 Test 函数里
// 　**25 个**函数体跑飞，最长的那个体内有 18 个 `func `、21296 字节。
// 　本仓当时一个都没登记进四张表，所以那 25 处**尚未造成实际伤害**；
// 　但任何一个只要沾上网关 env 键，就会在账本门里变成一个假的
// 　「未登记探针」—— 本轮真的踩到了（TestFabNonJudgedBranchesLeaveATrace）。
//
// 　⚠ 与 stripSrc 保留字符串并不矛盾：**剥字符串只用于定位结束位置**，
//
//	被检查/被返回的文本仍是含字符串的原文 —— 否则又回到
//	「掩码抹掉了我要找的东西」（§464/§467 的教训）。
func skipStringLit(src string, at int) int {
	q := src[at]
	for i := at + 1; i < len(src); i++ {
		switch q {
		case '`':
			if src[i] == '`' { // 反引号原样串：不处理转义
				return i + 1
			}
		case '"', '\'':
			if src[i] == '\\' {
				i++ // 跳过被转义的那个字符
				continue
			}
			if src[i] == q {
				return i + 1
			}
		}
	}
	return len(src) // 未闭合：就此为止，调用方会看到一个超长跨度并自行报红
}

// funcBody 从 `{` 起做花括号配平，取函数体源码。
//
// ⚠⚠ §501：配平时**跳过字符串字面量**（见 skipStringLit 的实测读数）。
// 　此前它把 `"switch …(arm, filter) {"` 这种字面量里的 `{` 当成结构，
// 　深度再也回不到 0 ⇒ 函数体被算成「从这里到文件末尾」。
func funcBody(code string, afterHeader int) (string, int) {
	open := strings.Index(code[afterHeader:], "{")
	if open < 0 {
		return "", -1
	}
	open += afterHeader
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '"', '`', '\'':
			i = skipStringLit(code, i) - 1
			continue
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return code[open:i], i
			}
		}
	}
	return code[open:], len(code)
}

// countTopLevelFuncOutsideStrings 数「字符串字面量**之外**、出现在行首」的 `func `。
//
// ⚠⚠⚠ §505 新增。§501.4 登记过「901 个 Test 函数里还有 3 个函数体跑飞，根因未查清」，
//
//	线索是「它们的字面量里有不配平的括号」。**那个判断是错的** —— 根因不是配平器，
//	而是**判据自己**：`strings.Count(body, "\nfunc ")` 把「函数体合法地包含一段
//	装着 Go 源码的 raw string 样本」误读成「吞进了兄弟函数」。
//
// 　实测（`../server` + `../stt` 全部 *_test.go，901 个 Test 函数）：
//
//	| 函数 | 旧判据 | 本判据 |
//	|---|---|---|
//	| long_lived_route_audit_test.go:840 TestUncoveredLongHandlerIsReported | 2 | **0** |
//	| long_lived_route_audit_test.go:887 TestCoveredLongHandlerIsNotReported | 2 | **0** |
//	| live_probe_skip_ledger_test.go:265 TestSkipLedgerExtractorIgnoresComments | 1 | **0** |
//
// 　这三处的函数体里都有 `const synthetic = ` + 反引号 Go 样本，样本里确有行首 `func `。
//
// ⇒ 与 funcBody 的取舍同源：**定位时跳字符串，被检查的文本仍是含字符串的原文。**
// ⚠ 列位置**不能**用来区分：raw string 样本里的 `func ` 同样在行首；
//
//	唯一能区分的就是「它在不在字符串里」。
func countTopLevelFuncOutsideStrings(body string) int {
	n := 0
	for i := 0; i < len(body); {
		switch body[i] {
		case '"', '`', '\'':
			next := skipStringLit(body, i)
			if next <= i { // 未闭合：跳 1 字节，避免死循环
				i++
			} else {
				i = next
			}
		case 'f':
			if (i == 0 || body[i-1] == '\n') && strings.HasPrefix(body[i:], "func ") {
				n++
			}
			i++
		default:
			i++
		}
	}
	return n
}

type probeSite struct {
	file string
	body string
}

// wired 报告这个探针函数体里是否真的接了完整性对账。
func (p probeSite) wired() bool {
	return strings.Contains(p.body, "livetest.New(") &&
		strings.Contains(p.body, "ledger.Complete(") &&
		strings.Contains(p.body, "ledger.Report()")
}

// gatewayProbes 抽出全部「函数体内出现网关 env 键」的 Test 函数。
func gatewayProbes(t *testing.T) map[string]probeSite {
	t.Helper()
	out := map[string]probeSite{}
	for _, root := range probePkgRoots {
		files, err := filepath.Glob(filepath.Join(root, "*_test.go"))
		if err != nil {
			t.Fatalf("列 %s 失败: %v", root, err)
		}
		if len(files) == 0 {
			t.Fatalf("⚠ 量具失效：%s 下没有任何 *_test.go —— 先怀疑路径，再怀疑代码", root)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("读 %s 失败: %v", f, err)
			}
			code := stripSrc(string(raw))
			for _, loc := range testFuncRe.FindAllStringIndex(code, -1) {
				name := testFuncRe.FindStringSubmatch(code[loc[0]:loc[1]])[1]
				body, _ := funcBody(code, loc[1])
				if !gatewayGateRe.MatchString(body) {
					continue
				}
				if prev, dup := out[name]; dup {
					t.Fatalf("测试名 %s 在 %s 与 %s 重复 —— 判据按名字索引，重名会让其中一条永远核不到",
						name, prev.file, f)
				}
				out[name] = probeSite{file: f, body: body}
			}
		}
	}
	return out
}

// ── 表一：已接账本 ────────────────────────────────────────────────────
//
// 门会逐个复核接线细节（含 Report 在读数之前、分母被打印）。
var ledgerWired = []string{
	// §488：实测过耗时的那条（560–1680s）。
	"TestLiveGatewayRefineFixesASRErrors",
	// §489：**头条量具**。§474/§481 报给用户的每个 CER 数字都出自它，
	// 而它有两条 continue（TTS/ASR 失败）⇒ 少跑几条，CER 仍然是个
	// 「看起来正常的百分比」，只有账本会出声。
	"TestLiveGatewayASRCer",
	// §491：**§478「切得越碎损得越狠」的唯一量具**。
	// 它有两处静默丢分片（跳过档不留痕迹 + 失败片被 `if p != ""` 滤掉），
	// 而末尾照旧打印「与基准一致? false」⇒ 读者会把**片丢了**读成**分片更差**。
	"TestLiveGatewayFullVsChunked",
	// §492：4 臂诊断探针（走 fmt.Printf 而非 t.Logf）。
	"TestDiagStreamZeroFrames",
	// §493：5 个重叠切点 + 两次 continue + 末尾一条「汇总：去重生效 N 次」。
	"TestLiveGatewayASRMergeOverlappingChunks",
	// §494：**#31 在「生产切分」探针上的落点**，原先从未登记。
	// 它原来的登记理由是错的（原写「有用段数要先用 POCKET_LIVE_SPLIT_ONLY=1
	// 打印出来再写死进 total」）—— 可那个数它**自己早就算出来了**：
	// 预算那个循环的条件正是 `s.IsUsefulSegment()`，`calls = 1 + 有用段数`；
	// 而 SPLIT_ONLY 模式在打印切段计划后就 `return` 了，**根本走不到 B/C 臂**，
	// 照那条建议做等于把一个只在真网关跑时才有意义的值固化成字面量。
	// 真正的缺陷是另一个：转写失败时 `tx` 返回空串，而空串照旧被追加进
	// `segTexts` ⇒ B 臂 `strings.Join` 多出一个空行，`normText` 用
	// `strings.Fields` 把它整个抹平 ⇒「B 与 A 一致」把没转出来的段
	// 当成了转出来了；C 臂的 `mergeIncremental` 反有空串守卫跳过它
	// ⇒ B/C 两臂分段数悄悄不同。
	// ⇒ 改成三态分流 + 分母来自账本 `calls-1` + 对账打在比较之前。
	"TestLiveGatewayProdSegment",
	// §495：**「预期跳过」在这个包里唯一的落地处**。
	// 它有四条 continue 出口（§494 逐条核过）：
	//   超时长（`c.endA > dur || c.startB >= dur || c.endA <= c.startA`）—— 先验，可豁免
	//   B 片装不下（`endB-c.startB < 0.15`）—— 先验，可豁免
	//   片 A / 片 B 转写失败 —— **真失败，故意不豁免**
	// 而末尾分组表打印的 `n` 与 `hit/n` 从来不声明「本应有几条」，
	// 且某组 `n == 0` 时那一行整行不打印 ⇒ 该组直接从读数里消失。
	// ⚠ 豁免**只给那两条先验的**：记成 Skip 就等于把失败洗成设计。
	"TestLiveGatewayOverlapTimingVsText",
	// §495：**三态映射**的第二个落地处。分母用 §492 指出的那个表达式
	// （`len(models)*rounds*len(arms)`，与 `total++` 处在循环里恒等 —— 正因恒等，
	// 它从来不是缺口）。真缺口是那四个计数器没有任何地方对账：
	// 新加一条 `continue` 忘了 ++ 时，四桶合计会静默变小，
	// 而「有内容 %d」照打 ⇒ 分母在无声地错。
	// ⚠ 「空/缺」算 Complete：**「模型明确没给出下一个会议」是有效读数**，不是没跑成；
	//   它已被 emptyCount 单独计数，不会与 ok 混成同一个数。
	"TestLiveNextMeetingRaw",
	// §500：缺口表最后一条离线能关的。三态映射与上面那条同口径 ——
	// 拿到读数算 Complete（含「⟪被兜底拦下⟫」那种非空但无效的输出，它仍是一次跑成）；
	// **三试都失败故意不记**（真失败不是先验豁免，不记即被 Report 算成「没做完」）。
	// ⚠ 它先前登记的修法是**两半**：① 接账本 ② 逐臂印「有效 %d / 应跑 %d」。
	//   §500 只做了 ②（口径抽成 abArmCountOf + 对账抢在比率之前），
	//   ① 是本轮补上的 —— 半修的登记留在表里比不修更坏：
	//   下一个读表的人会以为缺口已经关掉。
	"TestLiveGatewayRefineTopicVsTitle",
	// §501：缺口表最后一条。三处缺陷连修，每一处都有**独立的门**：
	// 　① §494：F 臂（toolless）不进 stats 却撞 nil 守卫 ⇒ 默认跑法 100% 崩。
	//     修法是读数据自己写明的 `toolless`，不是放宽守卫。
	// 　② §497：被 POCKET_FAB_ARMS 排除的臂在汇总里静默消失。
	// 　③ §499：三处同形的 filter 判断收敛成 `fabDispositionOf`，排序契约（Excluded 优先）可离线判。
	//
	// ⭐ 账本的 total 是**按 disposition 算**的：每臂次数 × 本轮判为 judged 的臂数。
	//   被排除的臂与 toolless 臂本轮本就不该跑；把它们算进分母就会逼着给
	//   「按设计不跑」记 Complete，而「用户排除」**不是先验理由**。
	//   ⇒ 二者不可互替：分母只算 judged，被排除的臂由留痕行负责出声。
	//
	// ⚠⚠ Complete 只记 `Verdict != verdictCollectFailed` 的那次。
	//   若每次 append 都记，这条账本就恒真（§494 已核：采样循环里没有任何
	//   continue 能绕开 `all = append(all, rec)`）⇒ Done() 恒等于 Total()，
	//   Report() 永不出声 ⇒ 恒真的对账等于没有对账。
	"TestLiveGatewayRefsFabricationRate",
}

// probeEntry 是登记条目。三张表共用一个结构，
// 这样「必须写清理由」这条纪律对**每一类**都成立 ——
// 空话解锁不了任何东西，而空话在第四张表里尤其容易写。
type probeEntry struct {
	test string
	// why 现在是这个状态（必须具体到「缺什么 / 被什么兜住」，不能写「暂缓」）。
	why string
	// todo 补它需要什么（不能为空 —— 空话解锁不了任何东西）。
	todo string
}

// ── 表二：真缺口 ──────────────────────────────────────────────────────
//
// 形如 §476（循环里 continue + 末尾完整外观小结），本轮**故意没接**。
// ⚠⚠ 登记 ≠ 豁免。这张表存在的意义是让缺口**可见且有主人**。
var ledgerGap = []probeEntry{
	// ⚠⚠§501 起这张表**空了**。空表不等于「没有缺口」——
	// 它只表示「每一个真网关探针都已被判别覆盖，且覆盖本身有门」。
	// ⛔ 已知的三个**未测**面（本表管不着，因为它们不是探针）：
	//   ① §494–§501 所有修复在真网关下的读数（凭据缺失，一条没验过）；
	//   ② 真人会议录音的 CER（噪声/多人/方言/远场）；
	//   ③ #29 的两条阻塞（噪声底噪 30% vs 5 条样本、模型非确定）。
	// ⇒ 下次要往这张表里加东西时，先问「我上轮是不是也这么想过」。
}

// ── 表三：形态危险但已被别的机制兜住 ──────────────────────────────────
//
// ⚠ 这张表最容易被当成「变相豁免」，所以每条都必须写清**兜住它的是什么**——
// 　「已有人工确认」不算，只有**代码里可指认的东西**才算。
var ledgerSelfProtected = []probeEntry{
	{"TestLiveGatewayRefineTermHintTopic",
		"有 continue 跳过失败臂，但**失败臂被 failedArms 计数**，且末尾有显式结论分支：" +
			"failedArms>0 时打印「⚠⚠ 有 N 个臂失败 ⇒ **噪声底噪未测到**」并说明" +
			"「S1 即使显示 ON 修好也不能归因给主题」；==0 时打印「三样本六臂全部成功」" +
			"⇒ 它的输出**自带完整性声明**，与账本等价",
		"接账本可让它与 §488 那批口径统一；在此之前以 failedArms 分支为准"},
}

// ── 表四：根本不存在部分完成形态 ──────────────────────────────────────
//
// ⛔ 为什么这张表必须存在：只登记「危险的那些」的话，
//
//	扫出来的 24 个会有 13 个永远对不上任何一张表 ——
//	而下一个人看到「门红着、缺口表里却没有」，第一反应是「门坏了」。
//	⇒ 把「**为什么它安全**」也登记下来，门才有**闭合的账**。
//
// ⚠ 「安全」在这里只指「不会出现截断的跑被当成完整结果上报」这一条，
//
//	**不代表它测出来的东西是对的** —— 那是各探针自己的判据负责的事。
var ledgerNotDangerous = []probeEntry{
	{"TestLiveGatewayASRCandidateSupply",
		"⛔ §493 收回原理由（原写「total 不能在编译期写死」—— 那是**运行时**，" +
			"而 `len(cands)` 在循环前就知道，完全可接）。逐条复核后：**它没有部分完成形态**——" +
			"每个候选用独立的 90s ctx 跑到底，失败**计入 nosupply** 而非 continue；" +
			"外层 60s ctx 只用于列目录。⭐ §493 修的是**另一个**真缺陷：" +
			"「返回空文本」那一支原先 usable/nosupply **两个桶都不进** ⇒ 「可用 N 个」的分母是错的" +
			"且无人解释（#31 的落点）—— 现已三桶齐全并断言对账",
		"无需动作；若将来改成「失败即跳过」，必须重评"},
	{"TestLiveGatewayMeetingRefineLongTranscript",
		"⛔ §492 收回原理由（原写「接账本，total = len(probeMeetingSegs)」—— " +
			"**那不是它的循环**：它先把 8 轮 × len(segs) 摊成**一个** segs，再**单次**调用）。" +
			"真理由：它只有**一次**网关调用且失败即 t.Fatalf ⇒ 本来就**没有部分完成形态**",
		"无需动作；若将来改成逐段调用，再重评"},
	{"TestLiveGatewayOneASRModel",
		"单次调用（无循环），且失败即 t.Skip/t.Fatalf —— 没有「跑了一半」的中间态",
		"无需动作；若将来改成多模型循环，必须重评"},
	{"TestLiveGatewayMeetingRefine",
		"单次调用，无循环；空结果已 t.Fatal",
		"无需动作；若将来加入分窗口循环，必须重评"},
	{"TestLiveGatewayNoteSummaryJSON",
		"单次调用；解析出空 summary 已 t.Error",
		"无需动作；若将来加入多样本循环，必须重评"},
	{"TestLiveGatewayAgentToolCall",
		"两次固定调用（A 带工具 / B 无工具），无循环；两臂结论为空各有 t.Error。" +
			"⚠ 它的判据是「A 臂有没有真的调工具」，而那本身是可变量（模型可能不调）—— " +
			"但那是**能力**问题不是完整性问题，且它已用 toolCalls 显式区分了两种情况",
		"无需动作；若将来加入多夹具/多轮，必须重评"},
	{"TestLiveGatewayAgentReferencesAreReal",
		"单次 agent 调用；解析失败已 t.Fatalf，编造 id 逐条比对无「跳过」语义",
		"无需动作；若将来改为多样本，必须重评"},
	{"TestLiveGatewayMeetingRecommend",
		"单次网关调用；循环只遍历**已解析出的** items（打印用），" +
			"调用失败是 t.Fatal 而非 continue",
		"无需动作；若将来加入重试/多模型，必须重评"},
	{"TestLiveASRCandidateCoverage",
		"循环遍历的是**网关模型列表**做集合运算，全部无 continue；" +
			"列目录失败是 t.Fatalf（整个测试停）",
		"无需动作；若将来改成逐个实调模型，必须重评"},
	{"TestNoteSearcherScopeProbe",
		"单次 Execute 调用 + 计数断言（期望 ListScoped 被调 1 次，实得不等即 Fail）",
		"无需动作；若将来改为多样本，必须重评"},
	{"TestLiveGatewayASR",
		"单次转写调用（无循环）；失败即 t.Fatalf，成功后逐字段断言。" +
			"⚠ 它在 skip 台账里登记着要 POCKET_LIVE_ASR_AUDIO，" +
			"但那与完整性无关（是**能不能跑**，不是**跑完没跑完**）",
		"无需动作；若将来加入多音频/多模型循环，必须重评"},
	{"TestLiveGatewayRollingSummary",
		"逐轮循环，但**每轮失败都是 t.Fatalf**（不是 continue）⇒ " +
			"中途失败时整个测试立刻红，不会留下「跑了一半」的完整外观读数。" +
			"⚠ 它同时对每轮 summary 做了非空硬断言",
		"无需动作；若将来把 t.Fatalf 改成 continue，必须重评"},
	{"TestLiveLongStream",
		"单次流式请求，失败即 `fmt.Printf` + `return`。⚠ 它用 Printf 而非 t.Logf，" +
			"所以失败时**退出码仍是 0** —— 但因为只有一次请求，不存在「跑了一半」的形态，" +
			"输出里必然有「请求失败」那一行。（⚠ 这条门只管完整性，不管它该不该 Fatal）",
		"无需动作；另记：它该改成 t.Fatalf 才不影响 CI，但那超出本门的职责"},
}

// ── 判据 ⑤：配平器不许跑飞（§501 新增）────────────────────────────
//
// ⚠⚠ 背景是本轮踩到的一次真事故：新写的 `TestFabNonJudgedBranchesLeaveATrace`
//
//	里有一行锚点字符串 `"switch fabDispositionOf(arm, filter) {"` ——
//	末尾那个 `{` 在**字符串字面量**里。配平器此前也数字符串，
//	于是深度再也回不到 0，函数体被算成「从这里到文件末尾」
//	（实测 21296 字节、体内 18 个 `func `），
//	连带把本门自己变成了「一个未登记的网关探针」并当场报红。
//
// ✅ 根因已修：funcBody / balancedEnd / balancedBrace 现在都跳过字符串字面量
//
//	（skipStringLit）。实测 901 个 Test 函数的跑飞数由 **25 降到 3**。
//
// ✅⚠⚠ §505：那剩下的 3 个**已查清，且根因不是配平器** —— 是本判据自己的假阳性。
//
//	旧判据 `strings.Count(body, "\nfunc ")` 无法区分「吞进了兄弟函数」与
//	「函数体合法地包含一段装着 Go 源码的 raw string 样本」，而那三处都属后者
//	（long_lived_route_audit_test.go 的两处 `const synthetic = ` 反引号样本、
//	 live_probe_skip_ledger_test.go 的 `` `package stt `` 负控样本）。
//	⇒ 改用 countTopLevelFuncOutsideStrings（跳字符串再数）后，全量读数 **3 → 0**。
//	⚠ 详见该函数的注释表。
//
// ⇒ 这条判据**只对已登记探针**生效：跑飞在别处是别人的事，
//
//	而在这 24 个身上是本门自己的判断依据不可信 ⇒ 必须红。
func TestRegisteredProbesHaveNonRunawayBodies(t *testing.T) {
	got := gatewayProbes(t)
	checked := 0
	for _, e := range allRegisteredProbes() {
		site, ok := got[e]
		if !ok {
			continue // 判据①已报过
		}
		checked++
		if n := countTopLevelFuncOutsideStrings(site.body); n > 0 {
			t.Errorf("%s（%s）配平跑飞：函数体被算成 %d 字节、体内还有 %d 个顶层 func ⇒ "+
				"本门对它做的「Report 在汇总之前」「分母被打印」等判据都是**在错误区间上**执行的",
				e, site.file, len(site.body), n)
		}
	}
	// ⚠ 量具自证：一条都数不到 ⇒ 这条判据在空转，必须报红而不是安静地绿。
	if checked == 0 {
		t.Fatal("已登记探针一个都没取到 ⇒ 这条判据量具失效（先怀疑路径与函数名，再怀疑代码）")
	}
	t.Logf("已核对 %d 个已登记探针的函数体区间", checked)
}

// allRegisteredProbes 把四张表里的探针名合成一个集合（去重、保持稳定顺序）。
func allRegisteredProbes() []string {
	seen := map[string]bool{}
	var out []string
	add := func(names []string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	add(ledgerWired)
	for _, e := range ledgerGap {
		add([]string{e.test})
	}
	for _, e := range ledgerSelfProtected {
		add([]string{e.test})
	}
	for _, e := range ledgerNotDangerous {
		add([]string{e.test})
	}
	sort.Strings(out)
	return out
}

// TestSkipStringLitKeepsBraceMatchingHonest — §501 根因修复的**阳性对照**。
//
// ⚠ 为什么必须单独写这条：判据 ⑤ 今天**没有任何可触发的实例**
//
//	（24 个已登记探针的字面量里都没有不配平的括号）⇒ ⑤ 是**护栏**，
//	不是覆盖。而「护栏没有实例」与「护栏没有用」在读数上完全同形。
//
// ⇒ 这里直接喂一段**恰好会触发旧缺陷**的源码进去，验证配平不再跑飞。
//
// ⚠ 这段是喂给 stripSrc/funcBody 的**纯文本**，不参与编译 ⇒ 可以故意写坏。
func TestSkipStringLitKeepsBraceMatchingHonest(t *testing.T) {
	// 三个各自会骗过配平器的字面量：
	// 　① 双引号串里一个孤立的花括号（旧实现会让深度永不归零）
	// 　② 反引号串里装着 Go 源码，且源码里有 `//` 注释
	// 　③ 字符字面量 '{'
	const src = "func TestAlpha(t *testing.T) {\n" +
		"	open := \"{\"\n" +
		"	raw := `package x\n// func TestInComment(t *testing.T) { }\n`\n" +
		"	ch := '{'\n" +
		"	_ = open; _ = raw; _ = ch\n" +
		"}\n" +
		"func TestBeta(t *testing.T) {\n" +
		"	t.Fatal(\"不该被算进 TestAlpha 的函数体\")\n" +
		"}\n"

	code := stripSrc(src)
	// ⚠ 先自证 stripSrc 没有把 ② 那段吃掉 —— 它若吃了，反引号就失配，
	//   本条的失败原因会变成「另一个 bug」，与本条要验的东西混在一起。
	if !strings.Contains(code, "// func TestInComment") {
		t.Fatalf("量具失效：stripSrc 把反引号串里的 // 当注释删了（%q）", code)
	}

	loc := testFuncRe.FindStringIndex(code)
	if loc == nil {
		t.Fatal("量具失效：连函数头都认不出")
	}
	body, end := funcBody(code, loc[1])
	if strings.Contains(body, "\nfunc ") {
		t.Errorf("函数体跑飞：%d 字节，体内含别的函数 ⇒ 字符串字面量里的括号被当成了结构", len(body))
	}
	if strings.Contains(body, "TestBeta") {
		t.Error("TestAlpha 的函数体里混进了 TestBeta ⇒ 配平结束位置算错了")
	}
	if end <= 0 || end >= len(code) {
		t.Errorf("配平结束位置 %d 不合理（源码共 %d 字节）", end, len(code))
	}

	// ── 同一份阳性对照，顺带覆盖另两个配平器 ──────────────────
	// ⚠⚠ 为什么必须显式覆盖：变异实测里，把 balancedBrace 退回「不跳字符串」
	//   **全门没有任何可观测后果**（Q3 绿）—— 它今天没有能被触发的实例。
	//   「无后果」与「无价值」在读数上同形，所以这里直接喂一个必触发的样本。
	//
	// ⚠ 样本必须**编译得过**：balancedEnd/balancedBrace 收的是**源码片段**，
	//   而本文件里已有的若干用例把真实 Go 源码塞在 raw string 里当样本
	//   （`funcStub` 之类）。塞一段语法错的进去会让整包编译失败，
	//   那就不是判据红了、而是「本门测不了这个」—— 两者必须分开。
	const parenSrc = "t.Logf(\"空 (%d 次\", 1)\n"
	// ⚠⚠⚠ 这个字符串里**必须真的有那个孤立的 `(`**。
	// 首版写的是 `"空 %d 次"`（里面没有括号）⇒ 期望值与「不跳字符串」的算法碰巧一致
	// ⇒ 这半条判据**恒真**，而变异 Q4（balancedEnd 退回不跳字符串）实测全绿，
	// 正是它照出来的。恒真与「真的必然」在读数上完全同形。
	open := strings.Index(parenSrc, "(")
	// ⚠ 期望值不是 len(src)：配平器返回**闭合符之后**的位置，而样本末尾留着一个换行。
	//   （这处第一版写成了 len(src)，门当场报红 —— 是判据写错，不是代码写错。）
	wantParen := strings.LastIndex(parenSrc, ")") + 1
	if got := balancedEnd(parenSrc, open); got != wantParen {
		t.Errorf("balancedEnd = %d，期望 %d ⇒ 字符串里的括号被当成了结构",
			got, wantParen)
	}

	const braceSrc = "if x {\n\ty := \"{\"\n\t_ = y\n}\n"
	bo := strings.Index(braceSrc, "{")
	wantBrace := strings.LastIndex(braceSrc, "}") + 1
	if got := balancedBrace(braceSrc, bo); got != wantBrace {
		t.Errorf("balancedBrace = %d，期望 %d ⇒ 字符串里的花括号被当成了结构",
			got, wantBrace)
	}
}

// TestCountTopLevelFuncOutsideStringsSeparatesRunawayFromRawStrings — §505 新判据的**阳性对照**。
//
// ⚠⚠ 为什么必须单独写：§501.4 把「3 个跑飞」当成**配平器的缺陷**查了两轮没查出来，
//
//	真因是判据把「函数体里合法地含着一段 raw string Go 样本」误读成「吞了兄弟函数」。
//	⇒ 换成 countTopLevelFuncOutsideStrings 后全量读数 3 → 0。
//	⚠ 但「把假阳性消掉」与「把真阳性一起消掉」在读数上**完全同形**
//
//	—— 只报 0 的话，没人能说清它是修好了还是瞎了。
//
// ⇒ 三条同时钉：不跳字符串的旧判据会在①②上误报；新判据在①上报真、在②③上不报。
func TestCountTopLevelFuncOutsideStringsSeparatesRunawayFromRawStrings(t *testing.T) {
	// ① 真阳性：一个确实吞掉了兄弟函数的函数体（体内有**不在字符串里**的 `func `）
	const reallyRunaway = "\nfunc TestBeta(t *testing.T) {\n\tt.Fatal(\"x\")\n}\n"
	if got := countTopLevelFuncOutsideStrings(reallyRunaway); got != 1 {
		t.Errorf("① 真跑飞应数到 1，实测 %d ⇒ 新判据漏报了真阳性（它比旧判据还弱）", got)
	}
	// ⚠ 对照：旧判据在这里也报 1 —— 说明①本身不是「两判据一致」的平凡用例。
	if got := strings.Count(reallyRunaway, "\nfunc "); got != 1 {
		t.Fatalf("量具失效：旧判据在①上应数到 1，实测 %d", got)
	}

	// ② 假阳性本体：raw string 里装着 Go 源码，`func ` 在行首但在字符串内
	const rawSample = "\n\ts := `package x\nfunc TestInSample(t *testing.T) { }\n`\n\t_ = s\n"
	if got := countTopLevelFuncOutsideStrings(rawSample); got != 0 {
		t.Errorf("② raw string 里的 func 被当成了顶层函数：实测 %d，期望 0", got)
	}
	// ⚠ 这正是 §501.4 登记的那三处的形态 —— 旧判据在这里报 1。
	if got := strings.Count(rawSample, "\nfunc "); got != 1 {
		t.Fatalf("量具失效：旧判据在②上应数到 1（这才是假阳性的来源），实测 %d", got)
	}

	// ③ 双引号串里同样不行（不只是反引号）
	const dqSample = "\n\ts := \"func TestInString(t *testing.T) {}\\n\"\n\t_ = s\n"
	if got := countTopLevelFuncOutsideStrings(dqSample); got != 0 {
		t.Errorf("③ 双引号串里的 func 被当成了顶层函数：实测 %d，期望 0", got)
	}

	// ④ 体首（无前导换行）也必须数到 —— `strings.Count(x, "\nfunc ")` 在这里恒为 0
	const atBodyStart = "func TestGamma(t *testing.T) { }\n"
	if got := countTopLevelFuncOutsideStrings(atBodyStart); got != 1 {
		t.Errorf("④ 体首的 func 应数到 1，实测 %d", got)
	}
	if got := strings.Count(atBodyStart, "\nfunc "); got != 0 {
		t.Fatalf("量具失效：旧判据在④上应恒为 0（它数不到体首），实测 %d", got)
	}

	// ⑤ 字符字面量里的 'f' 不能被当成 func 的开头
	const charLit = "\n\tc := 'f'\n\tif c == 'f' { }\n"
	if got := countTopLevelFuncOutsideStrings(charLit); got != 0 {
		t.Errorf("⑤ 字符字面量 'f' 被误判成 func：实测 %d，期望 0", got)
	}

	// ⑥ 「必须在行首」这一条本身也要承重。
	//    缩进的 func 在**字符串之外**，但它是嵌套声明/闭包，不是「吞进来的兄弟函数」。
	// ⚠⚠ 这一条是 §505 第一版遗漏的：②③ 都把 func 写在**字符串里**，
	//    而「跳字符串但不看列位置」这个中间态照样是 0 ⇒ 变异 M2 全绿。
	//    ⇒ 区分它必须用一条「在字符串外、但不在行首」的样本。
	const indentedFunc = "\n\trun(func TestInNested(t *testing.T) { })\n"
	if got := countTopLevelFuncOutsideStrings(indentedFunc); got != 0 {
		t.Errorf("⑥ 缩进的 func（在字符串外）被当成了顶层函数：实测 %d，期望 0", got)
	}
	// ⚠ 对照：旧判据在这里恒为 0（它本来就要求行首），只有「跳字符串但不管列位置」
	//   这个中间态才会报 1 —— M2 变异打的就是它。
	if got := strings.Count(indentedFunc, "\nfunc "); got != 0 {
		t.Fatalf("量具失效：旧判据在⑥上应恒为 0，实测 %d", got)
	}
}

func TestGatewayProbeLedgerIsCompleteAndNotStale(t *testing.T) {
	got := gatewayProbes(t)

	// 纪律③：量具先自证。
	//
	// ⚠⚠ 这个数字必须随新增探针一起涨，否则「门没抓到」会被误读成「门有洞」。
	// 2026-10-11 实测扫到 24 个；若将来改动让它掉下来，先怀疑抽取器。
	if len(got) < 20 {
		t.Fatalf("量具失效：只扫到 %d 个网关门控探针（门槛 20）。\n"+
			"  ⚠ 这**不是**「探针变少了」—— 先怀疑 stripSrc / regex，再怀疑代码。",
			len(got))
	}

	registered := map[string]string{} // test → 所属表名
	for _, n := range ledgerWired {
		registered[n] = "ledgerWired"
	}
	for _, tbl := range []struct {
		name string
		list []probeEntry
	}{
		{"ledgerGap", ledgerGap},
		{"ledgerSelfProtected", ledgerSelfProtected},
		{"ledgerNotDangerous", ledgerNotDangerous},
	} {
		for _, e := range tbl.list {
			if prev, dup := registered[e.test]; dup {
				t.Errorf("%s 同时出现在 %s 与 %s 两张表里 —— 一个探针只能有一种状态",
					e.test, prev, tbl.name)
				continue
			}
			registered[e.test] = tbl.name
		}
	}

	// 方向①：代码里有网关探针，四张表都没登记 ⇒ 静默缺口。
	for name, site := range got {
		if _, ok := registered[name]; !ok {
			t.Errorf("网关探针未登记：%s（%s）\n"+
				"  ⇒ 四选一：已接账本(ledgerWired) / 真缺口(ledgerGap) /\n"+
				"     已有别的机制兜住(ledgerSelfProtected) / 形态不危险(ledgerNotDangerous)，\n"+
				"     后三张都必须写清 why 与 todo。\n"+
				"  ⚠ 不登记的后果不是「报错」，而是**覆盖率看起来是满的**。", name, site.file)
		}
	}
	// 方向②：表里有，代码里已经没有了 ⇒ 陈旧条目。
	//
	// ⚠⚠ 这一条是四张表**共同的**生命线：缺口表可以减、不能只增不减。
	for name, table := range registered {
		if _, ok := got[name]; !ok {
			t.Errorf("台账陈旧：%s 在 %s 里，但源码里找不到这个网关门控测试。\n"+
				"  ⇒ 它被改名/删除了，或已不再门控网关 —— 两种都要更新台账。\n"+
				"  ⚠ 台账只增不减就会烂成一张永久通行证。", name, table)
		}
	}
	// 每张表的 why/todo 都不能是空话。
	for _, tbl := range []struct {
		name string
		list []probeEntry
	}{
		{"ledgerGap", ledgerGap},
		{"ledgerSelfProtected", ledgerSelfProtected},
		{"ledgerNotDangerous", ledgerNotDangerous},
	} {
		for _, e := range tbl.list {
			if strings.TrimSpace(e.why) == "" || strings.TrimSpace(e.todo) == "" {
				t.Errorf("%s 条目 %s 缺 why 或 todo —— 「暂缓」不是理由，空话解锁不了任何东西",
					tbl.name, e.test)
			}
		}
	}

	// ── 等式断言：登记数必须**恰好**等于扫描数 ──────────────────────
	//
	// ⚠⚠⚠ 这条是变异 M6 逼出来的，**不是**预防性设计。
	//
	// 变异 M6 把「未登记就报错」改成恒假（等于把那个分支整个废掉），
	// 结果**全门仍绿** —— 因为当时 24 个探针全部已登记，
	// 那个分支本来就没有内容可报。⇒ 方向①的有效性依赖「它恰好没被改坏」。
	//
	// ⇒ 这里用一个**独立于任何循环体**的等式把它钉死：
	//	多一个、少一个，都必须红。它不关心是谁发现的，只关心等式不成立。
	if len(registered) != len(got) {
		t.Errorf("登记数 %d ≠ 扫描数 %d —— 双向精确匹配被破坏了。\n"+
			"  ⚠ 这条等式是**故意**独立于逐条报错的：变异实测表明，"+
			"把逐条检查改坏时全门仍绿（因为那时它本就没有内容可报）。\n"+
			"  ⇒ 逐条检查给你「是哪一条」，等式给你「判据本身还活着」。",
			len(registered), len(got))
	}
}

// ── 判据 ②：登记为 wired 的，代码里必须**真的**接了 ──────────────────
//
// ⚠ 这条与判据①独立：①只保证「登记与代码不脱节」，
// 它**不**保证「登记为 wired 的那个真接了」——
// 把一个函数名挪进 ledgerWired 就能骗过①。
func TestWiredProbesActuallyWireTheLedger(t *testing.T) {
	got := gatewayProbes(t)
	for _, name := range ledgerWired {
		site, ok := got[name]
		if !ok {
			continue // 方向②已经报过了
		}
		if !site.wired() {
			t.Errorf("%s 登记为已接账本，但函数体里找不到 livetest.New/ledger.Complete/ledger.Report",
				name)
		}
	}
}

// ── 判据 ③：登记为 gap 的，代码里必须**真的还没接** ────────────────
//
// ⚠⚠ 这条是本门最容易被绕过的一条，也是它区别于「一张愿望清单」的地方：
//
// 　有人把某个 gap 的探针**补上了账本**，却忘了把它从 ledgerGap 挪走
// 　⇒ 表与代码脱节 ⇒ 而缺口表**只减不增**时，没人会发现。
// 　⇒ 这里主动要求：补上了就从表里删掉。
func TestGapProbesStillLackTheLedger(t *testing.T) {
	got := gatewayProbes(t)
	for _, e := range ledgerGap {
		site, ok := got[e.test]
		if !ok {
			continue
		}
		if site.wired() {
			t.Errorf("%s 已登记为「尚未接账本」，但它现在接上了。\n"+
				"  ⇒ 很好 —— 请把它从 ledgerGap 挪进 ledgerWired，否则这张缺口表会慢慢变成谎话。",
				e.test)
		}
	}
}

// ── 判据 ④：对账必须在打印汇总**之前**，且分母必须被打印 ──────────────
//
// ⚠⚠ 这是整套机制里唯一防 §476.1 ① 那条形态的判据。
//
// `Report()` 若在汇总**之后**才调用，不完整的分母已经先印出去了 ——
// 而「不完整的分母 + 完整外观的表格」正是我们要防的东西。
func TestLedgerReportPrecedesTheSummary(t *testing.T) {
	got := gatewayProbes(t)

	// summaryAnchors 是「哪一行是**读数本身**」的特征串。
	summaryAnchors := []string{"分组 CER", "整体 CER", "配对结果", "小结：", "── 汇总", "┌─", "与基准一致?",
		// ⚠ §495 实测踩到：`TestLiveNextMeetingRaw` 的末尾汇总是
		// `════ 汇总（分母 = 全部 %d 次调用）════`，而上面七项一个都不含它
		// ⇒ 判据报「找不到汇总锚点」，而**汇总就在那儿**。
		// 与 §492 的 printfAnchors、§493 的 diagSummaryAnchors 同款：
		// 这是**判据没覆盖这个形态**，不是探针没汇总。
		"════ 汇总"}
	// printfAnchors 是「用 fmt.Printf 而不是 t.Logf 输出」的探针的读数特征。
	//
	// ⚠⚠ §492 实测踩到：`TestDiagStreamZeroFrames` 是**诊断工具**，
	// 它用 `fmt.Printf("DIAG ...")` 逐臂打时间戳（给人眼在终端看），不走 -v
	// ⇒ 只扫 t.Log/t.Logf 的话它一个锚点都找不到，而「它确实在打印读数」是真的。
	// ⇒ 不能因此报错说「找不到锚点」—— 那是**判据没覆盖这个形态**。
	printfAnchors := []string{"DIAG %-28s status="}
	// diagSummaryAnchors 是「诊断型探针的**末尾汇总行**」特征。
	//
	// ⚠⚠ §493 实测踩到：`TestLiveGatewayASRMergeOverlappingChunks` 逐切点读数在循环**内**，
	// 末尾一条「汇总：去重生效 N 次 / 未生效 M 次」——
	// 它没被攒起来搬（逐切点各占一段，整体搬不动）。
	diagSummaryAnchors := []string{"汇总：去重生效"}
	// bufferedPrinters 是「把攒下来的读数**统一打印**」的循环特征。
	//
	// ⚠⚠§491：判据问的是「**读数什么时候被打印**」，而正确写法是
	//	`for _, line := range tierLines { t.Log(line) }`
	// —— 打印点在 Report **之后**，但那个循环里**一个锚点字面量都没有**
	//（字面量在循环**内**构造时就已经进了切片）。
	// ⇒ 只认字面量 ⇒ 报「找不到锚点」—— **而实际顺序是对的**。
	bufferedPrinters := []string{"range tierLines", "range tierReadings", "range diffs", "range lines"}
	allAnchors := append(append(append([]string{}, summaryAnchors...), printfAnchors...), diagSummaryAnchors...)

	for _, name := range ledgerWired {
		site, ok := got[name]
		if !ok {
			continue
		}
		reportAt := strings.Index(site.body, "ledger.Report()")
		if reportAt < 0 {
			t.Errorf("%s 没有 ledger.Report()", name)
			continue
		}
		// ⚠⚠§491：锚点必须匹配在**打印调用**里，不能裸匹配字面量。
		//　FullVsChunked 改成「先攒、对账后再打印」之后，
		//　`t.Logf("   与基准一致? %v", …)` 变成了 `fmt.Sprintf(…)`（只是 append），
		//　真正打印的是对账之后那个循环 —— 裸匹配会命中那个**字符串字面量**。
		//　⇒ 判据要问「**哪一行真的会打印**」，不是「哪个字面量出现得早」。
		sumAt := -1
		for _, at := range emitCallOffsets(site.body) {
			end := emitSpanEnd(site.body, at)
			if end < 0 {
				continue
			}
			call := site.body[at:end]
			if isExplanatory(call) {
				continue
			}
			for _, anchor := range allAnchors {
				if i := strings.Index(call, anchor); i >= 0 {
					if sumAt < 0 || at+i < sumAt {
						sumAt = at + i
					}
				}
			}
			for _, sig := range bufferedPrinters {
				if strings.Contains(call, sig) && (sumAt < 0 || at < sumAt) {
					sumAt = at
				}
			}
		}
		if sumAt < 0 {
			t.Errorf("%s 找不到任何**在打印调用里**的汇总锚点（%v）—— "+
				"要么读数被改成了攒起来后统一打印（那要对账在前），要么锚点漂了。",
				name, allAnchors)
			continue
		}
		if reportAt > sumAt {
			t.Errorf("%s 的 ledger.Report() 在汇总**之后**（第 %d 字节 vs 汇总第 %d 字节）⇒ "+
				"不完整的分母已经先印出去了", name, reportAt, sumAt)
		}

		// ⚠⚠ §492：报告了不完整还不够，**分母本身必须被打印出来**。
		//
		// 变异 D3 实测：把「DIAG 汇总：%d/%d 臂跑完」那行删掉，
		// 只留 `ledger.Report()` 的 t.Error ⇒ **全门仍绿** ——
		// 而那条汇总是这类诊断探针**唯一**告诉读者「跑了几臂」的地方。
		// ⇒ §476.1 ① 的形态是**分母不出现**，不是「不出现时进程挂掉」。
		if !printsCompletion(site.body) {
			t.Errorf("%s 接了账本却没有任何地方打印 Done()/Total() ⇒ "+
				"读者看不到「跑了几项 / 共几项」，不完整的读数与完整读数仍同形（§492）", name)
		}
	}
}

// ── 负控：判据必须抓得住「把账本拆掉」 ──────────────────────────────
//
// ⚠⚠ 没有这一条，上面全是恒真的 —— 而「恒真」与「正确」在退出码上毫无区别。
//
// ★ 本负控专治 §489 收回的那件事：**文件级 vs 函数级**。
// 　 §488 的门断文件，于是「同文件里 1 个接了账本」被当成「同文件都接了」。
// ── 判据 ⑤：豁免只给「先验理由」—— 而先验与否，账本自己判断不了 ─────
//
// ⛔ §495.6 留下的一笔账：`Skip("…")` 里那个理由是「还没跑就知道不成立」
// 还是「试了一下没成」，**只有调用点知道**。把它写在注释里没人守，
// 于是下一个 continue 分支很可能顺手也记一个 Skip
// ⇒ 「转写失败」被洗成「按设计跳过」—— 那比不记更糟。
//
// ⇒ 把它变成**受控词表**：每个 `ledger.Skip(` 的实参必须含其中一项，
// 而新增一项要显式改这张表 ⇒ 每次扩充都会有人看一眼。
//
// ⚠ 词表当前只有一个用户（OverlapTiming 的两条先验跳过）。词表小 = 门严。
var skipPriorReasons = []string{"输入不足"}

// TestSkipLabelsMustBeAPrioriReasons 把上面那张词表变成会红的门。
//
// ⚠ 判据打在**函数体**上（不是文件）：同文件里可能有别的函数也叫 Skip。
// ⚠⚠ 红因**不报行号**：site.body 是剥过注释的源码，行号与原文件已错位
//
//	（§493 踩过：剥注释后数出来的行号指向的是 `<script>` 段）。
//	报「哪个探针 + 哪个豁免理由」已经够定位，且不会骗人。
func TestSkipLabelsMustBeAPrioriReasons(t *testing.T) {
	got := gatewayProbes(t)
	checked := 0
	for _, name := range ledgerWired {
		site, ok := got[name]
		if !ok {
			continue
		}
		// ⚠ 按行切：`ledger.Skip(c.label + "（输入不足）")` 里没有 ASCII 括号，
		//   按 `)` 截断在将来有人写 `fmt.Sprintf("x(%d)")` 时会截错。
		for _, line := range strings.Split(site.body, "\n") {
			at := strings.Index(line, "ledger.Skip(")
			if at < 0 {
				continue
			}
			checked++
			label := strings.TrimSpace(line[at+len("ledger.Skip("):])
			if j := strings.Index(label, ")"); j >= 0 {
				label = strings.TrimSpace(label[:j])
			}
			prior := false
			for _, r := range skipPriorReasons {
				if strings.Contains(label, r) {
					prior = true
					break
				}
			}
			if !prior {
				t.Errorf("%s 的豁免理由 %q 不在词表 %v 里 ⇒ 这一处 Skip 可能是"+
					"「试了一下没成」而不是「按设计不成立」；确认是先验的就把它加进"+
					"skipPriorReasons，别删判据", name, label, skipPriorReasons)
			}
		}
	}
	// ⛔ 量具自证：词表一个用户都没有时，这条判据就是恒真的。
	if checked == 0 {
		t.Fatal("⛔ 全仓找不到任何 ledger.Skip( ⇒ 这条判据是恒真的" +
			"（要么 Skip 已被移除，要么本条判据打错了地方）")
	}
	t.Logf("已核对 %d 处豁免（%d 个 wired 探针），词表 %v",
		checked, len(ledgerWired), skipPriorReasons)
}

func TestWiringGateIsNotVacuous(t *testing.T) {
	// ⚠⚠ 样本里的两个探针**都必须**含网关 env 门控，否则 `gatewayGateRe`
	// 会把它们正确地排除掉 —— 那正是抽取器在工作，而负控会「因构造错误」失败。
	// 第一版就是漏了这个 env 键，负控直接报 `实际 []`。
	// ⇒ 这是「负控自身坏了」的又一处，必须记住：负控红了先看负控。
	mixed := `package server

// TestWiredOne 接了账本。
func TestWiredOne(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关")
	}
	ledger := livetest.New("x", 3)
	for _, c := range cases() {
		ledger.Complete(c.name)
	}
	if msg := ledger.Report(); msg != "" {
		t.Error(msg)
	}
	t.Logf("整体 CER = %.2f%%", 1.0)
}

// TestUnwiredOne 没接账本 —— 但它与上面**同属一个文件**。
func TestUnwiredOne(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关")
	}
	for _, c := range cases() {
		if bad(c) {
			continue
		}
		t.Logf("[%s] ok", c)
	}
	t.Logf("整体 CER = %.2f%%", 1.0)
}
`
	stripped := stripSrc(mixed)
	var names []string
	for _, loc := range testFuncRe.FindAllStringIndex(stripped, -1) {
		body, _ := funcBody(stripped, loc[1])
		if !gatewayGateRe.MatchString(body) {
			continue
		}
		names = append(names, testFuncRe.FindStringSubmatch(stripped[loc[0]:loc[1]])[1])
	}
	if len(names) != 2 {
		t.Fatalf("负控构造有误：期望认出 2 个网关门控探针，实际 %v —— "+
			"⚠ 先怀疑样本里少了 env 门控，那会让负控「因构造错误」失败", names)
	}
	wiredCount := 0
	for _, loc := range testFuncRe.FindAllStringIndex(stripped, -1) {
		body, _ := funcBody(stripped, loc[1])
		if strings.Contains(body, "livetest.New(") && strings.Contains(body, "ledger.Report()") {
			wiredCount++
		}
	}
	if wiredCount != 1 {
		t.Fatalf("负控构造有误：样本里接了账本的探针应为 1 个，实测 %d", wiredCount)
	}
	t.Log("负控通过：同文件 2 个探针只有 1 个接账本 —— 文件级判据放行，函数级判据必须红")

	// ── 反向自证：本仓真的存在这个并置形态 ──────────────────────────
	//
	// ⚠⚠ 这条不能省：若本仓已经不出现「同文件已接 + 同文件未接」，
	// 那么负控演示的是一个**本仓不存在的场景**，它会变成一句自我安慰。
	got := gatewayProbes(t)
	wiredIn := map[string]bool{}
	for _, n := range ledgerWired {
		wiredIn[n] = true
	}
	wiredFiles := map[string]bool{}
	for n, site := range got {
		if wiredIn[n] {
			wiredFiles[site.file] = true
		}
	}
	coLocated := ""
	for name, site := range got {
		if wiredFiles[site.file] && !wiredIn[name] {
			coLocated = name + "（与已接账本的探针同在 " + site.file + "）"
			break
		}
	}
	if coLocated == "" {
		t.Fatal("真实代码里找不到「同文件已接账本、同文件另有探针未接」的并置 ⇒ " +
			"负控演示的场景在本仓已不复存在，请重写负控（别让它继续自我安慰）")
	}
	t.Logf("反向自证通过：本仓确实存在该并置 —— %s", coLocated)
}

// ── 小工具 ────────────────────────────────────────────────────────────

// emitCallOffsets 给出函数体里所有「**读数被打印**」的位置。
//
// ⚠⚠§491：这里**不能只扫 `t.Log(` / `t.Logf(`**。
//
// 　「攒起来统一打印」的合法形态是
//
//	`for _, line := range tierLines { t.Log(line) }`
//
// 　而**循环头**才是位置所在 —— 只扫 `t.Log(` 会漏掉 `range tierLines`
// 　那个 sig，于是判据④报「找不到锚点」，而实际顺序是对的。
//
// ⇒ 故这里扫的是**两种位置**：直接打印调用 + 统一打印循环的循环头。
func emitCallOffsets(body string) []int {
	var out []int
	for _, name := range []string{
		"t.Logf(", "t.Log(", "fmt.Printf(", "fmt.Println(",
		"range tierLines", "range tierReadings", "range diffs", "range lines",
	} {
		from := 0
		for {
			i := strings.Index(body[from:], name)
			if i < 0 {
				break
			}
			out = append(out, from+i)
			from = from + i + len(name)
		}
	}
	sort.Ints(out)
	return out
}

// emitSpanEnd 给出某个打印位置的「这一条打印」在哪里结束。
//
// ⚠ 对 `t.Logf(`/`t.Log(`/`fmt.Printf(`/`fmt.Println(` 是括号配平；
// 对 `range xxx` 是**该 for 语句的花括号配平** ——
// 因为统一打印循环的判定信号在循环头，而它的 `t.Log(line)` 在循环体内。
func emitSpanEnd(src string, at int) int {
	head := src[at:]
	for _, p := range []string{"t.Logf(", "t.Log(", "fmt.Printf(", "fmt.Println("} {
		if strings.HasPrefix(head, p) {
			return balancedEnd(src, at+len(p)-1)
		}
	}
	brace := strings.Index(head, "{")
	if brace < 0 {
		return at + len(head)
	}
	return balancedBrace(src, at+brace)
}

// balancedEnd 从 `(` 起做括号配平，返回该调用的 `)` 之后的位置；-1 = 不配平。
//
// ⚠⚠ §501：同样跳过字符串字面量 —— `t.Logf("…（注）…", x)` 里若出现半角 `(`，
// 　配平就会跨过这次调用，报出来的「这一条打印在哪里结束」是错的。
func balancedEnd(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '"', '`', '\'':
			i = skipStringLit(src, i) - 1
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// balancedBrace 从 `{` 起做花括号配平。同上，§501 起跳过字符串字面量。
func balancedBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '"', '`', '\'':
			i = skipStringLit(src, i) - 1
			continue
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// isExplanatory 报这个打印调用是不是在**解释**别的读数（而不是读数本身）。
//
// ⚠ §491 变异 N1b 实测踩到：结尾那句
// `t.Log("✓ 零转写失败 ⇒ 上面每档的「与基准一致?」…")` 里含同一个锚点，
// 而它位于 Report 之后 ⇒ 掩盖了「读数打印被挪到对账之前」这个真违规。
//
// ⇒ 判别条件：实参里含 `⇒` 或 `↑` 的，是在说「**由上面那些行可知**」，
// 　 而不是在**产出**上面那些行。
func isExplanatory(call string) bool {
	return strings.Contains(call, "⇒") || strings.Contains(call, "↑")
}

// printsCompletion 报函数体里是否有一处打印同时用到 Done() 与 Total()。
//
// ⚠ 为什么不能用「出现 ledger.Done() 字面量」判：
// 它可能只出现在某个 `t.Logf` 的实参里被拼进别的文案，
// 而**分母的两个数没有同时出现** —— 那与没打印等价。
func printsCompletion(body string) bool {
	for _, at := range emitCallOffsets(body) {
		end := emitSpanEnd(body, at)
		if end < 0 {
			continue
		}
		call := body[at:end]
		if strings.Contains(call, "Done()") && strings.Contains(call, "Total()") {
			return true
		}
	}
	return false
}
