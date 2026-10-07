/**
 * useLiveSummary — 会议实时摘要 + 推荐，节流更新
 */
import { ref, watch, unref, type MaybeRef, type Ref } from 'vue'
import { meetingsApi, toLiveSummary } from '../api/meetings'
import { isAbortError } from '../api/http'
import { updateMeeting, type LiveSummary, type MeetingSegment, type RecommendItem } from '../features/meetings/meetings-store'
import { topicShift } from '../features/meetings/topic-change'
import { dedupeSegments } from '../features/meetings/meeting-dedup.ts'

const SUMMARY_INTERVAL_MS = 30_000
const SUMMARY_SEGMENT_THRESHOLD = 3

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
  const isUpdating = ref(false)
  const lastSegmentCount = ref(0)
  let throttleTimer: ReturnType<typeof setTimeout> | null = null
  let lastUpdateAt = 0

  async function refresh(force = false) {
    const id = unref(meetingId)
    if (!id || segments.value.length === 0) return
    const last = segments.value[segments.value.length - 1]
    const topicChanged = topicShift(
      liveSummary.value?.keyPoints?.[0] ?? liveSummary.value?.summary,
      last?.text ?? '',
    )
    const newCount = segments.value.length - lastSegmentCount.value
    const elapsed = Date.now() - lastUpdateAt
    if (!force && !topicChanged && newCount < SUMMARY_SEGMENT_THRESHOLD && elapsed < SUMMARY_INTERVAL_MS) return

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
      liveSummary.value = toLiveSummary(result)
      lastSegmentCount.value = segments.value.length
      lastUpdateAt = Date.now()

      await updateMeeting(id, { liveSummary: liveSummary.value, summary: result.summary })
      opts?.onUpdated?.(liveSummary.value)

      const recs = await meetingsApi.recommend(id, cleanSegments, result.summary)
      if (recs.length > 0) {
        recommendations.value = recs
        await updateMeeting(id, { recommendations: recs })
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
    throttleTimer = setTimeout(() => refresh(), 2000)
  }

  watch(segments, () => scheduleRefresh(), { deep: true })

  return { liveSummary, recommendations, isUpdating, refresh }
}
