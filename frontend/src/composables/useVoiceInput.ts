/**
 * useVoiceInput — 共享语音录制 + STT 转写逻辑。
 * 录音使用 MediaRecorder，转写走 sttApi（local sherpa → cloud fallback）。
 *
 * 权限闸：先走 useMicPermission().ensure()，被拒时给出可读的 deniedLabel，
 * 避免 getUserMedia 的 NotAllowedError 直接吞掉。
 */
import { ref, onBeforeUnmount } from 'vue'
import { sttApi } from '../api/stt'
import { useMicPermission } from './useMicPermission'

export function useVoiceInput() {
  const isRecording = ref(false)
  const isTranscribing = ref(false)
  const sttError = ref('')
  const mic = useMicPermission()

  let mediaRecorder: MediaRecorder | null = null
  let mediaStream: MediaStream | null = null
  let audioChunks: Blob[] = []
  let audioPath = ''
  let audioPathTimeout: ReturnType<typeof setTimeout> | null = null

  function cleanupMedia() {
    if (mediaRecorder && mediaRecorder.state !== 'inactive') {
      try { mediaRecorder.stop() } catch { /* already stopped */ }
    }
    mediaRecorder = null
    if (mediaStream) {
      mediaStream.getTracks().forEach((t) => t.stop())
      mediaStream = null
    }
  }

  function cleanupAudioPath() {
    if (audioPathTimeout) {
      clearTimeout(audioPathTimeout)
      audioPathTimeout = null
    }
    if (audioPath) {
      URL.revokeObjectURL(audioPath)
      audioPath = ''
    }
  }

  async function startRecording(): Promise<boolean> {
    if (isRecording.value || isTranscribing.value) return false
    cleanupAudioPath()
    sttError.value = ''

    // 权限前置闸：被拒时直接给友好文案，避免后续 getUserMedia 静默失败。
    const ok = await mic.ensure()
    if (!ok) {
      sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      return false
    }

    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, sampleRate: 16000 },
      })
      mediaRecorder = new MediaRecorder(mediaStream)
      audioChunks = []
      mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) audioChunks.push(e.data)
      }
      mediaRecorder.start()
      isRecording.value = true
      return true
    } catch {
      sttError.value = '麦克风权限被拒绝'
      cleanupMedia()
      return false
    }
  }

  async function stopRecording(): Promise<string | null> {
    if (!isRecording.value || !mediaRecorder) return null
    isRecording.value = false
    isTranscribing.value = true

    await new Promise<void>((resolve) => {
      mediaRecorder!.onstop = () => resolve()
      mediaRecorder!.stop()
    })
    cleanupMedia()

    const blob = new Blob(audioChunks, { type: 'audio/webm' })
    audioPath = URL.createObjectURL(blob)

    try {
      const result = await sttApi.transcribe({ audioPath })
      return result.text
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      sttError.value = `转写失败：${msg}`
      return null
    } finally {
      isTranscribing.value = false
      audioPathTimeout = setTimeout(cleanupAudioPath, 30000)
    }
  }

  async function toggleRecording(): Promise<string | null> {
    if (isRecording.value) return stopRecording()
    await startRecording()
    return null
  }

  onBeforeUnmount(() => {
    cleanupMedia()
    cleanupAudioPath()
  })

  return {
    isRecording,
    isTranscribing,
    sttError,
    startRecording,
    stopRecording,
    toggleRecording,
  }
}
