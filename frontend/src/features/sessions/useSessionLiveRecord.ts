import { ref, watch, onMounted } from 'vue'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { createMeeting, updateMeeting } from '../meetings/meetings-store'
import { getRecordingBySession } from '../meetings/meetings-live'
import { meetingsApi } from '../../api/meetings'
import { isAbortError } from '../../api/http'
import { sttSettingsApi } from '../../api/stt-settings'
import { refetchFullTranscript, shouldAdoptFullTranscript } from '../meetings/meeting-final-transcript'

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
    await recorder.stop()
    active.value = false
    if (!meetingId.value) return
    const id = meetingId.value
    // 收尾顺序有讲究：**先全量重转拿高精度文本，再交给 LLM 精校**。
    //
    // 2026-10-06 修 bug 的关键：此前精校是把**分段累积的错文本**直接丢给
    // LLM 润色。LLM 只能看到已经错了的字符，它并不知道音频里原本说的是
    // 什么，所以「润色」修不了同音字/专有名词/数字这类识别错误。
    // 必须先回到音频整段重转（整段上下文 = 最高准确率），再让 LLM 做
    // 它真正擅长的事：分段、去口水词、补标点。
    const segs = recorder.segments.value
    const finalText = await refetchFullTranscript(id, (blob, filename) =>
      sttSettingsApi.transcribeFull(blob, filename).then((r) => ({ text: r.text, failed: r.failed })),
    )
    // ★ 采纳判定不能省：全量重转**不保证**更好（用户中途换了更差的模型、
    //   或长音频被上游截断）。变短一半以上就沿用分段文本——让用户看到
    //   内容变少比看到几个错字更糟。
    const adopt = finalText.applied && finalText.text
      ? shouldAdoptFullTranscript(finalText.text, segs).adopt
      : false
    const baseSegments = adopt && finalText.text
      ? [{ id: `${id}-full`, meetingId: id, speakerLabel: null, lang: segs[0]?.lang ?? 'zh', confidence: 1, startMs: 0, endMs: recorder.elapsedMs.value, text: finalText.text }]
      : segs
    try {
      const result = await meetingsApi.refine(id, baseSegments)
      await updateMeeting(id, {
        refinedTranscript: result.refinedTranscript,
        status: 'refined',
      })
      if (adopt && finalText.applied) {
        const lost = finalText.failedSegments
        toast.success(
          lost
            ? `录音已结束，已整段重转并精翻（有 ${lost} 段未转出）`
            : '录音已结束，已整段重转并精翻',
        )
      } else {
        toast.success('录音已结束，精翻完成')
      }
    } catch (e) {
      // 「已取消」和「失败」对用户是两件不同的事：前者不该被提示成
      // 「稍后重试」，否则会让人以为录音或会议出了什么问题。
      if (isAbortError(e)) {
        toast.info('已停止精翻')
        return
      }
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
