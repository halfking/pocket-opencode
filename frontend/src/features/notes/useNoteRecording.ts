import { onBeforeUnmount, ref } from 'vue'
import { sttApi } from '../../api/stt'
import { sherpa } from '../../native/sherpa'
import { useMicPermission } from '../../composables/useMicPermission'
import { setHeaderTitle } from '../../composables/useAppHeaderTitle'
import {
  appendTranscript,
  formatRecordingClock,
  nextRecordingState,
  type RecordingPhase,
} from './note-recording'

const CHUNK_MS = 3000

export function useNoteRecording() {
  const phase = ref<RecordingPhase>('idle')
  const elapsedMs = ref(0)
  const transcript = ref('')
  const error = ref('')
  const recording = ref(false)

  let mediaRecorder: MediaRecorder | null = null
  let mediaStream: MediaStream | null = null
  let chunks: Blob[] = []
  let startedAt = 0
  let tick: ReturnType<typeof setInterval> | null = null
  let committed = ''
  let unlisten: { remove: () => void } | null = null
  let nativeListening = false

  function syncTitle() {
    if (phase.value === 'recording') setHeaderTitle(`录音 ${formatRecordingClock(elapsedMs.value)}`)
    else setHeaderTitle(null)
  }

  function startTick() {
    startedAt = Date.now()
    elapsedMs.value = 0
    tick = setInterval(() => {
      elapsedMs.value = Date.now() - startedAt
      syncTitle()
    }, 250)
  }

  function stopTick() {
    if (tick) clearInterval(tick)
    tick = null
  }

  async function start(): Promise<boolean> {
    if (phase.value !== 'idle') return false
    error.value = ''
    const mic = useMicPermission()
    const ok = await mic.ensure()
    if (!ok) {
      error.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      return false
    }
    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, sampleRate: 16000 },
      })
      chunks = []
      committed = ''
      transcript.value = ''
      mediaRecorder = new MediaRecorder(mediaStream)
      mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data)
        if (!nativeListening && e.data.size > 0 && phase.value === 'recording') {
          void transcribeSlice(e.data)
        }
      }
      mediaRecorder.start(CHUNK_MS)
      phase.value = nextRecordingState('idle', 'toggle')
      recording.value = true
      startTick()
      syncTitle()
      void startLiveStt()
      return true
    } catch {
      error.value = mic.deniedLabel.value || '无法打开麦克风'
      cleanupMedia()
      return false
    }
  }

  async function startLiveStt() {
    try {
      unlisten = await sherpa.addListener('partialResult', (result) => {
        const next = result.isFinal
          ? appendTranscript(committed, transcript.value, '', result.text)
          : appendTranscript(committed, transcript.value, result.text)
        committed = next.committed
        transcript.value = next.display
      })
      await sttApi.startStreaming()
      nativeListening = true
    } catch {
      nativeListening = false
    }
  }

  async function transcribeSlice(blob: Blob) {
    try {
      const result = await sttApi.transcribe({ audioBlob: blob })
      if (result.text.trim()) {
        const next = appendTranscript(committed, transcript.value, '', result.text)
        committed = next.committed
        transcript.value = next.display
      }
    } catch {
      /* 分片失败等下一段 */
    }
  }

  async function stop(): Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> {
    if (phase.value !== 'recording' || !mediaRecorder) return null
    phase.value = nextRecordingState('recording', 'toggle')
    recording.value = false
    stopTick()
    const durationMs = Date.now() - startedAt
    await new Promise<void>((resolve) => {
      mediaRecorder!.onstop = () => resolve()
      try { mediaRecorder!.stop() } catch { resolve() }
    })
    if (nativeListening) {
      try {
        const final = await sttApi.stopStreaming()
        const text = (final as { text?: string }).text || ''
        if (text) {
          const next = appendTranscript(committed, transcript.value, '', text)
          committed = next.committed
          transcript.value = next.display
        }
      } catch { /* keep current transcript */ }
      nativeListening = false
    }
    unlisten?.remove()
    unlisten = null
    cleanupMedia()
    const audioBlob = new Blob(chunks, { type: 'audio/webm' })
    if (!transcript.value.trim() && audioBlob.size > 0) {
      try {
        const result = await sttApi.transcribe({ audioBlob })
        transcript.value = result.text
      } catch (e) {
        error.value = e instanceof Error ? e.message : '转写失败'
      }
    }
    phase.value = nextRecordingState('stopping', 'drafted')
    setHeaderTitle(null)
    return { text: transcript.value.trim(), audioBlob, durationMs }
  }

  async function toggle(): Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> {
    if (phase.value === 'idle') {
      await start()
      return null
    }
    if (phase.value === 'recording') return stop()
    return null
  }

  function cleanupMedia() {
    if (mediaRecorder && mediaRecorder.state !== 'inactive') {
      try { mediaRecorder.stop() } catch { /* already stopped */ }
    }
    mediaRecorder = null
    mediaStream?.getTracks().forEach((t) => t.stop())
    mediaStream = null
  }

  onBeforeUnmount(() => {
    stopTick()
    cleanupMedia()
    unlisten?.remove()
    setHeaderTitle(null)
  })

  return { phase, elapsedMs, transcript, error, recording, start, stop, toggle }
}
