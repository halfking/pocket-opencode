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
import { ref } from 'vue'
import { sttApi } from '../../api/stt'

const emit = defineEmits<{
  transcribed: [result: { text: string; audioPath: string; durationSec: number }]
}>()

const recording = ref(false)
const status = ref('')
let mediaRecorder: MediaRecorder | null = null
let chunks: Blob[] = []
let startedAt = 0
let audioPath = '' // in production: write to a file and pass its path

async function start() {
  if (recording.value) return
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
    mediaRecorder = new MediaRecorder(stream)
    chunks = []
    mediaRecorder.ondataavailable = (e) => e.data.size > 0 && chunks.push(e.data)
    mediaRecorder.start()
    startedAt = Date.now()
    recording.value = true
    status.value = '录音中…'
  } catch (e) {
    status.value = '麦克风权限被拒绝'
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
  mediaRecorder?.stream.getTracks().forEach((t) => t.stop())

  // TODO: persist blob to a file and set audioPath. For now pass a placeholder.
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
  }
}
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
