<template>
  <div class="studio">
    <div v-if="loading" class="state"><Skeleton :count="3" /></div>
    <EmptyState
      v-else-if="!meeting"
      icon="⚠️"
      title="会议不存在"
      action-label="返回列表"
      @action="router.push('/meetings')"
    />
    <template v-else>
      <HeaderActionsPortal>
        <WaveformVisualizer
          v-if="micOn"
          :is-recording="true"
          :width="88"
          :height="28"
          color="var(--danger)"
          :show-time="false"
          :show-progress="false"
        />
        <button type="button" class="hdr" :disabled="summarizing || !displaySegments.length" @click="onSummarize">
          {{ summarizing ? '总结中' : '总结' }}
        </button>
        <button type="button" class="hdr icon" aria-label="会议设置" @click="showSettings = true">
          <span class="material-symbols-outlined">settings</span>
        </button>
      </HeaderActionsPortal>

      <p v-if="statusLine" class="status" role="status">{{ statusLine }}</p>
      <p v-if="sttError" class="err" role="alert">{{ sttError }}</p>

      <div class="split">
        <section class="transcript">
          <TranscriptSegmentList :segments="displaySegments" :is-recording="micOn" />
        </section>
        <MeetingInsightPanel
          :summary="liveSummary || meeting.liveSummary"
          :final-summary="meeting.summary"
          :recommendations="mergedRecs"
          :is-updating="isUpdating || summarizing"
          :is-recording="micOn"
          @share-todo="onShareTodo"
          @acc-todo="onAccTodo"
          @open-related="onOpenRelated"
        />
      </div>

      <MeetingMicDock :recording="micOn" :busy="micBusy" @toggle="onMicToggle" />
      <button
        v-if="micOn && speakers.length"
        type="button"
        class="speakers-btn"
        @click="showSpeakers = true"
      >👤 说话人</button>
      <SpeakerLabelSheet
        :open="showSpeakers"
        :speakers="speakers"
        @close="showSpeakers = false"
        @label="onSpeakerLabel"
      />
      <MeetingSettingsSheet
        :open="showSettings"
        :title="meeting.title"
        :topic="meeting.topic"
        :location="meeting.location"
        :participants="meeting.participants"
        :tags="meeting.tags"
        :summary-skill="meeting.summarySkill"
        :started-at="meeting.startedAt"
        :transcript-hint="displaySegments[0]?.text"
        @close="showSettings = false"
        @save="onMetaSave"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { EmptyState, Skeleton, WaveformVisualizer } from '@/components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useToast } from '../../composables/useToast'
import { getMeetingWithSegments, updateMeeting, type LocalMeeting, type MeetingSegment, type RecommendItem } from './meetings-store'
import { summarizeMeeting } from './meetings-ai'
import { buildSummaryPrompt } from './meeting-skills'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'
import { mergeRecommendations, relatedQueryFromTranscript } from './meeting-related'
import { searchRelatedContext } from './meeting-related-search'
import { createMeetingTodos, handoffTodoToAcc, shareTodoWithPerson } from './meeting-todo-persist'
import type { MeetingTodoDraft } from './meeting-todos'
import TranscriptSegmentList from './TranscriptSegmentList.vue'
import MeetingInsightPanel from './MeetingInsightPanel.vue'
import MeetingMicDock from './MeetingMicDock.vue'
import MeetingSettingsSheet from './MeetingSettingsSheet.vue'
import SpeakerLabelSheet from './SpeakerLabelSheet.vue'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const meetingId = computed(() => String(route.params.id || ''))
const loading = ref(true)
const meeting = ref<LocalMeeting | null>(null)
const storedSegments = ref<MeetingSegment[]>([])
const showSettings = ref(false)
const showSpeakers = ref(false)
const summarizing = ref(false)
const micBusy = ref(false)
const wantMic = ref(false)
const noteRecs = ref<RecommendItem[]>([])
let relatedTimer: ReturnType<typeof setTimeout> | null = null

const {
  isRecording, segments, sttError, speakers, start, stop, formatElapsed, labelSpeaker,
} = useMeetingRecorder(meetingId)
const { liveSummary, recommendations, isUpdating, refresh } = useLiveSummary(meetingId, segments, {
  meta: computed(() => ({
    title: meeting.value?.title ?? undefined,
    participants: meeting.value?.participants,
    location: meeting.value?.location ?? undefined,
  })),
})

const micOn = computed(() => wantMic.value || isRecording.value)
const displaySegments = computed(() => (segments.value.length ? segments.value : storedSegments.value))
const mergedRecs = computed(() => mergeRecommendations(recommendations.value, noteRecs.value))
const statusLine = computed(() => {
  if (micOn.value) return `录音中 ${formatElapsed()}`
  if (meeting.value?.location) return meeting.value.location
  return ''
})

async function load() {
  loading.value = true
  const data = await getMeetingWithSegments(meetingId.value)
  meeting.value = data?.meeting ?? null
  storedSegments.value = data?.segments ?? []
  loading.value = false
}

async function captureContext(m: LocalMeeting) {
  const loc = m.location || await captureDeviceLocation()
  const title = m.title || formatCapturedTitle({
    startedAt: m.startedAt, location: loc, topic: m.topic,
  })
  if (loc !== m.location || title !== m.title) {
    await updateMeeting(m.id, { location: loc, title })
    meeting.value = { ...m, location: loc, title }
  }
}

async function onMicToggle() {
  if (micBusy.value) return
  micBusy.value = true
  if (micOn.value) {
    wantMic.value = false
    try { await stop(); await refresh(true); await load() }
    finally { micBusy.value = false }
    return
  }
  wantMic.value = true
  try {
    if (storedSegments.value.length) {
      segments.value.splice(0, segments.value.length, ...storedSegments.value)
    }
    const ok = await start({ resume: storedSegments.value.length > 0 })
    if (!ok) wantMic.value = false
  } finally { micBusy.value = false }
}

async function onSummarize() {
  const segs = displaySegments.value
  if (!meeting.value || !segs.length) return
  summarizing.value = true
  try {
    const transcript = segs.map((s) => `[${s.speakerLabel || '说话人'}] ${s.text}`).join('\n')
    const summary = await summarizeMeeting(transcript, {
      skillPrompt: buildSummaryPrompt(meeting.value.summarySkill),
    })
    await updateMeeting(meetingId.value, { summary })
    const items = liveSummary.value?.actionItems ?? []
    if (items.length) await createMeetingTodos(meetingId.value, items, meeting.value.noteId)
    await load()
    toast.success('已生成当前总结')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '总结失败')
  } finally { summarizing.value = false }
}

async function onSpeakerLabel(profileId: string, displayName: string) {
  await labelSpeaker(profileId, displayName)
}

async function onMetaSave(data: {
  title: string; topic: string; location: string; participants: string[]; tags: string[]; summarySkill: string
}) {
  await updateMeeting(meetingId.value, data)
  await load()
}

async function onShareTodo(draft: MeetingTodoDraft) {
  await shareTodoWithPerson(draft, meeting.value?.title || '')
  toast.success('已生成转交内容')
}

async function onAccTodo(draft: MeetingTodoDraft) {
  const task = await handoffTodoToAcc(draft, meeting.value?.title || '')
  toast.success('已转交 ACC')
  router.push(`/settings/scheduled-tasks/${task.id}`)
}

function onOpenRelated(item: RecommendItem) {
  if (item.url) {
    window.open(item.url, '_blank', 'noopener')
    return
  }
  if (item.type === 'note') router.push(`/notes/${item.id}`)
  if (item.type === 'meeting' || item.type === 'knowledge') {
    router.push({ name: 'meeting-detail', params: { id: item.id } })
  }
}

async function refreshRelated() {
  const q = relatedQueryFromTranscript(displaySegments.value.map((s) => s.text))
  noteRecs.value = await searchRelatedContext(q, { excludeMeetingId: meetingId.value })
}

onMounted(async () => {
  await load()
  const m = meeting.value
  if (!m) return
  if (m.recommendations?.length) noteRecs.value = m.recommendations
  await captureContext(m)
  const auto = route.query.record === '1' || m.status === 'recording'
  if (auto) await onMicToggle()
  await refreshRelated()
})

watch(() => displaySegments.value.length, (count) => {
  if (count === 0) return
  if (relatedTimer) clearTimeout(relatedTimer)
  relatedTimer = setTimeout(() => { void refreshRelated() }, 2500)
})

onUnmounted(() => {
  if (relatedTimer) clearTimeout(relatedTimer)
})

</script>

<style scoped>
.studio { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.state { padding: var(--space-3); }
.status, .err { margin: 0; padding: 8px 12px; font-size: 12px; flex-shrink: 0; }
.status { color: var(--text-secondary); background: var(--bg-card); }
.err { color: var(--danger); }
.split {
  flex: 1; min-height: 0; display: grid;
  grid-template-columns: minmax(0, 7fr) minmax(140px, 3fr);
}
.transcript { min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
.hdr {
  min-height: 36px; padding: 0 10px; border: none; background: transparent;
  color: var(--brand-primary); font-weight: 600; font-size: 13px;
}
.hdr.icon { width: 44px; }
.hdr:disabled { opacity: 0.45; }
.speakers-btn {
  position: fixed; left: var(--space-4); bottom: calc(var(--app-safe-bottom, 12px) + var(--space-4));
  height: 40px; padding: 0 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); z-index: var(--z-fab); font-size: 13px;
}
@media (max-width: 720px) {
  .split { grid-template-columns: 1fr; grid-template-rows: minmax(0, 7fr) minmax(140px, 3fr); }
  .insight-border { border-left: none; }
}
</style>
