// 真网关探针（默认 skip，2026-10-08）：**next_meeting 的原文到底能不能解析成时刻**。
//
// ── 为什么这个探针现在就能做，而不需要真机 ──
//
// 「将一些时间点自动加入计划日程」这条需求在 `next_meeting` 上断过好几轮，
// 但一直**没有一条读数**说明模型实际吐出来的 next_meeting 长什么样：
//
//	· §17.1 实测过「next_meeting 有时间点」（1768 字那轮）——**只有有没有**，
//	  没有**是什么**，也没有「产品侧解析器吃不吃得下」。
//	· §54.2 实测过修复前 next_meeting 是 null。
//	· §229 的前端探针（`/tmp/opstt/probe-nextmeeting-226.ts`）量的是
//	  **我们自己构造的候选集合**（19 个形态里修复前 17 个解不出）——
//	  它证明「这些形态可达且会静默断掉」，**不构成任何真实分布的估计**。
//
// ⇒ 本探针补的就是这条缺失的读数：真网关 → 真提示词 → 真解析器。
//
// ── 两个臂，来源不同，别混 ──
//
// 臂 A：仓库里**真实 ASR 输出**（真网关 mimo-v2.5-asr 对 tts-meeting.wav 的
//
//	返回原文，2026-10-07 实测）。里面埋着一个**明说**的时间点
//	「下周三下午三点」⇒ 量基线。
//
// 臂 B：**构造**的转写，时间点只能用**指代**表达（「下周三下午三点那个会之后
//
//	咱们再对一次具体的」）。⚠ 这是我写的文本，**不是实测材料**。
//	它量的是**能力边界**（模型遇到指代时会吐什么、能不能解析），
//	**不是**真实会议里的出现频率。
//
// ── 纪律 ──
//
//  1. **边跑边落盘**：每一条样本立刻 O_APPEND 写进
//     /tmp/opstt/next-meeting-live.json ⇒ 跑到一半超时/崩溃也不丢已拿到的证据。
//  2. **解析失败也是数据**：`parseRefineJSON` 失败**不 Fatal**，照记一条
//     （那正是 §218「摘要回落被当成功」那一族的观测点）。
//  3. **分母写进文件名与日志**：每条记录带 arm/model/round，不做事后挑样本。
//  4. 耗时按实测估：glm-5.2 首字节样本 14~40s，网关 24~142s（§44.8），
//     单轮预算 240s（与既有探针同口径）。
//
// 显式开关（会消耗真实额度、依赖外网）：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveNextMeetingRaw -v -timeout 60m
package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

// 默认落盘路径。⚠ §241：做**协议形态 A/B** 时两次运行不能写同一个文件
// ——混在一起就分不出哪条是哪条（这正是「分母必须是全样本」在落盘侧的同款）。
// 可用 POCKET_LIVE_NEXT_MEETING_OUT 覆盖。
const nextMeetingOutDefault = "/tmp/opstt/next-meeting-live.json"

func nextMeetingOutPath() string {
	if v := strings.TrimSpace(os.Getenv("POCKET_LIVE_NEXT_MEETING_OUT")); v != "" {
		return v
	}
	return nextMeetingOutDefault
}

/**
 * 臂 A 的输入：仓库既有的真实 ASR 输出（不是这里写的）。
 *
 * ★ 复用常量而不是复制一份 —— 复制就会漂，而漂了之后
 *   「臂 A 是真实材料」这句话就不再成立。
 */
const armARealTranscript = realASRTranscript

/**
 * 臂 B 的输入：**我构造的**转写（见文件头「两个臂，来源不同，别混」）。
 *
 * 埋的是一个**只能用指代表达**的下次会议时间点：上文说「下周三下午三点」，
 * 后面只说「那之后咱们再对一次具体的」⇒ 模型若照抄原话，next_meeting
 * 大概率是「那之后」这类**指代**，而产品侧解析器对指代是明确判 null 的
 * （§229.2 的「不猜」负控钉着）。
 */
const armBReferentialTranscript = "这次客户评审会定在下周三下午三点，跟王总还有林兰一起。" +
	"对比表和预算确认都按原计划推进。" +
	"那之后咱们再对一次具体的，到时候再拉齐剩下的细节。"

/**
 * 臂 C 的输入：**我构造的长转写**（≈1800 字，模拟一场两小时的会）。
 *
 * ⚠ 构造文本，不是实测材料 —— 与 §17.1 的「8 倍，模拟两小时会议」同一做法。
 *
 * 为什么要它：§240 那道算术（anthropic 兜底 4096 < §101 实测会议精校输出 4472）
 * **只在输出够长时才可能显形**。§241 的短输入 A/B（两臂各 4 次）实测
 * anthropic 形态截断 0/4 —— 那不否证 §240，只说明**输入太短**。
 * ⇒ 要把机制验出来，必须把输出推到天花板附近。
 *
 * ★ 它同时是用户需求「录音完成后精校」最真实的场景：
 *   长会议 ⇒ 输出长 ⇒ 正好撞在 4096 上。
 */
const armCLongTranscript = "会议开始。张伟先过一下这周的进度，客户评审会的材料已经准备了两轮。" +
	"王总那边反馈希望能加上机载传感器的对比表，最好把历次的测试数据也放进去，" +
	"这样评审的时候能直接看趋势，不用再回头翻历史记录。李兰负责把测试数据整理出来，" +
	"周三之前发到共享目录里，格式跟上次那份保持一致。上次那份是用 Excel 做的，" +
	"这次如果数据量大，可能需要换成表格工具来出图，王总那边有人会用。" +
	"预算这边，张伟说工头安装的确认已经做完了，报价单也回来了，比上季度低了一成左右。" +
	"但是有一个问题，工头那边说如果要增加传感器模组，交付周期会多两周，" +
	"这个要提前跟客户说清楚，别到后面才发现。续期合同必须在十一月底之前签完，" +
	"这是硬指标，法务那边已经看过条款了，就等我们这边的最终版本。" +
	"关于续期的价格，王总的意思是按现在的量续一年，如果量能上去可以再谈折扣。" +
	"另外还有一个悬而未决的问题，就是机载传感器的供应商那边要不要换。" +
	"现在这家用了两年，价格一直偏高，但交付稳定。换的话要重新做认证，" +
	"周期至少两个月，赶不上十一月底的续期。这个得王总拍板，我们先把两家的对比做出来。" +
	"会议继续。林岚这边同步一下排期的事情。客户评审会定在下周三下午三点，" +
	"地点在星海科技总部三楼，会议室已经订好了。王总和林兰都会参加，" +
	"另外他们那边还想带一个人过来，具体是谁还没定，让行政对接一下。" +
	"评审会的材料需要在会前一天发出去，所以对比表和测试数据的整理要在这周内完成，" +
	"李兰周三之前给数据，张伟周四出对比表初稿，我周五统一排版。" +
	"关于后续的节奏，王总说评审完如果没问题，下个月可以再对一次具体的，" +
	"把下一步的实施计划定下来。上次那种临到跟前才定时间的做法这次不要了，" +
	"要提前把时间定下来写进日程里，免得到时候大家都记不住。" +
	"还有一个待办，林岚提到上次的验收报告一直没交，这个要尽快补上，" +
	"客户那边已经催了两次了。我这边周五之前会把验收报告的框架发出来，" +
	"林岚补充数据，月底之前交上去。验收报告的模板在共享目录里，名字我发群里。" +
	"关于预算，林岚问了下个月的培训费用能不能走今年的预算，" +
	"行政说原则上不行，除非是年初就排进去的项目。我建议还是按明年的预算走，" +
	"不然年底对账的时候会比较麻烦。这个事王总确认一下。" +
	"另外王总提到，他们那边年底有一个供应商大会，问我们要不要派人参加。" +
	"这个如果参加的话，我们这边可以顺便把续期的事情当面谈一谈，" +
	"比走邮件快。我倾向于派张伟去，因为他跟工头那边已经熟了。" +
	"时间大概在十二月中旬，具体日期还没定，等行政通知。" +
	"讨论到风险。会议最大的风险是续期合同赶不上十一月底，" +
	"因为供应商那边的决策流程要走三层，一层一层批下来至少要两周。" +
	"所以我们内部要把能提前的都提前，材料这周就要给到法务。" +
	"第二个风险是机载传感器如果中途要换供应商，认证周期会直接影响明年的排期。" +
	"第三个风险是验收报告拖着，这个已经是第三次被催了。" +
	"王总说这三个里面续期合同是最要紧的，其他的可以往后放。" +
	"那就按这个优先级来。续期的材料这周内准备好，法务那边同步启动。" +
	"验收报告月底之前必须交，这个不能再拖了。" +
	"机载传感器的评估放在下个月，跟续期不冲突。" +
	"好，最后确认一下时间点。客户评审会下周三下午三点，在星海科技总部三楼。" +
	"对比表初稿张伟周四给我。李兰周三之前把测试数据发出来。" +
	"续期合同材料这周内交给法务，目标十一月底之前签完。" +
	"验收报告月底之前交。预算的事情等王总确认。" +
	"供应商大会十二月中旬，派人参加，张伟去。" +
	"下次对一次的时间会后再定，到时候提前写进日程。" +
	"今天就到这里，谢谢大家。"

var armCLen = 0

/** 一条样本。字段全部原样保留，不做任何"清洗"。 */
type nextMeetingSample struct {
	Arm      string `json:"arm"`
	Model    string `json:"model"`
	Round    int    `json:"round"`
	NextMtg  string `json:"nextMeeting"` // 原样；null 时是空串
	Present  bool   `json:"present"`     // 字段在不在 / 有没有内容
	Err      string `json:"err,omitempty"`
	Elapsed  int64  `json:"elapsedMs"`
	StampISO string `json:"stampISO"`
}

/** 落盘一条（O_APPEND，每条一行 JSON）。失败只打日志，不影响探针。 */
func appendNextMeetingSample(t *testing.T, s nextMeetingSample) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Logf("样本序列化失败（仍继续下一条）：%v", err)
		return
	}
	f, ferr := os.OpenFile(nextMeetingOutPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr != nil {
		t.Logf("落盘失败（仍继续下一条）：%v", ferr)
		return
	}
	defer f.Close()
	if _, werr := f.Write(append(b, '\n')); werr != nil {
		t.Logf("写样本失败：%v", werr)
	}
}

/** 从 parseRefineJSON 的 map 里取 structured_minutes.next_meeting。 */
func nextMeetingOf(parsed map[string]any) (string, bool) {
	sm, ok := parsed["structured_minutes"].(map[string]any)
	if !ok {
		return "", false
	}
	raw, ok := sm["next_meeting"]
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case nil:
		return "", false
	case string:
		return v, true
	default:
		// 非字符串也要记 —— 那本身是契约漂移的观测点。
		return string(nextMeetingJSON(v)), true
	}
}

func nextMeetingJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestLiveNextMeetingRaw(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关探针：需 POCKET_LIVE_GATEWAY=1（消耗真实额度）")
	}
	// 名单走环境变量，默认三个此前对中文商务转写有过实测的现役模型。
	// ⚠ 不手抄全量 SSOT：那是 scripts/probe-preferred-models.mjs 的职责，
	//   抄一份必然漂（§196 实测过一次手抄名单漂了 14 个模型）。
	models := []string{"glm-5.3", "minimax-m3", "kimi-k3"}
	if v := strings.TrimSpace(os.Getenv("POCKET_LIVE_NEXT_MEETING_MODELS")); v != "" {
		models = strings.Split(v, ",")
	}
	rounds := 2
	if v := strings.TrimSpace(os.Getenv("POCKET_LIVE_NEXT_MEETING_ROUNDS")); v != "" {
		for _, c := range v {
			if c >= '0' && c <= '9' {
				rounds = int(c - '0')
				break
			}
		}
	}
	allArms := []struct {
		name string
		text string
	}{
		{"A-真实ASR-明说时间", armARealTranscript},
		{"B-构造-只可指代", armBReferentialTranscript},
		{"C-构造-长会议≈两小时", armCLongTranscript},
	}
	// 臂选择：§241 的 A/B 已把 A/B 两臂跑完并落盘，
	// 复核长转写时只跑 C 臂（省额度也避免把已落盘的读数混进新文件）。
	arms := allArms
	if v := strings.TrimSpace(os.Getenv("POCKET_LIVE_NEXT_MEETING_ARMS")); v != "" {
		want := map[string]bool{}
		for _, c := range strings.Split(v, ",") {
			want[strings.TrimSpace(c)] = true
		}
		arms = nil
		for _, a := range allArms {
			if want[a.name[:1]] {
				arms = append(arms, a)
			}
		}
		if len(arms) == 0 {
			t.Fatalf("POCKET_LIVE_NEXT_MEETING_ARMS=%q 没匹配到任何臂", v)
		}
	}
	t.Logf("臂 C 转写长度：%d 字符（§240 的 4096 天花板要在输出够长时才可能显形）", len([]rune(armCLongTranscript)))
	t.Logf("模型 %v · 每臂 %d 轮 · 共 %d 次调用（耗时按实测估，每轮 240s 预算）",
		models, rounds, len(models)*rounds*len(arms))

	total, okCount, emptyCount, errCount := 0, 0, 0, 0
	// ⚠⚠ §495 接账本。分母用 §492 指出的那个表达式
	// （= len(models)*rounds*len(arms)，与 `total++` 处在循环里恒等）。
	//
	// 三态映射（这一条的判别式）：
	//   okCount    ⇒ Complete（转写出内容）
	//   emptyCount ⇒ **也** Complete：「模型明确没给出下一个会议」是一个
	//                **有效读数**，不是没跑成。它已被 emptyCount 单独计数
	//                并在汇总里报出，所以不会与 ok 混成同一个数。
	//   errCount   ⇒ **故意不记**：调用/解析失败是真失败（不是按设计不成立），
	//                不记即被账本算进「没做完」⇒ Report() 会出声。
	//                ⚠ 这里**不豁免**：§495 的豁免只给「输入不足」那类先验理由。
	ledger := livetest.New("NextMeeting 探针", len(models)*rounds*len(arms))
	for _, m := range models {
		bff := liveProbeService(t)
		s := &Server{}
		s.SetLLMBFF(bff, nil)
		s.cfg.LLMModel = m
		for _, arm := range arms {
			for r := 1; r <= rounds; r++ {
				total++
				smp := nextMeetingSample{
					Arm: arm.name, Model: m, Round: r,
					StampISO: time.Now().Format(time.RFC3339),
				}
				started := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
				req := httptest.NewRequest("POST", "/api/meetings/m1/refine", nil)
				content, _, err := s.llmChatOnce(ctx, req, m, []aigate.ChatMessage{
					{Role: "user", Content: buildRefinePrompt(arm.text, "", meetingMetaIn{
						Title:        "客户评审会筹备",
						Participants: []string{"张伟", "林岚"},
					})},
				})
				cancel()
				smp.Elapsed = time.Since(started).Milliseconds()
				if err != nil {
					smp.Err = err.Error()
					errCount++
					t.Logf("[%s|%s|第%d轮] llmChatOnce 失败（记为数据，不 Fatal）：%v", arm.name, m, r, err)
					appendNextMeetingSample(t, smp)
					continue
				}
				// ★ 解析失败也是数据，不 Fatal。
				parsed, perr := parseRefineJSON(content)
				if perr != nil {
					smp.Err = "parseRefineJSON: " + perr.Error()
					errCount++
					t.Logf("[%s|%s|第%d轮] parseRefineJSON 失败：%v\n原始：%s", arm.name, m, r, perr, content)
					appendNextMeetingSample(t, smp)
					continue
				}
				v, present := nextMeetingOf(parsed)
				smp.NextMtg, smp.Present = v, present
				if !present || strings.TrimSpace(v) == "" {
					emptyCount++
					ledger.Complete(arm.name + "|第" + strconv.Itoa(r) + "轮(空/缺)")
					t.Logf("[%s|%s|第%d轮] next_meeting 空/缺（%dms）", arm.name, m, r, smp.Elapsed)
				} else {
					okCount++
					ledger.Complete(arm.name + "|第" + strconv.Itoa(r) + "轮")
					t.Logf("[%s|%s|第%d轮] next_meeting = %q（%dms）", arm.name, m, r, v, smp.Elapsed)
				}
				appendNextMeetingSample(t, smp)
			}
		}
	}
	// ⚠⚠ §495：等式对账必须打在**汇总之前**。
	//
	// 下面 `:314` 那行已经把 total 当分母印出去了 ⇒ 断言晚一步就成了
	//「先报读数、再补一句不完整」，那等于没补（§491/§492 两次都栽在这）。
	//
	// 为什么这条等式今天**必然成立**、却仍然要写：`total++` 在内层循环体第一行、
	// 早于任何失败分支，而三个桶的 ++ 都在各自分支里。⇒ 今天它恒真。
	// ⚠ 它防的是**将来**：新加一条 `continue` 却忘了 ++ 时，四桶合计会静默变小，
	//   而「有内容 %d」这个数仍然照打 ⇒ 分母在无声地错，且没有任何一处会红。
	// 这与 §495 收回的那条判据④ 不同：④ 的前提由**别处的 Fatal 先触发**，
	// 而这一条的前提（每个出口都 ++）就在**同一处**，没人别处抓 ⇒ 不是真空洞。
	if got, want := okCount+emptyCount+errCount, total; got != want {
		t.Errorf("四桶合计 %d ≠ 分母 %d ⇒ 有一次运行既没进任何一个桶、也没被报成失败；"+
			"下面那行「有内容 %d · 空/缺 %d · 调用或解析失败 %d」的分母是错的"+
			"（多半是新加的 continue 忘了 ++）", got, want, okCount, emptyCount, errCount)
	}
	// ⚠⚠ 账本对账同样打在汇总**之前**（同上）。
	if rep := ledger.Report(); rep != "" {
		t.Log(rep)
	}
	t.Logf("  账本：跑成 %d / 共 %d（= 有内容 %d + 空/缺 %d）· 真失败 %d（不计入跑成，由 Report 负责出声）",
		ledger.Done(), ledger.Total(), okCount, emptyCount, errCount)
	t.Logf("════ 汇总（分母 = 全部 %d 次调用）════", total)
	t.Logf("  有内容 %d · 空/缺 %d · 调用或解析失败 %d", okCount, emptyCount, errCount)
	t.Logf("  原始读数已逐条落盘：%s（形态=%s）", nextMeetingOutPath(), os.Getenv("POCKET_LLM_GATEWAY_FORMAT"))
	t.Logf("  ⚠ 分层统计（按臂/按模型）请用离线脚本从 JSON 重算，不要在这里凭印象报。")
}
