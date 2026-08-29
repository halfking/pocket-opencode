/**
 * useMeetingRecorder — VAD 分段 + STT + 声纹说话人识别
 *
 * Sprint 3: Web Audio VAD 替代固定 5s 切片；增量声纹聚类区分说话人。
 * cap-sherpa 原生就绪后 extractEmbedding / startListening 自动优先。
 */
import { ref, onBeforeUnmount } from 'vue'
import { sttApi } from '../api/stt'
import { saveMeetingAudio } from '../native/meeting-audio'
import { VadSegmenter } from '../native/vad-segmenter'
import { extractEmbedding } from '../native/speaker-embedding'
import { SpeakerDiarizer } from '../native/speaker-diarization'
import { loadSpeakerProfiles, saveVoiceprint } from '../features/meetings/voiceprints-store'
import {
  saveSegment, updateMeeting, updateTranscript, updateSegmentSpeaker,
  getMeeting, type MeetingSegment,
} from '../features/meetings/meetings-store'
import { syncMeetingMetadata } from '../features/meetings/meeting-ingest'
import { useMicPermission } from './useMicPermission'

const LANG_LABELS: Record<string, string> = {
  zh: '中文', en: 'English', ja: '日本語', ko: '한국어', fr: 'Français', de: 'Deutsch',
}

export function useMeetingRecorder(meetingId: string) {
  const isRecording = ref(false)
  const isPaused = ref(false)
  const elapsedMs = ref(0)
  const segments = ref<MeetingSegment[]>([])
  const sttError = ref('')
  const speakers = ref<{ profileId: string; label: string }[]>([])
  const processingCount = ref(0)

  let mediaStream: MediaStream | null = null
  let vadSegmenter: VadSegmenter | null = null
  let diarizer: SpeakerDiarizer | null = null
  let startTime = 0
  let elapsedTimer: ReturnType<typeof setInterval> | null = null
  /** segmentId → profileId，用于标注 */
  const segmentProfiles = new Map<string, string>()

  function cleanupMedia() {
    if (mediaStream) {
      mediaStream.getTracks().forEach((t) => t.stop())
      mediaStream = null
    }
    vadSegmenter = null
  }

  async function start(): Promise<boolean> {
    if (isRecording.value) return false
    sttError.value = ''
    segments.value = []
    speakers.value = []
    segmentProfiles.clear()
    elapsedMs.value = 0
    processingCount.value = 0

    diarizer = new SpeakerDiarizer(0.72)
    try {
      const profiles = await loadSpeakerProfiles()
      diarizer.loadProfiles(profiles)
    } catch { /* 首次使用，空库 */ }

    // 权限前置闸：首次录音走 getUserMedia 时，MainActivity 的 WebChromeClient
    // 会拦截 AUDIO_CAPTURE 并调起系统 RECORD_AUDIO 申请；这里再走一遍探测，
    // 一是为了复用统一文案，二是 getUserMedia 失败时给出可读的 deniedLabel。
    const mic = useMicPermission()
    const ok = await mic.ensure()
    if (!ok) {
      sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝，请在系统设置中授权后重试'
      return false
    }

    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, sampleRate: 16000 },
      })

      vadSegmenter = new VadSegmenter({
        silenceMs: 1500,
        minSpeechMs: 400,
        energyThreshold: 0.012,
        onSegment: (seg) => { void processSegment(seg.blob, seg.startMs, seg.endMs) },
      })
      await vadSegmenter.start(mediaStream)

      startTime = Date.now()
      isRecording.value = true
      elapsedTimer = setInterval(() => {
        if (!isPaused.value) elapsedMs.value = Date.now() - startTime
      }, 200)
      return true
    } catch {
      sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      cleanupMedia()
      return false
    }
  }

  async function processSegment(blob: Blob, startMs: number, endMs: number) {
    if (!diarizer) return
    processingCount.value++
    const audioPath = URL.createObjectURL(blob)

    try {
      const [sttResult, embedding] = await Promise.all([
        sttApi.transcribe({ audioPath }),
        extractEmbedding(blob),
      ])
      URL.revokeObjectURL(audioPath)
      if (!sttResult.text.trim()) return

      const { profileId, label, isNew } = diarizer.identify(embedding)
      syncSpeakers()

      const seg: Omit<MeetingSegment, 'id'> = {
        meetingId,
        speakerLabel: label,
        lang: detectLang(sttResult.text),
        confidence: sttResult.confidence,
        startMs,
        endMs,
        text: sttResult.text.trim(),
      }
      const id = await saveSegment(seg)
      segmentProfiles.set(id, profileId)
      segments.value.push({ id, ...seg })
      await syncTranscript()

      if (isNew) {
        // 新说话人：暂存 embedding，等用户标注后写入 voiceprints
      }
    } catch (e) {
      sttError.value = '转写失败，将在下一段重试'
      console.warn('[meeting-recorder] segment failed:', e)
    } finally {
      processingCount.value--
    }
  }

  function syncSpeakers() {
    if (!diarizer) return
    speakers.value = diarizer.getProfiles().map((p) => ({
      profileId: p.id,
      label: p.label,
    }))
  }

  async function syncTranscript() {
    const full = segments.value.map((s) =>
      `[${s.speakerLabel}] ${s.text}`,
    ).join('\n')
    await updateTranscript(meetingId, full)
  }

  /** 用户标注说话人 */
  async function labelSpeaker(profileId: string, displayName: string) {
    if (!diarizer) return
    diarizer.labelProfile(profileId, displayName)
    syncSpeakers()

    const profile = diarizer.getProfile(profileId)
    if (profile) {
      await saveVoiceprint({ id: profileId, displayName, embedding: profile.embedding })
    }

    // 更新所有属于该 profile 的 segment
    for (const [segId, pid] of segmentProfiles) {
      if (pid !== profileId) continue
      await updateSegmentSpeaker(segId, displayName)
      const seg = segments.value.find((s) => s.id === segId)
      if (seg) seg.speakerLabel = displayName
    }
    await syncTranscript()
  }

  async function stop(): Promise<{ audioPath: string; durationMs: number } | null> {
    if (!isRecording.value) return null
    isRecording.value = false
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null }

    const fullBlob = vadSegmenter?.stop() ?? null
    cleanupMedia()

    const durationMs = elapsedMs.value
    let audioPath = ''
    if (fullBlob && fullBlob.size > 0) {
      audioPath = URL.createObjectURL(fullBlob)
      try { await saveMeetingAudio(meetingId, fullBlob) } catch { /* ok */ }
    }

    await updateMeeting(meetingId, {
      audioPath: audioPath || null,
      durationMs,
      status: 'completed',
    })

    // 后台云同步元数据（不含音频）
    void getMeeting(meetingId).then((m) => {
      if (m) syncMeetingMetadata(m)
    })

    return audioPath ? { audioPath, durationMs } : { audioPath: '', durationMs }
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
    cleanupMedia()
  })

  return {
    isRecording, isPaused, elapsedMs, segments, sttError, speakers, processingCount,
    start, stop, formatElapsed, labelSpeaker, langLabels: LANG_LABELS,
  }
}

function pad(n: number): string { return n.toString().padStart(2, '0') }

function detectLang(text: string): string {
  const cjk = (text.match(/[\u4e00-\u9fff\u3400-\u4dbf]/g) || []).length
  const latin = (text.match(/[a-zA-Z]/g) || []).length
  const total = text.length || 1
  if (cjk / total > 0.3 && latin / total > 0.3) return 'mixed'
  if (cjk / total > 0.15) return 'zh'
  if (latin / total > 0.3) return 'en'
  return 'zh'
}
