/**
 * useLiveSummary — 会议实时摘要 + 推荐，节流更新
 */
import { ref, watch, type Ref } from 'vue'
import { meetingsApi, toLiveSummary } from '../api/meetings'
import { updateMeeting, type LiveSummary, type MeetingSegment, type RecommendItem } from '../features/meetings/meetings-store'

const SUMMARY_INTERVAL_MS = 30_000
const SUMMARY_SEGMENT_THRESHOLD = 3

export function useLiveSummary(
  meetingId: string,
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
    if (segments.value.length === 0) return
    const newCount = segments.value.length - lastSegmentCount.value
    const elapsed = Date.now() - lastUpdateAt
    if (!force && newCount < SUMMARY_SEGMENT_THRESHOLD && elapsed < SUMMARY_INTERVAL_MS) return

    isUpdating.value = true
    try {
      const result = await meetingsApi.summarize(
        meetingId,
        segments.value,
        liveSummary.value?.summary,
        opts?.meta?.value,
      )
      liveSummary.value = toLiveSummary(result)
      lastSegmentCount.value = segments.value.length
      lastUpdateAt = Date.now()

      await updateMeeting(meetingId, { liveSummary: liveSummary.value, summary: result.summary })
      opts?.onUpdated?.(liveSummary.value)

      const recs = await meetingsApi.recommend(meetingId, segments.value, result.summary)
      if (recs.length > 0) {
        recommendations.value = recs
        await updateMeeting(meetingId, { recommendations: recs })
      }
    } catch (e) {
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
