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
  let throttleTimer: ReturnType<typeof setTimeout> | null = null
  let lastUpdateAt = 0
  /** ⚠ §424：上一次**分段变化**的时刻。debounce 的 ceiling 必须相对它算，
   *  否则 ceiling 会被「刚重排过」这件事自己抵消掉。 */
  let lastSegmentAt = 0

  async function refresh(force = false) {
    const id = unref(meetingId)
    if (!id || segments.value.length === 0) return
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
    try {
      const result = await meetingsApi.summarize(
        id,
        cleanSegments,
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

      await updateMeeting(id, { liveSummary: liveSummary.value, summary: result.summary })
      opts?.onUpdated?.(liveSummary.value)

      const recs = await meetingsApi.recommend(id, cleanSegments, result.summary)
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
