import { ref, watch, onMounted } from 'vue'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { createMeeting, getMeetingWithSegments, updateMeeting } from '../meetings/meetings-store'
import { getRecordingBySession } from '../meetings/meetings-live'
import { finalizeRecording } from '../meetings/meeting-recording-finalize'
import { resolveRecordingTitle } from './recording-title.ts'

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
      title: resolveRecordingTitle(sessionId(), sessionTitle()),
      sessionId: sessionId(),
    })
    meetingId.value = m.id
    const ok = await recorder.start()
    if (!ok) {
      // 采集没起来就把会议标记结束，否则遗留的 recording 状态会在每次
      // 进入会话时触发"上次录音未正常结束"警告。
      await updateMeeting(m.id, { status: 'completed' }).catch(() => {})
      meetingId.value = ''
      toast.error(recorder.sttError.value || '无法开始录音')
      return
    }
    active.value = true
  }

  async function stop() {
    // ★ stop() 的返回值要接住：`durationMs` 只有它给得出（§49）。
    const stopped = await recorder.stop()
    active.value = false
    if (!meetingId.value) return
    const id = meetingId.value
    // ★★ 分段与时长都必须来自**持久层 / stop() 返回值**，不能读 recorder
    //   （recordingRuntime.stop() 在 return 前把 activeMeetingId 置空，
    //    而 useMeetingRecorder 的 segments/elapsedMs 都按它圈定 ⇒ stop 之后
    //    读到的恒为 [] 和 0，会让说话人归因整体失效 —— §49）。
    //
    //   读持久层而不是 `recorder.runtime.segments`：stop() 会 await 在途分段
    //   （上限 10s），那几段是 stop 期间才落库的，只有持久层拿得到。
    const stored = await getMeetingWithSegments(id)
    // 收尾的全部编排（全量重转 → 说话人归因 → 人名同音纠正 → 精校 → 入库）
    // 在共享模块里，**因为产品有两条录音路径**：
    //   ① 这里（随手记会话内录音）
    //   ② MeetingDetailView.onMicToggle（会议列表「开始录音」的主入口）
    // ② 这条以前完全没有精校，§49 抽出单一实现就是防它再漏一次。
    await finalizeRecording(
      id,
      stored?.segments ?? [],
      stopped?.durationMs ?? stored?.meeting.durationMs ?? 0,
      {
        notify: (kind, text) => {
          if (kind === 'success') toast.success(text)
          else if (kind === 'warning') toast.warning(text)
          else toast.info(text)
        },
      },
      // ★§825：与 durationMs 同源，都只能来自 stop() 的返回值。
      // 中途切过设备时这里非 0 ⇒ 收尾不再把整段重转按绝对时间挂回说话人。
      { audioStartsAtMs: stopped?.audioStartsAtMs ?? 0 },
    )
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
