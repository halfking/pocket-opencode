// Package stt 的「长音频切分 + 全量转写聚合」。
//
// 为什么必须有这个文件（2026-10-01）：
//
// 原始需求是「完成即时转换与全量转换的服务能力」。而「全量」这件事在调研之后
// 有了硬约束：**没有任何一家 ASR 允许无限长音频单次上传**。
//
//	MiniMax asr-1.0     ≤500 秒 / 50 MB
//	智谱 glm-asr-2512   ≤30 秒 / 25 MB，仅 wav/mp3
//	OpenRouter          上游约 60 秒超时 / 25 MB multipart
//	OpenAI              ≤25 MB（v2 API 另有更严的时长限制）
//
// 所以「把整场两小时会议丢给上游」从来就不是一个能工作的请求。全量转写的正确
// 形状是：**切段 → 逐段转写 → 按序聚合**，并且聚合结果必须能让用户看出
// 「哪段失败、哪段缺了」，否则就是静默丢内容。
//
// 切分算法选「按静音边界切」而不是「按固定 25 秒硬切」：
//   - 固定长度切会把词和句子劈成两半，两段的转写文本在拼接处各丢一半信息，
//     且中文尤其明显（常见量级的代价是每段 1-3 个字的错字）。
//   - 按静音切，段边界落在「停顿」上，ASR 的语言模型不会被打断。
//
// 同时**保留原格式**：切分只在能解析的容器上做（WAV/裸 PCM），其余格式
// （webm/opus、mp4/aac、mp3）不做瞎猜式重封装——WebM 的 cluster 边界不可
// 靠字节偏移推断，硬切会得到无法解码的碎片。此时如实告诉调用方「该格式
// 不支持自动切分，请改用 WAV」，而不是给一段错乱文本。
package stt

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Segment 是一段被切出来的音频及其在原始音频里的时间位置。
type Segment struct {
	Audio    []byte
	StartSec float64
	EndSec   float64
	Index    int
	Total    int
	// SilenceCut 为真表示这一段的边界落在静音处（而非硬切）。
	SilenceCut bool
}

// FullResult 是一次全量转写的聚合结果。
type FullResult struct {
	Text string `json:"text"`
	// Segments 是逐段明细，用于把「哪段失败」如实呈给用户。
	Segments []SegmentResult `json:"segments"`
	// DurationMS 是原始音频总时长。
	DurationMS int64 `json:"durationMs,omitempty"`
	// Failed 段数 > 0 时为 true。此时 Text 仍然是「成功段 + 失败占位」，
	// 不返回空——空会让上层以为整段没内容，从而静默清空用户的会议记录。
	Failed int `json:"failed,omitempty"`
	// Succeeded 是成功转写出文本的段数。
	//
	// 为什么必须有它：失败段会在 Text 里留「［第 N 段转写失败］」占位，
	// 于是 Text 永远非空，上层无法用「文本是否为空」区分「转写成功」与
	// 「全部段失败」。Succeeded==0 就是「这次没产出任何内容」的硬信号。
	Succeeded int     `json:"succeeded"`
	CostCents float64 `json:"costCents,omitempty"`
	Model     string  `json:"model,omitempty"`
	Channel   string  `json:"channel,omitempty"`
	Label     string  `json:"label,omitempty"`
}

// SegmentResult 是单段的转写结果。
type SegmentResult struct {
	Index    int     `json:"index"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	Text     string  `json:"text,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// 切分参数。
const (
	// defaultSegmentSec 是目标段长。取 25 秒是三方约束的交集：
	// 智谱限 30 秒（留 5 秒余量），OpenRouter 限约 60 秒，MiniMax 限 500 秒。
	// 再小则请求数暴涨（两小时会议 = 288 段），再大则撞智谱上限。
	defaultSegmentSec = 25

	// maxSegmentSec 是硬上限。超过此值强制切段，哪怕没找到静音点——
	// 持续讲话（没有静音）的会议录音必须能切，否则整个请求会失败。
	maxSegmentSec = 25

	// minSilenceMS 是「连续静音达到多久才算一个切点」。
	//
	// 为什么从 300ms 提到 800ms（2026-10-01 真引擎 + 真语音实测）：
	// 300ms 这个门槛会把**句内停顿**当成切点。对着 79 秒会议语料把静音分布
	// 打出来，分布是干净可分的：
	//
	//	句间停顿   1575 – 3475 ms
	//	句内停顿    100 –  600 ms   ← 逗号、换气、词组之间的自然停顿
	//
	// 用 300ms 门槛时，语料里 30 多处句内停顿全部成了候选切点：同一份 79 秒
	// 语料在均方门槛修好之后，300ms 切出 14 段、800ms 切出 10 段，而 10 段
	// 才是与句子边界对齐的那个。碎切不只是慢 —— 每段都要付一次上游调用与
	// 计费，而且句子照样被劈开。
	//
	// 800ms 不是从 TTS 语料上硬凑出来的数字：真人会议里句内换气也在
	// 300–700ms 区间，800ms 留了余量；而真达不到 800ms 的边界会退化成
	// 25 秒硬切，那条路径本来就是为「持续讲话无停顿」准备的。
	minSilenceMS = 800

	// silenceFloor 是静音的能量上界（0-1 RMS）。低于它算静音。
	silenceFloor = 0.012

	// silenceFloorSquared 是上面那个门槛的**平方**。
	//
	// 为什么必须显式写出来：frameRMS 省掉了 sqrt（只返回均方），主循环里
	// 拿它直接和 silenceFloor 比。两者口径不一致，等效门槛被放大到
	// sqrt(0.012) ≈ 0.11 —— 比设计意图松了整整 10 倍。后果是几乎所有换气、
	// 词组之间的自然停顿都被判成静音，79 秒会议语料被切成 16 段
	// （平均 4.9 秒），句子照样被劈开，而且每段都要付一次上游调用。
	//
	// 写死一个平方常量而不是在热循环里算乘法，是为了让「均方 vs 均方根」
	// 这个坑无法再被无声地带回来。
	silenceFloorSquared = silenceFloor * silenceFloor

	// minSilenceCutSec 是「允许在静音处切段」的最短累计时长。
	//
	// 为什么需要它（2026-10-01 真引擎实测发现）：原先的规则是「已过目标长度
	// 75% 才允许在静音处切」，兜底是到目标长度硬切。对会议录音这种**长句**
	// 场景，这两条规则会联手把句子从中间劈开：
	//
	//	句子 A 占 0–11s，停顿 0.9s，句子 B 占 12–22.7s
	//	→ 0–11s 时长度不足 18.75s，不切
	//	→ 18.43s 时（B 句中间）长度够了，于是**在 B 句中间硬切**
	//
	// 实测后果：79 秒会议录音被切成 5 段，其中一段把「越积越多，影响后续的
	// 开发效率」劈成「…技术债会越来越多」+「这些机会多」——两段各自都识别
	// 得不差，拼接处却各丢一半信息。同一份音频、同一引擎、同一时刻的前后
	// 对照：全局 CER_norm 8.9% → 5.5%，CER_strict 18.5% → 14.7%。
	//
	// CER 改善是「实打实但不夸张」的量级；更大的价值是结构性的 ——
	// 修复后 9 句话**每一句都完整落在单个返回段里**（覆盖率 100%、零丢失、
	// 零重复），修复前至少 3 句被跨段劈开。下游做会议纪要、抽待办时，
	// 句子是否完整比聚合 CER 几个百分点更要命。
	//
	// 取 5 秒是权衡：再小会让段数暴涨（每段一次上游调用、一次计费），
	// 再大又会把长句拦在门外。5 秒足以让绝大多数中文短句完整落在段内。
	//
	// 注意它**不覆盖** maxSegmentSec：目标段长被压得很小（某些上游限制）
	// 时仍按原来的 75% 走，避免造出一堆碎片段。
	minSilenceCutSec = 5
)

// minUsefulSegmentSec 是「值得送去转写」的段长下限。
//
// 短于此的段几乎全是静音：转写必然返回空文本，等于白白花一次上游调用
// （最便宜档也按秒计费），还会在聚合文本里留下空行。
//
// 关键区别：这里**不是**「不切」，而是「切出来但在 TranscribeFull 里跳过」。
// 早期实现选择不切，结果是 segStart 停在原地、后一轮又重复判定，
// 最终既丢了音频又算错段数——切分必须无损，静音过滤必须无损地进行。
const minUsefulSegmentSec = 1.0

// IsUsefulSegment 报告这一段是否值得送去转写。
func (s Segment) IsUsefulSegment() bool {
	return s.EndSec-s.StartSec >= minUsefulSegmentSec
}

// SplitWAV 按静音边界把 PCM WAV 切成不超过 maxSegmentSec 的若干段。
//
// 支持 16-bit PCM（bit depth 16）与 8-bit，这是浏览器 MediaRecorder 与
// Android AudioRecord 的实际输出范围。其他位深/压缩格式返回 ok=false，
// 调用方应如实上报「不支持切分」而不是硬切。
func SplitWAV(data []byte, maxSegmentSec float64) ([]Segment, bool) {
	if maxSegmentSec <= 0 {
		maxSegmentSec = defaultSegmentSec
	}
	payload, sampleRate, bitsPerSample, channels, dataLenAt, ok := parsePCMWAV(data)
	if !ok {
		return nil, false
	}
	if bitsPerSample != 16 {
		// 8-bit 是无符号偏移格式，判定静音的阈值口径不同，不在这里猜。
		return nil, false
	}
	bytesPerFrame := channels * 2
	if bytesPerFrame <= 0 {
		return nil, false
	}
	frameCount := len(payload) / bytesPerFrame
	if frameCount == 0 {
		return nil, false
	}
	targetFrames := int(maxSegmentSec * float64(sampleRate))
	if targetFrames < 1 {
		targetFrames = 1
	}

	var segments []Segment
	segStart := 0
	silenceRun := 0
	// 最近一个可切点：静音窗口的中点。硬切到静音里比切到静音边缘更居中。
	cutCandidate := -1

	flush := func(end int, silenceCut bool) {
		if end <= segStart {
			return
		}
		seg := Segment{
			Audio:      buildWAV(data, dataLenAt, payload[segStart*bytesPerFrame:end*bytesPerFrame]),
			StartSec:   float64(segStart) / float64(sampleRate),
			EndSec:     float64(end) / float64(sampleRate),
			Index:      len(segments),
			SilenceCut: silenceCut,
		}
		segments = append(segments, seg)
		segStart = end
	}

	// 静音判定必须用**滑动窗口 RMS**，不能用单帧振幅。
	//
	// 单帧的失败场景是真实存在的：正弦音（测试音、纯音乐前奏、很轻的说话声）
	// 在每个周期过零点时振幅≈0，逐帧看就是「静音-有声-静音」交替，
	// 累积 300ms 就会在过零区误切出一段 0.15s 的碎片——切出来的是纯静音，
	// 转写必然返回空文本，白白消耗一次上游调用并在聚合结果里留一个空段。
	// 窗口 RMS 不会被单个过零样本带偏。
	// 窗口取 25ms 而不是更短：必须**覆盖至少一个完整基频周期**，
	// 否则纯音（基频常在 100-300Hz，周期 3-10ms）的窗口 RMS 仍会周期性
	// 跌破阈值，被误判成静音。实测 10ms 窗口对 8ms 周期的正弦仍会误切，
	// 25ms 能覆盖到 40Hz，远低于任何语音基频。
	windowFrames := sampleRate / 40 // 25ms
	if windowFrames < 1 {
		windowFrames = 1
	}
	var winSum float64
	for frame := 0; frame < frameCount; frame++ {
		winSum += frameRMS(payload, frame*bytesPerFrame, channels)
		// 滑出窗口。expired 帧号用 max(0, …) 兜住，否则 frame-windowFrames
		// 为负时乘以 bytesPerFrame 会得到负偏移，切片直接 panic。
		expired := frame - windowFrames
		if expired < 0 {
			expired = 0
		}
		if frame >= windowFrames {
			winSum -= frameRMS(payload, expired*bytesPerFrame, channels)
		}
		span := windowFrames
		if frame+1 < windowFrames {
			span = frame + 1
		}
		rms := winSum / float64(span)
		// 注意与 silenceFloorSquared 比：frameRMS 返回的是均方，不是均方根。
		if rms < silenceFloorSquared {
			silenceRun++
			// 切点**持续更新**到当前静音窗口的中点，而不是只在「恰好等于阈值」
			// 那一帧设一次。
			//
			// 只设一次的后果：长静音（会议里的长停顿）会把切点钉在静音的
			// 最开头，于是下一次 flush 切出「段头紧跟一大段静音」，
			// 而紧跟它的那一段可能短到只有几十毫秒（纯静音，转写必然返回空
			// 文本，白花一次上游调用并在聚合结果里留一个空段）。
			// 持续更新保证切点始终落在「离当前最远的那个静音」上。
			if silenceRun >= minSilenceMS*sampleRate/1000 {
				cutCandidate = frame - silenceRun/2
			}
		} else {
			silenceRun = 0
			cutCandidate = -1
		}

		length := frame - segStart
		// 允许在静音处切段的最短累计长度。目标段长本身很小时（上游限 4-6 秒）
		// 退回原来的 75% 口径，否则会切出一堆碎片段。
		minCutFrames := minSilenceCutSec * sampleRate
		if target := targetFrames * 3 / 4; minCutFrames > target {
			minCutFrames = target
		}
		switch {
		case cutCandidate > segStart && length >= minCutFrames:
			// 已有一定长度且遇到静音点 → 在静音处切。
			flush(cutCandidate, true)
			silenceRun = 0
			cutCandidate = -1
		case length >= targetFrames:
			// 到目标长度但还没遇到静音（连续讲话）→ 硬切，保证不撞上游上限。
			flush(frame, false)
			silenceRun = 0
			cutCandidate = -1
		}
	}
	flush(frameCount, false)

	for i := range segments {
		segments[i].Total = len(segments)
	}
	if len(segments) == 0 {
		return nil, false
	}
	return segments, true
}

// frameRMS 算一帧的均方根振幅（0-1）。只用第一声道判断静音：多声道录音里
// 两个声道通常同源，逐声道判定没有额外信息量却让热路径翻倍。
func frameRMS(payload []byte, offset, channels int) float64 {
	if offset+2 > len(payload) {
		return 1 // 读不到数据时当作「有声」，宁可多切一段
	}
	sample := int16(binary.LittleEndian.Uint16(payload[offset:]))
	v := float64(sample) / 32768.0
	return v * v // 省掉 sqrt：只需要与 silenceFloor 的平方比较
}

// parsePCMWAV 拆出 data 段的载荷、格式参数，以及 data 段长度字段的字节偏移
// （dataLenFieldAt 供 buildWAV 原位改写长度用）。
func parsePCMWAV(data []byte) (payload []byte, sampleRate, bits, channels, dataLenFieldAt int, ok bool) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, 0, 0, false
	}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if id == "fmt " && body+16 <= len(data) {
			format := binary.LittleEndian.Uint16(data[body : body+2])
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			// 1 = PCM，3 = IEEE float（后者不按 16-bit 解析）
			if format != 1 {
				return nil, 0, 0, 0, 0, false
			}
		}
		if id == "data" {
			end := body + size
			if size <= 0 || end > len(data) {
				end = len(data) // 部分封装器写错 data size，容忍到文件尾
			}
			return data[body:end], sampleRate, bits, channels, pos + 4, true
		}
		pos = body + size + (size % 2)
	}
	return nil, 0, 0, 0, 0, false
}

// buildWAV 用原始 RIFF 头 + 新的 data 段载荷拼出一个合法 WAV。
//
// 复用原头保证 sampleRate/channels/byteRate 全部与原始一致——手写常量头
// 是这类切分代码最常见的 bug 来源（切出来的段与原音频参数不符，上游直接拒收）。
//
// **两个长度字段都必须原位改写**，少任何一个都会产出「声明长度 ≠ 实际长度」
// 的坏 WAV（症状是解析器读出一截垃圾采样，或直接判定文件损坏）：
//  1. 偏移 4：RIFF 块总长度 = 文件总长 - 8
//
// effectiveSegmentSec 算这次全量重转的实际切段长度。
//
// ★ 抽成纯函数不是为了整洁，是为了**让「有效值被钳制」这件事可测**：
//
//	能力表里登记的 MaxSeconds（30 / 60 / 500 / 600）与真正生效的段长
//	是**两件事**——最后一律被 maxSegmentSec 压回 25 秒。
//	只看登记表会以为「MiniMax 能一次吃 500 秒」，而实际是 25。
//
// ⚠ §121：25 的来源是**智谱一家 30 秒限制**（defaultSegmentSec 的注释自己
//
//	写了「覆盖智谱 30 秒这个最紧的约束」），却成了所有人的约束。
//	后果有两个：说话人分离在结构上拿不到跨块一致的身份；
//	60 分钟会议的请求数被放大 2.4~24 倍（25 秒/块 = 144 次 vs 600 秒/块 = 6 次）。
//
//	**本轮刻意不改**：调大段长的反向代价是「一次上游失败丢一大段文字」，
//	而短块的优点正是单次失败只丢几秒 ⇒ 这是真正的取舍，标成待拍板（§121.3）。
//	测试文件里有一条**只读现状登记**（full_segment_clamp_status_test.go），
//	它不是承重门：改段长时请连同 §121 的决策一起改，不要单点改。
func effectiveSegmentSec(model string) float64 {
	// 先按目标的单次上限决定切分长度；目标没登记上限时用全局默认 25 秒。
	segmentSec := float64(defaultSegmentSec)
	if limit := KnownMaxSeconds(model); limit > 0 && float64(limit) < segmentSec {
		segmentSec = float64(limit)
	}
	if segmentSec > maxSegmentSec {
		segmentSec = maxSegmentSec
	}
	return segmentSec
}

// 2. dataLenFieldAt：data 段载荷长度
func buildWAV(original []byte, dataLenFieldAt int, payload []byte) []byte {
	out := make([]byte, 0, dataLenFieldAt+4+len(payload))
	out = append(out, original[:dataLenFieldAt+4]...)

	// 先改 RIFF 块总长（偏移 4，在已复制的头部范围内）。
	//
	// RIFF 规范：这里的值是「从 'WAVE' 标签开始到文件末尾」的字节数，
	// 即 **文件总长 − 8**，不是文件总长本身。
	//
	// 写成文件总长会让每个切出来的段都比实际大 8 字节。宽容的解码器
	// （PyAV / ffmpeg）会默默截掉末尾 8 字节当作填充，**看起来一切正常**；
	// 严格的上游 API 则直接拒收整段。2026-10-01 实测就是先被宽容解码器
	// 吞掉，直到逐段比对文件头才发现。
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)+len(payload)-8))

	// 再改 data 段长度。
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(payload)))
	copy(out[dataLenFieldAt:dataLenFieldAt+4], size[:])

	out = append(out, payload...)
	return out
}

// TranscribeFull 做一次全量转写：能切就切段逐段转写并聚合，不能切就整段转写。
//
// 逐段失败**不中断**整体：两小时会议的第 37 段因网络抖动失败，不应该让
// 前 36 段和后 120 段的成果一起消失。失败段在聚合文本里留一个显式占位，
// 并在 Failed 计数与 Segments 明细里如实标记。
func (t *Transcriber) TranscribeFull(ctx context.Context, scope Scope, audio []byte, filename string) (*FullResult, error) {
	if t == nil || t.resolve == nil {
		return nil, fmt.Errorf("stt engine not configured")
	}
	target, err := t.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("stt not configured: no transcription target resolved")
	}

	segmentSec := effectiveSegmentSec(target.Model)

	segments, splittable := SplitWAV(audio, segmentSec)
	if !splittable {
		// 不可切（webm/mp4/mp3 或非 16-bit PCM）→ 整段走原有单次转写。
		// 这是既有能力，不算降级。
		res, err := t.transcribeFor(ctx, scope, audio, filename, true /* forcePlain: §120 本路径不读 Segments */)
		if err != nil {
			return nil, err
		}
		return &FullResult{
			Text: res.Text, Model: res.Model, Channel: res.Channel, Label: res.Label,
			CostCents: res.CostCents, DurationMS: res.DurationMS,
			Segments: []SegmentResult{{
				Index: 0, StartSec: 0, EndSec: float64(res.DurationMS) / 1000, Text: res.Text,
			}},
		}, nil
	}

	out := &FullResult{Model: target.Model, Channel: target.Channel, Label: target.Label}
	var texts []string
	for _, seg := range segments {
		// 跳过纯静音的碎段：不送上游（省一次计费调用），但**不丢音频**——
		// 切分本身保持无损，音频仍在 segments 里可供排查。
		// 只记一条明细，text 留空，这样前端能看出「这里本来就是静音」。
		if !seg.IsUsefulSegment() {
			out.Segments = append(out.Segments, SegmentResult{
				Index: seg.Index, StartSec: seg.StartSec, EndSec: seg.EndSec,
			})
			continue
		}
		// 逐段独立超时：不能让一段挂死把整个全量转写拖到客户端超时。
		segCtx, cancel := context.WithTimeout(ctx, segmentTimeout)
		res, err := t.transcribeFor(segCtx, scope, seg.Audio, filename, true /* forcePlain: §120 本路径不读 Segments */)
		cancel()

		item := SegmentResult{
			Index: seg.Index, StartSec: seg.StartSec, EndSec: seg.EndSec,
		}
		if err != nil {
			item.Error = firstLine(err)
			out.Failed++
		} else {
			item.Text = res.Text
			out.Succeeded++
			out.CostCents += res.CostCents
			if res.DurationMS > 0 {
				out.DurationMS += res.DurationMS
			}
		}
		out.Segments = append(out.Segments, item)
		if item.Text != "" {
			texts = append(texts, item.Text)
		} else if item.Error != "" {
			texts = append(texts, fmt.Sprintf("［第 %d 段转写失败：%s］", seg.Index+1, item.Error))
		}
	}
	if out.DurationMS == 0 {
		if secs, ok := wavDurationSeconds(audio); ok {
			out.DurationMS = int64(secs * 1000)
		}
	}
	out.Text = strings.Join(texts, "\n")
	// 判据是「成功段数」而不是「Text 是否为空」——失败占位符会让 Text 永远
	// 非空，用 TrimSpace(Text) 判断会导致**全部段失败也返回成功**，
	// 上层拿到的是一段全是「［第 N 段转写失败］」的文本，会当成会议内容存库。
	// 一次成功段都没有 = 这次全量转写没有产生任何内容，必须报错。
	if out.Succeeded == 0 {
		// 必须带上**第一段的具体错误**。
		//
		// 原来只报「N 段全部失败」，但每段明细都留在了返回值里、随 error
		// 一起被丢掉 —— 调用方只看到段数，完全无法判断是「上游 401」还是
		// 「切出来的 WAV 非法」。2026-10-01 黑盒实测就卡在这里：5/5 段失败，
		// 日志与响应里都没有任何线索。
		first := ""
		for _, s := range out.Segments {
			if s.Error != "" {
				first = s.Error
				break
			}
		}
		if first == "" {
			first = "（无逐段错误，全部段被判定为纯静音而跳过）"
		}
		return nil, fmt.Errorf("stt %s: 全量转写没有任何有效文本（%d 段全部失败，首段错误：%s）",
			target.Model, out.Failed, first)
	}
	return out, nil
}

// segmentTimeout 是单段转写的超时上限。
//
// 为什么单段要有独立上限：Transcriber 的整体超时是 120 秒，但全量转写要串行
// 跑 N 段。用整体 ctx 的话，第一段慢就会让后面所有段一起超时。逐段独立计时
// 换来的是「最坏情况 N × 90s」，由调用方的 HTTP 超时兜底——这是刻意的取舍：
// 宁可让上层看到明确的上游超时，也不要静默丢段。
const segmentTimeout = 90 * time.Second

// ToneDurationSeconds 返回任意 PCM WAV 的时长（秒），供调用方核算。
//
// 导出的原因：切分后需要确认「发给上游的每一段都在服务上限内」——这是可测
// 的硬约束（智谱 30 秒）。原先只有未导出的 wavDurationSeconds，调用方拿不到。
func ToneDurationSeconds(data []byte) (float64, bool) {
	return wavDurationSeconds(data)
}

// KnownMaxSeconds 返回预置模型的单次时长上限（秒），未知返回 0。
func KnownMaxSeconds(model string) int {
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.MaxSeconds
		}
	}
	return 0
}

// SupportsStreaming 报告预置模型是否支持服务端真流式。
func SupportsStreaming(model string) bool {
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.Streaming
		}
	}
	return false
}

// SupportsDiarization 报告预置模型是否支持服务端说话人分离。
func SupportsDiarization(model string) bool {
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.Diarization
		}
	}
	return false
}

// KnownDiarizationMaxSeconds 返回「开启分离后的」时长上限（秒）。
// 未公布返回 0（= 按不限制处理，与 MaxSeconds 的口径一致）。
func KnownDiarizationMaxSeconds(model string) int {
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, model) {
			return o.DiarizationMaxSeconds
		}
	}
	return 0
}

// ShouldRequestDiarization 决定这一次转写要不要请求说话人分离。
//
// ★ 为什么这个判定必须存在，而不是「模型支持就一律开」——
//
// 微软官方文档明写 MAI-Transcribe 的分离只支持较短录音：约 15 分钟及以上
// 会返回 408/500/503（diarization_unavailable），而同一段音频**关掉分离
// 就能转成功**。也就是说，对长录音硬开分离不是「少拿一个字段」，
// 是**把整段转写变成失败**。
//
// 而本项目的主场景就是会议——超过 15 分钟的会议是常态。
// 若无脑开，用户会得到「短会议正常、长会议整段失败」这种极难自查的现象。
//
// 判定用「音频时长」而不是「请求条数」：上游限制的是**单次请求的音频长度**，
// 而 TranscribeFull 会把长录音切成若干段分别送（每段都远小于上限），
// 此时每段请求都「够短」，判定自然放行——这正是我们想要的行为。
//
// durationSec <= 0 表示**时长未知**（非 WAV、无法解析头）。此时保守起见
// 不开分离：宁可少拿说话人标签，也不要用一个未知长度的请求去赌上游会不会 503。
func ShouldRequestDiarization(model string, durationSec float64) bool {
	if !SupportsDiarization(model) {
		return false
	}
	if durationSec <= 0 {
		return false
	}
	if max := KnownDiarizationMaxSeconds(model); max > 0 && durationSec > float64(max) {
		return false
	}
	return true
}
