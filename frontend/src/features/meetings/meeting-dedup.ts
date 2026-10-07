/**
 * meeting-dedup.ts — 相邻语音段之间的**重叠去重**（前端侧）。
 *
 * ── 为什么需要它（2026-10-06）──
 *
 * 用户实测反馈「录音片段重复」。追查发现真正的链路是：
 *
 *   VadSegmenter.finalizeSegment() 按 [speechStartMs-sliceMs, endMs] 取
 *   sliceBuffer 里的音频片段 → 每段独立 POST /stt/transcribe →
 *   ingestSpeechBlob 把结果**原样**存成一条 MeetingSegment →
 *   updateTranscript 用 '\n' 拼起来。
 *
 * ★ 也就是说：**整条前端链路没有任何去重**。后端 stt/incremental.go 里的
 *   mergeIncremental 写了一套去重，但前端从来没调用 IncrementalTranscriber
 *   ——它走的是逐段 sttApi.transcribe。所以那次修的是一条没人走的路径。
 *
 * 重复是怎么产生的（VadSegmenter 逐行核对后确认）：
 *   - sliceBuffer 是**累积队列**，只按 atMs >= cutoff(60s前) 滚动；
 *   - finalizeSegment 的取片窗口是 [speechStartMs - sliceMs, endMs]，
 *     其中 sliceMs(250ms) 是为了补偿 MediaRecorder 的缓冲延迟；
 *   - speechStartMs 来自 requestAnimationFrame 的能量判定。RAF 在后台标签页
 *     会被节流到 1fps 以上，语音起点的判定会**回退**；
 *   - 一旦相邻两段的取片窗口重叠，同一段音频会被转写两次，
 *     文本就变成「今天今天下午三点」。
 *
 * 这与讯飞听见/Otter 的做法一致：切片之间本来就要留重叠余量来避免断词，
 * 代价是必须在**文本侧**消解掉重叠。FunASR 官方部署矩阵里的两阶段模式
 * 同样是先流式再整段修正，而两阶段之间也必须做这一步融合。
 *
 * ── 为什么不用后端那份实现 ──
 *
 * 后端 stt/incremental.go 的 mergeIncremental 与本文件是同一套算法
 * （LCS 对齐 + anchorCoverage 闸），但 Go 与 TS 两地维护同一套阈值
 * 容易漂移。之所以仍写一份而不是共用：跨语言无法直接复用，且后端那份
 * 目前的调用方是 IncrementalTranscriber（保留给将来的真流式通道）。
 * 两边的阈值常量在注释里互相标注，改一处必须同步另一处。
 */

/**
 * LCS 覆盖率下限：这次识别出来的字里有多大比例是重复的。
 *
 * 取 0.6 的理由与后端一致（stt/incremental.go 的 anchorCoverage）：
 *   - 调低（0.5）会把「共享常用词的两句话」误判成重叠而吃掉真实内容；
 *   - 调高（0.7）会让真实的切片重叠因一两个字的差异漏判，重复照旧。
 * 中文短段的重叠区通常十几个字，容忍 1-2 个字增删对应 0.85+ 的覆盖率，
 * 留 0.6 是给 ASR 幻听留余量。
 */
const ANCHOR_COVERAGE = 0.6

const MAX_OVERLAP = 40

/**
 * 把新一段文本接到已有文本后，消解两段的重叠。
 *
 * 与后端 mergeIncremental 同契约：
 *   - 误去重的代价是**丢掉真实内容**（用户永远发现不了）；
 *   - 漏去重的代价是多几个重复字（用户一眼看见）。
 *   所以所有阈值偏保守，宁可少裁。
 */
export function dedupeSegmentText(committed: string, next: string): string {
  const n = next.trim()
  if (!committed) return n
  if (!n) return committed

  // 1) 精确公共后缀/前缀（原后端策略，命中即最可信）
  const cs = Array.from(committed)
  const ns = Array.from(n)
  const limit = Math.min(MAX_OVERLAP, cs.length, ns.length)
  for (let k = limit; k >= 2; k--) {
    if (cs.slice(cs.length - k).join('') === ns.slice(0, k).join('')) {
      return cs.join('') + ns.slice(k).join('')
    }
  }

  // 2) LCS 对齐消解（重叠区内部有标点/增删时必需）
  const netNew = lcsNetNew(cs, ns)
  if (netNew !== null) return cs.join('') + netNew

  // 3) 判为不重叠，原样拼接 —— 宁可多几个字，也不要丢真实内容
  return committed + n
}

/**
 * LCS 对齐求 next 相对 committed 的净增后缀；不构成重叠时返回 null。
 *
 * 只看 committed 尾部与 next 头部的窗口，不做全文对齐：
 * 切片重叠必然出现在边界，在文中任意位置找重叠反而会误删
 * 「用户真的重复说的话」。
 */
function lcsNetNew(cs: string[], ns: string[]): string | null {
  const limit = Math.min(MAX_OVERLAP, cs.length, ns.length)
  if (limit < 2) return null // 比 2 还短的对齐窗口无意义（单个字不能算重叠）
  const tail = cs.slice(cs.length - limit)
  const head = ns.slice(0, limit)

  const n = limit
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(n + 1).fill(0))
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= n; j++) {
      if (tail[i - 1] === head[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1
      else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1])
    }
  }
  // 唯一的闸：覆盖率。它同时承担了「LCS 太短」和「只是碰巧撞上几个字」
  // 两种拒绝——L=2 时覆盖率最多 2/n，远低于阈值，不必再单独判长度。
  //
  // ★ 此前还有一道 MIN_ANCHOR=4 的闸，2026-10-06 变异测试证明它是
  //   **冗余的**：只把 MIN_ANCHOR 放宽到 1 而 coverage 保持 0.6 时，
  //   15 条用例全绿（coverage 闸先拦住了）；只有两道同时放宽才会红。
  //   它只是 coverage 的一次快速短路，没有独立作用，已删除。
  const lcsLen = dp[n][n]
  if (lcsLen / n < ANCHOR_COVERAGE) return null

  // 回溯标记 head 中被 LCS 选为重复的下标，其余是净增。
  // 单次回溯到底，保证 matched 集合自洽（LCS 可能有多种最优解）。
  const matched = new Array<boolean>(n + 1).fill(false)
  let i = n
  let j = n
  while (i > 0 && j > 0) {
    if (tail[i - 1] === head[j - 1]) {
      matched[j] = true
      i--
      j--
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      i--
    } else {
      j--
    }
  }
  const out: string[] = []
  for (let k = 1; k <= n; k++) if (!matched[k]) out.push(head[k - 1])
  // 窗口外的 next 尾部一定是新内容。
  if (ns.length > limit) out.push(...ns.slice(limit))
  return out.join('')
}

/**
 * 对一段会议转写做**相邻去重**，返回可安全用于展示的文本。
 *
 * 与 dedupeSegmentText 的区别：这里处理的是「已经排好序的全部段落」，
 * 用于生成 transcript 全文；相邻两段之间的重叠都要消解。
 *
 * ⚠️ 只消解**相邻**两段：更远的段落即便相似也是用户真的重复说了。
 */
export function dedupeTranscriptParagraphs(texts: string[]): string {
  if (texts.length <= 1) return texts.join('')
  let out = texts[0] ?? ''
  for (let k = 1; k < texts.length; k++) {
    out = dedupeSegmentText(out, texts[k] ?? '')
  }
  return out
}

/**
 * 返回**去重后**的 segments 副本，供 LLM 消费（摘要 / 推荐 / 待办）。
 *
 * ── 为什么需要它（2026-10-06）──
 *
 * 重复文本不只污染「转写全文」，还会顺着 segments 流进 LLM：
 * `useLiveSummary` 把 segments 直接 POST 给 `/summary` 与 `/recommend`，
 * 于是「今天今天下午三点」这种重复会出现在摘要、关键点、行动项里，
 * 还会让 topicShift 的主题漂移判断失准。
 * 用户看到的「质量不行」不只是转写那一行，是整条内容链路。
 *
 * 返回**新对象**而不是原地改：segments 是 ref 里的响应式数组，
 * 原地改会触发无谓的 watch / 重新渲染。
 *
 * 只消解**相邻**两段：更远的相似段落是用户真的重复说了。
 * `startMs` 保持不变——时间戳是真实采集的，不该被去重逻辑篡改。
 */
export function dedupeSegments<T extends { text: string }>(segments: T[]): T[] {
  if (segments.length <= 1) return segments.slice()
  const out: T[] = []
  let prevBody = ''
  for (const s of segments) {
    const raw = s.text ?? ''
    if (!prevBody || !raw) {
      out.push(raw === prevBody ? s : { ...s, text: raw })
      if (raw) prevBody = raw
      continue
    }
    const body = dedupeSegmentText(prevBody, raw)
    // 净增 = 本段在去重后新增的内容。
    const delta = body.length > prevBody.length ? body.slice(prevBody.length) : raw
    out.push(delta === raw ? s : { ...s, text: delta })
    prevBody = body
  }
  return out
}

/**
 * 把段落列表渲染成 transcript 全文，**相邻段之间做重叠去重**。
 *
 * 为什么逐段保留 `[说话人]` 前缀、而不是先合并全文再拼前缀：
 * 去重要在**正文**层面做（重叠的音对应的是同一个说话人），但说话人标签
 * 必须留在它自己那一段上。所以逐段处理：对每段，与「已输出的累积正文」
 * 比较，裁掉重复部分，只追加净增。
 *
 * 只与**累积正文**比较是安全的：dedupeSegmentText 只看尾/头窗口，
 * 不会回头改写已输出的内容。
 */
export function renderTranscript(segments: Array<{ speakerLabel: string | null; text: string }>): string {
  const lines: string[] = []
  let prevBody = ''
  for (const s of segments) {
    const raw = s.text ?? ''
    // 首段无前文可对齐；其余与累积正文去重。
    const body = prevBody ? dedupeSegmentText(prevBody, raw) : raw
    // 净增部分 = 本段在去重后新增的内容。
    const delta = body.length > prevBody.length ? body.slice(prevBody.length) : raw
    lines.push(`[${s.speakerLabel}] ${delta}`)
    prevBody = body
  }
  return lines.join('\n')
}
