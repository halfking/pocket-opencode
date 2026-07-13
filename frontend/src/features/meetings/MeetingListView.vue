<!-- S2.2 会议列表：最近会议 + 新建录音入口。 -->
<template>
  <AppLayout>
    <div class="meetings-page">
      <header class="page-header">
        <div>
          <h1>会议记录</h1>
          <p>录音、转写和纪要都保存在本地</p>
        </div>
        <button class="primary" @click="router.push('/meetings/new')">开始会议</button>
      </header>

      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="meetings.length === 0" class="empty">
        <div class="empty-icon">🎙️</div>
        <p>还没有会议记录</p>
        <button class="primary" @click="router.push('/meetings/new')">开始第一场会议</button>
      </div>
      <ul v-else class="meeting-list">
        <li v-for="meeting in meetings" :key="meeting.id" @click="open(meeting.id)">
          <div class="meeting-icon">🎙️</div>
          <div class="meeting-main">
            <strong>{{ meeting.title || '未命名会议' }}</strong>
            <span>{{ formatDate(meeting.startedAt) }} · {{ duration(meeting.durationMs) }}</span>
            <small>{{ meeting.summary || meeting.transcript || '尚未生成纪要' }}</small>
          </div>
          <span class="arrow">›</span>
        </li>
      </ul>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { listMeetings, type LocalMeeting } from './meetings-store'

const router = useRouter()
const meetings = ref<LocalMeeting[]>([])
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    meetings.value = await listMeetings()
  } finally {
    loading.value = false
  }
}

function open(id: string) { router.push(`/meetings/${id}`) }
function formatDate(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
function duration(ms: number) {
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

onMounted(load)
</script>

<style scoped>
.meetings-page { padding: 16px; padding-bottom: 96px; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 18px; }
h1 { margin: 0; font-size: 24px; }
.page-header p { margin: 5px 0 0; color: var(--text-secondary); font-size: 12px; }
.primary { border: 0; border-radius: 9px; background: var(--brand-primary); color: white; padding: 9px 12px; font-size: 13px; cursor: pointer; }
.state, .empty { padding: 48px 12px; text-align: center; color: var(--text-secondary); }
.empty-icon { font-size: 44px; margin-bottom: 8px; }
.meeting-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
.meeting-list li { display: flex; align-items: center; gap: 11px; padding: 13px; background: var(--bg-card); border-radius: 12px; cursor: pointer; box-shadow: var(--shadow-sm); }
.meeting-icon { font-size: 25px; }
.meeting-main { min-width: 0; flex: 1; display: grid; gap: 3px; }
.meeting-main strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.meeting-main span, .meeting-main small { color: var(--text-secondary); font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.arrow { color: var(--text-muted); font-size: 24px; }
</style>
