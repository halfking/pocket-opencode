/**
 * native-part-timeline.ts — §956：原生分片的**会话时间轴**（纯函数，零 import、零 I/O）。
 *
 * ## 缺陷链（每一步都在生产代码里可核对）
 *
 * 1. `MeetingRecordService.loop()` 里 `long captureStart = System.currentTimeMillis();`
 *    是**每轮**重取的 ⇒ 原生服务**每启动一轮**，它的时间轴就从 0 重新开始。
 * 2. 中途切设备会**重启原生服务**（`switchDevice` → `start(..., {resume:true})`
 *    → `startBackgroundMic` → `ACTION_START` → `startCapture()` 先 `stopCapture()`）
 *    ⇒ 第二轮的 `startMs/endMs` **从 0 重新计数** ⇒ **时间轴回卷**。
 * 3. `recordingRuntime` 把 `part.startMs/part.endMs` 直接当**分段时间范围**用
 *    （`processSegment(blob, part.startMs, part.endMs)`）⇒ **回卷的时间戳进了库**。
 *
 * ## 回卷之后的三处下游损伤（本节要修的就是这三处）
 *
 * ① **顺序错乱**：`ingest-speech.ts` 按 `startMs` 有序插入分段 ⇒ 第二轮的分片
 *    （`startMs` 从 0 起）被插到**第一轮之前** ⇒ 后半场的话排在最前面。
 * ② **正文被裁**：`meeting-dedup.ts` 的 `dedupeStep` 在「下一段起点落在上一段结尾
 *    之前」时切到**精确裁剪档**（`useExact = overlapByTiming`）。而 §634/§683 实测
 *    结论是「会议链相邻段时间轴不重叠 ⇒ 该档在生产里**不可达**」⇒ **回卷把这条
 *    不变量打破了** ⇒ 一条此前不可达的、能**吃掉正文**的分支被打开。
 * ③ **说话人挂错人**：`attributeFullTranscript` 按时间重叠把整场重转的回执分桶
 *    ⇒ 回轮后的回执（音频坐标）与分段（墙钟坐标）差着一整轮 ⇒ 后半场的话
 *    挂到前半场的说话人名下。
 *
 * ## 修法：**原生只报事实，会话时间轴由 JS 侧算**
 *
 * 原生侧每片多带一个 `epochMs`（**本轮**采集原点的绝对墙钟，毫秒）⇒ JS 侧据此
 * 把任意一轮的相对时间**精确**重基到会话坐标：
 *
 * ```
 * sessionMs = (epochMs - baseEpochMs) + nativeRelativeMs
 * ```
 *
 * ⇒ 轮次之间的**真实空隙**（切换设备期间没录到音的那段墙钟）被**如实保留**，
 *   不需要 JS 墙钟参与（§955 的教训：要比/要算的数必须**同源**）。
 *
 * ⚠⚠ **为什么不在原生侧把锚点提成服务实例级**（那样相对时间本身就连续了）：
 *   那样修的话，JS 侧的重基就成了**永不触达**的分支 —— 于是「判据量到的」
 *   与「生产跑的」不是同一段（§955 的 M8 正是这么被逼出来的）。
 *   ⇒ 现在**由 JS 侧持有会话时间轴**：它在 node 里**能直接跑真件**、有行为判据；
 *   原生侧只负责如实上报「这一片相对本轮原点是什么时候、这一轮原点在什么时候」。
 *
 * ## 不变式（**本模块存在的全部理由**）
 *
 * 输出的时间轴恒满足：
 *
 * - **单调不减**：`startMs >= 上一片的 endMs`；
 * - **相邻不重叠**：`startMs >= prevEndMs` ⇒ `likelyAudioOverlap` 恒 false
 *   ⇒ ② 那条「能吃掉正文」的精确裁剪档**继续不可达**；
 * - **不倒退**：`endMs >= startMs`。
 *
 * ⇒ 即使拿不到 `epochMs`（旧原生包）或者收到**陈旧**的 epoch（比基准还早），
 *   本模块也会**钳位**（把倒退的部分拉回单调）⇒ 时间轴**永远不倒退**，
 *   代价是那一段变成**近似值**（`degraded`）⇒ 由调用方留痕。
 *
 * ## §957：把「切换空隙」变成一个**可测的量**
 *
 * §955 给护栏（§805）定的规则是「原生链切过设备 ⇒ 窗口不可信 ⇒ 判『量不到』」，
 * 理由是「原生时间轴**回卷** ⇒ 窗口是错基的」。
 * ⇒ **那个理由已经被本模块（§956）消灭了**：各轮已被精确拼回同一根轴。
 * ⇒ 于是护栏不必再把整场原生录音判成「量不到」——**切换空隙是可以量的**。
 *
 * ⚠⚠ **空隙只算「轮次之间」的那一段**（本模块的 `gapMs`）：
 *
 * | 现象 | 是不是空隙 | 判据 |
 * |---|---|---|
 * | 切设备 ⇒ 原生服务重启 ⇒ 两轮之间那段墙钟**没有音频** | ✅ **是**空隙 | 这一片的 `epochMs` 与**上一片**不同 |
 * | 同一轮里**丢了一片**（原生循环里片是连续的：`partStartRel = nowRel`） | ❌ **不是**空隙 | `epochMs` 没变，但时间戳跳了 ⇒ **丢片** |
 *
 * ⇒ 为什么必须分开：**空隙**是「本来就没录」（切设备那几秒没人在说话 / 服务在重启），
 *   护栏拿它去比音频就会**误报**「音频比墙钟短」；
 *   而**丢片**是「录了但没送到」⇒ 那是**真丢音频**，护栏**必须报出来**。
 * ⇒ ⇒ 若把两者混为一谈，护栏就会把真丢音频当成「切换空隙」放过去 ⇒ **护栏被废掉**。
 *
 * ⇒ 本模块因此同时给出两件事实：
 *   ① `gapMs`：**轮次之间**没有音频的墙钟（护栏要比的话要把它扣掉）；
 *   ② `exact`：这条时间轴**是否精确**（每片都带 epoch 且从未被钳位）
 *      —— 只有精确时，`gapMs` 才可信（否则分不清「空隙」与「丢片」）。
 */

/** 一片原生分片（**事实**，不是坐标）。 */
export interface NativeTimelinePart {
  /**
   * 本片相对**本轮**采集原点的起点（毫秒）。
   *
   * ⚠ 它是**轮内**相对量，不是会话坐标 —— 会话坐标由本模块算出来。
   */
  startMs: number
  /** 本片相对**本轮**采集原点的终点（毫秒）。 */
  endMs: number
  /**
   * 本轮采集原点的**绝对墙钟**（毫秒）—— 可选。
   *
   * ⚠ **可选是刻意的**：旧原生包不带这个字段 ⇒ 那时走「钳位保单调」的降级路
   *   （时间轴是近似的，但**不倒退**）。
   * ⚠ 非法值（NaN / ≤ 0）一律当「没有」⇒ 0 绝不能被当成 1970 年的原点。
   */
  epochMs?: number | null
}

/** 会话时间轴的状态（不可变 ⇒ 纯函数）。 */
export interface NativePartTimelineState {
  /**
   * 会话时间轴的原点：**第一片所属那一轮**的 epoch（毫秒）。
   * `null` = 还没见到带 epoch 的片。
   */
  baseEpochMs: number | null
  /** 已经发出过的最大结束时间（毫秒，会话坐标）。 */
  lastEndMs: number
  /**
   * 时间轴曾经**被迫钳位**过 ⇒ 它是**近似**的（粘性：一旦坏了就一直坏）。
   *
   * ⚠ 粘性是对的：已经按近似时间落库的分段改不回来了。
   */
  degraded: boolean
  /**
   * §957：**上一片**所属那一轮的 epoch（毫秒）—— 用来认出「轮次边界」。
   *
   * ⚠ 与 `baseEpochMs` **不是一回事**：base 是会话原点（第一片那轮），
   *   这个是「最近一片那轮」⇒ 两者不等 ⇒ **换了一轮**。
   */
  lastEpochMs: number | null
  /**
   * §957：累计的**轮次间空隙**（毫秒）—— 切换设备期间**没有音频**的那段墙钟。
   *
   * ⚠ 只累加**轮次边界**上的跳跃（见文件头那张表）：
   *   同一轮里的跳跃是「丢了一片」⇒ **不算**空隙（否则护栏会把真丢音频放过去）。
   */
  gapMs: number
  /**
   * §957：是否**每一片**都带 epoch（粘性：一旦有一片没带就一直不完整）。
   *
   * ⚠ 没有它就**分不清**「空隙」与「丢片」⇒ `gapMs` 不可信
   *   ⇒ 时间轴不算 `exact` ⇒ 护栏不拿它去比（宁可判「量不到」）。
   */
  allEpochsKnown: boolean
}

/** 一片分片重基之后的结果。 */
export interface NativePartTimelineResult {
  /** 会话坐标下的本片起点（毫秒）。 */
  startMs: number
  /** 会话坐标下的本片终点（毫秒）。 */
  endMs: number
  /** 本片之后的状态。 */
  state: NativePartTimelineState
  /** 本片是否被判定为「时间轴倒退」（原始坐标比上一片更早）。 */
  rolledBack: boolean
  /** 本片是否被**精确重基**（带 epoch，且本轮原点与基准不同）。 */
  rebased: boolean
  /** 本片是否被**钳位**（原始坐标会倒退 ⇒ 被拉回单调）。 */
  clamped: boolean
  /** §957：本片带来的**轮次间空隙**（毫秒）；同一轮里恒为 0。 */
  gapMs: number
  /** §957：本片是否**开启了新一轮**（这一片的 epoch 与上一片不同）。 */
  roundChanged: boolean
  /** §957：本片之后这条时间轴是否**精确**（可用来判护栏能不能比）。 */
  exact: boolean
}

/** 新一场录音的初始状态。 */
export function createNativePartTimelineState(): NativePartTimelineState {
  return {
    baseEpochMs: null,
    lastEndMs: 0,
    degraded: false,
    lastEpochMs: null,
    gapMs: 0,
    allEpochsKnown: true,
  }
}

/**
 * §957：这条会话时间轴**是否精确**（纯函数）。
 *
 * ⚠ 两个条件缺一不可：
 *   ① `allEpochsKnown`：**每一片**都带 epoch ⇒ 才认得出轮次边界
 *      ⇒ 才分得清「切换空隙」与「丢片」；
 *   ② `!degraded`：从未被钳位 ⇒ 时间轴没有被人为拉平过。
 *
 * ⇒ 不精确时，护栏**不得**拿这条时间轴的窗口去比音频（宁可判「量不到」）。
 */
export function isNativeTimelineExact(state: NativePartTimelineState): boolean {
  return state.allEpochsKnown && !state.degraded
}

/**
 * §956：把原生载荷里的采集原点读成**可选**的 epoch（**唯一**的实现）。
 *
 * ⚠⚠ **不能**写成 `Number(raw ?? 0)` —— 0 会被当成「1970 年的原点」
 *   ⇒ 第一片若带 0、后面几片带真实 epoch ⇒ 偏移是天文数字 ⇒ 整条时间轴废掉。
 * ⇒ 缺失 / 非法 / ≤0 一律 `undefined`（= 没有原点 ⇒ 走「钳位保单调」的降级路）。
 *
 * ⚠ **只认「数」与「数字串」**：`true` 被 `Number()` 折成 1 ⇒ 那是个合法 epoch
 *   （1 > 0）⇒ 后面几片的偏移就成了天文数字 ⇒ 所以这里**不**用 `Number()` 一把梭。
 *
 * ★ 放在**纯模块**里（而不是桥的 `.ts` 里）的理由：桥那个文件 import 了
 *   `@capacitor/core` ⇒ **node 里跑不到** ⇒ 放那儿就只能是源码扫描（零牙）。
 *   ⇒ 放这儿 ⇒ 「缺失的 epoch 不得被折成 0」这条**行为**判据在 node 里能跑。
 */
export function readNativeEpoch(raw: unknown): number | undefined {
  if (typeof raw === 'number') {
    return Number.isFinite(raw) && raw > 0 ? raw : undefined
  }
  if (typeof raw === 'string' && raw.trim() !== '') {
    const n = Number(raw)
    return Number.isFinite(n) && n > 0 ? n : undefined
  }
  return undefined
}

/** 内部用：把 epoch 读成「有 / 没有」两态。 */
function readEpoch(raw: unknown): number | null {
  return readNativeEpoch(raw) ?? null
}

/**
 * 把一片原生分片重基到**会话时间轴**。
 *
 * ⚠⚠ **本函数是纯函数**（不碰时钟、不碰文件、不改入参）⇒
 *   判据可以直接在 node 里喂真件形状跑（§955 的教训：判据量到的必须是真件）。
 *
 * 三条分支：
 *
 * ① **带 epoch** ⇒ 精确重基：`sessionMs = (epochMs - baseEpochMs) + nativeMs`
 *    ⇒ 轮次之间的真实空隙被如实保留（不掺 JS 墙钟）。
 * ② **不带 epoch**（旧原生包）⇒ 沿用轮内相对量（= 改动前的行为）。
 * ③ **两种都要过一道钳位**：任何情况下输出都不得与上一片重叠 ⇒
 *    §634/§683 那条「会议链相邻段不重叠」的不变量**继续成立**
 *    ⇒ 去重的精确裁剪档**继续不可达**。
 *
 * ⚠⚠ **§957 的第四条**：**空隙只算轮次边界上的跳跃**（`roundChanged`）
 *   —— 同一轮里的跳跃是「丢了一片」，不是空隙（见文件头那张表）。
 */
export function rebaseNativePartTimeline(
  state: NativePartTimelineState,
  part: NativeTimelinePart,
): NativePartTimelineResult {
  const prev = state
  let baseEpochMs = prev.baseEpochMs
  let rawStart = Number.isFinite(part.startMs) ? part.startMs : 0
  let rawEnd = Number.isFinite(part.endMs) ? part.endMs : rawStart
  if (rawEnd < rawStart) rawEnd = rawStart
  let rebased = false

  const epoch = readEpoch(part.epochMs)
  const hasEpoch = epoch !== null
  if (epoch !== null) {
    if (baseEpochMs === null) {
      // 第一片见到 epoch ⇒ 它就是会话时间轴的原点。
      baseEpochMs = epoch
    } else if (epoch !== baseEpochMs) {
      // 换了轮次 ⇒ 把这一轮的相对量平移到会话坐标。
      // ⚠ 平移量是**墙钟差**（两个 epoch 都是墙钟）⇒ 与音频同源，不掺 JS 墙钟。
      const offset = epoch - baseEpochMs
      rawStart += offset
      rawEnd += offset
      rebased = offset !== 0
    }
  }

  // ★§957：轮次边界 = 这一片的 epoch 与**上一片**不同（不是与基准不同）。
  //   ⚠ 与基准不同只说明「不是第一轮」；与**上一片**不同才说明「**刚刚**换了轮」
  //   ⇒ 只有后者才对应「两轮之间那段没有音频的墙钟」。
  const roundChanged = hasEpoch && prev.lastEpochMs !== null && epoch !== prev.lastEpochMs

  // 「倒退」的判据是**原始坐标**比上一片更早（重基前）。
  const rolledBack = rawStart < prev.lastEndMs

  // ★ 钳位：输出恒不与上一片重叠、恒不倒退。
  const startMs = Math.max(rawStart, prev.lastEndMs)
  const endMs = Math.max(rawEnd, startMs)
  const clamped = startMs !== rawStart || endMs !== rawEnd

  // ★§957：空隙 = **轮次之间**那段没有音频的墙钟。
  //   ⚠⚠ 同一轮里的跳跃**不算**空隙：原生循环里片是连续的
  //     （`partStartRel = nowRel`）⇒ 同一轮里跳一下 = **丢了一片**
  //     ⇒ 那是真丢音频 ⇒ 必须让护栏报出来，不能当成「空隙」放过去。
  const jumpMs = Math.max(0, startMs - prev.lastEndMs)
  const gapMs = roundChanged ? jumpMs : 0

  const degraded = prev.degraded || clamped
  const allEpochsKnown = prev.allEpochsKnown && hasEpoch

  const next: NativePartTimelineState = {
    baseEpochMs,
    lastEndMs: endMs,
    // ⚠ 粘性：坏了就一直坏（已经落库的近似时间改不回来）。
    degraded,
    lastEpochMs: hasEpoch ? epoch : prev.lastEpochMs,
    gapMs: prev.gapMs + gapMs,
    allEpochsKnown,
  }
  return {
    startMs,
    endMs,
    state: next,
    rolledBack,
    rebased,
    clamped,
    gapMs,
    roundChanged,
    exact: isNativeTimelineExact(next),
  }
}

/**
 * 时间轴退化的留痕文案（**只给开发者 console**，§813 的口径）。
 *
 * ⚠ 为什么单独抽成函数：文案也是判据的一部分 ⇒ 判据能直接断言它说了什么
 *   （而不是去 `console.warn` 的调用点做源码匹配 —— 那种判据零牙）。
 */
export function describeNativeTimelineDegradation(
  result: NativePartTimelineResult,
  part: NativeTimelinePart,
): string {
  const why = result.rebased
    ? '换轮次后的时间戳比基准更早（陈旧的采集原点）'
    : '原生分片的时间戳倒退了（切设备后原生服务重启 ⇒ 轮内相对量从 0 重新开始）'
  return (
    `[meeting-recorder] 原生分片时间轴退化：${why}`
    + `（本片原始 ${part.startMs}~${part.endMs}ms，已钳位到 `
    + `${result.startMs}~${result.endMs}ms）`
    + ' ⇒ 该段时间轴是近似值，已保证单调不重叠（相邻段仍不重叠 ⇒ 不会被去重裁掉正文）'
  )
}
