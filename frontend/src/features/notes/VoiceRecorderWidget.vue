<!--
  VoiceRecorderWidget — floating record button (FAB). Long-press to record,
  release to stop; the audio is transcribed via sttApi (local sherpa first,
  Groq cloud fallback) and the transcript is emitted to the parent.

  Skeleton: uses the browser MediaRecorder API which works in Capacitor
  WebView. The native cap-sherpa plugin handles the transcription.
-->
<template>
  <div class="recorder-fab">
    <button
      class="fab"
      :class="{ recording }"
      @touchstart.prevent="start"
      @touchend.prevent="stop"
      @mousedown.prevent="start"
      @mouseup.prevent="stop"
      @mouseleave="recording && stop()"
    >
      <span class="fab-icon">{{ recording ? '⏹' : '🎤' }}</span>
    </button>
    <div v-if="recording" class="pulse" />
    <div v-if="status" class="status">{{ status }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { sttApi } from '../../api/stt'

const emit = defineEmits<{
  transcribed: [result: { text: string; audioPath: string; durationSec: number }]
}>()

const recording = ref(false)
const status = ref('')
let mediaRecorder: MediaRecorder | null = null
let mediaStream: MediaStream | null = null
let chunks: Blob[] = []
let startedAt = 0
let audioPath = '' // blob URL，转写完成后释放
let audioPathTimeout: ReturnType<typeof setTimeout> | null = null

async function start() {
  if (recording.value) return
  try {
    mediaStream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
    mediaRecorder = new MediaRecorder(mediaStream)
    chunks = []
    mediaRecorder.ondataavailable = (e) => e.data.size > 0 && chunks.push(e.data)
    mediaRecorder.start()
    startedAt = Date.now()
    recording.value = true
    status.value = '录音中…'
  } catch (e) {
    status.value = '麦克风权限被拒绝'
    cleanupMedia()
  }
}

async function stop() {
  if (!recording.value || !mediaRecorder) return
  recording.value = false
  const durationSec = Math.round((Date.now() - startedAt) / 1000)
  status.value = '转写中…'

  await new Promise<void>((resolve) => {
    mediaRecorder!.onstop = () => resolve()
    mediaRecorder!.stop()
  })
  cleanupMedia()

  // TODO: persist blob to a real file path for native STT. Currently a blob URL.
  const blob = new Blob(chunks, { type: 'audio/webm' })
  audioPath = URL.createObjectURL(blob)

  try {
    const result = await sttApi.transcribe({ audioPath })
    emit('transcribed', {
      text: result.text,
      audioPath,
      durationSec,
    })
    status.value = ''
  } catch (e: any) {
    status.value = `转写失败：${e.message}`
    setTimeout(() => (status.value = ''), 3000)
  } finally {
    // 转写完成后释放 blob URL（避免内存泄漏；30s 后清理给复制窗口留时间）
    audioPathTimeout = setTimeout(() => {
      if (audioPath) { URL.revokeObjectURL(audioPath); audioPath = '' }
    }, 30000)
  }
}

/** 清理 MediaRecorder 和 MediaStream tracks（防 mic 泄漏） */
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

// 组件卸载时强制清理（防止录音中切走导致 mic 常开）
onBeforeUnmount(() => {
  cleanupMedia()
  if (audioPathTimeout) clearTimeout(audioPathTimeout)
  if (audioPath) URL.revokeObjectURL(audioPath)
})
</script>

<style scoped>
.recorder-fab {
  position: fixed;
  right: var(--space-5);
  bottom: calc(var(--bottomnav-height) + var(--space-4));
  z-index: 15;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
}
.fab {
  width: 60px;
  height: 60px;
  border-radius: 50%;
  border: none;
  background: var(--brand-gradient);
  color: white;
  font-size: 24px;
  box-shadow: var(--shadow-lg);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}
.fab.recording { transform: scale(1.1); background: var(--danger); }
.pulse {
  position: absolute;
  width: 60px; height: 60px;
  border-radius: 50%;
  border: 2px solid var(--danger);
  animation: pulse 1.2s infinite;
}
@keyframes pulse {
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(1.8); opacity: 0; }
}
.status {
  background: var(--bg-card);
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  font-size: 12px;
  color: var(--text-secondary);
  box-shadow: var(--shadow-sm);
}
</style>
