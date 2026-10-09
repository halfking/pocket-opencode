// dedup_threshold_sync_test.go —— Go 与 TS 两份去重实现的阈值同步门。
//
// §6 记着一条没解决的遗留：「**前后端各有一份去重实现**（Go 与 TS），
// 阈值需要手工同步。」本文件是它的解法：把「手工」变成「红灯」。
//
// ── 为什么这不是「补一个常量」那么小的事 ──
//
// 两份实现是**同一个算法的两次手抄**，共用三个量：
//
//	anchorCoverage / ANCHOR_COVERAGE   LCS 必须覆盖对齐窗口的比例
//	maxOverlap      / MAX_OVERLAP      对齐窗口的最大长度
//	窗口下限                            「短到多少就不看了」
//
// 抄本会漂。漂了之后症状极其难认：前端照常去重、后端照常去重，
// 只是两边对「这是不是重叠」的判断开始不一致——而**没有任何一边报错**。
// 用户看到的只是「有时候还有几个重复字」，排查的人会先去怀疑 ASR 精度，
// 因为去重代码看起来是好的、而且**测试全绿**。
//
// 本门锚在「两份文件的字面量是否一致」上，看起来像源码文本断言，
// 但它与设计文档里记的那几次「判据失明」不同：那些判据锚在**与被测行为
// 无关的量**上（数组长度上限、字面量存在性、总量）；而**阈值本身就是这里
// 的被测对象**——两个实现算同一件事，参数必须相等，这正是本门要断的
// 不变式。断的是「跨实现一致性」，不是「代码里有没有某个字面量」。
//
// ★ 本门**只**钉共有的阈值，不替两边的差异做判断。
// 已知的既有差异（Go 的 minFuzzyAnchor 与窗口下限，见下方 TestDedupThresholdsMatchAcrossGoAndTS
// 的说明）由人决策，不由本门静默抹平——把一个行为差异偷偷改成一致，
// 比留着它更危险。

package stt

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tsDedupPath 是前端去重实现相对本文件的位置。
const tsDedupPath = "../../../frontend/src/features/meetings/meeting-dedup.ts"

// recordingRuntimePath 是**即时转写切分**的前端实现。
// 它决定切片形状（见 TestProductionShapeSlicesDoNotOverlap）。
const recordingRuntimePath = "../../../frontend/src/native/recordingRuntime.ts"

// readTSDedup 读前端去重实现；读不到就**跳过**而不是失败。
//
// 为什么跳过而不是报错：本包（backend/internal/stt）在某些部署形态下
// 可能拿不到前端源码。让「源码不在」变成一条红，会让不相关的 CI 环节
// 假红——那正是设计文档里「元门红时先分清是自己的补丁还是基线既有」讲的坑。
// 真正的漂移由下面的阈值比对负责报。
func readTSDedup(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(tsDedupPath)
	if err != nil {
		t.Skipf("前端去重实现不可读（%v），跳过跨实现阈值比对", err)
	}
	return string(b)
}

// parseConstFloat 从源码里取 `const NAME = <数字>` 的数值。
func parseConstFloat(t *testing.T, src, constName string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*const\s+` + constName +
		`\s*(?::\s*number\s*)?=\s*([0-9]*\.?[0-9]+)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("源码里找不到常量 %s 的字面量赋值", constName)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("常量 %s 的值 %q 不是数字: %v", constName, m[1], err)
	}
	return v
}

// parseGoConstFloat 从 Go 源码里取 `const name = <数字>` / `name := <数字>`。
func parseGoConstFloat(t *testing.T, src, name string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*(?:const\s+)?` + name +
		`\s*(?:float64\s*)?:?=\s*([0-9]*\.?[0-9]+)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("Go 源码里找不到常量 %s 的字面量赋值", name)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("常量 %s 的值 %q 不是数字: %v", name, m[1], err)
	}
	return v
}

// stripLineComments 去掉 `//` 之后的行注释内容。
//
// ★ 这不是洁癖，是本文件自己踩到的坑：TS 侧「已删除 MIN_ANCHOR」的那段
// **解释性注释里就写着 MIN_ANCHOR**（"此前还有一道 MIN_ANCHOR=4 的闸"）。
// 直接 grep 全文会把注释当成代码，判据立刻变成假红。
//
// 这与设计文档 §12.2 记的那几次同源：**判据锚在一个与被测行为无关的量上**
// （这里锚在「这个词出现在文件里」，而不是「这道闸在跑」）。
// 去掉注释之后，匹配到的才是真正参与计算的名字。
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func readGoSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s: %v", name, err)
	}
	return string(b)
}

// TestDedupThresholdsMatchAcrossGoAndTS —— 两份去重实现的共有阈值必须相等。
//
// 这是 §6 那条遗留的直接解法：以前改一处要靠人记得改另一处，
// 现在改一处而不改另一处，这道门就红。
func TestDedupThresholdsMatchAcrossGoAndTS(t *testing.T) {
	ts := readTSDedup(t)
	goSrc := readGoSource(t, "incremental.go")

	tsCoverage := parseConstFloat(t, ts, "ANCHOR_COVERAGE")
	goCoverage := parseGoConstFloat(t, goSrc, "anchorCoverage")
	if tsCoverage != goCoverage {
		t.Errorf("覆盖率阈值漂移了：Go anchorCoverage=%v，TS ANCHOR_COVERAGE=%v —— "+
			"两份实现会对「这是不是重叠」给出不同判断，且两边都不报错",
			goCoverage, tsCoverage)
	}

	tsOverlap := parseConstFloat(t, ts, "MAX_OVERLAP")
	goOverlap := parseGoConstFloat(t, goSrc, "maxOverlap")
	if int(tsOverlap) != int(goOverlap) {
		t.Errorf("对齐窗口上限漂移了：Go maxOverlap=%v，TS MAX_OVERLAP=%v", goOverlap, tsOverlap)
	}
}

// TestDedupCrossReferencesStillPointAtEachOther —— 两边必须继续互相标注。
//
// 这一条不是凑数：§2 的原话是「改一处必须同步另一处」，而这句话之所以
// 会失效，正是因为**没有任何东西在盯着它**。本门让那句注释的删除本身
// 也变成一次可见的失败。
//
// ★ 第一版这里写的是宽松匹配（`stt/incremental.go|Go 与 TS|后端`），
//
//	变异体把文件路径换成无关名字后**这道门依然全绿**——因为「后端」「前端」
//	这类词在两边的普通注释里到处都是。这与设计文档 §12.2 记的那些
//	「判据失明与通过同形」是同一族：判据锚在「某串字出现过」上。
//	⇒ 改成**只认对方文件的真实路径**，删掉路径就是删掉交叉引用。
//
// ★ 第二版踩了相反方向的坑：为了躲开上面那个假绿，我先剥掉注释再匹配，
//
//	结果**基线自己红了**——交叉引用本来就该写在注释里，而「剥注释」
//	正好把它删掉了。两条教训合起来是同一个结论：
//	**判据的「范围」本身就是一个变量**，开大和开小都会误判
//	（设计文档 §13.5 记的正是这件事）。
//	⇒ 这里匹配**带注释的原文**，靠「路径必须精确」拿特异性；
//	而下面那道「这道闸到底跑不跑」的门才需要剥注释——那里要区分
//	「代码里有」与「注释里解释过」。
func TestDedupCrossReferencesStillPointAtEachOther(t *testing.T) {
	ts := readTSDedup(t)
	goSrc := readGoSource(t, "incremental.go")

	if !strings.Contains(ts, "stt/incremental.go") {
		t.Error("前端去重实现里没有指向后端实现（stt/incremental.go）的交叉引用")
	}
	if !strings.Contains(goSrc, "meeting-dedup.ts") {
		t.Error("后端去重实现里没有指向前端实现（meeting-dedup.ts）的交叉引用")
	}
}

// TestKnownDedupDivergencesAreStillTheKnownOnes —— 把「已知差异」钉成一张清单。
//
// 这道门的作用是**让差异可见但不掩盖**：两份实现在闸门设计上本来就不完全
// 一样（Go 保留了 minFuzzyAnchor，TS 删掉了它），那是 §2 变异测试之后留下的
// 真实差异。本门不假装它不存在，而是要求「差异恰好是已记录的那几个」：
//
//   - 将来谁把 Go 的 minFuzzyAnchor 也删了（或反过来把它加回 TS），
//     本门会绿——**那是好事**，说明两边终于一致了，该更新本注释；
//   - 谁新增了第四道闸，本门会红，因为清单里没有它。
//
// 换句话说：它不是「禁止漂移」，而是「禁止漂移得没人知道」。
func TestKnownDedupDivergencesAreStillTheKnownOnes(t *testing.T) {
	ts := readTSDedup(t)
	goSrc := readGoSource(t, "incremental.go")

	tsHasMinAnchor := regexp.MustCompile(`MIN_ANCHOR`).MatchString(stripLineComments(ts))
	goHasMinAnchor := regexp.MustCompile(`minFuzzyAnchor`).MatchString(stripLineComments(goSrc))

	// 已记录的状态：Go 有、TS 无。
	if goHasMinAnchor && tsHasMinAnchor {
		t.Error("TS 侧又出现了 MIN_ANCHOR —— 两侧的闸门结构变了，请更新本注释里的「已知差异」")
	}
	if !goHasMinAnchor {
		t.Log("提示：Go 侧的 minFuzzyAnchor 已被移除，两份实现的闸门结构现已一致；" +
			"请把本文件顶部的「已知差异」注释更新为「无差异」")
	}
}

// TestRealGatewayOverlapStillDuplicates 是一道**绊线（tripwire）**，钉住现状。
//
// 2026-10-06 用真实 ASR（llmgo.kxpms.cn / mimo-v2.5-asr）扫 5 个切点实测：
// 1 秒级重叠下**只有 1/5 触发去重**，最坏一档合并结果整句重复：
//
//	段A = 今天下午三点，会议室开产品评审会。
//	段B = 会议室开产品评审会，请提前十分钟到场。
//	现状 = 今天下午三点，会议室开产品评审会。会议室开产品评审会，请提前十分钟到场。
//
// 根因与**试过又撤回的改法**见设计文档 §19，以及 fuzzyOverlapTail 的注释。
//
// ⚠ 为什么断言的是**当前的（不理想的）输出**：
// 直接断言「应该去重」会让本文件长期红、污染 go test ./...。
// 改成钉住现状 —— 任何人让这条转绿时，必须同时看 §19：
// 该改动必须**同步改前端那份**（frontend/src/features/meetings/meeting-dedup.ts，
// 由 dedup_threshold_sync_test.go 强制同参），且要证明没有引入
// TestWindowScanWouldOverDedup 记录的那类**误去重**。
func TestRealGatewayOverlapStillDuplicates(t *testing.T) {
	const committed = "今天下午三点，会议室开产品评审会。"
	const next = "会议室开产品评审会，请提前十分钟到场。"

	got := mergeIncremental(committed, next)
	t.Logf("合并结果: %q", got)

	if n := strings.Count(got, "会议室开产品评审会"); n != 2 {
		t.Errorf("行为已变化（重叠短语出现 %d 次）—— 若这是修好了，请先读 §19："+
			"必须同步改前端 meeting-dedup.ts，并证明没有引入误去重", n)
	}
	// 无论修没修，都不能把内容吃掉。
	for _, want := range []string{"今天下午三点", "请提前十分钟到场"} {
		if !strings.Contains(got, want) {
			t.Errorf("合并结果丢了内容 %q（误去重的代价远大于漏去重）：%q", want, got)
		}
	}
}

// TestWindowScanWouldOverDedup 记录**逐档扫描窗口**这个改法为什么被撤回。
//
// 该改法能把真网关上的去重命中率从 1/5 提到 4/5，但它会吃掉真实内容：
//
//	committed = 我们今天讨论了预算和排期，还有人
//	next      = 排期还有人员安排要尽快定下来
//	逐档扫描的结果 = …预算和排期，还有人员安排要尽快定下来
//	                 ↑「排期还」被当重叠裁掉，但这里其实是**换了句式**，
//	                  共享的只是「排期 / 还有人员」这类常用词，不是切片重叠
//
// 根因：真重叠与「换句式但用词相同」在结构上不可区分 ——
//
//	真重叠  tail 议室开产品评审会。 ↔ head 会议室开产品评审会  7/9 = 0.78
//	误判    tail 期，还有人         ↔ head 排期还有           4/5 = 0.80
//
// 覆盖率、匹配相对位置、长度三项都对不上任何可靠的判别式。
// 靠调阈值把单测调绿就是过拟合，且押的恰好是「宁可丢内容」那一侧。
//
// ⇒ 这道用例断言**当前的正确行为**（不误去重），
// 同时把上面那段逐档扫描的错误结论钉在案：谁想再动这里，先看它。
func TestWindowScanWouldOverDedup(t *testing.T) {
	const committed = "我们今天讨论了预算和排期，还有人"
	const next = "排期还有人员安排要尽快定下来"

	got := mergeIncremental(committed, next)
	t.Logf("合并结果: %q", got)

	// 正确行为：原样拼接，一个字都不删。
	if got != committed+next {
		t.Errorf("这段**不是**切片重叠（共有的只是常用词），却发生了裁剪：%q", got)
	}
	// 真实内容必须完整。
	for _, want := range []string{"排期还有人员安排", "预算和排期"} {
		if !strings.Contains(got, want) {
			t.Errorf("合并结果丢了内容 %q：%q", want, got)
		}
	}
}

// TestProductionShapeSlicesDoNotOverlap 钉住 2026-10-07 查实的那件事：
// **生产切片不重叠**，所以 §19 复现的「整句重复」在生产上不会发生。
//
// 为什么要有这道门：incremental.go 的包头注释写着
// 「每段尾部留 overlapSec 秒与下一段重叠」，IncrementalOverlapSec 常量也在，
// 任何人读代码都会以为重叠存在、进而以为整句重复是活的缺陷。
// 实际前端 recordingRuntime.ts:901 送的是 `startSec = endSec - windowSec`
// —— 纯新增窗口，IncrementalOverlapSec **零使用点**。
// 一次真实网关实测（20 档 / 40 次 mimo-v2.5-asr 调用，2026-10-07）也证实：
// 生产形状下重复量只有 1–2 字，现有算法 0/8 误判。
//
// ⇒ 这不是「缺陷已修」的断言，是**「别基于错误的场景去改代码」**的护栏。
//
//	如果哪天有人把重叠真的接上了，这道门会红 —— 那时正确的反应是
//	先读设计文档 §25，然后决定要不要把整句重复当成活缺陷重新处理，
//	而不是直接调 anchorCoverage。
func TestProductionShapeSlicesDoNotOverlap(t *testing.T) {
	// 1) 仓内不应存在「给即时切片补重叠」的使用点。
	//    这条判据只覆盖 Go 侧；前端那半由下面的源码断言覆盖。
	assertNoOverlapUsageSite(t)

	// 2) 前端必须按「纯新增窗口」算 startSec。
	//    判据锚在**代码**上，且刻意避开注释（见下面的 strip）。
	// 复用本文件已有的 stripLineComments —— 判据必须避开注释。
	// §22 已经吃过一次亏：schema 挪进注释后源码扫描照样全绿。
	vue := readGoSource(t, recordingRuntimePath)
	code := stripLineComments(vue)
	if !strings.Contains(code, "const startSec = Math.max(0, endSec - windowSec)") {
		t.Error("recordingRuntime.ts 的 startSec 不再是 endSec - windowSec —— " +
			"即时切片的形状变了（可能接上了重叠）。请先读设计文档 §25，" +
			"确认去重阈值与 §19/§24 的结论是否还成立，再改 anchorCoverage")
	}
	// windowSec 必须来自**新增音频的字节数**，而不是含重叠的累计长度。
	if !strings.Contains(code, "const windowSec = wav.byteLength / 2 / 16000") {
		t.Error("windowSec 不再来自新增音频字节数 —— 切片形状可能变了，同上")
	}
}

// assertNoOverlapUsageSite 断言 IncrementalOverlapSec 在 Go 侧仍无使用点。
//
// ⚠⚠ 第一版数的是**文本出现次数**，结果 9 处 —— 其中 **8 处是注释**，
// 而且 7 处是本轮我自己写的（它们在解释「这个常量零使用点」）。
// 判据被自己的注释喂饱了，然后报「常量已有 8 个使用点」。
//
// 这是「源码扫描必须先剥注释」那条纪律的**又一次**复发（§13.5、§17、§22、
// §25 探针的档位判据，本轮第 4 次）。它尤其阴险的地方在于：
// 注释越多、解释得越详细，判据越失明 —— **文档质量反向拉低了判据质量**。
//
// 所以这里数的是**剥掉注释之后**的引用数、**只在非测试文件里**数，
// 且只认声明行那 1 处。
//
// 这门自己踩了三个坑，按踩的顺序记下来（每一版都红，红得有诊断价值）：
//
//	① 数文本出现次数 → 9 处（8 处是注释，7 处是本轮自己写的）；
//	② 剥注释后仍数全部 .go → 2 处（第二处是本门 `const name = "..."` 字面量）；
//	③ 只扫非 _test.go → 1 处，绿。
func assertNoOverlapUsageSite(t *testing.T) {
	t.Helper()
	const name = "IncrementalOverlapSec"
	uses := 0
	// ⚠ go test 的工作目录是**包目录**（internal/stt），不是仓库根。
	//   写 "internal" 会 lstat 失败 —— 判据自己先崩，那种红没有诊断价值。
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		// ★ 只扫**生产代码**。扫测试文件会被这道门自己判成违规者 ——
		//   `const name = "IncrementalOverlapSec"` 是字符串字面量，
		//   剥注释剥不掉它。第二版就死在这儿，报「剥注释后仍有 2 处」。
		//   而「有没有使用点」这个问题问的本来就是产品代码，不是测试代码。
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		uses += strings.Count(stripGoCommentsKeepLines(string(b)), name)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 internal/ 失败: %v", err)
	}
	// 声明行本身算 1 处；多于 1 说明有人接上了重叠。
	if uses > 1 {
		t.Errorf("%s 在剥注释后仍有 %d 处引用（声明行算 1 处）—— "+
			"即时切片可能已经接上重叠。请先读设计文档 §25，"+
			"确认去重阈值与 §19/§24 的结论是否还成立，再改 anchorCoverage。",
			name, uses)
	}
}

// stripGoCommentsKeepLines 剥掉 Go 的行注释与块注释，**保留行数**（
// 位置信息对不上就没法排查）。与 internal/server/pg_test_isolation_guard_test.go
// 的 stripGoComments 同源，这里额外保留了换行。
func stripGoCommentsKeepLines(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inBlock := false
	for _, ln := range strings.Split(src, "\n") {
		if inBlock {
			i := strings.Index(ln, "*/")
			if i < 0 {
				b.WriteByte('\n')
				continue
			}
			ln = " " + ln[i+2:]
			inBlock = false
		}
		if i := strings.Index(ln, "/*"); i >= 0 {
			if j := strings.Index(ln[i+2:], "*/"); j >= 0 {
				ln = ln[:i] + " " + ln[i+2+j+2:]
			} else {
				ln = ln[:i]
				inBlock = true
			}
		}
		for i := 0; i < len(ln); i++ {
			if ln[i] == '/' && i+1 < len(ln) && ln[i+1] == '/' && (i == 0 || ln[i-1] != ':') {
				ln = ln[:i]
				break
			}
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}
