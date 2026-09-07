<template>
  <div class="meetings-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="会议记录需要本地加密数据库"
      @relogin="router.push('/login')"
    />

    <template v-else>
      <div class="filter-row">
        <button
          v-for="f in filters"
          :key="f.id"
          type="button"
          class="chip"
          :class="{ active: filter === f.id }"
          @click="onFilter(f.id)"
        >{{ f.label }}</button>
      </div>

      <PullToRefresh :on-refresh="load" class="list-scroll">
        <div v-if="loading" class="state"><Skeleton :count="4" /></div>
        <EmptyState
          v-else-if="meetings.length === 0"
          icon="🎙️"
          :title="filter === 'archived' ? '没有已归档会议' : '还没有会议记录'"
          hint="点击右下角麦克风开始第一次会议"
          size="sm"
          variant="inline"
        />
        <div v-else class="meeting-list">
          <SwipeableListItem
            v-for="m in meetings"
            :key="m.id"
            :left-actions="filter === 'archived'
              ? [{ id: 'restore', icon: '↩', label: '恢复', type: 'success', onAction: () => onRestore(m.id) }]
              : [{ id: 'archive', icon: '📦', label: '归档', type: 'primary', onAction: () => onArchive(m.id) }]"
            :right-actions="[{ id: 'delete', icon: '🗑', label: '删除', type: 'danger', onAction: () => onDelete(m.id) }]"
            @activate="openMeeting(m)"
          >
            <div class="meeting-card">
              <div class="card-header">
                <h3 class="card-title">{{ m.title || '未命名会议' }}</h3>
                <span class="status-badge" :class="m.status">{{ statusText(m.status) }}</span>
              </div>
              <p v-if="m.topic && m.topic !== m.title" class="card-topic">{{ m.topic }}</p>
              <p v-else-if="m.summary" class="card-summary">{{ m.summary.slice(0, 80) }}</p>
              <div class="card-meta">
                <span>{{ formatMeetingWhen(m.startedAt) }}</span>
                <span v-if="m.durationMs">{{ formatDuration(m.durationMs) }}</span>
                <span v-if="formatLocationLine(m.location)">{{ formatLocationLine(m.location) }}</span>
                <span v-if="formatParticipants(m.participants)">👤 {{ formatParticipants(m.participants) }}</span>
              </div>
            </div>
          </SwipeableListItem>
        </div>
      </PullToRefresh>

      <button
        class="fab"
        :class="{ recording: starting }"
        type="button"
        :aria-label="starting ? '正在进入录音' : '开始会议录音'"
        :disabled="starting"
        @click="startNewMeeting"
      >🎙</button>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  Skeleton, EmptyState, DbLockedState, PullToRefresh, SwipeableListItem,
} from '@/components'
import {
  listMeetings, createMeeting, deleteMeeting, archiveMeeting, unarchiveMeeting,
  type LocalMeeting,
} from './meetings-store'
import { deleteMeetingAudio } from '../../native/meeting-audio'
import {
  formatDuration, formatLocationLine, formatMeetingWhen, formatParticipants, statusText,
  type MeetingListFilter,
} from './meeting-list'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'

const router = useRouter()
const dbNotReady = ref(false)
const loading = ref(true)
const starting = ref(false)
const filter = ref<MeetingListFilter>('active')
const meetings = ref<LocalMeeting[]>([])
const filters: Array<{ id: MeetingListFilter; label: string }> = [
  { id: 'active', label: '进行中' },
  { id: 'archived', label: '已归档' },
]

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    meetings.value = await listMeetings(50, { archived: filter.value === 'archived' })
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e)
    if (msg.includes('LocalDB 未初始化')) dbNotReady.value = true
  } finally {
    loading.value = false
  }
}

async function onFilter(next: MeetingListFilter) {
  filter.value = next
  await load()
}

async function startNewMeeting() {
  if (starting.value) return
  starting.value = true
  try {
    const startedAt = Date.now()
    const location = await captureDeviceLocation()
    const m = await createMeeting({
      startedAt,
      location: location ?? undefined,
      title: formatCapturedTitle({ startedAt, location }),
    })
    await router.push({ name: 'meeting-detail', params: { id: m.id }, query: { record: '1' } })
  } finally {
    starting.value = false
  }
}

function openMeeting(m: LocalMeeting) {
  router.push({ name: 'meeting-detail', params: { id: m.id } })
}

async function onArchive(id: string) {
  await archiveMeeting(id)
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

async function onRestore(id: string) {
  await unarchiveMeeting(id)
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

async function onDelete(id: string) {
  await deleteMeeting(id)
  try { await deleteMeetingAudio(id) } catch { /* ok */ }
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

onMounted(load)
</script>

<style scoped>
.meetings-page { position: relative; height: 100%; min-height: 0; display: flex; flex-direction: column; }
.filter-row { display: flex; gap: 8px; padding: var(--space-3) var(--space-3) 0; flex-shrink: 0; }
.chip {
  padding: 6px 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); color: var(--text-secondary); font-size: 13px;
}
.chip.active { background: var(--brand-bg); color: var(--brand-primary); border-color: var(--brand-primary); }
.list-scroll { flex: 1 1 auto; min-height: 0; }
.meeting-list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.meeting-card { padding: var(--space-3); background: var(--bg-card); }
.card-header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.card-title { margin: 0; font-size: 15px; font-weight: 600; color: var(--text-primary); }
.status-badge { font-size: 11px; padding: 2px 8px; border-radius: var(--radius-full); background: var(--bg-subtle); color: var(--text-muted); flex-shrink: 0; }
.status-badge.recording { background: var(--danger-bg); color: var(--danger); }
.card-topic, .card-summary { margin: var(--space-2) 0 0; font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.card-meta { display: flex; flex-wrap: wrap; gap: var(--space-3); margin-top: var(--space-2); font-size: 12px; color: var(--text-muted); }
.fab {
  position: fixed; right: var(--space-4); bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  width: 56px; height: 56px; border-radius: 50%; border: none;
  background: var(--brand-gradient, linear-gradient(135deg, #667eea, #764ba2));
  color: var(--text-inverse); font-size: 24px; box-shadow: var(--shadow-lg, 0 4px 20px rgba(0,0,0,0.2));
  z-index: var(--z-fab);
}
.fab.recording { background: var(--danger); }
.fab:disabled { opacity: 0.7; }
</style>
