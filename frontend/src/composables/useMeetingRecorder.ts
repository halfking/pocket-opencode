/**
 * useMeetingRecorder — MeetingRecorderRuntime 的页面级薄壳(2026-09-20 P0)。
 *
 * 录音的所有权与状态在进程级单例 native/recordingRuntime.ts(M1 模式):
 * 组件 unmount 不再停止录音 —— 详情页切走/切回录音持续,跨页可见全局
 * 录音指示条 RecordingPill。本 composable 只提供「按 meetingId 圈定的
 * 只读视图 + 动作转发」:
 * - 正在录的是别的会议时,本页 isRecording/segments/elapsed 等不亮(不串台);
 * - start():已在录本会议 → 幂等 true;别的会议在录 → 先正式收尾旧的再开新的。
 */
import { computed, unref, type MaybeRef } from 'vue'
import {
  meetingRecorderRuntime as rt,
  type MeetingRecorderRuntime,
} from '../native/recordingRuntime'
import type { MeetingSegment } from '../features/meetings/meetings-store'
import type { AudioInput } from '../native/audio-inputs'

const LANG_LABELS: Record<string, string> = {
  zh: '中文', en: 'English', ja: '日本語', ko: '한국어', fr: 'Français', de: 'Deutsch',
}

export function useMeetingRecorder(meetingId: MaybeRef<string>) {
  // 本页面对应的录音是否正在进行(runtime 全局唯一活跃录音的归属判定)。
  const scoped = computed(() => {
    const id = rt.activeMeetingId.value
    return id !== '' && id === unref(meetingId)
  })

  const isRecording = computed(() => rt.isRecording.value && scoped.value)
  const isPaused = computed(() => rt.isPaused.value && scoped.value)
  const elapsedMs = computed(() => (scoped.value ? rt.elapsedMs.value : 0))
  const segments = computed<MeetingSegment[]>(() => (scoped.value ? rt.segments.value : []))
  const interimCaption = computed(() => (scoped.value ? rt.interimCaption.value : ''))
  const speakers = computed(() => (scoped.value ? rt.speakers.value : []))
  const processingCount = computed(() => (scoped.value ? rt.processingCount.value : 0))
  // sttError 不圈定:start() 失败的错误必须回显到发起页;跨会议串扰的
  // 瞬态错误可接受(错误文案本身是易失的)。
  const sttError = computed(() => rt.sttError.value)
  const inputs = computed<AudioInput[]>(() => rt.inputs.value)
  const selectedInput = computed<AudioInput | undefined>(() => rt.selectedInput.value)

  return {
    isRecording, isPaused, elapsedMs, segments, sttError, interimCaption, speakers, processingCount,
    inputs, selectedInput,
    start: (opts?: { deviceId?: string; resume?: boolean }) => rt.start(unref(meetingId), opts),
    stop: () => rt.stop(),
    switchDevice: (deviceId: string) => rt.switchDevice(unref(meetingId), deviceId),
    seedSegments: (stored: MeetingSegment[]) => rt.seedSegments(unref(meetingId), stored),
    appendText: (text: string) => rt.appendText(text),
    formatElapsed: () => rt.formatElapsed(),
    labelSpeaker: (profileId: string, label: string) => rt.labelSpeaker(profileId, label),
    langLabels: LANG_LABELS,
    /** runtime 单例直取(全局指示条/测试用)。 */
    runtime: rt as MeetingRecorderRuntime,
  }
}
