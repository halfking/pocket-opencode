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
//
// ③ **量具先自证**：先断言「确实扫到了 ≥N 个可跳过的探针」，
//
//	否则「台账是空的」和「抽取器坏了」读数一样。
//
// ── 2026-10-17：这道门自己曾经**绿着漏掉一条** ─────────────────────
//
// §775 的普查轮把抽取器收编进 `internal/skipaudit` 时，实测发现：
//
//	台账登记 10 条，源码里真实在跳过的有 **11 个**。
//	多出来的那条是 `TestLiveASRModelFallsBackToGlobalDefault` —— 它用的是
//	**`t.Skipf(`**，而 §467 的抽取器只匹配字面量 `t.Skip(`。
//
// ⇒ **「双向精确匹配」在一个少认了一种写法的量具上双向精确。**
//
//	门是绿的，而它漏着。这正是本轮审计的主病：
//
//	**失效的东西因为一直在输出而没人怀疑。**
//
// 修法（连同 §467 抽取器的另外两个幽灵点一起）：
//
// ① 抽取器换成 `go/parser`（`internal/skipaudit`）。AST 里没有注释，
//
//	字符串字面量也不是 CallExpr ⇒ 纪律②免费成立，
//	§467 那版「剥注释后按正则找 t.Skip(」的三个幽灵点一起消失。
//
// ② **豁免整体取消。** §467 必须豁免本文件自己（负控样本里有原始字符串
//
//	写着 `func TestReal(t *testing.T) { t.Skip(...) }`）。改用 AST 后
//	那份样本不再是调用 ⇒ 本文件贡献 **0** 个跳过点。
//
//	TestSkipLedger_本文件不再需要豁免 把这件事钉死：
//	哪天有人在本文件里写进一个真的 t.Skip(，它会立刻报红并要求你
//	把豁免与自证**一起**加回来 —— 而不是像以前那样，豁免可以悄悄扩大。
package stt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// ── 台账 ──────────────────────────────────────────────────────────────
//
// unlock 是「让它跑起来需要什么」。⚠ 这里写的是**实测的 env 键名**，
// 不是泛泛的「配置一下」—— §464 的教训：范围与条件要写清，
// 否则下一个会话会把「跳过」重新登记成「没查」。
// ── 这道门的能力边界（必须如实说，不得夸大）─────────────────────────
//
// ⚠ 本门是**静态**检查：它看的是源码里「某个 Test 函数体内有没有跳过调用」，
//
//	**不是**「这次运行它到底跳没跳」。
//	外部变异②实测过了：把某探针的守卫从 `!= "1"` 改成 `== "1"`，
//	该测试在本机就从 SKIP 变成 **FAIL**（守卫失效 ⇒ 真去跑了 ⇒ 缺 env 而失败），
//	而本门**仍然绿** —— 因为那个跳过调用还在源码里。
//
// ⇒ 这不是缺陷，是**有意的取舍**：静态判据是确定性的、与本机有没有凭据无关；
//
//	运行时判据在有网关的机器上会自动变绿，等于把台账交给环境决定。
//	⚠ 代价必须记住：**「源码里有跳过调用」不等于「这次真的跳了」**。
//	要看真实跳过数请跑：`go test ./internal/stt/ -v | grep -c '^--- SKIP'`。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("TestLiveGatewayASR", skipaudit.CatCredential, "真网关 ASR 基础探针", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO（仓内无音频文件，需自备 16k WAV）", "POCKET_LIVE_ASR_MODEL"),
	skipaudit.EP("TestLiveGatewayASRCer", skipaudit.CatCredential, "真网关 CER 实测（用户最关心的「转写准不准」）", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"),
	skipaudit.EP("TestLiveGatewayASRMergeOverlappingChunks", skipaudit.CatCredential, "切片重叠去重的真网关实测", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"),
	skipaudit.EP("TestLiveGatewayFullVsChunked", skipaudit.CatCredential, "整段转写 vs 切块转写的真网关对比", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"),
	skipaudit.EP("TestLiveGatewayOverlapTimingVsText", skipaudit.CatCredential, "重叠时序 vs 重叠文本（语速锚点）", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"),
	// ⚠ 这一条有 **2 个**跳过调用（不是 1 个），Points 必须照实写 2。
	//
	//	Points 刻意**手写**而不是 init() 里自动补：自动补会让
	//	「台账点数 == 实际点数」这条交叉校验恒真 —— 那就白设了。
	//	（§467 的手写扫描器时代没有这条校验；加它当场就抓到了本条。）
	{
		Owner:  "TestLiveGatewayOneASRModel",
		Cat:    skipaudit.CatCredential,
		Reason: "指定单一 ASR 模型的定点验证",
		Unlock: []string{"POCKET_LIVE_GATEWAY=1", "POCKET_LIVE_ASR_MODEL"},
		Points: 2,
	},
	skipaudit.EP("TestLiveGatewayASRCandidateSupply", skipaudit.CatCredential, "网关实际供应了哪些 ASR 模型", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY"),
	skipaudit.EP("TestLiveASRCandidateCoverage", skipaudit.CatCredential, "目标清单里的候选是否真被网关覆盖", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY"),
	skipaudit.EP("TestLiveGatewayProdSegment", skipaudit.CatCredential, "生产整段切分形态 vs 整段单发（含跨段合并对照）", 1,
		"POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL", "POCKET_LLM_GATEWAY_API_KEY",
		"POCKET_LIVE_ASR_AUDIO", "POCKET_LIVE_ASR_MODEL"),
	skipaudit.EP("TestDumpSplitSegments", skipaudit.CatDiagProbe, "切分结果的人工取证转储（开发工具，非断言）", 1,
		"POCKET_DIAG_DATA_DIR 或显式运行（属转储工具而非门）"),
	// ↓ 2026-10-17 补登：§467 漏掉的第 11 条（用的是 t.Skipf）
	skipaudit.EP("TestLiveASRModelFallsBackToGlobalDefault", skipaudit.CatDefensiveGuard,
		"断言「线上真正在用的 mimo-v2.5-asr 未登记 MaxSeconds ⇒ 回落全局默认 25s」。"+
			"一旦该模型被登记 MaxSeconds，本用例的前提就不成立 ⇒ 跳过并要求更新 §481.5 的结论。"+
			"⚠ 正因为它是 CatPremiseFailed，它**永远不会失败**："+
			"模型被登记的那天，这里静默跳过，而绿色读数与「跑过并通过」无法区分。", 1,
		"无需 env；解锁方式是给本用例换一个仍满足「未登记 MaxSeconds」的模型，"+
			"或当 mimo-v2.5-asr 真的被登记后重写判据（此时它应转为一条真正的断言）"),

	skipaudit.EP("liveAudio", skipaudit.CatCredential,
		"真音频探针的夹具：没有真实语音 wav 就跳过（3 个测试受影响）。", 1,
		"POCKET_STT_LIVE_AUDIO=<真实语音 wav 的路径>"),
	skipaudit.EP("liveKey", skipaudit.CatCredential,
		"真 MiniMax 探针的夹具：没有 key 就跳过（2 个测试受影响）。⚠ 跑它会消耗真实额度。", 1,
		"STT_LIVE_MINIMAX_KEY=<MiniMax API key>"),
	skipaudit.EP("readTSDedup", skipaudit.CatHostCapability,
		"跨实现去重阈值比对要读前端 TS 实现；读不到就跳过（3 个测试受影响）。", 1,
		"在含前端源码的仓库里运行（夹具按相对路径读 frontend 的去重实现）",
		"或让该夹具能定位到 frontend 目录"),
}

var sttLedger = skipaudit.Ledger{
	Pkg:      "./internal/stt",
	Entries:  knownSkips,
	MinFuncs: 14, // 实测 14 个 owner
}

func sttDir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/stt")
	if err != nil {
		t.Fatalf("定位包目录失败: %v", err)
	}
	return d
}

// scanStt 是本包唯一的扫描入口。
// ⛔ exempt 传 nil：本包**不再有任何豁免**（见文件头）。
func scanStt(t *testing.T) skipaudit.Result {
	t.Helper()
	res, err := skipaudit.Check(sttLedger, sttDir(t), nil)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	return res
}

// ③ 量具自证。
func TestSkipLedgerIsCompleteAndNotStale_量具自证(t *testing.T) {
	res := scanStt(t)
	if err := skipaudit.SelfTestOK(sttLedger, res); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s：跳过测试 %d 个 / 跳过点 %d 个，台账登记 %d 条", sttLedger.Pkg, res.Found, res.Points, len(knownSkips))
}

// ① 双向精确匹配。
func TestSkipLedgerIsCompleteAndNotStale(t *testing.T) {
	res := scanStt(t)
	if !res.OK() {
		t.Error(res.String())
	}
	if res.LedgeredPoints != res.Points {
		t.Errorf("台账点数 %d ≠ 实际点数 %d ⇒ Points 字段没跑 Register 补齐",
			res.LedgeredPoints, res.Points)
	}
}

// 类别分布显式钉住。
//
// ⚠ 这一类的语义在 2026-10-21 被**实测推翻**过一次：
//
//	它曾经叫 CatPremiseFailed（「前提不成立 ⇒ 永远不会失败」）。
//	实测结果是：那批守卫**全部没有触发** ⇒ 那些测试今天**是在断言的**，
//	不是「永远失败」。名字写错 ⇒ 下一个会话会按错的前提去处理它
//	（去「修好那个永远失败的测试」，而它其实一直是绿的）。
//
// ⇒ 现在叫 CatDefensiveGuard（哑弹守卫），说的是真话：
//
//	守卫的前提**当前恒成立** ⇒ 这条测试今天在跑、在断言；
//	而一旦前提成立，它会**静默退出门外** ——
//	那时绿色读数与「跑过并通过」完全无法区分。
func TestSkipLedger_哑弹守卫计数被钉住(t *testing.T) {
	const wantDefensiveGuard = 1
	got := 0
	for _, e := range knownSkips {
		if e.Cat == skipaudit.CatDefensiveGuard {
			got++
		}
	}
	if got != wantDefensiveGuard {
		t.Errorf("哑弹守卫类实际 %d 条，钉住值是 %d。"+
			"⇒ 新增一条要有意识地改这个常量并写明理由。", got, wantDefensiveGuard)
	}
}

// ⭐ 负控：抽取器必须看不见「字面量里的假调用」。
//
// 这条是「本文件不再需要豁免」的全部依据 —— 如果哪天抽取器退回了
// 「剥注释 + 正则 + 配平花括号」，本测试会红，而豁免就必须加回来。
//
// 样本刻意覆盖三种曾经各坑过一次的形态：
//   - 行注释里的假函数（§464）
//   - 块注释里的假函数
//   - **原始字符串里的假函数**（§467 必须为本文件豁免的就是这个）
func TestSkipLedger_抽取器看不见字面量里的假调用(t *testing.T) {
	dir := t.TempDir()
	const sample = "package stt\n" +
		"// func TestInComment(t *testing.T) { t.Skip(\"注释里的不算\") }\n" +
		"/* func TestInBlock(t *testing.T) { t.Skip(\"块注释里的也不算\") } */\n" +
		"const raw = `func TestInRaw(t *testing.T) { t.Skip(\"原始字符串里的也不算\") }`\n" +
		"func TestReal(t *testing.T) { t.Skip(\"这个才算\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "zz_sample_test.go"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := skipaudit.ScanDir(dir, nil)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(fs) != 1 || fs[0].Name != "TestReal" || fs[0].Points != 1 {
		t.Fatalf("期望只认出 TestReal/1 点，实测 %+v\n"+
			"  ⚠ 注释或原始字符串里的假调用被算进来了 ⇒ 本文件的豁免必须加回来，"+
			"并且 §467 那三个幽灵点会重新出现在普查里", fs)
	}
}
