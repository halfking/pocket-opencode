// Package stt 的「即时转写」：把录音切成短段后**立刻**逐段转写并产出增量文本。
//
// 为什么需要一个和 TranscribeFull 并列的形态（2026-10-01）：
//
// 调研结论是「便宜的 ASR 都不支持流式」：OpenRouter 转写端点不支持 SSE
// （上游约 60 秒超时），MiniMax/智谱虽支持 SSE 但一请求就得 0.38-0.50 美元/小时
// 且限 500 秒/30 秒。也就是说，**真流式与低价在当前市场不可兼得**。
//
// 但「即时出字」是产品要求（用户录音时希望看到文字在长出来）。这两件事并不冲突：
// 真流式要求的是「一个连接内逐字下发」，而**分段增量**能提供的是「每 3-15 秒
// 冒出一段新文字」。对用户而言，后者已经满足「即时」的产品诉求，而且：
//   - 成本不变（同样按秒计费，段数不影响单价）
//   - 延迟可控（最长等待 = 一段的转写耗时）
//   - 不依赖上游是否支持 SSE（对 6 个候选模型全部可用）
//
// 所以这里的形态是：**短段 + 滑动 + 增量下发**。前端每收到一段就追加到
// 即时文本区，停止时再用 TranscribeFull 做一次全量校正。
//
// 关键设计：短段必须**在静音处切**且带**重叠**，否则句首字词会丢：
// 硬切在词中间，两侧各丢一半信息。所以每段尾部留 overlapSec 秒与下一段重叠，
// 聚合时对重叠区做去重（见 mergeIncremental）。
package stt

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// 即时转写的默认参数。
const (
	// IncrementalSegmentSec 是单段目标时长。取 8 秒是权衡结果：
	// 更小则请求数暴涨（30 分钟会议 = 225 段，按每段固定开销算很贵），
	// 更大则用户等待首个字的时长变长（用户要的是「马上看到」）。
	IncrementalSegmentSec = 8.0

	// IncrementalOverlapSec 是相邻段的重叠时长。1 秒足够覆盖中文一个短语的
	// 长度，同时不会让总转写时长膨胀太多（30 分钟会议多算约 3%）。
	IncrementalOverlapSec = 1.0

	// IncrementalSilenceSec 是「结束一段」所需的静音时长。
	// 比全量转写短（那边用 1.5 秒），因为即时转写要更快出字。
	IncrementalSilenceSec = 700
)

// IncrementalChunk 是一次即时转写的输入切片（调用方做完 VAD 切分后逐个送入）。
type IncrementalChunk struct {
	Audio    []byte
	StartSec float64
	EndSec   float64
	// SilenceCut 为真表示这个切片尾部带静音（调用方确实在停顿处切的），
	// 为假说明是强制时间切片（连续讲话被硬切），聚合时要更保守地去重。
	SilenceCut bool
}

// IncrementalResult 是一次即时转写的增量结果。
type IncrementalResult struct {
	Text     string  `json:"text"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	// Delta 相对上一次调用新增的文本（已去掉与前文重复的部分）。
	// 前端用这个做「只追加不重排」的增量更新。
	Delta string `json:"delta"`
	// IsFinal 为真表示这是本次会话最后一段。
	IsFinal bool `json:"isFinal,omitempty"`
	// Model/Channel/Cost 让前端能显示「这段是谁转的、花了多少」。
	Model     string  `json:"model,omitempty"`
	Channel   string  `json:"channel,omitempty"`
	CostCents float64 `json:"costCents,omitempty"`
	// Error 非空表示本段失败。**非致命**：调用方应保留已有文本继续推进，
	// 不要因为一段失败就丢掉此前的即时文字。
	Error string `json:"error,omitempty"`
}

// IncrementalTranscriber 做「会话式」即时转写。
//
// 为什么是「会话」而不是无状态函数：增量去重必须知道**此前已经产出过什么**。
// 无状态就意味着每次都要把历史文本重新传一遍、或者放弃去重（于是每段边界
// 的重复文字会在界面上累积成「今天今天下午三点」）。所以这里显式持有会话状态。
//
// 并发约定：单个实例**不是**并发安全的（内部有共享的累积文本）。
// 调用方需自行串行化，或为每个录音会话各建一个实例（推荐后者——
// 两个录音会话本就不该共享去重状态）。
type IncrementalTranscriber struct {
	tr *Transcriber

	mu        sync.Mutex
	emitted   string // 已下发给客户端的累积文本（用于算 delta）
	committed string // 已确认为最终文本的前缀（用于跨段去重）
}

// NewIncrementalTranscriber 基于一个 Transcriber 开会话式即时转写。
func NewIncrementalTranscriber(tr *Transcriber) *IncrementalTranscriber {
	return &IncrementalTranscriber{tr: tr}
}

// Rebind 换掉底层引擎并返回自身，用于链式调用。
//
// 为什么需要它：即时转写的会话状态（累积文本）必须跨请求保留，但**引擎必须
// 每次请求重新构造**——用户可能中途改了 STT 设置（换模型/换通道），若沿用
// 旧引擎，去重会拿旧模型的结果去消解新模型的输出。
func (i *IncrementalTranscriber) Rebind(tr *Transcriber) *IncrementalTranscriber {
	if i == nil {
		return nil
	}
	i.tr = tr
	return i
}

// Reset 清空会话状态（开始新的录音会话时调用）。
func (i *IncrementalTranscriber) Reset() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.emitted = ""
	i.committed = ""
}

// TranscribeChunk 转写一个切片并返回增量结果。
//
// 参数 scope 用于解析该用户的目标；isFinal 标记是否为最后一段。
func (i *IncrementalTranscriber) TranscribeChunk(
	ctx context.Context, scope Scope, chunk IncrementalChunk, isFinal bool,
) (*IncrementalResult, error) {
	if i == nil || i.tr == nil {
		return nil, fmt.Errorf("stt engine not configured")
	}
	if len(chunk.Audio) == 0 {
		// 空切片不算错误——VAD 在纯静音时会产出空段。返回空增量让调用方继续。
		//
		// Text 仍回填当前累积文本：前端做的是「用返回的 text 覆盖显示」，
		// 若这里返回空串，录音中一段纯静音就会把已经出字的即时文本清空。
		i.mu.Lock()
		defer i.mu.Unlock()
		return &IncrementalResult{
			Text: i.emitted, StartSec: chunk.StartSec, EndSec: chunk.EndSec, IsFinal: isFinal,
		}, nil
	}

	// 短切片直接走单次转写：再套一层切分只会得到 1 段，白走逻辑。
	res, err := i.tr.TranscribeFor(ctx, scope, chunk.Audio, "chunk.wav")
	if err != nil {
		// 段失败**不**上抛为整会话失败：调用方要的是「继续出字」，
		// 一段失败只意味着这一段没文字。错误带在结果里。
		//
		// 同样要回填已有文本：否则一段网络抖动会让界面上的即时文字消失。
		i.mu.Lock()
		defer i.mu.Unlock()
		return &IncrementalResult{
			Text:     i.emitted,
			StartSec: chunk.StartSec, EndSec: chunk.EndSec, IsFinal: isFinal,
			Error: firstLine(err),
		}, nil
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	full := mergeIncremental(i.committed, res.Text)
	delta := strings.TrimPrefix(full, i.emitted)
	i.emitted = full
	i.committed = full

	return &IncrementalResult{
		Text: full, Delta: delta,
		StartSec: chunk.StartSec, EndSec: chunk.EndSec, IsFinal: isFinal,
		Model: res.Model, Channel: res.Channel, CostCents: res.CostCents,
	}, nil
}

// mergeIncremental 把新一段文本接到已有文本后，并去掉两段重叠的重复内容。
//
// 为什么需要去重：切片有 1 秒重叠，ASR 对重叠区的两次识别结果基本一致。
// 直接拼接会得到「今天今天下午三点开项目评审会」这种明显错误。
//
// 判定策略（按可靠性从高到低）：
//  1. 最长公共后缀/前缀：上一段结尾 N 字符 == 新段开头 N 字符 → 去掉新段这部分。
//     这是最可靠的，因为重叠区在两次识别里几乎逐字相同。
//  2. 逐步缩短：N 从较长往较短试，第一个成立的即为切点。设上限 40 字符，
//     避免极长重复被误判（那通常是真的重复说话）。
//  3. 都不成立则原样拼接 —— 宁可多几个字，也不要因为误去重丢掉真实内容。
func mergeIncremental(committed, next string) string {
	next = strings.TrimSpace(next)
	if committed == "" {
		return next
	}
	if next == "" {
		return committed
	}

	// 必须按 rune 切，不能按字节。
	//
	// 中文一个字符占 3 字节（UTF-8），按字节的尾部/前缀比较会在字节层面错位：
	// committed 末尾 3 字节可能是「甲」的后两字节，next 开头 3 字节可能是
	// 另一个字（如「乙」）的前一字节——两者碰巧相等就会误判为重叠，
	// 于是把真实的下一个字吞掉。实测表现是「甲」接「乙」丢了「乙」。
	cs := []rune(committed)
	ns := []rune(next)

	maxOverlap := 40
	limit := len(cs)
	if limit > maxOverlap {
		limit = maxOverlap
	}
	if len(ns) < limit {
		limit = len(ns)
	}
	// 从长到短试，第一个成立的即为切点。
	//
	// 下限 2 个 rune 而不是 1：单个字相同的概率太高（「我」「好」这类高频字
	// 在段边界出现两次完全正常），按 1 字符判定会把大量真实内容误判成重叠。
	//
	// 「完全相同」要区别对待：若 next 整段就是 committed 的后缀（或反之），
	// 说明这是切片重叠把同一段话又转了一遍，去重是对的；但若双方都很长
	// 且**整段重复**，更可能是用户真的重复说了同一句话，保留两份才对。
	// 判据是重叠长度占整段的比例：切片重叠最多 1 秒音频（几十个 rune），
	// 不可能覆盖整段；整段重复则比例接近 1。
	if string(ns) == string(cs) && len(ns) > maxOverlap {
		return committed + next
	}
	for n := limit; n >= 2; n-- {
		if string(cs[len(cs)-n:]) == string(ns[:n]) {
			return string(cs) + string(ns[n:])
		}
	}
	return committed + next
}
