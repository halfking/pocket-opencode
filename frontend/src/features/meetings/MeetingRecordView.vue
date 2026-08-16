<!-- S2.2 会议录音页：录音结束后转写并生成纪要。
     E5-S2：支持暂停/继续/取消；录音分片实时落盘（local_meeting_audio_parts），
     异常退出后可从会议详情恢复转写。 -->
<template>
      <div class="record-page">
      <header><h1>开始会议</h1><p>{{ statusText }}</p></header>

      <!-- 上次未完成的录音（应用被杀/页面退出时遗留） -->
      <div v-if="unfinished" class="recovery-card" role="status">
        <div class="recovery-main">
          <strong>检测到未完成的录音</strong>
          <span>{{ unfinished.title || '未命名会议' }} · 约 {{ durationText(unfinished.durationMs) }}</span>
        </div>
        <div class="recovery-actions">
          <button class="primary" @click="router.push(`/meetings/${unfinished.id}`)">去处理</button>
          <button class="ghost" @click="discardUnfinished">丢弃</button>
        </div>
      </div>

      <input v-model="title" class="title-input" aria-label="会议标题" placeholder="会议标题（可选）" :disabled="phase !== 'idle' || transcribing" />
      <MicStatusBar :state="micState" @retry="checkMic" @settings="openSettings" />

      <div class="timer" :class="{ paused: isPaused }">{{ elapsedText }}</div>
      <div class="controls">
        <button
          v-if="canPause && (isRecording || isPaused)"
          class="round secondary"
          :aria-label="isPaused ? '继续录音' : '暂停录音'"
          :aria-pressed="isPaused"
          @click="togglePause"
        >
          <span aria-hidden="true">{{ isPaused ? '▶' : '⏸' }}</span>
        </button>
        <button
          class="record-button"
          :class="{ active: isRecording || isPaused }"
          :aria-label="isRecording || isPaused ? '停止录音' : '开始录音'"
          :aria-pressed="isRecording || isPaused"
          :disabled="transcribing || mic.state.value === 'unavailable'"
          @click="toggleRecord"
        >
          <span aria-hidden="true">{{ isRecording || isPaused ? '⏹' : '🎙️' }}</span>
        </button>
        <button
          v-if="isRecording || isPaused"
          class="round secondary"
          aria-label="取消录音"
          @click="cancelRecord"
        >
          <span aria-hidden="true">✕</span>
        </button>
      </div>
      <p class="record-hint">{{ recordHint }}</p>

      <div v-if="transcribing" class="progress-card">正在转写会议录音…</div>
      <ErrorActionCard
        v-if="errorMessage"
        :message="errorMessage"
        :retry="true"
        :settings="mic.state.value === 'denied'"
        @retry="retryRecording"
        @settings="openSettings"
      />
    </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { sttApi } from '../../api/stt'
import {
  appendAudioPart, createMeeting, deleteAudioParts, discardMeeting,
  findUnfinishedMeetings, updateMeetingRecording, updateTranscript,
  type LocalMeeting,
} from './meetings-store'
import { blobToBase64, createElapsedTracker, type ElapsedTracker } from './recorderTiming'
import { useMicPermission } from '../../composables/useMicPermission'
import { useAppSettings } from '../../composables/useAppSettings'
import MicStatusBar from '../../components/MicStatusBar.vue'
import ErrorActionCard from '../../components/ErrorActionCard.vue'

const router = useRouter()
const mic = useMicPermission()
const { openAppDetails } = useAppSettings()
const micState = computed(() => mic.state.value)
const title = ref('')
const phase = ref<'idle' | 'recording' | 'paused' | 'transcribing'>('idle')
const transcribing = ref(false)
const errorMessage = ref('')
const elapsedMs = ref(0)
const unfinished = ref<LocalMeeting | null>(null)

const isRecording = computed(() => phase.value === 'recording')
const isPaused = computed(() => phase.value === 'paused')
const canPause = ref(true)

let recorder: MediaRecorder | null = null
let stream: MediaStream | null = null
let chunks: Blob[] = []
let startedAt = 0
let meetingId: string | null = null
let seq = 0
let cancelled = false
/** onstop 后是否立即转写（停止按钮=true；取消/页面退出=false，留待恢复） */
let finalizeOnStop = false
let tracker: ElapsedTracker = createElapsedTracker()
/** 分片落盘串行链：保证 seq 顺序，也便于停止前确认已全部持久化 */
let persistChain: Promise<void> = Promise.resolve()
let timer: ReturnType<typeof setInterval> | null = null

const elapsedText = computed(() => durationText(elapsedMs.value))
const statusText = computed(() => {
  if (transcribing.value) return '正在处理录音'
  if (isRecording.value) return '录音中'
  if (isPaused.value) return '已暂停'
  return '本地录音，结束后自动转写'
})
const recordHint = computed(() => {
  if (transcribing.value) return '转写完成后自动进入详情'
  if (isPaused.value) return '已暂停，点击 ⏹ 结束并转写，✕ 取消'
  if (isRecording.value) return '点击 ⏹ 结束并开始转写'
  return '点击开始录音'
})

function durationText(ms: number) {
  const seconds = Math.floor(ms / 1000)
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
}

async function checkMic() {
  await mic.recheck()
}

async function openSettings() {
  await openAppDetails()
}

async function retryRecording() {
  errorMessage.value = ''
  await checkMic()
}

// ---- 恢复横幅 ----

async function loadUnfinished() {
  try {
    const list = await findUnfinishedMeetings()
    unfinished.value = list[0] ?? null
  } catch (error) {
    console.warn('[meeting] load unfinished failed:', error)
  }
}

async function discardUnfinished() {
  if (!unfinished.value) return
  try {
    await discardMeeting(unfinished.value.id)
    unfinished.value = null
  } catch (error: any) {
    errorMessage.value = error?.message || '丢弃失败，请重试。'
  }
}

// ---- 录音状态机 ----

async function startRecord() {
  errorMessage.value = ''
  // 录音前先确保麦克风权限，被拒时给出具体引导（而非笼统的"检查权限"）
  const ok = await mic.ensure()
  if (!ok) {
    errorMessage.value = mic.deniedLabel.value || '无法访问麦克风，请检查权限设置。'
    return
  }
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
    recorder = new MediaRecorder(stream)
    canPause.value = typeof recorder.pause === 'function' && typeof recorder.resume === 'function'
    // 会议行在录音开始时创建：崩溃后分片有主可寻（E5-S2 恢复判据）
    const meeting = await createMeeting({ title: title.value.trim() || '未命名会议', startedAt: Date.now() })
    meetingId = meeting.id
    chunks = []
    seq = 0
    cancelled = false
    finalizeOnStop = false
    tracker = createElapsedTracker()
    tracker.start()
    startedAt = Date.now()
    elapsedMs.value = 0
    recorder.ondataavailable = onChunk
    recorder.onstop = onRecorderStop
    // timeslice 1.5s：周期性 flush 分片，进程被杀也不丢已录内容
    recorder.start(1500)
    phase.value = 'recording'
    if (timer) clearInterval(timer)
    timer = setInterval(() => { elapsedMs.value = tracker.elapsed() }, 250)
  } catch (error) {
    console.error('[meeting] microphone error:', error)
    // 启动半途失败（如 recorder.start 抛错）时丢弃已创建的会议行，
    // 否则列表会留下 0 时长的孤儿记录
    await cleanupFailedStart()
    errorMessage.value = '无法访问麦克风，请检查权限设置。'
  }
}

/** startRecord 失败兜底：停止轨道、丢弃半成品会议行与已落盘分片。 */
async function cleanupFailedStart() {
  cancelled = true
  try {
    if (recorder && recorder.state !== 'inactive') recorder.stop()
  } catch { /* already inactive */ }
  stopTracks()
  if (timer) clearInterval(timer)
  timer = null
  const id = meetingId
  try {
    await persistChain
    if (id) await discardMeeting(id)
  } catch (error) {
    console.warn('[meeting] cleanup failed start:', error)
  }
  resetState()
}

/** 每个 timeslice 分片：内存留一份（停止时快路径转写），同时落盘一份（崩溃恢复）。 */
function onChunk(event: BlobEvent) {
  if (event.data.size === 0) return
  chunks.push(event.data)
  if (!meetingId || cancelled) return
  const partSeq = seq++
  const blob = event.data
  const currentMeeting = meetingId
  persistChain = persistChain
    .then(async () => {
      if (cancelled) return
      const base64 = await blobToBase64(blob)
      await appendAudioPart({
        meetingId: currentMeeting,
        seq: partSeq,
        mimeType: blob.type || recorder?.mimeType || 'audio/webm',
        dataBase64: base64,
      })
      // durationMs 随分片推进：崩溃后列表/恢复显示的时长也准确
      await updateMeetingRecording(currentMeeting, { durationMs: tracker.elapsed() })
    })
    .catch((error) => console.warn('[meeting] persist part failed:', error))
}

function onRecorderStop() {
  if (cancelled || !finalizeOnStop) return
  void finishRecord()
}

function stopTracks() {
  stream?.getTracks().forEach((track) => track.stop())
  stream = null
}

/** 停止（⏹）：落盘最后一拍后立即转写。 */
function stopRecord() {
  if (!recorder || recorder.state === 'inactive') return
  finalizeOnStop = true
  recorder.stop()
  stopTracks()
  if (timer) clearInterval(timer)
  timer = null
  transcribing.value = true
}

function toggleRecord() {
  if (isRecording.value || isPaused.value) stopRecord()
  else void startRecord()
}

function togglePause() {
  if (!recorder) return
  if (isRecording.value) {
    try {
      recorder.pause()
      tracker.pause()
      phase.value = 'paused'
    } catch (error) {
      // 部分内核不支持 pause：隐藏按钮，退化为只有开始/停止
      console.warn('[meeting] pause unsupported:', error)
      canPause.value = false
    }
  } else if (isPaused.value) {
    try {
      recorder.resume()
      tracker.start()
      phase.value = 'recording'
    } catch (error) {
      console.warn('[meeting] resume failed:', error)
    }
  }
}

/** 取消（✕）：丢弃本次录音（会议行 + 已落盘分片）。 */
async function cancelRecord() {
  cancelled = true
  finalizeOnStop = false
  try {
    if (recorder && recorder.state !== 'inactive') recorder.stop()
  } catch { /* already inactive */ }
  stopTracks()
  if (timer) clearInterval(timer)
  timer = null
  const id = meetingId
  try {
    await persistChain
    if (id) await discardMeeting(id)
  } catch (error) {
    console.warn('[meeting] discard failed:', error)
  }
  resetState()
}

function resetState() {
  phase.value = 'idle'
  transcribing.value = false
  elapsedMs.value = 0
  meetingId = null
  seq = 0
  cancelled = false
  finalizeOnStop = false
  chunks = []
  recorder = null
  persistChain = Promise.resolve()
}

async function finishRecord() {
  const id = meetingId
  try {
    await persistChain
    if (chunks.length === 0 || !id) {
      if (id) await discardMeeting(id)
      errorMessage.value = '没有录到有效音频。'
      resetState()
      return
    }
    const blob = new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' })
    const result = await sttApi.transcribe({ audioBlob: blob, forceEngine: 'cloud' })
    await updateTranscript(id, result.text)
    await updateMeetingRecording(id, { durationMs: tracker.elapsed() })
    // 转写已入库，音频分片不再需要（回收空间；失败路径保留供恢复）
    await deleteAudioParts(id)
    router.replace(`/meetings/${id}`)
  } catch (error: any) {
    console.error('[meeting] transcription failed:', error)
    // 分片与会议行保留：详情页可"继续转写"恢复
    errorMessage.value = error?.message || '转写失败，录音已保留，可稍后在会议详情中恢复。'
    resetState()
  }
}

onMounted(loadUnfinished)

onBeforeUnmount(() => {
  if (isRecording.value || isPaused.value) {
    // 页面退出（非进程死亡）：停止采集，分片已在本地，稍后可恢复
    finalizeOnStop = false
    try {
      if (recorder && recorder.state !== 'inactive') recorder.stop()
    } catch { /* already inactive */ }
    stopTracks()
  }
  if (timer) clearInterval(timer)
  timer = null
})
</script>

<style scoped>
.record-page { padding: 20px 16px 100px; text-align: center; }
h1 { margin: 0; font-size: 24px; }
header p { color: var(--text-secondary); font-size: 12px; margin: 6px 0 18px; }
.recovery-card { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 0 0 14px; padding: 12px 14px; border: 1px solid var(--border); border-radius: var(--radius-lg); background: var(--bg-card); text-align: left; }
.recovery-main { display: grid; gap: 3px; min-width: 0; }
.recovery-main strong { font-size: 14px; }
.recovery-main span { color: var(--text-secondary); font-size: 12px; }
.recovery-actions { display: flex; gap: 8px; flex-shrink: 0; }
.title-input { width: 100%; box-sizing: border-box; padding: 11px 13px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-card); color: var(--text-primary); font-size: 14px; }
.title-input:focus-visible { outline: none; box-shadow: 0 0 0 2px var(--brand-primary); }
.timer { margin: 40px 0 18px; font-size: 42px; font-variant-numeric: tabular-nums; font-weight: 700; }
.timer.paused { color: var(--text-secondary); }
.controls { display: flex; align-items: center; justify-content: center; gap: 18px; }
.record-button { width: 88px; height: 88px; border-radius: 50%; border: 0; background: var(--brand-primary); color: var(--text-inverse); font-size: 38px; box-shadow: var(--shadow-lg); cursor: pointer; }
.record-button.active { background: var(--danger, #dc2626); }
.record-button:disabled { opacity: .55; cursor: wait; }
.round { width: 56px; height: 56px; border-radius: 50%; border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary); font-size: 20px; cursor: pointer; }
.round:active { background: var(--bg-subtle, #f5f5f5); }
.record-hint { color: var(--text-secondary); font-size: 12px; }
.progress-card, .error-card, .transcript-card { margin-top: 24px; padding: 14px; border-radius: var(--radius-lg); text-align: left; background: var(--bg-card); }
.primary { border: 0; border-radius: var(--radius-md); background: var(--brand-primary); color: var(--text-inverse); padding: 8px 12px; font-size: 13px; cursor: pointer; }
.ghost { border: 1px solid var(--border); border-radius: var(--radius-md); background: transparent; color: var(--text-primary); padding: 8px 12px; font-size: 13px; cursor: pointer; }
</style>
