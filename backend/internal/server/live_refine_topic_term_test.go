// live_refine_topic_term_test.go — §138.3「主题接进术语表」的**配对实测**。
//
// ── 为什么必须实测，不能只说「无害」──
//
// `metaTermHint` 里已经有两段这样的记录：
//
//	§64.6 加 `location` 的依据是**实测无害**（4/4 样本 0 次被塞进结果）
//	「⚠ 只测到『无害』，**没测到『有益』**」
//
// 也就是说：把一段新的「可信上下文」加进提示词，**加错也测不出来** —
// 判据当时只验了「没造成伤害」。所以这一次必须把「有益」也量出来。
//
// ── 三个样本、每个两臂（OFF / ON）──
//
//	S1 目标：ASR 把「悬界芯片」转成了「玄介芯片」，
//	   主题里写着「悬界芯片客户评审会」。
//	   OFF = 无元数据；ON = 带主题。量的是「能不能修好」。
//
//	S2 **噪声底噪**：与 S1 逐字相同的转写，但主题写成「本周例会」
//	   （不含任何专有名词）。OFF / ON 两臂的提示词只差这段无关上下文。
//	   ⇒ 两臂的差异就是**模型非确定性的直接测量值**
//	     （§35 实测过：同一份输入在同一份代码上给出过不同结果）。
//	   ⇒ S1 的 ON−OFF 差异必须**明显大于**这个底噪，才谈得上是主题的功劳。
//	     没有 S2，任何「ON 修好了」都可能只是这次运气好。
//
//	S3 **越界风险**：转写里**根本没有**「芯片」二字，主题里有。
//	   §35 实测过名单类上下文会把转写里的人名「对齐」进名单
//	   （张伟/李娜 → 赵敏/孙磊）—— 那是**把对的改成错的**，比漏修危险得多。
//	   主题是另一类内容，但通路是同一条：多一段可信上下文。
//	   ⇒ 这一臂专门量「主题里的词会不会被塞进压根没提它的转写」。
//
// ⚠ 判据只报读数，不给通过阈值：LLM 修得对不对不是二值问题，
//
//	且 §35 已实测模型侧非确定，写死断言只会让下一个人去调到过拟合。
//	本文件要回答的是「方向对不对、代价有没有上升」。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveGatewayRefineTermHintTopic -v -count=1
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// §138.3 在真音频上实测到的两条读数（见 docs §138.3）：
//
//	同一段音频，mimo-v2.5-asr  →「悬界芯片」（对）
//	                 minimax-asr-1.0 →「玄介芯片」（错）
//
// 「悬界」这个写法在仓内语料里是权威的（long-with-due.txt /
// real-refine-prompt.txt 把它当会议主题，参会人写的是「张伟、林岚」）。
const (
	topicTermRight = "悬界"
	topicTermWrong = "玄介"
	topicFull      = "悬界芯片客户评审会"
)

type topicCase struct {
	name       string
	transcript string
	// onTopic 是 ON 臂带的主题；空串表示 ON 臂也不带（那两臂输入逐字相同）。
	onTopic string
	// want 是「如果被正确纠正」后应当出现的词（空 = 不期待任何纠正）。
	want string
	// injected 是「绝不该出现」的词（越界风险的判据）。
	injected string
}

func topicTermCases() []topicCase {
	return []topicCase{
		{
			name:       "S1 目标·ASR 把产品名转错了",
			transcript: "下周三跟客户过一遍，要准备" + topicTermWrong + "芯片和机载传感器的对比表",
			onTopic:    topicFull,
			want:       topicTermRight,
		},
		{
			// ⚠ 与 S1 逐字相同的转写，只换主题。差异即噪声底噪。
			name:       "S2 噪声底噪·主题不含专有名词",
			transcript: "下周三跟客户过一遍，要准备" + topicTermWrong + "芯片和机载传感器的对比表",
			onTopic:    "本周例会",
			want:       topicTermRight,
		},
		{
			// §35 的同族风险：上下文里的词会不会被塞进压根没提它的转写。
			name:       "S3 越界风险·转写里没有该产品名",
			transcript: "今天讨论预算和排期，周五前给结论",
			onTopic:    topicFull,
			injected:   topicTermRight,
		},
	}
}

func TestLiveGatewayRefineTermHintTopic(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("主题术语表配对实测：需 POCKET_LIVE_GATEWAY=1")
	}
	bff := liveGatewayBFF(t)
	s := &Server{llmBFF: bff}
	r, err := http.NewRequest("POST", "/api/meetings/m1/actions", nil)
	if err != nil {
		t.Fatal(err)
	}

	failedArms := 0
	// 每次调用独立超时 + 有限重试（沿用 §35 配对实验的形状）。
	// 单次网络抖动与「模型这么答」是两件事；失败必须计入分母并显式打印。
	retryCount := 0
	runArm := func(c topicCase, meta meetingMetaIn) (string, bool) {
		const maxTry = 3
		var lastErr error
		for try := 1; try <= maxTry; try++ {
			if try > 1 {
				retryCount++
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			res, err := s.llmMeetingRefine(ctx, r, []meetingSegmentIn{{
				Speaker: "发言人", Text: c.transcript, Lang: "zh",
			}}, nil, meta)
			cancel()
			if err != nil {
				lastErr = err
				t.Logf("     … 第 %d/%d 次尝试失败：%v", try, maxTry, err)
				continue
			}
			if rj, _ := res["refine_rejected"].(bool); rj {
				// 兜底拦下名单污染：预期行为，但本样本没传参会人，
				// 出现它说明有别的东西在起作用，如实报出来而不是当成功。
				return fmt.Sprint(res["refined_transcript"]), true
			}
			return fmt.Sprint(res["refined_transcript"]), false
		}
		t.Logf("     ✗ 该臂三试都失败：%v", lastErr)
		return "", true
	}

	// verdict 只判三态：修好 / 原样 / 越界。
	//
	// ⚠⚠ 第一版是 `gotOK := strings.Contains(got, want)`，而 S3 的 want 是**空串**
	//   ⇒ `Contains(got, "")` **恒真** ⇒ 那一臂无论注入没注入都被标成
	//   「✓ 修好」。这是**恒真判据**的又一种形态：判据本身没错，
	//   错在「空目标串」这个输入被当成了「已达成」。
	//   ⇒ 与「零结果先怀疑量具」同族：`want == ""` 必须单独成一支，
	//     那一支只判 `injected` 在不在，**不许**报「修好」。
	verdict := func(got, want, injected string) string {
		if injected != "" && strings.Contains(got, injected) {
			return "⚠ 越界：把上下文里的词塞进了原文"
		}
		if want == "" {
			return "— 无越界词（该臂不期待纠正）"
		}
		if strings.Contains(got, want) {
			return "✓ 修好"
		}
		return "— 原样"
	}

	for _, c := range topicTermCases() {
		offMeta := meetingMetaIn{}
		onMeta := meetingMetaIn{}
		if c.onTopic != "" {
			onMeta.Topic = c.onTopic
		}
		offText, offFailed := runArm(c, offMeta)
		onText, onFailed := runArm(c, onMeta)

		t.Logf("── %s", c.name)
		t.Logf("   转写：%s", c.transcript)
		if onMeta.Topic != "" {
			t.Logf("   ON 臂元数据：Topic=%q", onMeta.Topic)
		}
		if offFailed || onFailed {
			t.Logf("   ✗ 该样本有臂失败（计入分母）")
			failedArms++
			continue
		}
		t.Logf("   OFF：%s   ⇒ %s", offText, verdict(offText, c.want, c.injected))
		t.Logf("   ON ：%s   ⇒ %s", onText, verdict(onText, c.want, c.injected))
	}

	t.Log("")
	t.Log("═ 怎么读这组读数 ══")
	t.Log("  · S1 ON 优于 OFF ⇒ 主题确实带来了纠正（这条才是「有益」）")
	t.Log("  · S2 两臂的差异   ⇒ 模型非确定性的底噪；S1 的优势必须明显大于它")
	t.Log("  · S3 出现「越界」  ⇒ 主题有害，应把 ON 臂的 Topic 去掉")
	t.Logf("  · 本轮因网关抖动重试 %d 次（重试会同时抬高底噪）", retryCount)
	t.Log("")
	if failedArms > 0 {
		// ★ 这一条是本文件存在的理由：§11/§8/§17/§35 四次栽在
		//   「截断的跑被当成完整结果上报」。所以「有臂失败」必须是**显式结论**，
		//   不是一行日志 —— 底噪没测到时，S1 的方向利好**不可归因**。
		t.Logf("⚠⚠ 有 %d 个臂失败 ⇒ **噪声底噪未测到**", failedArms)
		t.Log("  ⇒ S1 即使显示「ON 修好 / OFF 原样」，也**不能**归因给主题：")
		t.Log("    底噪恰恰是「同一份输入在两臂给出不同结果」的那个量（§35）。")
		t.Log("  ⇒ 需要在网络稳定时重跑本探针，直到 S2 的两臂都成功。")
	} else {
		t.Log("✓ 三样本六臂全部成功 ⇒ 底噪已测到，可按上面的三条判读。")
	}
}
