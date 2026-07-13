<!-- S2.2 会议录音页：录音结束后转写并生成纪要。 -->
<template>
  <AppLayout>
    <div class="record-page">
      <header><h1>开始会议</h1><p>{{ statusText }}</p></header>

      <input v-model="title" class="title-input" placeholder="会议标题（可选）" :disabled="recording || transcribing" />

      <div class="timer">{{ elapsedText }}</div>
      <button class="record-button" :class="{ active: recording }" :disabled="transcribing" @click="toggleRecord">
        {{ recording ? '⏹' : '🎙️' }}
      </button>
      <p class="record-hint">{{ recording ? '点击停止并开始转写' : '点击开始录音' }}</p>

      <div v-if="transcribing" class="progress-card">正在转写会议录音…</div>
      <div v-if="errorMessage" class="error-card">{{ errorMessage }}</div>

      <section v-if="transcript" class="transcript-card">
        <h2>会议转写</h2>
        <p>{{ transcript }}</p>
        <button class="primary" :disabled="summarizing" @click="makeSummary">
          {{ summarizing ? '生成中…' : '生成会议纪要' }}
        </button>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { sttApi } from '../../api/stt'
import { createMeeting, updateTranscript, updateMeetingRecording, updateSummary } from './meetings-store'
import { summarizeMeeting } from './meetings-ai'

const router = useRouter()
const title = ref('')
const recording = ref(false)
const transcribing = ref(false)
const summarizing = ref(false)
const transcript = ref('')
const errorMessage = ref('')
const elapsedMs = ref(0)
let recorder: MediaRecorder | null = null
let stream: MediaStream | null = null
let chunks: Blob[] = []
let startedAt = 0
let timer: ReturnType<typeof setInterval> | null = null

const elapsedText = computed(() => {
  const seconds = Math.floor(elapsedMs.value / 1000)
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
})
const statusText = computed(() => {
  if (transcribing.value) return '正在处理录音'
  if (summarizing.value) return '正在生成纪要'
  return recording.value ? '录音中' : '本地录音，结束后自动转写'
})

async function startRecord() {
  errorMessage.value = ''
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
    recorder = new MediaRecorder(stream)
    chunks = []
    startedAt = Date.now()
    elapsedMs.value = 0
    recorder.ondataavailable = (event) => {
      if (event.data.size > 0) chunks.push(event.data)
    }
    recorder.onstop = finishRecord
    recorder.start()
    recording.value = true
    timer = setInterval(() => { elapsedMs.value = Date.now() - startedAt }, 250)
  } catch (error) {
    console.error('[meeting] microphone error:', error)
    errorMessage.value = '无法访问麦克风，请检查权限设置。'
  }
}

function stopRecord() {
  if (!recorder || recorder.state === 'inactive') return
  recorder.stop()
  recording.value = false
  if (timer) clearInterval(timer)
  timer = null
  stream?.getTracks().forEach((track) => track.stop())
  stream = null
}

function toggleRecord() {
  if (recording.value) stopRecord()
  else void startRecord()
}

async function finishRecord() {
  if (chunks.length === 0) {
    errorMessage.value = '没有录到有效音频。'
    return
  }
  transcribing.value = true
  const blob = new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' })
  const durationMs = Math.max(elapsedMs.value, Date.now() - startedAt)
  try {
    const meeting = await createMeeting({ title: title.value.trim() || '未命名会议', durationMs, startedAt })
    const result = await sttApi.transcribe({ audioBlob: blob, forceEngine: 'cloud' })
    transcript.value = result.text
    await updateTranscript(meeting.id, result.text)
    await updateMeetingRecording(meeting.id, { durationMs })
    router.replace(`/meetings/${meeting.id}`)
  } catch (error: any) {
    console.error('[meeting] transcription failed:', error)
    errorMessage.value = error?.message || '转写失败，请稍后重试。'
  } finally {
    transcribing.value = false
  }
}

async function makeSummary() {
  if (!transcript.value) return
  summarizing.value = true
  try {
    const summary = await summarizeMeeting(transcript.value)
    // 录音完成后已经跳转详情；此按钮主要为异常/快速操作兜底。
    const meetingId = router.currentRoute.value.params.id as string
    if (meetingId) await updateSummary(meetingId, summary)
    router.push(`/meetings/${meetingId}`)
  } catch (error: any) {
    errorMessage.value = error?.message || '纪要生成失败。'
  } finally {
    summarizing.value = false
  }
}

onBeforeUnmount(() => {
  if (recording.value) stopRecord()
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.record-page { padding: 20px 16px 100px; text-align: center; }
h1 { margin: 0; font-size: 24px; }
header p { color: var(--text-secondary); font-size: 12px; margin: 6px 0 18px; }
.title-input { width: 100%; box-sizing: border-box; padding: 11px 13px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg-card); font-size: 14px; }
.timer { margin: 40px 0 18px; font-size: 42px; font-variant-numeric: tabular-nums; font-weight: 700; }
.record-button { width: 88px; height: 88px; border-radius: 50%; border: 0; background: var(--brand-primary); color: #fff; font-size: 38px; box-shadow: 0 8px 22px rgba(37, 99, 235, .28); cursor: pointer; }
.record-button.active { background: var(--danger, #dc2626); }
.record-button:disabled { opacity: .55; cursor: wait; }
.record-hint { color: var(--text-secondary); font-size: 12px; }
.progress-card, .error-card, .transcript-card { margin-top: 24px; padding: 14px; border-radius: 10px; text-align: left; background: var(--bg-card); }
.error-card { color: var(--danger); }
.transcript-card h2 { font-size: 16px; margin: 0 0 8px; }
.transcript-card p { white-space: pre-wrap; line-height: 1.6; font-size: 13px; }
.primary { border: 0; border-radius: 8px; background: var(--brand-primary); color: white; padding: 10px 14px; cursor: pointer; }
.primary:disabled { opacity: .6; }
</style>
