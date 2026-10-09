// 真网关探针（默认 skip）：量「切片重叠秒数」与「实测文本重叠字数」的关系。
//
// 背景：§19 在真实 ASR 上复现了用户的「片段重复」（5 个切点只有 1 个去重
// 生效，最坏一档整句重复），并试过「逐档扫描窗口」的改法 —— 去重命中从
// 1/5 提到 4/5，但它**吃掉了真实内容**（把「我们今天讨论了预算和排期，还有人」
// +「排期还有人员安排要尽快定下来」里的「排期还」当重叠裁掉）。
//
// 撤回时写下的真正修法方向是：**用时间信号**。切片带已知的重叠时长
// （chunk.StartSec / EndSec），中文语速下重叠字数有上界，
// 用时间上界当闸就不必靠文本相似度瞎猜。
//
// ⚠⚠⚠ 2026-10-07 跑完 20 档（40 次真实调用）后，**推翻了 §19 的整个前提**。
// 这一段是本文件最重要的事，先读它再看用法。
//
// ── 1. 生产切片**不重叠** ──
// 前端 recordingRuntime.ts:899-901 送的是
//
//	startSec = endSec - windowSec
//
// 即**纯新增窗口**；VAD（silenceMs 1500）决定「何时发」，
// NOTE_CHUNK_MS=3000 只是连续讲话时的兜底 —— **两条路都不重叠**。
// 而 incremental.go:40 的 IncrementalOverlapSec=1.0 常量**零使用点**：
// 包头注释写的「每段尾部留 overlapSec 秒与下一段重叠」**从未接线**。
//
// ⇒ §19 整节复现「整句重复」用的切法带 1.8s 重叠，
//
//	**那个形状在生产上不会发生**。整整一节分析了一个不存在的场景。
//	教训：夹具形状必须照抄真实源（§14 我自己又犯过一次）。
//
// ── 2. 生产形状下重复量只有 1–2 字，现有算法 0/8 误判 ──
//
//	组别           档数  LCS/next 区间  现有算法判重叠
//	正控（真重叠）   7   0.18 ~ 0.92    2/7
//	负控（不相交）   3   0.05 ~ 0.14    0/3
//	生产形状        8   0.11 ~ 0.25    0/8   ← 用户实际会看到的
//	兜底A 无重叠    1   0.11           0/1
//	兜底B 有重叠    2   0.53           0/2
//
// ⇒ 生产面对的 12 档**全部不该去重**，现有算法 **0 误判**。它保守，但保守得对。
//
// ── 3. 真重叠上确实漏判，且漏在一个能量化的点上 ──
// 兜底B（3 秒硬切 + 1.6s 重叠）实测：
//
//	A(17字) = 今天下午三点，会议室开产品评审会。
//	B(19字) = 会议室开产品评审会，请提前十分钟到场。   真实重复 10 字
//	覆盖率 = 10 / min(17, 40, 19) = 10/17 = 0.59
//	anchorCoverage = 0.60   ← 差 0.01 漏判，合并后整句重复
//
// 改法（**未实施**）：闸从「覆盖率 0.6」改成「绝对长度 >= 6」。
// 20 档实测：命中 2/9 → 5/9，误去重仍 0/12。6 字对应 1.2 秒重叠。
//
// ⇒ **不改**，两条理由：① 6 是从这 20 档里挑的，而 20 档**全部来自同一条
//
//	4.6s 干净音频** —— 语料单一比样本量更要命；
//	② 它是「覆盖率」这道唯一闸的替代品，拆掉后 §19.3 那对「排期还」
//	（LCS=5）会被重新误判，而 5 与 6 只差 1。
//	⇒ 在有多条真实语料之前，0.59 vs 0.60 这个边界改不得。
//
// ── 4. 更根本的缺陷：兜底路径上词被切碎，且无重叠可依 ──
// 生产形状档实测到实例：A 结尾「…会议室开产品产。」B 开头「品评审会…」——
// 「产」被劈成两半。这正是 incremental.go 包头注释说的
// 「硬切在词中间，两侧各丢一半信息」。
// 全量路径（full.go）早用 VAD 修好了（实测 CER 8.9% → 5.5%），
// **即时转写路径没有** —— 它是唯一还在硬切的那条。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-16k.wav \
//	POCKET_LIVE_ASR_MODEL=mimo-v2.5-asr \
//	go test ./internal/stt -run TestLiveGatewayOverlapTimingVsText -v
//
// ⚠ 本探针**只打印数据**（t.Logf），不做判定断言：产物是给人看的分布，
//
//	断言写在哪里取决于分布长什么样。把观察直接写成断言就是在没看数据前编
//	结论 —— §19 这么栽过，§24 也是。
//	唯一的断言是**档位自身合法性**，见 isOverlappingGroup。
package stt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// isOverlappingGroup 报某一组档位在设计上是否要求两片在音频上重叠。
//
// ⚠ 单独抽出来是因为第一版把这个判断内联在循环里，加「兜底B」组时只改了
//
//	上半段（Fatal 提示），下半段仍按「非正控即不相交」判，于是兜底B 第一档
//	就被自己的校验 Fatal 掉 —— 判据没跟上档位设计。
//	加新组时**必须**改这里，别在内联处改。
func isOverlappingGroup(group string) bool {
	switch group {
	case "正控", "兜底B":
		return true
	default:
		return false
	}
}

type overlapProbe struct {
	label      string
	startA     float64
	endA       float64
	startB     float64
	endB       float64
	textA      string
	textB      string
	overlapLCS int
	dedupHit   bool
	negative   bool
	group      string
}

// TestLiveGatewayOverlapTimingVsText 在真实 ASR 上量时间重叠与文本重叠的关系。
func TestLiveGatewayOverlapTimingVsText(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关重叠计时探针：需 POCKET_LIVE_GATEWAY=1")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	audioPath := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_AUDIO"))
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	if baseURL == "" || apiKey == "" {
		t.Fatal("缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	if audioPath == "" {
		audioPath = "/tmp/gt-voice-16k.wav"
	}
	if model == "" {
		model = "mimo-v2.5-asr"
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("读音频 %s：%v", audioPath, err)
	}

	// 档位设计：重叠秒数从 0.2 拉到 2.0。
	//
	// ★ 关键：每档的两片**都必须真的在时间上重叠**（startB < endA），
	//   否则量到的是「不相交的相邻切片」，那个场景本来就不该去重，
	//   混进来会把分布搅乱（这正是「夹具形状要照抄真实源」那个教训）。
	//
	// 负控档（overlap0s-*）是**另一个问题**，不能省：
	// 时间上界能不能**保护**「换句式但用词相同」那档不被误去重？§19.3 撤回的
	// 原因正是它被误判（「排期还」被吃掉）。不相交 ⇒ 时间上界 = 0 ⇒
	// 任何合理实现都该直接跳过对齐。没有负控就证明不了这个保护存在 ——
	// 而那恰恰是时间信号相对于纯文本信号**唯一的增量价值**。
	//
	// 后一片的尾部对齐到音频末尾，保证每档的 B 都有内容。
	cuts := []struct {
		label        string
		startA, endA float64
		startB       float64
		endB         float64 // 0 表示对齐到音频末尾
		negative     bool
		group        string
	}{
		// ── 正控：两片在音频上真的重叠 ──
		{"overlap0.2s", 0.0, 2.4, 2.2, 0, false, "正控"},
		{"overlap0.4s", 0.0, 2.8, 2.4, 0, false, "正控"},
		{"overlap0.6s", 0.0, 3.0, 2.4, 0, false, "正控"},
		{"overlap0.8s", 0.0, 3.2, 2.4, 0, false, "正控"},
		{"overlap1.2s", 0.0, 3.4, 2.2, 0, false, "正控"},
		{"overlap1.6s", 0.0, 3.8, 2.2, 0, false, "正控"},
		{"overlap2.0s", 0.0, 4.2, 2.2, 0, false, "正控"},
		// ── 负控：两片在音频上**完全不相交**（时间上界应为 0）──
		{"overlap0s-负控-长A", 0.0, 1.6, 1.6, 0, true, "负控"},
		{"overlap0s-负控-短A", 0.0, 1.2, 1.2, 0, true, "负控"},
		{"overlap0s-负控-中A", 0.0, 2.0, 2.0, 0, true, "负控"},
		// ── 生产形状 ★ 这组才是真正回答「用户会看到什么」的 ──
		//
		// 2026-10-07 查出来的关键事实：前端 recordingRuntime.ts:899-901 送的是
		//   startSec = endSec - windowSec，即**新增窗口**，
		//   3 秒定长硬切、silenceCut: false —— 相邻两片在时间上**完全不重叠**。
		//
		// ⇒ §19 整节复现「整句重复」用的切法带 1.8s 重叠，
		//   **那个形状在生产上根本不存在**。那节是在一个不会发生的场景上
		//   花了整整一节（「夹具形状没照抄真实源」的复发）。
		//
		// 这组用生产真实切法重测，回答的是用户实际会看到什么。
		// 每档的 B 都是**定长一片**（与 A 同窗宽），对齐到音频末尾的写法在这里
		// 不适用：4.64s 的素材装不下两个 3 秒片，B 会长度为 0。
		// 装不下的档下面会如实跳过 —— 不用「凑一个更短的 B」来假装有样本。
		{"生产形状-3s-起点0", 0.0, 3.0, 3.0, 4.64, true, "生产形状"},
		{"生产形状-3s-起点1", 1.0, 4.0, 4.0, 4.64, true, "生产形状"},
		{"生产形状-2s-起点0", 0.0, 2.0, 2.0, 4.0, true, "生产形状"},
		{"生产形状-2s-起点1.5", 1.5, 3.5, 3.5, 4.64, true, "生产形状"},
		{"生产形状-1.5s-起点0", 0.0, 1.5, 1.5, 3.0, true, "生产形状"},
		{"生产形状-1.5s-起点1.8", 1.8, 3.3, 3.3, 4.64, true, "生产形状"},
		{"生产形状-1s-起点0", 0.0, 1.0, 1.0, 2.0, true, "生产形状"},
		{"生产形状-1s-起点1.2", 1.2, 2.2, 2.2, 3.2, true, "生产形状"},
		// ── 兜底路径 A/B 对照：连续讲话时的 3 秒硬切，加不加重叠 ──
		//
		// 前端 NOTE_CHUNK_MS=3000 是「连续讲话无静音」时的兜底（VAD 正常时
		// 按 1500ms 静音切）。**两条路都不重叠** —— recordingRuntime.ts:901
		// 的 startSec = endSec - windowSec 送的是纯新增窗口。
		// 而 incremental.go:40 的 IncrementalOverlapSec=1.0 常量**零使用点**：
		// 包头注释写的「每段尾部留 overlapSec 秒与下一段重叠」从未接线。
		//
		// ⇒ 兜底路径上词会被切碎，且**没有重叠可依**。上面「生产形状」组已
		//   看到实例：A 结尾「…开产品评审会。」B 开头「体评审会…」——「产」被劈开。
		//
		// 这三档直接量「加上重叠值不值」。重叠时长取 1.6s 不是拍的：
		// §24 实测 1.2s 档 LCS 7 字（判据 0.46，未过）而 1.6s 档 9 字（0.69，过）。
		{"兜底A-3s硬切-无重叠", 0.0, 3.0, 3.0, 4.64, true, "兜底A"},
		{"兜底B-3s硬切-重叠1.6s", 0.0, 3.0, 1.4, 4.64, false, "兜底B"},
		{"兜底B-3s硬切-重叠2.0s", 0.0, 3.0, 1.0, 4.64, false, "兜底B"},
	}
	dur := wavDurationSec(t, audio)
	pos, neg, prod := 0, 0, 0
	for _, c := range cuts {
		switch c.group {
		case "正控":
			pos++
		case "负控":
			neg++
		case "兜底A", "兜底B":
			prod++
		default:
			prod++
		}
	}
	t.Logf("音频 %s 时长 %.2fs，共 %d 档（正控 %d / 负控 %d / 生产形状 %d，每档 2 次真实 ASR 调用）",
		audioPath, dur, len(cuts), pos, neg, prod)

	tr := NewTranscriber(apiKey, model, baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	var rows []overlapProbe
	for _, c := range cuts {
		if c.endA > dur || c.startB >= dur || c.endA <= c.startA {
			t.Logf("跳过 %s：超出音频时长 %.2fs", c.label, dur)
			continue
		}
		// 档位自身合法性 —— 配错档位不会让程序报错，只会安静地量错东西。
		//
		// ⚠ 第一版只分「正控 / 负控」两类。生产形状组（3 秒定长硬切）
		//   与负控组一样是**严格不相交**的，若沿用「非负控即视为重叠」的判法，
		//   它会在第一次跑到时直接 Fatal —— 那是判据没跟上档位设计，
		//   不是档位配错。判据按 group 走，别按 bool 走。
		if isOverlappingGroup(c.group) != (c.startB < c.endA) {
			t.Fatalf("%s 档 %s 配置与组别矛盾：startB(%v) endA(%v)；"+
				"「%s」组要求两片%s。改档位时组别和坐标要一起改。",
				c.group, c.label, c.startB, c.endA, c.group,
				map[bool]string{true: "重叠", false: "严格不相交"}[isOverlappingGroup(c.group)])
		}

		endB := c.endB
		if endB <= 0 {
			endB = dur
		}
		// B 片装不下就如实跳过。sliceWav 对零长度区间是 Fatal，
		// 而「凑一个更短的 B」会把「定长切片」这个生产前提悄悄换掉 ——
		// 那正是本组档位存在的理由。
		if endB-c.startB < 0.15 {
			t.Logf("跳过 %s：B 片只有 %.2fs，装不下（素材仅 %.2fs）", c.label, endB-c.startB, dur)
			continue
		}
		audioA := sliceWav(t, audio, c.startA, c.endA)
		audioB := sliceWav(t, audio, c.startB, endB)

		ra, err := tr.TranscribeFor(ctx, Scope{UserID: "probe", WorkspaceID: "ws_probe"}, audioA, "a.wav")
		if err != nil {
			t.Logf("档位 %s：片A 转写失败 %v", c.label, err)
			continue
		}
		rb, err := tr.TranscribeFor(ctx, Scope{UserID: "probe", WorkspaceID: "ws_probe"}, audioB, "b.wav")
		if err != nil {
			t.Logf("档位 %s：片B 转写失败 %v", c.label, err)
			continue
		}

		textA := strings.TrimSpace(ra.Text)
		textB := strings.TrimSpace(rb.Text)
		overlapSec := c.endA - c.startB
		// 纯字符级 LCS：只看「两段文本到底有多少字是重复的」，
		// 刻意不过任何相似度阈值 —— 这里要的是**事实**，不是判定。
		lcsLen := rawLCSLen([]rune(textA), []rune(textB))
		// 现有算法在这一档上判不判重叠（用来对照，不参与结论）。
		_, hit := fuzzyOverlapTail([]rune(textA), []rune(textB), 40)

		rows = append(rows, overlapProbe{
			label: c.label, startA: c.startA, endA: c.endA, startB: c.startB, endB: endB,
			textA: textA, textB: textB, overlapLCS: lcsLen, dedupHit: hit, negative: c.negative, group: c.group,
		})
		t.Logf("[%s] 重叠 %.2fs | A(%d字,%v) B(%d字,%v) | 文本LCS=%d | 现有算法去重=%v",
			c.label, overlapSec, len([]rune(textA)), textA, len([]rune(textB)), textB, lcsLen, hit)
	}

	if len(rows) == 0 {
		t.Fatal("没有一档转写成功 —— 不能拿空分布下结论")
	}

	t.Log("")
	t.Log("┌─ 汇总：时间上界靠不靠得住 ──────────────────────────────────────────")
	t.Log("│ 档位               重叠秒  文本LCS字  字/秒   现有去重")
	var maxRate float64
	for _, r := range rows {
		ov := r.endA - r.startB
		if ov < 0 {
			ov = 0
		}
		rate := 0.0
		if ov > 0 {
			rate = float64(r.overlapLCS) / ov
			if rate > maxRate {
				maxRate = rate
			}
		}
		t.Logf("│ %-18s %6.2f  %8d  %6.2f  %v", r.label, ov, r.overlapLCS, rate, r.dedupHit)
	}
	// ── 怎么读这张表（判据是分布形状，不是任何单点）──
	//
	// ① 正控档的「字/秒」若**随重叠秒数近似线性上升**（斜率 = 语速），
	//    说明文本重叠确实受音频重叠约束 ⇒ 时间上界**可用**。
	//    若它散落无规律，说明 ASR 的切片边界效应压过了音频重叠 ⇒ 否决。
	// ② 取 maxRate 作「实测语速上界」，比拍脑袋的语速常数有依据。
	// ③ **负控档**（重叠 0s）的文本 LCS 必须**明显低于**正控档。
	//    若不相交的两片也测出高 LCS（常用词碰撞），那时间上界在
	//    overlap=0 处就压不住任何东西 ⇒ 上界形同虚设。
	t.Log("└──────────────────────────────────────────────────────────────────")
	t.Logf("实测语速上界（max 字/秒）= %.2f", maxRate)

	// ── 分组读数：生产形状那一组才是用户会看到的 ──
	t.Log("")
	t.Log("┌─ 分组：LCS 占 next 的比例（判据的分离度）──────────────────────────")
	t.Log("│ 组别        档数   LCS/next 区间        现有算法判重叠")
	for _, g := range []string{"正控", "负控", "生产形状", "兜底A", "兜底B"} {
		var lo, hi float64 = 1e9, -1e9
		var n, hit int
		for _, r := range rows {
			if r.group != g {
				continue
			}
			n++
			if r.dedupHit {
				hit++
			}
			ratio := float64(r.overlapLCS) / float64(len([]rune(r.textB)))
			if ratio < lo {
				lo = ratio
			}
			if ratio > hi {
				hi = ratio
			}
		}
		if n == 0 {
			continue
		}
		t.Logf("│ %-10s %4d   %.2f ~ %.2f         %d/%d", g, n, lo, hi, hit, n)
	}
	t.Log("└──────────────────────────────────────────────────────────────────")
	t.Log("★ 生产形状那组是**用户实际会看到的**（3 秒定长硬切、相邻片不重叠）。")
	t.Log("  §19 复现「整句重复」用的是带 1.8s 重叠的切法，那在生产上不会发生。")
	for _, r := range rows {
		if r.negative || r.endA-r.startB <= 0 {
			t.Logf("负控 %s：重叠 0s 但文本 LCS=%d「%q ∩ %q」—— 这是常用词碰撞，"+
				"时间上界在 overlap=0 处要靠它压住", r.label, r.overlapLCS, r.textA, r.textB)
		}
	}
	t.Log("⚠ 本探针不判定对错：判据是上面「字/秒」列的分布形状，由人读。")
}

// rawLCSLen 返回两段文本的最长公共子序列长度（不做任何阈值判定）。
//
// 复用 incremental.go 里的 lcsAlign，但避开 fuzzyOverlapTail 的两道闸 ——
// 那两道闸是**被测对象**，在这里用它们会循环论证。
func rawLCSLen(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	dp, _ := lcsAlign(a, b)
	return dp[len(a)][len(b)]
}

// wavDurationSec 从 16bit/单声道/16kHz 的 RIFF 头里读时长。
func wavDurationSec(t *testing.T, data []byte) float64 {
	t.Helper()
	if len(data) < 44 {
		t.Fatalf("音频太短，读不出 WAV 头")
	}
	byteRate := int(data[28]) | int(data[29])<<8 | int(data[30])<<16 | int(data[31])<<24
	if byteRate <= 0 {
		t.Fatalf("WAV 头 byteRate 非法：%d", byteRate)
	}
	return float64(len(data)-44) / float64(byteRate)
}
