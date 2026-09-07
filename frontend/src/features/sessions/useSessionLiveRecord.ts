import { ref, watch, onMounted } from 'vue'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { createMeeting, updateMeeting } from '../meetings/meetings-store'
import { getRecordingBySession } from '../meetings/meetings-live'
import { meetingsApi } from '../../api/meetings'

export function useSessionLiveRecord(sessionId: () => string, sessionTitle: () => string) {
  const meetingId = ref('')
  const active = ref(false)
  const resumeHint = ref('')
  const { confirm } = useConfirm()
  const toast = useToast()
  const recorder = useMeetingRecorder(meetingId)
  const summary = useLiveSummary(meetingId, recorder.segments)

  watch(meetingId, (id) => {
    if (id) void summary.refresh(true)
  })

  onMounted(async () => {
    const leftover = await getRecordingBySession(sessionId()).catch(() => null)
    if (leftover) {
      resumeHint.value = '上次录音未正常结束，点麦克风可开始新的实时记录（不会自动重开采集）'
      toast.warning(resumeHint.value)
    }
  })

  async function start() {
    const m = await createMeeting({
      title: sessionTitle() || '会话录音',
      sessionId: sessionId(),
    })
    meetingId.value = m.id
    const ok = await recorder.start()
    if (!ok) {
      toast.error(recorder.sttError.value || '无法开始录音')
      return
    }
    active.value = true
  }

  async function stop() {
    await recorder.stop()
    active.value = false
    if (!meetingId.value) return
    try {
      const result = await meetingsApi.refine(meetingId.value, recorder.segments.value)
      await updateMeeting(meetingId.value, {
        refinedTranscript: result.refinedTranscript,
        status: 'refined',
      })
      toast.success('录音已结束，精翻完成')
    } catch {
      toast.warning('录音已结束，精翻稍后可在会议详情重试')
    }
  }

  async function toggle() {
    if (recorder.isRecording.value) {
      const ok = await confirm({ title: '结束录音', message: '停止本会话的实时录音？' })
      if (ok) await stop()
      return
    }
    await start()
  }

  return { meetingId, active, resumeHint, recorder, summary, start, stop, toggle }
}
