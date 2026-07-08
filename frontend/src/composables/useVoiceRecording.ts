/**
 * useVoiceRecording — shared composable for voice input.
 *
 * Encapsulates MediaRecorder + sttApi integration used by
 * SessionConversationView, TasksView, and VoiceRecorderWidget.
 */
import { ref, onBeforeUnmount } from 'vue'
import { sttApi } from '../api/stt'

export interface UseVoiceRecordingOptions {
  /** Called with the transcribed text on success. */
  onTranscribed: (text: string) => void
  /** Called on STT error. Receives error message. */
  onError?: (message: string) => void
}

export function useVoiceRecording(opts: UseVoiceRecordingOptions) {
  const isRecording = ref(false)
  const transcribing = ref(false)

  let mediaRecorder: MediaRecorder | null = null
  let mediaStream: MediaStream | null = null
  let audioChunks: Blob[] = []

  async function startRecording() {
    if (isRecording.value || transcribing.value) return
    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, sampleRate: 16000 },
      })
      mediaRecorder = new MediaRecorder(mediaStream)
      audioChunks = []
      mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) audioChunks.push(e.data)
      }
      mediaRecorder.onstop = async () => {
        cleanupMedia()
        if (audioChunks.length === 0) return
        transcribing.value = true
        try {
          const blob = new Blob(audioChunks, { type: 'audio/webm' })
          const result = await sttApi.transcribe({ audioBlob: blob })
          opts.onTranscribed(result.text)
        } catch (e: any) {
          console.error('STT failed:', e)
          opts.onError?.('语音识别失败，请手动输入')
        } finally {
          transcribing.value = false
        }
      }
      mediaRecorder.start()
      isRecording.value = true
    } catch (e) {
      console.error('Microphone access denied:', e)
      opts.onError?.('麦克风权限被拒绝')
    }
  }

  function stopRecording() {
    if (mediaRecorder && mediaRecorder.state !== 'inactive') {
      mediaRecorder.stop()
    }
    isRecording.value = false
  }

  function toggleRecording() {
    if (isRecording.value) {
      stopRecording()
    } else {
      startRecording()
    }
  }

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

  onBeforeUnmount(() => {
    cleanupMedia()
  })

  return {
    isRecording,
    transcribing,
    startRecording,
    stopRecording,
    toggleRecording,
  }
}
