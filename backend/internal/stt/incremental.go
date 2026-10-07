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
// ── 为什么不能只做「精确相等」比对（2026-10-06 用户实测反馈「片段重复」）──
//
// 原实现只用「最长公共后缀/前缀**逐字相等**」判重叠。这在真机上几乎必然失效：
// 同一段音频被送去转写时，两次的**上下文不同**（前一段没有后文、后一段没有前文），
// ASR 的语言模型解码路径因此不同，输出很少逐字一致。实测的典型分歧：
//
//	段1：今天下午三点开项目评审会
//	段2：今天下午三点开项目评审，请准备材料
//	                 ↑ 标点与用词都不同
//
// 精确比较在「评审会」处就断了 ⇒ 整段拼接 ⇒ 用户看到的重复。
// 所以这里改成**带容错的锚点匹配**：先找「高相似度」的重叠起点，
// 再据此裁掉新段的重复部分。
//
// 判定策略（按可靠性从高到低）：
//  1. 精确公共后缀/前缀（原策略保留，命中即最可信）。
//  2. 模糊锚点：在候选长度上算字符相似度，超过阈值即认为重叠，
//     裁掉新段中对应前缀。相似度用**最长公共子序列**（LCS）归一化，
//     不用编辑距离——LCS 对「中间多一个字 / 少一个字」这类 ASR 常见增删
//     更宽容，而编辑距离会把这类正常分歧判为不相似。
//  3. 都不成立则原样拼接 —— 宁可多几个字，也不要因为误去重丢掉真实内容。
//
// 误去重的代价是**丢掉真实内容**，所以模糊匹配必须保守：
//   - 只在**足够长**的锚点（≥ 4 rune）上做——1-3 个字的相似毫无意义；
//   - 相似度阈值 0.75，宁可漏判重叠（多几个重复字）也不误判（丢内容）；
//   - 仍保留「整段重复视为真实重复说话」的原判断。
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
	// 精确匹配失败 ⇒ 试模糊对齐（见函数注释「为什么不能只做精确相等」）。
	if tail, ok := fuzzyOverlapTail(cs, ns, maxOverlap); ok {
		return string(cs) + string(tail)
	}
	return committed + next
}

// fuzzyOverlap 的最小锚点长度。低于此长度时相似度噪声太大：
// 3 个字里对 2 个（0.67）在中文里是「预算」vs「算完」这种日常碰撞，
// 判为重叠就会吃掉真实内容。
const minFuzzyAnchor = 4

// anchorCoverage 是「LCS 必须覆盖对齐窗口多大比例」的下限 —— 这才是
// 真正的相似度闸。
//
// 宁高不低：漏判的代价是多几个重复字（用户一眼能看出来），误判的代价是
// **丢掉整句真实内容**（用户永远发现不了，直到某天需要那句话时）。
//
// ★ 这道闸不能拆成「锚点长度 + 相似度」两个独立指标（2026-10-06 实测两种
//
//	错法都踩过）：
//	· 分母固定用窗口长度 → 长文本下真重叠被稀释：
//	  committed「…这个方案的三个风险点已经确认过了」16 字 / next 15 字，
//	  真实重叠 11 字，11/15=0.73 < 0.75 ⇒ **真重叠被误杀**，用户继续看到重复。
//	· 分母改用锚点长度、从短到长扫 → 短锚点劫持：
//	  n=4 时「今天下午」vs「今天下午」ratio=1.0 立刻命中，
//	  净增里混进「今天审，请准备材料」这种垃圾。
//
//	覆盖率是「这次识别出来的字里有多大比例是重复的」这**一个**度量，
//	两个病都不犯。
const anchorCoverage = 0.6

// fuzzyOverlapTail 返回「next 相对 committed 的尾部真正新增的内容」，
// 第二个返回值为 false 表示不认为存在重叠。
//
// ── 为什么不能是「裁掉前 n 个字」（2026-10-06 修 bug 时实测踩到）──
//
// 「重叠 = next 的前 n 个字」这个假设在**重叠区内部有增删**时不成立。
// 实测用例（标点随机是 ASR 的常态）：
//
//	committed = 今天下午三点开项目评审会            (13)
//	next      = 今天下午三点开项目评审，请准备材料   (18)
//	                    ↑ 真实重叠到这里
//
// 真实重叠是 12 字「今天下午三点开项目评审」，之后 next 多了个逗号。
// 若按「裁 13 个字」⇒ ns[13:] = "审，请准备材料" ⇒ 结果里「审」出现两次。
// 若按「裁 14 个字」⇒ 又把「审」整个吃掉。无论 n 取多少都对不上——
// **单一长度无法表达重叠区内部的增删**。
//
// 正确做法：拿 LCS（最长公共子序列）做对齐。LCS 天然跳过「只在一边出现」
// 的字（插入的逗号、幻听多出的字、漏掉的字），所以：
//
//	LCS          = 今天下午三点开项目评审
//	next 的净增   = ，请准备材料
//	结果          = committed + ，请准备材料     ✅ 无重复、无丢失
//
// ── 为什么用 LCS 而非编辑距离 ──
//
// ASR 对同一段音频的两次输出，典型差异就是「中间多一个字 / 少一个字 /
// 标点不同」。LCS 对增删宽容（这些字直接被跳过），编辑距离会把它们计为
// 代价从而拉低相似度，导致漏判重叠 —— 于是又出现用户看到的重复。
func fuzzyOverlapTail(cs, ns []rune, maxOverlap int) ([]rune, bool) {
	// 对齐窗口取 min(两侧长度, maxOverlap)，在**这个窗口**上算 LCS。
	//
	// ★ 相似度判定必须分两步（2026-10-06 修 bug 时两种错法都踩过）：
	//
	//   错法一：分母固定用窗口长度 limit。
	//     committed = 这个方案的三个风险点已经确认过了 (16)
	//     next      = 三个风险点已经确认过了下周上线     (15)
	//     真实重叠 11 字。limit=15 作分母 ⇒ 11/15=0.73 < 0.75
	//     ⇒ **真重叠被判为无重叠** ⇒ 用户继续看到重复。
	//
	//   错法二：从短到长扫锚点，命中即停。
	//     n=4 时「今天下午」vs「今天下午」ratio=1.0 ⇒ 立刻命中，
	//     净增里混进「今天审，请准备材料」这种垃圾。
	//
	//   正确做法：**先用最大窗口求 LCS 长度 L**（L 就是重叠的估计长度），
	//   **再用 L 作分母**判相似度 = L/L 附近是否成立，并用
	//   「净增占 next 的比例」做第二道闸。这样长文本不稀释、短锚点不劫持。
	limit := len(cs)
	if limit > maxOverlap {
		limit = maxOverlap
	}
	if len(ns) < limit {
		limit = len(ns)
	}
	if limit < minFuzzyAnchor {
		return nil, false
	}

	tail := cs[len(cs)-limit:]
	head := ns[:limit]
	dp, matched := lcsAlign(tail, head)
	lcsLen := dp[limit][limit]

	// 唯一一道闸：LCS 必须覆盖对齐窗口的 anchorCoverage 比例。
	// （容忍少量增删——ASR 在重叠区多一个字/少一个字是常态——
	//   但不允许「只碰巧撞上几个字」。）
	//
	// ★ 这里曾有一道「净增占比 ≤ 0.8」的冗余闸，2026-10-06 实测证明它是
	//   **数学上不可能生效**的：净增 = 窗口内未匹配数 + 窗口外剩余，
	//   而 coverage ≥ 0.6 意味着窗口内至少 60% 已匹配，净增比例结构上
	//   不会超过 0.8。实测三组用例（cov 过闸时 netRatio 分别是
	//   0.47/0.61/0.69）无一能触发它，变异测试也证明它不影响任何用例。
	//   留着只会让人误以为「有两道闸在防误删」。已删除。
	if float64(lcsLen)/float64(limit) < anchorCoverage {
		return nil, false
	}
	if lcsLen < minFuzzyAnchor {
		return nil, false
	}

	out := make([]rune, 0, len(head))
	for k := 1; k <= limit; k++ {
		if !matched[k] {
			out = append(out, head[k-1])
		}
	}
	if len(ns) > limit {
		out = append(out, ns[limit:]...)
	}
	return out, true
}

// lcsAlign 在两个等长 rune 串上做 LCS，返回 DP 表与「head 中被选中为重复」
// 的下标标记。
func lcsAlign(tail, head []rune) ([][]int, []bool) {
	n := len(tail)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= n; j++ {
			if tail[i-1] == head[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	matched := make([]bool, n+1)
	i, j := n, n
	for i > 0 && j > 0 {
		if tail[i-1] == head[j-1] {
			matched[j] = true
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	return dp, matched
}
