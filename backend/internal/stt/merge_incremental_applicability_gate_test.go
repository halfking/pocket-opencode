// merge_incremental_applicability_gate_test.go — §485：**离线**门。
// `mergeIncremental` 能不能用来做「跨段校对」——先断它的**输入前提**。
//
// ── 为什么要有这道门 ─────────────────────────────────────────────────
//
// #15 的 B-3 写的是「整段重转结果与切片文本**做合并/校对**」。
// 而仓里**唯一**的跨段合并函数是 `mergeIncremental`（incremental.go:222）。
// 谁来实现 B-3，最自然的动作就是「把它接上」——
// **§485 实测证明这个动作是错的，而且两个方向都错**：
//
//	① 生产切段形态下它是 **no-op**（非重叠 ⇒ 逐字等于纯拼接）
//	② 有真实重复内容时它**误删甚至改坏文本**（4/4 组实测全中）
//
// ⚠ 为什么这道门**必须离线**：要验证 ① 只需要纯函数 + 真实语料真值，
//
//	要验证「SplitWAV 产出是否重叠」只需要一个本地 WAV ——
//	**全都不需要网关、不需要 ASR、不需要凭据**。
//	⇒ §474.4 吃过一次「只能在有凭据的环境里发现」的亏；
//	   这一条既然能离线，就不该挂在真网关探针上。
//
// ⚠ 这道门守的是「**工具的输入前提与生产输入是否匹配**」，
// 不是「去重算法准不准」（那个由 `dedup_threshold_sync_test.go` 守着）。
package stt

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"
)

// buildGateWAV 造一段带静音间隔的 16k/单声道/16bit WAV。
//
// ⚠ 为什么要自己造：`testdata/` 里的音频是 TTS 生成的 45 秒语料，
// 而本门要验的是**切分器产出区间的几何关系**，与内容无关。
// 一段 12 秒的合成波形更快、更确定，也让「第 2 段切在哪」可预期。
func buildGateWAV(sec float64, speechRuns [][2]float64) []byte {
	const sr = 16000
	n := int(sec * sr)
	pcm := make([]int16, n)
	for i := range pcm {
		t := float64(i) / sr
		for _, r := range speechRuns {
			if t >= r[0] && t < r[1] {
				// 低幅正弦：能量高到超过 silenceFloorSquared，但不模拟真实语音。
				pcm[i] = int16(3000 * math.Sin(2*math.Pi*220*t))
				break
			}
		}
	}
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)*2))
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&buf, binary.LittleEndian, uint32(sr))
	binary.Write(&buf, binary.LittleEndian, uint32(sr*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(2))    // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))   // bits
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)*2))
	for _, v := range pcm {
		binary.Write(&buf, binary.LittleEndian, v)
	}
	return buf.Bytes()
}

// TestSplitWAVProducesNonOverlappingSegments 断**前提**本身。
//
// ⭐ 这条是本门的承重墙：`mergeIncremental` 的设计前提是「相邻切片重叠」
// （incremental.go:19-21），而这里证明**生产切分器的产出恰好不重叠**。
// ⇒ 两个前提互斥，却共用「合并」这个词 —— 这就是 §485 的全部。
func TestSplitWAVProducesNonOverlappingSegments(t *testing.T) {
	// 12 秒：3 段 3 秒语音 + 1.5 秒静音，交替。
	wav := buildGateWAV(12, [][2]float64{{0, 3}, {4.5, 7.5}, {9, 12}})
	segments, ok := SplitWAV(wav, 4)
	if !ok {
		t.Fatal("SplitWAV 判定为不可切 ⇒ 本门失去意义（前提可能已变）")
	}
	if len(segments) < 2 {
		t.Fatalf("只切出 %d 段，验不了「相邻段是否重叠」", len(segments))
	}
	// 断言 1：严格递增且**首尾相接不交叠**。
	for i := 1; i < len(segments); i++ {
		prev, cur := segments[i-1], segments[i]
		if cur.StartSec < prev.EndSec {
			t.Errorf("段%d (%.2f–%.2f) 与段%d (%.2f–%.2f) **重叠了 %.2fs** —— "+
				"若这是有意改的（给切分加交叠以便去重），请先更新设计文档 §485，"+
				"因为 B-3 的整个前提都建立在「不重叠」上",
				i-1, prev.StartSec, prev.EndSec, i, cur.StartSec, cur.EndSec,
				prev.EndSec-cur.StartSec)
		}
		if cur.StartSec > prev.EndSec {
			t.Logf("  段%d→%d 之间有 %.2fs 间隔（静音被跳过，不是重叠）",
				i-1, i, cur.StartSec-prev.EndSec)
		}
	}
	// 断言 2：必须真的切出了多段 —— 否则「无重叠」是平凡成立的。
	t.Logf("切出 %d 段：", len(segments))
	for _, s := range segments {
		t.Logf("   段%d %.2f–%.2fs SilenceCut=%v 有用=%v",
			s.Index, s.StartSec, s.EndSec, s.SilenceCut, s.IsUsefulSegment())
	}
}

// TestMergeIncrementalIsNoOpOnNonOverlappingText 证明 ①。
//
// ⚠ 必须带**阴性对照**：否则「逐字相同」可能只是因为这个函数恒返回 a+b
// （那会让结论变成「它坏了」而不是「它不适用」）。
func TestMergeIncrementalIsNoOpOnNonOverlappingText(t *testing.T) {
	// 用 §481.2 造语料的真值分段（与 SplitWAV 实际切出的句群同形）。
	truthBytes, err := os.ReadFile("testdata/meeting-pauses.truth.txt")
	if err != nil {
		t.Fatal(err)
	}
	truth := strings.TrimSpace(string(truthBytes))
	parts := []string{
		"各位，今天我们开第三季度产品评审会。",
		"第一项议题是结算上线的验收标准，目前进度是百分之七十，剩下的百分之三十主要是联调和回归测试，预计下周三之前完成。",
		"第二项议题是预算，本季度预算是三百五十万，分三期付款，每期一百二十万，请财务确认现金流。",
		"第三项议题是人力，当前团队有八个人，缺口是两个后端。",
	}
	// 量具自证：这四段必须真的来自那份真值，否则本门在验别的文本。
	if joined := strings.Join(parts, ""); joined != truth {
		t.Fatalf("四段拼起来与 testdata 真值不一致（%d vs %d 字）—— 语料改了，先更新本门",
			len([]rune(joined)), len([]rune(truth)))
	}
	plain, merged := "", ""
	for i, p := range parts {
		got := mergeIncremental(merged, p)
		if got != merged+p {
			t.Errorf("第 %d 段上 mergeIncremental 做了非常规拼接：\n  期望 %q\n  实得 %q",
				i, merged+p, got)
		}
		merged, plain = got, plain+p
	}
	if merged != plain {
		t.Fatalf("非重叠段上它本应是 no-op：\n  纯拼接 %q\n  实得   %q", plain, merged)
	}
	t.Logf("非重叠 4 段 ⇒ 与纯拼接逐字相同（%d 字）⇒ 对 B-3 是 no-op", len([]rune(merged)))

	// 阴性对照：**真的**有重叠时它必须会动，否则上面是恒等函数而非「不适用」。
	a, b := "今天下午三点开项目评审会", "今天下午三点开项目评审，请准备材料"
	got := mergeIncremental(a, b)
	if got == a+b {
		t.Fatal("阴性对照失效：真重叠上它也没动 ⇒ 上面那条是恒等函数，不是「不适用」")
	}
	if got != "今天下午三点开项目评审会，请准备材料" {
		t.Errorf("阴性对照结果变了：%q", got)
	}
	t.Logf("阴性对照（真重叠）：%q ⇒ 它确实会干活 ⇒ 上面是**前提不匹配**不是**函数坏了**", got)
}

// TestMergeIncrementalLosesGenuineRepetition 证明 ②（**现状登记**，非承重门）。
//
// ⚠ 先读这段：这里登记的是**一个已知危害**，不是一条不变量。
// 「相邻段里的重复内容应当保留」是产品口径；本门只把**危害形状**钉住，
// 免得有人把 B-3 接上之后**以为它是对的**。
//
// 若将来给 `mergeIncremental` 增加了「区分切片重叠 vs 用户复述」的机制，
// 本门会转红 —— 那时请把它改成登记新现状，并回头更新 #15 的 B-3/B-5。
func TestMergeIncrementalLosesGenuineRepetition(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"整段重复", "我们今天讨论了预算和排期，还有人员安排要尽快定下来。",
			"我们今天讨论了预算和排期，还有人员安排要尽快定下来。"},
		{"明说「再说一遍」", "这个季度的预算是三百五十万，分三期付款，每期一百二十万。",
			"这个季度的预算是三百五十万。好，我再说一遍，分三期付款。"},
		{"明说「第二遍」", "进度是百分之七十，剩下的百分之三十主要是联调和回归测试。",
			"进度是百分之七十，剩下的百分之三十主要是联调和回归测试，这是第二遍了。"},
	}
	// 正控（量具自证）：真重叠上它**应该**去重 —— 若这里也不动，本门恒真。
	if mergeIncremental("今天下午三点开会", "今天下午三点开会，地点在二楼") == "今天下午三点开会今天下午三点开会，地点在二楼" {
		t.Fatal("正控失效：真重叠上它也没去重 ⇒ 下面的断言可能恒真")
	}
	lost := 0
	for _, c := range cases {
		got := mergeIncremental(c.a, c.b)
		if got == c.a+c.b {
			t.Errorf("%s：期望它会误删（这是本门登记的危害形状），实际原样保留了 —— "+
				"若这是有意修好的，请改成登记新现状并更新 #15", c.name)
			continue
		}
		lost++
		t.Logf("%s ⇒ 丢了 %d 字", c.name,
			len([]rune(c.a+c.b))-len([]rune(got)))
		t.Logf("   得 %q", got)
	}
	if lost == 0 {
		t.Fatal("一条都没复现危害 ⇒ 本门判据可能已失效，先查锚点再谈产品")
	}
	t.Logf("⇒ %d/%d 组复现「误删真实重复」。这就是 B-3 不能靠复用 mergeIncremental 解决的原因",
		lost, len(cases))
}
