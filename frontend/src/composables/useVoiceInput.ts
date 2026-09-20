/**
 * useVoiceInput — 共享语音录制 + STT 转写逻辑。
 * 录音使用 MediaRecorder，转写走 sttApi（local sherpa → cloud fallback）。
 *
 * 权限闸：先走 useMicPermission().ensure()，被拒时给出可读的 deniedLabel，
 * 避免 getUserMedia 的 NotAllowedError 直接吞掉。
 *
 * 转写结果生存（2026-09-20 P0/G5）：转写是已发出的 HTTP 任务，组件
 * unmount 不取消；组件不在场时结果不再作废 —— 落剪贴板并 toast 告知，
 * 用户口述文本可找回。若 unmount 时仍在录音，同样收尾转写已捕获音频。
 */
import { ref, onBeforeUnmount } from 'vue'
import { sttApi } from '../api/stt'
import { openPreferredMicStream } from '../native/audio-inputs'
import { useMicPermission } from './useMicPermission'
import { useToast } from './useToast'

export function useVoiceInput() {
  const isRecording = ref(false)
  const isTranscribing = ref(false)
  const sttError = ref('')
  const mic = useMicPermission()
  const toast = useToast()

  let mediaRecorder: MediaRecorder | null = null
  let mediaStream: MediaStream | null = null
  let audioChunks: Blob[] = []
  let audioPath = ''
  let audioPathTimeout: ReturnType<typeof setTimeout> | null = null
  /** 组件已卸载:此后完成的转写走「孤儿交付」(剪贴板 + toast)。 */
  let ownerGone = false

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

  /** 组件不在场时的转写交付:文本进剪贴板,toast 告知(不弹全文,长文本刷屏)。 */
  async function deliverOrphan(text: string): Promise<void> {
    const trimmed = text.trim()
    if (!trimmed) return
    let copied = false
    try {
      await navigator.clipboard.writeText(trimmed)
      copied = true
    } catch { /* WebView 可能无剪贴板权限 */ }
    toast.success(copied
      ? `语音转写已完成并复制到剪贴板：${trimmed.slice(0, 24)}${trimmed.length > 24 ? '…' : ''}`
      : `语音转写已完成：${trimmed.slice(0, 60)}${trimmed.length > 60 ? '…' : ''}`)
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
      const opened = await openPreferredMicStream()
      mediaStream = opened.stream
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
      const result = await sttApi.transcribe({ audioBlob: blob })
      if (ownerGone) await deliverOrphan(result.text)
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

  onBeforeUnmount(async () => {
    ownerGone = true
    if (isTranscribing.value) {
      // 转写已在途:不取消,结果由 stopRecording 的收尾路径孤儿交付。
      cleanupAudioPath()
      return
    }
    if (isRecording.value) {
      // 还在录:收尾已捕获音频并完成转写(语音输入是即时交互,unmount 停止
      // 采集;但已录的部分不丢)。
      cleanupMedia()
      const blob = new Blob(audioChunks, { type: 'audio/webm' })
      isTranscribing.value = true
      try {
        if (blob.size > 0) {
          const result = await sttApi.transcribe({ audioBlob: blob })
          await deliverOrphan(result.text)
        }
      } catch { /* 孤儿转写失败无从提示,静默 */ } finally {
        isTranscribing.value = false
      }
    }
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
