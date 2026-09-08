<template>
  <div class="meetings-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="会议记录需要本地加密数据库"
      @relogin="router.push('/login')"
    />

    <template v-else>
      <HeaderActionsPortal>
        <button
          v-for="f in filters"
          :key="f.id"
          type="button"
          class="hdr-filter"
          :class="{ active: filter === f.id }"
          :aria-pressed="filter === f.id"
          @click="onFilter(f.id)"
        >{{ f.label }}</button>
      </HeaderActionsPortal>

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
            <div class="meeting-card" role="link" tabindex="0" @click="openMeeting(m)" @keydown.enter="openMeeting(m)">
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
          <div v-if="meetings.length > 0" ref="moreEl" class="more">
            <span v-if="loadingMore">加载中…</span>
            <span v-else-if="hasMore">上拉加载更多</span>
            <span v-else>没有更多了</span>
          </div>
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
import { useListSentinel } from '../../composables/use-list-sentinel'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import {
  Skeleton, EmptyState, DbLockedState, PullToRefresh, SwipeableListItem,
} from '@/components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import {
  listMeetings, createMeeting, deleteMeeting, archiveMeeting, unarchiveMeeting, updateMeeting,
  type LocalMeeting,
} from './meetings-store'
import { deleteMeetingAudio } from '../../native/meeting-audio'
import {
  formatDuration, formatLocationLine, formatMeetingWhen, formatParticipants, statusText,
  MEETING_LIST_FILTERS, type MeetingListFilter,
} from './meeting-list'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'
import { useListScene } from '../../composables/use-list-scene'

defineOptions({ name: 'MeetingListView' })

const router = useRouter()
const dbNotReady = ref(false)
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
const starting = ref(false)
const filter = ref<MeetingListFilter>('active')
const meetings = ref<LocalMeeting[]>([])
const filters = MEETING_LIST_FILTERS

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    const page = await listMeetings(DEFAULT_LIST_PAGE_SIZE, {
      archived: filter.value === 'archived',
      offset: 0,
    })
    meetings.value = page
    hasMore.value = pageHasMore(page.length)
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e)
    if (msg.includes('LocalDB 未初始化')) dbNotReady.value = true
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return
  loadingMore.value = true
  try {
    const page = await listMeetings(DEFAULT_LIST_PAGE_SIZE, {
      archived: filter.value === 'archived',
      offset: meetings.value.length,
    })
    const seen = new Set(meetings.value.map((m) => m.id))
    meetings.value = [...meetings.value, ...page.filter((m) => !seen.has(m.id))]
    hasMore.value = pageHasMore(page.length)
  } finally {
    loadingMore.value = false
  }
}

const { moreEl } = useListSentinel(loadMore)

async function onFilter(next: MeetingListFilter) {
  filter.value = next
  await load()
}

async function startNewMeeting() {
  if (starting.value) return
  starting.value = true
  try {
    const startedAt = Date.now()
    const m = await createMeeting({
      startedAt,
      title: formatCapturedTitle({ startedAt }),
    })
    void captureDeviceLocation().then(async (location) => {
      if (!location) return
      await updateMeeting(m.id, {
        location,
        title: formatCapturedTitle({ startedAt, location }),
      })
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
/* KeepAlive 现场保持：active/archived 筛选保留；详情页改动过会议才刷新 */
useListScene('meetings', load)
</script>

<style scoped>
.meetings-page { position: relative; height: 100%; min-height: 0; display: flex; flex-direction: column; }
.hdr-filter { font-size: 13px; color: var(--text-secondary); }
.hdr-filter.active { color: var(--brand-primary); font-weight: 700; }
.list-scroll { flex: 1 1 auto; min-height: 0; }
.meeting-list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.meeting-card { padding: var(--space-3); background: var(--bg-card); cursor: pointer; }
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
.more { padding: 16px 0 24px; text-align: center; font-size: 12px; color: var(--text-muted); }
</style>
