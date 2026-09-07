/**
 * useMeetingRecorder — VAD 分段 + STT + 声纹；Android 走 BackgroundMic 前台服务。
 */
import { ref, unref, onBeforeUnmount, type MaybeRef } from 'vue'
import { Capacitor } from '@capacitor/core'
import { saveMeetingAudio } from '../native/meeting-audio'
import { VadSegmenter } from '../native/vad-segmenter'
import { SpeakerDiarizer } from '../native/speaker-diarization'
import { loadSpeakerProfiles, saveVoiceprint } from '../features/meetings/voiceprints-store'
import {
  updateMeeting, updateSegmentSpeaker, getMeeting, type MeetingSegment,
} from '../features/meetings/meetings-store'
import { syncMeetingMetadata } from '../features/meetings/meeting-ingest'
import { ingestSpeechBlob } from '../features/meetings/ingest-speech'
import { useMicPermission } from './useMicPermission'
import { openPreferredMicStream, listAudioInputs, type AudioInput } from '../native/audio-inputs'
import {
  isBackgroundMicSupported, listNativeMicInputs, startBackgroundMic,
  stopBackgroundMic, listenBackgroundMicParts, nativePartToBlob,
} from '../native/background-mic'
import { useToast } from './useToast'

const LANG_LABELS: Record<string, string> = {
  zh: '中文', en: 'English', ja: '日本語', ko: '한국어', fr: 'Français', de: 'Deutsch',
}

export function useMeetingRecorder(meetingId: MaybeRef<string>) {
  const isRecording = ref(false)
  const isPaused = ref(false)
  const elapsedMs = ref(0)
  const segments = ref<MeetingSegment[]>([])
  const sttError = ref('')
  const speakers = ref<{ profileId: string; label: string }[]>([])
  const processingCount = ref(0)
  const inputs = ref<AudioInput[]>([])
  const selectedInput = ref<AudioInput | undefined>()
  const toast = useToast()

  let mediaStream: MediaStream | null = null
  let vadSegmenter: VadSegmenter | null = null
  let diarizer: SpeakerDiarizer | null = null
  let startTime = 0
  let elapsedTimer: ReturnType<typeof setInterval> | null = null
  const segmentProfiles = new Map<string, string>()
  let partSeq = 0
  let unlistenNative: (() => void) | null = null
  let nativeMode = false

  async function cleanupMedia() {
    if (mediaStream) {
      mediaStream.getTracks().forEach((t) => t.stop())
      mediaStream = null
    }
    vadSegmenter = null
    if (unlistenNative) { unlistenNative(); unlistenNative = null }
    if (nativeMode) await stopBackgroundMic()
    nativeMode = false
    document.removeEventListener('visibilitychange', onHidden)
    navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)
  }

  function onHidden() {
    if (document.visibilityState !== 'hidden' || !isRecording.value || nativeMode) return
    toast.error('浏览器无法在后台继续录音，请保持应用在前台或使用 Android 客户端')
  }

  async function start(opts?: { deviceId?: string; resume?: boolean }): Promise<boolean> {
    if (isRecording.value || !unref(meetingId)) return false
    sttError.value = ''
    if (!opts?.resume) {
      segments.value = []
      speakers.value = []
      segmentProfiles.clear()
      elapsedMs.value = 0
      processingCount.value = 0
      partSeq = 0
    }
    if (!opts?.resume || !diarizer) {
      diarizer = new SpeakerDiarizer(0.72)
      try {
        const profiles = await loadSpeakerProfiles()
        diarizer.loadProfiles(profiles)
      } catch { /* 空库 */ }
    }

    const mic = useMicPermission()
    const ok = await mic.ensure()
    if (!ok) {
      sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝，请在系统设置中授权后重试'
      return false
    }

    try {
      nativeMode = false
      if (isBackgroundMicSupported()) {
        const nativeInputs = await listNativeMicInputs()
        if (nativeInputs.length) inputs.value = nativeInputs
        nativeMode = await startBackgroundMic({
          meetingId: unref(meetingId),
          deviceId: opts?.deviceId,
        })
        if (nativeMode) {
          selectedInput.value = inputs.value.find((i) => i.deviceId === opts?.deviceId)
            || inputs.value[0]
          unlistenNative = await listenBackgroundMicParts((part) => {
            void processSegment(nativePartToBlob(part), part.startMs, part.endMs)
          }, (msg) => { sttError.value = msg })
        }
      }
      if (!nativeMode) {
        const opened = await openPreferredMicStream(opts?.deviceId)
        mediaStream = opened.stream
        inputs.value = opened.inputs
        selectedInput.value = opened.selected
        vadSegmenter = new VadSegmenter({
          silenceMs: 1500,
          minSpeechMs: 400,
          energyThreshold: 0.012,
          onSegment: (seg) => { void processSegment(seg.blob, seg.startMs, seg.endMs) },
        })
        await vadSegmenter.start(mediaStream)
        document.addEventListener('visibilitychange', onHidden)
      }

      startTime = Date.now()
      isRecording.value = true
      elapsedTimer = setInterval(() => {
        if (!isPaused.value) elapsedMs.value = Date.now() - startTime
      }, 200)
      navigator.mediaDevices?.addEventListener?.('devicechange', onDeviceChange)
      return true
    } catch {
      sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      void cleanupMedia()
      return false
    }
  }

  async function onDeviceChange() {
    const next = Capacitor.isNativePlatform()
      ? await listNativeMicInputs()
      : await listAudioInputs()
    if (!next.length) return
    inputs.value = next
    const best = next[0]
    if (selectedInput.value && best.deviceId !== selectedInput.value.deviceId && best.rank < selectedInput.value.rank) {
      toast.success(`检测到更好的麦克风：${best.label}，可在录音条切换`)
    }
  }

  async function switchDevice(deviceId: string) {
    if (!isRecording.value) return start({ deviceId })
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null }
    const keptElapsed = elapsedMs.value
    vadSegmenter?.stop()
    await cleanupMedia()
    isRecording.value = false
    const ok = await start({ deviceId, resume: true })
    if (ok) elapsedMs.value = keptElapsed
    return ok
  }

  async function processSegment(blob: Blob, startMs: number, endMs: number) {
    if (!diarizer) return
    processingCount.value++
    try {
      partSeq += 1
      await ingestSpeechBlob({
        meetingId, blob, startMs, endMs, seq: partSeq, diarizer,
        segments: segments.value, segmentProfiles,
      })
      syncSpeakers()
    } catch (e) {
      sttError.value = '转写失败，将在下一段重试'
      console.warn('[meeting-recorder] segment failed:', e)
    } finally {
      processingCount.value--
    }
  }

  function syncSpeakers() {
    if (!diarizer) return
    speakers.value = diarizer.getProfiles().map((p) => ({ profileId: p.id, label: p.label }))
  }

  async function labelSpeaker(profileId: string, displayName: string) {
    if (!diarizer) return
    diarizer.labelProfile(profileId, displayName)
    syncSpeakers()
    const profile = diarizer.getProfile(profileId)
    if (profile) await saveVoiceprint({ id: profileId, displayName, embedding: profile.embedding })
    for (const [segId, pid] of segmentProfiles) {
      if (pid !== profileId) continue
      await updateSegmentSpeaker(segId, displayName)
      const seg = segments.value.find((s) => s.id === segId)
      if (seg) seg.speakerLabel = displayName
    }
  }

  async function stop(): Promise<{ audioPath: string; durationMs: number } | null> {
    if (!isRecording.value) return null
    isRecording.value = false
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null }
    navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)

    const fullBlob = vadSegmenter?.stop() ?? null
    await cleanupMedia()
    const durationMs = elapsedMs.value
    let audioPath = ''
    if (fullBlob && fullBlob.size > 0) {
      audioPath = URL.createObjectURL(fullBlob)
      try { await saveMeetingAudio(unref(meetingId), fullBlob) } catch { /* ok */ }
    }
    await updateMeeting(unref(meetingId), {
      audioPath: audioPath || null,
      durationMs,
      status: 'completed',
    })
    void getMeeting(unref(meetingId)).then((m) => { if (m) syncMeetingMetadata(m) })
    return { audioPath, durationMs }
  }

  function formatElapsed(): string {
    const s = Math.floor(elapsedMs.value / 1000)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = s % 60
    if (h > 0) return `${h}:${pad(m)}:${pad(sec)}`
    return `${pad(m)}:${pad(sec)}`
  }

  onBeforeUnmount(() => {
    if (elapsedTimer) clearInterval(elapsedTimer)
    navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)
    void cleanupMedia()
  })

  return {
    isRecording, isPaused, elapsedMs, segments, sttError, speakers, processingCount,
    inputs, selectedInput,
    start, stop, switchDevice, formatElapsed, labelSpeaker, langLabels: LANG_LABELS,
  }
}

function pad(n: number): string { return n.toString().padStart(2, '0') }
