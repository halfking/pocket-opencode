/**
 * useLiveSummary — 会议实时摘要 + 推荐，节流更新
 */
import { ref, watch, unref, type MaybeRef, type Ref } from 'vue'
import { meetingsApi, toLiveSummary } from '../api/meetings'
import { isAbortError } from '../api/http'
import { updateMeeting, type LiveSummary, type MeetingSegment, type RecommendItem } from '../features/meetings/meetings-store'
import { topicShift } from '../features/meetings/topic-change'
import { dedupeSegments } from '../features/meetings/meeting-dedup.ts'
import { mergeSummaryReferences } from '../features/meetings/meeting-summary-merge.ts'
import { shouldRefreshSummary, SUMMARY_INTERVAL_MS } from '../features/meetings/live-summary-gate.ts'
import { selectSummarySegments, selectRecommendSegments } from '../features/meetings/live-summary-payload.ts'
import { nextRefreshDelay, LIVE_SUMMARY_DEBOUNCE_MS } from '../features/meetings/live-summary-schedule.ts'


export function useLiveSummary(
  meetingId: MaybeRef<string>,
  segments: Ref<MeetingSegment[]>,
  opts?: {
    onUpdated?: (summary: LiveSummary) => void
    meta?: Ref<{ title?: string; participants?: string[]; location?: string }>
  },
) {
  const liveSummary = ref<LiveSummary | null>(null)
  const recommendations = ref<RecommendItem[]>([])
  /**
   * §223：true = 最近一轮的推荐是「没拿到」而不是「没有」。
   *
   * ⚠ 面板只在 `recommendations.length === 0` 时读它 ⇒ 已有好结果时它
   *   永远不会误报。这是有意的：**宁可少说一句，不可说错一句**（§30）。
   */
  const recommendFallback = ref(false)
  const isUpdating = ref(false)
  const lastSegmentCount = ref(0)
  /**
   * §924：已经并入**当前摘要**的段 id。
   *
   * ⚠ 为什么需要它：服务端 `buildSummaryPrompt` 的契约是
   *   「已有摘要 + **新增转写**」⇒ 请求体里的 segments 本该是「这一轮新增的部分」。
   *   而这里此前送的是**整场会议**（每 30 秒一次）⇒ 请求体与提示词都 O(会议时长)，
   *   一场会的累计流量是 O(时长²)；且服务端那条一次性 chat 路径不截断转写
   *   ⇒ 提示词随会议变长 ⇒ 45 秒预算被撑爆 ⇒ 回落浏览器内 32000 **字节**闸
   *   ⇒ §916 的「裁尾」让长会议上的摘要**只剩最后那 37 分钟**，且不报错。
   *
   * ⚠ 按 **id** 记，不按下标：`segments` 会被整份替换（`seedSegments`），
   *   按下标切会在替换后切到新数组的中间 ⇒ 前半段被静默跳过。
   */
  let sentSegmentIds: string[] = []
  /**
   * §924：这份状态属于**哪一场**会议。
   *
   * ⚠ 同一个 composable 实例会换会议：`useSessionLiveRecord` 的
   *   `meetingId.value = m.id`（同一会话里第二次开麦）⇒ 不隔离的话，
   *   上一场的摘要会作为「已有摘要」被带进新会议，
   *   而新会议的第一份摘要就成了「在旧摘要上更新」的产物。
   */
  let stateMeetingId = ''
  let throttleTimer: ReturnType<typeof setTimeout> | null = null
  let lastUpdateAt = 0
  /** ⚠ §424：上一次**分段变化**的时刻。debounce 的 ceiling 必须相对它算，
   *  否则 ceiling 会被「刚重排过」这件事自己抵消掉。 */
  let lastSegmentAt = 0

  async function refresh(force = false) {
    const id = unref(meetingId)
    if (!id) return
    // ⚠ §924：换场先把上一场的状态**全部**作废。
    //   这一步必须在「有没有段」的早退**之前** —— 否则新会议还没攒够段的
    //   那段时间里，界面显示的还是上一场的摘要（`liveSummary` 没被清）。
    if (id !== stateMeetingId) {
      stateMeetingId = id
      sentSegmentIds = []
      lastSegmentCount.value = 0
      lastUpdateAt = 0
      liveSummary.value = null
      recommendations.value = []
      recommendFallback.value = false
    }
    if (segments.value.length === 0) return
    // ⚠ §424 在飞守卫：**重排而不是跳过**。
    //   跳过不可接受 —— 会议最后一段到达后可能再没有新分段，
    //   丢掉这一次刷新就等于尾段永远进不了摘要。
    if (isUpdating.value) {
      scheduleRefresh()
      return
    }
    const last = segments.value[segments.value.length - 1]
    const topicChanged = topicShift(
      liveSummary.value?.keyPoints?.[0] ?? liveSummary.value?.summary,
      last?.text ?? '',
    )
    const newCount = segments.value.length - lastSegmentCount.value
    const elapsed = Date.now() - lastUpdateAt
    // ⚠ §418：闸的语义在纯函数 `shouldRefreshSummary` 里，且**有行为门钉住**
    //   （四个触发条件各自单独成立、都不成立、force 绕过）。
    if (!shouldRefreshSummary({ force, topicChanged, newCount, elapsedMs: elapsed })) return

    isUpdating.value = true
    // ★ 喂给 LLM 的 segments 必须先去重（2026-10-06）。
    //   切片重叠会让相邻段产生重复文本（「今天今天下午三点」），若原样送进
    //   /summary 与 /recommend，重复会出现在摘要、关键点、行动项里，
    //   还会让下面的 topicShift 主题漂移判断失准。
    //   去重后的副本不改 startMs —— 时间戳是真实采集的，不该被篡改。
    const cleanSegments = dedupeSegments(segments.value)
    // ⚠⚠ §924：服务端契约是「已有摘要 + **新增转写**」⇒ 本轮只送**新增的那几段**。
    //   此前送的是整场会议（每 30 秒一次）⇒ 请求体/提示词 O(会议时长)、
    //   累计 O(时长²)；长会议上服务端那条一次性 chat 路径的 45 秒预算会被撑爆
    //   ⇒ 回落浏览器内 32000 字节闸 ⇒ 摘要静默退化成「最后 37 分钟」。
    const selection = selectSummarySegments(cleanSegments, sentSegmentIds)
    const payloadSegments = selection.segments
    try {
      const result = await meetingsApi.summarize(
        id,
        payloadSegments,
        liveSummary.value?.summary,
        opts?.meta?.value,
      )
      // ⚠ §402：references **不能**跟着整体覆盖走。
      //   服务端提示词明写「宁可没有 references 也不要编造」⇒ 某一轮转写里
      //   没有值得回溯的话题时，返回空是**预期行为**。而 `toLiveSummary`
      //   会把它原样带过来，于是上一轮引用到的笔记被无声抹掉，
      //   界面的 `v-if="references.length"` 整块消失，降级提示也不出现
      //   （判据是 `!references.length && agent !== true`，而那是一次**成功**的摘要）。
      //   下方 recommendations 的分支早就是「空结果不覆盖」，这里补齐同一原则。
      liveSummary.value = {
        ...toLiveSummary(result),
        references: mergeSummaryReferences(liveSummary.value?.references, result.references),
      }
      lastSegmentCount.value = segments.value.length
      lastUpdateAt = Date.now()
      // ⚠ §924：本轮送出去的段已经「并入摘要」了 ⇒ 记下水位线。
      //   记在**摘要拿到之后**：`meetingsApi.summarize` 内部会吞掉失败并回落到
      //   浏览器内兜底（内容仍然被算作已并入），只有**中止**会抛出来。
      //   ⇒ 中止时不记 ⇒ 下一轮把同一批段重发 ⇒ **不丢内容**。
      sentSegmentIds = selection.sentIds

      await updateMeeting(id, { liveSummary: liveSummary.value, summary: result.summary })
      opts?.onUpdated?.(liveSummary.value)

      // ⚠ §924：推荐这条送**有界的尾部**（服务端自己会截到 2000 个渲染码元，
      //   它的注释写明「把预算花在最新的若干段上就够了，summary 已经承载了全局信息」）。
      //   送整场会议 ⇒ 中间那些段**每轮白传一遍**（O(时长²) 的另一半来源）。
      //   尾部取「≥ 服务端截断量」⇒ 服务端拼出来的提示词**逐字不变**。
      const recs = await meetingsApi.recommend(id, selectRecommendSegments(cleanSegments), result.summary)
      if (recs.items.length > 0) {
        recommendations.value = recs.items
        // 拿到东西 ⇒ 之前那次「没拿到」的说法就该撤销（§30：宁可少说一句）。
        recommendFallback.value = false
        await updateMeeting(id, { recommendations: recs.items })
      } else {
        // ⚠ 空结果**不覆盖**已有推荐（一轮抖动不该抹掉此前的好结果），
        //   但要记住「这一次没拿到」—— 只有在推荐**真的空**时它才会被读到，
        //   所以留着旧值不会误报。
        recommendFallback.value = recs.fallback
      }
    } catch (e) {
      // 中止不打 warn：那是用户（或上层换场）主动停的，不是故障。
      if (isAbortError(e)) return
      console.warn('[live-summary] update failed:', e)
    } finally {
      isUpdating.value = false
    }
  }

  function scheduleRefresh() {
    if (throttleTimer) clearTimeout(throttleTimer)
    // ⚠ §424：原来这里是硬编码 `setTimeout(() => refresh(), 2000)`，
    //   那是**纯 debounce**（尾沿、无上限）：分段间隔 < 2000ms 时
    //   定时器整场都不会到期 ⇒ 即时总结直接停摆（实跑：10 分钟会议全场刷新 1 次）。
    //   而文件头写的是「节流更新」。ceiling 取既有的 SUMMARY_INTERVAL_MS，
    //   **不引入新的产品常量** —— shouldRefreshSummary 里那个
    //   `elapsedMs >= intervalMs` 分支本来就写着「≥30s 必须更新」，
    //   是这层 debounce 把它变成了不可达代码。
    const { delayMs } = nextRefreshDelay({
      sinceSegmentMs: Date.now() - lastSegmentAt,
      sinceRefreshMs: Date.now() - lastUpdateAt,
      inFlight: isUpdating.value,
      debounceMs: LIVE_SUMMARY_DEBOUNCE_MS,
      ceilingMs: SUMMARY_INTERVAL_MS,
    })
    throttleTimer = setTimeout(() => refresh(), delayMs)
  }

  watch(segments, () => {
    lastSegmentAt = Date.now()
    scheduleRefresh()
  }, { deep: true })

  return { liveSummary, recommendations, recommendFallback, isUpdating, refresh }
}
