import { ref, watch, onMounted } from 'vue'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { createMeeting, getMeetingWithSegments, updateMeeting } from '../meetings/meetings-store'
import { getRecordingBySession } from '../meetings/meetings-live'
import { finalizeRecording } from '../meetings/meeting-recording-finalize'
import type { LocalMeeting } from '../meetings/meetings-store'
import { resolveRecordingTitle } from './recording-title.ts'
import { LEFTOVER_RECORDING_HINT } from '../meetings/meeting-finalize-persist-notice'
import {
  describeLiveRecordFailure,
  LIVE_RECORD_FAIL_TAG,
} from './live-record-failure-notice'

export function useSessionLiveRecord(sessionId: () => string, sessionTitle: () => string) {
  const meetingId = ref('')
  const active = ref(false)
  const resumeHint = ref('')
  const { confirm } = useConfirm()
  const toast = useToast()
  const recorder = useMeetingRecorder(meetingId)
  // ⚠⚠ §932：录音 runtime 的增量渲染器**已经**维护着同一批段的去重状态
  //   ⇒ 摘要链直接拿它那份**逐字相同**的结果（每轮从 O(n) 变成 O(新增)）。
  //   ⚠ 拿不到 ⇒ 返回 null ⇒ 摘要链回退整场去重（慢但正确）。
  const summary = useLiveSummary(meetingId, recorder.segments, {
    dedupedSegments: (raw) => recorder.runtime.dedupedSegmentsFor(meetingId.value, raw),
  })

  watch(meetingId, (id) => {
    if (id) void summary.refresh(true)
  })

  onMounted(async () => {
    // 查不到遗留录音 ⇒ 不弹「上次录音未正常结束」提示（用户以为自己没录过）。
    // 「查询失败」与「确实没有遗留」在界面上完全同形 ⇒ 留痕。
    const leftover = await getRecordingBySession(sessionId()).catch((e) => {
      console.warn('[live-record] 遗留录音查询失败（可能漏报「上次录音未正常结束」）：', e)
      return null
    })
    if (leftover) {
      // ⚠ §834/D2：这句话现在是**共享常量** —— 原来只有这里有一份，
      // 而 MeetingDetailView 对同一状态却是「静默自动开麦」。两处引同一个常量，
      // 「遗留 recording 不自动重开采集」这条承诺就不会再各自漂移。
      resumeHint.value = LEFTOVER_RECORDING_HINT
      toast.warning(resumeHint.value)
    }
  })

  async function start() {
    // ⚠⚠ §839/①：此前 `createMeeting` 是裸 await，而 `toggle()` 被
    //   `@live-record="liveRecord.toggle"` 直接绑成事件处理器
    //   ⇒ 写库失败时用户点麦克风**什么都不会发生**，也没有任何提示。
    let m: LocalMeeting
    try {
      m = await createMeeting({
        title: resolveRecordingTitle(sessionId(), sessionTitle()),
        sessionId: sessionId(),
      })
    } catch (e) {
      console.error(LIVE_RECORD_FAIL_TAG, 'create', e)
      toast.error(describeLiveRecordFailure('create', { error: e }))
      return
    }
    meetingId.value = m.id
    const ok = await recorder.start()
    if (!ok) {
      // 采集没起来就把会议标记结束，否则遗留的 recording 状态会在每次
      // 进入会话时触发"上次录音未正常结束"警告。
      //
      // ⚠⚠ §839/②：这行此前是 `.catch(() => {})` —— **它要防的那个后果，
      //   恰恰是它自己失败时会发生的后果**：清理写失败 ⇒ 会议永久停在
      //   `recording` ⇒ 列表一直显示「录音中」，且此后每次进入本会话
      //   都会弹「上次录音未正常结束」，而用户根本没录成（误导性警告）。
      //   ⇒ 失败必须说出来，并告诉用户怎么收拾这条残留记录。
      // 单一出口：收尾失败时**只说收尾那一句**（那是用户现在必须动手做的事），
      // 收尾成功才说「无法开始录音」⇒ 不会连弹两条 error。
      let cleanupFailed = false
      try {
        await updateMeeting(m.id, { status: 'completed' })
      } catch (e) {
        cleanupFailed = true
        console.error(LIVE_RECORD_FAIL_TAG, 'cleanup', e)
      }
      meetingId.value = ''
      toast.error(cleanupFailed
        ? describeLiveRecordFailure('cleanup')
        : recorder.sttError.value || '无法开始录音')
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
    // ⚠⚠ §839/③：此前是裸 await ⇒ 读不回分段时 `finalizeRecording` 整段不发生，
    //   而此时录音已停、状态已写 completed、面板已关 ⇒ 用户看到的是「正常结束」。
    //   ⚠ 处置是**停下**而不是继续：拿不到分段还去跑收尾编排，等于把一份残缺的
    //   输入交给一条会重写 transcript 的链 ⇒ 宁可不做，也不要在读失败时改写已有正文。
    let stored: Awaited<ReturnType<typeof getMeetingWithSegments>> = null
    try {
      stored = await getMeetingWithSegments(id)
    } catch (e) {
      console.error(LIVE_RECORD_FAIL_TAG, 'load-segments', e)
      toast.error(describeLiveRecordFailure('load-segments', { error: e }))
      return
    }
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
