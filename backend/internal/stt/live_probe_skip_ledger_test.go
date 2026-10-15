// live_probe_skip_ledger_test.go —— §467
//
// 让「被跳过的测试」**可见**，而不是被 `go test` 的 `ok` 吞掉。
//
// ── 为什么需要这道门 ────────────────────────────────────────────────
//
// 2026-10-11 实测（照 CI 口径 `go test ./... -race -count=1`）读数是
// 「**57 个包全过、0 FAIL**」。这句话**本身没错**，但它把 **SKIP 当成了 PASS**：
//
//	internal/stt   114 PASS / 9 SKIP     ← go test 仍然报 ok
//
// 那 9 条里有 **8 条是真网关探针**（CER、切块合并、重叠计时、候选模型…），
// 它们要 `POCKET_LIVE_GATEWAY=1` 才跑，而本机没有配 ⇒ **一次都没执行过**。
//
// ⚠ 这是 §453（`backend/security` 的 13 条测试**从未编译过**）的**第二个同族实例**：
//
//	第一次是**不编译**，这一次是**编译了但不执行**，而两者的共同点是
//	—— **绿色的读数完全掩盖了「这一段压根没跑」**。
//
// ⇒ 本门不试图让探针跑起来（那要凭据，见台账里的解锁条件），
//
//	它只保证一件事：**跳过是登记在册的，且不多不少。**
//
// ── 三条纪律（每条都来自被它们抓到过的事）──────────────────────────
//
// ① **台账必须双向精确匹配**：漏一条（新探针静默跳过）要红，
//
//	多一条（测试改名后条目变陈旧）也要红 ——
//	台账一旦可以只增不减，它就会烂成一张**永久通行证**。
//
// ② **必须剥注释**：`t.Skip` 出现在注释里时不算数（§464 同族）。
// ③ **量具先自证**：先断言「确实扫到了 ≥8 个可跳过的探针」，
//
//	否则「台账是空的」和「抽取器坏了」读数一样。
package stt

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── 台账 ──────────────────────────────────────────────────────────────
//
// unlock 是「让它跑起来需要什么」。⚠ 这里写的是**实测的 env 键名**，
// 不是泛泛的「配置一下」—— §464 的教训：范围与条件要写清，
// 否则下一个会话会把「跳过」重新登记成「没查」。
// ── 这道门的能力边界（必须如实说，不得夸大）─────────────────────────
//
// ⚠ 本门是**静态**检查：它看的是源码里「某个 Test 函数体内有没有 `t.Skip(`」，
//
//	**不是**「这次运行它到底跳没跳」。
//
//	外部变异②实测过了：把某探针的守卫从 `!= "1"` 改成 `== "1"`，
//	该测试在本机就从 SKIP 变成 **FAIL**（守卫失效 ⇒ 真去跑了 ⇒ 缺 env 而失败），
//	而本门**仍然绿** —— 因为 `t.Skip(` 那行还在源码里。
//
// ⇒ 这不是缺陷，是**有意的取舍**：静态判据是确定性的、与本机有没有凭据无关；
//
//	运行时判据在有网关的机器上会自动变绿，等于把台账交给环境决定。
//	⚠ 代价必须记住：**「有 t.Skip 字样」不等于「这次真的跳了」**。
//	要看真实跳过数请跑：`go test ./internal/stt/ -v | grep -c '^--- SKIP'`。
//
// skipLedgerEntry 结构体字段定义在其上方（knownSkips 变量表之前）。
type skipLedgerEntry struct {
	test   string
	reason string
	unlock []string
}

// knownSkips 是**全部**允许被跳过的测试。不在表内的一律红。
var knownSkips = []skipLedgerEntry{
	{"TestLiveGatewayASR", "真网关 ASR 基础探针",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO（仓内无音频文件，需自备 16k WAV）", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayASRCer", "真网关 CER 实测（用户最关心的「转写准不准」）",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayASRMergeOverlappingChunks", "切片重叠去重的真网关实测",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayFullVsChunked", "整段转写 vs 切块转写的真网关对比",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayOverlapTimingVsText", "重叠时序 vs 重叠文本（语速锚点）",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayOneASRModel", "指定单一 ASR 模型的定点验证",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LIVE_ASR_MODEL"}},
	{"TestLiveGatewayASRCandidateSupply", "网关实际供应了哪些 ASR 模型",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY"}},
	{"TestLiveASRCandidateCoverage", "目标清单里的候选是否真被网关覆盖",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY"}},
	{"TestLiveGatewayProdSegment", "生产整段切分形态 vs 整段单发（含跨段合并对照）",
		[]string{"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
			"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"}},
	{"TestDumpSplitSegments", "切分结果的人工取证转储（开发工具，非断言）",
		[]string{"POCKET_DIAG_DATA_DIR 或显式运行（属转储工具而非门）"}},
}

// stripGoComments 去掉 // 与 /* */ 注释，**保留字符串字面量**
// （与前端那两道门同样的教训：我要找的东西可能就住在字符串里）。
func stripGoComments(src string) string {
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
		case c == '"':
			b.WriteByte(c)
			i++
			for i < len(src) {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteString(src[i : i+2])
					i += 2
					continue
				}
				b.WriteByte(src[i])
				if src[i] == '"' {
					i++
					break
				}
				i++
			}
		case c == '`':
			b.WriteByte(c)
			i++
			for i < len(src) {
				b.WriteByte(src[i])
				if src[i] == '`' {
					i++
					break
				}
				i++
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

var testFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]*)\s*\(\s*t\s+\*testing\.T\s*\)`)

// skippableTests 返回本包里「函数体内出现 t.Skip(」的测试名。
func skippableTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		// ⚠ 豁免**只有本文件一个**，且是必需的：
		//   本文件的负控样本是一个**反引号原始字符串**，里面写着
		//   `func TestReal(t *testing.T) { t.Skip("…") }`。
		//   而 stripGoComments **保留字符串字面量**（前端两道门踩出来的教训），
		//   ⇒ 样本里的 t.Skip 会被算成真跳过，门会把自己报成「未登记」。
		//
		//   ⚠ 这是同一个家族的第 N 次：2026-10-11 这一轮我已经在 §463/§464/§466
		//   踩了三次「掩码抹掉了我要找的东西」，这次是**反面**——
		//   「掩码没抹掉我不想它看见的东西」。⇒ 两个方向都要考虑。
		if f == "live_probe_skip_ledger_test.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		code := stripGoComments(string(raw))
		for _, loc := range testFuncRe.FindAllStringIndex(code, -1) {
			name := testFuncRe.FindStringSubmatch(code[loc[0]:loc[1]])[1]
			// 从 `{` 起做花括号配平，取函数体
			open := strings.Index(code[loc[1]:], "{")
			if open < 0 {
				continue
			}
			open += loc[1]
			depth, end := 0, -1
			for i := open; i < len(code); i++ {
				if code[i] == '{' {
					depth++
				} else if code[i] == '}' {
					depth--
					if depth == 0 {
						end = i
						break
					}
				}
			}
			if end < 0 {
				continue
			}
			if strings.Contains(code[open:end], "t.Skip(") {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

func TestSkipLedgerIsCompleteAndNotStale(t *testing.T) {
	got := skippableTests(t)

	// 纪律③：量具先自证
	if len(got) < 8 {
		t.Fatalf("量具失效：只扫到 %d 个可跳过的测试（%v）。\n"+
			"  ⚠ 这**不是**「现在没有东西被跳过」—— 先怀疑抽取器，再怀疑代码。", len(got), got)
	}

	ledger := map[string]skipLedgerEntry{}
	for _, e := range knownSkips {
		ledger[e.test] = e
	}

	actual := map[string]bool{}
	for _, n := range got {
		actual[n] = true
	}

	// 方向①：有东西在跳过，却不在台账里
	for _, n := range got {
		if _, ok := ledger[n]; !ok {
			t.Errorf("跳过但未登记：%s\n"+
				"  ⇒ 它需要什么才能跑？请写进 knownSkips（含 unlock），否则「全绿」会一直掩盖它。", n)
		}
	}
	// 方向②：台账里有，可代码里已经不跳了（陈旧条目）
	for name, e := range ledger {
		if !actual[name] {
			t.Errorf("台账陈旧：%s 已经在台账里，但源码里找不到它的 t.Skip。\n"+
				"  ⇒ 要么测试被删/改名，要么它不再跳过 —— 两种都要更新台账。\n"+
				"  ⚠ 台账只增不减就会烂成一张永久通行证。", name)
		}
		if strings.TrimSpace(e.reason) == "" || len(e.unlock) == 0 {
			t.Errorf("台账条目 %s 缺 reason 或 unlock —— 空话解锁不了任何东西", name)
		}
	}
}

// 负控：注释里的 t.Skip 不算。
func TestSkipLedgerExtractorIgnoresComments(t *testing.T) {
	sample := `package stt
// func TestInComment(t *testing.T) { t.Skip("注释里的不算") }
/* func TestInBlock(t *testing.T) { t.Skip("块注释里的也不算") } */
func TestReal(t *testing.T) { t.Skip("这个才算") }
`
	code := stripGoComments(sample)
	loc := testFuncRe.FindStringIndex(code)
	if loc == nil {
		t.Fatalf("负控样本没被解析器认出来 —— 量具失效")
	}
	if strings.Contains(code[:loc[0]], "TestInComment") {
		t.Fatalf("注释里的测试名没被剥掉")
	}
	if strings.Count(code, "t.Skip(") != 1 {
		t.Fatalf("期望只剩 1 个 t.Skip(，实测 %d 个 ⇒ 注释没剥干净", strings.Count(code, "t.Skip("))
	}
}

// 豁免自证：豁免必须**只有本文件一个，且确实必需**。
// 否则「本文件可跳过」会在某次重构后变成一条悄悄的永久豁免
// —— 台账一旦能被悄悄绕过，它就不再是台账。
func TestSkipLedgerExemptionIsExactlyThisFileAndStillNeeded(t *testing.T) {
	files, _ := filepath.Glob("*_test.go")
	exempt := 0
	selfHasSkipInSample := false
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		if f == "live_probe_skip_ledger_test.go" {
			exempt++
			// 判据自己**必须**仍含有会被误读的样本，否则豁免已无必要。
			selfHasSkipInSample = strings.Contains(string(raw), "t.Skip(") &&
				strings.Contains(string(raw), "`package stt")
			continue
		}
		if !strings.Contains(string(raw), "t.Skip(") {
			continue
		}
		// 其它含 t.Skip 的文件必须都在台账里（重复一遍主判据，确保豁免没有外溢）
		code := stripGoComments(string(raw))
		for _, loc := range testFuncRe.FindAllStringIndex(code, -1) {
			name := testFuncRe.FindStringSubmatch(code[loc[0]:loc[1]])[1]
			open := strings.Index(code[loc[1]:], "{")
			if open < 0 {
				continue
			}
			open += loc[1]
			depth, end := 0, -1
			for i := open; i < len(code); i++ {
				if code[i] == '{' {
					depth++
				} else if code[i] == '}' {
					depth--
					if depth == 0 {
						end = i
						break
					}
				}
			}
			if end > 0 && strings.Contains(code[open:end], "t.Skip(") {
				if _, ok := ledgerIndex()[name]; !ok {
					t.Errorf("非豁免文件里的 %s 在跳过但没登记（豁免外溢）", name)
				}
			}
		}
	}
	if exempt != 1 {
		t.Fatalf("豁免数应为 1，实测 %d", exempt)
	}
	if !selfHasSkipInSample {
		t.Fatal("本文件的负控样本已不复存在 ⇒ 豁免不再必需，请把这一处删掉（否则豁免会烂成通行证）")
	}
}

func ledgerIndex() map[string]skipLedgerEntry {
	m := map[string]skipLedgerEntry{}
	for _, e := range knownSkips {
		m[e.test] = e
	}
	return m
}
