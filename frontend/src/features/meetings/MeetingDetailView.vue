<template>
  <div class="detail-view">
    <div v-if="loading" class="state"><Skeleton :count="3" /></div>

    <EmptyState
      v-else-if="!data"
      icon="⚠️"
      title="会议不存在"
      action-label="返回列表"
      @action="router.push('/meetings')"
    />

    <template v-else>
      <ScrollChromePortal>
        <div class="detail-toolbar">
          <button
            type="button"
            class="refine-btn"
            :disabled="refining"
            @click="onRefine"
          >
            {{ refining ? '精翻中…' : '✨ 精翻' }}
          </button>
        </div>
      </ScrollChromePortal>

      <div class="detail-content">
        <h2 class="detail-title">{{ data.meeting.title || '未命名会议' }}</h2>
        <div class="detail-meta">
          <span>{{ formatDate(data.meeting.startedAt) }}</span>
          <span v-if="data.meeting.durationMs">{{ formatDuration(data.meeting.durationMs) }}</span>
          <span v-if="data.meeting.location">📍 {{ data.meeting.location }}</span>
        </div>

        <!-- 波形回放 -->
        <div v-if="data.meeting.audioPath" class="waveform-section">
          <WaveformVisualizer
            :waveform-data="waveformData"
            :is-playing="isPlaying"
            :current-time="playTime"
            :duration="playDuration"
            :width="waveWidth"
            :height="56"
            @click="seekTo"
          />
          <button type="button" class="play-btn" @click="togglePlay">
            {{ isPlaying ? '⏸' : '▶' }}
          </button>
        </div>

        <!-- 入库结果 -->
        <div v-if="ingestMsg" class="ingest-banner">{{ ingestMsg }}</div>

        <div v-if="data.meeting.noteId" class="note-link-row">
          <router-link :to="`/notes/${data.meeting.noteId}`" class="note-link">
            📝 查看关联笔记
          </router-link>
        </div>

        <!-- AI 纪要 -->
        <section v-if="data.meeting.summary || data.meeting.liveSummary" class="section">
          <h3 class="section-title">📋 会议纪要</h3>
          <p class="summary-text">
            {{ data.meeting.summary || data.meeting.liveSummary?.summary }}
          </p>
          <ul v-if="data.meeting.liveSummary?.actionItems?.length" class="action-list">
            <li v-for="(a, i) in data.meeting.liveSummary.actionItems" :key="i">
              ✅ {{ a.text }}
              <span v-if="a.due" class="due">（{{ a.due }}）</span>
            </li>
          </ul>
        </section>

        <!-- 精翻结果 -->
        <section v-if="data.meeting.refinedTranscript" class="section">
          <h3 class="section-title">✨ 精翻文稿</h3>
          <pre class="refined-text">{{ data.meeting.refinedTranscript }}</pre>
        </section>

        <!-- 完整转写 -->
        <section class="section">
          <h3 class="section-title">📝 完整转写</h3>
          <TranscriptSegmentList :segments="data.segments" />
        </section>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Skeleton, EmptyState, WaveformVisualizer } from '@/components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import { meetingsApi } from '../../api/meetings'
import { loadMeetingAudio } from '../../native/meeting-audio'
import { ingestMeetingArtifacts } from './meeting-ingest'
import {
  getMeetingWithSegments, updateMeeting,
  type LocalMeeting, type MeetingSegment,
} from './meetings-store'
import TranscriptSegmentList from './TranscriptSegmentList.vue'

const route = useRoute()
const router = useRouter()
const meetingId = route.params.id as string

const loading = ref(true)
const refining = ref(false)
const ingestMsg = ref('')
const data = ref<{ meeting: LocalMeeting; segments: MeetingSegment[] } | null>(null)
const waveWidth = ref(window.innerWidth - 32)
const waveformData = ref<number[]>([])
const isPlaying = ref(false)
const playTime = ref(0)
const playDuration = ref(0)
let audioEl: HTMLAudioElement | null = null

async function load() {
  loading.value = true
  data.value = await getMeetingWithSegments(meetingId)
  loading.value = false
  if (!data.value) return

  // 优先 IndexedDB，fallback 到 DB 中的 blob URL
  const idbUrl = await loadMeetingAudio(meetingId)
  const audioPath = idbUrl ?? data.value.meeting.audioPath
  if (audioPath) initAudio(audioPath)
}

function initAudio(path: string) {
  audioEl = new Audio(path)
  audioEl.addEventListener('loadedmetadata', () => {
    playDuration.value = audioEl!.duration
  })
  audioEl.addEventListener('timeupdate', () => {
    playTime.value = audioEl!.currentTime
  })
  audioEl.addEventListener('ended', () => { isPlaying.value = false })
}

function togglePlay() {
  if (!audioEl) return
  if (isPlaying.value) {
    audioEl.pause()
    isPlaying.value = false
  } else {
    audioEl.play()
    isPlaying.value = true
  }
}

function seekTo(time: number) {
  if (audioEl) audioEl.currentTime = time
}

async function onRefine() {
  if (!data.value) return
  refining.value = true
  ingestMsg.value = ''
  try {
    await updateMeeting(meetingId, { status: 'processing' })
    const m = data.value.meeting
    const result = await meetingsApi.refine(
      meetingId,
      data.value.segments,
      ['en'],
      {
        title: m.title ?? undefined,
        location: m.location ?? undefined,
        participants: m.participants,
      },
    )
    const ingested = await ingestMeetingArtifacts(m, result)
    const parts: string[] = []
    if (ingested.noteId) parts.push('已生成笔记')
    if (ingested.todosCreated > 0) parts.push(`${ingested.todosCreated} 条待办`)
    if (result.tasksCreated) parts.push(`${result.tasksCreated} 个任务已同步`)
    if (ingested.cloudSynced) parts.push('已云同步')
    ingestMsg.value = parts.length ? parts.join(' · ') : '精翻完成'
    await load()
  } finally {
    refining.value = false
  }
}

function formatDate(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN')
}

function formatDuration(ms: number): string {
  const m = Math.floor(ms / 60000)
  const s = Math.floor((ms % 60000) / 1000)
  return `${m}分${s}秒`
}

onMounted(load)
onUnmounted(() => { if (audioEl) { audioEl.pause(); audioEl = null } })
</script>

<style scoped>
.detail-view {
  min-height: 100%;
}

.detail-toolbar {
  display: flex;
  justify-content: flex-end;
  padding: var(--space-2) var(--space-3);
}

.refine-btn {
  padding: 6px 14px;
  border: 1px solid var(--brand-primary);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--brand-primary);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.refine-btn:disabled {
  opacity: 0.5;
}

.detail-content {
  padding: var(--space-3);
}

.detail-title {
  margin: 0 0 var(--space-2);
  font-size: 20px;
  font-weight: 700;
}

.detail-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  font-size: 13px;
  color: var(--text-muted);
  margin-bottom: var(--space-4);
}

.waveform-section {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
  padding: var(--space-3);
  background: var(--bg-card);
  border-radius: var(--radius-md);
}

.play-btn {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: none;
  background: var(--brand-primary);
  color: var(--text-inverse);
  font-size: 16px;
  cursor: pointer;
  flex-shrink: 0;
}

.section {
  margin-bottom: var(--space-4);
}

.section-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 var(--space-2);
  color: var(--text-secondary);
}

.summary-text {
  font-size: 14px;
  line-height: 1.7;
  color: var(--text-primary);
  margin: 0;
}

.action-list {
  margin: var(--space-3) 0 0;
  padding-left: var(--space-4);
  font-size: 13px;
  line-height: 1.8;
}

.due {
  color: var(--brand-primary);
  font-size: 12px;
}

.refined-text {
  white-space: pre-wrap;
  font-size: 14px;
  line-height: 1.7;
  color: var(--text-primary);
  background: var(--bg-subtle);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  margin: 0;
}

.ingest-banner {
  padding: 10px 14px;
  margin-bottom: var(--space-3);
  background: var(--success-bg);
  border: 1px solid rgba(34, 197, 94, 0.3);
  border-radius: var(--radius-md);
  font-size: 13px;
  color: var(--success);
}

.note-link-row {
  margin-bottom: var(--space-3);
}

.note-link {
  font-size: 14px;
  font-weight: 600;
  color: var(--brand-primary);
  text-decoration: none;
}
</style>
