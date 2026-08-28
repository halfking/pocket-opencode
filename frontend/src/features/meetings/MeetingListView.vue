<template>
  <div class="meetings-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="会议记录需要本地加密数据库"
      @relogin="goLogin"
    />

    <template v-else>
      <PullToRefresh :on-refresh="load" class="list-scroll">
        <div v-if="loading" class="state"><Skeleton :count="4" /></div>

        <EmptyState
          v-else-if="meetings.length === 0"
          icon="🎙️"
          title="还没有会议记录"
          hint="点击右下角按钮开始第一次会议录音"
          size="sm"
          variant="inline"
        />

        <div v-else class="meeting-list">
          <SwipeableListItem
            v-for="m in meetings"
            :key="m.id"
            :right-actions="[{ id: 'delete', icon: '🗑', label: '删除', type: 'danger', onAction: () => onDelete(m.id) }]"
          >
            <div class="meeting-card" @click="openMeeting(m)">
              <div class="card-header">
                <h3 class="card-title">{{ m.title || '未命名会议' }}</h3>
                <span class="status-badge" :class="m.status">{{ statusText(m.status) }}</span>
              </div>
              <p v-if="m.summary" class="card-summary">{{ m.summary.slice(0, 80) }}…</p>
              <div class="card-meta">
                <span>{{ formatDate(m.startedAt) }}</span>
                <span v-if="m.durationMs">{{ formatDuration(m.durationMs) }}</span>
                <span v-if="m.location">📍 {{ m.location }}</span>
              </div>
            </div>
          </SwipeableListItem>
        </div>
      </PullToRefresh>

      <button class="fab" aria-label="新会议" @click="startNewMeeting">
        🎙
      </button>
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
  listMeetings, createMeeting, deleteMeeting,
  type LocalMeeting, type MeetingStatus,
} from './meetings-store'
import { deleteMeetingAudio } from '../../native/meeting-audio'

const router = useRouter()
const dbNotReady = ref(false)
const loading = ref(true)
const meetings = ref<LocalMeeting[]>([])

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    meetings.value = await listMeetings()
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e)
    if (msg.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
    }
  } finally {
    loading.value = false
  }
}

async function startNewMeeting() {
  const m = await createMeeting({})
  router.push({ name: 'meeting-record', params: { id: m.id } })
}

function openMeeting(m: LocalMeeting) {
  if (m.status === 'recording') {
    router.push({ name: 'meeting-record', params: { id: m.id } })
  } else {
    router.push({ name: 'meeting-detail', params: { id: m.id } })
  }
}

async function onDelete(id: string) {
  await deleteMeeting(id)
  try { await deleteMeetingAudio(id) } catch { /* ok */ }
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

function goLogin() { router.push('/login') }

function statusText(s: MeetingStatus): string {
  const map: Record<MeetingStatus, string> = {
    recording: '录音中', completed: '已完成', processing: '处理中', refined: '已精翻',
  }
  return map[s] ?? s
}

function formatDate(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function formatDuration(ms: number): string {
  const m = Math.floor(ms / 60000)
  const s = Math.floor((ms % 60000) / 1000)
  return m > 0 ? `${m}分${s}秒` : `${s}秒`
}

onMounted(load)
</script>

<style scoped>
.meetings-page {
  position: relative;
  min-height: 100%;
}

.list-scroll {
  min-height: calc(100vh - var(--topbar-height) - var(--bottomnav-height) - 80px);
}

.meeting-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
}

.meeting-card {
  padding: var(--space-3);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.card-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}

.status-badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-muted);
  flex-shrink: 0;
}

.status-badge.recording {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}

.card-summary {
  margin: var(--space-2) 0 0;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.5;
}

.card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-top: var(--space-2);
  font-size: 12px;
  color: var(--text-muted);
}

.fab {
  position: fixed;
  right: var(--space-4);
  bottom: calc(var(--bottomnav-height, 56px) + var(--space-4));
  width: 56px;
  height: 56px;
  border-radius: 50%;
  border: none;
  background: var(--brand-gradient, linear-gradient(135deg, #667eea, #764ba2));
  color: #fff;
  font-size: 24px;
  box-shadow: var(--shadow-lg, 0 4px 20px rgba(0,0,0,0.2));
  cursor: pointer;
  z-index: var(--z-fab);
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
